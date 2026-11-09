#!/usr/bin/env python3
"""AOS environment host: supervise the GUI group and relay native ALAS control.

Tasks, stop semantics and logs belong to ALAS ProcessManager in the GUI child.
The relay returns 503 during GUI reload instead of inventing an idle task state.
"""
import atexit
import fcntl
import os
import signal
import stat
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HOST = '127.0.0.1'
PORT = 22400
BASE_DIR = os.path.dirname(os.path.abspath(__file__))
LOG_DIR = os.path.join(BASE_DIR, 'log')
LOCK_PATH = os.path.join(LOG_DIR, 'wrapper.lock')
_STOP_GRACE_SEC = 3.0
_gui = None
_gui_lock = threading.Lock()
_gui_started_at = None
_GUI_BACKOFF_INIT = 5.0
_GUI_BACKOFF_MAX = 60.0
_GUI_HEALTHY_UPTIME = 300.0
_closing = threading.Event()

def _gui_alive():
    return _gui is not None and _gui.poll() is None


def _start_gui_once():
    """拉起一次 gui.py（端口由 config/deploy.yaml 的 WebuiPort 定，默认 22267）。
    uvicorn 输出追加到 ./log/gui.out（gui.py 自身的 ALAS 日志另走 {date}_gui.txt）。"""
    global _gui, _gui_started_at
    os.makedirs(LOG_DIR, exist_ok=True)
    out = open(os.path.join(LOG_DIR, 'gui.out'), 'ab')
    _gui = subprocess.Popen(
        [sys.executable, os.path.join(BASE_DIR, 'alasaos_gui.py')],
        cwd=BASE_DIR,
        preexec_fn=os.setsid,  # 独立进程组，清理走 os.killpg
        stdin=subprocess.DEVNULL,
        stdout=out,
        stderr=subprocess.STDOUT,
    )
    out.close()
    _gui_started_at = time.time()


def _stop_gui():
    """SIGTERM → 3s → SIGKILL 杀 gui 进程组。幂等：没在跑直接返回。"""
    global _gui
    with _gui_lock:
        if _gui is None:
            return
        pgid = _gui.pid  # setsid: the group survives even if its leader died
        try:
            os.killpg(pgid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        deadline = time.time() + _STOP_GRACE_SEC
        while time.time() < deadline and _gui.poll() is None:
            time.sleep(0.05)
        if _gui.poll() is None:
            try:
                os.killpg(pgid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            _gui.wait(timeout=5)
        try:
            os.killpg(pgid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        _gui = None


def _gui_supervisor():
    """崩溃重拉循环：wait() 阻塞到 gui 死亡；活过 5 分钟视为健康、退避复位，
    否则退避翻倍（5s→60s 封顶）。_closing 置位（关停）后不重拉直接退出。"""
    backoff = _GUI_BACKOFF_INIT
    while not _closing.is_set():
        with _gui_lock:
            try:
                _start_gui_once()
                proc = _gui
                print(f'AlasAos wrapper: gui.py started pid={proc.pid}', flush=True)
            except OSError as e:
                print(f'AlasAos wrapper: gui spawn failed: {e}', file=sys.stderr, flush=True)
                proc = None
        if proc is None:
            if _closing.wait(backoff):
                break
            backoff = min(backoff * 2, _GUI_BACKOFF_MAX)
            continue
        proc.wait()
        _stop_gui()  # collect any orphan task/Manager processes before restarting
        if _closing.is_set():
            break
        uptime = time.time() - (_gui_started_at or time.time())
        backoff = _GUI_BACKOFF_INIT if uptime > _GUI_HEALTHY_UPTIME \
            else min(backoff * 2, _GUI_BACKOFF_MAX)
        print(f'AlasAos wrapper: gui.py exited code={proc.returncode} '
              f'uptime={uptime:.0f}s, respawn in {backoff:.0f}s', flush=True)
        if _closing.wait(backoff):
            break



def _cleanup():
    _closing.set()
    _stop_gui()


def _on_signal(signum, frame):
    _cleanup()
    # 按信号语义退出：128+signum
    sys.exit(128 + signum)


def _stdin_watchdog():
    """stdin 管道 EOF = 父进程已死 → 杀进程组自我了断（m0 孤儿教训）。"""
    try:
        sys.stdin.buffer.read()
    except (OSError, ValueError):
        pass
    _cleanup()
    os._exit(0)


def _arm_stdin_watchdog():
    # 只有 stdin 是管道（FIFO）时才挂监控：父进程死亡 → 管道 EOF → 自尽。
    # tty（手工调试）没有"父进程管道"语义；/dev/null（如 Java Redirect.DISCARD）
    # 读即 EOF，挂上会立即自尽——两者都不挂。
    try:
        if sys.stdin is not None and not sys.stdin.isatty() \
                and stat.S_ISFIFO(os.fstat(sys.stdin.fileno()).st_mode):
            threading.Thread(target=_stdin_watchdog, daemon=True).start()
    except (OSError, ValueError):
        pass


def _acquire_instance_lock():
    """单实例锁：返回锁文件对象（引用防 GC 关 fd），已有实例则 sys.exit(2)。"""
    os.makedirs(LOG_DIR, exist_ok=True)
    fd = open(LOCK_PATH, 'a', encoding='utf-8')
    try:
        fcntl.flock(fd.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        print(f'AlasAos wrapper: another instance holds {LOCK_PATH}', file=sys.stderr)
        sys.exit(2)
    fd.write(str(os.getpid()))
    fd.flush()
    return fd



class _Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def relay(self):
        if self.path.split('?', 1)[0] not in (
                '/status', '/configs', '/configs/delete', '/logs', '/start', '/stop', '/tool/start', '/tool/stop', '/theme'):
            self.send_error(404)
            return
        request = urllib.request.Request('http://127.0.0.1:22401' + self.path,
                                         data=b'' if self.command == 'POST' else None,
                                         method=self.command)
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                body, code = response.read(), response.status
                content_type = response.headers.get('Content-Type', 'application/json')
        except urllib.error.HTTPError as exc:
            body, code = exc.read(), exc.code
            content_type = 'application/json'
        except (OSError, urllib.error.URLError):
            body, code = b'{"error":"ALAS GUI control not ready"}', 503
            content_type = 'application/json'
        self.send_response(code)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    do_GET = do_POST = relay


def main():
    _lock_fd = _acquire_instance_lock()  # noqa: F841 - 引用防 GC
    atexit.register(_cleanup)
    signal.signal(signal.SIGTERM, _on_signal)
    signal.signal(signal.SIGINT, _on_signal)
    _arm_stdin_watchdog()
    if os.environ.get('ALASAOS_WEBUI', '1') != '0':
        threading.Thread(target=_gui_supervisor, daemon=True).start()
    server = ThreadingHTTPServer((HOST, PORT), _Handler)
    print(f'AlasAos wrapper: listening on http://{HOST}:{PORT}', flush=True)
    server.serve_forever()


if __name__ == '__main__':
    main()

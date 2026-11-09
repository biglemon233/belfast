#!/usr/bin/env python3
"""让 Android App 成为 WebUI 进程树的生命周期属主。

rootfs 的入口进程：由 app 侧 ProotHost 经 PRoot 拉起，chdir 到 /opt/azurpilot 后
以 venv Python 启动上游 gui.py（WebUI 与 WebSocket 网关，默认监听 127.0.0.1:25548）。
本进程退出或父进程消亡（stdin 管道 EOF）都会终结整棵子进程组，运行时因此不会
在 app 侧死亡后变成孤儿。

局域网控制改绑 0.0.0.0：绑定地址由 App 侧
设置开关经 ProotHost 写入 deploy.yaml。监听地址、端口与口令均由部署配置决定，
而 /android 薄接口的回环校验不随绑定变化，控制通道不会因此暴露到局域网。

Makes the Android app the lifecycle owner of the WebUI process tree.

This is the rootfs entry process: spawned by the app-side ProotHost through
PRoot, it chdirs to /opt/azurpilot and starts the upstream gui.py with the venv
Python (WebUI and WebSocket gateway listening on 127.0.0.1:25548 by default).
Its own exit or the parent's death (EOF on the stdin pipe) tears down the whole
child process group, so the runtime cannot outlive the app as an orphan.

LAN control rebinds to 0.0.0.0:
the bind address is written to deploy.yaml by the app-side toggle through ProotHost.
The deployment config determines the bind address, port and password, while the
/android thin API's loopback check is bind-independent,
so the control channel is never exposed to the LAN.
"""

import json
import os
import signal
import stat
import subprocess
import sys
import threading


def read_webui_config(root):
    """读取 Webui 段并保留 YAML 标量类型。

    Reads the Webui section while retaining YAML scalar types.
    """
    import yaml

    with open(os.path.join(root, 'config/deploy.yaml'), encoding='utf-8') as stream:
        return (yaml.safe_load(stream) or {}).get('Deploy', {}).get('Webui', {})


def read_password(root):
    """读取部署口令，与网关一样将旧配置中的数字等标量转换成字符串。

    Reads the deployment password, converting legacy scalar values to strings like the gateway.
    """
    password = read_webui_config(root).get('Password')
    return str(password) if password is not None else None


def connection_config(root):
    """读取部署配置对应的本机连接地址，不返回访问口令。

    Reads the local endpoint from deployment config without returning the access password.
    """
    webui = read_webui_config(root)
    host = webui.get('WebuiHost') or '127.0.0.1'
    if host not in ('127.0.0.1', 'localhost', '0.0.0.0', '::1', '::'):
        raise ValueError('Android WebUI host must allow loopback connections')
    port = int(webui.get('WebuiPort') if webui.get('WebuiPort') is not None else 25548)
    if not 1 <= port <= 65535:
        raise ValueError('WebUI port must be between 1 and 65535')
    return {'host': '[::1]' if ':' in host else '127.0.0.1', 'port': port,
            'tls': bool(webui.get('WebuiSSLKey') and webui.get('WebuiSSLCert'))}


def main():
    """拉起 WebUI 子进程并守望其整个生命周期，返回子进程退出码。

    Spawns the WebUI child, supervises it for its whole lifetime, and returns
    the child's exit code.
    """
    root = os.path.dirname(os.path.abspath(__file__))
    if sys.argv[1:] == ['--describe-connection']:
        print('ANDROID_CONNECTION=' + json.dumps(connection_config(root)))
        return 0
    # 上游按相对路径解析 config / frontend 等资源，必须先落到安装目录再启动。
    os.chdir(root)
    from android_update import ensure_reload, recover

    recover(root)
    ensure_reload(root)
    child = subprocess.Popen(
        [os.path.join(root, '.venv/bin/python'), 'gui.py'],
        cwd=root, stdin=subprocess.DEVNULL, start_new_session=True,
    )
    closing = threading.Event()

    def stop(*_):
        # 幂等：信号与 stdin EOF 可能同时到达，清理只执行一次。
        if closing.is_set():
            return
        closing.set()
        try:
            os.killpg(child.pid, signal.SIGTERM)
        except ProcessLookupError:
            # 进程组已不存在，无需再收尸。
            return
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(child.pid, signal.SIGKILL)
            child.wait()

    def watch_stdin():
        # 阻塞读管道直到 EOF；EOF 即 app 侧已死，是整个运行时的停机信号。
        try:
            # daemon 线程不能持有 stdin 的缓冲锁，否则子进程退出时 Python 会中止解释器。
            while os.read(sys.stdin.fileno(), 4096):
                pass
        finally:
            stop()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        # 只有 stdin 真是管道（app 侧 keepalive）才起守望线程；tty 或 /dev/null 无 EOF 契约可言。
        if stat.S_ISFIFO(os.fstat(sys.stdin.fileno()).st_mode):
            threading.Thread(target=watch_stdin, name='android-parent-watch', daemon=True).start()
    except (OSError, ValueError):
        # 个别环境拿不到 stdin 的 fileno/fstat，退化为仅由信号驱动停机。
        pass
    code = child.wait()
    stop()
    return code


if __name__ == '__main__':
    sys.exit(main())

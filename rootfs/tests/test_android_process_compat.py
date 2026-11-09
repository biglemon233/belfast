"""android_process_compat 的离线回归：合成 /proc 树钉住子进程枚举行为。

被测模块不在任何包里，这里按文件路径直接加载 overlays/android_process_compat.py，
使测试可在宿主机上独立运行（经 uv，无需 Android 环境）；用临时目录伪造 /proc，
覆盖 proc_children 的过滤语义与 install() 的 psutil 打桩路径。

Offline regression for android_process_compat: a synthetic /proc tree pins down
the child-enumeration behavior.

The module under test lives outside any package, so it is loaded directly by
file path from overlays/android_process_compat.py, letting the test run
standalone on the host via uv without an Android environment; a temporary
directory fakes /proc, covering proc_children's filtering semantics and
install()'s psutil patching path.
"""

import importlib.util
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import psutil


# 被测模块不在任何包里，按文件路径直接加载，测试即可独立执行。
OVERLAY = Path(__file__).resolve().parents[1] / "overlays" / "android_process_compat.py"
spec = importlib.util.spec_from_file_location("android_process_compat", OVERLAY)
compat = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compat)


def add_process(root: Path, pid: int, ppid: int, ticks: int, state: str = "S") -> None:
    """向伪造的 /proc 根写入一个仅含 stat 的进程目录。

    Writes a stat-only process directory into a fake /proc root.

    Args:
        root: 伪造的 /proc 根目录。/ The fake /proc root directory.
        pid: 进程 PID，同时用作目录名。/ The process PID, also used as the
            directory name.
        ppid: 父进程 PID。/ The parent process PID.
        ticks: 进程启动时刻（时钟滴答）。/ The process start time in clock
            ticks.
        state: 进程状态字符，默认 S。/ The process state character; defaults to
            "S".
    """
    proc = root / str(pid)
    proc.mkdir()
    fields = [state, str(ppid), *(["0"] * 17), str(ticks)]
    (proc / "stat").write_text(f"{pid} (worker name) {' '.join(fields)}\n", encoding="ascii")


class ProcChildrenTest(unittest.TestCase):
    """钉住 proc_children 的后代过滤语义与 install() 的打桩行为。

    Pins down proc_children's descendant-filtering semantics and install()'s
    patching behavior.
    """

    def test_same_owner_descendants_and_pid_reuse(self):
        work = Path(__file__).resolve().parents[2] / ".tmp"
        work.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(dir=work) as directory:
            root = Path(directory)
            add_process(root, 100, 1, 1000)
            add_process(root, 101, 100, 1001)
            add_process(root, 102, 101, 1002)
            add_process(root, 103, 100, 999)  # PID 重用，早于父进程
            add_process(root, 104, 100, 1003, state="Z")
            self.assertEqual(compat.proc_children(100, proc_root=root), [101])
            self.assertEqual(compat.proc_children(100, recursive=True, proc_root=root), [101, 102])

    def test_permission_failure_uses_proc_fallback(self):
        # install() 会永久改写 psutil.Process.children，必须 try/finally 还原，
        # 避免污染同一进程里的其他测试。
        original = psutil.Process.children
        try:
            with patch.object(psutil.Process, "children", side_effect=psutil.AccessDenied(pid=os.getpid())):
                compat.install()
                with patch.object(compat, "proc_children", return_value=[os.getpid()]) as fallback:
                    result = psutil.Process().children(recursive=True)
                    self.assertEqual([child.pid for child in result], [os.getpid()])
                    fallback.assert_called_once_with(os.getpid(), True)
        finally:
            psutil.Process.children = original


if __name__ == "__main__":
    unittest.main()

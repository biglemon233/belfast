"""在 Android PRoot 环境下为 psutil 的子进程枚举提供 /proc 后备实现。

Android 的 /proc 权限模型会让 psutil 原生 children() 抛 AccessDenied；本模块
安装打桩，在原生实现失败时改走手工扫描 /proc 的兼容路径，对调用方保持同一
psutil 异常语义。

Provides a /proc fallback for psutil's child enumeration under Android PRoot.

Android's /proc permission model makes psutil's native children() raise
AccessDenied; this module installs a patch that switches to a manual /proc scan
whenever the native implementation fails, preserving psutil's exception
semantics for callers.
"""

import os
from pathlib import Path

import psutil


def _read_stat(path: Path) -> tuple[str, int, int]:
    """解析 /proc/<pid>/stat，返回 (状态, ppid, 启动时刻)。

    Parses /proc/<pid>/stat into (state, ppid, start time).

    comm 字段可含空格与括号，必须定位最后一个 ')' 再切分；内核字段 22（进程
    启动时刻，单位为时钟滴答）在切分结果里位于下标 19。

    The comm field may contain spaces and parentheses, so splitting must start
    after the last ')'; kernel field 22 (process start time, in clock ticks)
    sits at index 19 of the split result.
    """
    raw = path.read_text(encoding="ascii")
    closing = raw.rfind(")")
    if closing < 0:
        raise ValueError("invalid /proc stat")
    fields = raw[closing + 1 :].split()
    if len(fields) <= 19:
        raise ValueError("short /proc stat")
    return fields[0], int(fields[1]), int(fields[19])


def proc_children(pid: int, recursive: bool = False, proc_root: Path = Path("/proc")) -> list[int]:
    """枚举指定进程的后代：仅同 UID、创建时刻不早于父进程者，避免 PID 复用误认。

    Enumerates the descendants of a process: only same-UID processes whose
    start time is not earlier than the parent's, guarding against PID-reuse
    false positives.

    Args:
        pid: 父进程 PID。/ PID of the parent process.
        recursive: True 时深挖整棵子树，否则只返回直接子进程。/ When True, walk
            the whole subtree; otherwise return direct children only.
        proc_root: /proc 挂载点，测试可注入临时目录。/ The /proc mount point;
            tests may inject a temporary directory.
    Returns:
        后代 PID 列表，顺序为遍历序。/ Descendant PIDs in traversal order.
    Raises:
        OSError: 父进程已消失或 stat 不可读。/ The parent has gone away or its
            stat is unreadable.
        ValueError: 父进程的 stat 格式异常。/ The parent's stat is malformed.
    """
    root = proc_root / str(pid)
    owner = root.stat().st_uid
    _, _, root_ticks = _read_stat(root / "stat")
    children_by_parent: dict[int, list[tuple[int, int]]] = {}
    for entry in proc_root.iterdir():
        if not entry.name.isdigit() or entry.name == str(pid):
            continue
        try:
            if entry.stat().st_uid != owner:
                continue
            state, ppid, ticks = _read_stat(entry / "stat")
        except (OSError, ValueError):
            # /proc 会随进程退出而变化；单个条目消失不影响其他后代。
            continue
        if state == "Z":
            continue
        children_by_parent.setdefault(ppid, []).append((int(entry.name), ticks))

    result: list[int] = []
    seen = {pid}
    pending = [(pid, root_ticks)]
    while pending:
        parent_pid, parent_ticks = pending.pop()
        for child_pid, child_ticks in children_by_parent.get(parent_pid, []):
            if child_pid in seen or child_ticks < parent_ticks:
                continue
            seen.add(child_pid)
            result.append(child_pid)
            if recursive:
                pending.append((child_pid, child_ticks))
    return result


def install() -> None:
    """给 psutil.Process.children 装上 Android /proc 兼容后备。

    Patches psutil.Process.children with the Android /proc fallback.

    只有原生实现抛 AccessDenied / PermissionError 时才走自实现枚举；后备枚举
    本身失败时，仍以 AccessDenied 抛出并附原始原因。

    The custom enumeration runs only when the native implementation raises
    AccessDenied / PermissionError; if the fallback itself fails, an
    AccessDenied carrying the original cause is raised.
    """
    original = psutil.Process.children

    def children(self, recursive=False):
        try:
            return original(self, recursive=recursive)
        except (psutil.AccessDenied, PermissionError):
            try:
                return [psutil.Process(pid) for pid in proc_children(self.pid, recursive)]
            except (OSError, ValueError) as exc:
                raise psutil.AccessDenied(self.pid, msg=f"Android /proc children unavailable: {exc}") from exc

    psutil.Process.children = children

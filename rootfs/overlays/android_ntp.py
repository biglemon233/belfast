"""让 Android 时间读取使用后台 NTP，避免 DNS 和 UDP 查询阻塞截图及调度。

Uses background NTP in Android so DNS and UDP queries cannot block screenshots or scheduling.
"""

import importlib.abc
import os
import sys
import threading
import time as time_
from typing import Any, Dict

TARGET_MODULE = 'module.config.time_source'


def initialize(source):
    """保留已有缓存，为异步刷新增加进程内状态。

    Preserves cached calibration and adds process-local asynchronous refresh state.
    """
    source._refreshing = False
    source._refresh_thread = None
    source._ntp_pid = os.getpid()


class BackgroundTime:
    """仅替换缓存读写与刷新时机，复用上游查询、配置和日志。

    Adapts refresh timing and cache access, retaining upstream queries, settings, and logging.
    """

    def _ensure_process(self):
        """fork 后父线程不在子进程中存活，必须重置锁及查询占用标记。

        Resets locks and in-flight state after fork because parent threads do not survive it.
        """
        if self._ntp_pid != os.getpid():
            self._lock = threading.RLock()
            initialize(self)

    def refresh(self, force: bool = False) -> bool:
        """请求后台校时并立即返回缓存状态。

        Starts a background refresh and returns cached sync status without waiting for DNS or UDP.

        Args:
            force: 忽略刷新及退避间隔；已有查询运行时仍合并请求。

        Returns:
            bool: 是否已有成功校时缓存，不代表本次后台请求的结果。
        """
        self._ensure_process()
        if not self.enabled:
            return False
        with self._lock:
            current = time_.monotonic()
            if self._refreshing:
                return self.synced
            if not force and self.synced and current - self.last_sync_monotonic < self.refresh_interval:
                return True
            if not force and current < self.retry_after_monotonic:
                return self.synced
            self._refreshing = True
            worker = threading.Thread(target=self._refresh_in_background,
                                      name='AzurPilot-NTP', daemon=True)
            self._refresh_thread = worker
            try:
                worker.start()
            except Exception:
                self._refreshing = False
                raise
            return self.synced

    def _refresh_in_background(self) -> None:
        """网络查询在锁外执行，失败保留时间缓存。

        Queries outside the lock and publishes briefly under lock, preserving cache on failure."""
        try:
            errors = []
            for server in self.servers:
                try:
                    offset = self._query_server(server)
                except Exception as e:
                    # OSError 以外的异常（协议解析失败、上游签名变化等）同样按
                    # 服务器故障处理：继续尝试后续服务器，全部失败时统一设置
                    # 重试间隔，避免 worker 静默退出导致无退避的反复重试。
                    errors.append(f'{server}: {e}')
                    continue

                with self._lock:
                    self.offset = offset
                    self.base_timestamp = time_.time() + offset
                    self.base_monotonic = time_.monotonic()
                    self.server = server
                    self.synced = True
                    self.last_sync_monotonic = self.base_monotonic
                    self.retry_after_monotonic = 0.0
                    self._warned = False
                self._log_info(f'网络时间已校准: {server}, offset={offset:.3f}s')
                return

            with self._lock:
                self.retry_after_monotonic = time_.monotonic() + self.retry_interval
                warn = not self._warned
                self._warned = True
                fallback = '上次校准结果' if self.synced else '本机时间'
            if warn:
                detail = '; '.join(errors) if errors else '没有可用服务器'
                self._log_warning(f'NTP 校时失败，继续使用{fallback}（未阻塞调度）: {detail}')
        finally:
            with self._lock:
                self._refreshing = False

    def timestamp(self) -> float:
        """立即读取快照，首次校时未完成时使用手机时间。

        Returns a snapshot immediately, using device time before the first successful sync."""
        self.refresh()
        with self._lock:
            if self.synced:
                return self.base_timestamp + (time_.monotonic() - self.base_monotonic)
            return time_.time() + self.offset

    def status(self) -> Dict[str, Any]:
        """获取当前网络校时状态。

        Returns the current cached network-time status.

        Returns:
            Dict[str, Any]: 包含同步状态、服务器、偏差与刷新间隔的字典。
        """
        self.refresh()
        with self._lock:
            return {
                'enabled': self.enabled,
                'synced': self.synced,
                'server': self.server or '-',
                'offset': self.offset,
                'refresh_interval': self.refresh_interval,
                'last_sync_elapsed': (
                    time_.monotonic() - self.last_sync_monotonic
                    if self.synced else None
                ),
            }


def patch_module(module):
    """为原类及单例接线，已提供后台刷新的新版核心保持原实现。

    Connects the original class and singleton, leaving already-asynchronous cores unchanged.
    """
    cls = module.NetworkTimeSource
    if getattr(cls, '_android_ntp_adapter', False):
        module._android_ntp_adapter = True
        return
    if hasattr(cls, '_refresh_in_background'):
        module._android_ntp_adapter = True
        return
    original_init = cls.__init__

    def init(self):
        original_init(self)
        initialize(self)

    cls.__init__ = init
    for name in ('_ensure_process', 'refresh', '_refresh_in_background', 'timestamp', 'status'):
        setattr(cls, name, getattr(BackgroundTime, name))
    initialize(module.network_time)
    cls._android_ntp_adapter = True
    module._android_ntp_adapter = True


class _Loader(importlib.abc.Loader):
    """模块加载后接入，不写上游源码。 / Connects after loading without changing upstream files."""

    def __init__(self, original):
        self.original = original

    def create_module(self, spec):
        return self.original.create_module(spec)

    def exec_module(self, module):
        self.original.exec_module(module)
        patch_module(module)


class _Finder(importlib.abc.MetaPathFinder):
    """委托后续加载器，兼容其它 overlay。 / Delegates to later finders to compose with overlays."""

    _android_ntp_adapter = True

    def find_spec(self, fullname, path, target=None):
        if fullname != TARGET_MODULE:
            return None
        for finder in sys.meta_path[sys.meta_path.index(self) + 1:]:
            spec = finder.find_spec(fullname, path, target)
            if spec is not None:
                if spec.loader:
                    spec.loader = _Loader(spec.loader)
                return spec
        return None


def install():
    """仅注册一次导入钩子；热更后的新进程重新接入当前核心。

    Registers one import hook, rebinding the current core in new processes after hot updates.
    """
    if TARGET_MODULE in sys.modules:
        patch_module(sys.modules[TARGET_MODULE])
    if not any(getattr(finder, '_android_ntp_adapter', False) for finder in sys.meta_path):
        sys.meta_path.insert(0, _Finder())

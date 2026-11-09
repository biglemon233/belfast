"""验证共享内存截图的快照归属、错误回退、重连及导入钩子组合。

Verifies shared-memory snapshot ownership, fallback, reconnection, and import-hook composition.
"""

import importlib.util
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import numpy as np


OVERLAYS = Path(__file__).resolve().parents[1] / 'overlays'
spec = importlib.util.spec_from_file_location('android_capture', OVERLAYS / 'android_capture.py')
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class CaptureTest(unittest.TestCase):
    """验证图像从 BGR 映射复制为自有 RGB，错误不污染后续帧。

    Verifies owned RGB copies from BGR mappings and isolation of failed reads from subsequent frames.
    """

    def setUp(self):
        for owner, name, value in (
                (adapter.socket, 'SCM_RIGHTS', 1), (adapter.socket, 'MSG_CTRUNC', 8),
                (adapter.socket, 'CMSG_SPACE', lambda size: size + 32),
                (adapter.mmap, 'MAP_SHARED', 1), (adapter.mmap, 'PROT_READ', 1)):
            constant = patch.object(owner, name, value, create=True)
            constant.start()
            self.addCleanup(constant.stop)

    def client(self, metadata=None):
        """提供可被服务覆盖的模拟映射。 / Supplies a simulated server-writable mapping."""
        client = adapter.SharedFrameClient()
        client.sock = Mock()
        client.mapping = bytearray([10, 20, 30, 40, 50, 60])
        client.capacity = 6
        client._response = Mock(return_value=metadata or {
            'ok': True, 'width': 2, 'height': 1, 'channels': 3, 'length': 6, 'frames': 7})
        return client

    def test_rgb_snapshot_survives_server_overwrite(self):
        client = self.client()
        image = client.capture()
        self.assertEqual(image.tolist(), [[[30, 20, 10], [60, 50, 40]]])
        self.assertTrue(image.flags.c_contiguous)
        self.assertTrue(image.flags.owndata)
        client.mapping[:] = b'\xff' * 6
        self.assertEqual(image[0, 0].tolist(), [30, 20, 10])
        self.assertEqual(client.last_metadata['frames'], 7)

    def test_invalid_geometry_closes_and_retries_only_once(self):
        client = self.client({'ok': True, 'width': 100, 'height': 1, 'channels': 3, 'length': 6})
        client.close = Mock()
        with self.assertRaisesRegex(adapter.SharedFrameError, 'geometry'):
            client.capture()
        self.assertEqual(client.close.call_count, 2)

    def test_disconnect_reopens_and_returns_new_frame(self):
        client = self.client()
        metadata = client._response.return_value
        client._response.side_effect = [adapter.SharedFrameError('closed'), metadata]
        client.close = Mock(side_effect=lambda: setattr(client, 'sock', None))
        client._connect = Mock(side_effect=lambda: setattr(client, 'sock', Mock()))
        self.assertEqual(client.capture().shape, (1, 2, 3))
        client._connect.assert_called_once()

    def test_fork_replaces_inherited_connection_and_lock(self):
        client = self.client()
        old_lock = client.lock
        client.pid = -1
        client.close = Mock(side_effect=lambda: setattr(client, 'sock', None))
        client._connect = Mock(side_effect=lambda: setattr(client, 'sock', Mock()))
        client.capture()
        self.assertIsNot(client.lock, old_lock)
        client.close.assert_called_once()

    def test_large_header_read_requires_few_syscalls(self):
        client = self.client()
        del client._response
        client.sock.recv.side_effect = [b'{"ok":', b'true}\n']
        self.assertEqual(client._response(), {'ok': True})
        self.assertEqual(client.sock.recv.call_count, 2)

    def test_payload_after_json_is_rejected(self):
        client = self.client()
        del client._response
        client.sock.recv.return_value = b'{"ok":true}\nx'
        with self.assertRaisesRegex(adapter.SharedFrameError, 'unexpected'):
            client._response()

    def test_handshake_failure_closes_all_received_fds(self):
        client = self.client()
        del client._response
        fds = adapter.array.array('i', [21, 22]).tobytes()
        client.sock.recvmsg.return_value = (
            b'{"ok":true,"version":1,"capacity":6}\n',
            [(adapter.socket.SOL_SOCKET, adapter.socket.SCM_RIGHTS, fds)], 0, None)
        with patch.object(adapter.os, 'close') as close:
            with self.assertRaisesRegex(adapter.SharedFrameError, 'handshake'):
                client._response(receive_fd=True)
        self.assertEqual([call.args[0] for call in close.call_args_list], [21, 22])

    def test_handshake_maps_read_only_and_closes_descriptor(self):
        client = self.client()
        del client._response
        client.sock.recvmsg.return_value = (
            b'{"ok":true,"version":1,"capacity":6}\n',
            [(adapter.socket.SOL_SOCKET, adapter.socket.SCM_RIGHTS,
              adapter.array.array('i', [21]).tobytes())], 0, None)
        with patch.object(adapter.os, 'close') as close, patch.object(adapter.mmap, 'mmap') as mapped:
            client._response(receive_fd=True)
        mapped.assert_called_once_with(21, 6, flags=adapter.mmap.MAP_SHARED,
                                       prot=adapter.mmap.PROT_READ)
        close.assert_called_once_with(21)


class AdapterTest(unittest.TestCase):
    """验证旧桥和远程地址回退，且截图不取得上游输入锁。

    Verifies legacy and remote fallback while capture avoids the upstream input lock.
    """

    def setUp(self):
        constant = patch.object(adapter.socket, 'SCM_RIGHTS', 1, create=True)
        constant.start()
        self.addCleanup(constant.stop)

    def fixture(self, address='127.0.0.1:22301'):
        """提供上游类最小契约。 / Supplies the minimal upstream class contract."""
        original = Mock(return_value='tcp image')
        backend = type('Backend', (), {'screenshot_azurpilot_android': original})
        module = SimpleNamespace(AzurPilotAndroid=backend, logger=Mock())
        adapter.patch_ap_module(module)
        device = backend()
        device.azurpilot_android_addr = address
        return module, device, original

    def test_local_prefers_memory_without_touch_lock(self):
        _, device, original = self.fixture()
        with patch.object(adapter, 'SharedFrameClient') as client:
            client.return_value.capture.return_value = 'memory image'
            self.assertEqual(device.screenshot_azurpilot_android(), 'memory image')
        self.assertEqual(device.android_capture_transport, 'shared_memory')
        original.assert_not_called()

    def test_remote_keeps_tcp(self):
        _, device, original = self.fixture('192.0.2.1:22301')
        with patch.object(adapter, 'SharedFrameClient') as client:
            self.assertEqual(device.screenshot_azurpilot_android(), 'tcp image')
            client.assert_not_called()
        original.assert_called_once()

    def test_failed_channel_falls_back_and_retries_after_backoff(self):
        module, device, original = self.fixture()
        with patch.object(adapter, 'SharedFrameClient') as client, \
                patch.object(adapter.time, 'monotonic', return_value=100):
            client.return_value.capture.side_effect = OSError('old APK')
            self.assertEqual(device.screenshot_azurpilot_android(), 'tcp image')
            self.assertEqual(device.screenshot_azurpilot_android(), 'tcp image')
            self.assertEqual(client.return_value.capture.call_count, 1)
        self.assertEqual(original.call_count, 2)
        module.logger.warning.assert_called_once()
        with patch.object(adapter.time, 'monotonic', return_value=131):
            device._android_frame_client.capture.side_effect = None
            device._android_frame_client.capture.return_value = 'memory image'
            self.assertEqual(device.screenshot_azurpilot_android(), 'memory image')

    def test_explicit_tcp_does_not_probe_memory(self):
        _, device, _ = self.fixture()
        with patch.dict(adapter.os.environ, {'AZURPILOT_ANDROID_CAPTURE': 'tcp'}), \
                patch.object(adapter, 'SharedFrameClient') as client:
            device.screenshot_azurpilot_android()
            client.assert_not_called()

    def test_import_finder_composes_with_device_adapter(self):
        finder = adapter._Finder()
        following = Mock()
        original = Mock()
        spec = SimpleNamespace(loader=original)
        following.find_spec.return_value = spec
        with patch.object(sys, 'meta_path', [finder, following]):
            result = finder.find_spec(adapter.TARGET_MODULE, None)
        self.assertIs(result.loader.original, original)
        following.find_spec.assert_called_once_with(adapter.TARGET_MODULE, None, None)


if __name__ == '__main__':
    unittest.main()

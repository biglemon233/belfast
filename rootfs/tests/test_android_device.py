"""验证虚拟屏前台检测的 ROM 格式兼容、屏幕隔离和延迟安装。

Verifies ROM format compatibility, display isolation, and lazy app-detection installation.
"""

import importlib.util
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch


OVERLAYS = Path(__file__).resolve().parents[1] / 'overlays'
spec = importlib.util.spec_from_file_location('android_device', OVERLAYS / 'android_device.py')
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)

WINDOWS = '''
  Display: mDisplayId=0 stacks=7
    mCurrentFocus=Window{host u0 com.azurpilot.ghio/.MainActivity}
    displayId=0
  Display: mDisplayId=7 stacks=1
    mCurrentFocus=Window{game u0 com.bilibili.azurlane/com.manjuu.azurlane.MainActivity}
    displayId=7
'''
ACTIVITIES = '''
Display #0 (activities from top to bottom):
  mResumedActivity: ActivityRecord{host u0 com.azurpilot.ghio/.MainActivity t10}
Display #7 (activities from top to bottom):
  mResumedActivity: ActivityRecord{game u0 com.bilibili.azurlane/com.manjuu.azurlane.MainActivity t11}
'''
GAME = 'com.bilibili.azurlane'


class DisplayParserTest(unittest.TestCase):
    """钉住主屏和虚拟屏同时存在时的解析边界。

    Pins parsing boundaries when primary and virtual displays coexist.
    """

    def test_miui_colon_headers_do_not_return_primary_app(self):
        self.assertEqual(adapter.focused_package(WINDOWS, 7), GAME)

    def test_supported_header_formats(self):
        for title in ('Display: mDisplayId=7', 'Display #7:', 'Display 7:',
                      'Display id=unused displayId=7'):
            with self.subTest(title=title):
                output = WINDOWS.replace('Display: mDisplayId=7 stacks=1', title)
                self.assertEqual(adapter.focused_package(output, 7), GAME)

    def test_display_id_is_exact_and_not_a_substring(self):
        output = WINDOWS.replace('mDisplayId=7', 'mDisplayId=70')
        self.assertEqual(adapter.focused_package(output, 7), '')
        self.assertEqual(adapter.focused_package(output, 70), GAME)

    def test_missing_display_never_falls_back_to_primary_focus(self):
        self.assertEqual(adapter.focused_package(WINDOWS, 8), '')
        self.assertEqual(adapter.focused_package('mCurrentFocus=Window{x u0 com.host/.Main}', 7), '')

    def test_unknown_header_still_ends_target_section(self):
        output = '''Display: mDisplayId=7
  mCurrentFocus=null
Display UNKNOWN
  mCurrentFocus=Window{host u0 com.azurpilot.ghio/.MainActivity}
'''
        self.assertEqual(adapter.focused_package(output, 7), '')

    def test_target_without_global_focus_uses_its_own_window_or_app(self):
        for field, record in (('mFocusedWindow', 'Window'), ('mFocusedApp', 'ActivityRecord')):
            with self.subTest(field=field):
                output = WINDOWS.replace('mCurrentFocus=Window{game',
                                         f'mCurrentFocus=null\n    {field}={record}' + '{game')
                self.assertEqual(adapter.focused_package(output, 7), GAME)

    def test_focus_precedes_focused_app_in_same_display(self):
        output = WINDOWS.replace('displayId=7', 'mFocusedApp=ActivityRecord{x u0 com.other/.Main}')
        self.assertEqual(adapter.focused_package(output, 7), GAME)

    def test_resumed_activity_is_scoped_to_target_display(self):
        self.assertEqual(adapter.focused_package(ACTIVITIES, 7, activity=True), GAME)
        self.assertEqual(adapter.focused_package(ACTIVITIES, 8, activity=True), '')


class DeviceAdapterTest(unittest.TestCase):
    """验证前台查询的后备路径、超时预算及模块接线，不启动或点击游戏。

    Verifies query fallback, timeout budgets, and integration without launching or clicking games.
    """

    def fixture(self, output=WINDOWS):
        """提供上游类的最小契约。 / Supplies the minimal upstream class contract."""
        class Backend:
            app_current_azurpilot_android = Mock()
            app_start_azurpilot_android = Mock()
            screenshot_azurpilot_android = Mock()

        module = SimpleNamespace(AzurPilotAndroid=Backend, logger=Mock())
        adapter.patch_ap_module(module)
        device = Backend()
        device.azurpilot_android_display_id = 7
        device.azurpilot_android_shell_output = Mock(return_value=output)
        return module, device

    def test_patched_backend_reads_virtual_display(self):
        module, device = self.fixture()
        self.assertEqual(device.app_current_azurpilot_android(), GAME)
        self.assertEqual(device.azurpilot_android_shell_output.call_count, 1)
        module.logger.attr.assert_called_once_with('App current (azurpilot_android)', GAME)
        device.app_start_azurpilot_android.assert_not_called()
        device.screenshot_azurpilot_android.assert_not_called()

    def test_no_window_focus_uses_target_resumed_activity(self):
        _, device = self.fixture()
        device.azurpilot_android_shell_output.side_effect = ['', ACTIVITIES]
        self.assertEqual(device.app_current_azurpilot_android(), GAME)
        self.assertEqual([call.args[0] for call in device.azurpilot_android_shell_output.call_args_list],
                         ['dumpsys window displays', 'dumpsys activity activities'])

    def test_fallback_shares_total_timeout_budget(self):
        _, device = self.fixture()
        device.azurpilot_android_shell_output.side_effect = ['', ACTIVITIES]
        with patch.object(adapter.time, 'monotonic', side_effect=[100, 101, 104]):
            self.assertEqual(device.app_current_azurpilot_android(timeout=10), GAME)
        self.assertEqual([call.kwargs['timeout'] for call in
                          device.azurpilot_android_shell_output.call_args_list], [9, 6])

    def test_exhausted_budget_does_not_issue_second_query(self):
        _, device = self.fixture('')
        with patch.object(adapter.time, 'monotonic', side_effect=[100, 101, 111]):
            self.assertEqual(device.app_current_azurpilot_android(timeout=10), '')
        self.assertEqual(device.azurpilot_android_shell_output.call_count, 1)

    def test_display_recreation_is_not_cached(self):
        _, device = self.fixture()
        self.assertEqual(device.app_current_azurpilot_android(), GAME)
        device.azurpilot_android_display_id = 8
        self.assertEqual(device.app_current_azurpilot_android(), '')

    def test_bridge_failure_propagates(self):
        _, device = self.fixture()
        device.azurpilot_android_shell_output.side_effect = RuntimeError('bridge unavailable')
        with self.assertRaisesRegex(RuntimeError, 'bridge unavailable'):
            device.app_current_azurpilot_android()

    def test_loaded_module_and_finder_install_idempotently(self):
        module, device = self.fixture()
        current = type(device).app_current_azurpilot_android
        with patch.dict(sys.modules, {adapter.TARGET_MODULE: module}), \
                patch.object(sys, 'meta_path', list(sys.meta_path)):
            adapter.install()
            adapter.install()
            self.assertIs(type(device).app_current_azurpilot_android, current)
            self.assertEqual(sum(getattr(finder, '_android_display_adapter', False)
                                 for finder in sys.meta_path), 1)

    def test_lazy_loader_patches_only_android_backend_after_execution(self):
        finder = adapter._Finder()
        loader = Mock()
        loader.create_module.return_value = None
        with patch.object(adapter.importlib.machinery.PathFinder, 'find_spec') as find:
            self.assertIsNone(finder.find_spec('module.device.device', None))
            find.assert_not_called()
            spec = SimpleNamespace(loader=loader)
            find.return_value = spec
            found = finder.find_spec(adapter.TARGET_MODULE, ['runtime'])
            self.assertIsNone(found.loader.create_module(found))
            module, _ = self.fixture()
            with patch.object(adapter, 'patch_ap_module') as connect:
                found.loader.exec_module(module)
                loader.exec_module.assert_called_once_with(module)
                connect.assert_called_once_with(module)


if __name__ == '__main__':
    unittest.main()

"""Game task placement parsing regressions, runnable without an ALAS installation."""
import ast
from pathlib import Path
import re
import unittest


SOURCE = Path(__file__).resolve().parents[1] / 'patches/module/device/method/alasaos.py'
TREE = ast.parse(SOURCE.read_text(encoding='utf-8'))
FUNCTION = next(node for node in TREE.body if isinstance(node, ast.FunctionDef)
                and node.name == '_game_task_placements')
NAMESPACE = {'re': re}
exec(compile(ast.Module(body=[FUNCTION], type_ignores=[]), str(SOURCE), 'exec'), NAMESPACE)
placements = NAMESPACE['_game_task_placements']

GAME = 'com.bilibili.blhx.qihoo'
MAIN = 'com.manjuu.azurlane.MainActivity'
HOST = 'io.github.shinarin.alasaos'
DUMP = f'''
  Window #0 Window{{6fb71dc u0 StatusBar}}:
    mDisplayId=0 stackId=0 mSession=Session{{a 1272:u0a10049}} mClient=android.os.BinderProxy@1
    isVisible=false
  Window #1 Window{{547573a u0 {HOST}}}
    mDisplayId=0 stackId=0 mSession=Session{{b 6541:u0a10115}} mClient=android.os.BinderProxy@2
    isVisible=true
  Window #5 Window{{6f3e630 u0 {GAME}/{MAIN}}}:
    mDisplayId=0 stackId=22 mSession=Session{{c 9870:u0a10113}} mClient=android.os.BinderProxy@3
    isVisible=true
  Window #6 Window{{4ed688 u0 {GAME}/{MAIN}}}:
    mDisplayId=0 stackId=22 mSession=Session{{d 9870:u0a10113}} mClient=android.os.BinderProxy@4
    isVisible=true
'''


class GameTaskPlacementTests(unittest.TestCase):
    def test_task_on_default_display(self):
        self.assertEqual(placements(DUMP, GAME), [(22, 0)])

    def test_windows_of_same_task_collapse(self):
        self.assertEqual(len(placements(DUMP, GAME)), 1)

    def test_task_on_virtual_display(self):
        on_vd = DUMP.replace('mDisplayId=0 stackId=22', 'mDisplayId=3 stackId=22')
        self.assertEqual(placements(on_vd, GAME), [(22, 3)])

    def test_modern_android_task_id_is_a_movable_root_task(self):
        modern = DUMP.replace('stackId=', 'taskId=')
        self.assertEqual(placements(modern, GAME), [(22, 0)])
        self.assertEqual(placements(modern.replace('mDisplayId=0 taskId=22',
                                                  'mDisplayId=7 taskId=22'), GAME), [(22, 7)])

    def test_other_packages_are_ignored(self):
        self.assertEqual(placements(DUMP, HOST), [])
        self.assertEqual(placements(DUMP, 'com.android.settings'), [])

    def test_launcher_referencing_game_window_is_not_game(self):
        dump = f"""
  Window #0 Window{{launcher u0 com.android.launcher3/.Launcher}}:
    mDisplayId=0 stackId=41
    mObscuringWindow=Window{{game u0 {GAME}/{MAIN}}}
  Window #1 Window{{game u0 {GAME}/{MAIN}}}:
    mDisplayId=20 stackId=44
"""
        self.assertEqual(placements(dump, GAME), [(44, 20)])

    def test_global_footer_reference_does_not_reclassify_last_window(self):
        dump = f"""
  Window #0 Window{{launcher u0 com.android.launcher3/.Launcher}}:
    mDisplayId=0 stackId=41
  mLastWakeLockHoldingWindow=Window{{game u0 {GAME}/{MAIN}}}
"""
        self.assertEqual(placements(dump, GAME), [])

    def test_preamble_reference_is_not_a_window(self):
        dump = f"""mCurrentFocus=Window{{game u0 {GAME}/{MAIN}}}
    mDisplayId=0 stackId=41
"""
        self.assertEqual(placements(dump, GAME), [])

    def test_missing_package(self):
        self.assertEqual(placements(DUMP, 'com.not.installed'), [])

    def test_empty_dump(self):
        self.assertEqual(placements('', GAME), [])

    def test_multiple_tasks_reported_separately(self):
        two = DUMP.replace(
            f'Window #6 Window{{4ed688 u0 {GAME}/{MAIN}}}:\n'
            '    mDisplayId=0 stackId=22',
            f'Window #6 Window{{4ed688 u0 {GAME}/com.qihoo.gamecenter.sdk.activity.ContainerActivity}}:\n'
            '    mDisplayId=0 stackId=23')
        self.assertEqual(sorted(placements(two, GAME)), [(22, 0), (23, 0)])

    def test_package_substring_does_not_leak(self):
        other = f'  Window #7 Window{{x u0 {GAME}.helper/.MainActivity}}:\n    mDisplayId=1 stackId=9\n'
        self.assertEqual(placements(other, GAME), [])

    def test_window_without_stack_line(self):
        broken = f'  Window #8 Window{{x u0 {GAME}/{MAIN}}}:\n    isVisible=true\n'
        self.assertEqual(placements(broken, GAME), [])


if __name__ == '__main__':
    unittest.main()

"""Display parsing regressions, runnable without an ALAS installation."""
import ast
from pathlib import Path
import re
import unittest


SOURCE = Path(__file__).resolve().parents[1] / 'patches/module/device/method/alasaos.py'
TREE = ast.parse(SOURCE.read_text(encoding='utf-8'))
FUNCTION = next(node for node in TREE.body if isinstance(node, ast.FunctionDef)
                and node.name == '_display_foreground_package')
NAMESPACE = {'re': re}
exec(compile(ast.Module(body=[FUNCTION], type_ignores=[]), str(SOURCE), 'exec'), NAMESPACE)
foreground = NAMESPACE['_display_foreground_package']

GAME = 'com.bilibili.azurlane'
HOST = 'io.github.shinarin.alasaos'
CLOUD = f'''
  Display: mDisplayId=7
  mCurrentFocus=null
  mFocusedApp=AppWindowToken{{a token=Token{{b ActivityRecord{{c u0 {GAME}/com.manjuu.azurlane.MainActivity t1}}}}}}
  displayId=7
  Display: mDisplayId=0
  mCurrentFocus=Window{{d u0 {HOST}/com.aliothmoon.maafw.MainActivity}}
  displayId=0
'''


class ForegroundTests(unittest.TestCase):
    def test_cloud_unfocused_virtual_display(self):
        self.assertEqual(foreground(CLOUD, 7), GAME)

    def test_physical_display_remains_separate(self):
        self.assertEqual(foreground(CLOUD, 0), HOST)

    def test_missing_display(self):
        self.assertEqual(foreground(CLOUD, 17), '')

    def test_empty_virtual_display_does_not_borrow_physical_focus(self):
        empty = CLOUD.replace(CLOUD.splitlines()[3], '  mFocusedApp=null')
        self.assertEqual(foreground(empty, 7), '')

    def test_display_id_is_not_a_substring_match(self):
        modern = CLOUD.replace('Display: mDisplayId=7', 'Display: mDisplayId=17')
        modern = modern.replace('displayId=7', 'displayId=17')
        self.assertEqual(foreground(modern, 17), GAME)
        self.assertEqual(foreground(modern, 7), '')

    def test_space_header_and_focus_priority(self):
        output = f'''Display 7 info:
 displayId=7
 mCurrentFocus=Window{{a u0 com.example.dialog/.Dialog$Inner}}
 mFocusedApp=ActivityRecord{{b u0 {GAME}/.MainActivity}}
Display 0 info:
 mCurrentFocus=Window{{c u0 {HOST}/.MainActivity}}
'''
        self.assertEqual(foreground(output, 7), 'com.example.dialog')

    def test_display_id_header(self):
        output = CLOUD.replace('Display: mDisplayId=7', 'Display displayId=7')
        self.assertEqual(foreground(output, 7), GAME)

    def test_asset_matches_source(self):
        asset = SOURCE.parents[5] / 'app/app/src/main/assets/alas/patches/module/device/method/alasaos.py'
        self.assertEqual(SOURCE.read_bytes(), asset.read_bytes())


if __name__ == '__main__':
    unittest.main()

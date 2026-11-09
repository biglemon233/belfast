"""Bridge-mode package auto-detection parsing, runnable without an ALAS installation.

Guards the fix for the "auto -> hardcoded com.bilibili.azurlane" bug: in bridge mode
ALAS's native detect_package (real adb) can't run, so AlasAos enumerates installed
packages via the bridge shell and filters against ALAS's known package sets.
`_known_azurlane_packages` is the pure parse/filter step.
"""
import ast
from pathlib import Path
import re
import unittest


SOURCE = Path(__file__).resolve().parents[1] / 'patches/module/device/method/alasaos.py'
TREE = ast.parse(SOURCE.read_text(encoding='utf-8'))
FUNCTION = next(node for node in TREE.body if isinstance(node, ast.FunctionDef)
                and node.name == '_known_azurlane_packages')
NAMESPACE = {'re': re}
exec(compile(ast.Module(body=[FUNCTION], type_ignores=[]), str(SOURCE), 'exec'), NAMESPACE)
known = NAMESPACE['_known_azurlane_packages']

# Representative subset of ALAS's VALID_PACKAGE | VALID_CHANNEL_PACKAGE
VALID = {
    'com.bilibili.azurlane',        # CN B-server (default)
    'com.bilibili.blhx.qihoo',      # CN 360 channel (device 66)
    'com.YoStarEN.AzurLane',        # EN
}


def pm(*packages):
    """Render `pm list packages` output for the given package names."""
    return ''.join(f'package:{p}\n' for p in packages)


class KnownPackagesTests(unittest.TestCase):
    def test_single_channel_package(self):
        out = pm('android', 'com.bilibili.blhx.qihoo', 'io.github.shinarin.alasaos')
        self.assertEqual(known(out, VALID), ['com.bilibili.blhx.qihoo'])

    def test_single_bserver_package(self):
        out = pm('com.android.settings', 'com.bilibili.azurlane')
        self.assertEqual(known(out, VALID), ['com.bilibili.azurlane'])

    def test_multiple_known_returns_all_sorted(self):
        # caller treats len != 1 as "cannot decide" and falls back
        out = pm('com.bilibili.blhx.qihoo', 'com.bilibili.azurlane')
        self.assertEqual(known(out, VALID), ['com.bilibili.azurlane', 'com.bilibili.blhx.qihoo'])

    def test_no_known_package(self):
        out = pm('android', 'com.android.settings', 'io.github.shinarin.alasaos')
        self.assertEqual(known(out, VALID), [])

    def test_empty_output(self):
        self.assertEqual(known('', VALID), [])

    def test_deduplicates_repeats(self):
        out = pm('com.bilibili.blhx.qihoo', 'com.bilibili.blhx.qihoo')
        self.assertEqual(known(out, VALID), ['com.bilibili.blhx.qihoo'])

    def test_substring_lookalike_not_matched(self):
        # a helper package that merely starts with a known name must not count
        out = pm('com.bilibili.blhx.qihoo.helper', 'com.bilibili.azurlane.demo')
        self.assertEqual(known(out, VALID), [])

    def test_ignores_trailing_garbage_on_line(self):
        # `pm list packages -f` style suffixes shouldn't break the package match
        out = 'package:com.bilibili.blhx.qihoo\npackage:com.android.shell\n'
        self.assertEqual(known(out, VALID), ['com.bilibili.blhx.qihoo'])


if __name__ == '__main__':
    unittest.main()

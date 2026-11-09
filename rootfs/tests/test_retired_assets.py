"""Retired AOS assets must come from the user's current ALAS Git source."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
ASSET = 'assets/cn/storage/MATERIAL_CHECK.png'
ASSETS = [
    'assets/cn/daily/DAILY_SKIP.png',
    'assets/cn/handler/AUTO_SEARCH_TITLE.png',
    'assets/cn/handler/CLEAR_MODE_TITLE.png',
    'assets/cn/map/MAP_PREPARATION.png',
    'assets/cn/retire/SORT_ASC.png',
    'assets/cn/retire/SORT_DESC.png',
    ASSET,
    'assets/cn/ui/SHOP_CHECK.png',
    'module/daily/assets.py',
    'module/map/assets.py',
    'module/retire/assets.py',
    'module/ui/assets.py',
]
SOURCE = (ROOT / 'rootfs/seeds/env_fix.sh').read_text(encoding='utf-8')
RESTORE = SOURCE[SOURCE.index('for asset_file in'):SOURCE.index('mark "done imageio=')]
BASH = os.environ.get('ALASAOS_TEST_BASH') or shutil.which('bash')


@unittest.skipUnless(BASH, 'Bash is required for the runtime migration')
class RetiredAssetsTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/retired-assets-tests'
        base.mkdir(parents=True, exist_ok=True)
        temp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.env = dict(os.environ, GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
                        GIT_CEILING_DIRECTORIES=str(self.root.parent), GIT_TERMINAL_PROMPT='0')
        self.git('init', '-q')
        self.asset = self.root / ASSET
        self.original = b'\x89PNG\r\n\x1a\ncurrent-source-template\x00\xff'
        for name in ASSETS:
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(self.original)
        self.unrelated = self.root / 'unrelated.py'
        self.unrelated.write_text('upstream\n')
        self.git('add', *ASSETS, 'unrelated.py')
        self.git('-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 'commit', '-qm', 'fixture')
        self.unrelated.write_text('keep active AOS overlay\n')

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args],
                                       env=self.env, stderr=subprocess.STDOUT, timeout=15)

    def restore(self):
        result = subprocess.run([BASH, '-c', 'mark() { echo "$*"; }\n' + RESTORE],
                                cwd=self.root, env=self.env, text=True,
                                capture_output=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.unrelated.read_text(), 'keep active AOS overlay\n')
        return result.stdout

    def test_restores_old_overlay_and_is_idempotent(self):
        for name in ASSETS:
            (self.root / name).write_bytes(b'old AOS overlay')
        self.assertIn('retired overlay restored', self.restore())
        for name in ASSETS:
            self.assertEqual((self.root / name).read_bytes(), self.original)
        stamp = self.asset.stat().st_mtime_ns
        self.assertEqual(self.restore(), '')
        self.assertEqual(self.asset.stat().st_mtime_ns, stamp)

    def test_uses_current_head_instead_of_the_index_or_a_bundled_template(self):
        self.original = b'different configured source\x00\xff'
        self.asset.write_bytes(self.original)
        self.git('add', ASSET)
        self.git('-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 'commit', '-qm', 'new source')
        self.asset.write_bytes(b'staged old overlay')
        self.git('add', ASSET)
        self.restore()
        self.assertEqual(self.asset.read_bytes(), self.original)

    def test_missing_upstream_asset_is_left_untouched(self):
        self.git('rm', '-q', ASSET)
        self.git('-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 'commit', '-qm', 'source without asset')
        self.asset.parent.mkdir(parents=True, exist_ok=True)
        self.asset.write_bytes(b'keep when upstream asset is absent')
        self.assertEqual(self.restore(), '')
        self.assertEqual(self.asset.read_bytes(), b'keep when upstream asset is absent')


if __name__ == '__main__':
    unittest.main()

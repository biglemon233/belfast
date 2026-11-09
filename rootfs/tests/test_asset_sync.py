"""The dual-source gate must reject drift in either direction, including binary assets."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('asset_sync', ROOT / 'app/scripts/check_asset_sync.py')
asset_sync = importlib.util.module_from_spec(spec)
spec.loader.exec_module(asset_sync)


class AssetSyncTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/asset-sync-tests'
        base.mkdir(parents=True, exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.tmp.cleanup)
        self.left, self.right = [Path(self.tmp.name) / name for name in ('rootfs', 'apk')]
        self.left.mkdir()
        self.right.mkdir()

    def test_missing_extra_and_binary_drift_are_rejected(self):
        (self.left / 'model.bin').write_bytes(b'\x00\xff')
        self.assertEqual(asset_sync.compare(self.left, self.right), ['Only in rootfs: model.bin'])
        (self.right / 'model.bin').write_bytes(b'\x00\xfe')
        self.assertEqual(asset_sync.compare(self.left, self.right), ['Content differs: model.bin'])
        (self.right / 'model.bin').write_bytes(b'\x00\xff')
        self.assertEqual(asset_sync.compare(self.left, self.right), [])
        (self.right / 'extra.py').write_text('extra')
        self.assertEqual(asset_sync.compare(self.left, self.right), ['Only in APK: extra.py'])

    def test_layout_exclusions_do_not_hide_runtime_seeds(self):
        (self.left / 'deploy.yaml').write_text('baked separately')
        (self.left / 'seed_deploy.py').write_text('runtime seed')
        self.assertEqual(asset_sync.compare(self.left, self.right, ('deploy.yaml',)),
                         ['Only in rootfs: seed_deploy.py'])

    def test_local_bytecode_is_not_an_asset(self):
        cache = self.left / '__pycache__'
        cache.mkdir()
        (cache / 'local.pyc').write_bytes(b'cache')
        self.assertEqual(asset_sync.compare(self.left, self.right), [])

    def test_missing_directory_is_not_reported_as_identical(self):
        self.assertTrue(asset_sync.compare(self.left, self.right / 'absent'))


if __name__ == '__main__':
    unittest.main()

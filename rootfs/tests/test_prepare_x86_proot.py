import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('prepare_x86', ROOT/'app/scripts/prepare_x86_proot.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class PreparedProotTests(unittest.TestCase):
    def setUp(self):
        base = ROOT/'.tmp/prepared-proot-tests'
        base.mkdir(parents=True, exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.output = self.root/'app/app/src/main/prootLibs/x86_64'
        shutil.copytree(ROOT/'app/app/src/main/prootLibs/x86_64', self.output)

    def test_exact_pinned_outputs_allow_fully_offline_preparation(self):
        with patch.object(m, 'ROOT', self.root), patch.object(m.urllib.request, 'urlopen') as network:
            m.main()
        network.assert_not_called()
        self.assertTrue(m.prepared_matches(self.output))
        self.assertFalse((self.root/'.tmp/x86-proot').exists())

    def test_changed_or_missing_binary_cannot_bypass_archive_verification(self):
        proot = self.output/'libproot.so'
        original = proot.read_bytes()
        proot.write_bytes(original + b'changed')
        self.assertFalse(m.prepared_matches(self.output))
        with patch.object(m, 'ROOT', self.root), patch.object(m.urllib.request, 'urlopen', side_effect=OSError('offline')) as network:
            with self.assertRaises(OSError):
                m.main()
        network.assert_called_once()
        proot.write_bytes(original)
        self.assertTrue(m.prepared_matches(self.output))
        proot.unlink()
        self.assertFalse(m.prepared_matches(self.output))

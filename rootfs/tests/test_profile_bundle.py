"""Synthetic archive/ELF fixtures for the read-only bundle profiler."""
import importlib.util
import io
from pathlib import Path
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('profile_bundle', ROOT / 'rootfs/build/profile_bundle.py')
profile = importlib.util.module_from_spec(spec)
spec.loader.exec_module(profile)


class BundleProfileTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/bundle-profile-tests'
        base.mkdir(parents=True, exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name)

    def test_rootfs_counts_groups_and_does_not_extract_links(self):
        path = self.path / 'rootfs.tar.xz'
        with tarfile.open(path, 'w:xz') as archive:
            for name, data in [('opt/alas/bin/cnocr_models/model', b'model'),
                               ('usr/local/lib/python3.12/dist-packages/numpy/test', b'array')]:
                info = tarfile.TarInfo(name)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
            link = tarfile.TarInfo('outside')
            link.type, link.linkname = tarfile.SYMTYPE, '../must-not-exist'
            archive.addfile(link)
        report = profile.profile_rootfs(path, self.path, compress_groups=True)
        self.assertEqual(report['file_bytes'], 10)
        self.assertEqual(report['groups']['alas/models']['file_bytes'], 5)
        self.assertGreater(report['groups']['python/numpy']['independent_xz_bytes_estimate'], 0)
        self.assertFalse((self.path / 'outside').exists())
        self.assertEqual(len(report['sha256']), 64)
        self.assertEqual(profile.differences({'rootfs': report}, {'rootfs': report})['rootfs']['total_bytes_delta'], 0)

    def test_apk_zip_sizes_plus_overhead_equal_actual_file(self):
        path = self.path / 'app.apk'
        with zipfile.ZipFile(path, 'w') as archive:
            archive.writestr('assets/rootfs/rootfs.tar.xz', b'rootfs' * 50)
            archive.writestr('classes.dex', b'code' * 100, compress_type=zipfile.ZIP_DEFLATED)
        report = profile.profile_apk(path)
        self.assertEqual(report['bytes'], path.stat().st_size)
        self.assertEqual(report['groups']['rootfs']['zip_compressed_bytes'], 300)
        self.assertEqual(sum(g['zip_compressed_bytes'] for g in report['groups'].values())
                         + report['zip_headers_and_signing_bytes'], report['bytes'])

    def test_dynamic_dependencies_parse_without_section_headers(self):
        data = bytearray(512)
        data[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', data, 18, 183)
        struct.pack_into('<Q', data, 32, 64)
        struct.pack_into('<HH', data, 54, 56, 2)
        struct.pack_into('<IIQQQQQQ', data, 64, 1, 0, 0, 0, 0, 512, 512, 8)
        struct.pack_into('<IIQQQQQQ', data, 120, 2, 0, 176, 176, 0, 64, 64, 8)
        for index, pair in enumerate([(5, 260), (1, 1), (14, 12), (0, 0)]):
            struct.pack_into('<qQ', data, 176 + index * 16, *pair)
        strings = b'\x00libbase.so\x00libexample.so\x00'
        data[260:260 + len(strings)] = strings
        result = profile.elf_dependencies(bytes(data))
        self.assertEqual(result['machine'], 183)
        self.assertEqual(result['needed'], ['libbase.so'])
        self.assertEqual(result['soname'], 'libexample.so')

    def test_non_elf_and_truncated_elf_are_distinguished(self):
        self.assertIsNone(profile.elf_dependencies(b'not ELF'))
        with self.assertRaises(ValueError):
            profile.elf_dependencies(b'\x7fELF\x02\x01')

    def test_output_cannot_replace_an_input_archive(self):
        path = self.path / 'input.apk'
        path.write_bytes(b'keep original')
        result = subprocess.run([sys.executable, str(ROOT / 'rootfs/build/profile_bundle.py'),
                                 '--apk', str(path), '--output', str(path)],
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 2)
        self.assertIn('Output must differ', result.stderr)
        self.assertEqual(path.read_bytes(), b'keep original')

    def test_model_and_runtime_library_classification(self):
        self.assertEqual(profile.rootfs_group('./opt/alas/assets/cn/test.png'), 'alas/assets')
        self.assertEqual(profile.rootfs_group('./opt/alas/module/ocr/models.py'), 'alas/code-and-data')
        self.assertEqual(profile.rootfs_group('./usr/local/mxnet/libmxnet.so'), 'python/mxnet')
        self.assertEqual(profile.rootfs_group('usr/share/zoneinfo/Asia/Shanghai'), 'linux/other')


if __name__ == '__main__':
    unittest.main()

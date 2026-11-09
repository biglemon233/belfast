import importlib.util
import io
import lzma
from pathlib import Path
import shutil
import tarfile
import tempfile
import unittest
import sys

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('trim_rootfs', ROOT / 'rootfs/build/trim_rootfs.py')
trim = importlib.util.module_from_spec(spec)
spec.loader.exec_module(trim)
sys.modules['trim_rootfs'] = trim
validation_spec = importlib.util.spec_from_file_location('validate_trim', ROOT / 'rootfs/build/validate_trim.py')
validation = importlib.util.module_from_spec(validation_spec)
validation_spec.loader.exec_module(validation)


class TrimRootfsTest(unittest.TestCase):
    def setUp(self):
        scratch = ROOT / '.tmp/trim-tests'
        scratch.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=scratch)
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name)

    def archive(self, entries):
        path = self.path / 'input.tar'
        with tarfile.open(path, 'w') as t:
            for name, value in entries:
                m = tarfile.TarInfo(name)
                m.uid, m.gid, m.mode = 123, 456, 0o755
                if isinstance(value, tuple):
                    m.type, m.linkname = value
                    t.addfile(m)
                else:
                    m.size = len(value)
                    t.addfile(m, io.BytesIO(value))
        return path

    def test_never_trims_alas_models_runtime_helpers_or_licenses(self):
        names = ['opt/alas/tests/case.py', 'opt/alas/bin/cnocr_models/model.params',
                 'usr/share/doc/libfoo/copyright', 'usr/share/doc/libfoo/LICENSE.gz',
                 'usr/lib/aarch64-linux-gnu/libblas.so', 'etc/ssl/certs/ca.pem',
                 'usr/share/zoneinfo/Asia/Shanghai', 'usr/share/fonts/font.ttf',
                 'usr/lib/python3/dist-packages/numpy/testing/_private/utils.py',
                 'usr/lib/python3/dist-packages/scipy/_lib/_testutils.py',
                 'usr/lib/python3/dist-packages/pip/__init__.py',
                 'usr/lib/python3/dist-packages/uiautomator2cache/cache/atx-agent_0.10.0_linux_arm64.tar.gz-123/archive']
        for name in names:
            with self.subTest(name=name):
                self.assertIsNone(trim.removal_group(name))

    def test_roundtrip_preserves_bytes_metadata_links_and_source(self):
        src = self.archive([('opt/alas/code.py', b'original'),
                            ('usr/lib/libfoo.so.1', b'ELF payload'),
                            ('usr/lib/libfoo.so', (tarfile.SYMTYPE, 'libfoo.so.1')),
                            ('usr/lib/alias', (tarfile.LNKTYPE, 'usr/lib/libfoo.so.1')),
                            ('usr/share/man/man1/foo.1', b'documentation')])
        old = src.read_bytes()
        dest = self.path / 'trimmed.tar.xz'
        report = trim.trim_archive(src, dest)
        self.assertEqual(src.read_bytes(), old)
        self.assertEqual(report['removed_bytes'], 13)
        self.assertNotIn('compressed_saved_bytes', report, 'An uncompressed tar is not a size baseline')
        with tarfile.open(dest) as t:
            self.assertEqual(t.extractfile('opt/alas/code.py').read(), b'original')
            m = t.getmember('opt/alas/code.py')
            self.assertEqual((m.uid, m.gid, m.mode), (123, 456, 0o755))
            self.assertTrue(t.getmember('usr/lib/libfoo.so').issym())
            self.assertEqual(t.extractfile('usr/lib/alias').read(), b'ELF payload')
        self.assertEqual(report['alas_tree_sha256'], trim.trim_archive(dest)['alas_tree_sha256'])

    def test_dangling_hardlink_refuses_completed_output(self):
        src = self.archive([('usr/share/man/foo', b'x'),
                            ('usr/lib/runtime', (tarfile.LNKTYPE, 'usr/share/man/foo'))])
        dest = self.path / 'bad.tar.xz'
        with self.assertRaisesRegex(ValueError, 'hard link'):
            trim.trim_archive(src, dest)
        self.assertFalse(dest.exists())

    def test_groups_are_explicit_and_unknown_groups_rejected(self):
        src = self.archive([('usr/share/man/foo', b'x'), ('usr/include/foo.h', b'y')])
        r = trim.trim_archive(src, groups=['linux-docs'])
        self.assertEqual([x['path'] for x in r['removed']], ['usr/share/man/foo'])
        with self.assertRaises(ValueError):
            trim.trim_archive(src, groups=['everything'])

    def test_unsafe_paths_and_overwrites_rejected(self):
        for name in ('../opt/alas/data', '/opt/alas/data', 'usr/../../etc/passwd'):
            with self.assertRaises(ValueError):
                trim.removal_group(name)
        src = self.archive([('safe', b'x')])
        with self.assertRaises(ValueError):
            trim.trim_archive(src, src)

    def test_repack_control_preserves_all_entries(self):
        src = self.archive([('usr/share/man/foo', b'doc'), ('opt/alas/one', b'code')])
        dest = self.path / 'control.tar.xz'
        report = trim.trim_archive(src, dest, groups=())
        self.assertEqual(report['removed'], [])
        with tarfile.open(dest) as t:
            self.assertEqual(t.extractfile('usr/share/man/foo').read(), b'doc')

    def test_plain_intermediate_matches_existing_xz_payload_exactly(self):
        source = self.archive([('opt/alas/original', b'original' * 1024),
                               ('usr/share/man/foo', b'doc'),
                               ('usr/lib/alias', (tarfile.LNKTYPE, 'opt/alas/original'))])
        plain, compressed = self.path / 'trimmed.tar', self.path / 'trimmed.tar.xz'
        left = trim.trim_archive(source, plain, compress=False)
        right = trim.trim_archive(source, compressed)
        self.assertEqual(plain.read_bytes(), lzma.decompress(compressed.read_bytes()))
        self.assertEqual(left['alas_tree_sha256'], right['alas_tree_sha256'])
        self.assertEqual(left['removed'], right['removed'])
        self.assertEqual(left['compression'], {'format': 'tar'})

    def test_parallel_native_controls_keep_the_exact_sequential_xz_bytes(self):
        sys.path.insert(0, str(ROOT / 'rootfs/build'))
        try:
            from validate_slim_opencv import compress_controls
            source = self.archive([('opt/alas/original', b'original' * 1024)])
            sequential = self.path / 'sequential.tar.xz'
            trim.trim_archive(source, sequential, groups=())
            reports = compress_controls({'baseline': source, 'candidate': source}, self.path)
            for name in reports:
                self.assertEqual((self.path / f'{name}.tar.xz').read_bytes(), sequential.read_bytes())
        finally:
            sys.path.pop(0)

    def test_compression_presets_preserve_the_same_tar_with_bounded_decoder_memory(self):
        source = self.archive([('opt/alas/original', b'original bytes' * 4096),
                               ('usr/lib/libfoo.so', (tarfile.SYMTYPE, 'libfoo.so.1'))])
        decoded = []
        for preset in (6, 7, 8):
            output = self.path / f'preset-{preset}.tar.xz'
            report = trim.trim_archive(source, output, groups=(), xz_preset=preset)
            decoded.append(lzma.decompress(output.read_bytes(), memlimit=40 * 1024 * 1024))
            self.assertEqual(report['compression']['preset'], preset)
            self.assertEqual(report['removed'], [])
        self.assertEqual(decoded[0], decoded[1])
        self.assertEqual(decoded[0], decoded[2])
        default = self.path / 'default.tar.xz'
        report = trim.trim_archive(source, default, groups=())
        self.assertEqual(report['compression']['dictionary_bytes'], 32 * 1024 * 1024)
        self.assertEqual(lzma.decompress(default.read_bytes()), decoded[0])
        with self.assertRaises(ValueError):
            trim.trim_archive(source, self.path / 'unsupported.tar.xz', xz_preset=9)

    @unittest.skipUnless(shutil.which('tar'), 'Requires the tar used by the build entrypoint')
    def test_build_tar_and_artifact_xz_both_unpack(self):
        source = self.archive([('opt/alas/file', b'original code')])
        compressed = self.path / 'artifact.tar.xz'
        trim.trim_archive(source, compressed, groups=())
        for index, archive in enumerate((source, compressed)):
            with self.subTest(archive=archive.name):
                root = self.path / f'unpacked-{index}'
                root.mkdir()
                validation.unpack_archive(archive, root)
                self.assertEqual((root / 'opt/alas/file').read_bytes(), b'original code')

    def test_tree_apply_refuses_tampered_alas_entry(self):
        p = self.path / 'opt/alas/file'
        p.parent.mkdir(parents=True)
        p.write_bytes(b'original')
        plan = {'removed': [{'path': 'opt/alas/file', 'bytes': 8, 'group': 'linux-docs'}]}
        with self.assertRaises(ValueError):
            validation.apply_group(self.path, plan, 'linux-docs')
        self.assertEqual(p.read_bytes(), b'original')

    @unittest.skipIf(sys.platform == 'win32', 'Linux symlink behavior is tested by the Actions runner')
    def test_tree_apply_never_follows_symlink_parent(self):
        root = self.path / 'root'
        outside = self.path / 'outside'
        (root / 'usr/share').mkdir(parents=True)
        outside.mkdir()
        (outside / 'one').write_bytes(b'preserve')
        (root / 'usr/share/man').symlink_to(outside, target_is_directory=True)
        plan = {'removed': [{'path': 'usr/share/man/one', 'bytes': 8, 'group': 'linux-docs'}]}
        with self.assertRaises(ValueError):
            validation.apply_group(root, plan, 'linux-docs')
        self.assertEqual((outside / 'one').read_bytes(), b'preserve')


if __name__ == '__main__':
    unittest.main()

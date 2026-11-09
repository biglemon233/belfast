"""Offline AArch64 ELF fixtures; all temporary files stay under project .tmp."""
import importlib.util
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
BUILD = ROOT / 'rootfs/build'
spec = importlib.util.spec_from_file_location('profile_bundle', BUILD / 'profile_bundle.py')
profile = importlib.util.module_from_spec(spec)
spec.loader.exec_module(profile)
sys.modules.setdefault('profile_bundle', profile)
spec = importlib.util.spec_from_file_location('slim_opencv_backends', BUILD / 'slim_opencv_backends.py')
slim = importlib.util.module_from_spec(spec)
spec.loader.exec_module(slim)


def elf(soname='', needed=(), exports=(), undefined=(), machine=183, kind=3):
    """Minimal ELF64 shared object with independent dynamic and section tables."""
    data = bytearray(4096)
    data[:16] = b'\x7fELF\x02\x01\x01' + bytes(9)
    struct.pack_into('<HHIQQQIHHHHHH', data, 16, kind, machine, 1, 0, 64, 3072,
                     0, 64, 56, 2, 64, 3, 0)
    strings = bytearray(b'\0')
    names = {}
    for name in (soname, *needed, *exports, *undefined):
        if name and name not in names:
            names[name] = len(strings)
            strings.extend(name.encode() + b'\0')
    data[1024:1024 + len(strings)] = strings
    tags = [(5, 1024), *([(14, names[soname])] if soname else []),
            *((1, names[n]) for n in needed), (0, 0)]
    struct.pack_into('<IIQQQQQQ', data, 64, 1, 4, 0, 0, 0, len(data), len(data), 8)
    struct.pack_into('<IIQQQQQQ', data, 120, 2, 4, 256, 256, 0, len(tags) * 16, len(tags) * 16, 8)
    for i, tag in enumerate(tags):
        struct.pack_into('<qQ', data, 256 + i * 16, *tag)
    for i, (name, section) in enumerate([*((n, 1) for n in exports), *((n, 0) for n in undefined)], 1):
        struct.pack_into('<IBBHQQ', data, 2048 + i * 24, names[name], 0x12, 0, section, 0, 0)
    struct.pack_into('<IIQQQQIIQQ', data, 3136, 0, 3, 0, 0, 1024, len(strings), 0, 0, 1, 0)
    struct.pack_into('<IIQQQQIIQQ', data, 3200, 0, 11, 0, 0, 2048,
                     (1 + len(exports) + len(undefined)) * 24, 1, 0, 8, 24)
    return bytes(data)


class SlimOpenCVTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/slim-opencv-tests'
        base.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.temp.cleanup)
        # Windows CI/desktop may deny symlink creation. Keep graph/ABI tests
        # executable with file-link fixtures; real filesystem tests stay Linux-only.
        self.emulated, self.links = False, {}
        real_is_link, real_readlink = Path.is_symlink, os.readlink
        link_check = patch.object(Path, 'is_symlink', lambda p:
                                  (p in self.links and p.exists()) or real_is_link(p))
        link_read = patch.object(os, 'readlink', lambda p, **kw:
                                 self.links[p] if p in self.links else real_readlink(p, **kw))
        link_check.start()
        link_read.start()
        self.addCleanup(link_check.stop)
        self.addCleanup(link_read.stop)
        self.work = Path(self.temp.name)
        self.root = self.work / 'rootfs'
        self.lib = self.root / slim.LIBDIR
        self.lib.mkdir(parents=True)
        (self.root / 'etc').mkdir()
        (self.root / 'etc/os-release').write_text('ID=ubuntu\n', encoding='utf-8')
        (self.root / 'opt/alas').mkdir(parents=True)
        (self.root / 'opt/alas/upstream.py').write_bytes(b'upstream must stay intact\n')
        self.original = self.root / slim.TARGET
        self.original.write_bytes(elf(slim.SONAME, ('libcore.so.1', 'libgdal.so.34'), ('cv_imread', 'cv_unused')))
        self.link(self.lib / slim.SONAME, self.original.name)
        self.library('libcore.so.1', exports=('core_symbol',))
        self.library('libgdal.so.34')
        self.replacement = self.work / 'new-imgcodecs.so'
        self.replacement.write_bytes(elf(slim.SONAME, ('libcore.so.1',), ('cv_imread',)))
        self.mxnet = self.root / 'usr/local/mxnet/libmxnet.so'
        self.mxnet.parent.mkdir(parents=True)
        self.mxnet.write_bytes(elf('libmxnet.so', (slim.SONAME,), undefined=('cv_imread',)))

    def link(self, path, target):
        try:
            path.symlink_to(target, target_is_directory=(path.parent / target).is_dir())
        except OSError as error:
            if os.name != 'nt' or error.winerror != 1314:
                raise
            self.emulated = True
            path.write_bytes(b'symlink-fixture')
            self.links[path] = target

    def library(self, name, **kwargs):
        path = self.lib / name
        path.write_bytes(elf(name, **kwargs))
        return path

    def snapshot(self):
        return {p.relative_to(self.root).as_posix(): os.readlink(p) if p.is_symlink() else p.read_bytes()
                for p in self.root.rglob('*') if p.is_symlink() or p.is_file()}

    def test_dry_run_is_read_only_and_reports_hashes_and_consumed_exports(self):
        before = self.snapshot()
        report = slim.slim(self.root, self.replacement)
        self.assertEqual(self.snapshot(), before)
        self.assertFalse(report['applied'])
        self.assertEqual(report['original'], profile.identity(self.original))
        self.assertEqual(report['replacement'], profile.identity(self.replacement))
        self.assertEqual(report['consumers']['usr/local/mxnet/libmxnet.so'], ['cv_imread'])
        self.assertEqual(report['remove_bytes'], len(elf('libgdal.so.34')))

    def test_apply_removes_only_explicit_backends_and_their_aliases(self):
        preserved = ['libicuuc.so.74', 'libxml2.so.2', 'libopenblas.so.0', 'libgdal.so.35', 'libunused.so.1']
        for name in preserved:
            self.library(name)
        self.library('libOpenEXR-3_1.so.30')
        self.link(self.lib / 'libgdal.so', 'libgdal.so.34')
        nested = self.lib / 'ogdi/4.1/libgdal.so'
        nested.parent.mkdir(parents=True)
        nested.write_bytes(elf())
        before = self.snapshot()
        report = slim.slim(self.root, self.replacement, apply=True)
        self.assertTrue(report['applied'])
        deleted = {e['path'] for e in report['remove']}
        self.assertEqual(deleted, {str(slim.LIBDIR / n) for n in
                                   ('libgdal.so.34', 'libgdal.so', 'libOpenEXR-3_1.so.30')})
        self.assertEqual(self.original.read_bytes(), self.replacement.read_bytes())
        after = self.snapshot()
        for path, content in before.items():
            if path not in deleted | {str(slim.TARGET)}:
                self.assertEqual(after[path], content, path)
        self.assertFalse((self.lib / 'libgdal.so').is_symlink())

    def test_remaining_consumer_retains_backend_and_its_dependencies(self):
        self.library('libgdcmMSFF.so.3.0', needed=('libgdcmCommon.so.3.0',))
        self.library('libgdcmCommon.so.3.0')
        self.library('libother.so.1', needed=('libgdcmMSFF.so.3.0',))
        report = slim.slim(self.root, self.replacement, apply=True)
        self.assertEqual(set(report['retained']), {str(slim.LIBDIR / n) for n in
                                                  ('libgdcmMSFF.so.3.0', 'libgdcmCommon.so.3.0')})
        self.assertTrue((self.lib / 'libgdcmCommon.so.3.0').is_file())
        self.assertIn('libother.so.1', str(report['retained']))

    def test_foreign_assets_are_preserved_without_entering_arm64_graph(self):
        directory = self.root / 'opt/alas/bin/ascreencap'
        directory.mkdir(parents=True)
        x86 = bytearray(elf(machine=3))
        x86[4] = 1  # The inventory parser intentionally does not decode ELF32.
        samples = {'x86': bytes(x86), 'x86_64': elf(machine=62, needed=('libgdal.so.34',))}
        for name, data in samples.items():
            (directory / name).write_bytes(data)
        cached = self.root / 'usr/local/lib/python3.12/dist-packages/uiautomator2cache/cache/minicap.so'
        cached.parent.mkdir(parents=True)
        cached.write_bytes(bytes(x86))
        report = slim.slim(self.root, self.replacement, apply=True)
        self.assertEqual(set(report['preserved_foreign_assets']),
                         {f'opt/alas/bin/ascreencap/{name}' for name in samples} |
                         {cached.relative_to(self.root).as_posix()})
        self.assertFalse((self.lib / 'libgdal.so.34').exists())
        for name, data in samples.items():
            self.assertEqual((directory / name).read_bytes(), data)
        self.assertEqual(cached.read_bytes(), bytes(x86))

    def test_alias_outside_library_directory_retains_target(self):
        self.link(self.root / 'opt/alas/private-backend.so', '../../usr/lib/aarch64-linux-gnu/libgdal.so.34')
        report = slim.slim(self.root, self.replacement)
        self.assertFalse(report['remove'])
        self.assertIn('Alias outside', str(report['retained']))

    def test_path_based_dependency_cannot_hide_a_backend_consumer(self):
        self.library('libother.so.1', needed=('/usr/lib/aarch64-linux-gnu/libgdal.so.34',))
        before = self.snapshot()
        with self.assertRaisesRegex(ValueError, 'dependency path'):
            slim.slim(self.root, self.replacement, apply=True)
        self.assertEqual(self.snapshot(), before)

    def test_invalid_replacement_rejected_without_changes(self):
        cases = [
            (elf(slim.SONAME, exports=('cv_imread',), machine=62), 'AArch64'),
            (elf('libopencv_imgcodecs.so.410', exports=('cv_imread',)), 'SONAME'),
            (elf(slim.SONAME, exports=('cv_imread',), kind=2), 'shared ELF'),
            (elf(slim.SONAME, ('libmissing.so.1',), ('cv_imread',)), 'Missing replacement'),
            (elf(slim.SONAME, ('libgdal.so.34',), ('cv_imread',)), 'optional backends'),
            (elf(slim.SONAME, exports=('different_api',)), 'Missing consumer exports'),
        ]
        for data, error in cases:
            with self.subTest(error=error):
                before = self.snapshot()
                self.replacement.write_bytes(data)
                with self.assertRaisesRegex(ValueError, error):
                    slim.slim(self.root, self.replacement, apply=True)
                self.assertEqual(self.snapshot(), before)

    def test_indirect_backend_dependency_is_rejected(self):
        self.library('libcore.so.1', needed=('libgdal.so.34',))
        with self.assertRaisesRegex(ValueError, 'transitively needs backend'):
            slim.slim(self.root, self.replacement)

    def test_missing_or_wrong_architecture_transitive_dependency_is_rejected(self):
        self.library('libcore.so.1', needed=('libmissing.so.1',))
        with self.assertRaisesRegex(ValueError, 'Missing transitive'):
            slim.slim(self.root, self.replacement)
        self.library('libmissing.so.1', machine=62)
        with self.assertRaisesRegex(ValueError, 'Non-AArch64'):
            slim.slim(self.root, self.replacement)

    def test_existing_missing_dependency_is_reported_but_not_newly_introduced(self):
        self.library('libexisting.so.1', needed=('libalready_missing.so.1',))
        report = slim.slim(self.root, self.replacement)
        self.assertIn((str(slim.LIBDIR / 'libexisting.so.1'), 'libalready_missing.so.1'),
                      report['preexisting_missing'])

    def test_guest_links_never_resolve_against_host_root(self):
        self.link(self.lib / 'absolute', '/usr/lib/aarch64-linux-gnu/libcore.so.1')
        self.assertEqual(slim.guest_path(self.root, slim.LIBDIR / 'absolute'), slim.LIBDIR / 'libcore.so.1')
        self.link(self.lib / 'escape', '../../../../outside')
        with self.assertRaisesRegex(ValueError, 'escapes rootfs'):
            slim.slim(self.root, self.replacement)

    def test_symlink_parent_root_alias_and_alas_write_paths_rejected(self):
        with self.assertRaisesRegex(ValueError, 'allowlist'):
            slim.writable(self.root, 'opt/alas/upstream.py')
        with self.assertRaisesRegex(ValueError, 'allowlist'):
            slim.writable(self.root, '../outside')
        if self.emulated:
            self.skipTest('Root/parent filesystem symlinks require Linux or Windows symlink privilege')
        alias = self.work / 'root-alias'
        self.link(alias, 'rootfs')
        with self.assertRaisesRegex(ValueError, 'real offline directory'):
            slim.slim(alias, self.replacement)
        original_dir = self.root / 'real-lib'
        self.lib.rename(original_dir)
        self.link(self.lib, '../../../rootfs/real-lib')
        with self.assertRaisesRegex(ValueError, 'Symlink parent'):
            slim.slim(self.root, self.replacement)

    def test_backend_hardlink_to_upstream_is_rejected(self):
        backend = self.lib / 'libgdal.so.34'
        os.link(backend, self.root / 'opt/alas/keep.so')
        with self.assertRaisesRegex(ValueError, 'Hardlink'):
            slim.slim(self.root, self.replacement, apply=True)
        self.assertTrue(backend.exists())

    def test_report_cannot_overwrite_upstream(self):
        report = self.root / 'opt/alas/upstream.py'
        result = subprocess.run([sys.executable, str(BUILD / 'slim_opencv_backends.py'),
                                 '--rootfs', str(self.root), '--replacement', str(self.replacement),
                                 '--output', str(report), '--apply'], capture_output=True, text=True, timeout=15)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Report must be outside rootfs', result.stderr)
        self.assertEqual(report.read_bytes(), b'upstream must stay intact\n')

    def test_cli_writes_dry_run_json(self):
        if self.emulated:
            self.skipTest('CLI subprocess needs real symlink fixtures; run on Linux')
        output = self.work / 'report.json'
        result = subprocess.run([sys.executable, str(BUILD / 'slim_opencv_backends.py'),
                                 '--rootfs', str(self.root), '--replacement', str(self.replacement),
                                 '--output', str(output)], capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(json.loads(output.read_text(encoding='utf-8'))['applied'])


class ElfAuditTests(unittest.TestCase):
    def test_symbols_separate_exported_and_undefined_api(self):
        exported, undefined = slim.symbols(elf('example.so', exports=('cv_api',), undefined=('dependency',)))
        self.assertEqual(exported, {'cv_api'})
        self.assertEqual(undefined, {'dependency'})

    def test_missing_section_table_cannot_silently_pass_abi_audit(self):
        data = bytearray(elf(slim.SONAME, exports=('cv_api',)))
        struct.pack_into('<H', data, 60, 0)
        with self.assertRaisesRegex(ValueError, 'Missing ELF section table'):
            slim.symbols(bytes(data))

    def test_truncated_dynsym_is_rejected(self):
        data = bytearray(elf(slim.SONAME, exports=('cv_api',)))
        struct.pack_into('<Q', data, 3200 + 32, 4800)
        with self.assertRaisesRegex(ValueError, 'Truncated ELF dynsym'):
            slim.symbols(bytes(data))

    def test_elf_inventory_parser_agrees_on_fixture_abi(self):
        details = slim.inspect_library(elf(slim.SONAME, ('libcore.so.1',), ('cv_api',)))
        self.assertEqual(details, {'machine': 183, 'needed': ['libcore.so.1'], 'soname': slim.SONAME})


if __name__ == '__main__':
    unittest.main()

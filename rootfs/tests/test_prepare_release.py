import importlib.util
from pathlib import Path
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('prepare_release', ROOT / 'app/scripts/prepare_release.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class PrepareReleaseTests(unittest.TestCase):
    def setUp(self):
        scratch = ROOT / '.tmp/release-tests'
        scratch.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=scratch)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'rootfs.tar.xz').write_bytes(b'archive')
        (self.root / 'BUILD_MANIFEST').write_bytes(b'manifest')
        self.full = self.root / 'ALAS-AOS-v0.1.11-arm64-v8a-full.apk'
        self.lite = self.root / 'ALAS-AOS-v0.1.11-universal-lite.apk'
        with zipfile.ZipFile(self.full, 'w') as z:
            z.writestr('assets/rootfs/rootfs.tar.xz', b'archive')
            z.writestr('assets/rootfs/BUILD_MANIFEST', b'manifest')
        with zipfile.ZipFile(self.lite, 'w') as z:
            z.writestr('classes.dex', b'dex')

    def prepare(self, tag='v0.1.11'):
        release.prepare(tag, '0.1.11', '# Changes\n\n## v0.1.11（2026-10-13）\n\n- 新改动\n\n## v0.1.10\n旧改动\n',
                        self.root, self.root, self.root / 'output')

    def test_matching_pair_generates_only_current_notes_and_all_checksums(self):
        self.prepare()
        notes = (self.root / 'output/notes.md').read_text(encoding='utf-8')
        self.assertIn('新改动', notes)
        self.assertNotIn('旧改动', notes)
        self.assertEqual(4, len((self.root / 'output/SHA256SUMS').read_text().splitlines()))

    def test_mismatched_tag_or_runtime_refuses_release(self):
        with self.assertRaises(ValueError):
            self.prepare('v0.1.12')
        (self.root / 'rootfs.tar.xz').write_bytes(b'different archive')
        with self.assertRaisesRegex(ValueError, 'runtime differs'):
            self.prepare()

    def test_generated_notes_do_not_require_a_tracked_changelog(self):
        release.prepare('v0.1.11', '0.1.11', '', self.root, self.root,
                        self.root / 'output', notes_override='Generated release changes')
        self.assertIn('Generated release changes',
                      (self.root / 'output/notes.md').read_text(encoding='utf-8'))
        with self.assertRaisesRegex(ValueError, 'empty'):
            release.prepare('v0.1.11', '0.1.11', '', self.root, self.root,
                            self.root / 'empty', notes_override='  ')

    def test_missing_changelog_or_bundled_lite_refuses_release(self):
        with self.assertRaises(ValueError):
            release.release_notes('## v0.1.10\nold', 'v0.1.11')
        with zipfile.ZipFile(self.lite, 'a') as z:
            z.writestr('assets/rootfs/BUILD_MANIFEST', b'manifest')
        with self.assertRaisesRegex(ValueError, 'Lite APK'):
            self.prepare()

    def test_two_full_packages_share_one_lite_and_keep_architecture_pairs(self):
        (self.root / 'rootfs-amd64.tar.xz').write_bytes(b'amd64 archive')
        (self.root / 'BUILD_MANIFEST-amd64').write_bytes(b'amd64 manifest')
        full = self.root / 'ALAS-AOS-v0.1.11-x86_64-full.apk'
        with zipfile.ZipFile(full, 'w') as z:
            z.writestr('assets/rootfs/rootfs.tar.xz', b'amd64 archive')
            z.writestr('assets/rootfs/BUILD_MANIFEST', b'amd64 manifest')
        release.prepare('v0.1.11', '0.1.11', '', self.root, self.root,
                        self.root / 'output', notes_override='Changes', abis=('arm64-v8a', 'x86_64'))
        checksums = (self.root / 'output/SHA256SUMS').read_text().splitlines()
        self.assertEqual(7, len(checksums))
        self.assertEqual(1, sum('universal-lite' in line for line in checksums))
        (self.root / 'rootfs-amd64.tar.xz').write_bytes(b'wrong architecture archive')
        with self.assertRaisesRegex(ValueError, 'runtime differs'):
            release.prepare('v0.1.11', '0.1.11', '', self.root, self.root,
                            self.root / 'bad', notes_override='Changes', abis=('arm64-v8a', 'x86_64'))


if __name__ == '__main__':
    unittest.main()

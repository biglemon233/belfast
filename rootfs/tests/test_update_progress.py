"""Offline tests: real numeric transfer progress without raw Git/network diagnostics."""
import contextlib
import io
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[2]
SEEDS = ROOT / 'rootfs/seeds'
sys.path.insert(0, str(SEEDS))
import update_progress as progress
import cdn_update as cdn
sys.path.pop(0)


class ProgressTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/transfer-progress-tests'
        base.mkdir(parents=True, exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name)

    def test_git_bytes_are_not_object_percentage(self):
        self.assertIsNone(progress.git_progress('Receiving objects: 50% (1/2)'))
        self.assertEqual(progress.git_progress('Receiving objects: 50% (1/2), 1.50 MiB | 12.00 KiB/s'),
                         (1572864, 12288))
        self.assertIsNone(progress.git_progress('fatal: https://user:FAKE_TEST_SECRET@example.invalid'))

    def test_process_drains_large_private_lines_and_preserves_exit_code(self):
        snapshot = self.path / 'progress'
        command = [sys.executable, '-c',
                   "import sys; sys.stderr.write('FAKE_TEST_SECRET'*20000+'\\rReceiving objects: 100% (2/2), 1.00 MiB | 2.00 MiB/s\\r'); sys.exit(7)"]
        code = progress.run_process(command, progress.ProgressWriter(snapshot))
        self.assertEqual(code, 7)
        self.assertEqual(snapshot.read_text(), 'AOS_PROGRESS download 1048576 -1 2097152\n')
        self.assertLess(snapshot.stat().st_size, 512)
        self.assertNotIn('FAKE_TEST_SECRET', snapshot.read_text())

    def test_snapshot_is_numeric_atomic_and_bounded(self):
        snapshot = self.path / 'progress'
        writer = progress.ProgressWriter(snapshot)
        writer.report('download', 100, -1, 200, force=True)
        writer.report('unpack', force=True)
        self.assertEqual(snapshot.read_text(), 'AOS_PROGRESS unpack 0 -1 0\n')
        self.assertEqual(list(self.path.glob('*.tmp')), [])
        progress.ProgressWriter(self.path / 'missing/parent').report('download', force=True)

    def test_cdn_counts_known_and_unknown_content_length(self):
        latest, current = 'a' * 40, 'b' * 40
        raw = io.BytesIO()
        with zipfile.ZipFile(raw, 'w') as archive:
            archive.writestr(f'pack-{latest}.pack', b'pack-data')
            archive.writestr(f'pack-{latest}.idx', b'index-data')
        data = raw.getvalue()
        for known in (True, False):
            with self.subTest(known=known):
                response = io.BytesIO(data)
                response.headers = {'Content-Length': str(len(data))} if known else {}
                with patch.object(cdn.urllib.request, 'build_opener') as opener, patch.object(cdn, 'ProgressWriter') as writer:
                    opener.return_value.open.return_value = response
                    ok, reason = cdn.download_pack('https://example.invalid', latest, current, str(self.path))
                    self.assertTrue(ok, reason)
                    calls = writer.return_value.report.call_args_list
                    self.assertTrue(any(c.args == ('download', len(data), len(data) if known else -1)
                                        for c in calls))
                    self.assertEqual(calls[-1].args[0], 'unpack')
                self.assertEqual((self.path / f'.git/objects/pack/pack-{latest}.pack').read_bytes(), b'pack-data')
                self.assertEqual((self.path / '.git/refs/remotes/origin/master').read_text().strip(), latest)

    def test_network_exception_never_exposes_private_text(self):
        output = io.StringIO()
        with patch.object(cdn.urllib.request, 'build_opener') as opener, contextlib.redirect_stdout(output):
            opener.return_value.open.side_effect = OSError('https://user:FAKE_TEST_SECRET@example.invalid')
            self.assertEqual(cdn.fetch_latest(), (None, ''))
            self.assertEqual(cdn.download_pack('https://example.invalid', 'a'*40, 'b'*40, str(self.path)),
                             (False, 'UNAVAILABLE transfer'))
        self.assertNotIn('FAKE_TEST_SECRET', output.getvalue())

    def test_bad_zip_has_a_fixed_error(self):
        response = io.BytesIO(b'not a zip')
        response.headers = {}
        with patch.object(cdn.urllib.request, 'build_opener') as opener, patch.object(cdn, 'ProgressWriter'):
            opener.return_value.open.return_value = response
            self.assertEqual(cdn.download_pack('https://example.invalid', 'a'*40, 'b'*40, str(self.path)),
                             (False, 'UNAVAILABLE not-a-zip'))


if __name__ == '__main__':
    unittest.main()

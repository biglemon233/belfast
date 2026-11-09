"""Private deploy read protocol: quoting, validation, and refusal of the old writer."""
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'rootfs/seeds/sync_deploy.py'
spec = importlib.util.spec_from_file_location('source_reader', SCRIPT)
reader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reader)


class SourceReaderTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/source-reader-tests'
        base.mkdir(parents=True, exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.tmp.cleanup)
        self.deploy = Path(self.tmp.name) / 'deploy.yaml'

    def write(self, repo, branch):
        self.deploy.write_bytes((f'Deploy:\r\n  Git:\r\n    Repository: {repo}\r\n'
                                 f'    Branch: {branch}\r\n').encode())

    def test_quotes_and_inline_comments_are_not_values(self):
        self.write("'https://gitee.com/custom/repo.git'  # keep", '"cloud" # keep')
        before = self.deploy.read_bytes()
        self.assertEqual(reader.read_source(self.deploy), ('https://gitee.com/custom/repo.git', 'cloud'))
        self.assertEqual(self.deploy.read_bytes(), before)

    def test_hash_inside_url_password_is_not_a_comment(self):
        self.write("'https://user:p%23ss@example.invalid/repo' # keep", "'feature/#new' # keep")
        self.assertEqual(reader.read_source(self.deploy)[1], 'feature/#new')

    def test_single_quote_escape_in_branch(self):
        self.write('https://gitee.com/custom/repo', "'feature/user''s' # keep")
        self.assertEqual(reader.read_source(self.deploy)[1], "feature/user's")

    def test_valid_branch_and_repository_matrix(self):
        for branch in ['master', 'cloud', 'feature/cloud', '中文', 'v1.2', '#topic', 'true', "user's", '@']:
            self.assertTrue(reader.valid_branch(branch), branch)
            result = subprocess.run(['git', 'check-ref-format', '--branch', branch],
                                    cwd=self.deploy.parent, capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0, branch)
        for repo in ['git://git.lyoko.io/AzurLaneAutoScript', 'https://github.com/LmeSzinc/AzurLaneAutoScript',
                     'https://gitee.com/custom/repo', 'git@example.invalid:user/repo.git',
                     'ssh://git@example.invalid/user/repo.git', '/local/repo', 'file:///local/repo']:
            self.assertTrue(reader.valid_repository(repo), repo)

    def test_invalid_branch_matrix(self):
        for branch in ['', '-bad', 'a..b', 'a//b', '.a', 'a.lock', 'a/.b', 'a@{b', 'a b',
                       'a:b', 'a\\b', 'a~b', 'a^b', 'a?b', 'a*b', 'a[b', 'a\nb', 'a/', 'a.']:
            self.assertFalse(reader.valid_branch(branch), repr(branch))

    def test_invalid_repository_matrix(self):
        for repo in ['', '-x', 'ext::sh', 'https://a/repo\nBranch: injected', 'https://a/repo#fragment',
                     'https://user:secret@', 'https://a:99999/r', 'https://a/a b']:
            self.assertFalse(reader.valid_repository(repo), repr(repo))

    def test_ambiguous_missing_and_multiline_fields_fail_closed(self):
        for text in ['Repository: https://gitee.com/custom/r\n',
                     'Repository: https://gitee.com/custom/r\nBranch: cloud\nBranch: master\n',
                     'Repository: |\n  https://gitee.com/custom/r\nBranch: cloud\n',
                     'Repository: https://gitee.com/custom/r\nBranch: "cloud\n']:
            self.deploy.write_text(text, encoding='utf-8')
            with self.assertRaises(ValueError):
                reader.read_source(self.deploy)

    def test_unrelated_keys_are_not_git_configuration(self):
        self.write('https://gitee.com/custom/r', 'cloud')
        with self.deploy.open('a', encoding='utf-8') as stream:
            stream.write('Other:\n  Repository: unrelated\n  Branch: keep\n')
        self.assertEqual(reader.read_source(self.deploy), ('https://gitee.com/custom/r', 'cloud'))

    def test_other_blocks_and_literal_text_cannot_supply_git_keys(self):
        for text in [
            'Deploy:\n  Other:\n    Repository: https://example.invalid/r\n    Branch: cloud\n',
            'Deploy:\n  Note: |\n    Git:\n      Repository: https://example.invalid/r\n      Branch: cloud\n',
        ]:
            self.deploy.write_text(text, encoding='utf-8')
            with self.assertRaises(ValueError):
                reader.read_source(self.deploy)

    def test_unicode_escapes_from_yaml_editors_are_decoded(self):
        self.write('https://gitee.com/custom/r', '"\\u4e2d\\u6587"')
        self.assertEqual(reader.read_source(self.deploy)[1], '中文')

    def test_retired_writer_fails_without_mutation_or_secret_output(self):
        self.write('https://gitee.com/custom/r', 'cloud')
        before = self.deploy.read_bytes()
        config = self.deploy.parent / 'config'
        config.mkdir()
        (config / 'deploy.yaml').write_bytes(before)
        secret = 'FAKE_PRIVATE_SECRET'
        p = subprocess.run([sys.executable, str(SCRIPT), f'https://u:{secret}@example.invalid/r', 'master'],
                           env=dict(os.environ, ALASAOS_ALAS_ROOT=str(self.deploy.parent)),
                           capture_output=True, text=True, timeout=10)
        self.assertEqual(p.returncode, 2)
        self.assertEqual(p.stdout.strip(), 'FAILED deploy-config')
        self.assertNotIn(secret, p.stdout + p.stderr)
        self.assertEqual((config / 'deploy.yaml').read_bytes(), before)

    def test_invalid_utf8_returns_only_fixed_failure(self):
        self.deploy.write_bytes(b'\xffhttps://u:FAKE_PRIVATE_SECRET@example.invalid/r')
        p = subprocess.run([sys.executable, str(SCRIPT), '--read', str(self.deploy)],
                           capture_output=True, text=True, timeout=10)
        self.assertEqual(p.returncode, 2)
        self.assertEqual(p.stdout.strip(), 'FAILED deploy-config')
        self.assertEqual(p.stderr, '')


if __name__ == '__main__':
    unittest.main()

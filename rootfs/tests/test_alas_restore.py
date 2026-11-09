"""Offline image/restore regressions; disposable Git fixtures stay in .tmp."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'rootfs/build'))
from validate_trim import materialize_alas_tree, strip_alas_tree

SEED = ROOT / 'rootfs/seeds/alasaos_restore.sh'
PENDING = '.alasaos_restore_pending'
MARK = '.alasaos_restored'


def shell_path(path):
    value = str(path).replace('\\', '/')
    return '/' + value[0].lower() + value[2:] if len(value) > 1 and value[1] == ':' else value


class RestoreTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/restore-tests'
        base.mkdir(parents=True, exist_ok=True)
        tmp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.alas = self.root / 'opt/alas'
        self.alas.mkdir(parents=True)
        self.bash = os.environ.get('ALASAOS_TEST_BASH', shutil.which('bash') or '/bin/bash')
        self.env = dict(os.environ, GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
                        GIT_CEILING_DIRECTORIES=str(self.root),
                        GIT_TERMINAL_PROMPT='0', PYTHONDONTWRITEBYTECODE='1',
                        ALASAOS_ALAS_ROOT=shell_path(self.alas))
        self.env['PATH'] = str(Path(self.bash).parent) + os.pathsep + self.env['PATH']
        env_patch = patch.dict(os.environ, self.env)
        env_patch.start()
        self.addCleanup(env_patch.stop)
        self.files = {'alas.py': b'# entry\n', 'gui.py': b'# gui\n',
                      'module/config/config.py': b'# config\n',
                      'assets/a file.bin': b'\x00\xff\x10image payload'}

    def write_tree(self):
        for name, data in self.files.items():
            target = self.alas / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)

    def git(self, *args):
        return subprocess.run(['git', '-C', str(self.alas), *args], env=self.env,
                              check=True, capture_output=True, text=True, timeout=20).stdout.strip()

    def repository(self):
        self.write_tree()
        self.git('init', '-q', '-b', 'master')
        self.git('config', 'user.name', 'Restore Test')
        self.git('config', 'user.email', 'restore@example.invalid')
        self.git('config', 'core.autocrlf', 'false')
        self.git('add', '.')
        self.git('commit', '-qm', 'fixture')
        self.git('repack', '-ad')
        # The validator runs on Linux, where unlink ignores a file's read-only
        # bit. Match that behavior for disposable pack files on Windows.
        if os.name == 'nt':
            for pack in (self.alas / '.git/objects/pack').iterdir():
                pack.chmod(0o644)

    def restore(self):
        return subprocess.run([self.bash, shell_path(SEED)], env=self.env,
                              capture_output=True, text=True, timeout=30)

    def assert_tree(self):
        for name, data in self.files.items():
            self.assertEqual((self.alas / name).read_bytes(), data)

    def test_legacy_full_tree_without_git_can_start(self):
        self.write_tree()
        result = self.restore()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.alas / MARK).is_file())
        self.assert_tree()

    def test_legacy_failed_fetch_keeps_full_tree_usable(self):
        self.write_tree()
        self.git('init', '-q', '-b', 'master')
        result = self.restore()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.alas / MARK).is_file())
        self.assert_tree()

    def test_pack_image_with_invalid_head_must_not_use_legacy_fallback(self):
        self.write_tree()
        self.git('init', '-q', '-b', 'master')
        (self.alas / PENDING).write_text('git-pack-v1\n')
        result = self.restore()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.alas / MARK).exists())
        self.assertTrue((self.alas / PENDING).exists())

    def test_incomplete_tree_without_head_fails(self):
        (self.alas / 'alas.py').write_bytes(self.files['alas.py'])
        result = self.restore()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.alas / MARK).exists())

    def test_restore_never_discovers_a_parent_repository(self):
        parent_file = self.root / 'keep.txt'
        parent_file.write_text('original')
        for args in [('init', '-q'), ('add', 'keep.txt'),
                     ('-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'parent fixture')]:
            subprocess.run(['git', '-C', str(self.root), *args], env=self.env,
                           check=True, capture_output=True, timeout=20)
        parent_file.write_text('keep dirty parent')
        result = subprocess.run([self.bash, shell_path(SEED)],
                                env=dict(self.env, GIT_CEILING_DIRECTORIES=''),
                                capture_output=True, text=True, timeout=30)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(parent_file.read_text(), 'keep dirty parent')

    def test_old_git_image_gets_seed_before_strip_and_restores_offline(self):
        self.repository()
        config = self.alas / 'config/deploy.yaml'
        config.parent.mkdir()
        config.write_bytes(b'private fixture configuration\n')
        (self.alas / MARK).touch()  # A repacked tree must not ship a completed marker.
        strip_alas_tree(self.root)
        self.assertFalse((self.alas / 'alas.py').exists())
        self.assertFalse((self.alas / MARK).exists())
        self.assertTrue((self.alas / PENDING).is_file())
        self.assertEqual((self.alas / 'seeds/alasaos_restore.sh').read_bytes(), SEED.read_bytes())
        result = self.restore()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assert_tree()
        self.assertEqual(config.read_bytes(), b'private fixture configuration\n')
        self.assertTrue((self.alas / MARK).is_file())
        self.assertFalse((self.alas / PENDING).exists())
        self.assertIn('skip', self.restore().stdout)

    def test_interrupted_restore_with_sentinel_is_completed(self):
        self.repository()
        strip_alas_tree(self.root)
        (self.alas / 'alas.py').write_bytes(self.files['alas.py'])
        (self.alas / MARK).touch()  # Pending takes precedence over stale completion.
        result = self.restore()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assert_tree()
        self.assertFalse((self.alas / PENDING).exists())

    def test_validator_materialize_then_repack_keeps_pack_layout(self):
        self.repository()
        strip_alas_tree(self.root)
        materialize_alas_tree(self.root)
        self.assert_tree()
        strip_alas_tree(self.root)
        self.assertFalse((self.alas / 'alas.py').exists())
        self.assertTrue((self.alas / 'seeds/alasaos_restore.sh').is_file())
        self.assertEqual(self.restore().returncode, 0)
        self.assert_tree()

    def test_archive_without_git_remains_a_full_tree(self):
        self.write_tree()
        strip_alas_tree(self.root)
        self.assert_tree()
        self.assertFalse((self.alas / PENDING).exists())

    def test_invalid_git_image_is_not_stripped(self):
        self.write_tree()
        self.git('init', '-q', '-b', 'master')
        with self.assertRaises(subprocess.CalledProcessError):
            strip_alas_tree(self.root)
        self.assert_tree()


if __name__ == '__main__':
    unittest.main()

"""Runtime deploy reader / policy regressions, with no ALAS dependencies."""
import os
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class DeploySeedTests(unittest.TestCase):
    def test_default_config_is_seeded_only_when_no_user_config_remains(self):
        base = ROOT / '.tmp/deploy-seed-tests'
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=base) as tmp:
            config = Path(tmp) / 'config'
            config.mkdir()
            (config / 'template.json').write_text(json.dumps({'Alas': {'Emulator': {}}}))
            other = config / 'other.json'
            other.write_text('{"keep": true}')
            env = dict(os.environ, ALASAOS_ALAS_ROOT=tmp, PYTHONDONTWRITEBYTECODE='1')
            command = [sys.executable, '-S', str(ROOT / 'rootfs/seeds/seed_config.py')]
            subprocess.run(command, env=env, capture_output=True, check=True, timeout=10)
            self.assertFalse((config / 'alas.json').exists())
            self.assertEqual(other.read_text(), '{"keep": true}')
            other.unlink()
            subprocess.run(command, env=env, capture_output=True, check=True, timeout=10)
            seeded = json.loads((config / 'alas.json').read_text())
            self.assertEqual(seeded['Alas']['Emulator']['Serial'], 'alasaos')

    def test_policy_and_source_reader_preserve_user_settings_and_line_endings(self):
        base = ROOT / '.tmp/deploy-seed-tests'
        base.mkdir(parents=True, exist_ok=True)
        for newline in ['\n', '\r\n']:
            with self.subTest(newline=repr(newline)), tempfile.TemporaryDirectory(dir=base) as tmp:
                alas = Path(tmp)
                (alas / 'config').mkdir()
                deploy = alas / 'config/deploy.yaml'
                repo = 'https://test:FAKE_TEST_SECRET@example.invalid/repo'
                original = (f"# keep comment\nDeploy:\n  Git:\n    Repository: '{repo}' # keep inline\n"
                            '    Branch: cloud  # chosen branch\n'
                            '  Update:\n    EnableReload: false\n    CheckUpdateInterval: 0\n'
                            '  Webui:\n    WebuiPort: 22267\n    Password: retain-test-value\n')
                deploy.write_bytes(original.replace('\n', newline).encode())
                env = dict(os.environ, ALASAOS_ALAS_ROOT=str(alas), PYTHONDONTWRITEBYTECODE='1')
                policy = [sys.executable, '-S', str(ROOT / 'rootfs/seeds/seed_deploy.py')]
                reader = [sys.executable, '-S', str(ROOT / 'rootfs/seeds/sync_deploy.py'), '--read']
                p = subprocess.run(policy, env=env, capture_output=True, text=True, timeout=10)
                self.assertEqual(p.returncode, 0, p.stderr)
                self.assertNotIn('FAKE_TEST_SECRET', p.stdout + p.stderr)
                expected = original.replace('EnableReload: false', 'EnableReload: true')
                self.assertEqual(deploy.read_bytes(), expected.replace('\n', newline).encode())
                before = deploy.stat().st_mtime_ns
                p = subprocess.run(reader, env=env, capture_output=True, text=True, timeout=10)
                self.assertEqual(p.returncode, 0, p.stderr)
                self.assertEqual(p.stdout.splitlines(), [repo, 'cloud'])  # Private protocol, not a log.
                subprocess.run(policy, env=env, capture_output=True, check=True, timeout=10)
                self.assertEqual(deploy.stat().st_mtime_ns, before)
                self.assertEqual(deploy.read_bytes(), expected.replace('\n', newline).encode())

    def test_missing_deploy_is_not_created_and_reader_fails(self):
        base = ROOT / '.tmp/deploy-seed-tests'
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=base) as tmp:
            env = dict(os.environ, ALASAOS_ALAS_ROOT=tmp, PYTHONDONTWRITEBYTECODE='1')
            for script, args, code in [('seed_deploy.py', [], 0), ('sync_deploy.py', ['--read'], 2),
                                       ('sync_deploy.py', ['test-source', 'cloud'], 2)]:
                p = subprocess.run([sys.executable, '-S', str(ROOT / 'rootfs/seeds' / script), *args],
                                   env=env, capture_output=True, timeout=10)
                self.assertEqual(p.returncode, code)
            self.assertFalse((Path(tmp) / 'config/deploy.yaml').exists())

    def test_theme_env_updates_deploy_yaml(self):
        base = ROOT / '.tmp/deploy-seed-tests'
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=base) as tmp:
            alas = Path(tmp)
            (alas / 'config').mkdir()
            deploy = alas / 'config/deploy.yaml'
            deploy.write_text('Deploy:\n  Webui:\n    Theme: default\n')
            env = dict(os.environ, ALASAOS_ALAS_ROOT=str(alas), ALASAOS_THEME='dark', PYTHONDONTWRITEBYTECODE='1')
            subprocess.run([sys.executable, '-S', str(ROOT / 'rootfs/seeds/seed_deploy.py')], env=env, check=True, timeout=10)
            self.assertIn('Theme: dark', deploy.read_text())


if __name__ == '__main__':
    unittest.main()

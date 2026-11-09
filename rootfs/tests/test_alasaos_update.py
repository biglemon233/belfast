"""Offline updater regressions using real Git repos.

Run with Python unittest; on Windows set ALASAOS_TEST_BASH to Git Bash.
All test files live under the project .tmp directory. No remote network is used.
"""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SEEDS = ROOT / 'rootfs/seeds'


def shell_path(path):
    s = str(path).replace('\\', '/')
    return '/' + s[0].lower() + s[2:] if len(s) > 1 and s[1] == ':' else s


class UpdateTests(unittest.TestCase):
    def setUp(self):
        base = ROOT / '.tmp/updater-tests'
        base.mkdir(parents=True, exist_ok=True)
        self.tmp = tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.origin = self.root / 'origin'
        self.alas = self.root / 'alas'
        self.bin = self.root / 'bin'
        for p in (self.origin, self.alas, self.bin):
            p.mkdir()
        self.env = dict(os.environ, GIT_CONFIG_NOSYSTEM='1',
                        GIT_CEILING_DIRECTORIES=str(self.root),
                        GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0',
                        ALASAOS_UPDATE_NO_CDN='1', ALASAOS_UPDATE_TIMEOUT='5',
                        ALASAOS_ALAS_ROOT=shell_path(self.alas), PYTHONDONTWRITEBYTECODE='1')
        self.git(self.origin, 'init', '-q', '-b', 'master')
        self.git(self.origin, 'config', 'user.name', 'Updater Test')
        self.git(self.origin, 'config', 'user.email', 'updater@example.invalid')
        (self.origin / 'payload').write_text('one\n')
        self.git(self.origin, 'add', 'payload')
        self.git(self.origin, 'commit', '-qm', 'fixture')
        self.sha = self.git(self.origin, 'rev-parse', 'HEAD')
        self.git(self.origin, 'branch', 'zenko')
        (self.alas / 'seeds').mkdir()
        (self.alas / 'config').mkdir()
        for name in ['alasaos_update.sh', 'sync_deploy.py', 'update_progress.py']:
            (self.alas / 'seeds' / name).write_bytes((SEEDS / name).read_bytes().replace(b'\r\n', b'\n'))
        (self.alas / 'config/deploy.yaml').write_text('Deploy:\n  Git:\n    Repository: old\n    Branch: master\n  Update:\n    EnableReload: true\n')
        # Deterministic faults, real git for everything else. No private credentials.
        real_git = shell_path(shutil.which('git'))
        (self.bin / 'git').write_text(
            '#!/bin/bash\n'
            'if [[ "$1" == ls-remote && -n "${FAIL_PROBE:-}" ]]; then exit 1; fi\n'
            'if [[ "$1" == fetch && -n "${FAIL_FETCH:-}" ]]; then echo "fatal: test failure" >&2; exit 1; fi\n'
            f'exec "{real_git}" "$@"\n', newline='\n')
        # Keep the real progress wrapper; Windows requires Bash to execute our Git fault stub.
        progress_child = (
            'if [[ "$1" == seeds/update_progress.py ]]; then\n'
            '  shift 2\n'
            f'  exec "{shell_path(sys.executable)}" "${{python_flags[@]}}" seeds/update_progress.py -- "$ALASAOS_TEST_BASH" "{shell_path(self.bin / "git")}" "${{@:2}}"\n'
            'fi\n'
        ) if os.name == 'nt' else ''
        (self.bin / 'python3').write_text(
            '#!/bin/bash\npython_flags=()\n'
            'if [[ "${1:-}" == -S ]]; then python_flags+=("$1"); shift; fi\n'
            + progress_child + f'exec "{shell_path(sys.executable)}" "${{python_flags[@]}}" "$@"\n', newline='\n')
        for p in self.bin.iterdir():
            p.chmod(0o755)

    def git(self, cwd, *args):
        p = subprocess.run(['git', '-C', str(cwd), *args], env=self.env,
                           text=True, capture_output=True, timeout=15)
        if p.returncode:
            raise AssertionError(p.stderr)
        return p.stdout.strip()

    def update(self, source=None, branch='zenko', **env):
        # Simulate one settings save. The updater must consume deploy, not old argv/env.
        repo = (source or shell_path(self.origin)).replace("'", "''")
        deploy = self.alas / 'config/deploy.yaml'
        deploy.write_bytes((f"# retain\r\nDeploy:\r\n  Git:\r\n    Repository: '{repo}'  # private\r\n"
                            f"    Branch: '{branch}'  # branch\r\n  Update:\r\n    EnableReload: true\r\n").encode())
        before = deploy.read_bytes()
        p = self.run_update(**env)
        self.assertEqual(deploy.read_bytes(), before, 'Updater must never rewrite deploy')
        return p

    def run_update(self, **env):
        bash = os.environ.get('ALASAOS_TEST_BASH', shutil.which('bash') or '/bin/bash')
        script = f'export PATH="{shell_path(self.bin)}:$PATH"; exec /bin/bash "$1" "$2" "$3"'
        return subprocess.run([bash, '-c', script, 'updater-test', shell_path(self.alas / 'seeds/alasaos_update.sh'),
                               'https://stale:FAKE_STALE_SECRET@example.invalid/old', 'stale-branch'],
                              env=dict(self.env, ALASAOS_UPDATE_REPO='https://stale.invalid/old',
                                       ALASAOS_UPDATE_BRANCH='stale-branch', **env),
                              capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=25)

    def test_first_install_matching_manifest_still_populates_git(self):
        (self.alas / 'BUILD_MANIFEST').write_text('{"alas_commit": "' + self.sha + '"}')
        p = self.update()
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(self.git(self.alas, 'rev-parse', '--verify', 'HEAD'), self.sha)
        self.assertTrue((self.alas / 'payload').exists())

    def test_second_start_keeps_upstream_history(self):
        self.assertEqual(self.update().returncode, 0)
        self.assertEqual(self.update().returncode, 0)
        self.assertEqual(self.git(self.alas, 'rev-parse', '--verify', 'origin/zenko'), self.sha)

    def baked_repository(self):
        self.git(self.alas, 'init', '-q', '-b', 'master')
        self.git(self.alas, 'remote', 'add', 'origin', shell_path(self.origin))
        self.git(self.alas, 'fetch', '--depth', '1', 'origin', 'master')
        self.git(self.alas, 'checkout', '-q', '-B', 'master', 'FETCH_HEAD')

    def test_baked_same_source_and_head_skip_first_fetch(self):
        self.baked_repository()
        p = self.update(branch='master', FAIL_FETCH='1')
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertIn('UNCHANGED ' + self.sha, p.stdout)
        self.assertNotIn('PHASE fetch', p.stdout)
        self.assertEqual((self.alas / '.alasaos_update_source').read_text().strip(),
                         hashlib.sha256((shell_path(self.origin) + '\nmaster').encode()).hexdigest())

    def test_baked_official_github_base_uses_domestic_cdn_on_first_start(self):
        self.baked_repository()
        self.git(self.alas, 'remote', 'set-url', 'origin',
                 'https://github.com/LmeSzinc/AzurLaneAutoScript.git')
        (self.alas / 'seeds/cdn_update.py').write_text('print("UPTODATE")\n')
        p = self.update(source='git://git.lyoko.io/AzurLaneAutoScript', branch='master',
                        ALASAOS_UPDATE_NO_CDN='', FAIL_FETCH='1', FAIL_PROBE='1')
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertIn('UNCHANGED ' + self.sha + ' (cdn)', p.stdout)
        self.assertEqual(self.git(self.alas, 'rev-parse', 'origin/master'), self.sha)

    def test_unknown_baked_source_does_not_bootstrap_official_cdn(self):
        self.baked_repository()
        (self.alas / 'seeds/cdn_update.py').write_text('print("UPTODATE")\n')
        p = self.update(source='git://git.lyoko.io/AzurLaneAutoScript', branch='master',
                        ALASAOS_UPDATE_NO_CDN='', FAIL_FETCH='1', FAIL_PROBE='1')
        self.assertEqual(p.returncode, 1, p.stdout)
        self.assertIn('PHASE source-changed', p.stdout)
        self.assertIn('FAILED fetch', p.stdout)

    def test_baked_branch_change_still_checks_out_selected_branch(self):
        self.baked_repository()
        p = self.update(branch='zenko')
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertIn('UPDATED ' + self.sha, p.stdout)
        self.assertEqual(self.git(self.alas, 'symbolic-ref', '--short', 'HEAD'), 'zenko')

    def test_changed_branch_at_same_commit_checks_out_branch(self):
        self.assertEqual(self.update(branch='master').returncode, 0)
        self.assertEqual(self.update(branch='zenko').returncode, 0)
        self.assertEqual(self.git(self.alas, 'symbolic-ref', '--short', 'HEAD'), 'zenko')
        self.assertEqual(self.git(self.alas, 'rev-parse', '--verify', 'origin/zenko'), self.sha)

    def test_failed_update_retries_on_every_start_and_ignores_old_marker(self):
        self.assertEqual(self.update(FAIL_PROBE='1', FAIL_FETCH='1').returncode, 1)
        marker = self.alas / '.alasaos_update_fail_date'
        marker.write_text('2099-01-01\n')
        p = self.update(FAIL_PROBE='1', FAIL_FETCH='1')
        self.assertEqual(p.returncode, 1, p.stdout)
        self.assertIn('PHASE fetch', p.stdout)
        self.assertFalse(marker.exists())
        p = self.update()
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertNotIn('backoff-until-tomorrow', p.stdout)

    def test_probe_failure_falls_back_to_fetch(self):
        p = self.update(FAIL_PROBE='1')
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(self.git(self.alas, 'rev-parse', 'HEAD'), self.sha)

    def test_source_secret_not_written_to_log_or_marker(self):
        secret = 'FAKE_TEST_SECRET'
        p = self.update(source=f'https://test:{secret}@example.invalid/repo', FAIL_PROBE='1', FAIL_FETCH='1')
        self.assertNotIn(secret, p.stdout + p.stderr)
        marker = self.alas / '.alasaos_update_source'
        self.assertTrue(marker.exists())
        self.assertNotIn(secret, marker.read_text())

    def test_stale_commit_marker_does_not_skip_checkout(self):
        self.assertEqual(self.update().returncode, 0)
        (self.origin / 'payload').write_text('two\n')
        self.git(self.origin, 'commit', '-qam', 'second')
        new = self.git(self.origin, 'rev-parse', 'HEAD')
        self.git(self.origin, 'branch', '-f', 'zenko', new)
        (self.alas / '.alasaos_alas_commit').write_text(new)
        self.assertEqual(self.update().returncode, 0)
        self.assertEqual(self.git(self.alas, 'rev-parse', 'HEAD'), new)

    def test_same_sha_source_switch_retains_origin_refs_and_git_directory(self):
        self.assertEqual(self.update(branch='master').returncode, 0)
        sentinel = self.alas / '.git/aos-test-sentinel'
        sentinel.write_text('do not rebuild git')
        # A second local URL is enough to prove source fingerprint changes at the same SHA.
        second = self.root / 'second-origin'
        self.git(self.root, 'clone', '-q', str(self.origin), str(second))
        p = self.update(source=shell_path(second), branch='master')
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertIn('UPDATED ' + self.sha, p.stdout)
        self.assertEqual(shell_path(self.git(self.alas, 'remote', 'get-url', 'origin')), shell_path(second))
        self.assertEqual(self.git(self.alas, 'rev-parse', 'origin/master'), self.sha)
        self.assertTrue(sentinel.is_file())

    def test_cloud_and_master_round_trip_at_same_sha(self):
        self.git(self.origin, 'branch', 'cloud')
        for branch in ('master', 'cloud', 'master'):
            p = self.update(branch=branch)
            self.assertEqual(p.returncode, 0, p.stdout)
            self.assertEqual(self.git(self.alas, 'symbolic-ref', '--short', 'HEAD'), branch)
            self.assertEqual(self.git(self.alas, 'rev-parse', 'origin/' + branch), self.sha)
            self.assertNotIn('FAKE_STALE_SECRET', p.stdout + p.stderr)

    def test_missing_or_invalid_deploy_fails_without_git_or_backoff(self):
        deploy = self.alas / 'config/deploy.yaml'
        deploy.unlink()
        p = self.run_update()
        self.assertEqual(p.returncode, 1)
        self.assertEqual(p.stdout.strip(), 'FAILED deploy-config')
        deploy.write_text('Repository: https://test:FAKE_SECRET@example.invalid/r\nBranch: bad..branch\n')
        p = self.run_update()
        self.assertEqual(p.returncode, 1)
        self.assertEqual(p.stdout.strip(), 'FAILED deploy-config')
        self.assertEqual(p.stderr, '')
        self.assertFalse((self.alas / '.git').exists())
        self.assertFalse((self.alas / '.alasaos_update_fail_date').exists())

    def test_proxy_env_reaches_git_only_for_this_process_and_no_proxy_survives(self):
        real_git = shell_path(shutil.which('git'))
        marker = self.root / 'proxy-observed'
        # Fixed marker only; never dump environment values to logs.
        (self.bin / 'git').write_text(
            '#!/bin/bash\n'
            'if [[ "$1" == fetch ]]; then\n'
            ' [[ "$http_proxy" == "http://proxy.invalid:8080" && "$https_proxy" == "$http_proxy" ]] || exit 1\n'
            ' [[ "$NO_PROXY" == "localhost,127.0.0.1,keep.invalid" ]] || exit 1\n'
            f' printf ok > "{shell_path(marker)}"\n'
            'fi\n'
            f'exec "{real_git}" "$@"\n', newline='\n')
        parent = dict(os.environ)
        p = self.update(http_proxy='http://proxy.invalid:8080', https_proxy='http://proxy.invalid:8080',
                        NO_PROXY='localhost,127.0.0.1,keep.invalid')
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertEqual(marker.read_text(), 'ok')
        self.assertNotIn('proxy.invalid', p.stdout + p.stderr)
        self.assertEqual(dict(os.environ), parent)

    def test_cdn_raw_output_and_invalid_verdict_are_never_printed(self):
        self.assertEqual(self.update(branch='master').returncode, 0)
        domestic = 'git://git.lyoko.io/AzurLaneAutoScript'
        # Map the official URL to a local fixture: absolutely no network/CDN access.
        self.git(self.alas, 'config', f'url.{shell_path(self.origin)}.insteadOf', domestic)
        (self.alas / '.alasaos_update_source').write_text(
            hashlib.sha256((domestic + '\nmaster').encode()).hexdigest())
        (self.alas / 'seeds/cdn_update.py').write_text(
            'print("raw https://user:FAKE_CDN_SECRET@example.invalid/repo")\n'
            'print("PACK_READY FAKE_CDN_SECRET")\n')
        p = self.update(source=domestic, branch='master', ALASAOS_UPDATE_NO_CDN='')
        self.assertEqual(p.returncode, 0, p.stdout)
        self.assertIn('PHASE cdn-fallback', p.stdout)
        self.assertNotIn('FAKE_CDN_SECRET', p.stdout + p.stderr)
        self.assertIn('UNCHANGED ' + self.sha, p.stdout)

    def test_env_fix_restores_retired_webui_overlay_from_current_fork(self):
        rel = 'module/webui/utils.py'
        source = self.origin / rel
        source.parent.mkdir(parents=True)
        source.write_text('class Icon:\n    STOP = "fork-stop"\n')
        self.git(self.origin, 'add', rel)
        self.git(self.origin, 'commit', '-qm', 'fork icon')
        self.git(self.origin, 'branch', '-f', 'zenko', 'HEAD')
        self.assertEqual(self.update().returncode, 0)
        (self.alas / rel).write_text('class Icon:\n    RUN = "old"\n')
        config_before = (self.alas / 'config/deploy.yaml').read_bytes()
        # Avoid importing/installing any host Python dependency in this test.
        imports = self.root / 'imageio-imports'
        (self.bin / 'python3').write_text(
            f'#!/bin/bash\nprintf . >> "{shell_path(imports)}"\necho 2.27.0\n', newline='\n')
        bash = os.environ.get('ALASAOS_TEST_BASH', shutil.which('bash') or '/bin/bash')
        p = subprocess.run([bash, '-c', f'export PATH="{shell_path(self.bin)}:$PATH"; exec /bin/bash "$1"',
                            'seed-test', shell_path(SEEDS / 'env_fix.sh')], env=self.env,
                           capture_output=True, text=True, timeout=25)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual((self.alas / rel).read_bytes(), source.read_bytes())
        self.assertEqual((self.alas / 'config/deploy.yaml').read_bytes(), config_before)
        self.assertEqual(imports.read_text(), '.', 'An already valid imageio needs only one import')


if __name__ == '__main__':
    unittest.main()

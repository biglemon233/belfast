#!/usr/bin/env python3
"""Validate every trim group in a disposable Linux ARM64 rootfs (run as root)."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess

from trim_rootfs import GROUPS, clean_name, removal_group, trim_archive


def unpack_archive(archive, root):
    # Standard builds pass a plain tar; artifact validation passes tar.xz.
    # tar detects compression on extraction; forcing -J rejects the build tar.
    subprocess.run(['tar', '-xf', str(archive.resolve()), '-C', str(root)], check=True)


def materialize_alas_tree(root):
    """First-boot parity: the shipped image keeps tracked ALAS files only as git
    objects. Materialize them from the pack exactly as seeds/alasaos_restore.sh
    does on device, so the runtime smoke sees the real post-restore tree.
    Legacy archives without .git already carry the full tree (no-op)."""
    alas = root / 'opt/alas'
    git_dir = alas / '.git'
    if not (git_dir / 'objects' / 'pack').is_dir():
        return
    # reset --hard rewrites .git metadata (index stat cache with fresh mtimes,
    # reflog timestamps, ORIG_HEAD), which would make otherwise identical trees
    # compare unequal across roots; restore the archive's .git afterwards so
    # tree_identity stays deterministic while the worktree stays materialized.
    pristine = root / '.aos-git-pristine'
    shutil.copytree(git_dir, pristine)
    try:
        subprocess.run(['git', '-C', str(alas), '-c', 'safe.directory=*',
                        '--git-dir=.git', '--work-tree=.',
                        'reset', '--hard', '--quiet'], check=True)
    finally:
        shutil.rmtree(git_dir)
        pristine.rename(git_dir)


def strip_alas_tree(root):
    """Inverse of materialize_alas_tree and of the image-side strip in
    build-rootfs.sh: remove git-tracked working-tree files (pack + untracked AOS
    assets stay). Validators that repack a materialized root must strip first,
    otherwise the repacked artifact would carry the full tree again and silently
    undo the image-side slimming. Install the current restore seed and pending
    marker even for old full-tree Git archives. Archives without .git keep their tree."""
    alas = root / 'opt/alas'
    if not (alas / '.git' / 'objects' / 'pack').is_dir():
        return
    git = ['git', '-C', str(alas), '-c', 'safe.directory=*', '--git-dir=.git', '--work-tree=.']
    subprocess.run(git + ['rev-parse', '--verify', 'HEAD^{commit}'],
                   stdout=subprocess.DEVNULL, check=True)
    metadata = ['seeds/alasaos_restore.sh', '.alasaos_restored', '.alasaos_restore_pending']
    tracked = subprocess.run(git + ['ls-files', '-z', '--', *metadata],
                             stdout=subprocess.PIPE, check=True).stdout
    if tracked:
        raise ValueError('Restore metadata collides with tracked ALAS files')
    seed = alas / metadata[0]
    seed.parent.mkdir(parents=True, exist_ok=True)
    seed.write_bytes((Path(__file__).resolve().parents[1] / metadata[0]).read_bytes())
    seed.chmod(0o755)
    (alas / '.alasaos_restored').unlink(missing_ok=True)
    (alas / '.alasaos_restore_pending').write_text('git-pack-v1\n', encoding='utf-8')
    subprocess.run(
        ['bash', '-c',
         'set -euo pipefail; cd "$1"; '
         'git -c safe.directory=* --git-dir=.git --work-tree=. ls-files -z | xargs -0 -r rm -f --; '
         'find . -depth -type d -empty -not -path "./.git" -not -path "./.git/*" -delete',
         'strip-alas-tree', str(alas)], check=True)


def tree_identity(root):
    result = {}
    for directory, dirs, files in os.walk(root, followlinks=False):
        for name in dirs + files:
            p = Path(directory) / name
            mode = p.lstat().st_mode
            key = p.relative_to(root).as_posix()
            if p.is_symlink():
                content = os.readlink(p)
            elif stat.S_ISREG(mode):
                with p.open('rb') as f:
                    content = hashlib.file_digest(f, 'sha256').hexdigest()
            else:
                content = ''
            result[key] = (mode, content)
    return result


def apply_group(root, plan, group):
    root = root.resolve()
    count = size = 0
    for item in plan['removed']:
        if item['group'] != group:
            continue
        name = clean_name(item['path'])
        if removal_group(name) != group:
            raise ValueError('Plan does not match the current removal policy')
        p = root / name
        # Resolve the parent only: unlinking a symlink must never follow it.
        if not p.parent.resolve().is_relative_to(root) or (p.is_dir() and not p.is_symlink()):
            raise ValueError(f'Unsafe removal target: {name}')
        p.unlink()
        count += 1
        size += item['bytes']
    return {'group': group, 'files': count, 'removed_bytes': size}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', required=True, type=Path)
    parser.add_argument('--expected-sha256', required=True)
    parser.add_argument('--work', required=True, type=Path)
    parser.add_argument('--dist', required=True, type=Path)
    parser.add_argument('--plain-tar', action='store_true',
                        help='Validated intermediate for native slimming; compress only the final image')
    parser.add_argument('--no-trim', action='store_true', help='Validate and package the complete amd64 image')
    args = parser.parse_args()
    if os.name != 'posix' or os.geteuid() != 0 or os.uname().machine not in ('aarch64', 'x86_64'):
        parser.error('Use a disposable native Linux 64-bit runner with root privileges')
    work, dist = args.work.resolve(), args.dist.resolve()
    root = work / 'rootfs'
    if root.exists():
        parser.error('Work rootfs already exists; inspect the interrupted run first')
    dist.mkdir(parents=True, exist_ok=True)
    plan = trim_archive(args.input)
    if plan['input']['sha256'] != args.expected_sha256:
        parser.error('Baseline SHA does not match the requested artifact')
    (dist / 'TRIM_PLAN.json').write_text(json.dumps(plan, indent=2), encoding='utf-8')
    root.mkdir(parents=True)
    unpack_archive(args.input, root)
    materialize_alas_tree(root)
    protected = tree_identity(root / 'opt/alas')
    stage = root / 'aos-validation'
    stage.mkdir()
    shutil.copyfile(Path(__file__).with_name('runtime_smoke.py'), stage / 'runtime_smoke.py')
    mounted = []
    reports = []
    try:
        for source in ('/dev', '/proc'):
            target = root / source.lstrip('/')
            if target.is_symlink() or not target.resolve().is_relative_to(root):
                raise ValueError('Unsafe mountpoint')
            target.mkdir(exist_ok=True)
            subprocess.run(['mount', '--bind', source, str(target)], check=True)
            mounted.append(target)
        baseline = None
        for group in ('baseline', *(() if args.no_trim else GROUPS)):
            action = {'group': group}
            if group != 'baseline':
                action.update(apply_group(root, plan, group))
            command = ['chroot', str(root), '/usr/bin/env', '-i',
                       'PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
                       'HOME=/aos-validation', 'LANG=C.UTF-8', 'PYTHONDONTWRITEBYTECODE=1',
                       'OPENBLAS_NUM_THREADS=1', 'OMP_NUM_THREADS=1', 'MXNET_CPU_WORKER_NTHREADS=1',
                       'python3', '/aos-validation/runtime_smoke.py',
                       '--output', '/aos-validation/result.json']
            if args.no_trim:
                command.append('--basic-codecs')
            with (dist / f'smoke-{group}.log').open('wb') as log:
                try:
                    subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True, timeout=300)
                except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
                    log.flush()
                    print((dist / f'smoke-{group}.log').read_text(encoding='utf-8', errors='replace')[-16000:], flush=True)
                    raise
            result = json.loads((stage / 'result.json').read_text(encoding='utf-8'))
            if baseline is None:
                baseline = result
            elif result != baseline:
                raise ValueError(f'Runtime/OCR result changed after {group}')
            if tree_identity(root / 'opt/alas') != protected:
                raise ValueError(f'ALAS tree changed during {group}')
            action.update(runtime_matches_baseline=True, alas_unchanged=True, result=result)
            reports.append(action)
            (dist / 'VALIDATION.json').write_text(json.dumps(reports, indent=2), encoding='utf-8')
            print('VALIDATED', group, flush=True)
    finally:
        for target in reversed(mounted):
            subprocess.run(['umount', str(target)], check=True)
    output = dist / ('rootfs.tar' if args.plain_tar else 'rootfs.tar.xz')
    if args.no_trim:
        if args.plain_tar:
            shutil.copyfile(args.input, output)
        else:
            with output.open('wb') as stream:
                subprocess.run(['xz', '-T2', '-8', '-c', str(args.input)], stdout=stream, check=True)
        report = {'trimmed': False, 'output': {'bytes': output.stat().st_size,
                  'sha256': hashlib.sha256(output.read_bytes()).hexdigest()}}
    else:
        report = trim_archive(args.input, output, compress=not args.plain_tar)
        verification = trim_archive(output)
        if verification['alas_tree_sha256'] != report['alas_tree_sha256'] or verification['removed']:
            raise ValueError('Final archive integrity or trim idempotence failed')
    (dist / 'TRIM_REPORT.json').write_text(json.dumps(report, indent=2), encoding='utf-8')
    shutil.copyfile(root / 'opt/alas/BUILD_MANIFEST', dist / 'BUILD_MANIFEST')
    print('TRIM_VALIDATION_OK', json.dumps(report['output']), flush=True)


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Compare a real replacement against an unchanged ARM64 rootfs before packaging."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import shutil
import subprocess

from profile_bundle import identity
from slim_opencv_backends import slim
from trim_rootfs import trim_archive
from validate_trim import materialize_alas_tree, strip_alas_tree, tree_identity, unpack_archive


def compress_controls(raw_archives, dist):
    # Separate tar inputs and outputs; liblzma releases the GIL. Preserve both
    # preset-8 controls and their exact bytes while using two runner cores.
    with ThreadPoolExecutor(max_workers=2) as pool:
        pending = {name: pool.submit(trim_archive, raw, dist / f'{name}.tar.xz',
                                     groups=(), xz_preset=8)
                   for name, raw in raw_archives.items()}
        return {name: future.result() for name, future in pending.items()}


def run_smoke(root, output):
    stage = root / 'aos-validation'
    stage.mkdir()
    shutil.copyfile(Path(__file__).with_name('runtime_smoke.py'), stage / 'runtime_smoke.py')
    mounted = []
    try:
        for source in ('/dev', '/proc'):
            target = root / source.lstrip('/')
            if target.is_symlink() or not target.resolve().is_relative_to(root):
                raise ValueError('Unsafe mountpoint')
            target.mkdir(exist_ok=True)
            subprocess.run(['mount', '--bind', source, str(target)], check=True)
            mounted.append(target)
        command = ['chroot', str(root), '/usr/bin/env', '-i',
                   'PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
                   'HOME=/aos-validation', 'LANG=C.UTF-8', 'PYTHONDONTWRITEBYTECODE=1',
                   'OPENBLAS_NUM_THREADS=1', 'OMP_NUM_THREADS=1', 'MXNET_CPU_WORKER_NTHREADS=1',
                   'LD_BIND_NOW=1', 'python3', '/aos-validation/runtime_smoke.py',
                   '--output', '/aos-validation/result.json']
        with output.with_suffix('.log').open('wb') as log:
            subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True, timeout=300)
        result = json.loads((stage / 'result.json').read_text())
        output.write_text(json.dumps(result, indent=2) + '\n')
        return result
    finally:
        for target in reversed(mounted):
            subprocess.run(['umount', str(target)], check=True)
        # This fixed directory was created above and contains only test output.
        if stage.exists() and stage.resolve().parent == root and not stage.is_symlink():
            shutil.rmtree(stage)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--expected-sha256', required=True)
    parser.add_argument('--replacement', type=Path, required=True)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--dist', type=Path, required=True)
    args = parser.parse_args()
    if os.name != 'posix' or os.geteuid() != 0 or os.uname().machine != 'aarch64':
        parser.error('Use a native Linux ARM64 runner with root privileges')
    if identity(args.input)['sha256'] != args.expected_sha256:
        parser.error('Baseline archive SHA256 does not match')
    work, dist = args.work.resolve(), args.dist.resolve()
    if work.exists() or dist.exists() or work.is_relative_to(dist) or dist.is_relative_to(work):
        parser.error('Work and dist must be fresh, separate directories')
    work.mkdir(parents=True)
    dist.mkdir(parents=True)
    roots = {name: work / name for name in ('baseline', 'candidate')}
    for root in roots.values():
        root.mkdir()
        unpack_archive(args.input, root)
        materialize_alas_tree(root)
    # The source/Ubuntu patch audit was done for this exact distribution build.
    # A later vendor revision needs a new audit, even if its SONAME is unchanged.
    packages = (roots['baseline'] / 'var/lib/dpkg/status').read_text(encoding='utf-8').split('\n\n')
    package = next((block for block in packages
                    if block.startswith('Package: libopencv-imgcodecs406t64\n')), '')
    if '\nVersion: 4.6.0+dfsg-13.1ubuntu1\n' not in package:
        raise ValueError('Unreviewed OpenCV distribution version')
    protected = tree_identity(roots['baseline'] / 'opt/alas')
    if tree_identity(roots['candidate'] / 'opt/alas') != protected:
        raise ValueError('Unpacked ALAS trees differ')
    plan = slim(roots['candidate'], args.replacement, apply=True)
    (dist / 'NATIVE_PLAN.json').write_text(json.dumps(plan, indent=2) + '\n')
    baseline = run_smoke(roots['baseline'], dist / 'runtime-baseline.json')
    candidate = run_smoke(roots['candidate'], dist / 'runtime-candidate.json')
    if candidate != baseline:
        raise ValueError('Runtime, native image decoding or original OCR changed')
    for root in roots.values():
        if tree_identity(root / 'opt/alas') != protected:
            raise ValueError('ALAS tree changed during validation')
    # Identical compressor and tar metadata for the control and candidate.
    # The roots hold materialized ALAS trees (smoke parity); strip them back to
    # the shipped image layout before repacking, or the candidate would re-bake
    # the full tree and silently undo the image-side slimming.
    for root in roots.values():
        strip_alas_tree(root)
    raw_archives = {}
    for name, root in roots.items():
        raw = work / f'{name}.tar'
        subprocess.run(['tar', '--sort=name', '-cf', str(raw), '-C', str(root), '.'], check=True)
        raw_archives[name] = raw
    archives = {}
    for name, report in compress_controls(raw_archives, dist).items():
        (dist / f'{name}-archive.json').write_text(json.dumps(report, indent=2) + '\n')
        archives[name] = report['output']
    saved = archives['baseline']['bytes'] - archives['candidate']['bytes']
    if saved <= 0:
        raise ValueError('Candidate has no measured compressed saving')
    (dist / 'VALIDATION.json').write_text(json.dumps({
        'runtime_matches_baseline': True, 'alas_unchanged': True,
        'baseline': archives['baseline'], 'candidate': archives['candidate'],
        'compressed_saved_bytes': saved,
    }, indent=2) + '\n')
    shutil.copyfile(roots['candidate'] / 'opt/alas/BUILD_MANIFEST', dist / 'BUILD_MANIFEST')
    print('SLIM_OPENCV_VALIDATED', saved, 'compressed bytes saved', flush=True)


if __name__ == '__main__':
    main()

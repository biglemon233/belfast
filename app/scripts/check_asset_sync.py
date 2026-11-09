#!/usr/bin/env python3
"""Check the rootfs and APK copies, including runtime seeds (standard library only)."""
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
ASSETS = ROOT / 'app/app/src/main/assets/alas'


def files(root, exclude=()):
    return {
        p.relative_to(root).as_posix(): p
        for p in root.rglob('*')
        if p.is_file() and '__pycache__' not in p.parts and p.suffix != '.pyc'
        and not any(p.relative_to(root).as_posix() == name
                    or p.relative_to(root).as_posix().startswith(name + '/')
                    for name in exclude)
    }


def compare(source, target, source_exclude=(), target_exclude=()):
    if not source.is_dir() or not target.is_dir():
        return ['Missing source or target directory']
    left, right = files(source, source_exclude), files(target, target_exclude)
    errors = []
    for name in sorted(left.keys() | right.keys()):
        if name not in left:
            errors.append(f'Only in APK: {name}')
        elif name not in right:
            errors.append(f'Only in rootfs: {name}')
        elif left[name].read_bytes() != right[name].read_bytes():
            errors.append(f'Content differs: {name}')
    return errors


def main():
    pairs = [
        ('overlay', ROOT / 'rootfs/overlays', ASSETS / 'overlay', (), ('seeds',)),
        ('patches', ROOT / 'rootfs/patches', ASSETS / 'patches', (), ()),
        # deploy.yaml is baked into config/ separately, not copied by AlasOverlay.
        ('seeds', ROOT / 'rootfs/seeds', ASSETS / 'overlay/seeds', ('deploy.yaml',), ()),
    ]
    failed = False
    for label, source, target, source_exclude, target_exclude in pairs:
        errors = compare(source, target, source_exclude, target_exclude)
        for error in errors:
            print(f'{label}: {error}')
        if not errors:
            print(f'{label}: identical')
        failed |= bool(errors)
    return int(failed)


if __name__ == '__main__':
    raise SystemExit(main())

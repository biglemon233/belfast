#!/usr/bin/env python3
"""Validate release inputs and generate user-facing notes and asset checksums."""
import argparse
import hashlib
from pathlib import Path
import re
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def release_notes(changelog, tag):
    sections = re.split(r'(?=^## v)', changelog, flags=re.MULTILINE)
    matches = [s for s in sections if re.match(rf'^## {re.escape(tag)}(?:\s|[（(]|$)', s)]
    if len(matches) != 1:
        raise ValueError('CHANGELOG must contain exactly one section for this release')
    return matches[0].strip() + '\n'


def digest(stream):
    return hashlib.file_digest(stream, 'sha256').hexdigest()


def prepare(tag, version, changelog, apk_dir, rootfs_dir, output, notes_override=None, abis=('arm64-v8a',)):
    if tag != f'v{version}' or not re.fullmatch(r'v\d+\.\d+\.\d+', tag):
        raise ValueError('Release tag must match app.versionName')
    files = []
    for abi in abis:
        if abi not in ('arm64-v8a', 'x86_64'):
            raise ValueError('Unsupported release ABI')
        apks = [apk_dir / f'ALAS-AOS-v{version}-{abi}-full.apk',
                apk_dir / f'ALAS-AOS-v{version}-universal-lite.apk']
        suffix = '-amd64' if abi == 'x86_64' else ''
        archive, manifest = rootfs_dir / f'rootfs{suffix}.tar.xz', rootfs_dir / f'BUILD_MANIFEST{suffix}'
        inputs = [*apks, archive, manifest]
        if not all(p.is_file() and p.stat().st_size > 0 for p in inputs):
            raise ValueError('Both APK editions, runtime archive and manifest are required for ' + abi)
        with archive.open('rb') as stream:
            rootfs_sha = digest(stream)
        with zipfile.ZipFile(apks[0]) as full, zipfile.ZipFile(apks[1]) as lite:
            if any(n.startswith('assets/rootfs/') for n in lite.namelist()):
                raise ValueError('Lite APK must not bundle runtime assets')
            with full.open('assets/rootfs/rootfs.tar.xz') as stream:
                if digest(stream) != rootfs_sha:
                    raise ValueError('Full APK runtime differs from the release archive')
            if full.read('assets/rootfs/BUILD_MANIFEST') != manifest.read_bytes():
                raise ValueError('Full APK manifest differs from the release manifest')
        files.extend(inputs)
    notes = release_notes(changelog, tag) if notes_override is None else notes_override.strip()
    if not notes:
        raise ValueError('Release notes must not be empty')
    notes += ('\n- Full：内置运行环境，适合离线部署。\n'
              '- Lite：小安装包，首次启动下载完整运行环境。\n'
              '- 支持 64 位 ARM / arm64-v8a 与 x86_64（amd64）、Android 9 及以上，请按设备架构下载。\n')
    files = list(dict.fromkeys(files))
    checksums = []
    for p in files:
        with p.open('rb') as stream:
            checksums.append(f'{digest(stream)}  {p.name}')
    output.mkdir(parents=True, exist_ok=True)
    (output / 'notes.md').write_text(notes, encoding='utf-8')
    (output / 'SHA256SUMS').write_text('\n'.join(checksums) + '\n', encoding='utf-8')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--abis', default='arm64-v8a')
    parser.add_argument('--apk-dir', type=Path, required=True)
    parser.add_argument('--rootfs-dir', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--notes-file', type=Path, required=True)
    args = parser.parse_args()
    props = dict(line.strip().split('=', 1) for line in
                 (ROOT / 'app/gradle.properties').read_text(encoding='utf-8').splitlines()
                 if '=' in line and not line.lstrip().startswith('#'))
    prepare(args.tag, props['app.versionName'], '', args.apk_dir, args.rootfs_dir, args.output,
            notes_override=args.notes_file.read_text(encoding='utf-8'), abis=tuple(args.abis.split(',')))
    print('RELEASE_INPUTS_VERIFIED', args.tag)


if __name__ == '__main__':
    main()

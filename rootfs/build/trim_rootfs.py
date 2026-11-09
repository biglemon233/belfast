#!/usr/bin/env python3
"""Filter a rootfs tar without extracting it or modifying ALAS and runtime libraries.

Groups are deliberately narrow. A static absence of callers is not enough to
remove shared-library functions, NumPy testing helpers, pip, or runtime data.
"""
import argparse
from collections import Counter
import hashlib
import json
import lzma
from pathlib import Path, PurePosixPath
import re
import tarfile

GROUPS = (
    'linux-docs', 'build-tools', 'python-tests', 'foreign-u2',
    'proj-data', 'perl-headers', 'apt-cache', 'u2-apks', 'stdlib-tests'
)
# Preset 8 uses a 32 MiB decoder dictionary; retain 6/7 for measured controls.
DEFAULT_XZ_PRESET = 8
XZ_DICTIONARY_BYTES = {6: 8 * 1024 * 1024, 7: 16 * 1024 * 1024, 8: 32 * 1024 * 1024}
BINUTILS = frozenset(('addr2line', 'ar', 'as', 'c++filt', 'dwp', 'elfedit',
                     'gprof', 'ld', 'ld.bfd', 'ld.gold', 'nm', 'objcopy',
                     'objdump', 'ranlib', 'readelf', 'size', 'strings', 'strip'))


def clean_name(name):
    p = PurePosixPath(name)
    if p.is_absolute() or '..' in p.parts:
        raise ValueError(f'Unsafe archive path: {name}')
    return p.as_posix().removeprefix('./')


def removal_group(name):
    name = clean_name(name)
    if name == 'opt/alas' or name.startswith('opt/alas/'):
        return None
    parts = PurePosixPath(name).parts
    if name.startswith(('usr/share/doc/', 'usr/share/man/', 'usr/share/info/')):
        # Keep distribution/license notices, including compressed variants.
        if any(re.search(r'copyright|licen[cs]e|copying|notice', p, re.I) for p in parts):
            return None
        return 'linux-docs'
    if name.startswith('usr/include/'):
        return 'build-tools'
    if name.startswith('usr/bin/'):
        command = parts[-1].removeprefix('aarch64-linux-gnu-')
        if len(parts) == 3 and command in BINUTILS:
            return 'build-tools'
    if name.startswith('usr/share/proj/') and name.endswith(('.db', '.gtx', '.tif', '.json')):
        return 'proj-data'
    if name.startswith('usr/lib/aarch64-linux-gnu/perl/') and name.endswith('.h'):
        return 'perl-headers'
    if name.startswith(('var/cache/apt/', 'var/cache/debconf/')) or (name.startswith('var/lib/dpkg/info/') and name.endswith('.symbols')):
        return 'apt-cache'
    if name.startswith('usr/lib/python3.12/test/') or ('tornado/test/' in name):
        return 'stdlib-tests'
    for marker in ('dist-packages', 'site-packages'):
        if marker not in parts:
            continue
        tail = parts[parts.index(marker) + 1:]
        if tail and tail[0] in ('numpy', 'scipy') and 'tests' in tail[1:]:
            return 'python-tests'
        if tail and tail[0] == 'uiautomator2cache' and 'cache' in tail:
            if re.search(r'atx-agent_[^/]+_linux_(?:386|amd64|armv6|armv7)\.tar\.gz', name):
                return 'foreign-u2'
            if name.endswith('.apk'):
                return 'u2-apks'
    return None


class HashReader:
    def __init__(self, stream):
        self.stream = stream
        self.digest = hashlib.sha256()

    def read(self, size=-1):
        data = self.stream.read(size)
        self.digest.update(data)
        return data


def identity(path):
    with Path(path).open('rb') as f:
        return {'bytes': Path(path).stat().st_size,
                'sha256': hashlib.file_digest(f, 'sha256').hexdigest()}


def trim_archive(source, output=None, groups=GROUPS, xz_preset=DEFAULT_XZ_PRESET, compress=True):
    if xz_preset not in XZ_DICTIONARY_BYTES:
        raise ValueError('Select a supported XZ preset (6, 7 or 8)')
    groups = set(groups)
    if groups - set(GROUPS):
        raise ValueError('Select known trim groups')
    source = Path(source)
    output = Path(output) if output else None
    if output and (output.exists() or output.resolve() == source.resolve()):
        raise ValueError('Output must be a new file, distinct from the input')
    partial = output.with_name(output.name + '.partial') if output else None
    if partial and partial.exists():
        raise ValueError('An unfinished output exists; inspect it before retrying')
    removed, links, present = [], [], set()
    totals = Counter()
    protected = hashlib.sha256()
    archive_out = compressed = None
    try:
        if partial:
            partial.parent.mkdir(parents=True, exist_ok=True)
            compressed = lzma.open(partial, 'xb', preset=xz_preset) if compress else partial.open('xb')
            archive_out = tarfile.open(fileobj=compressed, mode='w|', format=tarfile.PAX_FORMAT)
        with tarfile.open(source, 'r|*') as archive:
            for member in archive:
                name = clean_name(member.name)
                present.add(name)
                group = removal_group(name)
                # Keep directory metadata; an empty directory costs no file payload.
                remove = not member.isdir() and group in groups
                if remove:
                    removed.append({'path': name, 'bytes': member.size, 'group': group})
                    totals[group] += member.size
                    continue
                if member.islnk():
                    links.append((name, clean_name(member.linkname)))
                reader = HashReader(archive.extractfile(member)) if member.isfile() else None
                if archive_out:
                    archive_out.addfile(member, reader)
                elif reader:
                    while reader.read(1024 * 1024):
                        pass
                if name == 'opt/alas' or name.startswith('opt/alas/'):
                    fingerprint = (name, member.type.decode('ascii'), member.linkname,
                                   member.mode, member.uid, member.gid,
                                   reader.digest.hexdigest() if reader else '')
                    protected.update(json.dumps(fingerprint, ensure_ascii=True).encode() + b'\n')
        removed_names = {item['path'] for item in removed}
        for name, target in links:
            if target in removed_names or target not in present:
                raise ValueError(f'Kept hard link has no target: {name} -> {target}')
    finally:
        if archive_out:
            archive_out.close()
        if compressed:
            compressed.close()
    report = {'schema': 1, 'input': identity(source), 'groups': sorted(groups),
              'removed_bytes': sum(totals.values()), 'removed_by_group': dict(totals),
              'removed': removed, 'alas_tree_sha256': protected.hexdigest(),
              'notes': ['Only output archive changes; source archive remains intact.',
                        'Runtime shared libraries, ALAS, models and license notices are preserved.',
                        'Removed file bytes are uncompressed; compressed savings require an actual output.']}
    if output:
        partial.rename(output)
        report['output'] = identity(output)
        report['compression'] = ({'format': 'xz', 'preset': xz_preset,
                                  'dictionary_bytes': XZ_DICTIONARY_BYTES[xz_preset]}
                                 if compress else {'format': 'tar'})
        with source.open('rb') as f:
            if compress and f.read(6) == b'\xfd7zXZ\x00':
                report['compressed_saved_bytes'] = report['input']['bytes'] - report['output']['bytes']
    return report


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--input', required=True, type=Path)
    p.add_argument('--output', type=Path, help='Omit for a read-only inventory')
    p.add_argument('--report', required=True, type=Path)
    p.add_argument('--groups', default=','.join(GROUPS))
    p.add_argument('--repack-only', action='store_true', help='Compression control; preserve every entry')
    p.add_argument('--xz-preset', type=int, choices=sorted(XZ_DICTIONARY_BYTES), default=DEFAULT_XZ_PRESET,
                   help='XZ preset; default 8 uses a 32 MiB decoder dictionary')
    args = p.parse_args()
    report = trim_archive(args.input, args.output, () if args.repack_only else args.groups.split(','),
                          xz_preset=args.xz_preset)
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2, ensure_ascii=False), encoding='utf-8')
    print(json.dumps({k: v for k, v in report.items() if k != 'removed'}, indent=2))


if __name__ == '__main__':
    main()

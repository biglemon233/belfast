#!/usr/bin/env python3
"""Read-only APK/rootfs size and ELF dependency inventory (standard library only).

ZIP compressed sizes are exact. Independent per-group XZ sizes are estimates:
XZ shares a dictionary across the original tar, so these must NOT be summed as
its actual composition. File sizes exclude filesystem allocation/link overhead.
No archive paths are extracted. DT_NEEDED does not cover dlopen or runtime data.
"""
import argparse
from collections import defaultdict
import hashlib
import io
import json
import lzma
from pathlib import Path, PurePosixPath
import struct
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def identity(path):
    with path.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    return {'bytes': path.stat().st_size, 'sha256': digest}


def rootfs_group(name):
    name = name.removeprefix('./').lstrip('/')
    if name.startswith('opt/alas/bin/cnocr_models/'):
        return 'alas/models'
    if name.startswith('opt/alas/assets/'):
        return 'alas/assets'
    if name.startswith('opt/alas/'):
        return 'alas/code-and-data'
    if name.startswith('usr/local/mxnet/'):
        return 'python/mxnet'
    parts = PurePosixPath(name).parts
    for marker in ('dist-packages', 'site-packages'):
        if marker in parts and len(parts) > parts.index(marker) + 1:
            package = parts[parts.index(marker) + 1]
            if package.endswith(('.dist-info', '.egg-info')):
                return 'python/metadata'
            return 'python/' + package
    for prefix, label in (
        ('usr/share/doc/', 'linux/documentation'),
        ('usr/share/man/', 'linux/documentation'),
        ('usr/share/info/', 'linux/documentation'),
        ('usr/share/locale/', 'linux/locales'),
        ('usr/lib/aarch64-linux-gnu/', 'linux/shared-libraries'),
        ('var/cache/', 'linux/caches'),
        ('root/.cache/', 'linux/caches'),
    ):
        if name.startswith(prefix):
            return label
    return 'linux/other'


def elf_dependencies(data):
    """Parse ELF64 little-endian program headers, including stripped binaries."""
    if data[:6] != b'\x7fELF\x02\x01':
        return None
    if len(data) < 64:
        raise ValueError('Truncated ELF header')
    phoff = struct.unpack_from('<Q', data, 32)[0]
    entsize, count = struct.unpack_from('<HH', data, 54)
    if entsize < 56 or phoff + entsize * count > len(data):
        raise ValueError('Invalid ELF program table')
    loads, dynamic = [], None
    for i in range(count):
        kind, _, offset, address, _, size, _, _ = struct.unpack_from('<IIQQQQQQ', data, phoff + i * entsize)
        if kind == 1:
            loads.append((address, offset, size))
        elif kind == 2:
            dynamic = (offset, size)
    result = {'machine': struct.unpack_from('<H', data, 18)[0], 'needed': []}
    if dynamic is None:
        return result
    offset, size = dynamic
    if offset + size > len(data):
        raise ValueError('Truncated ELF dynamic table')
    tags = defaultdict(list)
    for pos in range(offset, offset + size - 15, 16):
        tag, value = struct.unpack_from('<qQ', data, pos)
        if tag == 0:
            break
        tags[tag].append(value)
    if not tags[5]:
        return result
    address = tags[5][0]
    string_table = next((off + address - addr for addr, off, length in loads
                         if addr <= address < addr + length), None)
    if string_table is None:
        raise ValueError('ELF dynamic string table is not file-backed')

    def text(index):
        start = string_table + index
        end = data.find(b'\x00', start)
        if not 0 <= start < len(data) or end < 0:
            raise ValueError('Invalid ELF dynamic string')
        return data[start:end].decode('utf-8', errors='replace')

    result['needed'] = [text(value) for value in tags[1]]
    for tag, key in ((14, 'soname'), (15, 'rpath'), (29, 'runpath')):
        if tags[tag]:
            result[key] = text(tags[tag][0])
    return result


def profile_rootfs(path, work_dir, compress_groups=False, top=30):
    work_dir.mkdir(parents=True, exist_ok=True)
    groups, entries, elf = {}, [], []
    with tempfile.TemporaryDirectory(prefix='profile-', dir=work_dir) as temp:
        spools = {}
        try:
            with tarfile.open(path, 'r|*') as archive:
                for member in archive:
                    if not member.isfile():
                        continue
                    name = member.name.removeprefix('./').lstrip('/')
                    group = rootfs_group(name)
                    stats = groups.setdefault(group, {'file_bytes': 0, 'files': 0})
                    stats['file_bytes'] += member.size
                    stats['files'] += 1
                    entries.append({'path': name, 'bytes': member.size, 'group': group})
                    with archive.extractfile(member) as stream:
                        data = stream.read()
                    if data.startswith(b'\x7fELF'):
                        try:
                            details = elf_dependencies(data)
                            elf.append({'path': name, **(details or {'unparsed': 'Not ELF64 little-endian'})})
                        except (ValueError, struct.error) as error:
                            elf.append({'path': name, 'unparsed': str(error)})
                    if compress_groups:
                        if group not in spools:
                            spools[group] = tarfile.open(Path(temp) / str(len(spools)), 'w')
                        # Preserve the tar header but use our own sequential files, never member paths.
                        spools[group].addfile(member, io.BytesIO(data))
        finally:
            for spool in spools.values():
                spool.close()
        for group, spool in spools.items():
            compressor = lzma.LZMACompressor(preset=6)
            size = 0
            with open(spool.name, 'rb') as stream:
                while chunk := stream.read(1024 * 1024):
                    size += len(compressor.compress(chunk))
            size += len(compressor.flush())
            groups[group]['independent_xz_bytes_estimate'] = size
    return {**identity(path), 'file_bytes': sum(g['file_bytes'] for g in groups.values()),
            'groups': dict(sorted(groups.items())),
            'top_files': sorted(entries, key=lambda e: (-e['bytes'], e['path']))[:top],
            'elf': sorted(elf, key=lambda e: e['path'])}


def profile_apk(path, top=30):
    groups, entries = {}, []
    with zipfile.ZipFile(path) as archive:
        for info in archive.infolist():
            if info.is_dir():
                continue
            name = info.filename
            if name == 'assets/rootfs/rootfs.tar.xz':
                group = 'rootfs'
            elif name.startswith('lib/'):
                group = 'native'
            elif name.startswith('classes') and name.endswith('.dex'):
                group = 'dex'
            elif name.startswith('assets/'):
                group = 'assets'
            else:
                group = 'resources-and-metadata'
            stats = groups.setdefault(group, {'file_bytes': 0, 'zip_compressed_bytes': 0})
            stats['file_bytes'] += info.file_size
            stats['zip_compressed_bytes'] += info.compress_size
            entries.append({'path': name, 'bytes': info.file_size, 'zip_compressed_bytes': info.compress_size})
    total = sum(g['zip_compressed_bytes'] for g in groups.values())
    return {**identity(path), 'zip_headers_and_signing_bytes': path.stat().st_size - total,
            'groups': groups, 'top_files': sorted(entries, key=lambda e: (-e['zip_compressed_bytes'], e['path']))[:top]}


def differences(before, after):
    result = {}
    for kind in ('apk', 'rootfs'):
        if kind in before and kind in after:
            result[kind] = {'total_bytes_delta': after[kind]['bytes'] - before[kind]['bytes']}
            left, right = before[kind]['groups'], after[kind]['groups']
            result[kind]['file_bytes_delta_by_group'] = {
                group: right.get(group, {}).get('file_bytes', 0) - left.get(group, {}).get('file_bytes', 0)
                for group in sorted(left.keys() | right.keys())
            }
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rootfs', type=Path)
    parser.add_argument('--apk', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--baseline', type=Path)
    parser.add_argument('--compress-groups', action='store_true')
    args = parser.parse_args()
    if not args.rootfs and not args.apk:
        parser.error('Specify --rootfs and/or --apk')
    if args.output.resolve() in {p.resolve() for p in (args.rootfs, args.apk, args.baseline) if p}:
        parser.error('Output must differ from all input files')
    report = {'schema': 1, 'notes': [
        'ZIP compressed sizes are exact; independent XZ group estimates are NOT additive.',
        'File sizes exclude filesystem allocation and link overhead.',
        'DT_NEEDED does not prove a file is unused: dlopen, ABI, data and runtime tests still matter.',
    ]}
    if args.rootfs:
        report['rootfs'] = profile_rootfs(args.rootfs, ROOT / '.tmp/bundle-profile', args.compress_groups)
    if args.apk:
        report['apk'] = profile_apk(args.apk)
    if args.baseline:
        report['comparison'] = differences(json.loads(args.baseline.read_text(encoding='utf-8')), report)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    for kind in ('rootfs', 'apk'):
        if kind in report:
            print(f'{kind}: {report[kind]["bytes"]} bytes; SHA256 {report[kind]["sha256"]}')
    print(f'Report: {args.output}')


if __name__ == '__main__':
    main()

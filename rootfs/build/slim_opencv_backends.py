#!/usr/bin/env python3
"""Replace imgcodecs in an offline rootfs; prune only named optional backends.

Default is a read-only JSON plan. This is a conservative DT_NEEDED/name audit,
not a substitute for the ARM64 loader, image-format and original OCR gates.
Never run against a live rootfs or concurrently modify the candidate directory.
"""
import argparse
from collections import defaultdict
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import struct

from profile_bundle import elf_dependencies, identity

LIBDIR = PurePosixPath('usr/lib/aarch64-linux-gnu')
TARGET = LIBDIR / 'libopencv_imgcodecs.so.4.6.0'
SONAME = 'libopencv_imgcodecs.so.406'
BACKENDS = {'libgdal.so.34', 'libImath-3_1.so.29'} | {
    f'lib{name}-3_1.so.30' for name in ('OpenEXR', 'OpenEXRCore', 'OpenEXRUtil', 'Iex', 'IlmThread')
} | {f'libgdcm{name}.so.3.0' for name in
     ('Common', 'DICT', 'DSED', 'IOD', 'MEXD', 'MSFF', 'jpeg8', 'jpeg12', 'jpeg16')}
FORBIDDEN = re.compile(r'^lib(?:gdal|gdcm|OpenEXR|Iex|IlmThread|Imath)')


def require(condition, message):
    if not condition:
        raise ValueError(message)


def guest_path(root, name):
    """Resolve Linux symlinks inside root, never by host absolute-link semantics."""
    pending, parts, links = list(PurePosixPath(name).parts), [], 0
    while pending:
        item = pending.pop(0)
        if item in ('/', '.'):
            continue
        if item == '..':
            require(bool(parts), f'Path escapes rootfs: {name}')
            parts.pop()
            continue
        require('\\' not in item and ':' not in item, f'Invalid guest path: {name}')
        path = root.joinpath(*parts, item)
        if path.is_symlink():
            links += 1
            require(links <= 40, f'Symlink loop: {name}')
            target = os.readlink(path)
            if target.startswith('/'):
                parts = []
            pending = list(PurePosixPath(target).parts) + pending
        else:
            require(not path.is_junction(), f'Junction not allowed: {name}')
            parts.append(item)
    return PurePosixPath(*parts)


def writable(root, name):
    name = PurePosixPath(name)
    require(name.parent == LIBDIR, f'Write outside library allowlist: {name}')
    path = root / name
    require(path.parent.resolve() == root / LIBDIR, f'Symlink parent: {name}')
    require(guest_path(root, name).parent == LIBDIR, f'Library link escapes allowlist: {name}')
    require(path.is_symlink() or path.stat().st_nlink == 1, f'Hardlink not allowed: {name}')
    return path


def symbols(data):
    """Read SHT_DYNSYM; reject stripped section tables rather than guess ABI."""
    offset = struct.unpack_from('<Q', data, 40)[0]
    size, count = struct.unpack_from('<HH', data, 58)
    require(size >= 64 and count and offset + size * count <= len(data), 'Missing ELF section table')
    sections = [struct.unpack_from('<IIQQQQIIQQ', data, offset + i * size) for i in range(count)]
    exported, undefined = set(), set()
    for section in sections:
        if section[1] != 11:
            continue
        require(section[6] < count and section[9] == 24 and section[5] % 24 == 0, 'Invalid ELF dynsym')
        strings = sections[section[6]]
        require(strings[4] + strings[5] <= len(data) and section[4] + section[5] <= len(data),
                'Truncated ELF dynsym')
        for pos in range(section[4], section[4] + section[5], 24):
            name, info, visibility, index, _, _ = struct.unpack_from('<IBBHQQ', data, pos)
            require(name < strings[5], 'Invalid ELF symbol name')
            start = strings[4] + name
            end = data.find(b'\0', start, strings[4] + strings[5])
            require(end >= 0, 'Unterminated ELF symbol name')
            if name and info >> 4 in (1, 2, 10):
                symbol = data[start:end].decode('utf-8', errors='strict')
                if index == 0:
                    undefined.add(symbol)
                elif visibility & 3 in (0, 3):
                    exported.add(symbol)
    return exported, undefined


def inspect_library(data):
    details = elf_dependencies(data)
    require(details is not None and details['machine'] == 183, 'Expected AArch64 ELF64 little-endian')
    require(struct.unpack_from('<H', data, 16)[0] == 3, 'Expected shared ELF library')
    require(details.get('soname') == SONAME, f'Expected SONAME {SONAME}')
    return details


def slim(root, replacement, apply=False):
    root, replacement = Path(root).absolute(), Path(replacement).absolute()
    require(root == root.resolve() and root != Path(root.anchor), 'Rootfs must be a real offline directory')
    require((root / 'etc/os-release').is_file() and (root / 'opt/alas').is_dir(), 'Not an ALAS rootfs')
    target = writable(root, TARGET)
    require(not target.is_symlink(), 'Replacement target must be a regular file')
    require(guest_path(root, LIBDIR / SONAME) == TARGET, 'SONAME link must resolve to replacement target')
    require(not replacement.is_relative_to(root), 'Replacement must be outside rootfs')
    require(replacement == replacement.resolve(), 'Replacement symlink not allowed')
    old_data, new_data = target.read_bytes(), replacement.read_bytes()
    old, new = inspect_library(old_data), inspect_library(new_data)
    require(not any(FORBIDDEN.match(n) for n in new['needed']), 'Replacement still needs optional backends')
    old_exports, _ = symbols(old_data)
    new_exports, _ = symbols(new_data)
    require(old_exports and new_exports, 'Empty imgcodecs export table')
    elf, links, consumers, foreign_assets = {}, {}, {}, []
    for directory, dirs, files in os.walk(root, followlinks=False):
        for item in dirs + files:
            path = Path(directory) / item
            name = PurePosixPath(path.relative_to(root).as_posix())
            if path.is_symlink():
                links[name] = guest_path(root, name)
            elif path.is_junction():
                raise ValueError(f'Junction not allowed: {name}')
            elif path.is_file():
                with path.open('rb') as stream:
                    if stream.read(4) != b'\x7fELF':
                        continue
                    stream.seek(0)
                    data = stream.read()
                details = elf_dependencies(data)
                if not name.is_relative_to(LIBDIR) and (
                        details is None or details['machine'] != 183):
                    # ALAS and Python packages ship Android helpers for several
                    # architectures. Preserve them outside the ARM64 library graph.
                    require(len(data) >= 20 and data[4] in (1, 2) and data[5] in (1, 2),
                            f'Malformed foreign ELF asset: {name}')
                    foreign_assets.append(str(name))
                    continue
                require(details is not None, f'Unsupported ELF: {name}')
                require(not any('/' in needed for needed in details['needed']),
                        f'Unexpected dependency path in {name}; requires manual audit')
                elf[name] = details
                if name != TARGET and SONAME in details['needed']:
                    used = symbols(data)[1] & old_exports
                    require(used <= new_exports, f'Missing consumer exports for {name}: {sorted(used - new_exports)}')
                    consumers[str(name)] = sorted(used)
    require(TARGET in elf, 'Original imgcodecs absent from inventory')
    require('usr/local/mxnet/libmxnet.so' in consumers, 'MXNet consumer missing from rootfs')
    candidates = {p for p, e in elf.items() if p.parent == LIBDIR and e['machine'] == 183 and e.get('soname') in BACKENDS
                  and (p.name == e['soname'] or p.name.startswith(e['soname'] + '.'))}
    aliases = defaultdict(set)
    for path in elf:
        aliases[path.name].add(path)
    for path, resolved in links.items():
        if resolved in elf:
            aliases[path.name].add(resolved)
    before = {(str(p), n) for p, e in elf.items() for n in e['needed'] if not aliases[n]}
    elf[TARGET] = new
    for needed in new['needed']:
        require('/' not in needed, f'Unexpected dependency path: {needed}')
        search = [LIBDIR, PurePosixPath('lib/aarch64-linux-gnu'), PurePosixPath('lib'), PurePosixPath('usr/lib')]
        require(any(guest_path(root, d / needed) in elf for d in search), f'Missing replacement dependency: {needed}')
    pending, visited = list(new['needed']), set()
    while pending:
        needed = pending.pop()
        require(not FORBIDDEN.match(needed), f'Replacement transitively needs backend: {needed}')
        require(bool(aliases[needed]), f'Missing transitive replacement dependency: {needed}')
        require(all(elf[p]['machine'] == 183 for p in aliases[needed]), f'Non-AArch64 dependency: {needed}')
        if needed not in visited:
            visited.add(needed)
            pending.extend(n for p in aliases[needed] for n in elf[p]['needed'])
    removed, retained = set(candidates), {}
    while True:
        reasons = defaultdict(list)
        for path, details in elf.items():
            if path not in removed:
                for needed in details['needed']:
                    for provider in aliases[needed] & removed:
                        reasons[provider].append(f'DT_NEEDED by {path}: {needed}')
        for path, resolved in links.items():
            if resolved in removed and path.parent != LIBDIR:
                reasons[resolved].append(f'Alias outside deletion directory: {path}')
        if not reasons:
            break
        removed -= reasons.keys()
        retained.update({str(p): sorted(set(why)) for p, why in reasons.items()})
    after = {(str(p), n) for p, e in elf.items() if p not in removed for n in e['needed']
             if not aliases[n] - removed}
    require(not after - before, f'New missing dependencies: {sorted(after - before)}')
    delete = removed | {p for p, resolved in links.items() if resolved in removed and p.parent == LIBDIR}
    records = []
    for path in sorted(delete):
        actual = writable(root, path)
        records.append({'path': str(path), 'bytes': actual.lstat().st_size,
                        **({'link': os.readlink(actual)} if actual.is_symlink() else identity(actual))})
    report = {'schema': 1, 'applied': False, 'target': str(TARGET),
              'original': {'bytes': len(old_data), 'sha256': hashlib.sha256(old_data).hexdigest()},
              'replacement': {'bytes': len(new_data), 'sha256': hashlib.sha256(new_data).hexdigest()},
              'replacement_needed': new['needed'],
              'consumers': consumers, 'remove': records, 'remove_bytes': sum(r['bytes'] for r in records),
              'retained': retained, 'preexisting_missing': sorted(before),
              'preserved_foreign_assets': sorted(foreign_assets),
              'note': 'Named-backend DT_NEEDED audit only; dlopen/data and ARM64 runtime gates remain required.'}
    if apply:
        require(identity(target) == report['original'] and identity(replacement) == report['replacement'],
                'Inputs changed during audit')
        for entry in records:
            path = writable(root, entry['path'])
            current = {'link': os.readlink(path)} if path.is_symlink() else identity(path)
            require(all(entry[k] == v for k, v in current.items()), f'Candidate changed: {path}')
        staged = target.with_name('.imgcodecs-replacement.tmp')
        with staged.open('xb') as stream:
            stream.write(new_data)
        staged.chmod(stat.S_IMODE(target.stat().st_mode))
        os.replace(staged, target)
        for entry in records:
            writable(root, entry['path']).unlink()
        report['applied'] = True
        report['installed'] = identity(target)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rootfs', type=Path, required=True)
    parser.add_argument('--replacement', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    try:
        output = args.output.absolute()
        require(not output.resolve().is_relative_to(args.rootfs.resolve()), 'Report must be outside rootfs')
        require(output.resolve() != args.replacement.resolve() and not output.is_symlink(), 'Unsafe report path')
        report = slim(args.rootfs, args.replacement, args.apply)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
    except (OSError, ValueError, struct.error) as error:
        parser.exit(1, f'Refused: {error}\n')


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Prepare pinned Android x86_64 PRoot libraries, never desktop Linux binaries."""
import hashlib
import io
from pathlib import Path
import struct
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
PACKAGES = {
    'proot': ('p/proot/proot_5.1.107.96_x86_64.deb', '77ea45540071ca543adda2b51aca2bc3761c52904d013fd0890682288eff9455'),
    'libtalloc': ('libt/libtalloc/libtalloc_2.5.0_x86_64.deb', 'b8c6d95f20075dc1f9ec6573575b2444e8d526e48e0d8d6d5cf4e071e6e06530'),
    'libandroid-shmem': ('liba/libandroid-shmem/libandroid-shmem_0.7_x86_64.deb', 'ffa9e4c87467b158b148d0ff92dda796aa038276c2075af3269cdcdb06f25797'),
}
FILES = {'bin/proot': 'libproot.so', 'libexec/proot/loader': 'libproot-loader.so',
         'lib/libtalloc.so.2.5.0': 'libtalloc.so', 'lib/libandroid-shmem.so': 'libandroid-shmem.so'}
# Exact outputs of the checksum-pinned archives above, including the DT_NEEDED edit.
PREPARED_SHA256 = {
    'libproot.so': 'cd893e5243aa0213a4f8dec38c84fb41b24a104605fda71e61d0b2f459f31177',
    'libproot-loader.so': 'a836b0569c84b1661333d16bacf6b396ff08fea816a5c936a82e08ddeeca8341',
    'libtalloc.so': '7fa6722a4320495b1b85832fc7fd02de0cb367b45f116c1921b2830a6936da90',
    'libandroid-shmem.so': '092926060298acd3778e6239033d7aef1280dcb59aebe021a3719612e6a3465f',
}


def prepared_matches(output):
    return all((output / name).is_file() and
               hashlib.sha256((output / name).read_bytes()).hexdigest() == sha
               for name, sha in PREPARED_SHA256.items())


def deb_payload(data):
    if not data.startswith(b'!<arch>\n'):
        raise ValueError('Invalid Debian archive')
    pos = 8
    while pos + 60 <= len(data):
        header = data[pos:pos + 60]
        size = int(header[48:58])
        name = header[:16].decode().strip().rstrip('/')
        payload = data[pos + 60:pos + 60 + size]
        if name.startswith('data.tar'):
            return payload
        pos += 60 + size + size % 2
    raise ValueError('Debian archive has no data payload')


def main():
    cache = ROOT / '.tmp/x86-proot'
    output = ROOT / 'app/app/src/main/prootLibs/x86_64'
    if prepared_matches(output):
        print('Pinned Android x86_64 PRoot binaries verified; no download required')
        return
    cache.mkdir(parents=True, exist_ok=True)
    output.mkdir(parents=True, exist_ok=True)
    found = set()
    for path, sha in PACKAGES.values():
        archive = cache / Path(path).name
        if not archive.exists():
            with urllib.request.urlopen('https://packages.termux.dev/apt/termux-main/pool/main/' + path, timeout=120) as response:
                archive.write_bytes(response.read())
        data = archive.read_bytes()
        if hashlib.sha256(data).hexdigest() != sha:
            raise ValueError('Termux package checksum mismatch: ' + archive.name)
        with tarfile.open(fileobj=io.BytesIO(deb_payload(data)), mode='r:*') as tar:
            for entry in tar:
                name = entry.name.removeprefix('./').removeprefix('data/data/com.termux/files/usr/')
                if name not in FILES:
                    continue
                if not entry.isfile():
                    raise ValueError('Expected regular native binary: ' + name)
                blob = tar.extractfile(entry).read()
                if blob[:4] != b'\x7fELF' or struct.unpack_from('<H', blob, 18)[0] != 62:
                    raise ValueError('Expected x86_64 ELF: ' + name)
                old, new = b'libtalloc.so.2\0', b'libtalloc.so\0'
                if name == 'bin/proot':
                    if blob.count(old) != 1:
                        raise ValueError('Unexpected PRoot DT_NEEDED layout')
                    blob = blob.replace(old, new.ljust(len(old), b'\0'))
                target = output / FILES[name]
                target.write_bytes(blob)
                target.chmod(0o755)
                found.add(name)
                print(target.name, len(blob), hashlib.sha256(blob).hexdigest())
    if found != set(FILES):
        raise ValueError('Missing native PRoot inputs: ' + str(set(FILES) - found))


if __name__ == '__main__':
    main()

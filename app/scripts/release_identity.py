#!/usr/bin/env python3
"""Restore CI signing material and verify the public identity of a release APK."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import struct
import subprocess
import sys
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def settings():
    return dict(line.strip().split('=', 1) for line in
                (ROOT / 'app/gradle.properties').read_text(encoding='utf-8').splitlines()
                if line.strip() and not line.lstrip().startswith('#') and '=' in line)


def java_tool(name):
    home = os.environ.get('JAVA_HOME')
    return str(Path(home) / 'bin' / (name + ('.exe' if os.name == 'nt' else ''))) if home else name


def release_apk():
    version = settings()['app.versionName']
    abi = os.environ.get('RUNTIME_ABI', 'arm64-v8a')
    edition = 'full' if (ROOT / 'app/app/src/main/assets/rootfs/rootfs.tar.xz').is_file() else 'lite'
    return ROOT / f'app/app/build/outputs/apk/release/ALAS-AOS-v{version}-{abi}-{edition}.apk'


def prepare():
    required = ('KEYSTORE_BASE64', 'KEYSTORE_PATH', 'KEYSTORE_PASSWORD', 'KEY_ALIAS', 'KEY_PASSWORD')
    missing = [name for name in required if not os.environ.get(name)]
    if missing:
        raise ValueError('Missing release signing configuration: ' + ', '.join(missing))
    path = Path(os.environ['KEYSTORE_PATH']).resolve()
    if not path.is_relative_to(ROOT / '.tmp'):
        raise ValueError('CI signing material must stay in the project .tmp directory')
    if path.exists():
        raise ValueError('Refusing to overwrite existing signing material')
    data = base64.b64decode(os.environ['KEYSTORE_BASE64'], validate=True)
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        with path.open('xb') as output:
            output.write(data)
        path.chmod(0o600)
        result = subprocess.run([java_tool('keytool'), '-exportcert', '-keystore', str(path),
                                 '-storepass:env', 'KEYSTORE_PASSWORD', '-alias', os.environ['KEY_ALIAS']],
                                capture_output=True)
        if result.returncode:
            raise ValueError('Cannot read the configured signing certificate')
        digest = hashlib.sha256(result.stdout).hexdigest()
        if digest != settings()['app.signingCertificateSha256']:
            raise ValueError('Signing certificate does not match existing installations')
    except Exception:
        path.unlink(missing_ok=True)
        raise
    print('Release signing certificate verified: ' + digest)


def verify_identity(cert_output, badging, expected):
    # Human-readable signer labels differ across build-tools and SDK-targeted signatures.
    # Hash the certificate DER itself; repeated SDK ranges may legitimately print the same cert.
    pem = re.findall(r'-----BEGIN CERTIFICATE-----\s*([A-Za-z0-9+/=\s]+?)\s*-----END CERTIFICATE-----', cert_output)
    certificates = {hashlib.sha256(base64.b64decode(''.join(body.split()), validate=True)).hexdigest() for body in pem}
    if not certificates:
        raise ValueError('apksigner did not output a signing certificate')
    if certificates != {expected['app.signingCertificateSha256']}:
        raise ValueError('APK signing certificate does not match existing installations; observed SHA-256: '
                         + ', '.join(sorted(certificates)))
    package = re.search(r"^package: name='[^']+' versionCode='(\d+)' versionName='([^']+)'", badging, re.MULTILINE)
    if not package or package.group(1) != expected['app.versionCode'] or package.group(2) != expected['app.versionName']:
        raise ValueError('APK version does not match app/gradle.properties')
    return {'versionName': package.group(2), 'versionCode': int(package.group(1)),
            'certificateSha256': next(iter(certificates))}


def verify(apk):
    sdk = os.environ.get('ANDROID_HOME') or os.environ.get('ANDROID_SDK_ROOT')
    if not sdk:
        raise ValueError('Set ANDROID_HOME or ANDROID_SDK_ROOT to verify the release APK')
    jars = list((Path(sdk) / 'build-tools').glob('*/lib/apksigner.jar'))
    if not jars:
        raise ValueError('Android SDK apksigner is unavailable')
    jar = max(jars, key=lambda p: tuple(map(int, re.findall(r'\d+', p.parent.parent.name))))
    result = subprocess.run([java_tool('java'), '-jar', str(jar), 'verify', '--print-certs-pem', str(apk)],
                            capture_output=True, text=True)
    if result.returncode:
        raise ValueError('APK signature verification failed')
    aapt = jar.parent.parent / ('aapt.exe' if os.name == 'nt' else 'aapt')
    metadata = subprocess.run([str(aapt), 'dump', 'badging', str(apk)], capture_output=True, text=True)
    if metadata.returncode:
        raise ValueError('Cannot read APK version metadata')
    identity = verify_identity(result.stdout, metadata.stdout, settings())
    with zipfile.ZipFile(apk) as archive:
        abis = sorted({name.split('/')[1] for name in archive.namelist()
                       if name.startswith('lib/') and name.endswith('.so')})
        if os.environ.get('RUNTIME_ABI') == 'universal':
            verify_universal_proot(archive)
    abi = os.environ.get('RUNTIME_ABI', 'arm64-v8a')
    expected_abis = ['arm64-v8a', 'x86_64'] if abi == 'universal' else [abi]
    if abis != expected_abis:
        raise ValueError('APK native ABIs do not match the runtime package label')
    identity['abis'] = abis
    identity['apkBytes'] = apk.stat().st_size
    print(json.dumps(identity, indent=2))
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(os.environ['GITHUB_STEP_SUMMARY'], 'a', encoding='utf-8') as summary:
            summary.write(f"Release **{identity['versionName']}** (code {identity['versionCode']}), "
                          f"**{os.environ.get('RUNTIME_ABI', 'arm64-v8a')}**; "
                          f"certificate SHA-256: `{identity['certificateSha256']}`.\n\n")


def verify_universal_proot(archive):
    """Both executable families must survive extraction under either app ABI."""
    for install_abi in ('arm64-v8a', 'x86_64'):
        for suffix, machine, talloc, shmem in (('arm64', 183, 'libta.so', 'libsa.so'),
                                                ('x86_64', 62, 'libtx.so', 'libsx.so')):
            names = (f'libproot_{suffix}.so', f'libproot_loader_{suffix}.so', talloc, shmem)
            for name in names:
                path = f'lib/{install_abi}/{name}'
                try:
                    data = archive.read(path)
                except KeyError:
                    raise ValueError('Universal APK is missing a native runtime: ' + path) from None
                if (len(data) < 20 or data[:6] != b'\x7fELF\x02\x01'
                        or struct.unpack_from('<H', data, 18)[0] != machine):
                    raise ValueError('Universal APK native runtime has the wrong ELF architecture: ' + path)
                if name == names[0] and any(dep.encode() + b'\0' not in data for dep in (talloc, shmem)):
                    raise ValueError('Universal APK native runtime dependency aliases differ: ' + path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('prepare', 'verify'))
    parser.add_argument('--apk', type=Path)
    args = parser.parse_args()
    try:
        prepare() if args.action == 'prepare' else verify(args.apk or release_apk())
    except ValueError as error:
        # Only bounded validation messages; raw keytool/apksigner diagnostics stay private.
        print('Release identity check failed: ' + str(error), file=sys.stderr)
        return 1
    except (OSError, KeyError):
        print('Release identity check failed; verify signing inputs and tool availability.', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())

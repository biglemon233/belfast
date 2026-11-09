#!/usr/bin/env python3
"""ALAS CDN pack updates with bounded diagnostics and real transfer progress."""
import json
import os
import re
import shutil
import sys
import tempfile
import urllib.error
import urllib.request
import zipfile

from update_progress import ProgressWriter

BASE_URLS = [
    'https://1818706573.cdn.123clouddisk.com/1818706573/pack/LmeSzinc_AzurLaneAutoScript_master',
    'https://vip.123pan.cn/1818706573/pack/LmeSzinc_AzurLaneAutoScript_master',
]
LATEST_TIMEOUT = 3
PACK_TIMEOUT = 20
SHA_RE = re.compile(r'[0-9a-f]{40}')


def fetch_latest():
    for base in BASE_URLS:
        try:
            opener = urllib.request.build_opener()
            with opener.open(base + '/latest.json', timeout=LATEST_TIMEOUT) as resp:
                if resp.status != 200:
                    continue
                data = resp.read(16 * 1024 + 1)
                if len(data) > 16 * 1024:
                    continue
                commit = json.loads(data.decode('utf-8'))['commit']
            if isinstance(commit, str) and SHA_RE.fullmatch(commit):
                return base, commit
        except Exception:
            # Redirect targets, proxy credentials and exception text stay private.
            print('cdn| latest request failed')
    return None, ''


def download_pack(base, latest, current, alas_dir):
    """Spool chunks instead of retaining a whole ZIP in memory; emit no raw network error."""
    if not SHA_RE.fullmatch(latest) or not SHA_RE.fullmatch(current):
        return False, 'NO_PACK invalid-revision'
    progress = ProgressWriter()
    pack_dir = os.path.join(alas_dir, '.git', 'objects', 'pack')
    try:
        with tempfile.TemporaryFile(dir=alas_dir) as data:
            opener = urllib.request.build_opener()
            with opener.open(f'{base}/{latest}/{current}.zip', timeout=PACK_TIMEOUT) as resp:
                try:
                    total = int(resp.headers.get('Content-Length', '-1'))
                except ValueError:
                    total = -1
                done = 0
                progress.report('download', 0, total, force=True)
                while chunk := resp.read(64 * 1024):
                    data.write(chunk)
                    done += len(chunk)
                    progress.report('download', done, total)
                progress.report('download', done, total, force=True)
            data.seek(0)
            progress.report('unpack', force=True)
            with zipfile.ZipFile(data) as zipped:
                os.makedirs(pack_dir, exist_ok=True)
                for name in (f'pack-{latest}.pack', f'pack-{latest}.idx'):
                    temp = os.path.join(pack_dir, name + '.tmp')
                    try:
                        with zipped.open(zipped.getinfo(name)) as source, open(temp, 'wb') as target:
                            shutil.copyfileobj(source, target)
                        os.replace(temp, os.path.join(pack_dir, name))
                    finally:
                        if os.path.exists(temp):
                            os.unlink(temp)
    except urllib.error.HTTPError as error:
        return False, f'NO_PACK http-{error.code}'
    except zipfile.BadZipFile:
        return False, 'UNAVAILABLE not-a-zip'
    except KeyError:
        return False, 'UNAVAILABLE zip-missing'
    except Exception:
        return False, 'UNAVAILABLE transfer'

    ref_file = os.path.join(alas_dir, '.git', 'refs', 'remotes', 'origin', 'master')
    try:
        os.makedirs(os.path.dirname(ref_file), exist_ok=True)
        with open(ref_file + '.tmp', 'w', encoding='ascii') as target:
            target.write(latest + '\n')
        os.replace(ref_file + '.tmp', ref_file)
    except OSError:
        return False, 'UNAVAILABLE reference'
    return True, ''


def main():
    alas_dir = sys.argv[1]
    current = sys.argv[2].strip() if len(sys.argv) > 2 else ''
    base, latest = fetch_latest()
    if not latest:
        print('UNAVAILABLE latest.json')
        return 1
    if latest == current:
        print('UPTODATE')
        return 0
    if not SHA_RE.fullmatch(current):
        print('NO_PACK unknown-current')
        return 1
    ok, reason = download_pack(base, latest, current, alas_dir)
    print(f'PACK_READY {latest}' if ok else reason)
    return 0 if ok else 1


if __name__ == '__main__':
    sys.exit(main())

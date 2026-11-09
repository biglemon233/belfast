#!/usr/bin/env python3
"""通过 ADB 在已安装工具链的 Android PRoot 中运行热更与监督器回归。

只写入隔离的测试目录，不触发正式源码更新，不读取控制口令。设备必须安装可
run-as 的调试包及包含 Node.js/npm/Git 的运行时，测试结束后保存 JSON 和日志。

Runs hot-update and supervisor regressions inside an equipped Android PRoot through ADB.

Uses an isolated fixture without updating live source or reading control tokens. Requires
a debuggable app and a runtime with Node.js/npm/Git, and saves JSON and execution logs.
"""

import argparse
import datetime
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import time
import uuid


REPOSITORY = Path(__file__).resolve().parents[1]


class Device:
    """用明确设备和应用范围执行 ADB 命令。 / Executes ADB with explicit device and app scopes."""

    def __init__(self, adb, serial, package):
        if not re.fullmatch(r'[A-Za-z][A-Za-z0-9_.]+', package):
            raise ValueError('Invalid Android package')
        self.adb = adb
        self.serial = serial
        self.package = package
        self.base = self.call('shell', 'run-as', package, 'pwd').strip()
        if not re.fullmatch(r'/data/(?:user/\d+|data)/' + re.escape(package), self.base):
            raise ValueError('Unexpected run-as data directory')
        self.root = self.base + '/files/rootfs'
        abi = self.call('shell', 'getprop', 'ro.product.cpu.abi').strip()
        library_arch = {'arm64-v8a': 'arm64', 'x86_64': 'x86_64'}.get(abi)
        if not library_arch:
            raise ValueError(f'Unsupported device ABI: {abi}')
        self.abi = abi
        apk = self.call('shell', 'pm', 'path', package).splitlines()[0].removeprefix('package:')
        self.native = apk.rsplit('/', 1)[0] + '/lib/' + library_arch
        self.fixture = 'files/rootfs/opt/azurpilot-device-update-tests'

    def call(self, *arguments, timeout=120):
        """返回合并输出，失败时携带设备诊断。 / Returns merged output with device diagnostics on failure."""
        process = subprocess.run([self.adb, '-s', self.serial, *arguments], timeout=timeout,
                                 stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        output = process.stdout.decode('utf-8', 'replace').replace('\r', '')
        if process.returncode:
            raise RuntimeError(output.strip())
        return output

    def app(self, script, timeout=120):
        """在应用私有目录执行命令。 / Runs commands within the app-private directory."""
        return self.call('shell', 'run-as', self.package, 'sh', '-c', shlex.quote(script),
                         timeout=timeout)

    def push_fixture(self, source, relative):
        """经临时文件导入隔离测试输入，成功后删除共享暂存文件。

        Imports isolated test inputs through staging files and removes shared staging afterwards.
        """
        staged = '/data/local/tmp/azurpilot-test-' + uuid.uuid4().hex
        self.call('push', str(source), staged)
        target = self.fixture + '/' + relative
        try:
            self.app('mkdir -p ' + shlex.quote(target.rsplit('/', 1)[0]) + '; cp ' +
                     shlex.quote(staged) + ' ' + shlex.quote(target))
        finally:
            self.call('shell', 'rm', '-f', staged)

    def guest(self, script, timeout=120):
        """复用 App 的 loader、共享内存绑定和硬链接兼容选项。

        Uses the app's loader, private shared-memory bind, and hard-link compatibility options.
        """
        self.app('mkdir -p files/proot-tmp files/rootfs/dev/shm')
        prefix = (f'LD_LIBRARY_PATH={shlex.quote(self.native)} '
                  f'PROOT_LOADER={shlex.quote(self.native + "/libproot-loader.so")} '
                  f'PROOT_TMP_DIR={shlex.quote(self.base + "/files/proot-tmp")} ')
        arguments = [self.native + '/libproot.so', '--link2symlink', '-r', self.root,
                     '-w', '/opt/azurpilot', '-b', '/dev:/dev', '-b', '/proc:/proc',
                     '-b', '/sys:/sys', '-b', self.root + '/dev/shm:/dev/shm',
                     '/usr/bin/env', '-i', 'HOME=/root', 'PATH=/usr/local/bin:/usr/bin:/bin',
                     'LANG=C.UTF-8', 'TMPDIR=/tmp', 'GIT_TERMINAL_PROMPT=0',
                     'NPM_CONFIG_CACHE=/opt/azurpilot-npm-cache', 'AZURPILOT_ANDROID=0',
                     '/bin/sh', '-c', script]
        return self.app(prefix + shlex.join(arguments), timeout)


def find_adb():
    """查找 PATH 或 Windows Android SDK 的 adb。 / Finds adb in PATH or the Windows Android SDK."""
    candidate = shutil.which('adb')
    if candidate:
        return candidate
    for directory in (os.environ.get('ANDROID_HOME'), os.environ.get('ANDROID_SDK_ROOT'),
                      str(Path.home() / 'AppData/Local/Android/Sdk')):
        if directory:
            path = Path(directory) / 'platform-tools/adb.exe'
            if path.is_file():
                return str(path)
    raise RuntimeError('adb unavailable; pass --adb')


def main():
    """验证工具链并保存真机回归结果。 / Verifies the toolchain and saves device regression results."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial', required=True)
    parser.add_argument('--package', default='com.azurpilot.ghio')
    parser.add_argument('--adb')
    parser.add_argument('--output', type=Path, default=REPOSITORY / '.tmp/android-hot-update')
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    device = Device(args.adb or find_adb(), args.serial, args.package)
    report = {'testedAtUtc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'deviceModel': device.call('shell', 'getprop', 'ro.product.model').strip(),
              'androidVersion': device.call('shell', 'getprop', 'ro.build.version.release').strip(),
              'abi': device.abi, 'package': args.package, 'passed': False}
    start = time.monotonic()
    try:
        report['toolchain'] = device.guest('set -e; node --version; npm --version; git --version').strip().splitlines()
        device.push_fixture(REPOSITORY / 'rootfs/tests/test_android_update.py', 'tests/test_android_update.py')
        for name in ('android_update.py', 'android_host.py'):
            device.push_fixture(REPOSITORY / 'rootfs/overlays' / name, 'overlays/' + name)
        script = ('.venv/bin/python /opt/azurpilot-device-update-tests/tests/test_android_update.py -v '
                  '> /opt/azurpilot-device-update-tests/result.log 2>&1; '
                  'result=$?; cat /opt/azurpilot-device-update-tests/result.log; exit "$result"')
        output = device.guest(script, timeout=1200)
        (args.output / 'tests.log').write_text(output, encoding='utf-8')
        match = re.search(r'Ran (\d+) tests in ([0-9.]+)s', output)
        if not match or not re.search(r'^OK$', output, re.M) or 'skipped' in output:
            raise RuntimeError('Device suite did not pass every test')
        report.update(passed=True, testCount=int(match[1]), testSeconds=float(match[2]))
    except Exception as error:
        report['error'] = str(error)
        (args.output / 'failure.log').write_text(str(error), encoding='utf-8')
    finally:
        # 路径是本工具固定的隔离目录，不能删除正式源码或用户配置。
        device.app('rm -rf ' + shlex.quote(device.fixture))
        report['elapsedSeconds'] = round(time.monotonic() - start, 3)
        (args.output / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report, ensure_ascii=True, indent=2))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())

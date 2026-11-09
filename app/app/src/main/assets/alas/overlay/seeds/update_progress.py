#!/usr/bin/env python3
"""Numeric-only update progress. Raw Git output and URLs never reach the snapshot."""
import os
from pathlib import Path
import re
import subprocess
import sys
import time

MAX_INT = 2**63 - 1
GIT_BYTES = re.compile(r'Receiving objects:.*?,\s*(\d{1,12}(?:\.\d{1,3})?)\s+(bytes|B|KiB|MiB|GiB|KB|MB|GB)(?:\s*\|\s*(\d{1,12}(?:\.\d{1,3})?)\s+(bytes|B|KiB|MiB|GiB|KB|MB|GB)/s)?')
UNITS = {'bytes': 1, 'B': 1, 'KiB': 1024, 'MiB': 1024**2, 'GiB': 1024**3,
         'KB': 1000, 'MB': 1000**2, 'GB': 1000**3}


class ProgressWriter:
    def __init__(self, path=None):
        self.path = Path(path or os.environ.get('ALASAOS_PROGRESS_FILE') or
                         Path(os.environ.get('ALASAOS_ALAS_ROOT', '/opt/alas')) / '.alasaos_update_progress')
        self.started = time.monotonic()
        self.last_write = 0.0

    def report(self, stage, done=0, total=-1, speed=None, force=False):
        if stage not in ('download', 'unpack'):
            return
        now = time.monotonic()
        if not force and now - self.last_write < 0.2:
            return
        self.last_write = now
        done = max(0, min(MAX_INT, int(done)))
        total = min(MAX_INT, int(total)) if total > 0 else -1
        if speed is None:
            speed = done / max(now - self.started, 0.001)
        speed = max(0, min(MAX_INT, int(speed)))
        temp = self.path.with_name(self.path.name + f'.{os.getpid()}.tmp')
        try:
            temp.write_text(f'AOS_PROGRESS {stage} {done} {total} {speed}\n', encoding='ascii')
            os.replace(temp, self.path)
        except OSError:
            pass  # Telemetry failure must not turn a valid update into a failed update.
        finally:
            try:
                temp.unlink(missing_ok=True)
            except OSError:
                pass


def git_progress(line):
    match = GIT_BYTES.search(line)
    if not match:
        return None
    done = int(float(match[1]) * UNITS[match[2]])
    speed = int(float(match[3]) * UNITS[match[4]]) if match[3] else 0
    # Git's object percentage is NOT a byte percentage. Total bytes remain unknown.
    return min(done, MAX_INT), min(speed, MAX_INT)


def run_process(command, writer=None):
    writer = writer or ProgressWriter()
    writer.report('download', force=True)
    pending = b''
    latest = None
    with subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE) as process:
        while chunk := process.stderr.read1(4096):
            parts = re.split(b'[\r\n]', pending + chunk)
            pending = parts.pop()[-4096:]
            for part in parts:
                value = git_progress(part.decode('utf-8', errors='replace'))
                if value is not None:
                    latest = value
                    writer.report('download', value[0], speed=value[1])
        value = git_progress(pending.decode('utf-8', errors='replace'))
        latest = value or latest
        if latest:
            writer.report('download', latest[0], speed=latest[1], force=True)
        return process.wait()


if __name__ == '__main__':
    command = sys.argv[1:]
    if command[:1] == ['--']:
        command = command[1:]
    if not command:
        raise SystemExit(2)
    try:
        raise SystemExit(run_process(command))
    except OSError:
        raise SystemExit(1)

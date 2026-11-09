#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Read deploy Repository/Branch for the update process, without rewriting YAML.

The historic sync command is intentionally retired. Only the host-side
AlasSourceRepository may migrate legacy preferences / save this pair. --read
emits a private two-line protocol, captured by the shell and NEVER logged.
All errors have a fixed public verdict and nonzero status.
"""
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import urlsplit

def scalar(raw):
    quote = None
    comment = len(raw)
    i = 0
    while i < len(raw):
        c = raw[i]
        if quote == "'" and raw[i:i + 2] == "''":
            i += 2
            continue
        if quote == '"' and c == '\\':
            i += 2
            continue
        if quote and c == quote:
            quote = None
        elif quote is None and i == 0 and c in "'\"":
            quote = c
        elif quote is None and c == '#' and (i == 0 or raw[i - 1].isspace()):
            comment = i
            break
        i += 1
    if quote:
        raise ValueError('Invalid deploy scalar')
    token = raw[:comment].rstrip()
    if token.startswith("'"):
        if len(token) < 2 or not token.endswith("'"):
            raise ValueError('Invalid deploy scalar')
        return token[1:-1].replace("''", "'")
    if token.startswith('"'):
        return json.loads(token)
    return token


def valid_repository(value):
    if not value or any(c.isspace() or ord(c) < 32 or ord(c) == 127 for c in value):
        return False
    if value.startswith('/'):
        return True
    if re.fullmatch(r'(?:[A-Za-z0-9._-]+@)?[A-Za-z0-9.-]+:[^/].+', value) and '://' not in value and '::' not in value:
        return True
    try:
        uri = urlsplit(value)
        return (uri.scheme in ('http', 'https', 'git', 'ssh', 'file') and
                (uri.scheme == 'file' or bool(uri.hostname)) and bool(uri.path) and
                not uri.fragment and (uri.port is None or 0 <= uri.port <= 65535))
    except ValueError:
        return False


def valid_branch(value):
    return (bool(value) and not value.startswith('-') and
            not value.endswith('.') and '..' not in value and '@{' not in value and
            not any(ord(c) <= 32 or ord(c) == 127 or c in '~^:?*[\\' for c in value) and
            all(part and not part.startswith('.') and not part.endswith('.lock') for part in value.split('/')))


def source_fields(text):
    parents, fields = [], []
    scalar_indent = None
    deploy_count = git_count = 0
    for raw in text.splitlines():
        line = raw.removeprefix('﻿')
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        indent = len(line) - len(line.lstrip(' '))
        if scalar_indent is not None and indent > scalar_indent:
            continue
        scalar_indent = None
        while parents and parents[-1][0] >= indent:
            parents.pop()
        match = re.fullmatch(r'( *)([A-Za-z_]\w*):[ \t]*(.*)', line)
        if not match:
            continue
        key, value = match[2], match[3]
        path = [name for _, name in parents]
        if path == ['Deploy', 'Git'] and key in ('Repository', 'Branch'):
            fields.append((key, value))
        if not value or value.startswith('#'):
            deploy_count += key == 'Deploy' and not path
            git_count += key == 'Git' and path == ['Deploy']
            parents.append((indent, key))
        elif value.startswith(('|', '>')):
            scalar_indent = indent
    if deploy_count != 1 or git_count != 1 or len(fields) != 2 or {k for k, _ in fields} != {'Repository', 'Branch'}:
        raise ValueError('Missing or ambiguous deploy source')
    return fields


def read_source(path):
    data = Path(path).read_bytes()
    fields = source_fields(data.decode('utf-8'))
    fields = {key: scalar(raw) for key, raw in fields}
    repo, branch = fields['Repository'], fields['Branch']
    if not valid_repository(repo) or not valid_branch(branch):
        raise ValueError('Invalid source configuration')
    return repo, branch


def main(argv):
    if len(argv) not in (2, 3) or argv[1] != '--read':
        print('FAILED deploy-config')
        return 2
    path = argv[2] if len(argv) == 3 else os.path.join(
        os.environ.get('ALASAOS_ALAS_ROOT', '/opt/alas'), 'config', 'deploy.yaml')
    try:
        repo, branch = read_source(path)
    except (OSError, UnicodeError, ValueError):
        print('FAILED deploy-config')
        return 2
    sys.stdout.reconfigure(newline='\n')
    print(repo)
    print(branch)
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))

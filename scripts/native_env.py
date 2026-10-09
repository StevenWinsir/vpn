#!/usr/bin/env python3
"""Strict parser for apps/macos/.env — public build parameters only.

Mirrors apps/macos/Sources/AsterLinkShared/ClientEnvironment.swift; both are pinned
to shared/contracts/env-v1.json. This is a parser, never `source .env`.
"""
from pathlib import Path
from urllib.parse import urlsplit
import json
import sys

ALLOWED = ('APP_ENV', 'API_BASE_URL', 'WEBSITE_BASE_URL')
API_PATH = '/api/v1/client'
UNSAFE = set('"\'$`\\;|&<>(){}*!')
LOOPBACK = {'localhost', '127.0.0.1', '::1'}


class EnvError(ValueError):
    pass


def _safe(value: str) -> bool:
    return bool(value) and all(0x20 < ord(c) < 0x7f and c not in UNSAFE for c in value)


def _url(value: str, key: str, channel: str, required_path):
    try:
        parts = urlsplit(value)
        host = parts.hostname
        parts.port  # noqa: B018 - raises on an invalid port
    except ValueError as error:
        raise EnvError('invalid URL for ' + key) from error
    scheme = parts.scheme.lower()
    if not host or parts.username is not None or parts.password is not None or parts.query or parts.fragment:
        raise EnvError('invalid URL for ' + key)
    if not (scheme == 'https' or (scheme == 'http' and channel == 'development' and host.lower() in LOOPBACK)):
        raise EnvError('invalid URL scheme/host for ' + key)
    if required_path is not None:
        if parts.path != required_path:
            raise EnvError('invalid path for ' + key)
    elif parts.path not in ('', '/'):
        raise EnvError('invalid path for ' + key)
    return value


def parse_env(text: str) -> dict:
    values = {}
    for number, raw in enumerate(text.split('\n'), 1):
        line = raw.rstrip('\r')
        if not line or line.startswith('#'):
            continue
        if '=' not in line:
            raise EnvError('malformed line %d' % number)
        key, value = line.split('=', 1)
        if key not in ALLOWED:
            raise EnvError('unknown key ' + key)
        if key in values:
            raise EnvError('duplicate key ' + key)
        if not _safe(value):
            raise EnvError('unsafe value for ' + key)
        values[key] = value
    for key in ALLOWED:
        if key not in values:
            raise EnvError('missing key ' + key)
    channel = values['APP_ENV']
    if channel not in ('development', 'release'):
        raise EnvError('invalid APP_ENV')
    _url(values['API_BASE_URL'], 'API_BASE_URL', channel, API_PATH)
    _url(values['WEBSITE_BASE_URL'], 'WEBSITE_BASE_URL', channel, None)
    return values


def load_env_file(path: Path) -> dict:
    if path.is_symlink() or not path.is_file():
        raise EnvError('env file must be a regular, non-symlink file')
    if path.stat().st_size > 8192:
        raise EnvError('env file too large')
    return parse_env(path.read_text(encoding='utf-8'))


if __name__ == '__main__':
    try:
        print(json.dumps(load_env_file(Path(sys.argv[1])), indent=2))
    except (EnvError, OSError, IndexError) as error:
        sys.exit('invalid env: ' + str(error))

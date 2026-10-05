#!/usr/bin/env python3
"""Back up and initialize the explicitly configured DEVELOPMENT database.

No real node credentials are imported. Existing customer accounts and plans are
not edited. The separate administrator password stays in a private local receipt.
"""
import argparse
import base64
import datetime
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def read_env(path):
    if path.is_symlink() or not path.is_file():
        raise RuntimeError('A regular backend/.env file is required')
    values = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        if '=' not in line:
            raise RuntimeError('Unsupported .env syntax')
        key, value = line.removeprefix('export ').split('=', 1)
        words = shlex.split(value, comments=True)
        if len(words) > 1 or not re.fullmatch(r'[A-Z][A-Z0-9_]*', key):
            raise RuntimeError('Unsupported .env syntax')
        values[key] = words[0] if words else ''
    return values


def write_private(path, data):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w') as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def run(command, *, env, cwd=ROOT / 'backend', stdin=None, output=None):
    result = subprocess.run(command, cwd=cwd, env=env, input=stdin,
        text=True, stdout=output or subprocess.PIPE, stderr=subprocess.PIPE,
        timeout=240)
    if result.returncode:
        raise RuntimeError(Path(command[0]).name + ' failed; output suppressed to protect credentials')
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--admin-email', required=True)
    parser.add_argument('--apply', action='store_true', help='explicitly authorize backup, additive migration and separate administrator creation')
    args = parser.parse_args()
    if not re.fullmatch(r'[^\s@]+@[^\s@]+\.[^\s@]+', args.admin_email) or len(args.admin_email) > 254:
        parser.error('A valid separate administrator email is required')
    cfg_file = ROOT / 'backend/.env'
    cfg = read_env(cfg_file)
    if cfg.get('APP_ENV', 'development') != 'development':
        raise RuntimeError('This bootstrap only supports APP_ENV=development')
    if cfg.get('AUTO_MIGRATE', 'false') != 'false':
        raise RuntimeError('Set AUTO_MIGRATE=false before running explicit development bootstrap')
    schema = cfg.get('DATABASE_SCHEMA', 'vpn_app')
    if not re.fullmatch(r'[a-z][a-z0-9_]{0,40}', schema):
        raise RuntimeError('Invalid database schema')
    required = ['DATABASE_HOST', 'DATABASE_NAME', 'DATABASE_USER', 'DATABASE_PASSWORD']
    if any(not cfg.get(key) for key in required):
        raise RuntimeError('Complete backend/.env database settings first')
    tools = {name: shutil.which(name) for name in ['go', 'pg_dump', 'pg_restore']}
    if not all(tools.values()):
        raise RuntimeError('Go and PostgreSQL backup tools must be on PATH')
    if not args.apply:
        print('Dry run: will back up the configured schema and .env, enable development catalog/test access, run additive migrations and create one separate audited administrator. No customer or node data will be edited.')
        return 0
    os.umask(0o077)
    runtime = ROOT / '.runtime'
    if runtime.is_symlink():
        raise RuntimeError('Refusing a symlinked runtime directory')
    runtime.mkdir(mode=0o700, exist_ok=True)
    os.chmod(runtime, 0o700)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    output = runtime / ('development-bootstrap-' + stamp + '-' + secrets.token_hex(3))
    output.mkdir(mode=0o700)
    print('Private backup and administrator receipt directory: ' + str(output.relative_to(ROOT)), flush=True)
    env = {key: value for key, value in os.environ.items() if not key.startswith(
        ('DATABASE_', 'PG', 'JWT_', 'APP_', 'HTTP_', 'CLIENT_', 'AUTO_MIGRATE',
         'COOKIE_', 'TEST_PURCHASE', 'ALLOWED_ORIGINS'))}
    env.update(cfg)
    env.update(APP_ENV='development', AUTO_MIGRATE='false', DATABASE_SCHEMA=schema)
    env.update(PGHOST=cfg['DATABASE_HOST'], PGPORT=cfg.get('DATABASE_PORT', '5432'),
        PGDATABASE=cfg['DATABASE_NAME'], PGUSER=cfg['DATABASE_USER'],
        PGPASSWORD=cfg['DATABASE_PASSWORD'], PGSSLMODE=cfg.get('DATABASE_SSLMODE', 'verify-full'),
        PGCONNECT_TIMEOUT='10', PGPASSFILE='/dev/null')
    if cfg.get('DATABASE_SSLROOTCERT'):
        env['PGSSLROOTCERT'] = cfg['DATABASE_SSLROOTCERT']
    backup = output / 'schema-before.dump'
    run([tools['pg_dump'], '--format=custom', '--no-owner', '--no-acl',
        '--schema=' + schema, '--file=' + str(backup)], env=env)
    if not backup.is_file() or backup.stat().st_size == 0:
        raise RuntimeError('Database backup was not produced; no changes made')
    run([tools['pg_restore'], '--list', str(backup)], env=env)
    write_private(output / 'backend-before.env', cfg_file.read_text())
    executable = output / 'api'
    run([tools['go'], 'build', '-o', str(executable), './cmd/api'], env=env)
    changes = {'CLIENT_NODE_CATALOG_ENABLED': 'true',
        'CLIENT_ALLOW_TEST_ENTITLEMENTS': 'true', 'TEST_PURCHASE_ENABLED': 'true'}
    if not cfg.get('NODE_ENCRYPTION_KEY'):
        changes['NODE_ENCRYPTION_KEY'] = base64.b64encode(secrets.token_bytes(32)).decode()
    else:
        try:
            valid = len(base64.b64decode(cfg['NODE_ENCRYPTION_KEY'], validate=True)) == 32
        except ValueError:
            valid = False
        if not valid:
            raise RuntimeError('Existing node key is invalid; refusing to rotate it automatically')
    lines = cfg_file.read_text().splitlines()
    for key, value in changes.items():
        pattern = re.compile(r'^\s*(?:export\s+)?' + re.escape(key) + r'=')
        lines = [line for line in lines if not pattern.match(line)]
        lines.append(key + '=' + value)
    staged = output / 'backend-new.env'
    write_private(staged, '\n'.join(lines) + '\n')
    os.replace(staged, cfg_file)
    env.update(changes)
    run([str(executable), '-migrate'], env=env)
    password = secrets.token_urlsafe(30)
    receipt = output / 'administrator.json'
    write_private(receipt, json.dumps({'email': args.admin_email.lower(), 'password': password,
        'purpose': 'development administrator only; never commit or send this file',
        'login_url': 'http://localhost:3000/login'}, indent=2) + '\n')
    run([str(executable), '-create-admin', args.admin_email], env=env,
        stdin=json.dumps({'password': password}))
    write_private(output / 'completed.json', json.dumps({
        'completed': True, 'schema_backup': str(backup.relative_to(ROOT)),
        'credentials_file': str(receipt.relative_to(ROOT)), 'customer_accounts_unchanged': True,
        'real_nodes_imported': False, 'production_ready': False}, indent=2) + '\n')
    print('Development migration and audited administrator creation completed. Read administrator.json locally. No password was printed; no real nodes were imported.')
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        print(str(error) if isinstance(error, RuntimeError) else 'Development bootstrap failed; details suppressed. Inspect the private backup directory before retrying.', file=sys.stderr)
        sys.exit(1)

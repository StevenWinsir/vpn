#!/usr/bin/env python3
"""Exercise local web/native compatibility, additive migration and backup restore.

Only creates its own PostgreSQL cluster and loopback servers. No external DSN,
remote URL, deployment command, production environment file or down migration.
"""
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import argparse
import hashlib
import http.cookiejar
import json
import os
import secrets
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
WEB_TABLES = ['users', 'plans', 'sessions', 'subscriptions', 'orders', 'nodes']
ALL_TABLES = WEB_TABLES + ['native_sessions', 'client_traffic_reports', 'node_configs', 'admin_audits']


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError('Unexpected redirect from isolated fixture')


class Client:
    def __init__(self, url):
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port:
            raise ValueError('Loopback fixture URL required')
        self.url = url
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}), NoRedirect(), urllib.request.HTTPCookieProcessor(self.jar))

    def call(self, route, body=None, *, token=None, origin=None, key=None, expected=200):
        headers = {'Content-Type': 'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        if origin:
            headers['Origin'] = origin
        if key:
            headers['Idempotency-Key'] = key
        request = urllib.request.Request(self.url + '/api/v1' + route,
            data=None if body is None else json.dumps(body).encode(), headers=headers)
        try:
            response = self.opener.open(request, timeout=15)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            value = json.loads(response.read())
            if response.status != expected:
                code = value.get('error', {}).get('code', 'unknown')
                raise AssertionError(f'{route}: HTTP {response.status}, expected {expected}; code={code}')
            return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--rollback-ref', default='HEAD')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if (output / 'summary.json').exists():
        parser.error('Use a new output directory to preserve previous evidence')
    os.umask(0o077)
    env = {key: value for key, value in os.environ.items() if not key.startswith(
        ('DATABASE_', 'PG', 'JWT_', 'APP_', 'API_', 'NEXT_PUBLIC_', 'HTTP_', 'CLIENT_', 'AUTO_MIGRATE', 'COOKIE_', 'TEST_PURCHASE', 'ALLOWED_ORIGINS'))}
    env.update(PATH='/opt/homebrew/opt/postgresql@16/bin:/opt/homebrew/bin:' + env.get('PATH', ''),
        LC_ALL='C', LANG='C', GOPROXY='off', GOTOOLCHAIN='local', CGO_ENABLED='0',
        NEXT_TELEMETRY_DISABLED='1')
    for tool in ['initdb', 'pg_ctl', 'pg_dump', 'pg_restore', 'psql', 'createdb', 'go', 'node']:
        if not shutil.which(tool, path=env['PATH']):
            raise SystemExit('Missing local tool: ' + tool)
    summary = {'passed': False, 'checks': [], 'scope': 'private synthetic web-only schema; current and Git baseline normal API binaries; real browser/Next/Gin/PostgreSQL',
        'production_modified': False, 'tls_acceptance': False, 'started_at_unix': time.time()}
    start = time.monotonic()
    with tempfile.TemporaryDirectory(prefix='vpn-release-', dir='/tmp') as temporary:
        root = Path(temporary)
        pg_started = False
        processes, streams = [], []
        data, socket_dir = root / 'postgres', root / 'sock'
        socket_dir.mkdir(mode=0o700)

        def run(name, command, *, cwd=root, environment=None, timeout=600, stdin=None):
            with (output / (name + '.log')).open('a') as log:
                result = subprocess.run(command, cwd=cwd, env=environment or env, input=stdin,
                    text=True, stdout=log, stderr=subprocess.STDOUT, timeout=timeout)
            if result.returncode:
                raise RuntimeError(name + ' failed; see its local log')

        def spawn(name, command, environment):
            stream = (output / (name + '.log')).open('a')
            streams.append(stream)
            process = subprocess.Popen(command, cwd=root, env=environment,
                stdout=stream, stderr=subprocess.STDOUT)
            processes.append(process)
            return process

        def stop(process):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
            if process in processes:
                processes.remove(process)

        def sql(database, query):
            result = subprocess.run(['psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-d', database, '-c', query],
                cwd=root, env=pg_env, capture_output=True, text=True, timeout=30)
            if result.returncode:
                raise RuntimeError('Private database assertion failed (details suppressed)')
            return result.stdout.strip()

        def fingerprint(database, tables):
            return {table: sql(database,
                f"SELECT count(*) || ':' || md5(COALESCE(string_agg(row_to_json(t)::text, E'\\n' ORDER BY {('node_id' if table == 'node_configs' else 'id')}::text),'')) FROM rehearsal.{table} t")
                for table in tables}

        def api_env(database, port):
            return dict(env, APP_ENV='test', HTTP_ADDR=f'127.0.0.1:{port}',
                DATABASE_HOST='127.0.0.1', DATABASE_PORT=str(pg_port), DATABASE_NAME=database,
                DATABASE_USER='postgres', DATABASE_PASSWORD=secret, DATABASE_SCHEMA='rehearsal',
                DATABASE_SSLMODE='disable', JWT_SECRET=jwt, ALLOWED_ORIGINS=web_url,
                COOKIE_SECURE='false', AUTO_MIGRATE='false', TEST_PURCHASE_ENABLED='true',
                CLIENT_ALLOW_TEST_ENTITLEMENTS='true', CLIENT_PROFILE_DIR=str(profiles),
                BCRYPT_COST='10', AUTH_REQUESTS_PER_MINUTE='10000', API_REQUESTS_PER_MINUTE='100000')

        def start_api(binary, database, name):
            port = free_port()
            process = spawn(name, [str(binary)], api_env(database, port))
            client = Client(f'http://127.0.0.1:{port}')
            for _ in range(150):
                if process.poll() is not None:
                    raise RuntimeError(name + ' exited before readiness')
                try:
                    client.call('/meta')
                    time.sleep(0.1)
                    if process.poll() is not None:
                        raise RuntimeError(name + ' lost its listener')
                    return process, client
                except (OSError, urllib.error.URLError):
                    time.sleep(0.1)
            raise TimeoutError(name + ' readiness timed out')

        try:
            rollback = subprocess.check_output(['git', 'rev-parse', '--verify', args.rollback_ref + '^{commit}'], cwd=ROOT, text=True).strip()
            summary['rollback_commit'] = rollback
            summary['root_commit'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
            baseline = root / 'baseline'
            baseline.mkdir()
            names = subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', rollback, 'backend'], cwd=ROOT, text=True).splitlines()
            for name in names:
                if Path(name).name.startswith('.env'):
                    continue
                destination = baseline / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(subprocess.check_output(['git', 'show', rollback + ':' + name], cwd=ROOT))
            current_binary, baseline_binary = root / 'api-current', root / 'api-baseline'
            run('build-api-current', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(current_binary), './cmd/api'], cwd=ROOT / 'backend')
            run('build-api-baseline', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(baseline_binary), './cmd/api'], cwd=baseline / 'backend')
            summary['binary_sha256'] = {name: hashlib.sha256(binary.read_bytes()).hexdigest()
                for name, binary in [('current', current_binary), ('baseline', baseline_binary)]}
            summary['distinct_backend_versions'] = summary['binary_sha256']['current'] != summary['binary_sha256']['baseline']
            secret, jwt = secrets.token_urlsafe(36), secrets.token_urlsafe(48)
            password_file = root / 'pg-password'
            password_file.write_text(secret)
            pg_port, web_port = free_port(), free_port()
            web_url = f'http://127.0.0.1:{web_port}'
            pg_env = dict(env, PGHOST=str(socket_dir), PGPORT=str(pg_port), PGUSER='postgres', PGPASSWORD=secret)
            profiles = root / 'profiles'
            profiles.mkdir(mode=0o700)
            (profiles / 'pro.yaml').write_text('proxies:\n  - {name: Drill-Only, type: http, server: 127.0.0.1, port: 9}\nproxy-groups:\n  - {name: VIP, type: select, proxies: [Drill-Only]}\nrules: ["MATCH,VIP"]\n')
            run('postgres-init', ['initdb', '-D', str(data), '-U', 'postgres', '--auth-local=scram-sha-256',
                '--auth-host=scram-sha-256', '--pwfile=' + str(password_file), '--no-locale', '-E', 'UTF8'])
            run('postgres-start', ['pg_ctl', '-D', str(data), '-l', str(root / 'postgres.log'), '-o',
                f'-k {socket_dir} -p {pg_port} -h 127.0.0.1 -c unix_socket_permissions=0700', '-w', 'start'])
            pg_started = True
            if Path(sql('postgres', "SHOW data_directory")).resolve() != data.resolve():
                raise RuntimeError('Database ownership proof failed')
            if sql('postgres', 'SHOW listen_addresses') != '127.0.0.1':
                raise RuntimeError('Database listener is not isolated')
            summary['checks'].append('new private SCRAM PostgreSQL cluster with verified data_directory and loopback-only listener')
            run('create-seed', ['createdb', 'seed'], environment=pg_env)
            run('seed-migrate', [str(baseline_binary), '-migrate'], environment=api_env('seed', free_port()))
            process, client = start_api(baseline_binary, 'seed', 'api-seed')
            email, password = 'migration-' + str(uuid.uuid4()) + '@example.invalid', secrets.token_urlsafe(24)
            client.call('/auth/register', {'name': 'Migration Drill', 'email': email, 'password': password, 'remember': True}, origin=web_url, expected=201)
            client.call('/orders/test-purchase', {'plan_id': 'pro'}, origin=web_url, key=str(uuid.uuid4()), expected=201)
            seed_fingerprint = fingerprint('seed', WEB_TABLES)
            stop(process)
            legacy_dump = root / 'web-only.dump'
            run('backup-web-only', ['pg_dump', '-Fc', '-d', 'seed', '--schema=rehearsal',
                '--exclude-table=rehearsal.native_sessions', '--exclude-table=rehearsal.client_traffic_reports', '-f', str(legacy_dump)], environment=pg_env)
            run('create-upgrade', ['createdb', 'upgrade'], environment=pg_env)
            run('restore-web-only', ['pg_restore', '--exit-on-error', '--no-owner', '-d', 'upgrade', str(legacy_dump)], environment=pg_env)
            if sql('upgrade', "SELECT to_regclass('rehearsal.native_sessions') IS NULL") != 't':
                raise AssertionError('Synthetic web-only fixture still has native tables')
            assert fingerprint('upgrade', WEB_TABLES) == seed_fingerprint
            run('migration-first', [str(current_binary), '-migrate'], environment=api_env('upgrade', free_port()))
            assert fingerprint('upgrade', WEB_TABLES) == seed_fingerprint
            first_schema = sql('upgrade', "SELECT count(*) FROM information_schema.tables WHERE table_schema='rehearsal'")
            assert first_schema == str(len(ALL_TABLES))
            run('migration-repeat', [str(current_binary), '-migrate'], environment=api_env('upgrade', free_port()))
            assert fingerprint('upgrade', WEB_TABLES) == seed_fingerprint
            summary['checks'].append('web-only fixture upgrade creates native tables; repeated additive migration preserves every pre-existing row')
            summary['legacy_fixture'] = 'synthetic web-only pg_dump subset from Git baseline, not a production snapshot'
            process, client = start_api(current_binary, 'upgrade', 'api-upgraded')
            client.call('/auth/login', {'email': email, 'password': password}, origin=web_url)
            assert client.call('/orders')['total'] == 1
            tokens = []
            for _ in range(2):
                token = Client(client.url).call('/client/login', {'email': email, 'password': password,
                    'device_id': str(uuid.uuid4()), 'platform': 'macos', 'app_version': 'release-drill'})['token']
                Client(client.url).call('/client/config', token=token)
                tokens.append(token)
            payloads = [{'sequence': 1, 'upload_bytes': 100, 'download_bytes': 200},
                        {'sequence': 1, 'upload_bytes': 300, 'download_bytes': 400}]
            def report(index):
                return Client(client.url).call('/client/traffic', payloads[index], token=tokens[index])
            with ThreadPoolExecutor(max_workers=2) as pool:
                list(pool.map(report, [0, 1]))
            for index in range(2):
                assert report(index)['replayed'] is True
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == '1000000'
            assert sql('upgrade', 'SELECT sum(used_units) FROM rehearsal.subscriptions') == '1000000'
            assert sql('upgrade', 'SELECT count(*) FROM rehearsal.client_traffic_reports') == '2'
            summary['checks'].append('two real HTTP native sessions share a balance; concurrent cumulative reports and retries debit exactly once')
            # A separate source tree cannot load frontend/.env or reuse its build output.
            stage = root / 'frontend'
            stage.mkdir()
            files = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', 'frontend'], cwd=ROOT, text=True).splitlines()
            for filename in files:
                source = ROOT / filename
                if not source.is_file() or source.is_symlink() or source.name.startswith('.env') or source.name.endswith('.tsbuildinfo'):
                    continue
                relative = Path(filename).relative_to('frontend')
                if any(part in ['.next', 'node_modules', 'test-results', 'playwright-report'] for part in relative.parts):
                    continue
                destination = stage / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, destination)
            (stage / 'node_modules').symlink_to(ROOT / 'frontend/node_modules', target_is_directory=True)
            web_env = dict(env, API_INTERNAL_URL=client.url + '/api/v1', PLAYWRIGHT_BASE_URL=web_url,
                E2E_ISOLATED='private-postgres', E2E_ARTIFACTS_DIR=str(output / 'browser'))
            chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
            if chrome.is_file():
                web_env['PLAYWRIGHT_CHROME_PATH'] = str(chrome)
            next_cli = str(stage / 'node_modules/next/dist/bin/next')
            run('web-build', ['node', next_cli, 'build', '--webpack'], cwd=stage, environment=web_env, timeout=600)
            run('web-typecheck', ['npm', 'run', 'typecheck'], cwd=stage, environment=web_env)
            run('web-lint', ['npm', 'run', 'lint'], cwd=stage, environment=web_env)
            web_stream = (output / 'web-server.log').open('a')
            streams.append(web_stream)
            web_process = subprocess.Popen(['node', next_cli, 'start', '--hostname', '127.0.0.1', '--port', str(web_port)],
                cwd=stage, env=web_env, stdout=web_stream, stderr=subprocess.STDOUT)
            processes.append(web_process)
            for _ in range(150):
                if web_process.poll() is not None:
                    raise RuntimeError('Next.js failed before readiness')
                try:
                    with urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect()).open(web_url + '/api/runtime-config', timeout=1) as response:
                        assert json.loads(response.read())['apiBaseUrl'] == '/api/v1'
                        break
                except (OSError, urllib.error.URLError):
                    time.sleep(0.1)
            else:
                raise TimeoutError('Next.js readiness timed out')
            run('browser-current', ['node', str(stage / 'node_modules/@playwright/test/cli.js'), 'test'], cwd=stage, environment=web_env, timeout=600)
            summary['checks'].append('real production Next.js / Chromium / Gin / PostgreSQL registration, purchase, cookie rotation, login/logout and responsive pages')
            stop(web_process)
            stop(process)
            before_restore = fingerprint('upgrade', ALL_TABLES)
            full_dump = root / 'upgraded.dump'
            run('backup-upgraded', ['pg_dump', '-Fc', '-d', 'upgrade', '--schema=rehearsal', '-f', str(full_dump)], environment=pg_env)
            run('create-restored', ['createdb', 'restored'], environment=pg_env)
            run('restore-upgraded', ['pg_restore', '--exit-on-error', '--no-owner', '-d', 'restored', str(full_dump)], environment=pg_env)
            assert fingerprint('restored', ALL_TABLES) == before_restore
            run('restored-migrate', [str(current_binary), '-migrate'], environment=api_env('restored', free_port()))
            assert fingerprint('restored', ALL_TABLES) == before_restore
            summary['checks'].append('custom-format full backup restored into a second database; all ten table fingerprints match before/after migration')
            process, old = start_api(baseline_binary, 'upgrade', 'api-rollback')
            old.call('/auth/login', {'email': email, 'password': password}, origin=web_url)
            assert old.call('/orders')['total'] == 1
            for index, token in enumerate(tokens):
                assert Client(old.url).call('/client/traffic', payloads[index], token=token)['replayed'] is True
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == '1000000'
            stop(process)
            process, restored = start_api(current_binary, 'restored', 'api-restored')
            restored.call('/auth/login', {'email': email, 'password': password}, origin=web_url)
            assert restored.call('/orders')['total'] == 1
            stop(process)
            summary['checks'].append('Git baseline normal binary rollback without down migration preserves web orders and native idempotency; restored current binary remains usable')
            summary['table_fingerprints_before_restore'] = before_restore
            summary['backup_sha256'] = {'web_only': hashlib.sha256(legacy_dump.read_bytes()).hexdigest(),
                                      'upgraded': hashlib.sha256(full_dump.read_bytes()).hexdigest()}
            summary['backup_retained'] = False
            summary['passed'] = True
        except Exception as error:
            summary['failure'] = f'{type(error).__name__}: {error}'
            raise
        finally:
            for process in list(reversed(processes)):
                stop(process)
            for stream in streams:
                stream.close()
            if pg_started:
                try:
                    run('postgres-stop', ['pg_ctl', '-D', str(data), '-m', 'fast', '-w', 'stop'])
                except Exception:
                    summary['passed'] = False
                    summary['cleanup_failed'] = True
            summary['duration_seconds'] = round(time.monotonic() - start, 3)
            (output / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    print(json.dumps(summary, indent=2))


if __name__ == '__main__':
    main()

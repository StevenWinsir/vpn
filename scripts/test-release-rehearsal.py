#!/usr/bin/env python3
"""Exercise local web/native compatibility, additive migration, backup restore and rollback.

Three normal API binaries take part: the legacy release that produced the existing data
(before mailbox verification), the current tree, and the rollback target (an accepted release
that already enforces verification and password versions; handoff_auth.md 12.2). The browser
stage needs a mail inbox, which a normal binary cannot have without trusting a test SMTP server
in the machine's trust store; it runs the current API as a Go test assembly instead, with the
real worker delivering into a capture inbox (TestReleaseRehearsalFixture).

Only creates its own PostgreSQL cluster and loopback servers. No external DSN,
remote URL, deployment command, production environment file or down migration.
"""
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import argparse
import base64
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
ALL_TABLES = WEB_TABLES + ['native_sessions', 'client_traffic_reports', 'node_configs', 'admin_audits',
    'node_agents', 'subscription_periods', 'connection_grants', 'node_usage_reports',
    'email_auth_challenges', 'auth_mail_jobs', 'auth_rate_buckets',
    'node_groups', 'connection_leases', 'node_assignments', 'usage_buckets', 'node_health_events']
# main before handoff_auth.md round 1: open registration, no mailbox verification.
LEGACY_REF = '67e7b37f935e654e4378c14a32bc888ccff2065f'
# Round 3 merge: verified registration, mail reset, password versions and session revocation.
ROLLBACK_REF = 'c84de59'
ORDER_COLUMN = {'node_configs': 'node_id', 'auth_rate_buckets': 'key',
    'node_assignments': "user_id::text || group_id", 'usage_buckets': "user_id::text || bucket_start"}


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

    def call(self, route, body=None, *, token=None, origin=None, key=None, expected=200, code=None):
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
            actual = value.get('error', {}).get('code', 'unknown') if isinstance(value, dict) else 'unknown'
            if response.status != expected or (code and actual != code):
                raise AssertionError(f'{route}: HTTP {response.status}, expected {expected}; code={actual}')
            return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--legacy-ref', default=LEGACY_REF, help='release that produced the existing data')
    parser.add_argument('--rollback-ref', default=ROLLBACK_REF, help='accepted release to roll back to (must enforce verification)')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if (output / 'summary.json').exists():
        parser.error('Use a new output directory to preserve previous evidence')
    os.umask(0o077)
    env = {key: value for key, value in os.environ.items() if not key.startswith(
        ('DATABASE_', 'PG', 'JWT_', 'APP_', 'API_', 'NEXT_PUBLIC_', 'HTTP_', 'CLIENT_', 'AUTO_MIGRATE', 'COOKIE_', 'TEST_PURCHASE', 'ALLOWED_ORIGINS',
         'SMTP_', 'AUTH_MAIL_', 'REGISTRATION_', 'ALLOW_REGISTRATION', 'WEBSITE_BASE_URL', 'TRUSTED_PROXIES', 'CATALOG_', 'REHEARSAL_', 'NATIVE_TEST_'))}
    env.update(PATH='/opt/homebrew/opt/postgresql@16/bin:/opt/homebrew/bin:' + env.get('PATH', ''),
        LC_ALL='C', LANG='C', GOPROXY='off', GOTOOLCHAIN='local', CGO_ENABLED='0',
        NEXT_TELEMETRY_DISABLED='1')
    for tool in ['initdb', 'pg_ctl', 'pg_dump', 'pg_restore', 'psql', 'createdb', 'go', 'node']:
        if not shutil.which(tool, path=env['PATH']):
            raise SystemExit('Missing local tool: ' + tool)
    summary = {'passed': False, 'checks': [], 'scope': 'private synthetic web-only schema from the legacy release; legacy, current and rollback normal API binaries; real browser/Next/PostgreSQL against the current API test assembly with a capture inbox',
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

        def spawn(name, command, environment, cwd=None):
            stream = (output / (name + '.log')).open('a')
            streams.append(stream)
            process = subprocess.Popen(command, cwd=cwd or root, env=environment,
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

        def columns(database, table):
            return sql(database, f"SELECT string_agg(quote_ident(column_name), ',' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='rehearsal' AND table_name='{table}'")

        def fingerprint(database, tables, only=None):
            # `only` restricts each table to the columns it had before (added columns are checked apart).
            return {table: sql(database,
                f"SELECT count(*) || ':' || md5(COALESCE(string_agg(row_to_json(t)::text, E'\\n' ORDER BY {ORDER_COLUMN.get(table, 'id')}::text),'')) "
                f"FROM (SELECT {only[table] if only else '*'} FROM rehearsal.{table}) t")
                for table in tables}

        def api_env(database, port, mail=False):
            return dict(env, **(mail_env if mail else {}), APP_ENV='test', HTTP_ADDR=f'127.0.0.1:{port}',
                DATABASE_HOST='127.0.0.1', DATABASE_PORT=str(pg_port), DATABASE_NAME=database,
                DATABASE_USER='postgres', DATABASE_PASSWORD=secret, DATABASE_SCHEMA='rehearsal',
                DATABASE_SSLMODE='disable', JWT_SECRET=jwt, ALLOWED_ORIGINS=web_url,
                COOKIE_SECURE='false', AUTO_MIGRATE='false', TEST_PURCHASE_ENABLED='true',
                CLIENT_ALLOW_TEST_ENTITLEMENTS='true', CLIENT_PROFILE_DIR=str(profiles),
                BCRYPT_COST='10', AUTH_REQUESTS_PER_MINUTE='10000', API_REQUESTS_PER_MINUTE='100000')

        def start_api(binary, database, name, mail=False):
            port = free_port()
            process = spawn(name, [str(binary)], api_env(database, port, mail))
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
            summary['root_commit'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()

            def build_ref(label, ref):
                commit = subprocess.check_output(['git', 'rev-parse', '--verify', ref + '^{commit}'], cwd=ROOT, text=True).strip()
                tree = root / label
                tree.mkdir()
                for name in subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', commit, 'backend'], cwd=ROOT, text=True).splitlines():
                    if Path(name).name.startswith('.env'):
                        continue
                    destination = tree / name
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    destination.write_bytes(subprocess.check_output(['git', 'show', commit + ':' + name], cwd=ROOT))
                binary = root / ('api-' + label)
                run('build-api-' + label, ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(binary), './cmd/api'], cwd=tree / 'backend')
                return commit, binary

            summary['legacy_commit'], legacy_binary = build_ref('legacy', args.legacy_ref)
            summary['rollback_commit'], rollback_binary = build_ref('rollback', args.rollback_ref)
            current_binary = root / 'api-current'
            run('build-api-current', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(current_binary), './cmd/api'], cwd=ROOT / 'backend')
            summary['binary_sha256'] = {name: hashlib.sha256(binary.read_bytes()).hexdigest()
                for name, binary in [('legacy', legacy_binary), ('current', current_binary), ('rollback', rollback_binary)]}
            # Identical binaries are reported, not hidden: a tree whose production backend equals the
            # rollback target makes the rollback step a same-binary restart over the newer data.
            digests = summary['binary_sha256']
            summary['distinct_backend_versions'] = len(set(digests.values())) == 3
            summary['identical_binaries'] = sorted(a + '=' + b for a, b in [('legacy', 'current'), ('legacy', 'rollback'), ('current', 'rollback')]
                if digests[a] == digests[b])
            secret, jwt = secrets.token_urlsafe(36), secrets.token_urlsafe(48)
            password_file = root / 'pg-password'
            password_file.write_text(secret)
            pg_port, web_port = free_port(), free_port()
            web_url = f'http://127.0.0.1:{web_port}'
            # Test-only mail settings: complete enough to open registration, never a real account.
            # The normal binaries are never asked for a code or a reset link; the test assembly's
            # worker delivers into its capture inbox.
            mail_env = {'ALLOW_REGISTRATION': 'true', 'SMTP_HOST': 'smtp.rehearsal.invalid', 'SMTP_PORT': '465',
                'SMTP_SECURE': 'true', 'SMTP_USER': 'sender@rehearsal.invalid', 'SMTP_PASS': secrets.token_urlsafe(18),
                'SMTP_FROM_NAME': 'AsterLink VPN', 'WEBSITE_BASE_URL': web_url,
                'AUTH_MAIL_SECRET': base64.b64encode(secrets.token_bytes(32)).decode()}
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
            run('seed-migrate', [str(legacy_binary), '-migrate'], environment=api_env('seed', free_port()))
            process, client = start_api(legacy_binary, 'seed', 'api-seed')
            # The legacy release registered without any mailbox proof: that is how existing accounts
            # were created, so it is the honest way to produce one (with a non-allow-listed domain).
            email, password = 'migration-' + str(uuid.uuid4()) + '@example.invalid', secrets.token_urlsafe(24)
            client.call('/auth/register', {'name': 'Migration Drill', 'email': email, 'password': password, 'remember': True}, origin=web_url, expected=201)
            client.call('/orders/test-purchase', {'plan_id': 'pro'}, origin=web_url, key=str(uuid.uuid4()), expected=201)
            seed_fingerprint = fingerprint('seed', WEB_TABLES)
            legacy_columns = {table: columns('seed', table) for table in WEB_TABLES}
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
            assert fingerprint('upgrade', WEB_TABLES, legacy_columns) == seed_fingerprint
            tables = set(sql('upgrade', "SELECT string_agg(table_name, ',') FROM information_schema.tables WHERE table_schema='rehearsal'").split(','))
            if tables != set(ALL_TABLES):
                raise AssertionError(f'unexpected tables after migration: missing {sorted(set(ALL_TABLES) - tables)}, extra {sorted(tables - set(ALL_TABLES))}')
            # Added user columns hold their defaults: nothing pretends an old mailbox was verified.
            unverified = "SELECT count(*) FROM rehearsal.users WHERE auth_version <> 1 OR email_verified_at IS NOT NULL OR password_changed_at IS NOT NULL"
            assert sql('upgrade', unverified) == '0'
            run('migration-repeat', [str(current_binary), '-migrate'], environment=api_env('upgrade', free_port()))
            assert fingerprint('upgrade', WEB_TABLES, legacy_columns) == seed_fingerprint
            assert sql('upgrade', unverified) == '0'
            summary['checks'].append(f'legacy web-only data upgraded to all {len(ALL_TABLES)} tables; every pre-existing column value unchanged after two migrations; added user columns at their defaults (auth_version 1, never verified)')
            summary['legacy_fixture'] = 'synthetic web-only pg_dump subset produced by the legacy release, not a production snapshot'
            process, client = start_api(current_binary, 'upgrade', 'api-upgraded')
            client.call('/auth/login', {'email': email, 'password': password}, origin=web_url)
            assert client.call('/orders')['total'] == 1
            users_before = sql('upgrade', 'SELECT count(*) FROM rehearsal.users')
            # Registration closed (no mail settings): the legacy request shape cannot create an account.
            Client(client.url).call('/auth/register', {'name': 'Bypass', 'email': 'bypass-' + str(uuid.uuid4()) + '@gmail.com',
                'password': secrets.token_urlsafe(24), 'remember': False}, origin=web_url, expected=403, code='registration_disabled')
            assert sql('upgrade', 'SELECT count(*) FROM rehearsal.users') == users_before
            summary['checks'].append('current binary with registration closed: legacy account signs in with its old password; legacy registration request refused (403 registration_disabled), no account created')
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
            stop(process)
            fixture_dir = root / 'fixture'
            fixture_dir.mkdir(mode=0o700)
            fixture = spawn('api-fixture', ['go', 'test', '-count=1', '-run', '^TestReleaseRehearsalFixture$', '-timeout=20m', './internal/api'],
                dict(api_env('upgrade', free_port(), mail=True), REHEARSAL_FIXTURE_DIR=str(fixture_dir)), cwd=ROOT / 'backend')
            fixture_ready = fixture_dir / 'fixture.json'
            deadline = time.monotonic() + 180
            while not fixture_ready.exists():
                if fixture.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('Rehearsal API test assembly failed before readiness; see api-fixture.log')
                time.sleep(0.1)
            metadata = json.loads(fixture_ready.read_text())
            client = Client(metadata['api'])

            def inbox(route, to):
                request = urllib.request.Request(client.url + route + '?to=' + urllib.parse.quote(to), headers={'X-Acceptance-Control': metadata['control']})
                for _ in range(50):
                    try:
                        with client.opener.open(request, timeout=5) as response:
                            return json.loads(response.read())
                    except urllib.error.HTTPError as error:
                        if error.code != 404:
                            raise
                    time.sleep(0.2)
                raise AssertionError('no mail delivered to the rehearsal inbox')

            web_env = dict(env, API_INTERNAL_URL=client.url + '/api/v1', PLAYWRIGHT_BASE_URL=web_url,
                E2E_ISOLATED='private-postgres', E2E_ARTIFACTS_DIR=str(output / 'browser'), REHEARSAL_FIXTURE=str(fixture_ready))
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
            summary['checks'].append('real production Next.js / Chromium / PostgreSQL: email-code registration (code from the capture inbox), purchase, cookie rotation, login/logout and responsive public pages incl. /forgot-password')
            stop(web_process)

            # The legacy account (non-allow-listed domain, never verified) resets its password by mail link.
            web = Client(client.url)
            web.call('/auth/login', {'email': email, 'password': password}, origin=web_url)
            web.call('/auth/forgot-password', {'email': email}, origin=web_url, expected=202)
            token = inbox('/fixture/reset-link', email)['token']
            web.call('/auth/reset-password/validate', {'token': token}, origin=web_url)
            new_password = secrets.token_urlsafe(24)
            web.call('/auth/reset-password', {'token': token, 'password': new_password, 'password_confirmation': new_password}, origin=web_url)
            web.call('/auth/reset-password', {'token': token, 'password': new_password + 'x', 'password_confirmation': new_password + 'x'},
                origin=web_url, expected=400, code='reset_token_invalid')
            for token_value in tokens:
                Client(client.url).call('/client/session', token=token_value, expected=401, code='native_session_expired')
                Client(client.url).call('/client/traffic', payloads[0], token=token_value, expected=401, code='native_session_expired')
            Client(client.url).call('/auth/login', {'email': email, 'password': password}, origin=web_url, expected=401, code='invalid_credentials')
            Client(client.url).call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            account = f"FROM rehearsal.users WHERE email = '{email}'"
            assert sql('upgrade', f"SELECT auth_version || ':' || (email_verified_at IS NOT NULL) || ':' || (password_changed_at IS NOT NULL) {account}") == '2:true:true'
            assert sql('upgrade', f"SELECT count(*) FROM rehearsal.native_sessions WHERE revoked_at IS NULL AND user_id = (SELECT id {account})") == '0'
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == '1000000'
            assert sql('upgrade', 'SELECT sum(used_units) FROM rehearsal.subscriptions') == '1000000'
            summary['checks'].append('legacy account mail-link reset: token single use; both native sessions refused (401 native_session_expired) for session and traffic; old password refused, new accepted; auth_version 2 and mailbox verified by the reset; confirmed traffic and balance unchanged')
            (fixture_dir / 'stop').touch()
            fixture.wait(timeout=60)
            processes.remove(fixture)
            if fixture.returncode:
                raise RuntimeError('Rehearsal API test assembly failed; see api-fixture.log')
            before_restore = fingerprint('upgrade', ALL_TABLES)
            full_dump = root / 'upgraded.dump'
            run('backup-upgraded', ['pg_dump', '-Fc', '-d', 'upgrade', '--schema=rehearsal', '-f', str(full_dump)], environment=pg_env)
            run('create-restored', ['createdb', 'restored'], environment=pg_env)
            run('restore-upgraded', ['pg_restore', '--exit-on-error', '--no-owner', '-d', 'restored', str(full_dump)], environment=pg_env)
            assert fingerprint('restored', ALL_TABLES) == before_restore
            run('restored-migrate', [str(current_binary), '-migrate'], environment=api_env('restored', free_port()))
            assert fingerprint('restored', ALL_TABLES) == before_restore
            summary['checks'].append(f'custom-format full backup restored into a second database; all {len(ALL_TABLES)} table fingerprints match before/after migration')
            # Rollback with registration switched on: the accepted release still demands a code.
            process, old = start_api(rollback_binary, 'upgrade', 'api-rollback', mail=True)
            users_before = sql('upgrade', 'SELECT count(*) FROM rehearsal.users')
            Client(old.url).call('/auth/register', {'name': 'Bypass', 'email': 'bypass-' + str(uuid.uuid4()) + '@gmail.com',
                'password': secrets.token_urlsafe(24), 'remember': False}, origin=web_url, expected=400, code='verification_required')
            assert sql('upgrade', 'SELECT count(*) FROM rehearsal.users') == users_before
            Client(old.url).call('/auth/login', {'email': email, 'password': password}, origin=web_url, expected=401, code='invalid_credentials')
            old.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            assert old.call('/orders')['total'] == 1
            for index, token_value in enumerate(tokens):
                Client(old.url).call('/client/traffic', payloads[index], token=token_value, expected=401, code='native_session_expired')
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == '1000000'
            assert sql('upgrade', f"SELECT auth_version FROM rehearsal.users WHERE email = '{email}'") == '2'
            stop(process)
            process, restored = start_api(current_binary, 'restored', 'api-restored')
            Client(restored.url).call('/auth/login', {'email': email, 'password': password}, origin=web_url, expected=401, code='invalid_credentials')
            restored.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            assert restored.call('/orders')['total'] == 1
            stop(process)
            summary['checks'].append('rollback to the accepted verification release (no down migration, registration on): code-less registration refused (400 verification_required), old password stays dead, revoked native tokens stay revoked, new password, orders and confirmed traffic preserved; restored current binary behaves the same')
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

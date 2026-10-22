#!/usr/bin/env python3
"""Exercise local web/native compatibility, additive migration, backup restore and rollback.

Three normal API binaries take part: the legacy release that produced the existing data
(before mailbox verification), the current tree, and the rollback target (the last release
before node groups, which already enforces verification and password versions;
handoff_auth.md 12.2, handoff_node_use_history_and_load_balancing_c.md 12 round 7 and 14).
On the current binary an administrator builds a node group of same-named servers with real
capacities, and native devices are admitted through both the v3 and the older /config path
under the device cap and server capacity; after the rollback the old binary serves the
dual-written columns (one line per server, the pre-group behaviour) and the next upgrade
restores the groups. The browser
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
# PR #49 merge, the last release before node groups (presence plan 14 step 5); it also has
# verified registration, mail reset, password versions and session revocation.
ROLLBACK_REF = 'a63a882'
LEASES_QUERY = "SELECT count(*) || ':' || md5(COALESCE(string_agg(id::text || state, ',' ORDER BY id), '')) FROM rehearsal.connection_leases"
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
    parser.add_argument('--rollback-ref', default=ROLLBACK_REF, help='accepted release to roll back to (must enforce verification and predate node groups)')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if (output / 'summary.json').exists():
        parser.error('Use a new output directory to preserve previous evidence')
    os.umask(0o077)
    env = {key: value for key, value in os.environ.items() if not key.startswith(
        ('DATABASE_', 'PG', 'JWT_', 'APP_', 'API_', 'NEXT_PUBLIC_', 'HTTP_', 'CLIENT_', 'AUTO_MIGRATE', 'COOKIE_', 'TEST_PURCHASE', 'ALLOWED_ORIGINS',
         'SMTP_', 'AUTH_MAIL_', 'REGISTRATION_', 'ALLOW_REGISTRATION', 'WEBSITE_BASE_URL', 'TRUSTED_PROXIES', 'CATALOG_', 'REHEARSAL_', 'NATIVE_TEST_',
         'NODE_', 'GRANT_'))}
    env.update(PATH='/opt/homebrew/opt/postgresql@16/bin:/opt/homebrew/bin:' + env.get('PATH', ''),
        LC_ALL='C', LANG='C', GOPROXY='off', GOTOOLCHAIN='local', CGO_ENABLED='0',
        NEXT_TELEMETRY_DISABLED='1')
    for tool in ['initdb', 'pg_ctl', 'pg_dump', 'pg_restore', 'psql', 'createdb', 'go', 'node']:
        if not shutil.which(tool, path=env['PATH']):
            raise SystemExit('Missing local tool: ' + tool)
    summary = {'passed': False, 'checks': [], 'scope': 'private synthetic web-only schema from the legacy release; legacy, current and rollback normal API binaries; real browser/Next/PostgreSQL against the current API test assembly with a capture inbox; node groups, online leases and usage history on the current binary, served by the rollback binary through the dual-written columns',
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

        def api_env(database, port, mail=False, catalog=False):
            return dict(env, **(mail_env if mail else {}), **(catalog_env if catalog else {}), APP_ENV='test', HTTP_ADDR=f'127.0.0.1:{port}',
                DATABASE_HOST='127.0.0.1', DATABASE_PORT=str(pg_port), DATABASE_NAME=database,
                DATABASE_USER='postgres', DATABASE_PASSWORD=secret, DATABASE_SCHEMA='rehearsal',
                DATABASE_SSLMODE='disable', JWT_SECRET=jwt, ALLOWED_ORIGINS=web_url,
                COOKIE_SECURE='false', AUTO_MIGRATE='false', TEST_PURCHASE_ENABLED='true',
                CLIENT_ALLOW_TEST_ENTITLEMENTS='true', CLIENT_PROFILE_DIR=str(profiles),
                BCRYPT_COST='10', AUTH_REQUESTS_PER_MINUTE='10000', API_REQUESTS_PER_MINUTE='100000')

        def start_api(binary, database, name, mail=False, catalog=False):
            port = free_port()
            process = spawn(name, [str(binary)], api_env(database, port, mail, catalog))
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
            # The database node catalog (servers sealed with a drill-only key); the earlier
            # stages use the static per-plan profile, as the legacy release did.
            catalog_env = {'CLIENT_NODE_CATALOG_ENABLED': 'true', 'NODE_ENCRYPTION_KEY': base64.b64encode(secrets.token_bytes(32)).decode()}
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
            ledger_units = 1000000

            # Node groups on the current binary (presence plan 3.1, 6, 12 round 7): an administrator
            # and a second customer are console accounts, as the groups drill makes them.
            admin_email, second_email = 'admin-' + str(uuid.uuid4()) + '@example.invalid', 'second-' + str(uuid.uuid4()) + '@example.invalid'
            admin_password, second_password = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
            for account, value in [(admin_email, admin_password), (second_email, second_password)]:
                run('create-account', [str(current_binary), '-create-admin', account], environment=api_env('upgrade', free_port()), stdin=json.dumps({'password': value}))
            sql('upgrade', f"UPDATE rehearsal.users SET role = 'user' WHERE email = '{second_email}'")
            process, current = start_api(current_binary, 'upgrade', 'api-groups', catalog=True)
            admin = Client(current.url)
            admin.call('/auth/login', {'email': admin_email, 'password': admin_password}, origin=web_url)
            group = admin.call('/admin/node-groups', {'name': 'JP1', 'region': 'JP', 'line_type': 'direct', 'rate_permille': 1500,
                'plan_ids': ['pro'], 'enabled': True, 'sort': 0, 'version': 0}, origin=web_url, expected=201)['group']
            servers = []
            for index in range(3):
                proxy = f'{{name: JP1, type: ss, server: 127.0.0.1, port: {19001 + index}, cipher: aes-256-gcm, password: drill-{secrets.token_hex(6)}}}'
                servers += admin.call('/admin/nodes', {'yaml': f'proxies: [{proxy}]', 'group_id': group['id'], 'max_connections': 1,
                    'label': f'jp1-{index + 1}', 'enabled': True, 'version': 0}, origin=web_url, expected=201)['nodes']
            server_ids = sorted(server['id'] for server in servers)
            assert len(set(server_ids)) == 3 and group['id'] not in server_ids

            def native_login(url, account, value):
                return Client(url).call('/client/login', {'email': account, 'password': value, 'device_id': str(uuid.uuid4()),
                    'platform': 'android', 'app_version': 'release-drill'})['token']

            def admit(url, token, expected=201, code=None):
                return Client(url).call('/client/connections', {'group_id': group['id'], 'last_sequence': 0}, token=token, key=str(uuid.uuid4()), expected=expected, code=code)
            devices = [native_login(current.url, email, new_password) for _ in range(3)]
            catalog = Client(current.url).call('/client/nodes', token=devices[0])
            assert [(node['id'], node['name']) for node in catalog['nodes']] == [(group['id'], 'JP1')], catalog['nodes']
            v3 = admit(current.url, devices[0])
            legacy = Client(current.url).call('/client/config?' + urllib.parse.urlencode({'node_id': group['id']}), token=devices[1])
            assert legacy['node_id'] == group['id'] and v3['node_id'] in server_ids and v3['connection']['group_id'] == group['id']
            admit(current.url, devices[2], 403, 'device_limit')
            second = Client(current.url)
            second.call('/auth/login', {'email': second_email, 'password': second_password}, origin=web_url)
            second.call('/orders/test-purchase', {'plan_id': 'pro'}, origin=web_url, key=str(uuid.uuid4()), expected=201)
            others = [native_login(current.url, second_email, second_password) for _ in range(2)]
            admit(current.url, others[0])
            admit(current.url, others[1], 409, 'group_full')
            Client(current.url).call('/client/traffic', {'sequence': 1, 'upload_bytes': 1000, 'download_bytes': 3000}, token=devices[0])
            Client(current.url).call('/client/traffic', {'sequence': 1, 'upload_bytes': 2000, 'download_bytes': 6000}, token=devices[1])
            ledger_units += 12000 * 1500
            owner = Client(current.url)
            owner.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            online = owner.call('/me/devices')
            assert online['online_count'] == 2 and online['device_limit'] == 2 and {device['line'] for device in online['online']} == {'JP1'}, online
            history = owner.call('/me/usage-history')
            # The window also holds the 1000 bytes the shared-balance stage reported at 1x.
            assert len(history['buckets']) == 8 and history['total_upload_bytes'] + history['total_download_bytes'] == 13000 and history['total_charged_units'] == 1000000 + 12000 * 1500, history
            listing = [entry for entry in admin.call('/admin/node-groups')['groups'] if entry['id'] == group['id']][0]
            assert sorted(server['online'] for server in listing['servers']) == [1, 1, 1], listing
            # Dual write (plan 4.1): every server's legacy columns and plan list follow its group.
            assert sql('upgrade', f"""SELECT count(*) FROM rehearsal.nodes n JOIN rehearsal.node_groups g ON g.id = n.group_id JOIN rehearsal.node_configs c ON c.node_id = n.id
                WHERE g.id = '{group['id']}' AND n.name = g.name AND n.region = g.region AND n.line_type = g.line_type AND n.rate_permille = g.rate_permille AND c.plan_ids = g.plan_ids""") == '3'
            assert sql('upgrade', "SELECT count(*) FROM rehearsal.connection_leases WHERE state = 'active'") == '3'
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == str(ledger_units)
            stop(process)
            summary['checks'].append('current binary with the database catalog: an administrator creates group JP1 (pro, 1.5x) and imports 3 same-named servers of capacity 1; the client catalog lists the group once; one device admitted through /connections and one through the older /config (echoing the group id), the third refused (403 device_limit); a second account takes the last slot and its next device is refused (409 group_full); every server 1/1 online; /me/devices shows 2 online on JP1, /me/usage-history 8 intervals holding the 12000 new bytes at 1.5x (plus the earlier 1000 at 1x); legacy server columns and plan lists dual-written from the group')
            leases_before_rollback = sql('upgrade', LEASES_QUERY)

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
            process, old = start_api(rollback_binary, 'upgrade', 'api-rollback', mail=True, catalog=True)
            users_before = sql('upgrade', 'SELECT count(*) FROM rehearsal.users')
            Client(old.url).call('/auth/register', {'name': 'Bypass', 'email': 'bypass-' + str(uuid.uuid4()) + '@gmail.com',
                'password': secrets.token_urlsafe(24), 'remember': False}, origin=web_url, expected=400, code='verification_required')
            assert sql('upgrade', 'SELECT count(*) FROM rehearsal.users') == users_before
            Client(old.url).call('/auth/login', {'email': email, 'password': password}, origin=web_url, expected=401, code='invalid_credentials')
            old.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            assert old.call('/orders')['total'] == 1
            for index, token_value in enumerate(tokens):
                Client(old.url).call('/client/traffic', payloads[index], token=token_value, expected=401, code='native_session_expired')
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == str(ledger_units)
            assert sql('upgrade', f"SELECT auth_version FROM rehearsal.users WHERE email = '{email}'") == '2'
            # Pre-group behaviour over the dual-written columns: one line per server, all named JP1.
            old_admin = Client(old.url)
            old_admin.call('/auth/login', {'email': admin_email, 'password': admin_password}, origin=web_url)
            old_servers = old_admin.call('/admin/nodes')['nodes']
            assert sorted(node['id'] for node in old_servers) == server_ids
            assert all(node['name'] == 'JP1' and node['region'] == 'JP' and node['rate_permille'] == 1500 and node['plan_ids'] == ['pro'] and node['enabled'] for node in old_servers), old_servers
            token = native_login(old.url, email, new_password)
            old_catalog = Client(old.url).call('/client/nodes', token=token)
            assert sorted(node['id'] for node in old_catalog['nodes']) == server_ids and {node['name'] for node in old_catalog['nodes']} == {'JP1'}, old_catalog['nodes']
            profile = Client(old.url).call('/client/config?' + urllib.parse.urlencode({'node_id': server_ids[0]}), token=token)
            assert profile['node_id'] == server_ids[0] and 'JP1' in profile['yaml']
            Client(old.url).call('/client/traffic', {'sequence': 1, 'upload_bytes': 400, 'download_bytes': 600}, token=token)
            ledger_units += 1000 * 1500
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == str(ledger_units)
            # The old binary neither reads nor writes the new tables.
            assert sql('upgrade', LEASES_QUERY) == leases_before_rollback
            stop(process)
            summary['checks'].append('rollback binary over the group data: admin list shows the 3 servers with the group\'s dual-written region/rate/plans; the client catalog lists one line per server (3 x "JP1", the pre-group behaviour), a server profile is served and a report is charged at the group rate (ledger continuous); lease rows untouched')
            # Upgrading again: the groups come back for the session created on the old binary.
            run('migration-after-rollback', [str(current_binary), '-migrate'], environment=api_env('upgrade', free_port()))
            process, again = start_api(current_binary, 'upgrade', 'api-reupgraded', catalog=True)
            regrouped = Client(again.url).call('/client/nodes', token=token)
            assert [(node['id'], node['name']) for node in regrouped['nodes']] == [(group['id'], 'JP1')], regrouped['nodes']
            assert sql('upgrade', 'SELECT count(*) FROM rehearsal.nodes WHERE group_id IS NULL') == '0'
            stop(process)
            summary['checks'].append('re-upgrade after the rollback: migration repeatable, no ungrouped server, the catalog lists group JP1 once again for the session created on the old binary')
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

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
restores the groups. A fourth binary, the release just before payments (handoff_payment_account.md
16), is the payment-era rollback target: it must keep serving accounts, orders and the node
groups over data that now holds gateway orders, coupons and announcements, answer the callback
route it does not have with a plain 404, and leave a live pending order for the re-upgraded
binary to settle. Before any rollback the operator command revokes impersonation sessions
(`api -revoke-impersonation`, checked with the doctor). The referral programme (handoff_coupon.md round 3) adds a fifth binary, the release
just before it (8c311ee): referral rows in every state exist on the current binary (registered through
the real code flow in the browser stage's test assembly, then qualified, rewarded and forfeited by real
payments and the real reward worker), a yearly referrer carries a reward, the old binary starts over
the new tables and columns, settles a first purchase without the check, purges accounts and opens a new
period under a reward; the next upgrade sweeps, rewards and drops the stale reward. The website of the
release before the referral work (e1fa661) is built and driven against the current backend. The browser
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
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from loopback_port import free_port

ROOT = Path(__file__).resolve().parents[1]
WEB_TABLES = ['users', 'plans', 'sessions', 'subscriptions', 'orders', 'nodes']
ALL_TABLES = WEB_TABLES + ['native_sessions', 'client_traffic_reports', 'node_configs', 'admin_audits',
    'node_agents', 'subscription_periods', 'connection_grants', 'node_usage_reports',
    'email_auth_challenges', 'auth_mail_jobs', 'auth_rate_buckets',
    'node_groups', 'connection_leases', 'node_assignments', 'usage_buckets', 'node_health_events',
    'payment_notifications', 'coupons', 'login_events', 'ip_geo_caches', 'announcements', 'announcement_reads', 'shop_settings', 'referrals']
# main before handoff_auth.md round 1: open registration, no mailbox verification.
LEGACY_REF = '67e7b37f935e654e4378c14a32bc888ccff2065f'
# PR #49 merge, the last release before node groups (presence plan 14 step 5); it also has
# verified registration, mail reset, password versions and session revocation.
ROLLBACK_REF = 'a63a882'
LEASES_QUERY = "SELECT count(*) || ':' || md5(COALESCE(string_agg(id::text || state, ',' ORDER BY id), '')) FROM rehearsal.connection_leases"
ORDER_COLUMN = {'node_configs': 'node_id', 'auth_rate_buckets': 'key',
    'node_assignments': "user_id::text || group_id", 'usage_buckets': "user_id::text || bucket_start",
    'ip_geo_caches': 'ip', 'announcement_reads': "announcement_id::text || user_id"}
# PR #58 merge: the release immediately before the payment, account and console work.
PAYMENT_ROLLBACK_REF = '9ee9066'
# PR #72 merge: the release immediately before the shop work (yearly billing, admin-defined plans).
SHOP_ROLLBACK_REF = 'a8bfebe'
# PR #77 merge: the release immediately before the referral programme (handoff_coupon.md 15.1).
REFERRAL_ROLLBACK_REF = '8c311ee'
# PR #78 merge: the last tree before the referral website; its frontend is the "old website" of the
# mixed-deployment check (handoff_coupon.md 15.1, R3-02).
OLD_WEBSITE_REF = 'e1fa661'
PAYMENT_TABLES = ['orders', 'payment_notifications', 'coupons', 'announcements', 'announcement_reads', 'login_events', 'ip_geo_caches']


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
    parser.add_argument('--payment-rollback-ref', default=PAYMENT_ROLLBACK_REF, help='release just before the payment work (must not have the payment routes)')
    parser.add_argument('--shop-rollback-ref', default=SHOP_ROLLBACK_REF, help='release just before the shop work (yearly billing, admin plans)')
    parser.add_argument('--referral-rollback-ref', default=REFERRAL_ROLLBACK_REF, help='release just before the referral programme')
    parser.add_argument('--old-website-ref', default=OLD_WEBSITE_REF, help='tree whose frontend is the website from before the referral programme')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if (output / 'summary.json').exists():
        parser.error('Use a new output directory to preserve previous evidence')
    os.umask(0o077)
    env = {key: value for key, value in os.environ.items() if not key.startswith(
        ('DATABASE_', 'PG', 'JWT_', 'APP_', 'API_', 'NEXT_PUBLIC_', 'HTTP_', 'CLIENT_', 'AUTO_MIGRATE', 'COOKIE_', 'TEST_PURCHASE', 'ALLOWED_ORIGINS',
         'SMTP_', 'AUTH_MAIL_', 'REGISTRATION_', 'ALLOW_REGISTRATION', 'WEBSITE_BASE_URL', 'TRUSTED_PROXIES', 'CATALOG_', 'REHEARSAL_', 'NATIVE_TEST_',
         'NODE_', 'GRANT_', 'PAYMENT_', 'IMPERSONATION_', 'GEOIP_'))}
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

        def doctor(environment):
            result = subprocess.run([str(doctor_binary)], cwd=root, env=environment, capture_output=True, text=True, timeout=60)
            if result.returncode:
                raise RuntimeError('doctor failed (details suppressed)')
            return json.loads(result.stdout)

        def columns(database, table):
            return sql(database, f"SELECT string_agg(quote_ident(column_name), ',' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='rehearsal' AND table_name='{table}'")

        def fingerprint(database, tables, only=None):
            # `only` restricts each table to the columns it had before (added columns are checked apart).
            return {table: sql(database,
                f"SELECT count(*) || ':' || md5(COALESCE(string_agg(row_to_json(t)::text, E'\\n' ORDER BY {ORDER_COLUMN.get(table, 'id')}::text),'')) "
                f"FROM (SELECT {only[table] if only else '*'} FROM rehearsal.{table}) t")
                for table in tables}

        def api_env(database, port, mail=False, catalog=False, payment=False, extra=None):
            # Mail and payment settings share WEBSITE_BASE_URL (the same value); later keys win.
            return dict(env, **{**(mail_env if mail else {}), **(catalog_env if catalog else {}), **(payment_env if payment else {})}, APP_ENV='test', HTTP_ADDR=f'127.0.0.1:{port}',
                DATABASE_HOST='127.0.0.1', DATABASE_PORT=str(pg_port), DATABASE_NAME=database,
                DATABASE_USER='postgres', DATABASE_PASSWORD=secret, DATABASE_SCHEMA='rehearsal',
                DATABASE_SSLMODE='disable', JWT_SECRET=jwt, ALLOWED_ORIGINS=web_url,
                COOKIE_SECURE='false', AUTO_MIGRATE='false', TEST_PURCHASE_ENABLED='true',
                CLIENT_ALLOW_TEST_ENTITLEMENTS='true', CLIENT_PROFILE_DIR=str(profiles),
                BCRYPT_COST='10', AUTH_REQUESTS_PER_MINUTE='10000', API_REQUESTS_PER_MINUTE='100000', **(extra or {}))

        def start_api(binary, database, name, mail=False, catalog=False, payment=False, extra=None):
            port = free_port()
            process = spawn(name, [str(binary)], api_env(database, port, mail, catalog, payment, extra))
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
            summary['payment_rollback_commit'], payment_rollback_binary = build_ref('payment-rollback', args.payment_rollback_ref)
            summary['shop_rollback_commit'], shop_rollback_binary = build_ref('shop-rollback', args.shop_rollback_ref)
            summary['referral_rollback_commit'], referral_rollback_binary = build_ref('referral-rollback', args.referral_rollback_ref)
            doctor_binary = root / 'doctor-current'
            run('build-doctor-current', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(doctor_binary), './cmd/doctor'], cwd=ROOT / 'backend')
            current_binary = root / 'api-current'
            run('build-api-current', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(current_binary), './cmd/api'], cwd=ROOT / 'backend')
            summary['binary_sha256'] = {name: hashlib.sha256(binary.read_bytes()).hexdigest()
                for name, binary in [('legacy', legacy_binary), ('current', current_binary), ('rollback', rollback_binary), ('payment_rollback', payment_rollback_binary), ('shop_rollback', shop_rollback_binary), ('referral_rollback', referral_rollback_binary)]}
            # Identical binaries are reported, not hidden: a tree whose production backend equals the
            # rollback target makes the rollback step a same-binary restart over the newer data.
            digests = summary['binary_sha256']
            summary['distinct_backend_versions'] = len(set(digests.values())) == 6
            summary['identical_binaries'] = sorted(a + '=' + b for a, b in [('legacy', 'current'), ('legacy', 'rollback'), ('current', 'rollback'), ('current', 'payment_rollback'), ('rollback', 'payment_rollback'), ('current', 'shop_rollback'), ('payment_rollback', 'shop_rollback'), ('current', 'referral_rollback'), ('shop_rollback', 'referral_rollback')]
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
            # Payments on the current binary: a gateway address nothing listens on (the drill never
            # contacts a real gateway; result-page status queries just fail), a secret made here,
            # and this machine's loopback as the one allowed callback source.
            payment_env = {'PAYMENT_ENABLED': 'true', 'PAYMENT_GATEWAY_URL': f'http://127.0.0.1:{free_port()}', 'PAYMENT_CHANNEL': '1001',
                'PAYMENT_SECRET': secrets.token_urlsafe(24), 'PAYMENT_NOTIFY_URL': f'{web_url}/api/v1/payments/notify',
                'PAYMENT_NOTIFY_ALLOWED_IPS': '127.0.0.1', 'WEBSITE_BASE_URL': web_url}
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

            # Referral bindings through the real registration flow (handoff_coupon.md 6): this assembly is the
            # only place that can read a mailbox code. Referrers first (the legacy account opens its page: the
            # code is created lazily, so a user from before the programme has none until then), then the
            # referees, each registering with a code; the payments come later on normal binaries.
            def web_register(prefix, referral_code=None):
                address, secret_value = f'{prefix}-{uuid.uuid4().hex[:10]}@gmail.com', secrets.token_urlsafe(24)
                receipt = Client(client.url).call('/auth/register-code', {'email': address}, origin=web_url, expected=202)
                body = {'name': 'Referral ' + prefix, 'email': address, 'password': secret_value, 'remember': False,
                    'verification_id': receipt['verification_id'], 'code': inbox('/fixture/mail', address)['code']}
                if referral_code is not None:
                    body['referral_code'] = referral_code
                Client(client.url).call('/auth/register', body, origin=web_url, expected=201)
                return address, secret_value

            def web_code(address, secret_value):
                page = Client(client.url)
                page.call('/auth/login', {'email': address, 'password': secret_value}, origin=web_url)
                summary_page = page.call('/me/referral')
                assert summary_page['enabled'] is True and summary_page['share_path'] == '/register?ref=' + summary_page['code'] and len(summary_page['code']) == 8, summary_page
                return summary_page['code']

            assert sql('upgrade', f"SELECT referral_code IS NULL FROM rehearsal.users WHERE email = '{email}'") == 't'
            ref_owner_code = web_code(email, password)
            assert sql('upgrade', f"SELECT referral_code FROM rehearsal.users WHERE email = '{email}'") == ref_owner_code
            assert web_code(email, password) == ref_owner_code
            ref_idle = web_register('refd')   # a referrer who never holds a plan
            ref_monthly = web_register('reff')  # a referrer with a monthly plan, for the stale-reward drill
            ref_codes = {'owner': ref_owner_code, 'idle': web_code(*ref_idle), 'monthly': web_code(*ref_monthly)}
            assert len(set(ref_codes.values())) == 3
            ref_people = {name: web_register(prefix, ref_codes[owner]) for name, prefix, owner in [
                ('b', 'refb', 'owner'), ('c', 'refc', 'idle'), ('e', 'refe', 'owner'), ('g', 'refg', 'monthly'),
                ('j', 'refj', 'owner'), ('h', 'refh', 'owner')]}
            assert sql('upgrade', "SELECT count(*) FROM rehearsal.referrals WHERE status = 'registered'") == '6'
            assert sql('upgrade', "SELECT count(*) FROM rehearsal.referrals WHERE referee_email_masked ~ '^[a-z0-9]{2}[*]{3}@gmail[.]com$'") == '6'
            # An unknown code, a malformed one and no code at all answer exactly alike and bind nothing.
            for odd in ['ZZZZZZZZ', '!!bad!!', None]:
                web_register('refx', odd)
            assert sql('upgrade', "SELECT count(*) FROM rehearsal.referrals") == '6'
            summary['checks'].append('referral bindings through the real registration flow on the current API assembly: a legacy account (no code before) gets one when it first opens its page and keeps it; two referrers and six referees register with codes and each binding is a registered row with a masked address; an unknown code, a malformed one and no code all register (201) and bind nothing')

            # Mixed deployment (handoff_coupon.md 15.1, R3-02): the website from before the referral work,
            # built from its own tree, against this backend.
            stop(web_process)
            old_stage = root / 'frontend-old'
            old_stage.mkdir()
            summary['old_website_commit'] = subprocess.check_output(['git', 'rev-parse', '--verify', args.old_website_ref + '^{commit}'], cwd=ROOT, text=True).strip()
            for name in subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', summary['old_website_commit'], 'frontend'], cwd=ROOT, text=True).splitlines():
                if Path(name).name.startswith('.env') or name.endswith('.tsbuildinfo'):
                    continue
                destination = old_stage / Path(name).relative_to('frontend')
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(subprocess.check_output(['git', 'show', summary['old_website_commit'] + ':' + name], cwd=ROOT))
            (old_stage / 'node_modules').symlink_to(ROOT / 'frontend/node_modules', target_is_directory=True)
            assert not (old_stage / 'src/components/referral-view.tsx').exists()
            old_env = dict(web_env, E2E_ARTIFACTS_DIR=str(output / 'browser-old-website'))
            old_cli = str(old_stage / 'node_modules/next/dist/bin/next')
            run('web-old-build', ['node', old_cli, 'build', '--webpack'], cwd=old_stage, environment=old_env, timeout=600)
            old_stream = (output / 'web-old-server.log').open('a')
            streams.append(old_stream)
            old_web = subprocess.Popen(['node', old_cli, 'start', '--hostname', '127.0.0.1', '--port', str(web_port)],
                cwd=old_stage, env=old_env, stdout=old_stream, stderr=subprocess.STDOUT)
            processes.append(old_web)
            for _ in range(150):
                if old_web.poll() is not None:
                    raise RuntimeError('The old website failed before readiness')
                try:
                    with urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect()).open(web_url + '/api/runtime-config', timeout=1) as response:
                        assert json.loads(response.read())['apiBaseUrl'] == '/api/v1'
                        break
                except (OSError, urllib.error.URLError):
                    time.sleep(0.1)
            else:
                raise TimeoutError('The old website readiness timed out')
            run('browser-old-website', ['node', str(old_stage / 'node_modules/@playwright/test/cli.js'), 'test'], cwd=old_stage, environment=old_env, timeout=600)
            summary['checks'].append('the website from before the referral programme (built from its own tree) against the current backend: registration, purchase, cookie rotation, login/logout and the public pages all pass unchanged (the old registration request carries no referral field, the new /meta field and routes are never used)')
            stop(old_web)

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
            process, current = start_api(current_binary, 'upgrade', 'api-groups', catalog=True, payment=True)
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
            # Payments, coupons, announcements and an impersonation session on the current binary
            # (handoff_payment_account.md 6, 9). The drill's loopback address is the only allowed
            # callback source, so the callbacks below are the trusted kind.
            def notify(order, cents, status='TRADE_SUCCESS'):
                money = f'{cents // 100}.{cents % 100:02d}'
                return Client(current.url).call('/payments/notify', {'trade_no': order['trade_no'], 'name': order['plan_name'], 'money': money, 'trade_status': status})

            gateway_url = payment_env['PAYMENT_GATEWAY_URL']
            owner_id = owner.call('/auth/me')['user']['id']
            quote = owner.call('/payments/quote', {'plan_id': 'pro', 'coupon_code': ''}, origin=web_url)['quote']
            assert quote['purchasable'] and quote['amount_cents'] == quote['list_price_cents'] and quote['test_price'] is False, quote
            paid_before = sql('upgrade', f"SELECT count(*) FROM rehearsal.orders WHERE user_id = '{owner_id}'")
            placed = owner.call('/payments/orders', {'plan_id': 'pro', 'pay_type': 'wxpay', 'coupon_code': ''}, origin=web_url, key=str(uuid.uuid4()), expected=201)
            order = placed['order']
            assert order['status'] == 'pending' and order['amount_cents'] == quote['amount_cents'] and placed['pay_url'].startswith(gateway_url + '/submit?'), order
            notify(order, order['amount_cents'] - 1)  # too little: never paid, left for a person
            assert owner.call('/payments/orders/' + order['id'])['order']['status'] == 'review'
            assert sql('upgrade', f"SELECT status || ':' || review_reason FROM rehearsal.orders WHERE id = '{order['id']}'") == 'review:amount_mismatch'
            placed = owner.call('/payments/orders', {'plan_id': 'pro', 'pay_type': 'alipay', 'coupon_code': ''}, origin=web_url, key=str(uuid.uuid4()), expected=201)
            order = placed['order']
            notify(order, order['amount_cents'])
            notify(order, order['amount_cents'])  # the gateway may repeat itself
            assert owner.call('/payments/orders/' + order['id'])['order']['status'] == 'paid'
            assert sql('upgrade', f"SELECT count(*) FROM rehearsal.orders WHERE user_id = '{owner_id}' AND status = 'paid'") == '1'
            assert sql('upgrade', f"SELECT count(*) FROM rehearsal.payment_notifications WHERE order_id = '{order['id']}'") == '2'
            assert sql('upgrade', f"SELECT (is_test = false) || ':' || plan_id FROM rehearsal.subscriptions WHERE user_id = '{owner_id}'") == 'true:pro'
            assert owner.call('/orders')['total'] == int(paid_before) + 2
            # An administrator creates a coupon; another customer pays with it.
            coupon = admin.call('/admin/console/coupons', {'code': 'DRILL10', 'discount_type': 'percent', 'percent_off': 10, 'plan_ids': ['pro'],
                'max_uses': 5, 'per_user_limit': 1, 'enabled': True, 'version': 0}, origin=web_url, expected=201)['coupon']
            discounted = second.call('/payments/quote', {'plan_id': 'pro', 'coupon_code': ' drill10 '}, origin=web_url)['quote']
            assert discounted['coupon']['applied'] and discounted['discount_cents'] == quote['list_price_cents'] // 10, discounted
            bought = second.call('/payments/orders', {'plan_id': 'pro', 'pay_type': 'wxpay', 'coupon_code': 'drill10'}, origin=web_url, key=str(uuid.uuid4()), expected=201)['order']
            assert bought['amount_cents'] == discounted['amount_cents'] and bought['coupon_code'] == coupon['code']
            notify(bought, bought['amount_cents'])
            assert sql('upgrade', f"SELECT used_count FROM rehearsal.coupons WHERE id = '{coupon['id']}'") == '1'
            # Announcements: one published and acknowledged by the owner, one left unread.
            for title in ['Drill notice one', 'Drill notice two']:
                note = admin.call('/admin/console/announcements', {'title': title, 'body': title + ' body', 'version': 0}, origin=web_url, expected=201)['announcement']
                admin.call(f"/admin/console/announcements/{note['id']}/publish", {'version': note['version']}, origin=web_url)
                time.sleep(1.1)  # publication order is by time
                if title.endswith('one'):
                    first_note = note
            assert [item['title'] for item in owner.call('/me/announcements')['announcements']] == ['Drill notice one', 'Drill notice two']
            owner.call(f"/me/announcements/{first_note['id']}/ack", {}, origin=web_url)
            assert [item['title'] for item in owner.call('/me/announcements')['announcements']] == ['Drill notice two']
            # A read-only impersonation session is left open on purpose.
            impersonator = Client(current.url)
            impersonator.call('/auth/login', {'email': admin_email, 'password': admin_password}, origin=web_url)
            impersonator.call(f'/admin/console/users/{owner_id}/impersonate', {'admin_password': admin_password}, origin=web_url)
            assert impersonator.call('/auth/me')['impersonation']['admin_email'] == admin_email
            impersonator.call('/orders/test-purchase', {'plan_id': 'pro'}, origin=web_url, key=str(uuid.uuid4()), expected=403, code='impersonation_read_only')
            assert impersonator.call('/me/announcements')['announcements'] == []
            current_api_env = api_env('upgrade', free_port(), catalog=True, payment=True)
            report = doctor(current_api_env)
            assert report['payment']['enabled'] and report['payment']['auto_fulfil'] and report['payment']['review_orders'] == 1, report['payment']
            assert report['impersonation']['active_sessions'] == 1 and 'impersonation_sessions_active_revoke_before_rollback' in report['warnings'], report
            assert report['missing_tables'] == [], report['missing_tables']
            serialized = json.dumps(report)
            assert payment_env['PAYMENT_SECRET'] not in serialized and payment_env['PAYMENT_GATEWAY_URL'] not in serialized and payment_env['PAYMENT_NOTIFY_URL'] not in serialized
            # The operator command ends it before any rollback (plan 16).
            run('revoke-impersonation', [str(current_binary), '-revoke-impersonation'], environment=current_api_env)
            assert 'impersonation sessions revoked: 1' in (output / 'revoke-impersonation.log').read_text()
            assert sql('upgrade', 'SELECT count(*) FROM rehearsal.sessions WHERE impersonator_id IS NOT NULL AND revoked_at IS NULL') == '0'
            assert sql('upgrade', "SELECT count(*) FROM rehearsal.admin_audits WHERE action = 'admin.impersonate_revoked'") == '1'
            impersonator.call('/auth/me', expected=401, code='unauthorized')
            report = doctor(current_api_env)
            assert report['impersonation']['active_sessions'] == 0 and 'impersonation_sessions_active_revoke_before_rollback' not in report['warnings'], report
            stale_impersonation = list(impersonator.jar)
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
            assert old.call('/orders')['total'] == 3
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
            # The revoked impersonation session stays dead on the older release as well.
            stale_older = Client(old.url)
            for cookie in stale_impersonation:
                stale_older.jar.set_cookie(cookie)
            stale_older.call('/auth/me', expected=401, code='unauthorized')
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
            # Rollback to the release just before payments (plan 16, R6-01). A live pending order is
            # open, an account is waiting to be purged, and the earlier impersonation was revoked.
            run('migration-before-payment-rollback', [str(current_binary), '-migrate'], environment=api_env('upgrade', free_port()))
            process, ready = start_api(current_binary, 'upgrade', 'api-payment-ready', catalog=True, payment=True)
            doomed_email, doomed_password = 'doomed-' + str(uuid.uuid4()) + '@example.invalid', secrets.token_urlsafe(24)
            run('create-doomed', [str(current_binary), '-create-admin', doomed_email], environment=api_env('upgrade', free_port()), stdin=json.dumps({'password': doomed_password}))
            sql('upgrade', f"UPDATE rehearsal.users SET role = 'user' WHERE email = '{doomed_email}'")
            ready_owner = Client(ready.url)
            ready_owner.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            pending = ready_owner.call('/payments/orders', {'plan_id': 'pro', 'pay_type': 'wxpay', 'coupon_code': ''}, origin=web_url, key=str(uuid.uuid4()), expected=201)['order']
            assert pending['status'] == 'pending'
            before_new_tables = {table: value for table, value in fingerprint('upgrade', ['coupons', 'payment_notifications', 'announcements', 'announcement_reads', 'ip_geo_caches']).items()}
            orders_before = fingerprint('upgrade', ['orders'])
            stop(process)
            # The account's first deletion phase has run; the purge has not (the state a rollback in
            # that window finds). Set directly, since the drill's own purge loop would finish it.
            sql('upgrade', f"UPDATE rehearsal.users SET status = 'deleting', deletion_requested_at = now() WHERE email = '{doomed_email}'")
            process, earlier = start_api(payment_rollback_binary, 'upgrade', 'api-payment-rollback', mail=True, catalog=True)
            rolled = Client(earlier.url)
            rolled.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            old_orders = rolled.call('/orders')
            assert old_orders['total'] == 4, old_orders
            assert sorted(item['status'] for item in old_orders['orders']) == sql('upgrade', f"SELECT string_agg(status, ',' ORDER BY status) FROM rehearsal.orders WHERE user_id = '{owner_id}'").split(','), old_orders
            assert {item['status'] for item in old_orders['orders']} >= {'pending', 'review', 'paid'}, old_orders
            Client(earlier.url).call('/auth/login', {'email': doomed_email, 'password': doomed_password}, origin=web_url, expected=401, code='invalid_credentials')
            old_admin = Client(earlier.url)
            old_admin.call('/auth/login', {'email': admin_email, 'password': admin_password}, origin=web_url)
            assert sorted(node['id'] for node in old_admin.call('/admin/nodes')['nodes']) == server_ids
            assert [item['id'] for item in old_admin.call('/admin/node-groups')['groups']] == [group['id']]
            earlier_token = native_login(earlier.url, email, new_password)
            assert [(node['id'], node['name']) for node in Client(earlier.url).call('/client/nodes', token=earlier_token)['nodes']] == [(group['id'], 'JP1')]
            # The group is full of leases that have not run out yet (the earlier release enforces capacity),
            # so admission itself is refused the way the current binary refuses it.
            Client(earlier.url).call('/client/config?' + urllib.parse.urlencode({'node_id': group['id']}), token=earlier_token, expected=409, code='group_full')
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == str(ledger_units)
            # The earlier release has no payment routes: a callback is a plain 404 and nothing is touched.
            try:
                urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect()).open(urllib.request.Request(earlier.url + '/api/v1/payments/notify',
                    data=json.dumps({'trade_no': pending['trade_no'], 'name': 'x', 'money': '1.00', 'trade_status': 'TRADE_SUCCESS'}).encode(), headers={'Content-Type': 'application/json'}), timeout=10)
                raise AssertionError('the earlier release answered a payment callback')
            except urllib.error.HTTPError as error:
                assert error.code == 404, error.code
            # The revoked impersonation cookies stay dead; the earlier release did not write the new data.
            stale = Client(earlier.url)
            for cookie in stale_impersonation:
                stale.jar.set_cookie(cookie)
            stale.call('/auth/me', expected=401, code='unauthorized')
            assert {table: value for table, value in fingerprint('upgrade', ['coupons', 'payment_notifications', 'announcements', 'announcement_reads', 'ip_geo_caches']).items()} == before_new_tables
            assert fingerprint('upgrade', ['orders']) == orders_before
            stop(process)
            summary['checks'].append('payments, coupon, announcements and an impersonation session on the current binary (underpaid callback goes to review, exact callback pays once and a repeat changes nothing, discounted coupon order raises used_count, announcement acknowledged once, impersonation write refused); doctor reports the open impersonation without secrets; the operator command revokes it with an audit row and the cookie is dead on the current and both older binaries')
            summary['checks'].append('rollback to the release just before payments (no down migration) with a live pending order and an account waiting to be purged: the owner signs in and lists all 4 orders (pending, paid, review), the waiting account cannot sign in, the administrator lists servers and groups, a client lists group JP1 once and is refused a full group (409 group_full), the ledger is unchanged, the payment callback route is a plain 404, coupon/notification/announcement/geo tables and every order row are byte-identical afterwards')
            # Upgrading again: the callback that could not be delivered meanwhile arrives and settles the order.
            run('migration-after-payment-rollback', [str(current_binary), '-migrate'], environment=api_env('upgrade', free_port()))
            process, back = start_api(current_binary, 'upgrade', 'api-payment-reupgraded', catalog=True, payment=True)
            Client(back.url).call('/payments/notify', {'trade_no': pending['trade_no'], 'name': pending['plan_name'], 'money': f"{pending['amount_cents'] // 100}.{pending['amount_cents'] % 100:02d}", 'trade_status': 'TRADE_SUCCESS'})
            back_owner = Client(back.url)
            back_owner.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            assert back_owner.call('/payments/orders/' + pending['id'])['order']['status'] == 'paid'
            assert back_owner.call('/me/announcements')['announcements'][0]['title'] == 'Drill notice two' and len(back_owner.call('/me/announcements')['announcements']) == 1
            assert sql('upgrade', f"SELECT count(*) FROM rehearsal.payment_notifications WHERE order_id = '{pending['id']}'") == '1'
            assert sql('upgrade', 'SELECT sum(charged_units) FROM rehearsal.client_traffic_reports') == str(ledger_units)
            stop(process)
            summary['checks'].append('re-upgrade after the payment rollback: migration repeatable, the late callback for the pending order is honoured (order paid, one notification row), earlier announcement acknowledgement and the coupon survive')
            # Shop round (handoff_shop.md 12.1, 13 round 4): yearly billing, an administrator-defined plan, a
            # soft-deleted plan, a changed global discount and a yearly-only coupon on the current binary;
            # then a rollback to the release just before the shop work (no down migration) and back.
            def utc_seconds(database, user_id, column='expires_at'):
                return int(float(sql(database, f"SELECT extract(epoch FROM {column}) FROM rehearsal.subscriptions WHERE user_id = '{user_id}'")))

            process, shop = start_api(current_binary, 'upgrade', 'api-shop', catalog=True, payment=True)
            shop_admin = Client(shop.url)
            shop_admin.call('/auth/login', {'email': admin_email, 'password': admin_password}, origin=web_url)
            shop_owner = Client(shop.url)
            shop_owner.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            shopper_email, shopper_password = 'shopper-' + str(uuid.uuid4()) + '@example.invalid', secrets.token_urlsafe(24)
            run('create-shopper', [str(current_binary), '-create-admin', shopper_email], environment=api_env('upgrade', free_port()), stdin=json.dumps({'password': shopper_password}))
            sql('upgrade', f"UPDATE rehearsal.users SET role = 'user' WHERE email = '{shopper_email}'")
            shopper = Client(shop.url)
            shopper.call('/auth/login', {'email': shopper_email, 'password': shopper_password}, origin=web_url)
            shopper_id = shopper.call('/auth/me')['user']['id']
            # The upgrade left every seed plan with the default yearly terms (9折, 365 days) and the settings row at 90.
            listing = shop_owner.call('/plans')
            assert listing['yearly_discount_percent'] == 90, listing
            seeded = {item['id']: item for item in listing['plans']}
            assert {'starter', 'pro', 'max'} <= set(seeded), sorted(seeded)
            for item in seeded.values():
                yearly = item['yearly']
                assert yearly and yearly['discount_percent'] == 90 and yearly['duration_days'] == 365, item
                assert yearly['original_cents'] == item['price_cents'] * 12 and yearly['price_cents'] == item['price_cents'] * 12 * 90 // 100, item
            assert [item['id'] for item in listing['plans'] if item['featured']] == ['pro']
            # An administrator defines a plan (with markup in its name) and a second one that is then soft-deleted.
            plan_body = {'id': 'drill-plan', 'name': 'Drill <b>plan</b>', 'description': '', 'note': 'internal drill note', 'price_cents': 1234, 'duration_days': 30,
                'traffic_bytes': 5 * 2 ** 30, 'max_devices': 1, 'allow_dedicated': False, 'yearly_enabled': True, 'yearly_duration_days': 365,
                'yearly_traffic_bytes': None, 'yearly_discount_percent': None, 'featured': False, 'sort': 90, 'active': True, 'version': 0}
            drill_plan = shop_admin.call('/admin/console/plans', plan_body, origin=web_url, expected=201)['plan']
            gone_plan = shop_admin.call('/admin/console/plans', dict(plan_body, id='drill-gone', name='Drill gone', active=False), origin=web_url, expected=201)['plan']
            shop_admin.call('/admin/console/plans/drill-gone/delete', {'version': gone_plan['version']}, origin=web_url)
            assert sql('upgrade', "SELECT (deleted_at IS NOT NULL) || ':' || active FROM rehearsal.plans WHERE id = 'drill-gone'") == 'true:false'
            settings = shop_admin.call('/admin/console/shop-settings')['settings']
            shop_admin.call('/admin/console/shop-settings', {'yearly_discount_percent': 85, 'version': settings['version']}, origin=web_url)
            listing = shop_owner.call('/plans')
            assert listing['yearly_discount_percent'] == 85 and 'drill-gone' not in {item['id'] for item in listing['plans']}
            drilled = {item['id']: item for item in listing['plans']}['drill-plan']
            assert drilled['name'] == 'Drill <b>plan</b>' and 'note' not in drilled and 'version' not in drilled, drilled
            assert drilled['yearly']['price_cents'] == 1234 * 12 * 85 // 100 and drilled['yearly']['original_cents'] == 1234 * 12, drilled
            assert sql('upgrade', "SELECT count(*) FROM rehearsal.admin_audits WHERE action LIKE 'admin.plan%' AND detail LIKE '%internal drill note%'") == '0'
            assert int(sql('upgrade', "SELECT count(*) FROM rehearsal.admin_audits WHERE action LIKE 'admin.plan%'")) >= 3
            shop_admin.call('/admin/console/plans/no-such-plan/delete', {'version': 1}, origin=web_url, expected=404)
            shop_owner.call('/admin/console/plans', origin=web_url, expected=403)
            pro_yearly = {item['id']: item for item in listing['plans']}['pro']['yearly']
            assert pro_yearly['discount_percent'] == 85, pro_yearly
            # A yearly-only coupon: refused for monthly, applied to yearly.
            yearly_coupon = shop_admin.call('/admin/console/coupons', {'code': 'DRILLYEAR', 'discount_type': 'percent', 'percent_off': 10, 'plan_ids': ['pro'],
                'max_uses': 5, 'per_user_limit': 1, 'enabled': True, 'billing_cycles': 'yearly', 'version': 0}, origin=web_url, expected=201)['coupon']
            monthly_quote = shopper.call('/payments/quote', {'plan_id': 'drill-plan', 'coupon_code': ''}, origin=web_url)['quote']
            assert monthly_quote['billing_cycle'] == 'monthly' and monthly_quote['amount_cents'] == 1234, monthly_quote
            cycle_refused = shop_owner.call('/payments/quote', {'plan_id': 'pro', 'coupon_code': 'DRILLYEAR'}, origin=web_url)['quote']['coupon']
            assert cycle_refused and not cycle_refused['applied'], cycle_refused
            yearly_quote = shop_owner.call('/payments/quote', {'plan_id': 'pro', 'coupon_code': 'DRILLYEAR', 'billing_cycle': 'yearly'}, origin=web_url)['quote']
            assert yearly_quote['coupon']['applied'] and yearly_quote['billing_cycle'] == 'yearly' and yearly_quote['original_cents'] == pro_yearly['original_cents'], yearly_quote
            # Runbook step before a rollback (docs/payment-operations.md): disable coupons with a cycle scope.
            shop_admin.call('/admin/console/coupons/' + yearly_coupon['id'], {'code': 'DRILLYEAR', 'discount_type': 'percent', 'percent_off': 10, 'plan_ids': ['pro'], 'max_uses': 5, 'per_user_limit': 1,
                'enabled': False, 'billing_cycles': 'yearly', 'version': yearly_coupon['version']}, origin=web_url)
            assert sql('upgrade', "SELECT enabled || ':' || billing_cycles FROM rehearsal.coupons WHERE code = 'DRILLYEAR'") == 'false:yearly'
            # Owner: a yearly order paid onto the live monthly subscription (365 days added; the monthly allowance joins the
            # first 30-day cycle and is refreshed every 30 days from the payment, handoff_shop.md D2 revised), another left pending.
            owner_expires, owner_limit = utc_seconds('upgrade', owner_id), int(sql('upgrade', f"SELECT traffic_limit_bytes FROM rehearsal.subscriptions WHERE user_id = '{owner_id}'"))
            paid_year = shop_owner.call('/payments/orders', {'plan_id': 'pro', 'pay_type': 'wxpay', 'coupon_code': '', 'billing_cycle': 'yearly',
                'expected_amount_cents': pro_yearly['price_cents']}, origin=web_url, key=str(uuid.uuid4()), expected=201)['order']
            assert paid_year['billing_cycle'] == 'yearly' and paid_year['amount_cents'] == pro_yearly['price_cents'] and paid_year['original_cents'] == pro_yearly['original_cents'], paid_year
            shop_owner.call('/payments/orders', {'plan_id': 'pro', 'pay_type': 'wxpay', 'coupon_code': '', 'billing_cycle': 'yearly',
                'expected_amount_cents': pro_yearly['price_cents'] - 1}, origin=web_url, key=str(uuid.uuid4()), expected=409, code='price_changed')
            money = lambda cents: f'{cents // 100}.{cents % 100:02d}'
            Client(shop.url).call('/payments/notify', {'trade_no': paid_year['trade_no'], 'name': paid_year['plan_name'], 'money': money(paid_year['amount_cents']), 'trade_status': 'TRADE_SUCCESS'})
            assert shop_owner.call('/payments/orders/' + paid_year['id'])['order']['status'] == 'paid'
            assert utc_seconds('upgrade', owner_id) - owner_expires == 365 * 86400
            yearly_traffic = int(sql('upgrade', f"SELECT traffic_limit_bytes FROM rehearsal.subscriptions WHERE user_id = '{owner_id}'")) - owner_limit
            assert pro_yearly['quota_cycle_days'] == 30 and pro_yearly['traffic_bytes'] == 12 * pro_yearly['quota_cycle_bytes'], pro_yearly
            assert yearly_traffic > 0 and yearly_traffic == pro_yearly['quota_cycle_bytes'], (yearly_traffic, pro_yearly)
            cycle = sql('upgrade', f"SELECT quota_cycle_days || ':' || quota_cycle_bytes || ':' || (quota_cycle_starts_at IS NOT NULL) FROM rehearsal.subscriptions WHERE user_id = '{owner_id}'")
            assert cycle == f"30:{pro_yearly['quota_cycle_bytes']}:true", cycle
            assert sql('upgrade', f"SELECT quota_cycle_days || ':' || quota_cycle_bytes || ':' || traffic_bytes FROM rehearsal.orders WHERE id = '{paid_year['id']}'") == \
                f"30:{pro_yearly['quota_cycle_bytes']}:{pro_yearly['traffic_bytes']}"
            snapshot = sql('upgrade', f"SELECT billing_cycle || ':' || cycle_discount_percent || ':' || original_cents || ':' || (ent_max_devices IS NOT NULL) FROM rehearsal.orders WHERE id = '{paid_year['id']}'")
            assert snapshot == f"yearly:85:{pro_yearly['original_cents']}:true", snapshot
            pending_year = shop_owner.call('/payments/orders', {'plan_id': 'pro', 'pay_type': 'alipay', 'coupon_code': '', 'billing_cycle': 'yearly',
                'expected_amount_cents': pro_yearly['price_cents']}, origin=web_url, key=str(uuid.uuid4()), expected=201)['order']
            # The shopper pays for the administrator-defined plan monthly on the current binary.
            drill_order = shopper.call('/payments/orders', {'plan_id': 'drill-plan', 'pay_type': 'wxpay', 'coupon_code': ''}, origin=web_url, key=str(uuid.uuid4()), expected=201)['order']
            Client(shop.url).call('/payments/notify', {'trade_no': drill_order['trade_no'], 'name': drill_order['plan_name'], 'money': money(drill_order['amount_cents']), 'trade_status': 'TRADE_SUCCESS'})
            assert sql('upgrade', f"SELECT plan_id || ':' || max_devices || ':' || is_test FROM rehearsal.subscriptions WHERE user_id = '{shopper_id}'") == 'drill-plan:1:false'
            shop_fingerprints = fingerprint('upgrade', ['plans', 'shop_settings', 'coupons'])
            stop(process)
            summary['checks'].append('shop on the current binary: upgrade leaves every seed plan with yearly terms at 90 (price = monthly x 12 x 90 / 100, 365 days, pro featured); an administrator defines plan drill-plan (HTML in its name, an internal note) and soft-deletes drill-gone, the public list shows the former as plain text without note or version and never the latter; global discount 85 reprices the yearly tiers; a yearly-only coupon is refused for monthly and applied to yearly; a yearly order with a wrong expected_amount_cents is refused (409 price_changed) and creates nothing; a paid yearly order adds exactly 365 days, carries one monthly allowance into the first 30-day cycle and switches the subscription to a 30-day refresh, and stores cycle, discount, original price, refresh and entitlement snapshot; a monthly order on the new plan activates it with its own device quota')
            # Rollback to the release just before the shop work: the pending yearly order is settled by the old fulfilOrder.
            process, shop_old = start_api(shop_rollback_binary, 'upgrade', 'api-shop-rollback', catalog=True, payment=True)
            old_owner = Client(shop_old.url)
            old_owner.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            old_plans = {item['id']: item for item in old_owner.call('/plans')['plans']}
            assert {'starter', 'pro', 'max', 'drill-plan'} <= set(old_plans) and 'drill-gone' not in old_plans, sorted(old_plans)
            assert old_plans['drill-plan']['price_cents'] == 1234 and old_plans['pro']['price_cents'] == seeded['pro']['price_cents'], old_plans
            orders_old = {item['id']: item for item in old_owner.call('/orders')['orders']}
            assert orders_old[paid_year['id']]['status'] == 'paid' and orders_old[pending_year['id']]['status'] == 'pending'
            old_shopper = Client(shop_old.url)
            old_shopper.call('/auth/login', {'email': shopper_email, 'password': shopper_password}, origin=web_url)
            old_shopper_order = old_shopper.call('/payments/orders', {'plan_id': 'drill-plan', 'pay_type': 'alipay', 'coupon_code': ''}, origin=web_url, key=str(uuid.uuid4()), expected=201)['order']
            assert old_shopper_order['amount_cents'] == 1234, old_shopper_order
            # A pending yearly order is fulfilled with its snapshot (365 days and the nominal twelve months of traffic at once:
            # the old binary knows no refresh, so it grants what the shop sold before the refresh existed).
            before_expires, before_limit = utc_seconds('upgrade', owner_id), int(sql('upgrade', f"SELECT traffic_limit_bytes FROM rehearsal.subscriptions WHERE user_id = '{owner_id}'"))
            Client(shop_old.url).call('/payments/notify', {'trade_no': pending_year['trade_no'], 'name': pending_year['plan_name'], 'money': money(pending_year['amount_cents']), 'trade_status': 'TRADE_SUCCESS'})
            assert old_owner.call('/payments/orders/' + pending_year['id'])['order']['status'] == 'paid'
            assert utc_seconds('upgrade', owner_id) - before_expires == 365 * 86400
            assert int(sql('upgrade', f"SELECT traffic_limit_bytes FROM rehearsal.subscriptions WHERE user_id = '{owner_id}'")) - before_limit == pro_yearly['traffic_bytes']
            assert sql('upgrade', f"SELECT billing_cycle FROM rehearsal.orders WHERE id = '{pending_year['id']}'") == 'yearly'
            # The old binary sells monthly terms (also on the administrator-defined plan) and writes orders without the new columns.
            old_pending = old_owner.call('/payments/orders', {'plan_id': 'pro', 'pay_type': 'wxpay', 'coupon_code': ''}, origin=web_url, key=str(uuid.uuid4()), expected=201)['order']
            assert old_pending['amount_cents'] == seeded['pro']['price_cents'], old_pending
            Client(shop_old.url).call('/payments/notify', {'trade_no': old_shopper_order['trade_no'], 'name': old_shopper_order['plan_name'], 'money': money(old_shopper_order['amount_cents']), 'trade_status': 'TRADE_SUCCESS'})
            assert sql('upgrade', f"SELECT status FROM rehearsal.orders WHERE id = '{old_shopper_order['id']}'") == 'paid'
            assert fingerprint('upgrade', ['plans', 'shop_settings', 'coupons']) == shop_fingerprints
            stop(process)
            summary['checks'].append('rollback to the release just before the shop work (no down migration, yearly-only coupon disabled first): it starts over the new columns and tables, lists the seed plans and the administrator-defined plan at monthly terms and never the soft-deleted one, settles the pending yearly order with 365 days and the nominal yearly traffic from the order snapshot (no refresh on that binary), sells monthly terms (including the new plan) and leaves plans, shop_settings and coupons byte-identical')
            run('migration-after-shop-rollback', [str(current_binary), '-migrate'], environment=api_env('upgrade', free_port()))
            process, shop_back = start_api(current_binary, 'upgrade', 'api-shop-reupgraded', catalog=True, payment=True)
            for order_id in (old_pending['id'], old_shopper_order['id']):
                assert sql('upgrade', f"SELECT billing_cycle || ':' || (ent_max_devices IS NULL) || ':' || (ent_allow_dedicated IS NULL) FROM rehearsal.orders WHERE id = '{order_id}'") == 'monthly:true:true'
            before_expires = utc_seconds('upgrade', owner_id)
            Client(shop_back.url).call('/payments/notify', {'trade_no': old_pending['trade_no'], 'name': old_pending['plan_name'], 'money': money(old_pending['amount_cents']), 'trade_status': 'TRADE_SUCCESS'})
            back_shop_owner = Client(shop_back.url)
            back_shop_owner.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            assert back_shop_owner.call('/payments/orders/' + old_pending['id'])['order']['status'] == 'paid'
            assert utc_seconds('upgrade', owner_id) - before_expires == int(sql('upgrade', "SELECT duration_days FROM rehearsal.plans WHERE id = 'pro'")) * 86400
            back_listing = back_shop_owner.call('/plans')
            assert back_listing['yearly_discount_percent'] == 85 and 'drill-gone' not in {item['id'] for item in back_listing['plans']}
            assert sql('upgrade', "SELECT count(*) FROM rehearsal.plans WHERE id = 'drill-gone' AND deleted_at IS NOT NULL") == '1'
            assert sql('upgrade', f"SELECT plan_id FROM rehearsal.subscriptions WHERE user_id = '{shopper_id}'") == 'drill-plan'
            # Put the global discount back to its default, as the user's own check list does (U-12).
            back_admin = Client(shop_back.url)
            back_admin.call('/auth/login', {'email': admin_email, 'password': admin_password}, origin=web_url)
            settings = back_admin.call('/admin/console/shop-settings')['settings']
            back_admin.call('/admin/console/shop-settings', {'yearly_discount_percent': 90, 'version': settings['version']}, origin=web_url)
            assert back_shop_owner.call('/plans')['yearly_discount_percent'] == 90
            stop(process)
            summary['checks'].append('re-upgrade after the shop rollback: migration repeatable; orders the old binary wrote read as monthly with empty entitlement snapshots and are fulfilled from the plan row (30 days); the settings row (85), the soft-deleted plan and the administrator-defined plan survive; the discount goes back to 90')
            # Referral programme (handoff_coupon.md 15, 16 round 3, R3-01): rows in every state, a yearly referrer,
            # a rollback to the release just before it and the upgrade back. REFERRAL_POLL_INTERVAL is 1 s so the
            # real worker settles within seconds.
            fast = {'REFERRAL_POLL_INTERVAL': '1s'}
            ref_db = 'upgrade'

            def ref_id(address):
                return sql(ref_db, f"SELECT id FROM rehearsal.users WHERE email = '{address}'")

            ref_ids = {name: ref_id(person[0]) for name, person in ref_people.items()}
            ref_ids.update(owner=owner_id, idle=ref_id(ref_idle[0]), monthly=ref_id(ref_monthly[0]))

            def wait_for(query, expected, what, seconds=40):
                deadline = time.monotonic() + seconds
                while time.monotonic() < deadline:
                    if sql(ref_db, query) == expected:
                        return
                    time.sleep(0.3)
                raise AssertionError(f'{what}: expected {expected!r}, got {sql(ref_db, query)!r}')

            def row(name, columns):
                return sql(ref_db, f"SELECT {columns} FROM rehearsal.referrals WHERE referee_id = '{ref_ids[name]}'")

            def sub_of(name, columns):
                return sql(ref_db, f"SELECT {columns} FROM rehearsal.subscriptions WHERE user_id = '{ref_ids[name]}'")

            def settle_at(url, order, status='TRADE_SUCCESS'):
                return Client(url).call('/payments/notify', {'trade_no': order['trade_no'], 'name': order['plan_name'], 'money': money(order['amount_cents']), 'trade_status': status})

            def pay_for(url, person, plan='pro'):
                account = Client(url)
                account.call('/auth/login', {'email': person[0], 'password': person[1]}, origin=web_url)
                order = account.call('/payments/orders', {'plan_id': plan, 'pay_type': 'wxpay', 'coupon_code': ''}, origin=web_url, key=str(uuid.uuid4()), expected=201)['order']
                settle_at(url, order)
                assert account.call('/payments/orders/' + order['id'])['order']['status'] == 'paid'
                return order

            reward_of = lambda order: order['amount_cents'] * 2 ** 30 // 100
            owner_person = (email, new_password)
            owner_limit_0 = int(sub_of('owner', 'traffic_limit_bytes'))
            owner_cycle = sub_of('owner', "quota_cycle_days || ':' || quota_cycle_bytes")
            owner_plan = sub_of('owner', 'plan_id')
            assert owner_cycle.startswith('30:') and sub_of('owner', 'bonus_bytes') == '0', owner_cycle
            pro_traffic = sql(ref_db, "SELECT traffic_bytes FROM rehearsal.plans WHERE id = 'pro'")

            # A. The current binary with the programme on: real payments, the real reward worker.
            process, ref = start_api(current_binary, ref_db, 'api-referral', catalog=True, payment=True, extra=fast)
            assert ref.call('/meta')['referral_enabled'] is True
            pay_for(ref.url, ref_monthly)  # the monthly referrer holds a plan
            order_b = pay_for(ref.url, ref_people['b'])
            wait_for(f"SELECT status FROM rehearsal.referrals WHERE referee_id = '{ref_ids['b']}'", 'rewarded', 'reward for B')
            grant_b = reward_of(order_b)
            assert row('b', "first_order_id || ':' || first_order_cents || ':' || reward_bytes || ':' || granted_bytes") == f"{order_b['id']}:{order_b['amount_cents']}:{grant_b}:{grant_b}"
            # The yearly referrer: the reward is added to the cycle allowance and remembered as a bonus of this period.
            assert int(sub_of('owner', 'traffic_limit_bytes')) - owner_limit_0 == grant_b
            assert sub_of('owner', "bonus_bytes || ':' || (bonus_starts_at = starts_at) || ':' || plan_id") == f"{grant_b}:true:{owner_plan}"
            assert sub_of('owner', "quota_cycle_days || ':' || quota_cycle_bytes") == owner_cycle
            owner_api = Client(ref.url)
            owner_api.call('/auth/login', {'email': owner_person[0], 'password': owner_person[1]}, origin=web_url)
            mine = owner_api.call('/me/referral')
            assert mine['has_active_plan'] is True and mine['current_bonus_bytes'] == grant_b and mine['stats'] == {'registered_count': 4, 'purchased_count': 1,
                'pending_count': 0, 'rewarded_count': 1, 'rewarded_bytes': grant_b, 'forfeited_count': 0}, mine
            assert owner_api.call('/me/dashboard')['bonus_bytes'] == grant_b
            listed = owner_api.call('/me/referrals')
            assert listed['total'] == 4 and all(set(item) == {'email_masked', 'registered_at', 'status', 'reward_bytes', 'granted_bytes', 'granted_at', 'forfeit_reason', 'referee_deleted'} for item in listed['items']), listed
            # A referrer without a plan: the reward is forfeited for good.
            pay_for(ref.url, ref_people['c'])
            wait_for(f"SELECT status FROM rehearsal.referrals WHERE referee_id = '{ref_ids['c']}'", 'forfeited', 'forfeit for C')
            assert row('c', "forfeit_reason || ':' || granted_bytes") == 'referrer_no_plan:0'
            idle_api = Client(ref.url)
            idle_api.call('/auth/login', {'email': ref_idle[0], 'password': ref_idle[1]}, origin=web_url)
            idle = idle_api.call('/me/referral')
            assert idle['has_active_plan'] is False and idle['stats']['forfeited_count'] == 1 and idle['stats']['rewarded_bytes'] == 0, idle
            # A monthly referrer: a one-shot allowance plus the bonus.
            order_g = pay_for(ref.url, ref_people['g'])
            wait_for(f"SELECT status FROM rehearsal.referrals WHERE referee_id = '{ref_ids['g']}'", 'rewarded', 'reward for G')
            grant_g = reward_of(order_g)
            assert sub_of('monthly', "bonus_bytes || ':' || (bonus_starts_at = starts_at)") == f"{grant_g}:true"
            assert row('e', 'status') == 'registered'
            stop(process)
            # Switched off: a first purchase is still recorded, nothing is paid out.
            process, off = start_api(current_binary, ref_db, 'api-referral-off', catalog=True, payment=True, extra=dict(fast, REFERRAL_ENABLED='false'))
            assert off.call('/meta')['referral_enabled'] is False
            order_j = pay_for(off.url, ref_people['j'])
            assert row('j', "status || ':' || first_order_id") == f"qualified:{order_j['id']}"
            time.sleep(4)
            assert row('j', 'status') == 'qualified' and int(sub_of('owner', 'traffic_limit_bytes')) - owner_limit_0 == grant_b
            stop(process)
            summary['checks'].append('referral programme on the current binary (real payments, real worker, REFERRAL_POLL_INTERVAL 1 s): the first paid order of a referee qualifies the row and the reward arrives within seconds (1 yuan = 1 GiB of the amount paid); a yearly referrer keeps its 30-day cycle and carries the reward as a bonus anchored to its period; a referrer without a plan forfeits (referrer_no_plan) and the page says so; a monthly referrer gets the bonus on its one-shot allowance; with REFERRAL_ENABLED=false the first purchase is recorded as qualified and nothing is paid out')

            # B. Rollback to the release just before the programme (no down migration).
            rid = {name: row(name, 'id') for name in ('e', 'c')}
            core = lambda: sql(ref_db, f"SELECT count(*) || ':' || md5(COALESCE(string_agg(row_to_json(t)::text, E'\\n' ORDER BY id::text),'')) FROM (SELECT * FROM rehearsal.referrals WHERE id NOT IN ('{rid['e']}', '{rid['c']}')) t")
            codes_state = lambda: sql(ref_db, f"SELECT count(*) || ':' || md5(COALESCE(string_agg(id::text || COALESCE(referral_code, ''), ',' ORDER BY id), '')) FROM rehearsal.users WHERE id IN ({', '.join(repr(v) for k, v in ref_ids.items() if k not in ('e', 'idle'))})")
            core_before, codes_before = core(), codes_state()
            process, old_ref = start_api(referral_rollback_binary, ref_db, 'api-referral-rollback', mail=True, catalog=True, payment=True)
            old_owner = Client(old_ref.url)
            old_owner.call('/auth/login', {'email': owner_person[0], 'password': owner_person[1]}, origin=web_url)
            old_dash = old_owner.call('/me/dashboard')
            assert old_dash['subscription']['traffic_limit_bytes'] == int(sub_of('owner', 'traffic_limit_bytes')) and 'bonus_bytes' not in old_dash
            # A first purchase on the old binary: fulfilled, but nobody checks the referral.
            order_h = pay_for(old_ref.url, ref_people['h'])
            assert row('h', 'status') == 'registered'
            # A reward belongs to its period: the monthly referrer's period ends and the old binary opens a new one
            # without knowing the bonus.
            grant_before = sub_of('monthly', 'bonus_bytes')
            sql(ref_db, f"UPDATE rehearsal.subscriptions SET starts_at = now() - interval '40 days', expires_at = now() - interval '10 days' WHERE user_id = '{ref_ids['monthly']}'")
            pay_for(old_ref.url, ref_monthly)
            assert sub_of('monthly', 'traffic_limit_bytes') == pro_traffic
            assert sub_of('monthly', 'bonus_bytes') == grant_before and sub_of('monthly', 'bonus_starts_at <> starts_at') == 't'
            # Account deletion on the old binary: a referee (its row stays, anonymised by the key) and a
            # referrer (its rows go with it).
            for person in (ref_people['e'], ref_idle):
                leaving = Client(old_ref.url)
                leaving.call('/auth/login', {'email': person[0], 'password': person[1]}, origin=web_url)
                leaving.call('/me/account/delete', {'email': person[0], 'password': person[1]}, origin=web_url)
            wait_for(f"SELECT count(*) FROM rehearsal.users WHERE id IN ('{ref_ids['e']}', '{ref_ids['idle']}')", '0', 'purge on the old binary', seconds=150)
            assert sql(ref_db, f"SELECT status || ':' || (referee_id IS NULL) FROM rehearsal.referrals WHERE id = '{rid['e']}'") == 'registered:true'
            assert sql(ref_db, f"SELECT count(*) FROM rehearsal.referrals WHERE id = '{rid['c']}'") == '0'
            assert core() == core_before, 'a referral row changed on the old binary'
            assert codes_state() == codes_before, 'a referral code changed on the old binary'
            assert sql(ref_db, "SELECT count(*) FROM rehearsal.referrals") == '5'
            stop(process)
            summary['checks'].append('rollback to the release just before the referral programme (8c311ee, no down migration): it starts over the new columns and table, the yearly referrer signs in and its dashboard matches the database, a referee pays a first order that is fulfilled without any qualification (the row stays registered), the monthly referrer buys again after expiry and the new period leaves the old bonus behind its anchor, deleting a referee keeps its row with a null referee and deleting a referrer removes its rows, and every other referral row and every referral code is byte-identical afterwards')

            # C. Upgrade again: the sweep, the rewards and the stale bonus.
            run('migration-after-referral-rollback', [str(current_binary), '-migrate'], environment=api_env(ref_db, free_port()))
            process, back = start_api(current_binary, ref_db, 'api-referral-reupgraded', catalog=True, payment=True, extra=fast)
            wait_for(f"SELECT status FROM rehearsal.referrals WHERE referee_id = '{ref_ids['h']}'", 'rewarded', 'sweep and reward for H')
            wait_for(f"SELECT status FROM rehearsal.referrals WHERE referee_id = '{ref_ids['j']}'", 'rewarded', 'reward for J after the switch is back on')
            grant_h, grant_j = reward_of(order_h), reward_of(order_j)
            assert row('h', "first_order_id || ':' || first_order_cents || ':' || granted_bytes") == f"{order_h['id']}:{order_h['amount_cents']}:{grant_h}"
            assert row('j', 'granted_bytes') == str(grant_j)
            owner_total = grant_b + grant_h + grant_j
            assert int(sub_of('owner', 'traffic_limit_bytes')) - owner_limit_0 == owner_total and sub_of('owner', 'bonus_bytes') == str(owner_total)
            owner_again = Client(back.url)
            owner_again.call('/auth/login', {'email': owner_person[0], 'password': owner_person[1]}, origin=web_url)
            mine = owner_again.call('/me/referral')
            assert mine['current_bonus_bytes'] == owner_total and mine['stats'] == {'registered_count': 4, 'purchased_count': 3, 'pending_count': 0,
                'rewarded_count': 3, 'rewarded_bytes': owner_total, 'forfeited_count': 0}, mine
            assert owner_again.call('/me/dashboard')['bonus_bytes'] == owner_total
            assert sorted(item['referee_deleted'] for item in owner_again.call('/me/referrals')['items']) == [False, False, False, True]
            # The bonus of the old period is gone from the monthly referrer's view and allowance, its history is not.
            monthly_api = Client(back.url)
            monthly_api.call('/auth/login', {'email': ref_monthly[0], 'password': ref_monthly[1]}, origin=web_url)
            dash = monthly_api.call('/me/dashboard')
            assert dash['bonus_bytes'] == 0 and str(dash['subscription']['traffic_limit_bytes']) == pro_traffic, dash
            seen = monthly_api.call('/me/referral')
            assert seen['current_bonus_bytes'] == 0 and seen['stats']['rewarded_bytes'] == grant_g and row('g', "status || ':' || granted_bytes") == f"rewarded:{grant_g}", seen
            # A yearly refresh keeps what is left of the bonus: 31 days into the period, one cycle has ended.
            sql(ref_db, f"UPDATE rehearsal.subscriptions SET starts_at = now() - interval '31 days', quota_cycle_starts_at = now() - interval '31 days', bonus_starts_at = now() - interval '31 days' WHERE user_id = '{owner_id}'")
            cycle_bytes = int(sub_of('owner', 'quota_cycle_bytes'))
            refreshed = owner_again.call('/me/dashboard')
            assert refreshed['subscription']['traffic_limit_bytes'] == cycle_bytes + owner_total and refreshed['bonus_bytes'] == owner_total and refreshed['quota_resets_at'], refreshed
            # Doctor: counts and switches only, a warning for a reward that waits too long.
            back_env = api_env(ref_db, free_port(), catalog=True, payment=True, extra=fast)
            report = doctor(back_env)
            assert report['referral'] == {'enabled': True, 'max_reward_gib': 0, 'registered': 1, 'qualified': 0, 'rewarded': 4, 'forfeited': 0, 'stale_qualified': 0}, report['referral']
            assert 'referral_rewards_stale' not in report['warnings'] and report['missing_tables'] == []
            sql(ref_db, f"UPDATE rehearsal.referrals SET status = 'qualified', qualified_at = now() - interval '11 minutes', next_attempt_at = now() + interval '1 hour', reward_bytes = 1 WHERE id = '{rid['e']}'")
            report = doctor(back_env)
            assert report['referral']['stale_qualified'] == 1 and 'referral_rewards_stale' in report['warnings'], report
            serialized = json.dumps(report)
            # (The allowed registration domains are configuration, so a domain alone proves nothing; addresses, ids and codes do.)
            assert not any(value in serialized for value in list(ref_codes.values()) + [person[0] for person in [ref_idle, ref_monthly, *ref_people.values()]] + [email] + list(ref_ids.values())), 'doctor output carries a code, an address or an id'
            sql(ref_db, f"UPDATE rehearsal.referrals SET status = 'registered', qualified_at = NULL, next_attempt_at = NULL, reward_bytes = 0 WHERE id = '{rid['e']}'")
            assert 'referral_rewards_stale' not in doctor(back_env)['warnings']
            stop(process)
            reupgrade_log = (output / 'api-referral-reupgraded.log').read_text()
            assert 'referral sweep qualified 1 referrals' in reupgrade_log and 'referral rewards settled' in reupgrade_log, 'the worker logs show no sweep or no reward'
            people_text = list(ref_codes.values()) + [person[0] for person in [ref_idle, ref_monthly, *ref_people.values()]]
            for log_name in ['api-referral', 'api-referral-off', 'api-referral-rollback', 'api-referral-reupgraded']:
                text = (output / (log_name + '.log')).read_text()
                assert not any(value in text for value in people_text), log_name + ' carries a referral code or an address'
            summary['checks'].append('upgrade again after the rollback: migration repeatable; the sweep qualifies the first order the old binary fulfilled (the order id is the old binary\'s) and the worker pays it, the reward held back while the switch was off is paid when it is on again; the yearly referrer ends with the three rewards as a bonus on its cycle allowance; the monthly referrer\'s old bonus is invisible (dashboard 0, allowance = plan) while its paid history stays; deleted referee shown as deleted; 31 days into the period the yearly refresh keeps the whole bonus (cycle allowance + bonus); doctor reports the counts, warns about a qualified row older than ten minutes and carries no code, address or id; the real process logs hold the sweep and reward lines and no code or address')
            process, restored = start_api(current_binary, 'restored', 'api-restored')
            Client(restored.url).call('/auth/login', {'email': email, 'password': password}, origin=web_url, expected=401, code='invalid_credentials')
            restored.call('/auth/login', {'email': email, 'password': new_password}, origin=web_url)
            assert restored.call('/orders')['total'] == 3
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

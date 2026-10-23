#!/usr/bin/env python3
"""Drill the node-group migration (handoff_node_use_history_and_load_balancing_c.md 4.2,
12 round 1 and 14 step 1) with real API binaries on a private PostgreSQL cluster.

1. The legacy release (default a63a882, before node groups) migrates an empty schema and
   produces real data through its own HTTP API: an administrator imports servers, a user
   buys a test plan, a native session selects a line and reports traffic.
2. The current tree runs `-migrate` twice. Every pre-existing column value must stay
   identical; each server becomes a one-server group with the same id, max_connections 1000.
3. The current binary serves the same client catalog, profile, website catalog and admin
   list byte for byte (round 1 changes no client-visible behaviour), and doctor reports
   the new tables.
4. Rollback: the legacy binary starts on the migrated database, reads the catalog,
   creates, edits and deletes servers; a further current migration adopts what it wrote.

Only creates its own cluster and loopback servers. No external DSN, remote URL, deployment
command, production environment file or down migration.
"""
from pathlib import Path
import argparse
import base64
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
# main after PR #49, the last release without node groups.
LEGACY_REF = 'a63a882'
LEGACY_TABLES = ['users', 'plans', 'sessions', 'subscriptions', 'orders', 'nodes', 'native_sessions',
    'client_traffic_reports', 'node_configs', 'admin_audits', 'node_agents', 'subscription_periods',
    'connection_grants', 'node_usage_reports', 'email_auth_challenges', 'auth_mail_jobs', 'auth_rate_buckets']
NEW_TABLES = ['node_groups', 'connection_leases', 'node_assignments', 'usage_buckets', 'node_health_events']
ORDER_COLUMN = {'node_configs': 'node_id', 'auth_rate_buckets': 'key'}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError('Unexpected redirect from isolated fixture')


class Client:
    def __init__(self, url, origin):
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port:
            raise ValueError('Loopback fixture URL required')
        self.url, self.origin = url, origin
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, route, body=None, *, method=None, token=None, key=None, expected=200):
        headers = {'Content-Type': 'application/json', 'Origin': self.origin}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        if key:
            headers['Idempotency-Key'] = key
        request = urllib.request.Request(self.url + '/api/v1' + route, method=method,
            data=None if body is None else json.dumps(body).encode(), headers=headers)
        try:
            response = self.opener.open(request, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            if response.status != expected:
                try:
                    code = json.loads(raw).get('error', {}).get('code', 'unknown')
                except ValueError:
                    code = 'unparseable'
                raise AssertionError(f'{route}: HTTP {response.status}, expected {expected}; code={code}')
            return json.loads(raw)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--legacy-ref', default=LEGACY_REF, help='release before node groups (source of the data and rollback target)')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if (output / 'summary.json').exists():
        parser.error('Use a new output directory to preserve previous evidence')
    os.umask(0o077)
    env = {key: value for key, value in os.environ.items() if not key.startswith(
        ('DATABASE_', 'PG', 'JWT_', 'APP_', 'API_', 'NEXT_PUBLIC_', 'HTTP_', 'CLIENT_', 'NODE_', 'AUTO_MIGRATE', 'COOKIE_',
         'TEST_PURCHASE', 'ALLOWED_ORIGINS', 'SMTP_', 'AUTH_MAIL_', 'REGISTRATION_', 'ALLOW_REGISTRATION', 'WEBSITE_BASE_URL',
         'TRUSTED_PROXIES', 'GRANT_', 'NATIVE_TEST_'))}
    env.update(PATH='/opt/homebrew/opt/postgresql@16/bin:/opt/homebrew/bin:' + env.get('PATH', ''),
        LC_ALL='C', LANG='C', GOPROXY='off', GOTOOLCHAIN='local', CGO_ENABLED='0')
    for tool in ['initdb', 'pg_ctl', 'psql', 'createdb', 'go']:
        if not shutil.which(tool, path=env['PATH']):
            raise SystemExit('Missing local tool: ' + tool)
    summary = {'passed': False, 'checks': [], 'scope': 'private synthetic data produced through the legacy release API; legacy and current normal API binaries; no production snapshot',
        'production_modified': False, 'started_at_unix': time.time()}
    start = time.monotonic()
    with tempfile.TemporaryDirectory(prefix='vpn-groups-', dir='/tmp') as temporary:
        root = Path(temporary)
        pg_started = False
        processes, streams = [], []
        data, socket_dir = root / 'postgres', root / 'sock'
        socket_dir.mkdir(mode=0o700)

        def run(name, command, *, cwd=root, environment=None, stdin=None, capture=False):
            with (output / (name + '.log')).open('a') as log:
                result = subprocess.run(command, cwd=cwd, env=environment or env, input=stdin, text=True,
                    stdout=subprocess.PIPE if capture else log, stderr=log if capture else subprocess.STDOUT, timeout=600)
            if result.returncode:
                raise RuntimeError(name + ' failed; see its local log')
            return result.stdout

        def spawn(name, command, environment):
            stream = (output / (name + '.log')).open('a')
            streams.append(stream)
            process = subprocess.Popen(command, cwd=root, env=environment, stdout=stream, stderr=subprocess.STDOUT)
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
            processes.remove(process)

        def sql(query):
            result = subprocess.run(['psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-d', 'drill', '-c', query],
                cwd=root, env=pg_env, capture_output=True, text=True, timeout=30)
            if result.returncode:
                raise RuntimeError('Private database assertion failed (details suppressed)')
            return result.stdout.strip()

        def columns(table):
            return sql(f"SELECT string_agg(quote_ident(column_name), ',' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='drill' AND table_name='{table}'")

        def fingerprint(only):
            return {table: sql(f"SELECT count(*) || ':' || md5(COALESCE(string_agg(row_to_json(t)::text, E'\\n' ORDER BY {ORDER_COLUMN.get(table, 'id')}::text),'')) "
                f"FROM (SELECT {only[table]} FROM drill.{table}) t") for table in LEGACY_TABLES}

        def api_env(port):
            return dict(env, APP_ENV='test', HTTP_ADDR=f'127.0.0.1:{port}', DATABASE_HOST='127.0.0.1', DATABASE_PORT=str(pg_port),
                DATABASE_NAME='drill', DATABASE_USER='postgres', DATABASE_PASSWORD=secret, DATABASE_SCHEMA='drill',
                DATABASE_SSLMODE='disable', JWT_SECRET=jwt, ALLOWED_ORIGINS=origin, COOKIE_SECURE='false', AUTO_MIGRATE='false',
                TEST_PURCHASE_ENABLED='true', CLIENT_ALLOW_TEST_ENTITLEMENTS='true', CLIENT_NODE_CATALOG_ENABLED='true',
                NODE_ENCRYPTION_KEY=node_key, BCRYPT_COST='10', AUTH_REQUESTS_PER_MINUTE='10000', API_REQUESTS_PER_MINUTE='100000')

        def start_api(binary, name):
            port = free_port()
            process = spawn(name, [str(binary)], api_env(port))
            client = Client(f'http://127.0.0.1:{port}', origin)
            for _ in range(150):
                if process.poll() is not None:
                    raise RuntimeError(name + ' exited before readiness')
                try:
                    client.call('/meta')
                    return process, client
                except (OSError, urllib.error.URLError):
                    time.sleep(0.1)
            raise TimeoutError(name + ' readiness timed out')

        def build_ref(ref):
            commit = subprocess.check_output(['git', 'rev-parse', '--verify', ref + '^{commit}'], cwd=ROOT, text=True).strip()
            tree = root / 'legacy'
            for name in subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', commit, 'backend'], cwd=ROOT, text=True).splitlines():
                if Path(name).name.startswith('.env'):
                    continue
                destination = tree / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(subprocess.check_output(['git', 'show', commit + ':' + name], cwd=ROOT))
            binary = root / 'api-legacy'
            run('build-api-legacy', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(binary), './cmd/api'], cwd=tree / 'backend')
            return commit, binary

        def login(client, email, password):
            client.call('/auth/login', {'email': email, 'password': password})
            return client

        def observe(client, token, node_id, admin):
            # Everything a client or the website can see, minus server clocks and per-call ids.
            catalog = client.call('/client/nodes', token=token)
            config = client.call('/client/config?' + urllib.parse.urlencode({'node_id': node_id, 'last_sequence': 1}), token=token)
            bootstrap = user_web.call('/client/bootstrap')
            listing = admin.call('/admin/nodes')
            return {'catalog_revision': catalog['revision'], 'catalog_nodes': catalog['nodes'], 'profile_yaml': config['yaml'],
                'profile_version': config['version'], 'profile_nodes': config['nodes'], 'profile_node_id': config['node_id'],
                'bootstrap_nodes': bootstrap['nodes'], 'admin_nodes': listing['nodes']}

        try:
            summary['root_commit'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
            summary['legacy_commit'], legacy_binary = build_ref(args.legacy_ref)
            current_binary, doctor_binary = root / 'api-current', root / 'doctor-current'
            run('build-api-current', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(current_binary), './cmd/api'], cwd=ROOT / 'backend')
            run('build-doctor-current', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(doctor_binary), './cmd/doctor'], cwd=ROOT / 'backend')
            secret, jwt = secrets.token_urlsafe(36), secrets.token_urlsafe(48)
            node_key = base64.b64encode(secrets.token_bytes(32)).decode()
            password_file = root / 'pg-password'
            password_file.write_text(secret)
            pg_port = free_port()
            origin = f'http://127.0.0.1:{free_port()}'
            pg_env = dict(env, PGHOST=str(socket_dir), PGPORT=str(pg_port), PGUSER='postgres', PGPASSWORD=secret)
            run('postgres-init', ['initdb', '-D', str(data), '-U', 'postgres', '--auth-local=scram-sha-256', '--auth-host=scram-sha-256',
                '--pwfile=' + str(password_file), '--no-locale', '-E', 'UTF8'])
            run('postgres-start', ['pg_ctl', '-D', str(data), '-l', str(root / 'postgres.log'), '-o',
                f'-k {socket_dir} -p {pg_port} -h 127.0.0.1 -c unix_socket_permissions=0700', '-w', 'start'])
            pg_started = True
            run('create-drill', ['createdb', 'drill'], environment=pg_env)
            if Path(sql('SHOW data_directory')).resolve() != data.resolve() or sql('SHOW listen_addresses') != '127.0.0.1':
                raise RuntimeError('Database ownership/isolation proof failed')
            summary['checks'].append('new private SCRAM PostgreSQL cluster with verified data_directory and loopback-only listener')

            # 1. Legacy data, produced by the legacy release itself.
            run('legacy-migrate', [str(legacy_binary), '-migrate'], environment=api_env(free_port()))
            admin_email, user_email = f'admin-{uuid.uuid4()}@example.invalid', f'user-{uuid.uuid4()}@example.invalid'
            admin_password, user_password = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
            for email, password in [(admin_email, admin_password), (user_email, user_password)]:
                run('legacy-create-account', [str(legacy_binary), '-create-admin', email], environment=api_env(free_port()), stdin=json.dumps({'password': password}))
            # The legacy release cannot register without mail; demote the second console account to a customer.
            sql(f"UPDATE drill.users SET role = 'user' WHERE email = '{user_email}'")
            process, client = start_api(legacy_binary, 'api-legacy-seed')
            admin_web = login(Client(client.url, origin), admin_email, admin_password)

            def proxy(name, port):
                return f'{{name: {name}, type: ss, server: 127.0.0.1, port: {port}, cipher: aes-256-gcm, password: drill-{secrets.token_hex(6)}}}'
            created = admin_web.call('/admin/nodes', {'yaml': f'proxies: [{proxy("JP1", 18001)}, {proxy("JP2", 18002)}]', 'region': 'JP', 'line_type': 'direct', 'rate_permille': 1000, 'enabled': True, 'plan_ids': [], 'version': 0}, expected=201)['nodes']
            created += admin_web.call('/admin/nodes', {'yaml': f'proxies: [{proxy("HK 专线", 18003)}]', 'region': 'HK', 'line_type': 'dedicated', 'rate_permille': 1500, 'enabled': True, 'plan_ids': ['max', 'pro'], 'version': 0}, expected=201)['nodes']
            created += admin_web.call('/admin/nodes', {'yaml': f'proxies: [{proxy("US-Starter", 18004)}]', 'region': 'US', 'line_type': 'direct', 'rate_permille': 500, 'enabled': True, 'plan_ids': ['starter'], 'version': 0}, expected=201)['nodes']
            disabled = admin_web.call('/admin/nodes', {'yaml': f'proxies: [{proxy("Retired", 18005)}]', 'region': 'DE', 'line_type': 'direct', 'rate_permille': 800, 'enabled': False, 'plan_ids': [], 'version': 0}, expected=201)['nodes'][0]
            # One edit so a server carries version 2 into the migration.
            jp1 = created[0]
            edited = admin_web.call('/admin/nodes/' + jp1['id'], {'yaml': f'proxies: [{proxy("JP1", 18011)}]', 'region': 'JP', 'line_type': 'direct', 'rate_permille': 1200, 'enabled': True, 'plan_ids': [], 'version': 1})['nodes'][0]
            assert edited['version'] == 2
            user_web = login(Client(client.url, origin), user_email, user_password)
            user_web.call('/orders/test-purchase', {'plan_id': 'pro'}, key=str(uuid.uuid4()), expected=201)
            token = Client(client.url, origin).call('/client/login', {'email': user_email, 'password': user_password,
                'device_id': str(uuid.uuid4()), 'platform': 'macos', 'app_version': 'groups-drill'})['token']
            native = Client(client.url, origin)
            native.call('/client/config?node_id=' + jp1['id'], token=token)
            native.call('/client/traffic', {'sequence': 1, 'upload_bytes': 1000, 'download_bytes': 3000}, token=token)
            before_view = observe(native, token, jp1['id'], admin_web)
            stop(process)
            counts = {table: sql(f'SELECT count(*) FROM drill.{table}') for table in ['nodes', 'node_configs', 'native_sessions', 'client_traffic_reports', 'subscriptions', 'admin_audits']}
            assert counts['nodes'] == '5' and counts['client_traffic_reports'] == '1' and counts['native_sessions'] == '1', counts
            if sql("SELECT to_regclass('drill.node_groups') IS NULL") != 't':
                raise AssertionError('legacy release already has node groups; wrong --legacy-ref')
            legacy_columns = {table: columns(table) for table in LEGACY_TABLES}
            seed_fingerprint = fingerprint(legacy_columns)
            summary['legacy_data'] = counts
            summary['checks'].append('legacy release produced 5 servers (batch import, dedicated, plan-limited, disabled, one edited to v2), a paid test subscription, a bound native session and one ledger row through its own API')

            # 2. Additive migration, twice.
            for attempt in ['first', 'repeat']:
                run('migrate-' + attempt, [str(current_binary), '-migrate'], environment=api_env(free_port()))
                assert fingerprint(legacy_columns) == seed_fingerprint, 'pre-existing column values changed by the ' + attempt + ' migration'
            tables = set(sql("SELECT string_agg(table_name, ',') FROM information_schema.tables WHERE table_schema='drill'").split(','))
            assert tables == set(LEGACY_TABLES + NEW_TABLES), sorted(tables ^ set(LEGACY_TABLES + NEW_TABLES))
            mismatched = sql("""SELECT count(*) FROM drill.nodes n LEFT JOIN drill.node_groups g ON g.id = n.group_id LEFT JOIN drill.node_configs c ON c.node_id = n.id
                WHERE g.id IS NULL OR n.group_id <> n.id OR g.name <> n.name OR g.region <> n.region OR g.line_type <> n.line_type OR g.rate_permille <> n.rate_permille
                OR g.enabled <> n.enabled OR g.version <> n.version OR g.plan_ids <> COALESCE(c.plan_ids, '[]') OR n.max_connections <> 1000 OR n.label <> '' OR n.unhealthy_until IS NOT NULL""")
            assert mismatched == '0' and sql('SELECT count(*) FROM drill.node_groups') == '5', 'servers not mapped one-to-one onto same-id groups'
            assert sql('SELECT count(*) FROM drill.connection_leases') == sql('SELECT count(*) FROM drill.usage_buckets') == '0'
            summary['checks'].append('two current migrations: all pre-existing column values in all 17 legacy tables unchanged (row fingerprints); 5 new tables; every server has a same-id group copying name/region/line/rate/enabled/version/plan_ids; max_connections 1000; no leases or history invented')

            # 3. Same client-visible behaviour on the current binary.
            process, client = start_api(current_binary, 'api-current')
            admin_web, user_web = login(Client(client.url, origin), admin_email, admin_password), login(Client(client.url, origin), user_email, user_password)
            native = Client(client.url, origin)
            after_view = observe(native, token, jp1['id'], admin_web)
            for key in before_view:
                assert after_view[key] == before_view[key], 'client-visible output changed: ' + key
            (output / 'client-view.json').write_text(json.dumps({'revision': after_view['catalog_revision'], 'catalog_nodes': after_view['catalog_nodes'], 'profile_version': after_view['profile_version']}, ensure_ascii=False, indent=2))
            stop(process)
            summary['checks'].append('current binary over migrated data: native catalog (nodes + revision), selected profile YAML/version, website bootstrap catalog and admin list identical to the legacy release byte for byte')
            doctor = json.loads(run('doctor', [str(doctor_binary)], environment=api_env(free_port()), capture=True))
            assert doctor['missing_tables'] == [] and doctor['node_groups'] == 5 and doctor['ungrouped_servers'] == 0, doctor
            summary['doctor'] = {key: doctor[key] for key in ['missing_tables', 'node_groups', 'ungrouped_servers', 'enabled_nodes']}
            summary['checks'].append('doctor on the migrated database: no missing tables, 5 node groups, 0 ungrouped servers')

            # 4. Rollback to the legacy binary over the migrated data.
            process, client = start_api(legacy_binary, 'api-legacy-rollback')
            admin_web, user_web = login(Client(client.url, origin), admin_email, admin_password), login(Client(client.url, origin), user_email, user_password)
            native = Client(client.url, origin)
            rollback_view = observe(native, token, jp1['id'], admin_web)
            for key in before_view:
                assert rollback_view[key] == before_view[key], 'rollback output changed: ' + key
            native.call('/client/traffic', {'sequence': 2, 'upload_bytes': 2000, 'download_bytes': 5000}, token=token)
            added = admin_web.call('/admin/nodes', {'yaml': f'proxies: [{proxy("SG1", 18006)}]', 'region': 'SG', 'line_type': 'direct', 'rate_permille': 900, 'enabled': True, 'plan_ids': ['pro'], 'version': 0}, expected=201)['nodes'][0]
            admin_web.call('/admin/nodes/' + created[1]['id'], {'yaml': f'proxies: [{proxy("JP2", 18012)}]', 'region': 'JP', 'line_type': 'direct', 'rate_permille': 1100, 'enabled': True, 'plan_ids': [], 'version': 1})
            admin_web.call('/admin/nodes/' + disabled['id'], {'version': 1}, method='DELETE')
            assert sql(f"SELECT group_id IS NULL AND max_connections = 1000 FROM drill.nodes WHERE id = '{added['id']}'") == 't'
            stop(process)
            summary['checks'].append('rollback: legacy binary starts on the migrated database, serves the identical catalog/profile/admin list, accepts a traffic report, creates (defaults: no group, max_connections 1000), edits and deletes servers')
            run('migrate-after-rollback', [str(current_binary), '-migrate'], environment=api_env(free_port()))
            assert sql(f"SELECT g.name || '|' || g.plan_ids || '|' || g.rate_permille FROM drill.node_groups g JOIN drill.nodes n ON n.group_id = g.id WHERE n.id = '{added['id']}'") == 'SG1|["pro"]|900'
            assert sql('SELECT count(*) FROM drill.nodes WHERE group_id IS NULL') == '0'
            # Groups win over edits made by the rollback binary (14 step 5); the deleted server leaves an empty, invisible group.
            assert sql(f"SELECT rate_permille FROM drill.node_groups WHERE id = '{created[1]['id']}'") == '1000'
            assert sql(f"SELECT count(*) FROM drill.node_groups WHERE id = '{disabled['id']}'") == '1'
            # Cumulative 1000/3000 then 2000/5000 bytes at the edited JP1 rate of 1.2x.
            assert sql("SELECT count(*) || ':' || sum(charged_units) FROM drill.client_traffic_reports") == '2:' + str(7000 * 1200)
            assert sql('SELECT used_units FROM drill.subscriptions') == str(7000 * 1200)
            doctor = json.loads(run('doctor-after-rollback', [str(doctor_binary)], environment=api_env(free_port()), capture=True))
            assert doctor['ungrouped_servers'] == 0 and doctor['node_groups'] == 6, doctor
            summary['checks'].append('re-upgrade after rollback: the server the legacy binary created is adopted into a same-id group; existing groups keep their values (group table wins); the deleted server leaves an empty group; ledger balance continuous across versions')
            summary['passed'] = True
        finally:
            for process in list(processes):
                stop(process)
            for stream in streams:
                stream.close()
            if pg_started:
                subprocess.run(['pg_ctl', '-D', str(data), '-m', 'fast', '-w', 'stop'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            summary['duration_seconds'] = round(time.monotonic() - start, 1)
            (output / 'summary.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2))
    print(json.dumps({'passed': summary['passed'], 'checks': len(summary['checks']), 'output': str(output)}))
    if not summary['passed']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Real browser -> Gin/private PostgreSQL -> real Mihomo catalog acceptance."""
from pathlib import Path
import argparse
import json
import os
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def free_port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return listener.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--skip-build', action='store_true')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if (output / 'summary.json').exists():
        parser.error('Use a new output directory to preserve evidence')
    os.umask(0o077)
    env = {key: value for key, value in os.environ.items() if not key.startswith(
        ('DATABASE_', 'PG', 'JWT_', 'APP_', 'API_', 'NEXT_PUBLIC_', 'HTTP_', 'CLIENT_', 'NODE_ENCRYPTION_KEY', 'AUTO_MIGRATE', 'COOKIE_', 'TEST_PURCHASE', 'ALLOWED_ORIGINS'))}
    env.update(PATH='/opt/homebrew/bin:' + env.get('PATH', ''), LC_ALL='C', LANG='C',
        GOPROXY='off', GOTOOLCHAIN='local', CGO_ENABLED='0', NEXT_TELEMETRY_DISABLED='1')
    for tool in ['initdb', 'pg_ctl', 'go', 'node']:
        if not shutil.which(tool, path=env['PATH']):
            raise SystemExit('Missing local tool: ' + tool)
    summary = {'passed': False, 'checks': [], 'scope': 'private PostgreSQL, real Next.js/browser, Gin and Mihomo; no OS TUN or system proxy mutation'}
    processes, streams = [], []

    def run(name, command, cwd, environment=env, timeout=600):
        with (output / (name + '.log')).open('w') as log:
            subprocess.run(command, cwd=cwd, env=environment, stdout=log,
                stderr=subprocess.STDOUT, check=True, timeout=timeout)

    def spawn(name, command, cwd, environment):
        log = (output / (name + '.log')).open('w')
        streams.append(log)
        process = subprocess.Popen(command, cwd=cwd, env=environment, stdout=log, stderr=subprocess.STDOUT)
        processes.append(process)
        return process

    with tempfile.TemporaryDirectory(prefix='vpn-catalog-', dir='/tmp') as temporary:
        root = Path(temporary)
        sock, data = root / 'sock', root / 'postgres'
        sock.mkdir(mode=0o700)
        started = False
        try:
            run('postgres-init', ['initdb', '-D', str(data), '-U', 'postgres', '--auth-local=trust',
                '--auth-host=reject', '--no-locale', '-E', 'UTF8'], root)
            run('postgres-start', ['pg_ctl', '-D', str(data), '-l', str(root / 'postgres.log'), '-o',
                f"-k {sock} -c listen_addresses='' -c unix_socket_permissions=0700", '-w', 'start'], root)
            started = True
            web_port = free_port()
            web_url = f'http://127.0.0.1:{web_port}'
            fixture_env = dict(env, NATIVE_TEST_DSN=f'host={sock} user=postgres dbname=postgres sslmode=disable',
                CATALOG_ACCEPTANCE_DIR=temporary, CATALOG_WEB_ORIGIN=web_url)
            fixture = spawn('fixture', ['go', 'test', '-count=1', '-run', '^TestNodeCatalogAcceptanceFixture$',
                '-timeout=20m', './internal/api'], ROOT / 'backend', fixture_env)
            ready = root / 'fixture.json'
            deadline = time.monotonic() + 120
            while not ready.exists():
                if fixture.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('Fixture readiness failed; see fixture.log')
                time.sleep(0.1)
            metadata = json.loads(ready.read_text())
            frontend_env = dict(env, API_INTERNAL_URL=metadata['api'] + '/api/v1')
            if not args.skip_build:
                run('frontend-build', ['node', 'node_modules/next/dist/bin/next', 'build'], ROOT / 'frontend', frontend_env)
            web = spawn('frontend', ['node', 'node_modules/next/dist/bin/next', 'start', '-H', '127.0.0.1',
                '-p', str(web_port)], ROOT / 'frontend', frontend_env)
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            deadline = time.monotonic() + 60
            while True:
                if web.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('Next.js readiness failed; see frontend.log')
                try:
                    with opener.open(web_url + '/api/v1/meta', timeout=2) as response:
                        if response.status == 200:
                            break
                except OSError:
                    time.sleep(0.1)
            browser_env = dict(frontend_env, E2E_ISOLATED='private-postgres', PLAYWRIGHT_BASE_URL=web_url,
                CATALOG_FIXTURE=str(ready), CATALOG_OUTPUTS=str(output / 'browser-artifacts'))
            chrome = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
            if Path(chrome).is_file():
                browser_env['PLAYWRIGHT_CHROME_PATH'] = chrome
            run('browser', ['node', 'node_modules/@playwright/test/cli.js', 'test', '--config=playwright.catalog.config.ts'],
                ROOT / 'frontend', browser_env)
            summary['checks'].append('administrator browser imports and edits weighted nodes; normal account receives HTTP 403; desktop/mobile rendering')
            core_env = dict(env, CATALOG_ACCEPTANCE_DIR=temporary, CATALOG_OUTPUTS=str(output))
            run('mihomo', ['go', 'test', '-count=1', '-v', '-run', '^TestCatalogBackendMihomoAcceptance$', '.'],
                ROOT / 'FlClash/core', core_env)
            summary['checks'].append('official Core logs in, applies single-node credentials, transfers real payload, rebinds 0.5x/1x rates after settlement, rejects expired entitlement')
            summary['passed'] = True
        finally:
            (root / 'stop').touch()
            for process in reversed(processes):
                if process.poll() is None:
                    if process is processes[0]:
                        try:
                            process.wait(timeout=10)
                            continue
                        except subprocess.TimeoutExpired:
                            pass
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)
            for stream in streams:
                stream.close()
            if started:
                run('postgres-stop', ['pg_ctl', '-D', str(data), '-m', 'fast', '-w', 'stop'], root)
            (output / 'summary.json').write_text(json.dumps(summary, indent=2))
    print('PASS: browser/admin -> PostgreSQL catalog -> official Mihomo real traffic and weighted ledger')


if __name__ == '__main__':
    main()

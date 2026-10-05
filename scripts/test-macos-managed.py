#!/usr/bin/env python3
"""Run the real macOS Application against an isolated Gin/PostgreSQL fixture."""
from pathlib import Path
import argparse
import json
import os
import shutil
import signal
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]


def alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--core', required=True, type=Path)
    args = parser.parse_args()
    output, core = args.output.resolve(), args.core.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if not core.is_file():
        raise SystemExit('Build the managed_acceptance-tagged Core first')
    env = dict(os.environ, LC_ALL='C', LANG='C', GOPROXY='off', GOTOOLCHAIN='local')
    env['PATH'] = '/opt/homebrew/bin:' + env.get('PATH', '')
    summary = {'passed': False, 'phases': [], 'scope': 'real macOS Flutter/Rust IPC/Go/Gin/PostgreSQL; system proxy command adapter isolated'}
    with tempfile.TemporaryDirectory(prefix='flclash-acceptance-', dir='/tmp') as temp:
        directory = Path(temp)
        os.chmod(directory, 0o700)
        socket = directory / 'sock'
        socket.mkdir(mode=0o700)
        data = directory / 'postgres'
        started = False
        fixture = None
        try:
            with (output / 'postgres-init.log').open('w') as log:
                subprocess.run(['initdb', '-D', str(data), '-U', 'postgres', '--auth-local=trust', '--auth-host=reject', '--no-locale', '-E', 'UTF8'], env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
            subprocess.run(['pg_ctl', '-D', str(data), '-l', str(directory / 'postgres.log'), '-o', f"-k {socket} -c listen_addresses='' -c unix_socket_permissions=0700", '-w', 'start'], env=env, check=True, stdout=subprocess.DEVNULL)
            started = True
            fixture_env = dict(env, NATIVE_TEST_DSN=f'host={socket} user=postgres dbname=postgres sslmode=disable', RUN_DB_TESTS='0', DESKTOP_ACCEPTANCE_DIR=temp)
            with (output / 'fixture.log').open('w') as fixture_log:
                fixture = subprocess.Popen(['go', 'test', '-tags=desktop_acceptance', '-count=1', '-run', '^TestDesktopAcceptanceFixture$', '-timeout=25m', './internal/api'], cwd=ROOT / 'backend', env=fixture_env, stdout=fixture_log, stderr=subprocess.STDOUT)
                deadline = time.monotonic() + 100
                while not (directory / 'fixture.json').exists():
                    if fixture.poll() is not None:
                        raise RuntimeError('Fixture did not start; see fixture.log')
                    if time.monotonic() > deadline:
                        raise TimeoutError('Isolated API startup timed out')
                    time.sleep(0.1)
                for phase in ['bundled', 'first', 'reopen']:
                    (directory / 'phase').write_text(phase)
                    (directory / 'exit-request').unlink(missing_ok=True)
                    (directory / 'app-pid.txt').unlink(missing_ok=True)
                    cmd = ['bash', 'scripts/flclash-env.sh', 'flutter', 'drive', '-d', 'macos', '--debug', '--no-pub', '--keep-app-running', '--target=integration_test/managed_account_test.dart', '--driver=test_driver/managed_account_test.dart', f'--dart-define=ACCEPTANCE_DIR={temp}', f'--dart-define=ACCEPTANCE_CORE={core}']
                    with (output / f'app-{phase}.log').open('w') as log:
                        result = subprocess.run(cmd, cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=900)
                    phase_result = {'phase': phase, 'driver_exit': result.returncode}
                    summary['phases'].append(phase_result)
                    if result.returncode:
                        raise RuntimeError(f'{phase}: Flutter integration driver failed')
                    report = directory / f'result-{phase}.json'
                    if not report.exists() or not json.loads(report.read_text()).get('passed'):
                        raise RuntimeError(f'{phase}: missing application assertions')
                    app_pid = int((directory / 'app-pid.txt').read_text())
                    (directory / 'exit-request').touch()
                    deadline = time.monotonic() + 12
                    while alive(app_pid) and time.monotonic() < deadline:
                        time.sleep(0.1)
                    if alive(app_pid):
                        raise RuntimeError('Normal application exit did not complete')
                    core_pids = [int(line) for line in (directory / 'core-pids.txt').read_text().splitlines()]
                    if any(alive(pid) for pid in core_pids):
                        raise RuntimeError('Owned Core survived normal application exit')
                    phase_result.update(app_exit_confirmed=True, core_exit_confirmed=True)
                    shutil.copy2(report, output / report.name)
                    for picture in directory.glob('*.png'):
                        shutil.copy2(picture, output / picture.name)
                summary['passed'] = True
        except Exception as error:
            summary['failure'] = f'{type(error).__name__}: {error}'
            raise
        finally:
            # Preserve only safe reports/screenshots, never fixture credentials.
            for pattern in ['*.png', 'progress-*.json']:
                for path in directory.glob(pattern):
                    if path.is_file() and not path.is_symlink():
                        shutil.copy2(path, output / path.name)
            (output / 'summary.json').write_text(json.dumps(summary, indent=2))
            for file in ['app-pid.txt', 'core-pids.txt']:
                path = directory / file
                if path.exists():
                    for line in path.read_text().splitlines():
                        pid = int(line)
                        if alive(pid):
                            command = subprocess.check_output(['ps', '-p', str(pid), '-o', 'comm='], text=True).strip()
                            if command == str(core) or command in [str(ROOT / 'FlClash/build/macos/Build/Products/Debug/FlClash.app/Contents/MacOS' / name) for name in ['FlClash', 'FlClashCore']]:
                                os.kill(pid, signal.SIGTERM)
            (directory / 'stop').touch()
            if fixture is not None:
                try:
                    fixture.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    fixture.terminate()
                    fixture.wait(timeout=5)
            if started:
                subprocess.run(['pg_ctl', '-D', str(data), '-m', 'fast', '-w', 'stop'], env=env, check=True, stdout=subprocess.DEVNULL)
            for path in directory.glob('result-*.json'):
                shutil.copy2(path, output / path.name)
    print('PASS: macOS real App managed configuration, real cumulative traffic/minute report/final settlement, login/gate, reopen and normal App/Core exit; isolated Gin/PostgreSQL')


if __name__ == '__main__':
    main()

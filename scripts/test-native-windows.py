#!/usr/bin/env python3
"""Windows client test entry point.

Implemented suites:
  foundation (round 1, needs no Windows machine)
    * apps/windows/toolchain.lock.json agrees with global.json and the CI workflow
    * apps/windows/.env.example is accepted by the Python parser
    * the C# contract suite runs and reports a non-zero test count
  core (round 2, runs on macOS, Linux and Windows)
    * the Go platform split selects the Windows files (and never the placeholder)
    * windows/amd64 PE builds of asterlink-core and asterlink-tuntest (GOAMD64=v1, CGO off,
      no fakeengine tag) and `go vet` for windows/amd64
    * the Wintun DLL embedded in the pinned sing-tun is byte-identical to the locked official build
    * the Core's Go test suite runs natively, with a non-zero test count

Exit codes: 0 = PASS, 1 = FAIL, 3 = BLOCKED (a prerequisite is missing, so nothing was proven).
Suites that need a real Windows host (unit/service/tun/...) arrive in later rounds and refuse
to run here instead of reporting a pass.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WINDOWS = ROOT / 'apps' / 'windows'
sys.path.insert(0, str(ROOT / 'scripts'))
import native_env  # noqa: E402

EXIT_PASS, EXIT_FAIL, EXIT_BLOCKED = 0, 1, 3
results = []


def record(case_id, ok, observed, status=None):
    status = status or ('PASS' if ok else 'FAIL')
    results.append({'case_id': case_id, 'status': status, 'observed': observed})
    print('%-8s %-28s %s' % (status, case_id, observed))


def check_lock_file():
    lock = json.loads((WINDOWS / 'toolchain.lock.json').read_text())
    sdk = json.loads((WINDOWS / 'global.json').read_text())['sdk']['version']
    record('lock.sdk', lock['dotnet']['sdk'] == sdk,
           'lock=%s global.json=%s' % (lock['dotnet']['sdk'], sdk))

    workflow = (ROOT / '.github' / 'workflows' / 'windows.yml').read_text()
    label = lock['ci']['runner_label']
    record('lock.runner', 'runs-on: ' + label in workflow and 'windows-latest' not in workflow,
           'workflow pins %s and avoids the moving windows-latest alias' % label)

    floor = 'net10.0-windows10.0.19041.0'
    missing = [name for name in ('AsterLink.App', 'AsterLink.Service')
               if floor not in (WINDOWS / name / (name + '.csproj')).read_text()]
    record('lock.os_floor', not missing and '19041' in lock['os_support']['build_floor'],
           'App and Service target %s (Windows 10 2004 floor)' % floor if not missing else 'missing in ' + ', '.join(missing))

    wintun = lock['wintun']
    record('lock.wintun_hash', bool(re.fullmatch(r'[0-9a-f]{64}', wintun['zip_sha256_published']))
           and wintun['zip_sha256_verified'] == wintun['zip_sha256_published']
           and bool(re.fullmatch(r'[0-9a-f]{64}', wintun['amd64_dll_sha256'])),
           'ZIP hash verified against a download in round 2; DLL hash recorded')

    go_version = lock['go']['version']
    ci = (ROOT / '.github' / 'workflows' / 'ci.yml').read_text()
    record('lock.go', "GO_VERSION: '%s'" % go_version in ci, 'lock=%s ci.yml' % go_version)

    mihomo = subprocess.run(['git', 'ls-tree', 'HEAD', 'native/third_party/Clash.Meta'],
                            cwd=ROOT, capture_output=True, text=True).stdout.split()
    record('lock.mihomo', len(mihomo) >= 3 and mihomo[2] == lock['mihomo']['commit'],
           'lock=%s tree=%s' % (lock['mihomo']['commit'][:12], mihomo[2][:12] if len(mihomo) >= 3 else 'n/a'))


def check_env_example():
    try:
        values = native_env.load_env_file(WINDOWS / '.env.example')
        record('env.example', True, 'accepted: ' + ', '.join(sorted(values)))
    except native_env.EnvError as error:
        record('env.example', False, str(error))
    macos = (ROOT / 'apps' / 'macos' / '.env.example').read_text()
    windows = (WINDOWS / '.env.example').read_text()
    strip = lambda text: [l for l in text.splitlines() if l and not l.startswith('#')]
    record('env.same_keys_as_macos', strip(macos) == strip(windows), 'public build keys identical')


CORE = ROOT / 'native' / 'core'


def go_env(**extra):
    env = dict(os.environ)
    env['CGO_ENABLED'] = '0'
    env.update(extra)
    return env


def go_run(args, **kwargs):
    return subprocess.run(['go', *args], cwd=kwargs.pop('cwd', CORE), capture_output=True, text=True, **kwargs)


def pe_machine(path):
    """(machine, optional-header magic) from the PE header, or None when the file is not a PE image."""
    data = Path(path).read_bytes()
    if data[:2] != b'MZ':
        return None
    offset = int.from_bytes(data[0x3C:0x40], 'little')
    if data[offset:offset + 4] != b'PE\0\0':
        return None
    return int.from_bytes(data[offset + 4:offset + 6], 'little'), int.from_bytes(data[offset + 24:offset + 26], 'little')


def check_platform_split():
    windows = go_env(GOOS='windows', GOARCH='amd64')
    linux = go_env(GOOS='linux', GOARCH='amd64')

    def files(package, env):
        out = go_run(['list', '-f', '{{join .GoFiles " "}}', package], env=env)
        return out.stdout.split() if out.returncode == 0 else None

    expectations = [
        ('./internal/network', ['network_windows.go'], ['network_other.go', 'network_darwin.go']),
        ('./internal/engine', ['platform_windows.go', 'sysdns_windows.go', 'ipv6_windows.go'], ['sysdns.go', 'ipv6_route.go', 'platform_unix.go']),
        ('./cmd/asterlink-core', ['ipc_windows.go'], ['ipc_unix.go']),
    ]
    for package, wanted, forbidden in expectations:
        got = files(package, windows)
        ok = got is not None and all(f in got for f in wanted) and not any(f in got for f in forbidden)
        record('split.windows.' + package.strip('./').replace('/', '_'), ok,
               'windows/amd64 selects %s and none of %s' % (wanted, forbidden) if ok else 'selected files: %s' % got)
    got = files('./internal/network', linux)
    record('split.linux_keeps_placeholder', bool(got) and 'network_other.go' in got and 'network_windows.go' not in got,
           'non-Windows CI hosts still use the test placeholder: %s' % got)


def check_cross_build(out_dir):
    out_dir.mkdir(parents=True, exist_ok=True)
    env = go_env(GOOS='windows', GOARCH='amd64', GOAMD64='v1')
    for name in ('asterlink-core', 'asterlink-tuntest'):
        target = out_dir / (name + '.exe')
        process = go_run(['build', '-trimpath', '-o', str(target), './cmd/' + name], env=env)
        if process.returncode != 0:
            record('build.' + name, False, process.stderr[-1500:])
            continue
        header = pe_machine(target)
        info = go_run(['version', '-m', str(target)], env=env).stdout
        lines = [line.split() for line in info.splitlines() if line.startswith('\tbuild')]
        have = {parts[1] for parts in lines if len(parts) > 1}
        tags = [parts[1] for parts in lines if len(parts) > 1 and parts[1].startswith('-tags')]
        ok = header == (0x8664, 0x20B) and {'GOOS=windows', 'GOARCH=amd64', 'GOAMD64=v1', 'CGO_ENABLED=0'} <= have and not tags
        record('build.' + name, ok, '%s (%d bytes) PE machine/magic=%s tags=%s' % (
            target.name, target.stat().st_size, header and tuple(hex(x) for x in header), tags or 'none'))
    vet = go_run(['vet', './...'], env=env)
    record('vet.windows_amd64', vet.returncode == 0, 'go vet ./... for windows/amd64' if vet.returncode == 0 else vet.stderr[-1500:])


def check_embedded_wintun():
    import hashlib
    lock = json.loads((WINDOWS / 'toolchain.lock.json').read_text())
    expected = lock['wintun']['amd64_dll_sha256']
    module = go_run(['list', '-m', '-f', '{{.Dir}}', 'github.com/metacubex/sing-tun'])
    dll = Path(module.stdout.strip()) / 'internal' / 'wintun' / 'amd64' / 'wintun.dll'
    if module.returncode != 0 or not dll.is_file():
        record('wintun.embedded', False, 'cannot locate the embedded wintun.dll: %s' % module.stderr[-300:])
        return
    actual = hashlib.sha256(dll.read_bytes()).hexdigest()
    record('wintun.embedded', actual == expected, 'embedded amd64 wintun.dll sha256=%s locked=%s' % (actual[:16], expected[:16]))


def run_core_tests():
    process = subprocess.run(['go', 'test', '-count=1', '-json', './...'], cwd=CORE, capture_output=True, text=True, env=go_env())
    passed = failed = skipped = 0
    failures = []
    for line in process.stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not event.get('Test'):
            continue
        action = event.get('Action')
        if action == 'pass':
            passed += 1
        elif action == 'fail':
            failed += 1
            failures.append('%s.%s' % (event.get('Package', '').rsplit('/', 1)[-1], event['Test']))
        elif action == 'skip':
            skipped += 1
    ok = process.returncode == 0 and failed == 0 and passed > 0
    record('go.core_tests', ok, 'native %s: passed=%d failed=%d skipped=%d%s' % (
        sys.platform, passed, failed, skipped, '' if ok else ' FAILED: ' + ', '.join(failures[:10]) + process.stderr[-500:]))


def dotnet_command():
    path = shutil.which('dotnet')
    if path:
        return path
    candidate = Path(os.environ.get('DOTNET_ROOT', '')) / 'dotnet'
    return str(candidate) if candidate.is_file() else None


def run_dotnet_tests():
    dotnet = dotnet_command()
    if not dotnet:
        record('csharp.contract_tests', False, 'dotnet SDK not found; nothing was tested', status='BLOCKED')
        return
    process = subprocess.run([dotnet, 'test', str(WINDOWS / 'AsterLink.Tests'), '-c', 'Release',
                              '--nologo', '--verbosity', 'minimal'],
                             cwd=WINDOWS, capture_output=True, text=True)
    output = process.stdout + process.stderr
    summary = re.search(r'Failed:\s*(\d+), Passed:\s*(\d+), Skipped:\s*(\d+), Total:\s*(\d+)', output)
    if not summary:
        record('csharp.contract_tests', False, 'no test summary in output:\n' + output[-1500:])
        return
    failed, passed, skipped, total = map(int, summary.groups())
    ok = process.returncode == 0 and failed == 0 and total > 0 and passed == total - skipped
    record('csharp.contract_tests', ok, 'passed=%d failed=%d skipped=%d total=%d' % (passed, failed, skipped, total))
    if not ok:
        print(output[-3000:])


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--suite', required=True)
    parser.add_argument('--report', help='write a JSON report here')
    parser.add_argument('--out', help='core suite: directory for the built PE files (default artifacts/native-windows/core)')
    args = parser.parse_args()

    if args.suite == 'foundation':
        check_lock_file()
        check_env_example()
        run_dotnet_tests()
    elif args.suite == 'core':
        if not shutil.which('go'):
            record('go.toolchain', False, 'go not found; nothing was tested', status='BLOCKED')
        else:
            check_platform_split()
            check_cross_build(Path(args.out) if args.out else ROOT / 'artifacts' / 'native-windows' / 'core')
            check_embedded_wintun()
            run_core_tests()
    else:
        print('suite %r needs a Windows x64 host and is implemented in a later round' % args.suite)
        return EXIT_BLOCKED

    if args.report:
        Path(args.report).write_text(json.dumps(results, indent=2))
    statuses = {r['status'] for r in results}
    print('\n%d cases: %s' % (len(results), ', '.join('%s=%d' % (s, sum(r['status'] == s for r in results)) for s in sorted(statuses))))
    if 'FAIL' in statuses:
        return EXIT_FAIL
    return EXIT_BLOCKED if 'BLOCKED' in statuses else EXIT_PASS


if __name__ == '__main__':
    sys.exit(main())

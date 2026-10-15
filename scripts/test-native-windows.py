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

  service (round 3, needs a real Windows x64 host and an elevated shell)
    * AsterLink.Tests.Windows: real named pipes, tokens, ACLs, Job Objects, recovery and the real Go Core
      launcher (zero skipped tests allowed)
    * the INTERNAL stage tree is built (scripts/package-native-windows.py) and scripts/windows/e2e-service.ps1
      installs it as a real service and drives GUI -> Service -> Core, then attacks it. It installs and removes
      AsterLinkService, so it only runs with --allow-service-install. It changes no route, DNS or adapter.

  billing (round 5)
    * unit: ledger formulas, lost-acknowledgement retry and the harness itself, with -race and a non-zero test count
    * windows/amd64 builds of asterlink-billtest and asterlink-trafficsink, plus a linux/amd64 sink for the server
    * live (elevated Windows host, --allow-network-changes, ASTERLINK_BILL_* in the environment): real login -> TUN ->
      controlled bytes -> disconnect -> ledger compared to the formula. Otherwise BLOCKED, never PASS.

  grants (round 7)
    * unit: the independent-client harness judged against a fake managed node in both directions (a faithful node
      passes; one that skips a duty - billing, cutting revoked flows, refusing /traffic, holding the quota - fails),
      the Core's grant state machine, the contract fixtures, the sink's UDP echo, and the node agent module's own
      tests; non-zero test counts required
    * builds of asterlink-billtest for windows/amd64, darwin/arm64 (the macOS side of the B06 race) and linux/amd64
    * live (ASTERLINK_BILL_* plus --spend-test-quota): the node-enforcement cases against the real API and a real
      managed node. Without a node-governed line on the test account the live part is BLOCKED, never PASS.

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

    record('lock.installer', ("INNO_VERSION: '%s'" % lock['installer']['version']) in workflow, 'lock=%s windows.yml' % lock['installer']['version'])

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


def check_cross_build(out_dir, names=('asterlink-core', 'asterlink-tuntest', 'asterlink-billtest', 'asterlink-trafficsink')):
    out_dir.mkdir(parents=True, exist_ok=True)
    env = go_env(GOOS='windows', GOARCH='amd64', GOAMD64='v1')
    for name in names:
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


def run_billing_suite(args):
    """Round 5. The unit part (ledger formulas, ack loss, harness logic against a faithful fake API) runs anywhere
    Go does. The live part drives the production state machine and a real TUN against the real API, so it runs only on an
    elevated Windows host with --allow-network-changes and the ASTERLINK_BILL_* environment; otherwise it is BLOCKED."""
    if not shutil.which('go'):
        record('go.toolchain', False, 'go not found; nothing was tested', status='BLOCKED')
        return
    out_dir = Path(args.out).resolve() if args.out else ROOT / 'artifacts' / 'native-windows' / 'core'
    packages = ['./internal/meter', './internal/managed', './internal/apiclient', './cmd/asterlink-billtest', './cmd/asterlink-trafficsink']
    race = [] if sys.platform == 'win32' else ['-race']  # the race detector needs cgo, which the Windows runner does not guarantee
    process = subprocess.run(['go', 'test', '-count=1', *race, '-json', *packages], cwd=CORE, capture_output=True, text=True, env=dict(os.environ))
    passed = failed = 0
    failures = []
    for line in process.stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get('Test') and event.get('Action') == 'pass':
            passed += 1
        elif event.get('Test') and event.get('Action') == 'fail':
            failed += 1
            failures.append(event['Test'])
    record('billing.unit', process.returncode == 0 and failed == 0 and passed > 0,
           'ledger, ack-loss and harness tests%s: passed=%d failed=%d %s' % (' (race)' if race else '', passed, failed, ', '.join(failures[:8])))
    check_cross_build(out_dir)
    sink = out_dir / 'asterlink-trafficsink-linux-amd64'
    built = go_run(['build', '-trimpath', '-o', str(sink), './cmd/asterlink-trafficsink'], env=go_env(GOOS='linux', GOARCH='amd64'))
    record('build.trafficsink_linux', built.returncode == 0, '%s (%d bytes)' % (sink.name, sink.stat().st_size) if built.returncode == 0 else built.stderr[-800:])

    needed = ['ASTERLINK_BILL_API_BASE', 'ASTERLINK_BILL_EMAIL', 'ASTERLINK_BILL_PASSWORD', 'ASTERLINK_BILL_SINK', 'ASTERLINK_BILL_SINK_TOKEN']
    missing = [name for name in needed if not os.environ.get(name)]
    if sys.platform != 'win32' or not args.allow_network_changes or missing or not is_windows_admin():
        why = 'needs an elevated Windows x64 host, --allow-network-changes' + (' and ' + ', '.join(missing) if missing else '')
        record('billing.live', False, 'live billing run not executed: ' + why, status='BLOCKED')
        return
    report = ROOT / 'artifacts' / 'native-windows' / 'billing-report'
    if report.exists():
        shutil.rmtree(report)
    live = subprocess.run([str(out_dir / 'asterlink-billtest.exe'), '--allow-network-changes', '--ack-loss', '--output', str(report)], capture_output=True, text=True)
    print(live.stdout)
    status = {0: 'PASS', 3: 'BLOCKED'}.get(live.returncode, 'FAIL')
    record('billing.live', status == 'PASS', 'asterlink-billtest exit %d; report %s' % (live.returncode, report / 'billing-report.json'), status=status)


def count_go_tests(cwd, packages, extra=()):
    process = subprocess.run(['go', 'test', '-count=1', '-json', *extra, *packages], cwd=cwd, capture_output=True, text=True, env=dict(os.environ))
    passed = failed = 0
    failures = []
    for line in process.stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get('Test') and event.get('Action') == 'pass':
            passed += 1
        elif event.get('Test') and event.get('Action') == 'fail':
            failed += 1
            failures.append(event['Test'])
    return process, passed, failed, failures


def run_grants_suite(args):
    """Round 7. Whether the NODE enforces billing, revocation and expiry. The unit part runs anywhere Go does; the live part
    needs the real API plus a node governed by a node agent, and without one it is BLOCKED (never a pass)."""
    if not shutil.which('go'):
        record('go.toolchain', False, 'go not found; nothing was tested', status='BLOCKED')
        return
    out_dir = Path(args.out).resolve() if args.out else ROOT / 'artifacts' / 'native-windows' / 'core'
    race = [] if sys.platform == 'win32' else ['-race']
    process, passed, failed, failures = count_go_tests(CORE, ['./cmd/asterlink-billtest', './cmd/asterlink-trafficsink', './internal/managed', './internal/apiclient'], race)
    record('grants.unit', process.returncode == 0 and failed == 0 and passed > 0,
           'harness, sink, grant state machine, contract fixtures%s: passed=%d failed=%d %s' % (' (race)' if race else '', passed, failed, ', '.join(failures[:8])))
    if sys.platform == 'win32':
        # The node agent is server software (Linux nodes); its tests belong to the Linux/macOS CI, not to a Windows client check.
        record('grants.nodeagent', False, 'not run on Windows: the node agent is server software, tested by the Linux/macOS jobs', status='BLOCKED')
    else:
        process, passed, failed, failures = count_go_tests(ROOT / 'native' / 'nodeagent', ['./...'], race)
        record('grants.nodeagent', process.returncode == 0 and failed == 0 and passed > 0,
               'node agent and reference forwarder tests%s: passed=%d failed=%d %s' % (' (race)' if race else '', passed, failed, ', '.join(failures[:8])))
    out_dir.mkdir(parents=True, exist_ok=True)
    for goos, goarch, suffix in (('windows', 'amd64', '.exe'), ('darwin', 'arm64', ''), ('linux', 'amd64', '')):
        target = out_dir / ('asterlink-billtest-%s-%s%s' % (goos, goarch, suffix))
        built = go_run(['build', '-trimpath', '-o', str(target), './cmd/asterlink-billtest'], env=go_env(GOOS=goos, GOARCH=goarch, GOAMD64='v1', CGO_ENABLED='0'))
        record('build.billtest_%s_%s' % (goos, goarch), built.returncode == 0, '%s (%d bytes)' % (target.name, target.stat().st_size) if built.returncode == 0 else built.stderr[-800:])

    needed = ['ASTERLINK_BILL_API_BASE', 'ASTERLINK_BILL_EMAIL', 'ASTERLINK_BILL_PASSWORD', 'ASTERLINK_BILL_SINK', 'ASTERLINK_BILL_SINK_TOKEN']
    missing = [name for name in needed if not os.environ.get(name)]
    if not args.spend_test_quota or missing:
        record('grants.live', False, 'live enforcement run not executed: needs --spend-test-quota' + (' and ' + ', '.join(missing) if missing else ''), status='BLOCKED')
        return
    exe = out_dir / 'asterlink-billtest-host.exe' if sys.platform == 'win32' else out_dir / 'asterlink-billtest-host'
    host = go_run(['build', '-trimpath', '-o', str(exe), './cmd/asterlink-billtest'], env=go_env(CGO_ENABLED='0'))
    if host.returncode != 0:
        record('grants.live', False, 'host build failed: ' + host.stderr[-500:])
        return
    report = ROOT / 'artifacts' / 'native-windows' / 'grants-report'
    if report.exists():
        shutil.rmtree(report)
    command = [str(exe), '--grants', '--spend-test-quota', '--idle-expiry', '--output', str(report)]
    if os.environ.get('ASTERLINK_BILL_UDP_SINK'):
        command += ['--udp-sink', os.environ['ASTERLINK_BILL_UDP_SINK']]
    if args.allow_network_changes:
        command += ['--tun', '--allow-network-changes']
    live = subprocess.run(command, capture_output=True, text=True)
    print(live.stdout)
    status = {0: 'PASS', 3: 'BLOCKED'}.get(live.returncode, 'FAIL')
    record('grants.live', status == 'PASS', 'asterlink-billtest --grants exit %d; report %s' % (live.returncode, report / 'billing-report.json'), status=status)


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


def is_windows_admin():
    if sys.platform != 'win32':
        return False
    import ctypes
    return bool(ctypes.windll.shell32.IsUserAnAdmin())


def run_service_suite(args):
    if sys.platform != 'win32':
        record('service.host', False, 'the service suite needs a real Windows x64 host; nothing was tested', status='BLOCKED')
        return
    if not is_windows_admin():
        record('service.elevation', False, 'needs an elevated shell (pipes, ACLs and the SCM are exercised); nothing was tested', status='BLOCKED')
        return
    dotnet = dotnet_command()
    if not dotnet or not shutil.which('go'):
        record('service.toolchain', False, 'dotnet and go are both required', status='BLOCKED')
        return

    out = Path(args.out).resolve() if args.out else ROOT / 'artifacts' / 'native-windows' / 'service'
    core_dir = out / 'core'
    check_cross_build(core_dir)
    core = core_dir / 'asterlink-core.exe'
    if not core.is_file():
        record('service.core', False, 'the Core could not be built')
        return

    env = dict(os.environ, ASTERLINK_TEST_CORE=str(core))
    process = subprocess.run([dotnet, 'test', str(WINDOWS / 'AsterLink.Tests.Windows'), '-c', 'Release', '--nologo', '--verbosity', 'minimal'],
                             cwd=WINDOWS, capture_output=True, text=True, env=env)
    text = process.stdout + process.stderr
    summary = re.search(r'Failed:\s*(\d+), Passed:\s*(\d+), Skipped:\s*(\d+), Total:\s*(\d+)', text)
    if not summary:
        record('windows.kernel_tests', False, 'no test summary in output:\n' + text[-1500:])
    else:
        failed, passed, skipped, total = map(int, summary.groups())
        ok = process.returncode == 0 and failed == 0 and skipped == 0 and total > 0
        record('windows.kernel_tests', ok, 'passed=%d failed=%d skipped=%d total=%d (skipped must be 0 on an elevated Windows host)' % (passed, failed, skipped, total))
        if not ok:
            print(text[-3000:])

    if not args.allow_service_install:
        record('service.e2e', False, 'refusing to install a Windows service without --allow-service-install', status='BLOCKED')
        return
    stage = out / 'stage'
    packaged = subprocess.run([sys.executable, str(ROOT / 'scripts' / 'package-native-windows.py'), '--channel', 'internal', '--stage', str(stage)],
                              capture_output=True, text=True)
    if packaged.returncode != 0:
        record('service.package', False, (packaged.stdout + packaged.stderr)[-1500:])
        return
    record('service.package', True, 'internal stage built at %s' % stage)

    report = out / 'e2e-service.json'
    e2e = subprocess.run(['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', str(ROOT / 'scripts' / 'windows' / 'e2e-service.ps1'),
                          '-Stage', str(stage), '-Report', str(report)], capture_output=True, text=True)
    print(e2e.stdout[-6000:])
    if not report.is_file():
        record('service.e2e', False, 'the harness wrote no report:\n' + e2e.stderr[-1500:])
        return
    cases = json.loads(report.read_text(encoding='utf-8-sig'))
    if isinstance(cases, dict):
        cases = [cases]
    for case in cases:
        results.append(case)
    record('service.e2e', e2e.returncode == 0 and bool(cases), '%d cases from the harness, exit code %d' % (len(cases), e2e.returncode))


def run_release_suite(args):
    """Round 8: the portable checks always run; the installer build and the real install/upgrade/uninstall need Windows + Inno Setup."""
    sys.path.insert(0, str(ROOT / 'scripts'))
    import tempfile
    import windows_release

    # -- portable: checksum writer/reader, the Inno script's contract, the refusal of a fake release channel ----------
    with tempfile.TemporaryDirectory() as tmp:
        folder = Path(tmp)
        (folder / 'a.bin').write_bytes(b'one')
        (folder / 'b.txt').write_text('two')
        windows_release.write_checksums(folder)
        record('release.sha256sums_roundtrip', windows_release.verify_checksums(folder) == [], 'SHA256SUMS written and read back clean')
        (folder / 'a.bin').write_bytes(b'changed')
        (folder / 'c.txt').write_text('unlisted')
        problems = windows_release.verify_checksums(folder)
        record('release.sha256sums_detect_tamper', any('mismatch' in p for p in problems) and any('not listed' in p for p in problems), 'problems: %s' % problems)

    iss = (WINDOWS / 'Packaging' / 'setup.iss').read_text(encoding='utf-8')
    required = {
        'PrivilegesRequired=admin': 'installer runs elevated by design (one UAC prompt)',
        'ArchitecturesAllowed=x64os': 'x64 only, not ARM64',
        'MinVersion=10.0.19041': 'Windows 10 2004 floor',
        'DefaultDirName={autopf}\\{#AppName}': 'the one directory the service accepts',
        'install --stage': 'files are installed by the verifying service verb, not copied by Inno',
        'runasoriginaluser': 'the app never starts with the administrator token',
        'REMOVEUSERDATA': 'explicit choice for saved data',
    }
    for needle, why in required.items():
        record('release.iss[%s]' % needle, needle in iss, why)
    # Inno must never copy product files into Program Files itself, and the stage must stay out of {app}.
    files_section = iss.split('[Files]')[1].split('[Icons]')[0]
    record('release.iss_files_only_to_tmp', '{tmp}' in files_section and '{app}' not in files_section, 'payload goes to Setup temp, never straight to {app}')
    # The verb must run before Inno writes unins000.exe / the Add/Remove Programs entry: at ssPostInstall its root
    # rename removed the fresh uninstaller, and Inno swallows an Abort raised there (Setup still exits 0).
    code = iss.split('[Code]')[1]
    # Inno refuses ExecAsOriginalUser in the uninstaller (fatal, uninstall exits 1); the cleanup uses Exec.
    record('release.iss_no_original_user_exec_in_uninstall', 'ExecAsOriginalUser(' not in code and "Exec(Gui, '--cleanup-user-data" in code,
           'per-user cleanup started with Exec (the uninstaller has no original-user token)')
    uninstall_code = code.split('procedure CurUninstallStepChanged')[1]
    record('release.iss_failed_uninstall_verb_aborts', 'Abort;' in uninstall_code,
           'a refused uninstall verb stops before Inno drops its entry and unins000.exe')
    record('release.iss_verb_before_inno_records',
           'dontcopy' in files_section and 'if CurStep <> ssInstall then Exit;' in code and "ExtractTemporaryFiles('{tmp}\\stage\\*')" in code,
           'stage unpacked and verb run at ssInstall, before the uninstaller exists')
    uninstall_dir = re.search(r'^UninstallFilesDir=\{app\}\\(\w+)\s*$', iss, re.M)
    layout = (WINDOWS / 'AsterLink.Shared' / 'Service' / 'InstallLayout.cs').read_text(encoding='utf-8')
    record('release.iss_uninstaller_dir_matches_layout',
           bool(uninstall_dir) and ('UninstallerDirectory = "%s";' % uninstall_dir.group(1)) in layout,
           '%s is the directory the install verb carries across a reinstall' % (uninstall_dir.group(0).strip() if uninstall_dir else 'missing'))

    refused = subprocess.run([sys.executable, str(ROOT / 'scripts' / 'package-native-windows.py'), '--channel', 'release'], capture_output=True, text=True)
    record('release.channel_release_refused', refused.returncode != 0 and 'release manifest' in (refused.stdout + refused.stderr), 'a "release" channel stays refused until the service verifies Authenticode')

    ps = shutil.which('pwsh') or shutil.which('powershell')
    if ps:
        for name in ('sign-files.ps1', 'release-check.ps1'):
            script = ROOT / 'scripts' / 'windows' / name
            parse = subprocess.run([ps, '-NoProfile', '-Command',
                                    "$e=$null;[void][System.Management.Automation.Language.Parser]::ParseFile('%s',[ref]$null,[ref]$e);if($e.Count){$e|%%{$_.Message};exit 1}" % script],
                                   capture_output=True, text=True)
            record('release.ps_syntax[%s]' % name, parse.returncode == 0, (parse.stdout + parse.stderr).strip()[:300] or 'parses')
    else:
        record('release.ps_syntax', False, 'no PowerShell on this host; syntax not checked', status='NOT_RUN')

    # -- Windows + Inno Setup: the real thing -----------------------------------------------------------------------
    if sys.platform != 'win32':
        record('release.installer', False, 'building and running the installer needs a Windows x64 host; nothing was tested', status='BLOCKED')
        return
    try:
        windows_release.find_iscc()
    except SystemExit as error:
        record('release.installer', False, str(error), status='BLOCKED')
        return
    out = Path(args.out).resolve() if args.out else ROOT / 'artifacts' / 'native-windows' / 'release'
    stage = out / 'stage'
    release_dir = out / 'installer'
    sign_mode = args.sign
    cmd = [sys.executable, str(ROOT / 'scripts' / 'package-native-windows.py'), '--channel', 'internal', '--stage', str(stage),
           '--installer', '--release-dir', str(release_dir), '--sign', sign_mode]
    if args.skip_source_archives:
        cmd.append('--skip-source-archives')
    packaged = subprocess.run(cmd, capture_output=True, text=True)
    print(packaged.stdout[-6000:])
    if packaged.returncode != 0:
        print(packaged.stderr[-3000:])
        record('release.package', False, (packaged.stdout + packaged.stderr)[-1500:])
        return
    record('release.package', True, 'installer built at %s (signing=%s)' % (release_dir, sign_mode))

    report = out / 'release-check.json'
    expect = 'none' if sign_mode == 'none' else 'test' if sign_mode == 'test' else 'release'
    ps_cmd = [shutil.which('pwsh') or 'powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', str(ROOT / 'scripts' / 'windows' / 'release-check.ps1'),
              '-ReleaseDir', str(release_dir), '-ExpectSignature', expect, '-Report', str(report)]
    if args.allow_service_install and is_windows_admin():
        ps_cmd.append('-Install')
    else:
        record('release.install', False, 'installing and uninstalling the product needs --allow-service-install and an elevated shell; only the file checks ran', status='BLOCKED')
    check = subprocess.run(ps_cmd, capture_output=True, text=True)
    print(check.stdout[-8000:])
    if not report.is_file():
        record('release.check', False, 'the harness wrote no report:\n' + check.stderr[-1500:])
        return
    cases = json.loads(report.read_text(encoding='utf-8-sig'))
    if isinstance(cases, dict):
        cases = [cases]
    for case in cases:
        results.append(case)
    record('release.check', check.returncode == 0 and bool(cases), '%d cases from the harness, exit code %d' % (len(cases), check.returncode))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--suite', required=True)
    parser.add_argument('--report', help='write a JSON report here')
    parser.add_argument('--allow-service-install', action='store_true', help='service suite: permit installing and removing AsterLinkService on this machine')
    parser.add_argument('--allow-network-changes', action='store_true', help='billing suite: permit the live run (changes routes/DNS, spends test-account quota)')
    parser.add_argument('--spend-test-quota', action='store_true', help='grants suite: permit the live run (spends test-account quota and device slots; changes no network setting unless --allow-network-changes adds the TUN case)')
    parser.add_argument('--out', help='core suite: directory for the built PE files (default artifacts/native-windows/core)')
    parser.add_argument('--sign', choices=('none', 'test', 'thumbprint', 'pfx'), default='test', help='release suite: signing mode used for the installer build (default: a self-signed TEST certificate)')
    parser.add_argument('--skip-source-archives', action='store_true', help='release suite: faster local run')
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
            check_cross_build(Path(args.out).resolve() if args.out else ROOT / 'artifacts' / 'native-windows' / 'core')
            check_embedded_wintun()
            run_core_tests()
    elif args.suite == 'service':
        run_service_suite(args)
    elif args.suite == 'billing':
        run_billing_suite(args)
    elif args.suite == 'grants':
        run_grants_suite(args)
    elif args.suite == 'release':
        run_release_suite(args)
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

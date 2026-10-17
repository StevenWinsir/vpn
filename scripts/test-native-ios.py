#!/usr/bin/env python3
"""iOS client test entry point (handoff_app_ios.md 11, 13).

Implemented suites (rounds 1-2):
  foundation  needs Go and Xcode; no simulator runtime
    * apps/ios/toolchain.lock.json agrees with native/core/go.mod (x/mobile) and the installed Xcode
    * apps/ios/.env.example is accepted by the env-v1 parser; the xcconfig escaping of '//' holds
    * the Xcode project has the app, tunnel extension and unit-test targets and a shared scheme
      that tests; both entitlements declare exactly packet-tunnel-provider
    * the Go platform split: an iOS build selects the iOS platform files and none of the macOS
      utun/networksetup/route files, and `go vet` passes for ios/arm64
  unit        needs Go and Xcode; no simulator runtime
    * the Core cross-compiles for ios/arm64; Go host tests of mobile/managed/network/engine and
      the AsterLinkKit Swift tests pass with a non-zero count
  simulator   needs the iOS simulator runtime; changes nothing outside the simulator
    * creates or reuses the asterlink-iphone simulator and runs the unit-test bundle in it:
      CoreBindingTests (ipc-v1 through the real linked Core, start, stop) and the E-SIM-NE
      experiment (whether a packet tunnel configuration can be saved; recorded, not assumed)
    * the test bundle carries no Go code (one Go runtime per process)
    * installs and launches the app, takes a screenshot and requires it to keep running
    * round 2: the handoff surface answers through the real Core (HandoffBindingTests), the app
      addresses the extension it embeds, and the extension links the Core
  device-tun  round 2 device acceptance (D-T01..D-T09, D-CYCLES, D-MEM-idle). Checks the
      prerequisites first: an Apple signing identity that can sign packet-tunnel-provider and a
      paired iPhone. Without them every case is reported BLOCKED (exit 3); the manual procedure is
      docs/ios-tun-design.md section 9.

Exit codes: 0 = PASS, 1 = FAIL, 3 = BLOCKED (a prerequisite is missing, so nothing was proven).
Exit codes: 0 = PASS, 1 = FAIL, 3 = BLOCKED (a prerequisite is missing, so nothing was proven).
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent.parent
IOS = ROOT / 'apps' / 'ios'
CORE = ROOT / 'native' / 'core'
RUNTIME = ROOT / '.runtime' / 'ios'
DERIVED = RUNTIME / 'DerivedData'
ARTIFACTS = ROOT / 'artifacts' / 'native-ios'
sys.path.insert(0, str(ROOT / 'scripts'))
import native_env  # noqa: E402

LOCK = json.loads((IOS / 'toolchain.lock.json').read_text())
EXIT_PASS, EXIT_FAIL, EXIT_BLOCKED = 0, 1, 3
SIMULATOR = 'asterlink-iphone'
results = []

# iOS builds must pick these platform files and never the macOS ones (handoff_app_ios.md 2.3).
IOS_FILES = {'internal/engine': {'platform_ios.go', 'sysdns_ios.go', 'ipv6_ios.go', 'ipv6filter.go', 'ipv6relay_unix.go'},
             'internal/network': {'network_ios.go'}}
MACOS_FILES = {'internal/engine': {'platform_unix.go', 'sysdns.go', 'ipv6_route.go'}, 'internal/network': {'network_darwin.go'}}


def record(case_id, ok, observed, status=None):
    status = status or ('PASS' if ok else 'FAIL')
    results.append({'case_id': case_id, 'status': status, 'observed': observed})
    print('%-8s %-30s %s' % (status, case_id, observed), flush=True)


def ios_env():
    sdk = subprocess.run(['xcrun', '--sdk', 'iphoneos', '--show-sdk-path'], capture_output=True, text=True, check=True).stdout.strip()
    clang = subprocess.run(['xcrun', '--sdk', 'iphoneos', '-f', 'clang'], capture_output=True, text=True, check=True).stdout.strip()
    return dict(os.environ, GOFLAGS='-buildvcs=false', CGO_ENABLED='1', GOOS='ios', GOARCH='arm64',
                CC='%s -isysroot %s -arch arm64 -miphoneos-version-min=%s' % (clang, sdk, LOCK['ios']['deployment_target']))


def build_script():
    spec = importlib.util.spec_from_file_location('build_native_ios', ROOT / 'scripts' / 'build-native-ios.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


# ---------------------------------------------------------------- foundation

def check_lock():
    gomod = (CORE / 'go.mod').read_text()
    pinned = re.search(r'golang.org/x/mobile (\S+)', gomod)
    tools = re.search(r'^tool \(\s*golang.org/x/mobile/cmd/gobind\s+golang.org/x/mobile/cmd/gomobile\s*\)', gomod, re.M)
    record('lock.x_mobile', bool(pinned and tools) and pinned.group(1) == LOCK['x_mobile']['version'],
           'go.mod pins x/mobile %s as a tool' % (pinned and pinned.group(1)))
    out = subprocess.run(['xcodebuild', '-version'], capture_output=True, text=True).stdout
    version = re.search(r'Xcode (\S+)', out)
    build = re.search(r'Build version (\S+)', out)
    found = (version and version.group(1), build and build.group(1))
    local = (LOCK['xcode']['version'], LOCK['xcode']['build'])
    ci = LOCK['xcode']['ci']
    # CI's build number is not locked until the first run records it; its version still must match.
    on_ci = found[0] == ci['version'] and (ci['build'] is None or found[1] == ci['build'])
    record('lock.xcode', found == local or on_ci,
           'installed Xcode %s (%s); lock: local %s (%s), ci %s (%s)' % (found + local + (ci['version'], ci['build'])))


def check_env():
    try:
        values = native_env.load_env_file(IOS / '.env.example')
        record('env.example', True, 'accepted: APP_ENV=%s' % values['APP_ENV'])
    except native_env.EnvError as error:
        record('env.example', False, str(error))
    escape = build_script().xcconfig_value
    cases = {'https://demo.hyshentou.cn/api/v1/client': 'https:/$()/demo.hyshentou.cn/api/v1/client',
             'http://127.0.0.1:8080/api/v1/client': 'http:/$()/127.0.0.1:8080/api/v1/client', 'development': 'development'}
    wrong = {k: escape(k) for k, v in cases.items() if escape(k) != v}
    record('env.xcconfig_escape', not wrong, "'//' becomes '/$()/' in %d values" % len(cases) if not wrong else 'wrong: %s' % wrong)


def check_project():
    out = subprocess.run(['xcodebuild', '-list', '-project', str(IOS / 'AsterLink.xcodeproj')], capture_output=True, text=True).stdout
    targets = set(re.findall(r'^\s{8}(\S+)$', out.split('Build Configurations:')[0], re.M))
    want = {'AsterLink', 'AsterLinkTunnel', 'AsterLinkTests'}
    record('project.targets', want <= targets, 'targets: %s' % ', '.join(sorted(targets)))
    scheme = IOS / 'AsterLink.xcodeproj/xcshareddata/xcschemes/AsterLink.xcscheme'
    record('project.scheme_tests', scheme.exists() and 'AsterLinkTests.xctest' in scheme.read_text(), 'shared AsterLink scheme tests AsterLinkTests')
    import plistlib
    for name in ('App', 'Tunnel'):
        data = plistlib.loads((IOS / 'Config' / ('%s.entitlements' % name)).read_bytes())
        ok = data == {'com.apple.developer.networking.networkextension': ['packet-tunnel-provider']}
        record('project.entitlements_' + name.lower(), ok, 'exactly packet-tunnel-provider' if ok else 'unexpected: %s' % data)


def check_platform_split():
    env = ios_env()
    for package in IOS_FILES:
        files = subprocess.run(['go', 'list', '-f', '{{join .GoFiles " "}}', './' + package], cwd=CORE, env=env,
                               capture_output=True, text=True, check=True).stdout.split()
        missing, leaked = IOS_FILES[package] - set(files), MACOS_FILES[package] & set(files)
        record('go.split_' + package.split('/')[-1], not missing and not leaked,
               'ios selects %s; no macOS files' % ', '.join(sorted(IOS_FILES[package])) if not missing and not leaked
               else 'missing %s, leaked %s' % (sorted(missing), sorted(leaked)))
    vet = subprocess.run(['go', 'vet', './internal/...', './mobile'], cwd=CORE, env=env, capture_output=True, text=True)
    record('go.vet_ios', vet.returncode == 0, 'go vet ios/arm64 clean' if vet.returncode == 0 else vet.stderr.strip()[-400:])


# ---------------------------------------------------------------- unit

def count_go_tests(output):
    return len(re.findall(r'^\s*--- PASS', output, re.M))


def check_unit():
    build = subprocess.run(['go', 'build', './internal/...', './mobile'], cwd=CORE, env=ios_env(), capture_output=True, text=True)
    record('go.build_ios', build.returncode == 0, 'Core builds for ios/arm64' if build.returncode == 0 else build.stderr.strip()[-400:])
    test = subprocess.run(['go', 'test', '-count=1', '-v', './mobile', './internal/managed', './internal/network', './internal/engine'],
                          cwd=CORE, env=dict(os.environ, GOFLAGS='-buildvcs=false'), capture_output=True, text=True)
    passed = count_go_tests(test.stdout)
    record('go.host_tests', test.returncode == 0 and passed > 0, '%d Go tests passed' % passed if test.returncode == 0 else test.stdout.strip()[-600:])
    kit = subprocess.run(['swift', 'test'], cwd=IOS / 'Packages/AsterLinkKit', capture_output=True, text=True)
    swift_passed = len(re.findall(r'✔ Test \S+ passed', kit.stdout + kit.stderr))
    record('swift.kit', kit.returncode == 0 and swift_passed > 0,
           '%d AsterLinkKit tests passed' % swift_passed if kit.returncode == 0 else (kit.stdout + kit.stderr).strip()[-600:])


# ---------------------------------------------------------------- device

DEVICE_CASES = ('D-T01', 'D-T02', 'D-T03', 'D-T04', 'D-T05', 'D-T06', 'D-T07', 'D-T09', 'D-CYCLES', 'D-MEM-idle')


def check_device(wanted):
    identities = subprocess.run(['security', 'find-identity', '-v', '-p', 'codesigning'], capture_output=True, text=True).stdout
    count = re.search(r'(\d+) valid identities found', identities)
    signing = bool(count and int(count.group(1)) > 0)
    record('device.signing_identity', signing, '%s valid code-signing identities' % (count.group(1) if count else '?'),
           status=None if signing else 'BLOCKED')
    listing = subprocess.run(['xcrun', 'devicectl', 'list', 'devices'], capture_output=True, text=True).stdout
    phones = [line for line in listing.splitlines() if 'iPhone' in line and 'simulated' not in line and ('paired' in line or 'connected' in line)]
    if wanted:
        phones = [line for line in phones if wanted in line]
    record('device.iphone', bool(phones), '%d paired iPhone(s)' % len(phones), status=None if phones else 'BLOCKED')
    if not (signing and phones):
        for case in DEVICE_CASES:
            record(case, False, 'not run: needs a paid Apple team (packet-tunnel-provider) and a paired iPhone', status='BLOCKED')
        return
    # With both present the provisioning and the run are manual for now (docs/ios-tun-design.md 9).
    for case in DEVICE_CASES:
        record(case, False, 'prerequisites present; run the manual procedure in docs/ios-tun-design.md 9 and record it', status='NOT_RUN')


# ---------------------------------------------------------------- simulator

def simctl(*args, check=True):
    return subprocess.run(['xcrun', 'simctl', *args], capture_output=True, text=True, check=check)


def simulator_udid(wanted):
    runtimes = [r for r in json.loads(simctl('list', 'runtimes', '-j').stdout)['runtimes'] if r.get('platform') == 'iOS' and r.get('isAvailable')]
    if wanted == 'latest':
        runtime = max(runtimes, key=lambda r: [int(x) for x in r['version'].split('.')], default=None)
    else:
        runtime = next((r for r in runtimes if r.get('version', '').split('.')[0] == wanted.split('.')[0]), None)
    if runtime is None:
        record('simulator.runtime', False, 'no available iOS %s simulator runtime (xcodebuild -downloadPlatform iOS)' % wanted, status='BLOCKED')
        return None
    record('simulator.runtime', True, 'iOS %s (%s)' % (runtime['version'], runtime.get('buildversion', '?')))
    devices = json.loads(simctl('list', 'devices', '-j').stdout)['devices'].get(runtime['identifier'], [])
    device = next((d for d in devices if d['name'] == SIMULATOR and d.get('isAvailable')), None)
    if device is None:
        types = [t for t in runtime.get('supportedDeviceTypes', []) if t.get('productFamily') == 'iPhone']
        # The runtime lists the newest models first; take the newest non-Max Pro.
        pro = [t for t in types if 'Pro' in t['name'] and 'Max' not in t['name']] or types
        if not pro:
            record('simulator.device', False, 'the runtime lists no iPhone device type', status='BLOCKED')
            return None
        udid = simctl('create', SIMULATOR, pro[0]['identifier'], runtime['identifier']).stdout.strip()
        record('simulator.device', True, 'created %s (%s, iOS %s)' % (SIMULATOR, pro[0]['name'], runtime['version']))
    else:
        udid = device['udid']
        record('simulator.device', True, 'reusing %s (iOS %s)' % (SIMULATOR, runtime['version']))
    simctl('boot', udid, check=False)
    simctl('bootstatus', udid, '-b')
    return udid


def check_simulator(out_dir: Path, wanted):
    udid = simulator_udid(wanted)
    if udid is None:
        return
    result = out_dir / 'tests.xcresult'
    if result.exists():
        shutil.rmtree(result)
    test = subprocess.run(['xcodebuild', 'test', '-project', str(IOS / 'AsterLink.xcodeproj'), '-scheme', 'AsterLink',
                           '-destination', 'id=' + udid, '-derivedDataPath', str(DERIVED), '-resultBundlePath', str(result),
                           'CODE_SIGNING_ALLOWED=NO'], capture_output=True, text=True)
    log = test.stdout + test.stderr
    (out_dir / 'xcodebuild-test.log').write_text(log)
    summary = subprocess.run(['xcrun', 'xcresulttool', 'get', 'test-results', 'summary', '--path', str(result)],
                             capture_output=True, text=True)
    try:
        counts = json.loads(summary.stdout)
        passed, failed = counts.get('passedTests', 0), counts.get('failedTests', 0)
    except ValueError:
        passed, failed = 0, -1
    record('simulator.unit_tests', test.returncode == 0 and passed > 0 and failed == 0,
           '%d passed, %d failed in the simulator (CoreBindingTests, E-SIM-NE)' % (passed, failed))
    # Anchored: the system's own log line also contains the configuration name "AsterLink E-SIM-NE".
    experiment = re.search(r'^E-SIM-NE: ((?:saved|refused).*)$', log, re.M)
    record('E-SIM-NE', experiment is not None, experiment.group(1).strip() if experiment else 'experiment line not found in the log',
           status='INFO' if experiment else 'FAIL')

    products = DERIVED / 'Build/Products/Debug-iphonesimulator'
    tests = products / 'AsterLink.app/PlugIns/AsterLinkTests.xctest/AsterLinkTests'
    if tests.exists():
        symbols = subprocess.run(['nm', '-a', str(tests)], capture_output=True, text=True).stdout
        record('simulator.one_go_runtime', 'runtime.goexit' not in symbols, 'the test bundle links no Go code')
    else:
        record('simulator.one_go_runtime', False, 'test bundle not found at %s' % tests)

    appex = products / 'AsterLink.app/PlugIns/AsterLinkTunnel.appex'
    linked = [f for f in (appex / 'AsterLinkTunnel.debug.dylib', appex / 'AsterLinkTunnel') if f.exists()]
    symbols = ''.join(subprocess.run(['nm', '-g', str(f)], capture_output=True, text=True).stdout for f in linked)
    record('simulator.extension_core', '_MobileAdoptSession' in symbols and '_MobileStart' in symbols,
           'the tunnel extension links the Core (MobileStart, MobileAdoptSession)')

    app = products / 'AsterLink.app'
    bundle_id = subprocess.run(['/usr/libexec/PlistBuddy', '-c', 'Print :CFBundleIdentifier', str(app / 'Info.plist')],
                               capture_output=True, text=True).stdout.strip()
    simctl('install', udid, str(app))
    launch = simctl('launch', udid, bundle_id, check=False)
    time.sleep(4)
    screenshot = out_dir / 'foundation.png'
    simctl('io', udid, 'screenshot', str(screenshot), check=False)
    running = bundle_id in simctl('spawn', udid, 'launchctl', 'list', check=False).stdout
    record('simulator.app_launch', launch.returncode == 0 and running,
           'app running after 4 s; screenshot %s' % screenshot.relative_to(ROOT) if running else 'launch: %s' % (launch.stderr.strip() or 'not running'))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--suite', required=True, choices=('foundation', 'unit', 'simulator', 'device-tun'))
    parser.add_argument('--report', help='write a JSON report here')
    parser.add_argument('--device', help='device-tun: the iPhone name or UDID to use')
    parser.add_argument('--runtime', default=LOCK['ios']['simulator_runtime']['wanted'].split()[-1],
                        help="simulator suite: iOS runtime major version, or 'latest' (CI)")
    args = parser.parse_args()

    import prepare_mihomo
    prepare_mihomo.prepare()
    out_dir = ARTIFACTS / args.suite
    out_dir.mkdir(parents=True, exist_ok=True)

    if args.suite == 'foundation':
        check_lock()
        check_env()
        check_project()
        check_platform_split()
    elif args.suite == 'unit':
        check_unit()
    elif args.suite == 'simulator':
        check_simulator(out_dir, args.runtime)
    elif args.suite == 'device-tun':
        check_device(args.device)

    report = Path(args.report) if args.report else out_dir / ('%s-report.json' % args.suite)
    report.write_text(json.dumps({'suite': args.suite, 'cases': results}, indent=2, ensure_ascii=False) + '\n')
    statuses = {r['status'] for r in results}
    if not results:
        print('no case ran')
        return EXIT_FAIL
    if 'FAIL' in statuses:
        return EXIT_FAIL
    if 'BLOCKED' in statuses or 'NOT_RUN' in statuses:
        return EXIT_BLOCKED
    return EXIT_PASS


if __name__ == '__main__':
    sys.exit(main())

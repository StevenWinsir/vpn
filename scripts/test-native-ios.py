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
  ui          round 3; needs the simulator runtime. Runs AsterLinkUITests (scheme AsterLinkUI): every
      screen of the interface is launched from a prepared state, asserted, and captured (at least
      24 screenshots, light, dark, AX3 text). No Core and no network are involved.
  account     round 3; needs the simulator runtime, Go, initdb and pg_ctl. Starts the backend world
      (isolated PostgreSQL, loopback only) and runs AccountAcceptanceTests in the simulator against
      the real Core and Keychain: A01 login/remember/relaunch/sign-out, A02 plans, A04 admin edits.
  billing     round 4; needs Go and Xcode, no simulator, no network, no account. The metering and
      hand-over ledger proofs that do not need a data path: the Core's exact-unit tests at 1x and 0.5x
      (20 MiB up + 80 MiB down), the lost-ACK replay, the unsettled-tail refusals, the app -> extension
      -> app hand-over with its 20 MiB baseline and 80 MiB settle, and the Swift connect-loop tests
      (hand-over routing, extension-ended alert once, session-lost silent re-login, per-connection
      usage). With --harness host|simulator|both it also runs the harness acceptance B01/B02/B04/A04/C01
      (handoff_app_ios.md 8.8): the real Core and pinned Mihomo with the in-process injection engine in
      place of a utun (Go tag asterlink_harness, a test-only framework that no app links), the real
      Gin/PostgreSQL backend world, the controlled test node, the traffic sink and (B04) the real node
      agent, all on the Mac's loopback; "simulator" runs the Core inside an iOS Simulator process. Needs
      initdb and pg_ctl; changes no network setting. Without --harness those cases are NOT_RUN. The
      device-billing cases are BLOCKED without a signed device; unit proofs never stand in for either.
  device-tun  round 2 device acceptance (D-T01..D-T09, D-CYCLES, D-MEM-idle). Checks the
      prerequisites first: an Apple signing identity that can sign packet-tunnel-provider and a
      paired iPhone. Without them every case is reported BLOCKED (exit 3); the manual procedure is
      docs/ios-tun-design.md section 9.
  lifecycle   round 5; needs Xcode's swift only. S-L01: the app-side lifecycle tests (foreground lease,
      system stop reasons, gate log, resource samples, rejoining a running tunnel after the app was killed,
      the heartbeat) and the sample summarizer's own checks. The device cases D-L01..D-L07 and D-MEM are
      BLOCKED without a signed device (exit 3); the procedure is docs/ios-lifecycle.md.

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
import tempfile
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


def check_harness_isolation():
    """The test-only harness (handoff_app_ios.md 8.8) can never ship: no production Core build links it,
    the app project does not reference it, and the production framework carries none of its symbols."""
    env = dict(ios_env(), GOFLAGS='-buildvcs=false')
    listing = subprocess.run(['go', 'list', '-deps', './mobile'], cwd=CORE, env=env, capture_output=True, text=True)
    linked = [line for line in listing.stdout.splitlines() if line.endswith('internal/harness')]
    record('harness.not_in_production_core', listing.returncode == 0 and not linked,
           'go list -deps ./mobile (ios/arm64, no tags) does not include internal/harness' if not linked else 'production Core links ' + ', '.join(linked))
    tagged = subprocess.run(['go', 'list', '-tags', 'asterlink_harness', '-deps', './mobile'], cwd=CORE, env=env, capture_output=True, text=True)
    record('harness.tag_is_what_adds_it', any(l.endswith('internal/harness') for l in tagged.stdout.splitlines()),
           'with -tags asterlink_harness the same list does include it (the check above can fail)')
    project = (IOS / 'AsterLink.xcodeproj/project.pbxproj').read_text()
    record('harness.not_in_project', 'Harness' not in project and 'runtime/ios/harness' not in project,
           'the Xcode project references neither apps/ios/Harness nor the harness framework')
    check_framework_clean(required=False)


def check_framework_clean(required):
    """The production xcframework carries no harness symbol. Foundation runs before the framework exists
    (CI), so it only checks one that is already there; the simulator suite, which follows the build, requires it."""
    framework = IOS / 'Frameworks/AsterCore.xcframework'
    binaries = sorted(framework.glob('*/AsterCore.framework/AsterCore')) if framework.exists() else []
    if not binaries:
        if required:
            record('harness.not_in_framework', False, 'no production xcframework to check')
        return
    leaked = [b.parent.parent.name for b in binaries if b'HarnessRun' in b.read_bytes()]
    record('harness.not_in_framework', not leaked, 'no production framework slice carries HarnessRun' if not leaked else 'harness symbols in ' + ', '.join(leaked))


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
    # The test-only harness stays compilable for the simulator and the host (its acceptance runs under --suite billing).
    tagged = subprocess.run(['go', 'vet', '-tags', 'asterlink_harness', './internal/engine', './internal/harness', './mobile'], cwd=CORE,
                            env=dict(ios_env(), GOARCH='arm64'), capture_output=True, text=True)
    record('go.harness_vet_ios', tagged.returncode == 0, 'the harness (tag asterlink_harness) vets for ios/arm64' if tagged.returncode == 0 else tagged.stderr.strip()[-400:])
    test = subprocess.run(['go', 'test', '-count=1', '-v', './mobile', './internal/managed', './internal/network', './internal/engine'],
                          cwd=CORE, env=dict(os.environ, GOFLAGS='-buildvcs=false'), capture_output=True, text=True)
    passed = count_go_tests(test.stdout)
    record('go.host_tests', test.returncode == 0 and passed > 0, '%d Go tests passed' % passed if test.returncode == 0 else test.stdout.strip()[-600:])
    kit = subprocess.run(['swift', 'test'], cwd=IOS / 'Packages/AsterLinkKit', capture_output=True, text=True)
    swift_passed = len(re.findall(r'✔ Test \S+ passed', kit.stdout + kit.stderr))
    record('swift.kit', kit.returncode == 0 and swift_passed > 0,
           '%d AsterLinkKit tests passed' % swift_passed if kit.returncode == 0
           else '; '.join(re.findall(r'✘[^\n]*', kit.stdout + kit.stderr)[:8]) or (kit.stdout + kit.stderr).strip()[-600:])


# ---------------------------------------------------------------- billing

# Go tests that prove the ledger arithmetic and the hand-over without a data path (names are pinned so a
# renamed or deleted test fails the suite instead of silently shrinking it).
BILLING_GO = (
    ('./internal/meter', ('TestExactIntegerChargeAtBothRates', 'TestLostAckRetriesOriginalBatchAndNeverDoubleCharges',
                          'TestCounterRegressionIsFatalAndNothingIsReported', 'TestRebaseAcrossSettledSegments',
                          'TestBeginRefusesUnsettledOrDisagreeingLedger', 'TestRateChangeIsReportedAfterBytesAreSettledAtTheBoundRate',
                          'TestEstimatedRemainingUsesIntegerUnitsAndFloorsAtZero')),
    ('./internal/managed', ('TestLostAcknowledgementReplaysTheSameBatch', 'TestLogoutSettlesTailBeforeRevoking',
                            'TestUnconfirmedTailBlocksReconnectUntilAcknowledged', 'TestHandoffRoundTrip',
                            'TestHandoffExportRefusesUnsettled')),
)
BILLING_SWIFT = ('MeteringArithmeticTests', 'TunnelFlowTests', 'TunnelBridgeTests', 'TunnelWatchTests')
HARNESS_CASES = (
    ('B01.harness', '20 MiB up + 80 MiB down through real Mihomo at 1x and 0.5x against the real backend'),
    ('B02.harness', 'ACK lost, the original batch retried once, charged once, with the real engine'),
    ('B04.harness', 'node_reported line: NodeAgent counts, /traffic refused'),
    ('A04.harness', 'rate change / disabled line / plan end / quota end stop the real connection and settle the tail'),
    ('C01.harness', 'disconnect then immediate reconnect through a real extension hand-over'),
)


def check_billing(harness=None, wanted=None):
    for package, names in BILLING_GO:
        pattern = '^(%s)$' % '|'.join(names)
        run = subprocess.run(['go', 'test', '-count=1', '-v', '-run', pattern, package], cwd=CORE,
                             env=dict(os.environ, GOFLAGS='-buildvcs=false'), capture_output=True, text=True)
        ran = set(re.findall(r'^--- PASS: (\w+)', run.stdout, re.M))
        missing = [name for name in names if name not in ran]
        record('billing.go.' + package.rsplit('/', 1)[-1], run.returncode == 0 and not missing,
               '%d/%d pinned Go tests passed' % (len(ran), len(names)) if not missing and run.returncode == 0
               else ('missing or failed: %s %s' % (', '.join(missing), run.stdout.strip()[-400:])))
    kit = subprocess.run(['swift', 'test'] + [arg for suite in BILLING_SWIFT for arg in ('--filter', suite)],
                         cwd=IOS / 'Packages/AsterLinkKit', capture_output=True, text=True)
    swift_passed = len(re.findall(r'✔ Test \S+ passed', kit.stdout + kit.stderr))
    record('billing.swift.kit', kit.returncode == 0 and swift_passed >= 20,
           '%d Swift tests passed (hand-over routing, alerts, usage, exact units)' % swift_passed if kit.returncode == 0
           else (kit.stdout + kit.stderr).strip()[-600:])
    stages = {'host': ('host',), 'simulator': ('simulator',), 'both': ('host', 'simulator')}.get(harness or '', ())
    for stage in stages:
        check_harness(stage, wanted)
    if not stages:
        for case_id, what in HARNESS_CASES:
            record(case_id, False, 'NOT_RUN: ' + what + '. Run with --harness host|simulator|both (needs initdb and pg_ctl); '
                   'the unit proofs above do not stand in for it', status='NOT_RUN')
    record('device-billing', False, 'BLOCKED: 20 MiB + 80 MiB through a real TUN needs a paid Apple team (packet-tunnel-provider) and a paired iPhone',
           status='BLOCKED')


def load_script(file_name, module_name):
    spec = importlib.util.spec_from_file_location(module_name, ROOT / 'scripts' / file_name)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def build_harness_runner(work):
    """The harness framework (tag asterlink_harness, simulator slice) and the Swift runner compiled against it."""
    build = subprocess.run([sys.executable, str(ROOT / 'scripts' / 'build-native-ios.py'), '--target', 'harness-framework'], capture_output=True, text=True)
    if build.returncode != 0:
        record('billing.simulator.build', False, 'harness framework: ' + (build.stdout + build.stderr)[-600:])
        return None
    framework = RUNTIME / 'harness/AsterCore.xcframework/ios-arm64_x86_64-simulator'
    sdk = subprocess.run(['xcrun', '--sdk', 'iphonesimulator', '--show-sdk-path'], capture_output=True, text=True, check=True).stdout.strip()
    runner = Path(work) / 'harness-runner'
    compiled = subprocess.run(['swiftc', '-sdk', sdk, '-target', 'arm64-apple-ios17.0-simulator', '-F', str(framework),
                               str(IOS / 'Harness/main.swift'), '-Xlinker', '-lresolv', '-o', str(runner)], capture_output=True, text=True)
    if compiled.returncode != 0:
        record('billing.simulator.build', False, 'runner: ' + (compiled.stdout + compiled.stderr)[-600:])
        return None
    record('billing.simulator.build', True, 'test-only Core framework (iOS Simulator slice, tag asterlink_harness) and the Swift runner built; '
           'neither is referenced by the app project')
    return runner


def check_harness(stage, wanted):
    """One full run of the harness acceptance in a fresh backend world (it mutates plans and lines)."""
    prefix = 'billing.%s.' % stage
    missing = [t for t in ('go', 'initdb', 'pg_ctl') if not shutil.which(t)]
    if missing:
        record(prefix + 'tools', False, 'not installed: %s' % ', '.join(missing), status='BLOCKED')
        return
    scratch = tempfile.mkdtemp(prefix='asterlink-ios-billing-')
    runner = udid = None
    if stage == 'simulator':
        udid = simulator_udid(wanted)
        if udid is None:
            shutil.rmtree(scratch, ignore_errors=True)
            return
        runner = build_harness_runner(scratch)
        if runner is None:
            shutil.rmtree(scratch, ignore_errors=True)
            return
    android = load_script('test-native-android.py', 'android_suite')
    helpers, pg_data, world, port = [], None, None, None
    try:
        # No REALITY inbound: the lines are Shadowsocks and plain VLESS (the TUN rounds cover REALITY).
        node, sink = android.start_helpers(argparse.Namespace(no_reality=True, reality_dest=''), scratch, helpers)
        agent = os.path.join(scratch, 'asterlink-nodeagent')
        subprocess.run(['go', 'build', '-o', agent, './cmd/asterlink-nodeagent'], cwd=ROOT / 'native' / 'nodeagent',
                       env=android.go_env(CGO_ENABLED='0'), check=True, capture_output=True)
        # The Android billing world is platform-neutral; the loopback is shared with the simulator.
        extra = {'ASTERLINK_ANDROID_TESTNODE': os.path.join(scratch, 'node.json'), 'ASTERLINK_ANDROID_NODE_HOST': '127.0.0.1',
                 'ASTERLINK_ANDROID_SINK': 'http://127.0.0.1:%s' % sink['port'], 'ASTERLINK_ANDROID_SINK_TOKEN': sink['token'],
                 'ASTERLINK_NODEAGENT_BIN': agent}
        pg_data, world, world_file, port = android.start_world(scratch, 'TestAndroidBillingWorld', 'ASTERLINK_ANDROID_BILLING_WORLD', extra)
        facts = json.loads(world_file.read_text())
        record(prefix + 'world', True, 'backend billing world on 127.0.0.1:%d (private PostgreSQL), test node + sink on the loopback; node agent: %s%s'
               % (port, 'yes' if facts.get('node_reported') else 'no',
                  '' if facts.get('agent_target_resolves') or not facts.get('node_reported') else ' (B04 target does not resolve here)'))
        cache = Path(scratch) / 'cache'
        cache.mkdir()
        config = {'world': facts, 'sink': {'host': '127.0.0.1', 'port': int(sink['port']), 'token': sink['token']},
                  'upload_mib': 20, 'download_mib': 80, 'background_bytes': 2 << 20, 'cache_dir': str(cache)}
        config_file, report_file = Path(scratch) / 'config.json', Path(scratch) / 'report.json'
        config_file.write_text(json.dumps(config))
        config_file.chmod(0o600)
        started = time.time()
        if stage == 'host':
            run = subprocess.run(['go', 'test', '-tags', 'asterlink_harness', '-count=1', '-timeout', '45m', '-run', '^TestAcceptance$', '-v', './internal/harness'],
                                 cwd=CORE, capture_output=True, text=True, timeout=3000,
                                 env=dict(os.environ, GOFLAGS='-buildvcs=false', ASTERLINK_HARNESS_CONFIG=str(config_file), ASTERLINK_HARNESS_REPORT=str(report_file)))
        else:
            run = subprocess.run(['xcrun', 'simctl', 'spawn', udid, str(runner), str(config_file), str(report_file)],
                                 capture_output=True, text=True, timeout=3000)
        log = run.stdout + run.stderr
        out = ARTIFACTS / 'billing'
        out.mkdir(parents=True, exist_ok=True)
        if not report_file.is_file():
            record(prefix + 'report', False, 'no report: ' + log[-800:])
            return
        report = json.loads(report_file.read_text())
        for case in report['cases']:
            record(prefix + case['case_id'], case['status'] == 'PASS', case['observed'], status=case['status'] if case['status'] == 'BLOCKED' else None)
        secrets = [facts['password'], node['ss_password'], node['vless_uuid'], sink['token']]
        leaked = [i for i, secret in enumerate(secrets) if secret and (secret in log or secret in report_file.read_text())]
        record(prefix + 'log_clean', not leaked, 'the run output (%d lines) and the report hold no password, node secret or sink token' % log.count('\n')
               if not leaked else 'the output contains secret #%s' % leaked)
        (out / ('harness-%s-report.json' % stage)).write_text(json.dumps({
            'environment': 'harness (%s)' % ('Go host test' if stage == 'host' else 'iOS Simulator process'),
            'seconds': round(time.time() - started), 'cases': report['cases']}, indent=2, ensure_ascii=False) + '\n')
    except (subprocess.SubprocessError, OSError, RuntimeError, KeyError, ValueError) as error:
        record(prefix + 'run', False, '%s: %s' % (type(error).__name__, str(error)[-600:]))
    finally:
        if port:
            try:
                import urllib.request
                urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:%d/__world/stop' % port, data=b'{}', method='POST'), timeout=10)
            except OSError:
                pass
        if world:
            try:
                world.wait(timeout=60)
            except subprocess.TimeoutExpired:
                world.kill()
        for helper in helpers:
            helper.terminate()
        if pg_data:
            subprocess.run(['pg_ctl', '-D', str(pg_data), '-m', 'fast', '-w', 'stop'], stdout=subprocess.DEVNULL, check=False)
        shutil.rmtree(scratch, ignore_errors=True)


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


# ---------------------------------------------------------------- lifecycle

LIFECYCLE_SWIFT = ('ForegroundLeaseTests', 'TunnelStopReasonTests', 'GateLogTests', 'ResourceSampleTests', 'LifecycleFlowTests')
LIFECYCLE_DEVICE_CASES = (
    ('D-L01', 'screen off 10 minutes, unlock: no manual reconnect, no forwarding while gated, gate length and reason logged'),
    ('D-L02', 'Wi-Fi <-> cellular and airplane mode: gate closes first, then re-verifies'),
    ('D-L03', 'another VPN takes over: traffic settled, notification names the other VPN'),
    ('D-L04', 'switched off in Settings: traffic settled, the app says why'),
    ('D-L05', 'app swiped away: tunnel stays up; reopened app shows connected without a login'),
    ('D-L06', 'phone restart: no automatic connection'),
    ('D-MEM', '30 minutes of heavy load without a jetsam kill; footprint peak recorded'),
    ('D-L07', '20 start/stop cycles and an 8 hour connection with samples (no guessed numbers)'),
)


def check_lifecycle():
    kit = subprocess.run(['swift', 'test'] + [arg for suite in LIFECYCLE_SWIFT for arg in ('--filter', suite)],
                         cwd=IOS / 'Packages/AsterLinkKit', capture_output=True, text=True)
    passed = len(re.findall(r'✔ Test \S+ passed', kit.stdout + kit.stderr))
    record('S-L01.kit', kit.returncode == 0 and passed >= 15,
           '%d Swift lifecycle tests passed' % passed if kit.returncode == 0 else (kit.stdout + kit.stderr).strip()[-600:])
    summarizer = ROOT / 'scripts' / 'ios' / 'summarize-samples.py'
    spec = importlib.util.spec_from_file_location('summarize_samples', summarizer)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    lines = ['sample t=%d footprint=%d peak=%d fds=40 threads=18 goroutines=80 heap=1 sys=2 gc=3' % (60 * i, (30 + i) << 20, (31 + i) << 20)
             for i in range(1, 6)]
    fine, code_fine = module.summarize(lines + ['gate reopened reason=wake outcome=reopened closed_ms=4200 attempts=2 total=1'])
    over, code_over = module.summarize(lines[:-1] + ['sample t=360 footprint=%d peak=%d fds=40 threads=18 goroutines=80 heap=1 sys=2 gc=3' % (46 << 20, 46 << 20)])
    leak, code_leak = module.summarize([l.replace('fds=40', 'fds=%d' % (40 + 10 * i)) for i, l in enumerate(lines)])
    _, code_none = module.summarize(['nothing here'])
    ok = (code_fine == 0 and fine['gate']['longest_closed_ms'] == 4200 and code_over == 1 and code_leak == 1 and code_none == 2)
    record('S-L01.summarizer', ok, 'within limits=%d, over the footprint limit=%d, open-file growth=%d, no samples=%d (expected 0,1,1,2)'
           % (code_fine, code_over, code_leak, code_none))
    identities = subprocess.run(['security', 'find-identity', '-v', '-p', 'codesigning'], capture_output=True, text=True).stdout
    count = re.search(r'(\d+) valid identities found', identities)
    signing = bool(count and int(count.group(1)) > 0)
    for case_id, what in LIFECYCLE_DEVICE_CASES:
        record(case_id, False, ('NOT_RUN: ' if signing else 'BLOCKED: needs a paid Apple team (packet-tunnel-provider) and a paired iPhone. ') + what
               + ' (docs/ios-lifecycle.md)', status='NOT_RUN' if signing else 'BLOCKED')


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
    record('simulator.harness_not_linked', 'HarnessRun' not in symbols and not any(
        b'HarnessRun' in f.read_bytes() for f in linked), 'neither the app extension nor the production framework carries the test-only harness')
    check_framework_clean(required=True)

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


# ---------------------------------------------------------------- ui (round 3)

# At least this many distinct screens must be captured and asserted (handoff_app_ios.md round 3).
MIN_SCREENSHOTS = 24


def xcodebuild_test(udid, scheme, result, extra_env=None, only=None, log_name=None, adhoc_sign=False):
    """Runs one scheme's tests in the simulator. TEST_RUNNER_<NAME> variables reach the test process as <NAME>."""
    if result.exists():
        shutil.rmtree(result)
    command = ['xcodebuild', 'test', '-project', str(IOS / 'AsterLink.xcodeproj'), '-scheme', scheme, '-destination', 'id=' + udid,
               '-derivedDataPath', str(DERIVED), '-resultBundlePath', str(result)]
    if adhoc_sign:
        # An unsigned simulator binary has no application identifier, so the Keychain refuses it
        # (-34018). Ad-hoc signing ("Sign to Run Locally") needs no Apple team and fixes that.
        command += ['CODE_SIGNING_ALLOWED=YES', 'CODE_SIGN_IDENTITY=-', 'DEVELOPMENT_TEAM=']
    else:
        command += ['CODE_SIGNING_ALLOWED=NO']
    for item in only or []:
        command += ['-only-testing:' + item]
    env = dict(os.environ, **{'TEST_RUNNER_' + k: v for k, v in (extra_env or {}).items()})
    run = subprocess.run(command, capture_output=True, text=True, env=env)
    (result.parent / (log_name or scheme + '-xcodebuild.log')).write_text(run.stdout + run.stderr)
    return run


def test_cases(result):
    """Every executed test case of an xcresult as (name, status)."""
    out = subprocess.run(['xcrun', 'xcresulttool', 'get', 'test-results', 'tests', '--path', str(result)], capture_output=True, text=True)
    cases = []

    def walk(node):
        if node.get('nodeType') == 'Test Case':
            cases.append((node.get('name', '?'), node.get('result', '?')))
        for child in node.get('children', []):
            walk(child)
    try:
        for node in json.loads(out.stdout).get('testNodes', []):
            walk(node)
    except ValueError:
        pass
    return cases


def check_ui(out_dir: Path, wanted):
    udid = simulator_udid(wanted)
    if udid is None:
        return
    shots = out_dir / 'screenshots'
    if shots.exists():
        shutil.rmtree(shots)
    shots.mkdir(parents=True)
    result = out_dir / 'ui.xcresult'
    run = xcodebuild_test(udid, 'AsterLinkUI', result, {'ASTER_SCREENSHOT_DIR': str(shots)})
    cases = test_cases(result)
    for name, status in cases:
        record('ui.' + name.rstrip('()'), status == 'Passed', status)
    failed = [n for n, s in cases if s != 'Passed']
    record('ui.suite', run.returncode == 0 and bool(cases) and not failed, '%d UI test cases, %d failed' % (len(cases), len(failed)))
    count = len(list(shots.glob('*.png')))
    record('ui.screenshots', count >= MIN_SCREENSHOTS,
           '%d screenshots captured under %s (>= %d required); each one is taken after its state assertion' % (count, shots.relative_to(ROOT), MIN_SCREENSHOTS))


# ---------------------------------------------------------------- account (round 3)

def free_port():
    import socket
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def start_world(scratch):
    """Private PostgreSQL (Unix socket only) + the backend's TestClientAccountWorld on 127.0.0.1."""
    data, sock = Path(scratch) / 'pg', Path(scratch) / 'sock'
    sock.mkdir(mode=0o700)
    env = dict(os.environ, LC_ALL='C', LANG='C')
    subprocess.run(['initdb', '-D', str(data), '-U', 'postgres', '--auth-local=trust', '--auth-host=reject', '--no-locale', '-E', 'UTF8'],
                   check=True, stdout=subprocess.DEVNULL, env=env)
    subprocess.run(['pg_ctl', '-D', str(data), '-l', str(Path(scratch) / 'postgres.log'), '-o', "-k %s -c listen_addresses=''" % sock, '-w', 'start'],
                   check=True, stdout=subprocess.DEVNULL, env=env)
    port = free_port()
    world_file = Path(scratch) / 'world.json'
    log = open(Path(scratch) / 'world.log', 'wb')
    world = subprocess.Popen(['go', 'test', '-count=1', '-timeout', '70m', '-run', '^TestClientAccountWorld$', '-v', './internal/api'], cwd=ROOT / 'backend',
                             env=dict(env, NATIVE_TEST_DSN='host=%s user=postgres dbname=postgres sslmode=disable' % sock, RUN_DB_TESTS='0',
                                      ASTERLINK_CLIENT_WORLD_PORT=str(port), GOFLAGS='-buildvcs=false', ASTERLINK_CLIENT_WORLD=str(world_file)),
                             stdout=log, stderr=subprocess.STDOUT)
    for _ in range(3000):
        if world_file.is_file() and world_file.stat().st_size > 0:
            return data, world, world_file, port
        if world.poll() is not None:
            break
        time.sleep(0.1)
    raise RuntimeError('the backend world did not start: ' + (Path(scratch) / 'world.log').read_text(errors='replace')[-1200:])


def check_account(out_dir: Path, wanted):
    missing = [t for t in ('go', 'initdb', 'pg_ctl') if not shutil.which(t)]
    if missing:
        record('account.tools', False, 'not installed: %s' % ', '.join(missing), status='BLOCKED')
        return
    udid = simulator_udid(wanted)
    if udid is None:
        return
    import tempfile
    import urllib.request
    scratch = tempfile.mkdtemp(prefix='asterlink-ios-account-')
    pg_data, world, port = None, None, None
    try:
        pg_data, world, world_file, port = start_world(scratch)
        facts = json.loads(world_file.read_text())
        secrets = [facts['password'], 'metadata-private.example.invalid', 'never-reaches-the-ui', 'aes-256-gcm']
        record('account.world', True, 'backend TestClientAccountWorld on 127.0.0.1:%d (private PostgreSQL on a Unix socket, synthetic accounts)' % port)
        report_file = Path(scratch) / 'report.json'
        result = out_dir / 'account.xcresult'
        run = xcodebuild_test(udid, 'AsterLink', result, {'ASTERLINK_WORLD_FILE': str(world_file), 'ASTERLINK_REPORT_FILE': str(report_file)},
                              only=['AsterLinkTests/AccountAcceptanceTests'], log_name='account-xcodebuild.log', adhoc_sign=True)
        if not report_file.is_file():
            record('account.report', False, 'no report from the simulator: ' + (run.stdout + run.stderr)[-1200:])
            return
        cases = json.loads(report_file.read_text())['cases']
        for case in cases:
            record('account.' + case['case_id'], case['status'] == 'PASS', case['observed'], status=case['status'])
        (out_dir / 'account-report.json').write_text(json.dumps({'cases': cases}, indent=2, ensure_ascii=False) + '\n')
        # The simulator's system log must not carry the password or a node secret either.
        log = simctl('spawn', udid, 'log', 'show', '--last', '15m', '--style', 'compact', check=False).stdout
        leaked = [i for i, secret in enumerate(secrets) if secret in log]
        record('account.log_clean', not leaked, 'simulator log (%d lines) holds no password, node address, node secret or cipher' % log.count('\n')
               if not leaked else 'log contains secret #%s' % leaked)
    finally:
        if port:
            try:
                urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:%d/__world/stop' % port, data=b'{}', method='POST'), timeout=10)
            except OSError:
                pass
        if world:
            try:
                world.wait(timeout=60)
            except subprocess.TimeoutExpired:
                world.kill()
        if pg_data:
            subprocess.run(['pg_ctl', '-D', str(pg_data), '-m', 'immediate', 'stop'], capture_output=True)
        shutil.rmtree(scratch, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--suite', required=True, choices=('foundation', 'unit', 'simulator', 'ui', 'account', 'billing', 'device-tun', 'lifecycle'))
    parser.add_argument('--report', help='write a JSON report here')
    parser.add_argument('--harness', choices=('host', 'simulator', 'both'),
                        help='billing suite: also run the harness acceptance (needs initdb and pg_ctl; changes no network setting)')
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
        check_harness_isolation()
    elif args.suite == 'unit':
        check_unit()
    elif args.suite == 'simulator':
        check_simulator(out_dir, args.runtime)
    elif args.suite == 'ui':
        check_ui(out_dir, args.runtime)
    elif args.suite == 'account':
        check_account(out_dir, args.runtime)
    elif args.suite == 'billing':
        check_billing(args.harness, args.runtime)
    elif args.suite == 'device-tun':
        check_device(args.device)
    elif args.suite == 'lifecycle':
        check_lifecycle()

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

#!/usr/bin/env python3
"""Android client test entry point (handoff_app_android.md 10, 12).

Implemented suites (round 1):
  foundation  needs Go and the NDK (for android/amd64 vet); no emulator or JDK
    * apps/android/toolchain.lock.json agrees with the version catalog, the Gradle wrapper,
      native/core/go.mod (x/mobile), the Mihomo submodule and .github/workflows/android.yml
    * apps/android/.env.example is accepted by the Python env-v1 parser
    * the Go platform split: an Android build links only the Android platform files (never
      the macOS utun/networksetup/route files or the do-nothing placeholder), and
      `go vet` passes for android/arm64 and android/amd64
    * the manifest baseline: backup off, every data domain excluded from cloud backup and
      device transfer, no cleartext, system CAs only; exactly the round-2 permissions and one
      VpnService (BIND_VPN_SERVICE, not exported, specialUse foreground service, no always-on)
  unit        needs Go, a JDK and the Core AAR (scripts/build-native-android.py --target aar)
    * Go host tests of the binding, IPC, host-network and managed packages (non-zero count)
    * Gradle: build-logic env-v1 contract tests and the app's JVM unit tests (non-zero count)
  emulator    needs the SDK emulator and a system image; changes nothing outside the emulator
    * boots (or reuses) an AVD, installs the Debug APK, runs the instrumented CoreBindingTest
      against the real AAR, launches the app and checks it shows the Core version and the
      Mihomo commit, and runs the Go binding tests cross-compiled for the emulator's ABI
      (the login request there must say platform=android)
    * records API level, ABI and kernel page size; a 16 KB page image is a separate AVD

Round 2:
  tun         needs Go, a JDK, the SDK emulator and a system image; --allow-network-changes
    * binds the Core AAR with the debug-only acceptance hooks and builds the test APK
    * starts cmd/asterlink-testnode (pinned Mihomo as Shadowsocks/VLESS/REALITY server) and
      cmd/asterlink-trafficsink on the host loopback only; pre-grants VPN consent (appops)
    * runs TunAcceptanceTest in the app (AsterVpnService, VpnService.Builder, fd hand-off,
      protect, Verify): T01 path proven by the node's own counter, T02 SS/VLESS/REALITY and a
      wrong key, T03 DNS (fake-ip, Private DNS off/opportunistic/strict, port-53 hijack, UDP),
      T04 IPv6, T05 control plane and loop, T06 udp:false never DIRECT, T07 stop, T-CYCLES;
      then T09 kills a connected app and requires the VPN to vanish
    * only the emulator's network changes; the user's own nodes (T02-real) come only from
      ASTERLINK_TUN_PROXY_JSON / ASTERLINK_TUN_REAL_NODES (disconnect a company VPN first)

Round 3:
  ui          needs a JDK and the Core AAR; no emulator
    * the Roborazzi screenshot set (ScreenshotTest: login, connect, lines, me, every empty and
      error state, dark, 200 % font, 360 dp, English) rendered on the JVM; PNGs are copied to
      artifacts/native-android/ui/ and at least 20 states are required
  account     needs Go, PostgreSQL (initdb/pg_ctl), a JDK, the SDK emulator and a system image
    * starts the backend's TestAndroidAccountWorld on a private Unix-socket PostgreSQL: the real
      Gin API with seeded synthetic accounts and lines, on 127.0.0.1 only; `adb reverse` lets
      the emulator reach it as 127.0.0.1 (the Core's loopback-only HTTP rule, no 10.0.2.2)
    * runs AccountAcceptanceTest in the app: the real SessionController, Go Core and Keystore
      credential store against that API (A01 sign-in/remember/relaunch/sign-out, A02 no plan,
      zero quota, expiry, device limit, A04 admin rename/rate/disable/enable/delete timings)
    * then searches logcat for the password, node address, node secret and cipher
    * never touches the remote test database, demo.hyshentou.cn or the emulator's network

Round 4:
  billing     needs Go, PostgreSQL, a JDK, the SDK emulator and a system image; --allow-network-changes
    * builds the normal debug APK (no acceptance hook in the AAR) and the test APK
    * starts the round-2 test node and traffic sink on the host loopback, builds the real node
      agent (native/nodeagent), and the backend's TestAndroidBillingWorld on a private
      Unix-socket PostgreSQL: the real Gin API whose lines lead to the test node (emulator view
      10.0.2.2) plus, with the agent, one node-reported line over real mTLS
    * runs BillingAcceptanceTest in the app: the real SessionController, AsterVpnService and Go
      Core connect for real (gates, first ACK, TUN, Verify) and move controlled bytes to the sink:
      B01 1x and 0.5x charged to the unit (server ledger = Core counters = app display), B02 a lost
      acknowledgement (dropped by the world after commit) resent unchanged and charged once,
      A04 re-rate / disable / plan end / quota exhaustion each stop the tunnel by itself with its
      reason and settle the tail at the bound rate, N01/N02 the live notification and the stop
      alert, B04 the node agent bills a node-reported connection with no client report
    * then searches logcat for the password, node secrets and the sink token
    * only the emulator's network changes; nothing reaches demo.hyshentou.cn or the remote database
      (Verify still probes its public URLs through the test node, i.e. through this Mac's network)

Round 5:
  lifecycle   needs Go, PostgreSQL, a JDK, the SDK emulator and a system image; --allow-network-changes
    * the billing suite's build, test node, sink and TestAndroidBillingWorld (user "life")
    * runs LifecycleAcceptanceTest in the app, changing the emulator like a phone changes (shell
      commands through UiAutomation, all undone afterwards): N01 notifications denied, L01 a
      15 s stall (the process SIGSTOPped and continued, so its clocks jump like after deep sleep),
      screen off (--screen-off-seconds) and a closed gate holding traffic while the server is
      unreachable, L02 Doze, 7.6 the catalog poll in the background and a verdict there, L03
      Wi-Fi <-> cellular and airplane mode, L04 a second VPN app (test APK) taking over, L05 the
      task removed, L07 cycles and a short soak (--soak-seconds; samples to soak-samples.json)
    * then from the host: L05 `am kill` (must not touch a foreground service) and force-stop
      (no VPN, no service left), L06 a reboot (nothing connects by itself; --no-reboot skips it)
    * L07-8h and L08 (Chrome DoH, WebRTC) are NOT_RUN here: docs/android-lifecycle.md
    * only the emulator's network changes; nothing reaches demo.hyshentou.cn or the remote database

Round 6:
  release     audits a packaged release folder and rehearses install/upgrade/uninstall
              (scripts/android_release_suite.py; docs/android-release.md)
    * R03 (no emulator): every APK of --release-dir (default artifacts/native-android/release, made by
      scripts/package-native-android.py) verifies with apksigner v2+v3 and one signer whose certificate
      is the one release-info.json names (a throwaway key is reported BLOCKED: not publishable);
      manifest facts (package, derived versionCode, minSdk 26, targetSdk 36, not debuggable/testOnly,
      no cleartext, backup off, permission allowlist, no test activity); zipalign -P 16 and every .so
      stored, ELF for its ABI with 16 KB LOAD alignment; no test classes, TUN hooks or debug tooling in
      the dex or libgojni.so; no secret (local .env values, private keys, connection strings, the key
      password) anywhere in the APK, with a planted-secret self check; the HTTPS API base baked in;
      notices, inventory, SOURCE.txt, source archives and SHA256SUMS; the R8 mapping kept out of it
    * R01/R02 rehearsal (--allow-network-changes, an emulator): the same release pipeline (R8, splits,
      apksigner) built twice (version and version+0.0.1) against TestAndroidBillingWorld on a loopback
      port and signed with one throwaway key; through the real UI: fresh install, sign in with
      "remember password", pick a line, the system VPN consent dialog, connected, disconnected;
      upgrade in place (signed in again by itself), downgrade refused, a different key refused,
      uninstall while connected (VPN, package, consent and data gone; a reinstall starts empty)
    * R01 per image (--avds, default api36,api36-16k,api26): the published APK installs, starts the
      Core (its version and Mihomo commit on the login screen) with no crash; missing images are BLOCKED
    * R04: --device <serial> runs the same on a real phone; without one it is BLOCKED
    * --static-only runs R03 alone (CI without an emulator for the publish ABI)

Exit codes: 0 = PASS, 1 = FAIL, 3 = BLOCKED (a prerequisite is missing, so nothing was proven).
The grants suite is not part of the Android plan (B04 runs inside the billing suite).
"""
import argparse
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
from urllib.parse import urlsplit
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ANDROID = ROOT / 'apps' / 'android'
CORE = ROOT / 'native' / 'core'
sys.path.insert(0, str(ROOT / 'scripts'))
import native_env  # noqa: E402
from loopback_port import free_port  # noqa: E402,F401 (android_release_suite uses ctx.free_port)

LOCK = json.loads((ANDROID / 'toolchain.lock.json').read_text())
EXIT_PASS, EXIT_FAIL, EXIT_BLOCKED = 0, 1, 3
ANDROID_NS = '{http://schemas.android.com/apk/res/android}'
results = []


def record(case_id, ok, observed, status=None):
    status = status or ('PASS' if ok else 'FAIL')
    results.append({'case_id': case_id, 'status': status, 'observed': observed})
    print('%-8s %-30s %s' % (status, case_id, observed), flush=True)


def go_env(**extra):
    env = dict(os.environ, GOFLAGS='-buildvcs=false')
    env.update(extra)
    return env


# ---------------------------------------------------------------- foundation

def check_lock_file():
    catalog = (ANDROID / 'gradle' / 'libs.versions.toml').read_text()

    def catalog_version(key):
        match = re.search(r'^%s = "([^"]+)"' % re.escape(key), catalog, re.M)
        return match and match.group(1)

    pairs = {'agp': LOCK['agp']['version'], 'kotlin': LOCK['kotlin']['version'], 'compose-bom': LOCK['compose_bom']['version']}
    mismatched = {k: (catalog_version(k), v) for k, v in pairs.items() if catalog_version(k) != v}
    record('lock.catalog', not mismatched, 'catalog == lock for %s' % ', '.join(pairs) if not mismatched else 'catalog vs lock: %s' % mismatched)

    wrapper = (ANDROID / 'gradle' / 'wrapper' / 'gradle-wrapper.properties').read_text()
    url = re.search(r'^distributionUrl=(.+)$', wrapper, re.M).group(1).replace('\\:', ':')
    sha = re.search(r'^distributionSha256Sum=(\w+)$', wrapper, re.M)
    record('lock.gradle_wrapper', url == LOCK['gradle']['distribution_url'] and sha and sha.group(1) == LOCK['gradle']['distribution_sha256'],
           'wrapper pins %s with its SHA-256' % url.rsplit('/', 1)[-1])
    import hashlib
    jar = hashlib.sha256((ANDROID / 'gradle' / 'wrapper' / 'gradle-wrapper.jar').read_bytes()).hexdigest()
    record('lock.wrapper_jar', jar == LOCK['gradle']['wrapper_jar_sha256'], 'gradle-wrapper.jar sha256 %s' % jar[:16])

    gomod = (CORE / 'go.mod').read_text()
    pinned = re.search(r'golang.org/x/mobile (\S+)', gomod)
    tools = re.search(r'^tool \(\s*golang.org/x/mobile/cmd/gobind\s+golang.org/x/mobile/cmd/gomobile\s*\)', gomod, re.M)
    record('lock.x_mobile', bool(pinned and tools) and pinned.group(1) == LOCK['x_mobile']['version'],
           'native/core/go.mod pins x/mobile %s as a tool' % (pinned and pinned.group(1)))
    for linked in ('golang.org/x/sys v0.30.0', 'golang.org/x/net v0.35.0', 'golang.org/x/crypto v0.33.0'):
        if linked not in gomod:
            record('lock.desktop_deps', False, 'native/core/go.mod no longer has %s: the binding moved the desktop Core dependencies' % linked)
            break
    else:
        record('lock.desktop_deps', True, 'x/sys 0.30, x/net 0.35, x/crypto 0.33 unchanged for the desktop Core')

    status = subprocess.run(['git', 'submodule', 'status', 'native/third_party/Clash.Meta'], cwd=ROOT, capture_output=True, text=True).stdout
    record('lock.mihomo', LOCK['mihomo']['commit'] in status, 'submodule at %s' % status.split()[0].lstrip('-+U')[:12] if status else 'submodule missing')

    workflow = (ROOT / '.github' / 'workflows' / 'android.yml').read_text()
    expected = {
        'GO_VERSION': LOCK['go']['version'],
        'ANDROID_NDK_VERSION': LOCK['ndk']['version'],
        'ANDROID_PLATFORM': LOCK['android']['compile_sdk_package'],
        'JAVA_VERSION': str(LOCK['jdk']['major']),
    }
    wrong = {k: v for k, v in expected.items() if not re.search(r"^\s+%s: '%s'$" % (k, re.escape(v)), workflow, re.M)}
    record('lock.workflow', not wrong and 'runs-on: ' + LOCK['ci']['runner_label'] in workflow and 'ubuntu-latest' not in workflow,
           'android.yml pins %s on %s' % (', '.join(expected), LOCK['ci']['runner_label']) if not wrong else 'android.yml differs: %s' % wrong)

    gradle = (ANDROID / 'app' / 'build.gradle.kts').read_text()
    sdk = {'compileSdk': LOCK['android']['compile_sdk'], 'minSdk': LOCK['android']['min_sdk'], 'targetSdk': LOCK['android']['target_sdk']}
    wrong = {k: v for k, v in sdk.items() if not re.search(r'\b%s = %d\b' % (k, v), gradle)}
    record('lock.sdk_levels', not wrong, 'compileSdk %(compileSdk)d, minSdk %(minSdk)d, targetSdk %(targetSdk)d' % sdk if not wrong else 'build.gradle.kts differs: %s' % wrong)


def check_env_example():
    try:
        values = native_env.load_env_file(ANDROID / '.env.example')
        record('env.example', values['API_BASE_URL'].startswith('https://'), 'accepted: %s / %s' % (values['APP_ENV'], values['API_BASE_URL']))
    except native_env.EnvError as error:
        record('env.example', False, str(error))
    ignored = subprocess.run(['git', 'check-ignore', '-q', 'apps/android/.env'], cwd=ROOT).returncode == 0
    record('env.real_file_ignored', ignored, 'apps/android/.env is git-ignored (never committed)')
    # An unanchored ignore rule once hid build-logic/src/.../com/asterlink/build/ from git.
    sources = [str(p.relative_to(ROOT)) for p in ANDROID.glob('*/src/**/*') if p.is_file()]
    hidden = subprocess.run(['git', 'check-ignore', '--stdin'], cwd=ROOT, input='\n'.join(sources), capture_output=True, text=True).stdout.split()
    record('repo.sources_tracked', bool(sources) and not hidden, '%d source files under apps/android/*/src, none git-ignored' % len(sources) if not hidden else 'ignored: %s' % hidden[:5])


def check_platform_split():
    if not shutil.which('go'):
        record('go.toolchain', False, 'go not found; nothing was tested', status='BLOCKED')
        return
    process = subprocess.run(['go', 'test', '-count=1', '-run', 'TestAndroidBuildLinksOnlyAndroidPlatformFiles', './mobile'],
                             cwd=CORE, capture_output=True, text=True, env=go_env())
    record('go.platform_split', process.returncode == 0,
           'android/{arm64,amd64,arm} link only *_android.go platform files' if process.returncode == 0 else process.stdout[-400:] + process.stderr[-400:])
    # The packages an Android build compiles (the cmd/ tools are desktop and test programs).
    # The go command refuses android/amd64 without cgo, so that vet uses the NDK compiler.
    for arch, abi in (('arm64', 'arm64-v8a'), ('amd64', 'x86_64')):
        env = go_env(GOOS='android', GOARCH=arch, CGO_ENABLED='0')
        if arch == 'amd64':
            clang = ndk_clang(sdk_root(), abi) if sdk_root() else None
            if clang is None:
                record('go.vet_android_' + arch, False, 'NDK %s not installed (android/amd64 needs cgo)' % LOCK['ndk']['version'], status='BLOCKED')
                continue
            env.update(CGO_ENABLED='1', CC=str(clang))
        vet = subprocess.run(['go', 'vet', './internal/...', './mobile/'], cwd=CORE, capture_output=True, text=True, env=env)
        record('go.vet_android_' + arch, vet.returncode == 0, 'go vet ./internal/... ./mobile for android/%s' % arch if vet.returncode == 0 else vet.stderr[-400:])


def check_manifest():
    manifest = ET.parse(ANDROID / 'app' / 'src' / 'main' / 'AndroidManifest.xml').getroot()
    app = manifest.find('application')
    attrs = {k.replace(ANDROID_NS, ''): v for k, v in app.attrib.items()}
    permissions = sorted(p.get(ANDROID_NS + 'name') for p in manifest.findall('uses-permission'))
    ok = (attrs.get('allowBackup') == 'false' and attrs.get('fullBackupContent') == 'false'
          and attrs.get('dataExtractionRules') == '@xml/data_extraction_rules'
          and attrs.get('networkSecurityConfig') == '@xml/network_security_config'
          and 'usesCleartextTraffic' not in attrs and 'debuggable' not in attrs)
    record('manifest.baseline', ok, 'backup off, extraction rules and network security config set; permissions %s' % permissions)
    expected = ['android.permission.ACCESS_NETWORK_STATE', 'android.permission.CHANGE_NETWORK_STATE', 'android.permission.FOREGROUND_SERVICE',
                'android.permission.FOREGROUND_SERVICE_SPECIAL_USE', 'android.permission.INTERNET', 'android.permission.POST_NOTIFICATIONS']
    record('manifest.permissions', permissions == expected, 'exactly %s' % ', '.join(p.rsplit('.', 1)[-1] for p in permissions))
    services = app.findall('service')
    by_name = {s.get(ANDROID_NS + 'name'): s for s in services}
    vpn = by_name.get('.vpn.AsterVpnService') if len(services) == 2 else None
    sattrs = {k.replace(ANDROID_NS, ''): v for k, v in (vpn.attrib.items() if vpn is not None else [])}
    actions = [a.get(ANDROID_NS + 'name') for f in (vpn.findall('intent-filter') if vpn is not None else []) for a in f.findall('action')]
    meta = {m.get(ANDROID_NS + 'name'): m.get(ANDROID_NS + 'value') for m in (vpn.findall('meta-data') if vpn is not None else [])}
    props = {m.get(ANDROID_NS + 'name'): m.get(ANDROID_NS + 'value') for m in (vpn.findall('property') if vpn is not None else [])}
    record('manifest.vpn_service', vpn is not None and sattrs.get('name') == '.vpn.AsterVpnService'
           and sattrs.get('permission') == 'android.permission.BIND_VPN_SERVICE' and sattrs.get('exported') == 'false'
           and sattrs.get('foregroundServiceType') == 'specialUse' and actions == ['android.net.VpnService']
           and meta.get('android.net.VpnService.SUPPORTS_ALWAYS_ON') == 'false'
           and props.get('android.app.PROPERTY_SPECIAL_USE_FGS_SUBTYPE') == 'vpn',
           'one VpnService: bound only by the system (BIND_VPN_SERVICE), not exported, specialUse foreground service, always-on refused')
    tile = by_name.get('.vpn.ConnectTileService')
    tattrs = {k.replace(ANDROID_NS, ''): v for k, v in (tile.attrib.items() if tile is not None else [])}
    tactions = [a.get(ANDROID_NS + 'name') for f in (tile.findall('intent-filter') if tile is not None else []) for a in f.findall('action')]
    record('manifest.tile_service', tile is not None and tattrs.get('permission') == 'android.permission.BIND_QUICK_SETTINGS_TILE'
           and tactions == ['android.service.quicksettings.action.QS_TILE'] and 'foregroundServiceType' not in tattrs,
           'the only other service is the Quick Settings tile, bindable only by the system UI (BIND_QUICK_SETTINGS_TILE)')

    rules = ET.parse(ANDROID / 'app' / 'src' / 'main' / 'res' / 'xml' / 'data_extraction_rules.xml').getroot()
    domains = {'root', 'file', 'database', 'sharedpref', 'external'}
    covered = all({e.get('domain') for e in rules.find(section).findall('exclude')} == domains for section in ('cloud-backup', 'device-transfer'))
    record('manifest.extraction_rules', covered, 'every domain excluded from cloud backup and device transfer')

    nsc = ET.parse(ANDROID / 'app' / 'src' / 'main' / 'res' / 'xml' / 'network_security_config.xml').getroot()
    base = nsc.find('base-config')
    anchors = [c.get('src') for c in base.find('trust-anchors').findall('certificates')]
    record('manifest.network_security', base.get('cleartextTrafficPermitted') == 'false' and anchors == ['system'] and nsc.find('domain-config') is None,
           'cleartext refused, system CAs only, no per-domain exceptions')


def check_release_config():
    """Round 6: the release build type is R8-minified with the gomobile keep rules, unsigned in Gradle
    (apksigner signs it; no key or password in the build files), and versionCode is derived."""
    gradle_file = (ANDROID / 'app' / 'build.gradle.kts').read_text()
    rules = (ANDROID / 'app' / 'proguard-rules.pro').read_text() if (ANDROID / 'app' / 'proguard-rules.pro').is_file() else ''
    release = gradle_file[gradle_file.find('release {'):gradle_file.find('splits {')]
    problems = []
    if 'isMinifyEnabled = true' not in release or 'proguard-rules.pro' not in release:
        problems.append('release is not R8-minified with proguard-rules.pro')
    for keep in ('-keep class go.** { *; }', '-keep class com.asterlink.core.** { *; }', 'implements com.asterlink.core.mobile.Platform'):
        if keep not in rules:
            problems.append('keep rule missing: ' + keep)
    if re.search(r'signingConfig\s*=|signingConfigs\s*\{|storePassword|keyPassword|storeFile', gradle_file):
        problems.append('a signing config in Gradle (the release key belongs to apksigner only)')
    if re.search(r'^asterlink\.versionCode=', (ANDROID / 'gradle.properties').read_text(), re.M):
        problems.append('a hand-written versionCode in gradle.properties')
    if 'keepDebugSymbols += "**/libgojni.so"' not in gradle_file:
        problems.append('AGP would strip libgojni.so (not the reproducible bound file)')
    record('release.config', not problems, 'release: R8 + gomobile keep rules, unsigned in Gradle (apksigner v2+v3 in package-native-android.py), versionCode derived, libgojni.so not re-stripped'
           if not problems else '; '.join(problems))


# ---------------------------------------------------------------- unit

def failure_excerpt(output):
    """The failed tests and their assertion messages from a Gradle log, else its tail."""
    lines = output.splitlines()
    picked = []
    for index, line in enumerate(lines):
        if ' FAILED' in line or 'AssertionError' in line or 'Exception' in line or line.startswith('e: '):
            picked += lines[index:index + 2]
    return ' | '.join(picked)[:1500] if picked else output[-600:]


def count_go_tests(packages):
    process = subprocess.run(['go', 'test', '-count=1', '-json', *packages], cwd=CORE, capture_output=True, text=True, env=go_env())
    passed, failed = 0, []
    for line in process.stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get('Test') and event.get('Action') == 'pass':
            passed += 1
        elif event.get('Test') and event.get('Action') == 'fail':
            failed.append(event['Test'])
    return process.returncode, passed, failed


def junit_counts(directory: Path):
    tests = failures = 0
    for report in directory.rglob('TEST-*.xml'):
        suite = ET.parse(report).getroot()
        tests += int(suite.get('tests', 0))
        failures += int(suite.get('failures', 0)) + int(suite.get('errors', 0))
    return tests, failures


def sdk_root():
    for key in ('ANDROID_HOME', 'ANDROID_SDK_ROOT'):
        if os.environ.get(key):
            return Path(os.environ[key])
    return None


def gradle(args, **kwargs):
    wrapper = ANDROID / ('gradlew.bat' if os.name == 'nt' else 'gradlew')
    return subprocess.run([str(wrapper), '--console=plain', *args], cwd=ANDROID, **kwargs)


def gradle_failure(process):
    """Gradle writes the reason ("What went wrong") to stderr; stdout alone ends with BUILD FAILED."""
    err = process.stderr or ''
    reason = err[err.find('* What went wrong'):] if '* What went wrong' in err else err
    return (reason.split('* Try:')[0].strip() or process.stdout)[-1200:]


def run_unit_suite():
    if not shutil.which('go'):
        record('go.toolchain', False, 'go not found; nothing was tested', status='BLOCKED')
    else:
        code, passed, failed = count_go_tests(['./mobile', './internal/ipc', './internal/network', './internal/managed', './internal/engine', './cmd/asterlink-testnode'])
        record('go.unit', code == 0 and passed > 0 and not failed, '%d Go tests passed%s' % (passed, ', failed: %s' % failed if failed else ''))
    if sdk_root() is None:
        record('gradle.unit', False, 'ANDROID_HOME not set; JVM tests not run', status='BLOCKED')
        return
    if not (ANDROID / 'build' / 'core' / 'asterlink-core.aar').is_file():
        record('gradle.unit', False, 'Core AAR missing: python3 scripts/build-native-android.py --target aar', status='BLOCKED')
        return
    logic = gradle(['-p', 'build-logic', 'test'], capture_output=True, text=True)
    tests, failures = junit_counts(ANDROID / 'build-logic' / 'build' / 'test-results' / 'test')
    record('gradle.env_contract', logic.returncode == 0 and tests > 0 and failures == 0,
           '%d build-logic tests (env-v1 parser) passed' % tests if logic.returncode == 0 else logic.stdout[-600:])
    app = gradle([':app:testDebugUnitTest'], capture_output=True, text=True)
    tests, failures = junit_counts(ANDROID / 'app' / 'build' / 'test-results' / 'testDebugUnitTest')
    record('gradle.app_unit', app.returncode == 0 and tests > 0 and failures == 0,
           '%d app JVM tests passed' % tests if app.returncode == 0 else failure_excerpt(app.stdout))


# ---------------------------------------------------------------- emulator

def tool(sdk: Path, *parts):
    path = sdk.joinpath(*parts)
    return path if path.exists() else None


def adb_run(adb, serial, *args, check=True, timeout=120):
    process = subprocess.run([str(adb), '-s', serial, *args], capture_output=True, text=True, timeout=timeout)
    if check and process.returncode != 0:
        output = (process.stderr or process.stdout).strip()
        # The reason (e.g. INSTALL_FAILED_*, the exception type) is at the start; a bare tail is only a stack.
        if len(output) > 600:
            output = output[:300] + ' ... ' + output[-300:]
        raise RuntimeError('adb %s failed: %s' % (' '.join(args), output))
    return process.stdout


def host_abi():
    return 'arm64-v8a' if platform.machine().lower() in ('arm64', 'aarch64') else 'x86_64'


def ensure_avd(sdk, name, image):
    avdmanager = tool(sdk, 'cmdline-tools', 'latest', 'bin', 'avdmanager')
    listing = subprocess.run([str(avdmanager), 'list', 'avd', '-c'], capture_output=True, text=True).stdout.split()
    if name in listing:
        return
    subprocess.run([str(avdmanager), 'create', 'avd', '-n', name, '-k', image, '-d', 'pixel_6', '--force'],
                   input='no\n', capture_output=True, text=True, check=True)


def boot(sdk, adb, name, keep_window, log_path):
    running = subprocess.run([str(adb), 'devices'], capture_output=True, text=True).stdout
    serials = re.findall(r'^(emulator-\d+)\s+device$', running, re.M)
    for serial in serials:
        if adb_run(adb, serial, 'emu', 'avd', 'name', check=False).splitlines()[:1] == [name]:
            return serial, None
    # The first free console port: another AVD (e.g. the API 36 one of an earlier suite) may still run.
    port = next(p for p in range(5580, 5640, 2) if 'emulator-%d' % p not in serials)
    args = [str(tool(sdk, 'emulator', 'emulator')), '-avd', name, '-port', str(port), '-no-snapshot-save', '-no-boot-anim', '-no-audio', '-no-metrics']
    if not keep_window:
        args += ['-no-window', '-gpu', 'swiftshader_indirect']
    # The emulator's own output is kept: it is the only diagnosis when it refuses to start.
    log = open(log_path, 'wb')
    process = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT)
    serial = 'emulator-%d' % port
    deadline = time.time() + 600
    while time.time() < deadline:
        if process.poll() is not None:
            log.close()
            tail = Path(log_path).read_text(errors='replace')[-1500:]
            raise RuntimeError('the emulator exited with %s:\n%s' % (process.returncode, tail))
        state = subprocess.run([str(adb), '-s', serial, 'shell', 'getprop', 'sys.boot_completed'], capture_output=True, text=True).stdout.strip()
        # boot_completed comes before the package manager settles on a cold boot; installs then fail.
        if state == '1' and adb_run(adb, serial, 'shell', 'pm', 'path', 'android', check=False).startswith('package:'):
            settle(adb, serial)
            return serial, process
        time.sleep(5)
    process.kill()
    raise RuntimeError('the emulator did not finish booting in 10 minutes')


def settle(adb, serial, limit=180):
    """A cold-booted emulator on a hosted runner keeps working long after boot_completed: system
    and System UI ANR dialogs, failed installs and instrumentation runs, no network yet. Wait (at
    most `limit` seconds) for a default network and for the load to fall below the vCPU count.
    Not settling is reported, not fatal: the cases themselves decide."""
    for scale in ('window_animation_scale', 'transition_animation_scale', 'animator_duration_scale'):
        adb_run(adb, serial, 'shell', 'settings', 'put', 'global', scale, '0', check=False)
    cpus = int(adb_run(adb, serial, 'shell', 'nproc', check=False).strip() or 1)
    started = time.time()
    network = load = None
    while time.time() - started < limit:
        default = adb_run(adb, serial, 'shell', 'dumpsys connectivity | grep -m1 "Active default network"', check=False)
        network = re.search(r'Active default network: (\d+)', default)
        load = float((adb_run(adb, serial, 'shell', 'cat', '/proc/loadavg', check=False).split() or ['99'])[0])
        if network and load < cpus:
            break
        time.sleep(5)
    print('emulator settled after %.0f s: default network %s, load %.2f on %d vCPU%s' % (
        time.time() - started, network.group(1) if network else 'none', load, cpus,
        '' if network and load < cpus else ' (NOT settled; continuing)'), flush=True)


def ui_text(adb, serial):
    adb_run(adb, serial, 'shell', 'uiautomator', 'dump', '/data/local/tmp/ui.xml')
    return adb_run(adb, serial, 'shell', 'cat', '/data/local/tmp/ui.xml')


def dismiss_foreign_anr(adb, serial, screen):
    """A slow emulator's own ANR dialog ("Process system isn't responding") covers the app; press Wait.
    One about AsterLink itself is never dismissed. Returns the stalled process's name, or None."""
    match = re.search(r'text="([^"]+) isn(?:\'|&apos;|&#39;)t responding"', screen)
    if not match or 'AsterLink' in match.group(1):
        return None
    wait = re.search(r'text="Wait"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"', screen)
    if wait:
        x1, y1, x2, y2 = map(int, wait.groups())
        adb_run(adb, serial, 'shell', 'input', 'tap', str((x1 + x2) // 2), str((y1 + y2) // 2), check=False)
    else:
        adb_run(adb, serial, 'shell', 'input', 'keyevent', 'KEYCODE_BACK', check=False)
    return match.group(1)


def ndk_clang(sdk, abi):
    """The NDK compiler for an Android executable (android/amd64 must link externally)."""
    triple = {'arm64-v8a': 'aarch64-linux-android', 'x86_64': 'x86_64-linux-android'}[abi]
    prebuilt = sdk / 'ndk' / LOCK['ndk']['version'] / 'toolchains' / 'llvm' / 'prebuilt'
    for host in sorted(prebuilt.glob('*')):
        clang = host / 'bin' / ('%s%d-clang' % (triple, LOCK['android']['min_sdk']))
        if clang.is_file():
            return clang
    return None


def go_tests_on_device(sdk, adb, serial, abi, out_dir):
    goarch = {'arm64-v8a': 'arm64', 'x86_64': 'amd64'}[abi]
    clang = ndk_clang(sdk, abi)
    if clang is None:
        record('emulator.go_binding', False, 'NDK %s not installed; the binding tests were not cross-compiled' % LOCK['ndk']['version'], status='BLOCKED')
        return
    binary = out_dir / ('mobile-%s.test' % goarch)
    build = subprocess.run(['go', 'test', '-c', '-o', str(binary), './mobile'], cwd=CORE, capture_output=True, text=True,
                           env=go_env(GOOS='android', GOARCH=goarch, CGO_ENABLED='1', CC=str(clang)))
    if build.returncode != 0:
        record('emulator.go_binding', False, 'cross-compiling the binding tests failed: ' + build.stderr[-400:])
        return
    adb_run(adb, serial, 'push', str(binary), '/data/local/tmp/asterlink-mobile.test', timeout=300)
    pattern = 'TestLoginCarriesThisBuildsPlatform|TestBeforeStartOnlyBuildIdentityAnswers|TestStartedCoreRunsTheManagedSession|TestStartRejectsBadInput|TestCloseInterruptsTheCommandInFlight'
    output = adb_run(adb, serial, 'shell', 'cd /data/local/tmp && TMPDIR=/data/local/tmp ./asterlink-mobile.test -test.count=1 -test.v -test.run "%s"' % pattern,
                     check=False, timeout=300)
    adb_run(adb, serial, 'shell', 'rm', '-f', '/data/local/tmp/asterlink-mobile.test', check=False)
    passed = re.findall(r'^--- PASS: (\S+)', output, re.M)
    failed = re.findall(r'^--- FAIL: (\S+)', output, re.M)
    record('emulator.go_binding', len(passed) == 5 and not failed and '\nPASS' in output,
           '%d binding tests passed on android/%s, including platform=android in the login body%s' % (len(passed), goarch, ', failed %s' % failed if failed else ''))


class Emulator:
    """A booted (or reused) AVD with the Debug APK's Core record checked against its ABI."""

    def __init__(self, sdk, adb, serial, process, abi, name, core_record, out_dir, facts):
        self.sdk, self.adb, self.serial, self.process = sdk, adb, serial, process
        self.abi, self.name, self.core_record, self.out_dir, self.facts = abi, name, core_record, out_dir, facts

    def close(self, keep):
        if self.process is not None and not keep:
            subprocess.run([str(self.adb), '-s', self.serial, 'emu', 'kill'], capture_output=True)
            try:
                self.process.wait(timeout=60)
            except subprocess.TimeoutExpired:
                self.process.kill()


def open_emulator(args, prefix, default_out):
    """Prerequisites, AVD and boot; None (with a BLOCKED record) when something is missing."""
    sdk = sdk_root()
    if sdk is None:
        record(prefix + '.sdk', False, 'ANDROID_HOME not set', status='BLOCKED')
        return None
    adb = tool(sdk, 'platform-tools', 'adb')
    abi = args.abi or host_abi()
    image = 'system-images;android-%s;google_apis;%s' % (args.api, abi)
    image_dir = tool(sdk, 'system-images', 'android-%s' % args.api, 'google_apis', abi)
    missing = [what for what, present in (('emulator', tool(sdk, 'emulator', 'emulator')), ('adb', adb),
                                          ('avdmanager', tool(sdk, 'cmdline-tools', 'latest', 'bin', 'avdmanager')),
                                          (image, image_dir)) if not present]
    if missing:
        record(prefix + '.prerequisites', False, 'not installed: %s' % ', '.join(missing), status='BLOCKED')
        return None
    apk = ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'debug' / 'app-debug.apk'
    core = ANDROID / 'build' / 'core' / 'core-build.json'
    if not apk.is_file() or not core.is_file():
        record(prefix + '.apk', False, 'build first: python3 scripts/build-native-android.py --target apk', status='BLOCKED')
        return None
    core_record = json.loads(core.read_text())
    if abi not in core_record['abis']:
        record(prefix + '.abi', False, 'the Core AAR has %s, the emulator needs %s (--abi %s)' % (sorted(core_record['abis']), abi, abi), status='BLOCKED')
        return None
    name = args.avd or 'asterlink-api%s' % args.api
    # avdmanager and the emulator must agree on where AVDs live; on the CI runner avdmanager
    # follows XDG_CONFIG_HOME while the emulator looks in ~/.android/avd.
    avd_home = Path(os.environ.get('ANDROID_AVD_HOME') or Path.home() / '.android' / 'avd')
    avd_home.mkdir(parents=True, exist_ok=True)
    os.environ['ANDROID_AVD_HOME'] = str(avd_home)
    ensure_avd(sdk, name, image)
    out_dir = Path(args.out or ROOT / 'artifacts' / 'native-android' / default_out)
    out_dir.mkdir(parents=True, exist_ok=True)
    serial, process = boot(sdk, adb, name, args.window, out_dir / 'emulator.log')
    facts = {'api': adb_run(adb, serial, 'shell', 'getprop', 'ro.build.version.sdk').strip(),
             'abi': adb_run(adb, serial, 'shell', 'getprop', 'ro.product.cpu.abi').strip(),
             'page_size': adb_run(adb, serial, 'shell', 'getconf', 'PAGE_SIZE').strip(),
             'avd': name}
    record(prefix + '.device', facts['abi'] == abi, 'API %(api)s, %(abi)s, page size %(page_size)s, AVD %(avd)s' % facts)
    return Emulator(sdk, adb, serial, process, abi, name, core_record, out_dir, facts)


def run_emulator_suite(args):
    emu = open_emulator(args, 'emulator', 'emulator')
    if emu is None:
        return
    sdk, adb, serial, abi, core_record, out_dir, facts = emu.sdk, emu.adb, emu.serial, emu.abi, emu.core_record, emu.out_dir, emu.facts
    apk = ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'debug' / 'app-debug.apk'
    try:
        # Instrumented test with the real AAR (builds and installs both APKs).
        connected_results = ANDROID / 'app' / 'build' / 'outputs' / 'androidTest-results' / 'connected'
        shutil.rmtree(connected_results, ignore_errors=True)
        # AccountAcceptanceTest and BillingAcceptanceTest (@SuiteDriver) are the account and billing
        # suites' drivers (run by class with their worlds); the connected-test reporter would count
        # their "no world" assumption as a failure.
        connected = gradle([':app:connectedDebugAndroidTest', '-Pandroid.testInstrumentationRunnerArguments.notAnnotation=com.asterlink.vpn.SuiteDriver'],
                           capture_output=True, text=True, env=dict(os.environ, ANDROID_SERIAL=serial))
        tests, failures = junit_counts(connected_results)
        record('emulator.core_binding', connected.returncode == 0 and tests > 0 and failures == 0,
               'instrumented tests on the device: %d (CoreBindingTest ipc-v1 + start/stop, SecurityStoreTest Keystore)' % tests
               + ('' if tests > 0 else '; Gradle output: ' + connected.stdout[-800:]) if connected.returncode == 0 else gradle_failure(connected))

        # The app itself: launch, wait for the Core, read the screen.
        adb_run(adb, serial, 'install', '-r', str(apk), timeout=300)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', 'com.asterlink.vpn')
        adb_run(adb, serial, 'shell', 'am', 'start', '-W', '-n', 'com.asterlink.vpn/.MainActivity')
        screen = ''
        stalled = []
        for _ in range(20):
            time.sleep(1.5)
            screen = ui_text(adb, serial)
            foreign = dismiss_foreign_anr(adb, serial, screen)
            if foreign:
                stalled.append(foreign)
                continue
            if re.search(r'Sign in to AsterLink|登录 AsterLink', screen):
                break
        time.sleep(4)
        screen = ui_text(adb, serial)
        # No engine identity is on screen any more (D8): the Core is up when the sign-in form shows without a start failure.
        shown = bool(re.search(r'Sign in to AsterLink|登录 AsterLink', screen)) and not re.search(r'could not start|应用启动失败', screen)
        record('emulator.app_shows_core', shown, 'the launched app shows the sign-in screen with the Core running (no start failure)'
               + ('; dismissed the emulator\'s own ANR dialog for: %s' % ', '.join(sorted(set(stalled))) if stalled else ''))
        with open(out_dir / ('foundation-api%s-%s.png' % (facts['api'], abi)), 'wb') as png:
            png.write(subprocess.run([str(adb), '-s', serial, 'exec-out', 'screencap', '-p'], capture_output=True, check=True).stdout)
        pid = adb_run(adb, serial, 'shell', 'pidof', 'com.asterlink.vpn', check=False).strip()
        record('emulator.process_alive', bool(pid), 'app process %s is running after the Core started' % (pid or '(none)'))

        if shutil.which('go'):
            with tempfile.TemporaryDirectory() as scratch:
                go_tests_on_device(sdk, adb, serial, abi, Path(scratch))
        else:
            record('emulator.go_binding', False, 'go not found', status='BLOCKED')
        (out_dir / 'device.json').write_text(json.dumps(facts, indent=2) + '\n')
    finally:
        emu.close(args.keep_emulator)


# ---------------------------------------------------------------- tun

PACKAGE = 'com.asterlink.vpn'
RUNNER = 'com.asterlink.vpn.test/androidx.test.runner.AndroidJUnitRunner'
TUN_CLASS = 'com.asterlink.vpn.TunAcceptanceTest'


# Everything the suite runs on the host listens on its loopback only. The emulator reaches
# the host's loopback at 10.0.2.2 without any VPN: that is how the app's protected socket
# reaches the test node, and the sink's "direct" address, which answers anyone who gets there
# without the node (a reply there through the tunnel would be traffic sent DIRECT). The app
# reaches the sink through the node only by name: the node resolves TARGET_NAME to its own
# loopback (publicly the name leads to IANA). No LAN or company-VPN address is involved.
LOOPBACK, EMULATOR_HOST, TARGET_NAME = '127.0.0.1', '10.0.2.2', 'example.com'


def write_private(adb, serial, name, data: bytes):
    """Into the debug app's private files through run-as and stdin: never argv, never logcat."""
    process = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s sh -c "mkdir -p files/tuntest && cat > files/tuntest/%s"' % (PACKAGE, name)],
                             input=data, capture_output=True, timeout=60)
    if process.returncode != 0:
        raise RuntimeError('writing %s into the app failed: %s' % (name, process.stderr.decode(errors='replace')[-300:]))


def read_private(adb, serial, name):
    return subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s cat files/tuntest/%s' % (PACKAGE, name)],
                          capture_output=True, text=True, timeout=60).stdout


def vpn_up(adb, serial):
    """Whether the device has any VPN network (the emulator runs no other VPN app). Only network
    agents count: network requests (SystemUI listens for VPNs) mention the transport too."""
    dump = adb_run(adb, serial, 'shell', 'dumpsys', 'connectivity', check=False, timeout=60)
    return bool(re.search(r'^\s*NetworkAgentInfo\{.*\bni\{VPN CONNECTED', dump, re.M))


def real_nodes_from_env():
    """The user's own nodes for T02-real, from the environment only (never argv or a tracked file)."""
    nodes = []
    if os.environ.get('ASTERLINK_TUN_PROXY_JSON'):
        nodes.append(json.loads(os.environ['ASTERLINK_TUN_PROXY_JSON']))
    if os.environ.get('ASTERLINK_TUN_REAL_NODES'):
        nodes.extend(json.loads(os.environ['ASTERLINK_TUN_REAL_NODES']))
    if not all(isinstance(n, dict) and n.get('type') and n.get('server') for n in nodes):
        raise RuntimeError('ASTERLINK_TUN_PROXY_JSON / ASTERLINK_TUN_REAL_NODES must be proxy objects with type and server')
    return nodes


def urlopen_text(url):
    import urllib.request
    with urllib.request.urlopen(url, timeout=10) as response:
        return response.read().decode()


def instrument_command(adb, serial, method):
    return [str(adb), '-s', serial, 'shell', 'am', 'instrument', '-w', '-r', '-e', 'class', '%s#%s' % (TUN_CLASS, method), RUNNER]


def start_helpers(args, scratch, helpers):
    """The controlled node (pinned Mihomo as a server) and the traffic sink, on this host's loopback."""
    for name in ('asterlink-testnode', 'asterlink-trafficsink'):
        subprocess.run(['go', 'build', '-o', os.path.join(scratch, name), './cmd/' + name], cwd=CORE, env=go_env(), check=True, capture_output=True)
    token = os.urandom(16).hex()
    sink_port, udp_port = free_port(), free_port()
    helpers.append(subprocess.Popen([os.path.join(scratch, 'asterlink-trafficsink'), '--listen', '%s:%d' % (LOOPBACK, sink_port), '--udp-listen', '%s:%d' % (LOOPBACK, udp_port)],
                                env=dict(os.environ, ASTERLINK_SINK_TOKEN=token), stdout=open(os.path.join(scratch, 'sink.log'), 'wb'), stderr=subprocess.STDOUT))
    ready_file = os.path.join(scratch, 'node.json')
    helpers.append(subprocess.Popen([os.path.join(scratch, 'asterlink-testnode'), '--listen', LOOPBACK, '--status-listen', LOOPBACK + ':0',
                                     '--ready-file', ready_file, '--reality-dest', '' if args.no_reality else args.reality_dest, '--max-seconds', '3600'],
                                    stdout=open(os.path.join(scratch, 'node.log'), 'wb'), stderr=subprocess.STDOUT))
    for _ in range(150):
        if os.path.exists(ready_file) and os.path.getsize(ready_file) > 0:
            break
        time.sleep(0.1)
    else:
        raise RuntimeError('the test node did not start: ' + Path(scratch, 'node.log').read_text(errors='replace')[-600:])
    time.sleep(0.2)
    node = json.loads(Path(ready_file).read_text())
    sink = {'name': TARGET_NAME, 'direct_host': EMULATOR_HOST, 'port': str(sink_port), 'udp_port': str(udp_port), 'token': token}
    return node, sink


def tun_config(args, node, sink, real):
    gateway = EMULATOR_HOST
    ss = {'name': 'ss', 'type': 'ss', 'server': gateway, 'port': node['ss_port'], 'cipher': node['ss_cipher'], 'password': node['ss_password'], 'udp': True}
    nodes = {
        'ss': ss,
        'vless': {'name': 'vless', 'type': 'vless', 'server': gateway, 'port': node['vless_port'], 'uuid': node['vless_uuid'], 'udp': True, 'tls': False},
        'ss_bad': dict(ss, password=os.urandom(16).hex()),
        'ss_noudp': dict(ss, udp=False),
    }
    if node.get('reality_port'):
        nodes['reality'] = {'name': 'reality', 'type': 'vless', 'server': gateway, 'port': node['reality_port'], 'uuid': node['vless_uuid'],
                            'udp': True, 'tls': True, 'servername': node['reality_server_name'], 'client-fingerprint': 'chrome',
                            'reality-opts': {'public-key': node['reality_public_key'], 'short-id': node['reality_short_id']}}
    api = args.api_host or '%s:443' % native_env.load_env_file(ANDROID / '.env.example')['API_BASE_URL'].split('/')[2]
    return {'nodes': nodes, 'real_nodes': real, 'cycles': args.cycles, 'download_bytes': 4 << 20, 'api_host': api, 'sink': sink,
            'node_stats': {'port': node['status_addr'].rsplit(':', 1)[1], 'token': node['status_token']}}


def kill_case(adb, serial, config, out_dir):
    """T09: kill the process while connected; the system must remove the VPN with it."""
    adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE)
    write_private(adb, serial, 'config.json', json.dumps(dict(config, hold=True)).encode())
    holder = subprocess.Popen(instrument_command(adb, serial, 'holdForKill'), stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    ready, deadline = None, time.time() + 120
    try:
        while time.time() < deadline:
            line = holder.stdout.readline()
            if not line:
                break
            match = re.search(r'asterlink=tun_ready=(\d)', line)
            if match:
                ready = match.group(1) == '1'
                break
        up_before = vpn_up(adb, serial)
        if not up_before:
            agents = [l for l in adb_run(adb, serial, 'shell', 'dumpsys', 'connectivity', check=False).splitlines() if 'NetworkAgentInfo{' in l]
            (out_dir / 'kill-dumpsys.txt').write_text('\n'.join(agents) + '\n')
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE)
        gone_after = None
        for i in range(20):
            if not vpn_up(adb, serial):
                gone_after = i * 0.5
                break
            time.sleep(0.5)
    finally:
        holder.kill()
    record('tun.T09-kill', bool(ready) and up_before and gone_after is not None,
           'connected=%s, VPN present before the kill=%s, gone %s after am force-stop'
           % (ready, up_before, '%.1f s' % gone_after if gone_after is not None else 'NOT within 10 s'))


def run_tun_suite(args):
    if not args.allow_network_changes:
        record('tun.consent', False, 'pass --allow-network-changes: the suite creates a VPN inside the emulator (the host network is not changed)', status='BLOCKED')
        return
    if not shutil.which('go'):
        record('tun.go', False, 'go not found', status='BLOCKED')
        return
    abi = args.abi or host_abi()
    if not args.skip_build:
        # Always rebind: the hooks AAR must be the current Go sources, never a leftover.
        build = subprocess.run([sys.executable, str(ROOT / 'scripts' / 'build-native-android.py'), '--target', 'apk', '--tun-test-hooks', '--rebuild-core', '--abi', abi],
                               capture_output=True, text=True)
        if build.returncode != 0:
            record('tun.build', False, 'hooks AAR + debug/test APKs: ' + (build.stdout + build.stderr)[-800:])
            return
    core = json.loads((ANDROID / 'build' / 'core' / 'core-build.json').read_text())
    test_apk = ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'androidTest' / 'debug' / 'app-debug-androidTest.apk'
    if not core.get('tuntest_hooks') or not test_apk.is_file():
        record('tun.build', False, 'the Core AAR lacks the acceptance hooks or the test APK is missing (run without --skip-build)', status='BLOCKED')
        return
    import prepare_mihomo
    if core.get('mihomo_patch') != prepare_mihomo.prepare(check=True):
        record('tun.build', False, 'Core AAR lacks the current Mihomo patch identity; run without --skip-build')
        return
    record('tun.build', True, 'Core AAR with the debug-only acceptance hooks (sha256 %s) and the test APK' % core['aar_sha256'][:16])
    real = real_nodes_from_env()

    emu = open_emulator(args, 'tun', 'tun')
    if emu is None:
        return
    adb, serial = emu.adb, emu.serial
    scratch = tempfile.mkdtemp(prefix='asterlink-tun-')
    helpers = []
    try:
        node, sink = start_helpers(args, scratch, helpers)
        record('tun.helpers', True, 'test node and sink on the host loopback only (emulator: %s; the sink by name only through the node); both stop with the suite' % EMULATOR_HOST)
        config = tun_config(args, node, sink, real)

        adb_run(adb, serial, 'install', '-r', '-t', str(ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'debug' / 'app-debug.apk'), timeout=300)
        adb_run(adb, serial, 'install', '-r', '-t', str(test_apk), timeout=300)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE)
        # The consent dialog cannot be automated; appops records exactly what the dialog grants.
        adb_run(adb, serial, 'shell', 'appops', 'set', PACKAGE, 'ACTIVATE_VPN', 'allow')
        if vpn_up(adb, serial):
            record('tun.clean_start', False, 'a VPN is already up in the emulator', status='BLOCKED')
            return

        write_private(adb, serial, 'config.json', json.dumps(config).encode())
        run = subprocess.run(instrument_command(adb, serial, 'acceptance'), capture_output=True, text=True, timeout=args.max_seconds)
        (emu.out_dir / 'instrument.txt').write_text(re.sub(r't=[0-9a-f]{32}', 't=<token>', run.stdout + run.stderr))
        try:
            cases = json.loads(read_private(adb, serial, 'report.json'))['cases']
        except (ValueError, KeyError):
            record('tun.report', False, 'no report from the device: ' + (run.stdout + run.stderr)[-1200:])
            return
        for case in cases:
            record('tun.' + case['case_id'], case['status'] == 'PASS', case['observed'], status=case['status'])
        for name in ('goroutines-before.txt', 'goroutines-first.txt', 'goroutines-last.txt'):  # T-CYCLES evidence
            (emu.out_dir / name).write_text(read_private(adb, serial, name))
        kill_case(adb, serial, config, emu.out_dir)
        totals = json.loads(urlopen_text('http://%s:%s/stats?t=%s' % (LOOPBACK, config['node_stats']['port'], config['node_stats']['token'])))
        (emu.out_dir / 'tun-report.json').write_text(json.dumps({
            'device': emu.facts, 'core': {k: core.get(k) for k in ('core_version', 'mihomo_revision', 'mihomo_patch', 'aar_sha256', 'tuntest_hooks')},
            'node_totals': totals, 'cases': results}, indent=2) + '\n')
    finally:
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE, check=False)
        adb_run(adb, serial, 'shell', 'run-as %s rm -rf files/tuntest' % PACKAGE, check=False)
        adb_run(adb, serial, 'shell', 'appops', 'set', PACKAGE, 'ACTIVATE_VPN', 'default', check=False)
        for helper in helpers:
            helper.terminate()
            try:
                helper.wait(timeout=10)
            except subprocess.TimeoutExpired:
                helper.kill()
        shutil.rmtree(scratch, ignore_errors=True)
        emu.close(args.keep_emulator)


# ---------------------------------------------------------------- ui (round 3)

def run_ui_suite(args):
    if sdk_root() is None:
        record('ui.sdk', False, 'ANDROID_HOME not set', status='BLOCKED')
        return
    if not (ANDROID / 'build' / 'core' / 'asterlink-core.aar').is_file():
        record('ui.core', False, 'Core AAR missing: python3 scripts/build-native-android.py --target aar', status='BLOCKED')
        return
    shots = ANDROID / 'app' / 'build' / 'outputs' / 'roborazzi'
    shutil.rmtree(shots, ignore_errors=True)
    results_dir = ANDROID / 'app' / 'build' / 'test-results' / 'testDebugUnitTest'
    shutil.rmtree(results_dir, ignore_errors=True)
    run = gradle([':app:testDebugUnitTest', '--tests', 'com.asterlink.vpn.ui.ScreenshotTest', '--rerun'], capture_output=True, text=True)
    tests, failures = junit_counts(results_dir)
    pngs = sorted(shots.glob('*.png')) if shots.is_dir() else []
    record('ui.screenshot_tests', run.returncode == 0 and tests > 0 and failures == 0,
           '%d screenshot tests passed (each also asserts its state)' % tests if run.returncode == 0 else gradle_failure(run))
    out_dir = Path(args.out or ROOT / 'artifacts' / 'native-android' / 'ui')
    shutil.rmtree(out_dir, ignore_errors=True)
    out_dir.mkdir(parents=True)
    for png in pngs:
        shutil.copy2(png, out_dir / png.name)
    names = [p.stem for p in pngs]
    required = ('login', 'admin-no-plan', 'expired', 'quota-zero', 'no-line', 'ready', 'connecting', 'connected', 'failed',
                'lines', 'lines-no-plan', 'lines-none', 'account', 'dark', 'font200', '360dp', 'en-', 'update-required')
    missing = [r for r in required if not any(r in n for n in names)]
    record('ui.states', len(pngs) >= 20 and not missing, '%d states in %s%s' % (len(pngs), out_dir, '; missing ' + ', '.join(missing) if missing else ''))


# ---------------------------------------------------------------- account (round 3)

ACCOUNT_CLASS = 'com.asterlink.vpn.AccountAcceptanceTest'


def start_world(scratch, test='TestAndroidAccountWorld', output_env='ASTERLINK_ANDROID_WORLD', extra_env=None, port=None):
    """Private PostgreSQL (Unix socket only) + a backend world test on 127.0.0.1. Without a port the
    world binds 127.0.0.1:0 itself, first thing, and reports its address: no port is picked here and
    released for something else on the host to take before the world binds it."""
    data, sock = Path(scratch) / 'pg', Path(scratch) / 'sock'
    sock.mkdir(mode=0o700)
    env = dict(os.environ, LC_ALL='C', LANG='C')
    subprocess.run(['initdb', '-D', str(data), '-U', 'postgres', '--auth-local=trust', '--auth-host=reject', '--no-locale', '-E', 'UTF8'],
                   check=True, stdout=subprocess.DEVNULL, env=env)
    subprocess.run(['pg_ctl', '-D', str(data), '-l', str(Path(scratch) / 'postgres.log'), '-o', "-k %s -c listen_addresses=''" % sock, '-w', 'start'],
                   check=True, stdout=subprocess.DEVNULL, env=env)
    world_file = Path(scratch) / 'world.json'
    log = open(Path(scratch) / 'world.log', 'wb')
    world = subprocess.Popen(['go', 'test', '-count=1', '-timeout', '70m', '-run', '^%s$' % test, '-v', './internal/api'], cwd=ROOT / 'backend',
                             env=dict(env, NATIVE_TEST_DSN='host=%s user=postgres dbname=postgres sslmode=disable' % sock, RUN_DB_TESTS='0',
                                      ASTERLINK_ANDROID_WORLD_PORT=str(port or 0), GOFLAGS='-buildvcs=false', **{output_env: str(world_file)}, **(extra_env or {})),
                             stdout=log, stderr=subprocess.STDOUT)
    for _ in range(3000):
        if world_file.is_file() and world_file.stat().st_size > 0:
            return data, world, world_file, urlsplit(json.loads(world_file.read_text())['api_base']).port
        if world.poll() is not None:
            break
        time.sleep(0.1)
    raise RuntimeError('the backend world did not start: ' + (Path(scratch) / 'world.log').read_text(errors='replace')[-1200:])


def run_account_suite(args):
    missing = [t for t in ('go', 'initdb', 'pg_ctl') if not shutil.which(t)]
    if missing:
        record('account.tools', False, 'not installed: %s' % ', '.join(missing), status='BLOCKED')
        return
    if not args.skip_build:
        build = gradle([':app:assembleDebug', ':app:assembleDebugAndroidTest'], capture_output=True, text=True)
        if build.returncode != 0:
            record('account.build', False, gradle_failure(build))
            return
    test_apk = ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'androidTest' / 'debug' / 'app-debug-androidTest.apk'
    emu = open_emulator(args, 'account', 'account')
    if emu is None:
        return
    adb, serial = emu.adb, emu.serial
    scratch = tempfile.mkdtemp(prefix='asterlink-account-')
    pg_data, world, port = None, None, None
    try:
        pg_data, world, world_file, port = start_world(scratch)
        facts = json.loads(world_file.read_text())
        secrets = [facts['password'], 'metadata-private.example.invalid', 'never-reaches-the-ui', 'aes-256-gcm']
        record('account.world', True, 'backend TestAndroidAccountWorld on 127.0.0.1:%d (private PostgreSQL on a Unix socket, synthetic accounts)' % port)
        adb_run(adb, serial, 'install', '-r', '-t', str(ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'debug' / 'app-debug.apk'), timeout=300)
        adb_run(adb, serial, 'install', '-r', '-t', str(test_apk), timeout=300)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE)
        # The emulator's 127.0.0.1:port is the host's: the Core keeps its loopback-only HTTP rule.
        adb_run(adb, serial, 'reverse', 'tcp:%d' % port, 'tcp:%d' % port)
        adb_run(adb, serial, 'logcat', '-c', check=False)
        process = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s sh -c "rm -rf files/accounttest && mkdir -p files/accounttest && cat > files/accounttest/world.json"' % PACKAGE],
                                 input=world_file.read_bytes(), capture_output=True, timeout=60)
        if process.returncode != 0:
            raise RuntimeError('writing world.json into the app failed')
        run = subprocess.run([str(adb), '-s', serial, 'shell', 'am', 'instrument', '-w', '-r', '-e', 'class', ACCOUNT_CLASS + '#acceptance', RUNNER],
                             capture_output=True, text=True, timeout=args.max_seconds)
        (emu.out_dir / 'instrument.txt').write_text(run.stdout + run.stderr)
        report = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s cat files/accounttest/report.json' % PACKAGE], capture_output=True, text=True, timeout=60).stdout
        try:
            cases = json.loads(report)['cases']
        except (ValueError, KeyError):
            record('account.report', False, 'no report from the device: ' + (run.stdout + run.stderr)[-1200:])
            return
        for case in cases:
            record('account.' + case['case_id'], case['status'] == 'PASS', case['observed'], status=case['status'])
        logcat = adb_run(adb, serial, 'logcat', '-d', check=False, timeout=120)
        leaked = [i for i, secret in enumerate(secrets) if secret in logcat]
        record('account.logcat_clean', not leaked, 'logcat (%d lines) holds no password, node address, node secret or cipher' % logcat.count('\n')
               if not leaked else 'logcat contains secret #%s' % leaked)
        (emu.out_dir / 'account-report.json').write_text(json.dumps({'device': emu.facts, 'cases': results}, indent=2) + '\n')
    finally:
        if port:
            try:
                urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:%d/__world/stop' % port, data=b'{}', method='POST'), timeout=10)
            except OSError:
                pass
            adb_run(adb, serial, 'reverse', '--remove', 'tcp:%d' % port, check=False)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE, check=False)
        adb_run(adb, serial, 'shell', 'run-as %s rm -rf files/accounttest' % PACKAGE, check=False)
        if world:
            try:
                world.wait(timeout=60)
            except subprocess.TimeoutExpired:
                world.kill()
        if pg_data:
            subprocess.run(['pg_ctl', '-D', str(pg_data), '-m', 'fast', '-w', 'stop'], stdout=subprocess.DEVNULL, check=False)
        shutil.rmtree(scratch, ignore_errors=True)
        emu.close(args.keep_emulator)


# ---------------------------------------------------------------- billing (round 4)

BILLING_CLASS = 'com.asterlink.vpn.BillingAcceptanceTest'


def ensure_plain_build(abi):
    """The normal debug APK and its test APK; the AAR must be the production binding (no hooks)."""
    core_record = ANDROID / 'build' / 'core' / 'core-build.json'
    rebuild = not core_record.is_file() or json.loads(core_record.read_text()).get('tuntest_hooks')
    command = [sys.executable, str(ROOT / 'scripts' / 'build-native-android.py'), '--target', 'apk', '--abi', abi] + (['--rebuild-core'] if rebuild else [])
    build = subprocess.run(command, capture_output=True, text=True)
    if build.returncode != 0:
        return 'debug APK: ' + (build.stdout + build.stderr)[-800:]
    test = gradle([':app:assembleDebugAndroidTest'], capture_output=True, text=True)
    if test.returncode != 0:
        return 'test APK: ' + gradle_failure(test)
    if json.loads(core_record.read_text()).get('tuntest_hooks'):
        return 'the Core AAR carries the TUN acceptance hooks; the billing suite runs the production binding only'
    return None


def run_billing_suite(args):
    if not args.allow_network_changes:
        record('billing.consent', False, 'pass --allow-network-changes: the suite creates a VPN inside the emulator (the host network is not changed)', status='BLOCKED')
        return
    missing = [t for t in ('go', 'initdb', 'pg_ctl') if not shutil.which(t)]
    if missing:
        record('billing.tools', False, 'not installed: %s' % ', '.join(missing), status='BLOCKED')
        return
    abi = args.abi or host_abi()
    if not args.skip_build:
        problem = ensure_plain_build(abi)
        if problem:
            record('billing.build', False, problem)
            return
    core = json.loads((ANDROID / 'build' / 'core' / 'core-build.json').read_text())
    record('billing.build', not core.get('tuntest_hooks'), 'production Core AAR (sha256 %s, no acceptance hooks) and the debug + test APKs' % core['aar_sha256'][:16])
    test_apk = ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'androidTest' / 'debug' / 'app-debug-androidTest.apk'

    emu = open_emulator(args, 'billing', 'billing')
    if emu is None:
        return
    adb, serial = emu.adb, emu.serial
    scratch = tempfile.mkdtemp(prefix='asterlink-billing-')
    helpers, pg_data, world, port = [], None, None, None
    try:
        # No REALITY inbound: these lines are Shadowsocks and plain VLESS (round 2 covers REALITY).
        node, sink = start_helpers(argparse.Namespace(**dict(vars(args), no_reality=True)), scratch, helpers)
        extra = {'ASTERLINK_ANDROID_TESTNODE': os.path.join(scratch, 'node.json'), 'ASTERLINK_ANDROID_NODE_HOST': EMULATOR_HOST,
                 'ASTERLINK_ANDROID_SINK': 'http://%s:%s' % (LOOPBACK, sink['port']), 'ASTERLINK_ANDROID_SINK_TOKEN': sink['token']}
        if not args.no_node_agent:
            agent = os.path.join(scratch, 'asterlink-nodeagent')
            subprocess.run(['go', 'build', '-o', agent, './cmd/asterlink-nodeagent'], cwd=ROOT / 'native' / 'nodeagent', env=go_env(CGO_ENABLED='0'), check=True, capture_output=True)
            extra['ASTERLINK_NODEAGENT_BIN'] = agent
        pg_data, world, world_file, port = start_world(scratch, 'TestAndroidBillingWorld', 'ASTERLINK_ANDROID_BILLING_WORLD', extra)
        facts = json.loads(world_file.read_text())
        record('billing.world', True, 'TestAndroidBillingWorld on 127.0.0.1:%d (private PostgreSQL); test node + sink on the host loopback; node agent: %s%s'
               % (port, 'yes' if facts.get('node_reported') else 'no', '' if facts.get('agent_target_resolves') or not facts.get('node_reported') else ' (B04 target does not resolve here)'))
        secrets = [facts['password'], node['ss_password'], node['vless_uuid'], sink['token']]

        adb_run(adb, serial, 'install', '-r', '-t', str(ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'debug' / 'app-debug.apk'), timeout=300)
        adb_run(adb, serial, 'install', '-r', '-t', str(test_apk), timeout=300)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE)
        # The consent dialog cannot be automated; appops records exactly what the dialog grants.
        adb_run(adb, serial, 'shell', 'appops', 'set', PACKAGE, 'ACTIVATE_VPN', 'allow')
        adb_run(adb, serial, 'shell', 'pm', 'grant', PACKAGE, 'android.permission.POST_NOTIFICATIONS', check=False)
        if vpn_up(adb, serial):
            record('billing.clean_start', False, 'a VPN is already up in the emulator', status='BLOCKED')
            return
        adb_run(adb, serial, 'reverse', 'tcp:%d' % port, 'tcp:%d' % port)
        adb_run(adb, serial, 'logcat', '-c', check=False)
        config = {'sink': {'name': TARGET_NAME, 'port': sink['port'], 'token': sink['token']}, 'upload_mib': args.upload_mib, 'download_mib': args.download_mib,
                  'background_bytes': 2 << 20}
        for name, data in (('world.json', world_file.read_bytes()), ('config.json', json.dumps(config).encode())):
            process = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s sh -c "mkdir -p files/billingtest && cat > files/billingtest/%s"' % (PACKAGE, name)],
                                     input=data, capture_output=True, timeout=60)
            if process.returncode != 0:
                raise RuntimeError('writing %s into the app failed' % name)
        started = time.time()
        run = subprocess.run([str(adb), '-s', serial, 'shell', 'am', 'instrument', '-w', '-r', '-e', 'class', BILLING_CLASS + '#acceptance', RUNNER],
                             capture_output=True, text=True, timeout=args.max_seconds)
        (emu.out_dir / 'instrument.txt').write_text(run.stdout + run.stderr)
        report = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s cat files/billingtest/report.json' % PACKAGE], capture_output=True, text=True, timeout=60).stdout
        try:
            cases = json.loads(report)['cases']
        except (ValueError, KeyError):
            record('billing.report', False, 'no report from the device: ' + (run.stdout + run.stderr)[-1200:])
            return
        for case in cases:
            record('billing.' + case['case_id'], case['status'] == 'PASS', case['observed'], status=case['status'])
        record('billing.vpn_gone', not vpn_up(adb, serial), 'no VPN network left in the emulator after the run (%.0f s)' % (time.time() - started))
        logcat = adb_run(adb, serial, 'logcat', '-d', check=False, timeout=120)
        leaked = [i for i, secret in enumerate(secrets) if secret and secret in logcat]
        record('billing.logcat_clean', not leaked, 'logcat (%d lines) holds no password, node secret or sink token' % logcat.count('\n')
               if not leaked else 'logcat contains secret #%s' % leaked)
        if not leaked and any(case['status'] != 'PASS' for case in cases):
            # Evidence for a failing case: the app's own lines only, and only when the leak check passed.
            lines = [line for line in logcat.splitlines() if any(tag in line for tag in ('asterlink', 'AsterLink', 'Core', 'mihomo', 'GoLog'))]
            (emu.out_dir / 'billing-logcat.txt').write_text('\n'.join(lines[-4000:]) + '\n')
        (emu.out_dir / 'billing-report.json').write_text(json.dumps({
            'device': emu.facts, 'core': {k: core.get(k) for k in ('core_version', 'mihomo_revision', 'mihomo_patch', 'aar_sha256', 'tuntest_hooks')},
            'upload_mib': args.upload_mib, 'download_mib': args.download_mib, 'cases': results}, indent=2) + '\n')
    finally:
        if port:
            try:
                urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:%d/__world/stop' % port, data=b'{}', method='POST'), timeout=10)
            except OSError:
                pass
            adb_run(adb, serial, 'reverse', '--remove', 'tcp:%d' % port, check=False)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE, check=False)
        adb_run(adb, serial, 'shell', 'run-as %s rm -rf files/billingtest' % PACKAGE, check=False)
        adb_run(adb, serial, 'shell', 'appops', 'set', PACKAGE, 'ACTIVATE_VPN', 'default', check=False)
        if world:
            try:
                world.wait(timeout=60)
            except subprocess.TimeoutExpired:
                world.kill()
            log = Path(scratch) / 'world.log'
            if log.is_file():
                # Synthetic accounts only; the world prints no password or node secret.
                (emu.out_dir / 'world.log').write_text(log.read_text(errors='replace')[-200_000:])
        for helper in helpers:
            helper.terminate()
            try:
                helper.wait(timeout=10)
            except subprocess.TimeoutExpired:
                helper.kill()
        if pg_data:
            subprocess.run(['pg_ctl', '-D', str(pg_data), '-m', 'fast', '-w', 'stop'], stdout=subprocess.DEVNULL, check=False)
        shutil.rmtree(scratch, ignore_errors=True)
        emu.close(args.keep_emulator)


# ---------------------------------------------------------------- lifecycle (round 5)

LIFECYCLE_CLASS = 'com.asterlink.vpn.LifecycleAcceptanceTest'


def write_lifecycle(adb, serial, name, data: bytes):
    process = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s sh -c "mkdir -p files/lifecycletest && cat > files/lifecycletest/%s"' % (PACKAGE, name)],
                             input=data, capture_output=True, timeout=60)
    if process.returncode != 0:
        raise RuntimeError('writing %s into the app failed' % name)


def app_pid(adb, serial):
    return adb_run(adb, serial, 'shell', 'pidof', PACKAGE, check=False).strip()


def lifecycle_host_cases(emu, world_file, config):
    """L05 (am kill / force-stop) and L06 (reboot): what kills the process, driven from the host."""
    adb, serial = emu.adb, emu.serial
    adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE)
    write_lifecycle(adb, serial, 'world.json', world_file.read_bytes())
    write_lifecycle(adb, serial, 'config.json', json.dumps(config).encode())
    write_lifecycle(adb, serial, 'hold', b'1')
    holder = subprocess.Popen([str(adb), '-s', serial, 'shell', 'am', 'instrument', '-w', '-r', '-e', 'class', LIFECYCLE_CLASS + '#holdConnected', RUNNER],
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    ready, deadline = None, time.time() + 120
    try:
        while time.time() < deadline:
            line = holder.stdout.readline()
            if not line:
                break
            match = re.search(r'asterlink=life_ready=(\d)', line)
            if match:
                ready = match.group(1) == '1'
                break
        pid = app_pid(adb, serial)
        # A process with a foreground service is "perceptible": `am kill` (what the system does
        # to cached background apps) must leave it and its VPN alone.
        adb_run(adb, serial, 'shell', 'am', 'kill', PACKAGE, check=False)
        time.sleep(3)
        survived = bool(pid) and app_pid(adb, serial) == pid and vpn_up(adb, serial)
        record('lifecycle.L05-am-kill', bool(ready) and survived,
               'connected=%s; after `am kill` the process %s and its VPN %s' % (ready, 'is the same' if app_pid(adb, serial) == pid else 'changed', 'remains' if vpn_up(adb, serial) else 'is gone'))
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE)
        gone_after = None
        for i in range(20):
            if not vpn_up(adb, serial):
                gone_after = i * 0.5
                break
            time.sleep(0.5)
        services = adb_run(adb, serial, 'shell', 'dumpsys', 'activity', 'services', PACKAGE, check=False)
        record('lifecycle.L05-force-stop', bool(ready) and gone_after is not None and 'AsterVpnService' not in services and not app_pid(adb, serial),
               'after `am force-stop`: VPN gone %s, no process, no service record; nothing restarts it (START_NOT_STICKY, always-on unsupported)'
               % ('%.1f s' % gone_after if gone_after is not None else 'NOT within 10 s'))
    finally:
        holder.kill()
        adb_run(adb, serial, 'shell', 'run-as %s rm -rf files/lifecycletest' % PACKAGE, check=False)


def reboot_case(emu):
    """L06: after a reboot nothing connects by itself: no process, no service, no VPN."""
    adb, serial = emu.adb, emu.serial
    adb_run(adb, serial, 'reboot', check=False, timeout=60)
    time.sleep(5)
    adb_run(adb, serial, 'wait-for-device', timeout=300)
    deadline = time.time() + 300
    while time.time() < deadline and adb_run(adb, serial, 'shell', 'getprop', 'sys.boot_completed', check=False).strip() != '1':
        time.sleep(2)
    booted = adb_run(adb, serial, 'shell', 'getprop', 'sys.boot_completed', check=False).strip() == '1'
    time.sleep(20)  # BOOT_COMPLETED receivers and always-on VPN would have run by now
    pid = app_pid(adb, serial)
    services = adb_run(adb, serial, 'shell', 'dumpsys', 'activity', 'services', PACKAGE, check=False)
    always_on = adb_run(adb, serial, 'shell', 'settings', 'get', 'secure', 'always_on_vpn_app', check=False).strip()
    record('lifecycle.L06-reboot', booted and not vpn_up(adb, serial) and 'AsterVpnService' not in services and always_on in ('', 'null'),
           'rebooted (boot completed=%s); 20 s later: VPN %s, app process %s, service record %s, always-on VPN app %r'
           % (booted, 'up' if vpn_up(adb, serial) else 'absent', pid or 'not running', 'present' if 'AsterVpnService' in services else 'absent', always_on))


def run_lifecycle_suite(args):
    if not args.allow_network_changes:
        record('lifecycle.consent', False, 'pass --allow-network-changes: the suite creates VPNs and toggles Wi-Fi, airplane mode and Doze inside the emulator (the host network is not changed)', status='BLOCKED')
        return
    missing = [t for t in ('go', 'initdb', 'pg_ctl') if not shutil.which(t)]
    if missing:
        record('lifecycle.tools', False, 'not installed: %s' % ', '.join(missing), status='BLOCKED')
        return
    abi = args.abi or host_abi()
    if not args.skip_build:
        problem = ensure_plain_build(abi)
        if problem:
            record('lifecycle.build', False, problem)
            return
    core = json.loads((ANDROID / 'build' / 'core' / 'core-build.json').read_text())
    record('lifecycle.build', not core.get('tuntest_hooks'), 'production Core AAR (sha256 %s, no acceptance hooks) and the debug + test APKs' % core['aar_sha256'][:16])
    test_apk = ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'androidTest' / 'debug' / 'app-debug-androidTest.apk'

    emu = open_emulator(args, 'lifecycle', 'lifecycle')
    if emu is None:
        return
    adb, serial = emu.adb, emu.serial
    scratch = tempfile.mkdtemp(prefix='asterlink-lifecycle-')
    helpers, pg_data, world, port = [], None, None, None
    try:
        node, sink = start_helpers(argparse.Namespace(**dict(vars(args), no_reality=True)), scratch, helpers)
        extra = {'ASTERLINK_ANDROID_TESTNODE': os.path.join(scratch, 'node.json'), 'ASTERLINK_ANDROID_NODE_HOST': EMULATOR_HOST,
                 'ASTERLINK_ANDROID_SINK': 'http://%s:%s' % (LOOPBACK, sink['port']), 'ASTERLINK_ANDROID_SINK_TOKEN': sink['token']}
        pg_data, world, world_file, port = start_world(scratch, 'TestAndroidBillingWorld', 'ASTERLINK_ANDROID_BILLING_WORLD', extra)
        facts = json.loads(world_file.read_text())
        record('lifecycle.world', True, 'TestAndroidBillingWorld on 127.0.0.1:%d (private PostgreSQL; report interval %s s); test node + sink on the host loopback'
               % (port, facts.get('report_interval_seconds')))
        secrets = [facts['password'], node['ss_password'], node['vless_uuid'], sink['token']]

        adb_run(adb, serial, 'install', '-r', '-t', str(ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'debug' / 'app-debug.apk'), timeout=300)
        adb_run(adb, serial, 'install', '-r', '-t', str(test_apk), timeout=300)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE)
        adb_run(adb, serial, 'shell', 'appops', 'set', PACKAGE, 'ACTIVATE_VPN', 'allow')
        # N01: the run starts with notifications denied (revoking kills the app, so it happens now).
        adb_run(adb, serial, 'shell', 'pm', 'revoke', PACKAGE, 'android.permission.POST_NOTIFICATIONS', check=False)
        adb_run(adb, serial, 'shell', 'svc', 'wifi', 'enable', check=False)
        adb_run(adb, serial, 'shell', 'cmd', 'connectivity', 'airplane-mode', 'disable', check=False)
        if vpn_up(adb, serial):
            record('lifecycle.clean_start', False, 'a VPN is already up in the emulator', status='BLOCKED')
            return
        adb_run(adb, serial, 'reverse', 'tcp:%d' % port, 'tcp:%d' % port)
        adb_run(adb, serial, 'logcat', '-c', check=False)
        config = {'sink': {'name': TARGET_NAME, 'port': sink['port'], 'token': sink['token']}, 'cycles': args.cycles,
                  'screen_off_seconds': args.screen_off_seconds, 'soak_seconds': args.soak_seconds}
        adb_run(adb, serial, 'shell', 'run-as %s rm -rf files/lifecycletest' % PACKAGE, check=False)
        write_lifecycle(adb, serial, 'world.json', world_file.read_bytes())
        write_lifecycle(adb, serial, 'config.json', json.dumps(config).encode())
        started = time.time()
        run = subprocess.run([str(adb), '-s', serial, 'shell', 'am', 'instrument', '-w', '-r', '-e', 'class', LIFECYCLE_CLASS + '#acceptance', RUNNER],
                             capture_output=True, text=True, timeout=args.max_seconds)
        (emu.out_dir / 'instrument.txt').write_text(run.stdout + run.stderr)
        report = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s cat files/lifecycletest/report.json' % PACKAGE], capture_output=True, text=True, timeout=60).stdout
        samples = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s cat files/lifecycletest/samples.json' % PACKAGE], capture_output=True, text=True, timeout=60).stdout
        if samples.strip().startswith('['):
            (emu.out_dir / 'soak-samples.json').write_text(samples)
        timeline = subprocess.run([str(adb), '-s', serial, 'shell', 'run-as %s cat files/lifecycletest/timeline.json' % PACKAGE], capture_output=True, text=True, timeout=60).stdout
        if timeline.strip().startswith('['):
            (emu.out_dir / 'timeline.json').write_text(timeline)
        try:
            cases = json.loads(report)['cases']
        except (ValueError, KeyError):
            record('lifecycle.report', False, 'no report from the device: ' + (run.stdout + run.stderr)[-1200:])
            return
        for case in cases:
            record('lifecycle.' + case['case_id'], case['status'] in ('PASS', 'NOT_RUN'), case['observed'], status=case['status'])
        record('lifecycle.vpn_gone', not vpn_up(adb, serial), 'no VPN network left in the emulator after the device run (%.0f s)' % (time.time() - started))
        lifecycle_host_cases(emu, world_file, config)
        logcat = adb_run(adb, serial, 'logcat', '-d', check=False, timeout=120)
        leaked = [i for i, secret in enumerate(secrets) if secret and secret in logcat]
        record('lifecycle.logcat_clean', not leaked, 'logcat (%d lines) holds no password, node secret or sink token' % logcat.count('\n')
               if not leaked else 'logcat contains secret #%s' % leaked)
        if args.no_reboot:
            record('lifecycle.L06-reboot', False, 'skipped (--no-reboot)', status='NOT_RUN')
        else:
            reboot_case(emu)
        (emu.out_dir / 'lifecycle-report.json').write_text(json.dumps({
            'device': emu.facts, 'core': {k: core.get(k) for k in ('core_version', 'mihomo_revision', 'mihomo_patch', 'aar_sha256', 'tuntest_hooks')},
            'cases': results}, indent=2) + '\n')
    finally:
        if port:
            try:
                urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:%d/__world/stop' % port, data=b'{}', method='POST'), timeout=10)
            except OSError:
                pass
            adb_run(adb, serial, 'reverse', '--remove', 'tcp:%d' % port, check=False)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', PACKAGE, check=False)
        adb_run(adb, serial, 'shell', 'run-as %s rm -rf files/lifecycletest' % PACKAGE, check=False)
        adb_run(adb, serial, 'shell', 'appops', 'set', PACKAGE, 'ACTIVATE_VPN', 'default', check=False)
        adb_run(adb, serial, 'shell', 'pm', 'grant', PACKAGE, 'android.permission.POST_NOTIFICATIONS', check=False)
        if world:
            try:
                world.wait(timeout=60)
            except subprocess.TimeoutExpired:
                world.kill()
            log = Path(scratch) / 'world.log'
            if log.is_file():
                (emu.out_dir / 'world.log').write_text(log.read_text(errors='replace')[-200_000:])
        for helper in helpers:
            helper.terminate()
            try:
                helper.wait(timeout=10)
            except subprocess.TimeoutExpired:
                helper.kill()
        if pg_data:
            subprocess.run(['pg_ctl', '-D', str(pg_data), '-m', 'fast', '-w', 'stop'], stdout=subprocess.DEVNULL, check=False)
        shutil.rmtree(scratch, ignore_errors=True)
        emu.close(args.keep_emulator)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--suite', required=True)
    parser.add_argument('--report', help='write a JSON report here')
    parser.add_argument('--avd', help='emulator suite: AVD name (default asterlink-api<API>)')
    parser.add_argument('--api', default='36', help='emulator suite: system image API level (default 36)')
    parser.add_argument('--abi', choices=('arm64-v8a', 'x86_64'), help='emulator suite: image ABI (default: the host CPU)')
    parser.add_argument('--out', help='emulator suite: evidence directory (default artifacts/native-android/emulator)')
    parser.add_argument('--window', action='store_true', help='emulator suite: show the emulator window')
    parser.add_argument('--keep-emulator', action='store_true', help='emulator suite: leave a booted emulator running')
    parser.add_argument('--allow-network-changes', action='store_true', help='tun suite: confirm a VPN may be created inside the emulator')
    parser.add_argument('--cycles', type=int, default=20, help='tun suite: start/verify/stop cycles for T-CYCLES (default 20)')
    parser.add_argument('--api-host', help='tun suite: host:port for T05 (default: the API host of apps/android/.env.example)')
    parser.add_argument('--reality-dest', default='itunes.apple.com:443', help='tun suite: TLS server the test node borrows its REALITY handshake from')
    parser.add_argument('--no-reality', action='store_true', help='tun suite: no REALITY inbound (T02-reality BLOCKED)')
    parser.add_argument('--skip-build', action='store_true', help='tun suite: use the hooks AAR and APKs already built')
    parser.add_argument('--max-seconds', type=int, default=1800, help='tun suite: upper bound for the instrumented run')
    parser.add_argument('--upload-mib', type=int, default=20, help='billing suite: controlled upload per B01 rate (default 20, handoff B01)')
    parser.add_argument('--download-mib', type=int, default=80, help='billing suite: controlled download per B01 rate (default 80)')
    parser.add_argument('--no-node-agent', action='store_true', help='billing suite: no node-reported line (B04 BLOCKED)')
    parser.add_argument('--screen-off-seconds', type=int, default=120, help='lifecycle suite: L01 screen-off time (handoff: 600)')
    parser.add_argument('--soak-seconds', type=int, default=180, help='lifecycle suite: connected soak sampled every 15 s')
    parser.add_argument('--no-reboot', action='store_true', help='lifecycle suite: skip L06 (reboot the emulator)')
    parser.add_argument('--release-dir', help='release suite: the packaged folder to audit (default artifacts/native-android/release)')
    parser.add_argument('--avds', default='api36,api36-16k,api26', help='release suite: images for the published-APK smoke (api<N>[-16k])')
    parser.add_argument('--static-only', action='store_true', help='release suite: R03 only (no emulator)')
    parser.add_argument('--device', help='release suite: a real phone serial for R04 (adb devices)')
    parser.add_argument('--expect-blocked', help='comma-separated case ids the environment cannot run (e.g. CI: no IPv6 upstream, '
                                                  'no real node); they stay BLOCKED in the report, but only they may be BLOCKED for exit 0')
    args = parser.parse_args()

    import prepare_mihomo
    prepare_mihomo.prepare()

    if args.suite == 'foundation':
        check_lock_file()
        check_env_example()
        check_platform_split()
        check_manifest()
        check_release_config()
    elif args.suite == 'unit':
        run_unit_suite()
    elif args.suite == 'emulator':
        try:
            run_emulator_suite(args)
        except (RuntimeError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
            record('emulator.run', False, str(error)[-1800:])
    elif args.suite == 'ui':
        run_ui_suite(args)
    elif args.suite == 'account':
        try:
            run_account_suite(args)
        except (subprocess.SubprocessError, OSError, RuntimeError) as error:
            record('account.run', False, str(error)[-800:])
    elif args.suite == 'tun':
        try:
            run_tun_suite(args)
        except (RuntimeError, subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError, ValueError) as error:
            record('tun.run', False, str(error)[-1800:])
    elif args.suite == 'billing':
        try:
            run_billing_suite(args)
        except (RuntimeError, subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError, ValueError) as error:
            detail = getattr(error, 'stderr', None) or getattr(error, 'output', None) or b''
            record('billing.run', False, (str(error) + ' ' + (detail.decode(errors='replace') if isinstance(detail, bytes) else str(detail)))[-1800:])
    elif args.suite == 'lifecycle':
        try:
            run_lifecycle_suite(args)
        except (RuntimeError, subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError, ValueError) as error:
            detail = getattr(error, 'stderr', None) or getattr(error, 'output', None) or b''
            record('lifecycle.run', False, (str(error) + ' ' + (detail.decode(errors='replace') if isinstance(detail, bytes) else str(detail)))[-1800:])
    elif args.suite == 'release':
        import android_release_suite
        try:
            android_release_suite.run(sys.modules[__name__], args)
        except (RuntimeError, subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError, ValueError) as error:
            detail = getattr(error, 'stderr', None) or getattr(error, 'output', None) or b''
            record('release.run', False, (str(error) + ' ' + (detail.decode(errors='replace') if isinstance(detail, bytes) else str(detail)))[-1800:])
    else:
        print('unknown suite %r (handoff_app_android.md 10, 12)' % args.suite)
        return EXIT_BLOCKED

    if args.report:
        Path(args.report).parent.mkdir(parents=True, exist_ok=True)
        Path(args.report).write_text(json.dumps(results, indent=2))
    statuses = {r['status'] for r in results}
    print('\n%d cases: %s' % (len(results), ', '.join('%s=%d' % (s, sum(r['status'] == s for r in results)) for s in sorted(statuses))))
    if 'FAIL' in statuses:
        return EXIT_FAIL
    blocked = sorted(r['case_id'] for r in results if r['status'] == 'BLOCKED')
    expected = set(filter(None, (args.expect_blocked or '').split(',')))
    if blocked and set(blocked) <= expected:
        # Still reported as BLOCKED (nothing was proven for them); the run passes only because
        # the caller declared exactly these as impossible in its environment.
        print('only the declared BLOCKED cases remain (%s): exit 0' % ', '.join(blocked))
        return EXIT_PASS
    return EXIT_BLOCKED if blocked else EXIT_PASS


if __name__ == '__main__':
    sys.exit(main())

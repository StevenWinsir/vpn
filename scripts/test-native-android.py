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
      device transfer, no cleartext, system CAs only, no VPN service or permission yet
  unit        needs Go, a JDK and the Core AAR (scripts/build-native-android.py --target aar)
    * Go host tests of the binding, IPC, host-network and managed packages (non-zero count)
    * Gradle: build-logic env-v1 contract tests and the app's JVM unit tests (non-zero count)
  emulator    needs the SDK emulator and a system image; changes nothing outside the emulator
    * boots (or reuses) an AVD, installs the Debug APK, runs the instrumented CoreBindingTest
      against the real AAR, launches the app and checks it shows the Core version and the
      Mihomo commit, and runs the Go binding tests cross-compiled for the emulator's ABI
      (the login request there must say platform=android)
    * records API level, ABI and kernel page size; a 16 KB page image is a separate AVD

Exit codes: 0 = PASS, 1 = FAIL, 3 = BLOCKED (a prerequisite is missing, so nothing was proven).
The tun/billing/grants/lifecycle/release suites arrive in rounds 2-6 and refuse to run here.
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
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ANDROID = ROOT / 'apps' / 'android'
CORE = ROOT / 'native' / 'core'
sys.path.insert(0, str(ROOT / 'scripts'))
import native_env  # noqa: E402

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
    record('manifest.no_vpn_yet', permissions == ['android.permission.ACCESS_NETWORK_STATE', 'android.permission.INTERNET'] and not app.findall('service'),
           'no VpnService/foreground-service declarations before round 2')

    rules = ET.parse(ANDROID / 'app' / 'src' / 'main' / 'res' / 'xml' / 'data_extraction_rules.xml').getroot()
    domains = {'root', 'file', 'database', 'sharedpref', 'external'}
    covered = all({e.get('domain') for e in rules.find(section).findall('exclude')} == domains for section in ('cloud-backup', 'device-transfer'))
    record('manifest.extraction_rules', covered, 'every domain excluded from cloud backup and device transfer')

    nsc = ET.parse(ANDROID / 'app' / 'src' / 'main' / 'res' / 'xml' / 'network_security_config.xml').getroot()
    base = nsc.find('base-config')
    anchors = [c.get('src') for c in base.find('trust-anchors').findall('certificates')]
    record('manifest.network_security', base.get('cleartextTrafficPermitted') == 'false' and anchors == ['system'] and nsc.find('domain-config') is None,
           'cleartext refused, system CAs only, no per-domain exceptions')


# ---------------------------------------------------------------- unit

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


def run_unit_suite():
    if not shutil.which('go'):
        record('go.toolchain', False, 'go not found; nothing was tested', status='BLOCKED')
    else:
        code, passed, failed = count_go_tests(['./mobile', './internal/ipc', './internal/network', './internal/managed'])
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
           '%d app JVM tests passed' % tests if app.returncode == 0 else app.stdout[-600:])


# ---------------------------------------------------------------- emulator

def tool(sdk: Path, *parts):
    path = sdk.joinpath(*parts)
    return path if path.exists() else None


def adb_run(adb, serial, *args, check=True, timeout=120):
    process = subprocess.run([str(adb), '-s', serial, *args], capture_output=True, text=True, timeout=timeout)
    if check and process.returncode != 0:
        raise RuntimeError('adb %s failed: %s' % (' '.join(args), (process.stderr or process.stdout)[-300:]))
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
    port = 5580
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
        if state == '1':
            return serial, process
        time.sleep(5)
    process.kill()
    raise RuntimeError('the emulator did not finish booting in 10 minutes')


def ui_text(adb, serial):
    adb_run(adb, serial, 'shell', 'uiautomator', 'dump', '/data/local/tmp/ui.xml')
    return adb_run(adb, serial, 'shell', 'cat', '/data/local/tmp/ui.xml')


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


def run_emulator_suite(args):
    sdk = sdk_root()
    if sdk is None:
        record('emulator.sdk', False, 'ANDROID_HOME not set', status='BLOCKED')
        return
    adb = tool(sdk, 'platform-tools', 'adb')
    abi = args.abi or host_abi()
    image = 'system-images;android-%s;google_apis;%s' % (args.api, abi)
    image_dir = tool(sdk, 'system-images', 'android-%s' % args.api, 'google_apis', abi)
    missing = [what for what, present in (('emulator', tool(sdk, 'emulator', 'emulator')), ('adb', adb),
                                          ('avdmanager', tool(sdk, 'cmdline-tools', 'latest', 'bin', 'avdmanager')),
                                          (image, image_dir)) if not present]
    if missing:
        record('emulator.prerequisites', False, 'not installed: %s' % ', '.join(missing), status='BLOCKED')
        return
    apk = ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'debug' / 'app-debug.apk'
    core = ANDROID / 'build' / 'core' / 'core-build.json'
    if not apk.is_file() or not core.is_file():
        record('emulator.apk', False, 'build first: python3 scripts/build-native-android.py --target apk', status='BLOCKED')
        return
    core_record = json.loads(core.read_text())
    if abi not in core_record['abis']:
        record('emulator.abi', False, 'the Core AAR has %s, the emulator needs %s (--abi %s)' % (sorted(core_record['abis']), abi, abi), status='BLOCKED')
        return
    name = args.avd or 'asterlink-api%s' % args.api
    # avdmanager and the emulator must agree on where AVDs live; on the CI runner avdmanager
    # follows XDG_CONFIG_HOME while the emulator looks in ~/.android/avd.
    avd_home = Path(os.environ.get('ANDROID_AVD_HOME') or Path.home() / '.android' / 'avd')
    avd_home.mkdir(parents=True, exist_ok=True)
    os.environ['ANDROID_AVD_HOME'] = str(avd_home)
    ensure_avd(sdk, name, image)
    out_dir = Path(args.out or ROOT / 'artifacts' / 'native-android' / 'emulator')
    out_dir.mkdir(parents=True, exist_ok=True)
    serial, process = boot(sdk, adb, name, args.window, out_dir / 'emulator.log')
    try:
        facts = {'api': adb_run(adb, serial, 'shell', 'getprop', 'ro.build.version.sdk').strip(),
                 'abi': adb_run(adb, serial, 'shell', 'getprop', 'ro.product.cpu.abi').strip(),
                 'page_size': adb_run(adb, serial, 'shell', 'getconf', 'PAGE_SIZE').strip(),
                 'avd': name}
        record('emulator.device', facts['abi'] == abi, 'API %(api)s, %(abi)s, page size %(page_size)s, AVD %(avd)s' % facts)

        # Instrumented test with the real AAR (builds and installs both APKs).
        connected_results = ANDROID / 'app' / 'build' / 'outputs' / 'androidTest-results' / 'connected'
        shutil.rmtree(connected_results, ignore_errors=True)
        connected = gradle([':app:connectedDebugAndroidTest'], capture_output=True, text=True, env=dict(os.environ, ANDROID_SERIAL=serial))
        tests, failures = junit_counts(connected_results)
        record('emulator.core_binding', connected.returncode == 0 and tests > 0 and failures == 0,
               'CoreBindingTest on the device: %d test(s), ipc-v1 cases, start/hello/read_snapshot/stop' % tests if connected.returncode == 0 else connected.stdout[-800:])

        # The app itself: launch, wait for the Core, read the screen.
        adb_run(adb, serial, 'install', '-r', str(apk), timeout=300)
        adb_run(adb, serial, 'shell', 'am', 'force-stop', 'com.asterlink.vpn')
        adb_run(adb, serial, 'shell', 'am', 'start', '-W', '-n', 'com.asterlink.vpn/.MainActivity')
        screen = ''
        for _ in range(20):
            time.sleep(1.5)
            screen = ui_text(adb, serial)
            if core_record['mihomo_revision'][:12] in screen:
                break
        shown = core_record['core_version'] in screen and core_record['mihomo_revision'][:12] in screen
        record('emulator.app_shows_core', shown, 'the launched app shows Core %s and Mihomo %s' % (core_record['core_version'], core_record['mihomo_revision'][:12]))
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
        if process is not None and not args.keep_emulator:
            subprocess.run([str(adb), '-s', serial, 'emu', 'kill'], capture_output=True)
            try:
                process.wait(timeout=60)
            except subprocess.TimeoutExpired:
                process.kill()


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
    args = parser.parse_args()

    if args.suite == 'foundation':
        check_lock_file()
        check_env_example()
        check_platform_split()
        check_manifest()
    elif args.suite == 'unit':
        run_unit_suite()
    elif args.suite == 'emulator':
        try:
            run_emulator_suite(args)
        except (RuntimeError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
            record('emulator.run', False, str(error)[-1800:])
    else:
        print('suite %r arrives in a later round (handoff_app_android.md 10)' % args.suite)
        return EXIT_BLOCKED

    if args.report:
        Path(args.report).parent.mkdir(parents=True, exist_ok=True)
        Path(args.report).write_text(json.dumps(results, indent=2))
    statuses = {r['status'] for r in results}
    print('\n%d cases: %s' % (len(results), ', '.join('%s=%d' % (s, sum(r['status'] == s for r in results)) for s in sorted(statuses))))
    if 'FAIL' in statuses:
        return EXIT_FAIL
    return EXIT_BLOCKED if 'BLOCKED' in statuses else EXIT_PASS


if __name__ == '__main__':
    sys.exit(main())

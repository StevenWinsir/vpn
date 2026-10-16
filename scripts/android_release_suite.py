"""`test-native-android.py --suite release`: audit the packaged Android release and rehearse its life cycle.

Imported by scripts/test-native-android.py, which passes itself as `ctx` (record, adb_run, open/boot
helpers, the backend world and test node starters). Every case ends PASS, FAIL or BLOCKED; a check
that needs something this machine does not have (the release key, a system image, a real phone) is
BLOCKED, never a quiet pass. See docs/android-release.md for what each case proves.
"""
import argparse
import importlib.util
import json
import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import urllib.request
import xml.etree.ElementTree as ET
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ANDROID = ROOT / 'apps' / 'android'
PACKAGE = 'com.asterlink.vpn'
sys.path.insert(0, str(ROOT / 'scripts'))
import release_suite  # noqa: E402  (secret patterns and local .env values, shared with macOS)
import windows_release  # noqa: E402  (SHA256SUMS verification)


def _package_module():
    spec = importlib.util.spec_from_file_location('package_native_android', ROOT / 'scripts' / 'package-native-android.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


pkg = _package_module()

# What the app may ask for. androidx.core adds the app-private dynamic-receiver permission.
ALLOWED_PERMISSIONS = {
    'android.permission.INTERNET', 'android.permission.ACCESS_NETWORK_STATE', 'android.permission.CHANGE_NETWORK_STATE',
    'android.permission.FOREGROUND_SERVICE', 'android.permission.FOREGROUND_SERVICE_SPECIAL_USE', 'android.permission.POST_NOTIFICATIONS',
    PACKAGE + '.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION',
}
REQUIRED_PERMISSIONS = {'android.permission.INTERNET', 'android.permission.FOREGROUND_SERVICE_SPECIAL_USE'}
# Test frameworks, the instrumented acceptance tests, the TUN hooks and debug-only tooling.
DEX_MARKERS = (b'Landroidx/test/', b'Lorg/junit/', b'Lorg/robolectric/', b'Lcom/github/takahirom/roborazzi', b'Lkotlinx/coroutines/test/',
               b'TunAcceptanceTest', b'BillingAcceptanceTest', b'LifecycleAcceptanceTest', b'SuiteDriver', b'CompetitorVpnService',
               b'tunTestStart', b'Landroidx/compose/ui/tooling/PreviewActivity', b'Landroidx/compose/ui/test/')
SO_MARKERS = (b'TunTestStart', b'tunTestStart', b'asterlink_tuntest')
ELF_MACHINE = {'arm64-v8a': 183, 'x86_64': 62, 'armeabi-v7a': 40}
MIN_ALIGN = 16384


def version_code(version):
    major, minor, patch = (int(x) for x in re.match(r'(\d+)\.(\d+)\.(\d+)', version).groups())
    return major * 10000 + minor * 100 + patch


def elf_facts(data: bytes):
    """(e_machine, [p_align of every PT_LOAD]) of an ELF shared object."""
    if data[:4] != b'\x7fELF':
        raise ValueError('not an ELF file')
    is64, order = data[4] == 2, '<' if data[5] == 1 else '>'
    machine = struct.unpack_from(order + 'H', data, 0x12)[0]
    if is64:
        phoff = struct.unpack_from(order + 'Q', data, 0x20)[0]
        entsize, count = struct.unpack_from(order + 'HH', data, 0x36)
    else:
        phoff = struct.unpack_from(order + 'I', data, 0x1C)[0]
        entsize, count = struct.unpack_from(order + 'HH', data, 0x2A)
    aligns = []
    for i in range(count):
        offset = phoff + i * entsize
        if struct.unpack_from(order + 'I', data, offset)[0] == 1:  # PT_LOAD
            aligns.append(struct.unpack_from(order + ('Q' if is64 else 'I'), data, offset + (0x30 if is64 else 0x1C))[0])
    return machine, aligns


def sh(cmd):
    return subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True)


# ---------------------------------------------------------------- R03: the release folder

def audit(ctx, out: Path):
    record = ctx.record
    info_file = out / 'release-info.json'
    if not info_file.is_file():
        record('release.R03-folder', False, 'no release-info.json in %s: python3 scripts/package-native-android.py --channel internal' % out, status='BLOCKED')
        return None
    info = json.loads(info_file.read_text())
    problems = windows_release.verify_checksums(out)
    record('release.R03-checksums', not problems, 'SHA256SUMS lists and matches every file (%d)' % len(list(out.iterdir())) if not problems else '; '.join(problems[:5]))

    honest = info.get('trusted_release') is False and info.get('channel') == 'internal' and bool(info.get('open_gates'))
    record('release.R03-not-trusted', honest, 'trusted_release=false, channel internal, %d open gates listed' % len(info.get('open_gates', [])))
    expected_code = version_code(info['version'])
    record('release.R03-version', info.get('version_code') == expected_code and all(a['version_code'] == expected_code for a in info['apks']),
           'version %s -> versionCode %d (MAJOR*10000+MINOR*100+PATCH)' % (info['version'], expected_code))

    key_mode = info['signing']['key']
    record('release.R03-release-key', key_mode == 'release-key',
           'signed with the AsterLink release key, certificate SHA-256 %s' % info['signing']['certificate_sha256'] if key_mode == 'release-key'
           else 'signed with a throwaway key (%s): the pipeline is proven, the files are not publishable' % key_mode,
           status=None if key_mode == 'release-key' else 'BLOCKED')

    needles = list(release_suite.SECRET_PATTERNS) + sorted(release_suite.env_secret_values(ROOT, sh))
    key_password = pkg.keychain_password() if key_mode == 'release-key' else None
    if key_password:
        needles.append(key_password.encode())
    if os.environ.get(pkg.PASS_ENV):
        needles.append(os.environ[pkg.PASS_ENV].encode())
    for apk_info in info['apks']:
        audit_apk(ctx, out / apk_info['file'], apk_info, info, needles)

    probe = tempfile.mkdtemp(prefix='asterlink-probe-')
    try:
        Path(probe, 'planted.bin').write_bytes(b'\x00junk postgres://user:pw@host/db junk')
        detected = bool(release_suite.scan_tree(probe, needles))
    finally:
        shutil.rmtree(probe, ignore_errors=True)
    record('release.R03-secret-self', detected, 'the secret scanner finds a planted connection string' if detected else 'the scanner MISSED a planted secret')

    notices = (out / 'THIRD_PARTY_LICENSES.txt').read_text(encoding='utf-8') if (out / 'THIRD_PARTY_LICENSES.txt').is_file() else ''
    maven = json.loads((ANDROID / 'app' / 'build' / 'outputs' / 'release-dependencies.json').read_text()) \
        if (ANDROID / 'app' / 'build' / 'outputs' / 'release-dependencies.json').is_file() else []
    gaps = [m for m in ('GNU GENERAL PUBLIC LICENSE', 'Apache License', 'github.com/metacubex/mihomo') if m not in notices]
    gaps += ['libgojni.so for %s' % a['abi'] for a in info['apks'] if 'libgojni.so for %s' % a['abi'] not in notices]
    gaps += [m for m in maven if m not in notices][:5]
    if info.get('go_modules_without_license_file'):
        gaps.append('Go modules without a licence file: %s' % info['go_modules_without_license_file'])
    for name in ('SOURCE.txt', 'dependencies.cdx.json'):
        if not (out / name).is_file():
            gaps.append(name + ' missing')
    record('release.R03-licenses', not gaps, 'notices: Mihomo GPL-3.0, %d Go modules, %d Maven artifacts (Apache-2.0); SOURCE.txt, CycloneDX inventory'
           % (info.get('third_party_go_modules', 0), len(maven)) if not gaps else 'missing: %s' % gaps)
    archives = [a for a in info.get('source_archives', []) if (out / a).is_file()]
    record('release.R03-source-archives', len(archives) == 2, 'GPL corresponding source: %s' % ', '.join(archives) if archives
           else 'packaged with --skip-source-archives: not publishable', status=None if len(archives) == 2 else 'BLOCKED')
    private = out.parent / (out.name + '-private')
    leaked = sorted(p.name for p in out.iterdir() if 'mapping' in p.name.lower() or p.suffix in ('.p12', '.jks', '.keystore', '.pass'))
    record('release.R03-private-kept-out', not leaked and any(private.glob('mapping-*.txt')),
           'the R8 mapping is in %s/, not in the published folder; no key file published' % private.name if not leaked else 'published: %s' % leaked)
    return info


def audit_apk(ctx, apk: Path, apk_info, info, needles):
    record, abi = ctx.record, apk_info['abi']
    tag = 'release.R03-%s' % abi
    verdict = pkg.verify_signature(apk)
    debug_cert = 'Android Debug' in (verdict['certificate_subject'] or '')
    record(tag + '-signature', verdict['v2'] and verdict['v3'] and verdict['signers'] == 1 and not debug_cert
           and verdict['certificate_sha256'] == info['signing']['certificate_sha256'],
           'apksigner: v2=%s v3=%s, %d signer, %s (%s)' % (verdict['v2'], verdict['v3'], verdict['signers'], (verdict['certificate_sha256'] or '?')[:16], verdict['certificate_subject']))

    facts = pkg.badging(apk)
    xml = subprocess.run([str(pkg.build_tool('aapt2')), 'dump', 'xmltree', '--file', 'AndroidManifest.xml', str(apk)], capture_output=True, text=True, check=True).stdout
    permissions = set(re.findall(r"uses-permission: name='([^']+)'", subprocess.run(
        [str(pkg.build_tool('aapt2')), 'dump', 'permissions', str(apk)], capture_output=True, text=True, check=True).stdout))
    problems = []
    if facts['package'] != PACKAGE or facts['version_name'] != info['version'] or facts['version_code'] != version_code(info['version']):
        problems.append('identity %s %s/%s' % (facts['package'], facts['version_name'], facts['version_code']))
    if (facts['min_sdk'], facts['target_sdk']) != (26, 36):
        problems.append('sdk %s/%s' % (facts['min_sdk'], facts['target_sdk']))
    if facts['debuggable'] or re.search(r'debuggable\(0x[0-9a-f]+\)=(true|0xffffffff)', xml):
        problems.append('debuggable')
    if facts['test_only'] or 'testOnly' in xml:
        problems.append('testOnly')
    if re.search(r'usesCleartextTraffic\(0x[0-9a-f]+\)=(true|0xffffffff)', xml):
        problems.append('cleartext')
    if not re.search(r'allowBackup\(0x[0-9a-f]+\)=(false|0x0)', xml):
        problems.append('backup not off')
    if 'androidx.activity.ComponentActivity' in xml or 'ui.test' in xml:
        problems.append('test activity in the manifest')
    if not re.search(r'networkSecurityConfig', xml):
        problems.append('no network security config')
    extra, missing = permissions - ALLOWED_PERMISSIONS, REQUIRED_PERMISSIONS - permissions
    if extra or missing:
        problems.append('permissions +%s -%s' % (sorted(extra), sorted(missing)))
    record(tag + '-manifest', not problems, '%s %s (versionCode %d), minSdk %d, targetSdk %d, not debuggable/testOnly, no cleartext, backup off, %d permissions in the allowlist'
           % (facts['package'], facts['version_name'], facts['version_code'], facts['min_sdk'], facts['target_sdk'], len(permissions)) if not problems else '; '.join(problems))

    align = subprocess.run([str(pkg.build_tool('zipalign')), '-c', '-P', '16', '4', str(apk)], capture_output=True, text=True)
    libs, bad = [], []
    dex_hits, so_hits, secret_hits, api_found = set(), set(), set(), False
    api = info['api_base'].encode()
    with zipfile.ZipFile(apk) as archive:
        for entry in archive.infolist():
            data = archive.read(entry)
            name = entry.filename
            if name.startswith('lib/'):
                parts = name.split('/')
                if parts[1] != abi:
                    bad.append(name)
                elif name.endswith('.so'):
                    machine, aligns = elf_facts(data)
                    libs.append('%s(0x%x)' % (parts[2], min(aligns)))
                    if entry.compress_type != zipfile.ZIP_STORED or machine != ELF_MACHINE[abi] or not aligns or min(aligns) < MIN_ALIGN:
                        bad.append('%s stored=%s machine=%d align=%s' % (name, entry.compress_type == zipfile.ZIP_STORED, machine, [hex(a) for a in aligns]))
                    so_hits.update(m.decode() for m in SO_MARKERS if m in data)
            if re.fullmatch(r'classes\d*\.dex', name):
                dex_hits.update(m.decode() for m in DEX_MARKERS if m in data)
                api_found = api_found or api in data
            secret_hits.update(name for needle in needles if needle in data)
    record(tag + '-alignment', align.returncode == 0 and not bad and any(l.startswith('libgojni.so') for l in libs),
           'zipalign -P 16 ok; only lib/%s, stored, ELF %s, LOAD >= 16 KB: %s' % (abi, abi, ', '.join(libs)) if align.returncode == 0 and not bad
           else 'zipalign rc=%d; %s' % (align.returncode, bad[:4]))
    record(tag + '-no-test-code', not dex_hits and not so_hits, 'no test framework, acceptance test, TUN hook or debug tooling in the dex or libgojni.so'
           if not dex_hits and not so_hits else 'found: %s' % sorted(dex_hits | so_hits))
    record(tag + '-no-secrets', not secret_hits, 'no private key, connection string, local .env secret or key password in any entry (%d needles)' % len(needles)
           if not secret_hits else 'secret-looking content in: %s' % sorted(secret_hits))
    record(tag + '-endpoint', api_found and info['api_base'].startswith('https://') and info.get('app_env') == 'release',
           'BuildConfig carries %s (APP_ENV=%s)' % (info['api_base'], info.get('app_env')))


# ---------------------------------------------------------------- device helpers

class Screen:
    """One uiautomator dump: nodes with text, content-desc, bounds, state."""

    def __init__(self, xml):
        self.nodes = []
        try:
            root = ET.fromstring(xml[xml.find('<?xml'):] if '<?xml' in xml else xml)
        except ET.ParseError:
            return
        for node in root.iter('node'):
            bounds = [int(v) for v in re.findall(r'\d+', node.get('bounds', '[0,0][0,0]'))]
            self.nodes.append({'text': node.get('text', ''), 'desc': node.get('content-desc', ''), 'bounds': bounds,
                               'checked': node.get('checked') == 'true', 'checkable': node.get('checkable') == 'true', 'enabled': node.get('enabled') == 'true',
                               'package': node.get('package', ''), 'resource': node.get('resource-id', ''), 'class': node.get('class', '')})

    def find(self, pattern, field=None):
        regex = re.compile(pattern)
        for node in self.nodes:
            if any(regex.search(node[f]) for f in ((field,) if field else ('text', 'desc'))):
                return node
        return None

    def texts(self):
        return [n['text'] or n['desc'] for n in self.nodes if n['text'] or n['desc']]


def screen(ctx, adb, serial):
    for _ in range(4):
        out = subprocess.run([str(adb), '-s', serial, 'exec-out', 'uiautomator', 'dump', '/dev/tty'], capture_output=True, text=True, timeout=60).stdout
        if '<hierarchy' in out:
            return Screen(out[:out.rfind('</hierarchy>') + len('</hierarchy>')])
        time.sleep(1)
    return Screen('')


def tap(ctx, adb, serial, node, what='element'):
    if node is None:
        raise RuntimeError('the %s to tap is not on the screen' % what)
    x1, y1, x2, y2 = node['bounds']
    ctx.adb_run(adb, serial, 'shell', 'input', 'tap', str((x1 + x2) // 2), str((y1 + y2) // 2))


def wait_for(ctx, adb, serial, predicate, timeout=60):
    """Polls the screen until predicate(screen) returns something truthy; also allows a system
    permission prompt (notifications) so it never blocks the flow. Returns (value, last screen)."""
    deadline, last = time.time() + timeout, Screen('')
    while time.time() < deadline:
        last = screen(ctx, adb, serial)
        allow = last.find(r'permission_allow_button', 'resource')
        if allow:
            tap(ctx, adb, serial, allow)
            continue
        value = predicate(last)
        if value:
            return value, last
        time.sleep(1)
    return None, last


def type_into(ctx, adb, serial, field, text):
    tap(ctx, adb, serial, field)
    time.sleep(0.5)
    # Typed as key events through `input text` (adb shell quoting: single quotes, no spaces in the values).
    ctx.adb_run(adb, serial, 'shell', "input text '%s'" % text.replace("'", ''))


def installed_version(ctx, adb, serial):
    dump = ctx.adb_run(adb, serial, 'shell', 'dumpsys', 'package', PACKAGE, check=False)
    match = re.search(r'versionName=(\S+)', dump)
    return match.group(1) if match else None


def install(ctx, adb, serial, apk, replace=True):
    return subprocess.run([str(adb), '-s', serial, 'install', *(['-r'] if replace else []), str(apk)], capture_output=True, text=True, timeout=300)


def launch(ctx, adb, serial):
    ctx.adb_run(adb, serial, 'shell', 'am', 'start', '-W', '-n', PACKAGE + '/.MainActivity')


SIGNED_IN = r'^(Lines|线路)$'
LOGIN_FIELD = r'^(Email|邮箱)$'


# ---------------------------------------------------------------- R01/R02: rehearsal on one emulator

def connect_via_ui(ctx, adb, serial, line):
    """Lines tab -> the line -> Connect tab -> the big button; accepts the system VPN consent dialog if
    it appears. Returns (connected, consent dialog seen, description)."""
    tap(ctx, adb, serial, screen(ctx, adb, serial).find(SIGNED_IN, 'text'), 'Lines tab')
    row, shot = wait_for(ctx, adb, serial, lambda s: s.find('^%s$' % re.escape(line), 'text'), 30)
    if not row:
        return None, False, 'line %s not listed; visible: %s' % (line, shot.texts()[:12])
    tap(ctx, adb, serial, row, 'line ' + line)
    time.sleep(1)
    tap(ctx, adb, serial, screen(ctx, adb, serial).find(r'^(Connect|连接)$', 'text'), 'Connect tab')

    def enabled_button(s):
        node = s.find(r'^(Connect button|连接按钮)', 'desc')
        return node if node and node['enabled'] else None
    button, shot = wait_for(ctx, adb, serial, enabled_button, 30)
    if not button:
        return None, False, 'connect button not enabled; visible: %s' % shot.texts()[:12]
    tap(ctx, adb, serial, button, 'connect button')
    started, seen, connected = time.time(), False, None
    while time.time() - started < 120 and not connected:
        shot = screen(ctx, adb, serial)
        allow = shot.find(r'permission_allow_button', 'resource')
        if allow:
            tap(ctx, adb, serial, allow, 'notification permission Allow')
        elif any(n['package'] == 'com.android.vpndialogs' for n in shot.nodes):
            seen = True
            ok = shot.find(r'^(OK|确定)$', 'text')
            if ok:
                tap(ctx, adb, serial, ok, 'consent OK')
        elif shot.find(r'^(Connected|已连接)') and ctx.vpn_up(adb, serial):
            connected = True
        time.sleep(1)
    return connected, seen, ('"Connected" (Core gates, TUN, Verify) and a VPN network in %.1f s' % (time.time() - started)
                             if connected else 'not connected; visible: %s' % shot.texts()[:12])


def build_rehearsal(ctx, emu, scratch: Path, port: int):
    env_file = scratch / 'rehearsal.env'
    env_file.write_text('APP_ENV=development\nAPI_BASE_URL=http://127.0.0.1:%d/api/v1/client\nWEBSITE_BASE_URL=http://localhost:3000\n' % port)
    base = re.search(r'^asterlink\.appVersion=(\S+)$', (ANDROID / 'gradle.properties').read_text(), re.M).group(1)
    major, minor, patch = (int(x) for x in re.match(r'(\d+)\.(\d+)\.(\d+)', base).groups())
    versions = [base, '%d.%d.%d' % (major, minor, patch + 1)]
    key = scratch / 'rehearsal.p12'
    apks = []
    for version in versions:
        out = scratch / ('v' + version)
        build = subprocess.run([sys.executable, str(ROOT / 'scripts' / 'package-native-android.py'), '--channel', 'internal', '--key', 'ephemeral',
                                '--ephemeral-key', str(key), '--env-file', str(env_file), '--abi', emu.abi, '--app-version', version,
                                '--release-dir', str(out), '--skip-source-archives'], capture_output=True, text=True)
        if build.returncode != 0:
            raise RuntimeError('rehearsal build %s failed: %s' % (version, (build.stdout + build.stderr)[-1200:]))
        apks.append(out / ('AsterLink-%s-android-%s.apk' % (version, emu.abi)))
    # The same v2 under a different key: what an impostor or a lost key looks like to the phone.
    foreign_key = pkg.ephemeral_key(scratch, 'foreign')
    foreign = scratch / 'foreign.apk'
    pkg.sign(apks[1], foreign, foreign_key, 26)
    return versions, apks, foreign


def rehearsal(ctx, args, emu, scratch: Path):
    record, adb, serial = ctx.record, emu.adb, emu.serial
    port = ctx.free_port()
    versions, (v1, v2), foreign = build_rehearsal(ctx, emu, scratch, port)
    record('release.R01-rehearsal-build', True, 'release pipeline (R8, ABI split, apksigner v2+v3, one throwaway key) built %s and %s for %s against 127.0.0.1:%d'
           % (versions[0], versions[1], emu.abi, port))
    helpers, pg_data, world = [], None, None
    try:
        node, sink = ctx.start_helpers(argparse.Namespace(no_reality=True, reality_dest=''), scratch, helpers)
        extra = {'ASTERLINK_ANDROID_TESTNODE': os.path.join(scratch, 'node.json'), 'ASTERLINK_ANDROID_NODE_HOST': ctx.EMULATOR_HOST,
                 'ASTERLINK_ANDROID_SINK': 'http://%s:%s' % (ctx.LOOPBACK, sink['port']), 'ASTERLINK_ANDROID_SINK_TOKEN': sink['token']}
        pg_data, world, world_file, port = ctx.start_world(scratch, 'TestAndroidBillingWorld', 'ASTERLINK_ANDROID_BILLING_WORLD', extra, port=port)
        facts = json.loads(world_file.read_text())
        email, password, line = facts['emails']['idle'], facts['password'], facts['names']['dedicated']

        subprocess.run([str(adb), '-s', serial, 'uninstall', PACKAGE], capture_output=True)
        ctx.adb_run(adb, serial, 'reverse', 'tcp:%d' % port, 'tcp:%d' % port)
        result = install(ctx, adb, serial, v1, replace=False)
        record('release.R01-install', result.returncode == 0 and installed_version(ctx, adb, serial) == versions[0],
               'fresh install of the signed %s APK: %s' % (versions[0], (result.stdout + result.stderr).strip()[-120:]))
        launch(ctx, adb, serial)
        field, shot = wait_for(ctx, adb, serial, lambda s: s.find(LOGIN_FIELD), 60)
        if not field:
            record('release.R01-sign-in', False, 'no login screen; visible: %s' % shot.texts()[:12])
            return
        type_into(ctx, adb, serial, field, email)
        shot = screen(ctx, adb, serial)
        type_into(ctx, adb, serial, shot.find(r'^(Password|密码)$'), password)
        ctx.adb_run(adb, serial, 'shell', 'input', 'keyevent', 'KEYCODE_BACK')  # hide the keyboard
        time.sleep(0.5)
        def remember_row(s):
            label = s.find(r'^(Remember password|记住密码)$')
            if label is None:
                return None
            middle = (label['bounds'][1] + label['bounds'][3]) // 2
            return next((n for n in s.nodes if n['checkable'] and n['bounds'][1] <= middle <= n['bounds'][3]), None)
        row = remember_row(screen(ctx, adb, serial))
        if row is not None and not row['checked']:
            tap(ctx, adb, serial, row, 'remember-password switch')
            time.sleep(0.5)
            row = remember_row(screen(ctx, adb, serial))
        if row is None or not row['checked']:
            record('release.R01-sign-in', False, 'could not turn "remember password" on')
            return
        tap(ctx, adb, serial, screen(ctx, adb, serial).find(r'^(Sign in|登录)$', 'text'))
        signed, shot = wait_for(ctx, adb, serial, lambda s: s.find(SIGNED_IN), 60)
        record('release.R01-sign-in', bool(signed), 'signed in through the login screen with "remember password" (synthetic world account)'
               if signed else 'not signed in; visible: %s' % shot.texts()[:12])
        if not signed:
            return
        connected, consent, detail = connect_via_ui(ctx, adb, serial, line)
        record('release.R01-connect', consent and bool(connected),
               'first-use system VPN consent dialog %s, then %s' % ('accepted' if consent else 'NOT shown', detail))
        if not connected:
            return
        tap(ctx, adb, serial, screen(ctx, adb, serial).find(r'^(Connect button|连接按钮)', 'desc'))
        stopped, shot = wait_for(ctx, adb, serial, lambda s: s.find(r'^(Not connected|未连接)') and not ctx.vpn_up(adb, serial), 60)
        record('release.R01-disconnect', bool(stopped), 'disconnected and settled; no VPN network left' if stopped else 'still connected; visible: %s' % shot.texts()[:12])

        # R02: upgrade in place keeps the remembered password, the settings and the VPN consent.
        result = install(ctx, adb, serial, v2)
        launch(ctx, adb, serial)
        # The login screen shows first while the remembered password signs in online (no token is kept).
        signed, shot = wait_for(ctx, adb, serial, lambda s: s.find(SIGNED_IN), 60)
        upgraded = installed_version(ctx, adb, serial)
        record('release.R02-upgrade', result.returncode == 0 and upgraded == versions[1] and bool(signed),
               'install -r %s over %s: %s; signed in again by itself from the remembered password'
               % (versions[1], versions[0], (result.stdout + result.stderr).strip()[-60:]) if signed
               else 'after the upgrade (%s) still not signed in after 60 s; remembered email %s; visible: %s'
               % (upgraded, 'shown' if shot.find(re.escape(email)) else 'NOT shown', shot.texts()[:12]))
        if not signed:
            return
        connected, seen_dialog, detail = connect_via_ui(ctx, adb, serial, line)
        record('release.R02-upgrade-consent', bool(connected) and not seen_dialog,
               'connected after the upgrade without a new consent dialog (the system kept it for the same signer)' if connected and not seen_dialog
               else 'consent dialog shown again=%s; %s' % (seen_dialog, detail))

        result = install(ctx, adb, serial, v1)
        output = (result.stdout + result.stderr).strip()
        record('release.R02-downgrade', result.returncode != 0 and 'INSTALL_FAILED_VERSION_DOWNGRADE' in output and installed_version(ctx, adb, serial) == versions[1],
               'installing %s over %s is refused: %s' % (versions[0], versions[1], output[-80:]))
        result = install(ctx, adb, serial, foreign)
        output = (result.stdout + result.stderr).strip()
        record('release.R02-foreign-key', result.returncode != 0 and 'INSTALL_FAILED_UPDATE_INCOMPATIBLE' in output,
               'the same version signed by another key is refused: %s' % output[-80:])

        was_up = ctx.vpn_up(adb, serial)
        result = subprocess.run([str(adb), '-s', serial, 'uninstall', PACKAGE], capture_output=True, text=True, timeout=120)
        gone = None
        for i in range(20):
            if not ctx.vpn_up(adb, serial):
                gone = i * 0.5
                break
            time.sleep(0.5)
        path = ctx.adb_run(adb, serial, 'shell', 'pm', 'path', PACKAGE, check=False).strip()
        appops = ctx.adb_run(adb, serial, 'shell', 'appops', 'get', PACKAGE, 'ACTIVATE_VPN', check=False)
        result2 = install(ctx, adb, serial, v2, replace=False)
        launch(ctx, adb, serial)
        fresh, shot = wait_for(ctx, adb, serial, lambda s: (s.find(LOGIN_FIELD) and 'login') or (s.find(SIGNED_IN) and 'signed'), 60)
        empty = fresh == 'login' and not shot.find(re.escape(email))
        record('release.R02-uninstall', result.returncode == 0 and was_up and gone is not None and not path and 'allow' not in appops and result2.returncode == 0 and empty,
               'uninstalled while connected: VPN gone in %s, package gone, VPN consent gone (%s); a reinstall starts at an empty login screen'
               % ('%.1f s' % gone if gone is not None else 'NOT 10 s', appops.strip()[-60:] or 'no record') if empty
               else 'after uninstall + reinstall the app shows: %s' % shot.texts()[:12])
    finally:
        subprocess.run([str(adb), '-s', serial, 'uninstall', PACKAGE], capture_output=True)
        try:
            urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:%d/__world/stop' % port, data=b'{}', method='POST'), timeout=10)
        except OSError:
            pass
        ctx.adb_run(adb, serial, 'reverse', '--remove', 'tcp:%d' % port, check=False)
        if world:
            try:
                world.wait(timeout=60)
            except subprocess.TimeoutExpired:
                world.kill()
        for helper in helpers:
            helper.terminate()
            try:
                helper.wait(timeout=10)
            except subprocess.TimeoutExpired:
                helper.kill()
        if pg_data:
            subprocess.run(['pg_ctl', '-D', str(pg_data), '-m', 'fast', '-w', 'stop'], stdout=subprocess.DEVNULL, check=False)


# ---------------------------------------------------------------- R01 per image / R04: the published APK

def smoke(ctx, adb, serial, apk: Path, info, case, label):
    """Install the published APK, start it, read the Core's identity from the login screen, uninstall."""
    record = ctx.record
    subprocess.run([str(adb), '-s', serial, 'uninstall', PACKAGE], capture_output=True)
    ctx.adb_run(adb, serial, 'logcat', '-c', check=False)
    result = install(ctx, adb, serial, apk, replace=False)
    if result.returncode != 0:
        record(case, False, '%s: install failed: %s' % (label, (result.stdout + result.stderr).strip()[-200:]))
        return
    launch(ctx, adb, serial)
    identity = 'Core %s' % info['core']['core_version']
    mihomo = info['core']['mihomo_revision'][:12]
    shown, shot = wait_for(ctx, adb, serial, lambda s: s.find(re.escape(identity) + '.*' + mihomo), 90)
    pid = ctx.adb_run(adb, serial, 'shell', 'pidof', PACKAGE, check=False).strip()
    crash = ctx.adb_run(adb, serial, 'logcat', '-d', '-b', 'crash', check=False)
    crashed = PACKAGE in crash
    record(case, bool(shown) and bool(pid) and not crashed,
           '%s: installed, started, the login screen shows %s · Mihomo %s (Core loaded through JNI under R8), process %s, no crash'
           % (label, identity, mihomo, pid) if shown and pid and not crashed
           else '%s: shown=%s pid=%s crashed=%s; visible %s' % (label, bool(shown), pid or '-', crashed, shot.texts()[:10]))
    subprocess.run([str(adb), '-s', serial, 'uninstall', PACKAGE], capture_output=True)


def parse_avd(spec):
    match = re.fullmatch(r'api(\d+(?:\.\d+)?)(-16k)?', spec.strip())
    if not match:
        raise ValueError('bad --avds entry %r (api<N> or api<N>-16k)' % spec)
    api, page16 = match.group(1), bool(match.group(2))
    return api, page16, 'asterlink-api%s%s' % (api, '-16k' if page16 else '')


def run(ctx, args):
    record = ctx.record
    out = Path(args.release_dir or ROOT / 'artifacts' / 'native-android' / 'release').resolve()
    info = audit(ctx, out)
    if args.static_only or info is None:
        return
    sdk = ctx.sdk_root()
    publish = {a['abi']: out / a['file'] for a in info['apks']}
    specs = [parse_avd(s) for s in args.avds.split(',') if s.strip()]
    for index, (api, page16, name) in enumerate(specs):
        abi = args.abi or ctx.host_abi()
        tag = 'google_apis_ps16k' if page16 else 'google_apis'
        case = 'release.R01-api%s%s' % (api, '-16k' if page16 else '')
        if sdk is None or ctx.tool(sdk, 'system-images', 'android-%s' % api, tag, abi) is None:
            record(case, False, 'system image %s;%s;%s is not installed' % (api, tag, abi), status='BLOCKED')
            continue
        emu_args = argparse.Namespace(**dict(vars(args), api=api, avd=name, abi=abi))
        # avdmanager and the emulator must agree on the AVD folder (on the CI runner avdmanager follows
        # XDG_CONFIG_HOME); set it before the AVD is created, as open_emulator does.
        avd_home = Path(os.environ.get('ANDROID_AVD_HOME') or Path.home() / '.android' / 'avd')
        avd_home.mkdir(parents=True, exist_ok=True)
        os.environ['ANDROID_AVD_HOME'] = str(avd_home)
        ctx.ensure_avd(sdk, name, 'system-images;android-%s;%s;%s' % (api, tag, abi))
        small_data_partition(name)
        emu = open_image(ctx, emu_args, sdk, name, abi)
        try:
            label = 'API %s %s, page size %s' % (emu.facts['api'], abi, emu.facts['page_size'])
            if page16 and emu.facts['page_size'] != '16384':
                record(case, False, '%s: expected a 16 KB page kernel' % label)
            elif abi not in publish:
                record(case, False, 'the release has no %s APK (%s)' % (abi, sorted(publish)), status='BLOCKED')
            else:
                smoke(ctx, emu.adb, emu.serial, publish[abi], info, case, label)
            if index == 0:
                if not args.allow_network_changes:
                    record('release.R01-rehearsal', False, 'pass --allow-network-changes: the rehearsal connects a VPN inside the emulator', status='BLOCKED')
                elif not all(shutil.which(t) for t in ('go', 'initdb', 'pg_ctl')):
                    record('release.R01-rehearsal', False, 'go, initdb and pg_ctl are needed for the backend world', status='BLOCKED')
                else:
                    scratch = Path(tempfile.mkdtemp(prefix='asterlink-release-'))
                    try:
                        rehearsal(ctx, args, emu, scratch)
                    finally:
                        shutil.rmtree(scratch, ignore_errors=True)
        finally:
            emu.close(args.keep_emulator and index == 0)
    if args.device:
        adb = ctx.tool(sdk, 'platform-tools', 'adb')
        abi = ctx.adb_run(adb, args.device, 'shell', 'getprop', 'ro.product.cpu.abi').strip()
        model = ctx.adb_run(adb, args.device, 'shell', 'getprop', 'ro.product.model').strip()
        if abi not in publish:
            record('release.R04-device', False, '%s is %s; the release has %s' % (model, abi, sorted(publish)), status='BLOCKED')
        else:
            smoke(ctx, adb, args.device, publish[abi], info, 'release.R04-device', '%s (API %s)' % (model, ctx.adb_run(adb, args.device, 'shell', 'getprop', 'ro.build.version.sdk').strip()))
    else:
        record('release.R04-device', False, 'no real phone connected (--device <serial>); release-info keeps trusted_release=false', status='BLOCKED')


def small_data_partition(name):
    """A smoke test needs little space: 4 GB userdata instead of the 12 GB default the emulator
    reserves up front (it refuses to boot when the disk has less free space than that)."""
    config = Path(os.environ.get('ANDROID_AVD_HOME') or Path.home() / '.android' / 'avd') / ('%s.avd' % name) / 'config.ini'
    if config.is_file():
        text = config.read_text()
        line = 'disk.dataPartition.size=4096M'
        if line not in text:
            text = '\n'.join(l for l in text.splitlines() if not l.startswith('disk.dataPartition.size=')) + '\n' + line + '\n'
            config.write_text(text)


class _Image:
    def __init__(self, adb, serial, process, abi, facts):
        self.adb, self.serial, self.process, self.abi, self.facts = adb, serial, process, abi, facts

    def close(self, keep):
        if self.process is not None and not keep:
            subprocess.run([str(self.adb), '-s', self.serial, 'emu', 'kill'], capture_output=True)
            try:
                self.process.wait(timeout=60)
            except subprocess.TimeoutExpired:
                self.process.kill()


def page_size(ctx, adb, serial):
    """getconf exists from API 28; older images report the kernel page size in smaps."""
    value = ctx.adb_run(adb, serial, 'shell', 'getconf', 'PAGE_SIZE', check=False).strip()
    if value.isdigit():
        return value
    match = re.search(r'KernelPageSize:\s+(\d+) kB', ctx.adb_run(adb, serial, 'shell', 'grep', '-m1', 'KernelPageSize', '/proc/self/smaps', check=False))
    return str(int(match.group(1)) * 1024) if match else 'unknown'


def open_image(ctx, args, sdk, name, abi):
    adb = ctx.tool(sdk, 'platform-tools', 'adb')
    avd_home = Path(os.environ.get('ANDROID_AVD_HOME') or Path.home() / '.android' / 'avd')
    avd_home.mkdir(parents=True, exist_ok=True)
    os.environ['ANDROID_AVD_HOME'] = str(avd_home)
    out_dir = ROOT / 'artifacts' / 'native-android' / 'release-suite'
    out_dir.mkdir(parents=True, exist_ok=True)
    serial, process = ctx.boot(sdk, adb, name, args.window, out_dir / ('%s.log' % name))
    facts = {'api': ctx.adb_run(adb, serial, 'shell', 'getprop', 'ro.build.version.sdk').strip(),
             'abi': ctx.adb_run(adb, serial, 'shell', 'getprop', 'ro.product.cpu.abi').strip(),
             'page_size': page_size(ctx, adb, serial), 'avd': name}
    return _Image(adb, serial, process, abi, facts)

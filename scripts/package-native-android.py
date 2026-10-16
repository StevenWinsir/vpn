#!/usr/bin/env python3
"""Signs and packages the Android client for website download (handoff_app_android.md round 6).

    python3 scripts/package-native-android.py --create-keystore      # once, on the release machine
    python3 scripts/package-native-android.py --channel internal     # every release

--create-keystore makes the free, self-signed AsterLink release key (alias "asterlink", RSA 4096,
PKCS12, valid 30 years) at ~/.asterlink/android/asterlink-release.p12 with a random password stored in
the macOS login Keychain (service "asterlink-android-release"). Android identifies an app by this key:
every later version must be signed with the same key or phones refuse the upgrade. Losing it means
users must uninstall (and lose their saved settings) to install a new build: keep an offline backup of
the .p12 file AND its password (docs/android-release.md). It is never written inside this repository.

The release run
  1. builds the R8-minified release APKs, one per --abi (scripts/build-native-android.py
     --configuration Release; apps/android/.env must say APP_ENV=release and use HTTPS),
  2. signs each with apksigner, APK Signature Scheme v2 + v3 (v1/JAR signing is off: minSdk 26),
     reading the key from ASTERLINK_ANDROID_KEYSTORE / ASTERLINK_ANDROID_KEYSTORE_PASSWORD /
     ASTERLINK_ANDROID_KEY_ALIAS or, by default, the file and Keychain entry above; the password only
     travels in the environment of apksigner, never on a command line,
  3. verifies the signature, the certificate, zipalign -P 16 and the stored, 16 KB aligned .so again,
  4. writes artifacts/native-android/release/ (or --release-dir):
       AsterLink-<version>-android-<abi>.apk   the files the website offers
       THIRD_PARTY_LICENSES.txt                Go modules of libgojni.so per ABI + AndroidX/Kotlin (Apache-2.0)
       dependencies.cdx.json                   CycloneDX-shaped inventory (Go modules, Maven artifacts, Mihomo)
       SOURCE.txt + two source archives        the GPL corresponding source (Mihomo + this repository)
       release-info.json                       version, versionCode, per-APK hashes, certificate, open gates
       SHA256SUMS                              of every file above
     and, beside it in release-private/ (NOT for publishing): the R8 mapping.txt for crash stack traces
     and website-env.txt, the lines for frontend/.env that make the website's /download page offer the APK.

--key ephemeral signs with a throwaway key made in a temporary directory (CI and the release
rehearsal); release-info then says so. The channel is always "internal" and trusted_release stays
false until the open gates in release-info.json are closed; --channel release is refused.

Exit codes: 0 = packaged, 1 = failed, 3 = BLOCKED (a prerequisite is missing).
"""
import argparse
import datetime
import hashlib
import json
import os
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ANDROID = ROOT / 'apps' / 'android'
CORE = ROOT / 'native' / 'core'
sys.path.insert(0, str(ROOT / 'scripts'))
import native_env  # noqa: E402
import windows_release  # noqa: E402  (platform-neutral helpers: source archives, SHA256SUMS)

EXIT_OK, EXIT_FAIL, EXIT_BLOCKED = 0, 1, 3
PACKAGE = 'com.asterlink.vpn'
KEY_DIR = Path.home() / '.asterlink' / 'android'
DEFAULT_KEYSTORE = KEY_DIR / 'asterlink-release.p12'
DEFAULT_ALIAS = 'asterlink'
KEYCHAIN_SERVICE = 'asterlink-android-release'
KEYCHAIN_ACCOUNT = 'keystore'
KEY_DNAME = 'CN=AsterLink, OU=Android, O=AsterLink'
PASS_ENV = 'ASTERLINK_ANDROID_KEYSTORE_PASSWORD'
GO_ARCH = {'arm64-v8a': 'arm64', 'armeabi-v7a': 'arm', 'x86_64': 'amd64'}

# Maven groups in the APK and their license. All are Apache-2.0; a group not listed here fails the
# packaging until someone has checked its license and added it.
MAVEN_LICENSES = {
    'androidx.': 'Apache-2.0', 'org.jetbrains.kotlin': 'Apache-2.0', 'org.jetbrains.kotlinx': 'Apache-2.0',
    'org.jetbrains': 'Apache-2.0', 'com.squareup.okio': 'Apache-2.0', 'com.google.guava': 'Apache-2.0',
    'org.jspecify': 'Apache-2.0',
}
OPEN_GATES = [
    'R04: a real phone (or cloud device) spot check, including vendor ROM background killing',
    'B04-live: a public managed node with authoritative node_reported metering (client_reported is development metering)',
    'legal review: Mihomo GPL-3.0 obligations and the go-lzo (GPL-2.0) combination',
    'website operations: payment callbacks, refunds, password reset, privacy policy and terms pages',
    'distribution: the self-signed APK is installed from the website; Android shows its "unknown sources" prompt and Play Protect may warn',
]


class Blocked(Exception):
    pass


def run(cmd, **kwargs):
    print('+ ' + ' '.join(str(c) for c in cmd), flush=True)
    return subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def capture(cmd, **kwargs):
    return subprocess.run([str(c) for c in cmd], check=True, capture_output=True, text=True, **kwargs).stdout.strip()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with open(path, 'rb') as source:
        for chunk in iter(lambda: source.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def sdk_root() -> Path:
    for key in ('ANDROID_HOME', 'ANDROID_SDK_ROOT'):
        if os.environ.get(key):
            return Path(os.environ[key])
    raise Blocked('ANDROID_HOME is not set')


def build_tool(name: str) -> Path:
    found = sorted((sdk_root() / 'build-tools').glob('*/' + name), key=lambda p: [int(x) for x in re.findall(r'\d+', p.parent.name)])
    if not found:
        raise Blocked('%s not found in the SDK build-tools' % name)
    return found[-1]


def keytool() -> str:
    home = os.environ.get('JAVA_HOME')
    if home and (Path(home) / 'bin' / 'keytool').is_file():
        return str(Path(home) / 'bin' / 'keytool')
    if shutil.which('keytool'):
        return 'keytool'
    raise Blocked('keytool not found (set JAVA_HOME to a JDK)')


# ---------------------------------------------------------------- the release key

def keychain_password():
    if sys.platform != 'darwin' or not shutil.which('security'):
        return None
    found = subprocess.run(['security', 'find-generic-password', '-s', KEYCHAIN_SERVICE, '-a', KEYCHAIN_ACCOUNT, '-w'], capture_output=True, text=True)
    return found.stdout.rstrip('\n') if found.returncode == 0 and found.stdout.strip() else None


def create_keystore(path: Path) -> int:
    if path.exists():
        print('FAIL: %s already exists; a release key is never replaced (it would break every upgrade)' % path)
        return EXIT_FAIL
    if sys.platform != 'darwin' and not os.environ.get(PASS_ENV):
        print('BLOCKED: outside macOS set %s to the password to use (and store it yourself)' % PASS_ENV)
        return EXIT_BLOCKED
    password = os.environ.get(PASS_ENV) or secrets.token_urlsafe(32)
    if sys.platform == 'darwin' and not os.environ.get(PASS_ENV):
        if keychain_password() is not None:
            print('FAIL: the Keychain already holds %s/%s but %s is missing; restore the file from your backup instead' % (KEYCHAIN_SERVICE, KEYCHAIN_ACCOUNT, path))
            return EXIT_FAIL
        # `security -i` reads the command from stdin, so the password is never on a command line.
        stored = subprocess.run(['security', '-i'], input='add-generic-password -s %s -a %s -l "AsterLink Android release key" -w %s\n'
                                % (KEYCHAIN_SERVICE, KEYCHAIN_ACCOUNT, password), capture_output=True, text=True)
        if stored.returncode != 0 or keychain_password() != password:
            print('FAIL: could not store the password in the login Keychain: %s' % (stored.stderr or stored.stdout).strip())
            return EXIT_FAIL
    path.parent.mkdir(parents=True, exist_ok=True)
    os.chmod(path.parent, 0o700)
    env = dict(os.environ, **{PASS_ENV: password})
    run([keytool(), '-genkeypair', '-keystore', path, '-storetype', 'PKCS12', '-alias', DEFAULT_ALIAS, '-keyalg', 'RSA', '-keysize', '4096',
         '-sigalg', 'SHA256withRSA', '-validity', '10950', '-dname', KEY_DNAME, '-storepass:env', PASS_ENV, '-keypass:env', PASS_ENV], env=env)
    os.chmod(path, 0o600)
    fingerprint = certificate_sha256(path, DEFAULT_ALIAS, env)
    print('\nrelease key created: %s (alias %s)\ncertificate SHA-256: %s' % (path, DEFAULT_ALIAS, fingerprint))
    print('password: %s' % ('macOS login Keychain, service %s, account %s' % (KEYCHAIN_SERVICE, KEYCHAIN_ACCOUNT) if sys.platform == 'darwin' else 'the value of ' + PASS_ENV))
    print('BACK UP NOW: copy the .p12 file and its password to offline storage. Without both, no future version can upgrade an installed app.')
    return EXIT_OK


def certificate_sha256(keystore: Path, alias: str, env: dict) -> str:
    listing = capture([keytool(), '-list', '-v', '-keystore', keystore, '-storetype', 'PKCS12', '-alias', alias, '-storepass:env', PASS_ENV], env=env)
    match = re.search(r'SHA256:\s*([0-9A-F:]{95})', listing)
    if not match:
        raise RuntimeError('keytool printed no SHA-256 certificate fingerprint for %s' % alias)
    return match.group(1).replace(':', '').lower()


class SigningKey:
    def __init__(self, keystore: Path, alias: str, password: str, mode: str):
        self.keystore, self.alias, self.mode = keystore, alias, mode
        self.env = dict(os.environ, **{PASS_ENV: password})
        self.certificate = certificate_sha256(keystore, alias, self.env)


def user_key() -> SigningKey:
    keystore = Path(os.environ.get('ASTERLINK_ANDROID_KEYSTORE') or DEFAULT_KEYSTORE)
    if not keystore.is_file():
        raise Blocked('no release key at %s (create it once: python3 scripts/package-native-android.py --create-keystore)' % keystore)
    password = os.environ.get(PASS_ENV) or keychain_password()
    if not password:
        raise Blocked('the key password is neither in %s nor in the Keychain (%s/%s)' % (PASS_ENV, KEYCHAIN_SERVICE, KEYCHAIN_ACCOUNT))
    return SigningKey(keystore, os.environ.get('ASTERLINK_ANDROID_KEY_ALIAS') or DEFAULT_ALIAS, password, 'release-key')


def ephemeral_key(scratch: Path, name='ephemeral', keep: Path = None) -> SigningKey:
    """A throwaway key (CI, rehearsal). Trusted by nobody; it proves the pipeline only. With `keep`
    the key (and its password, in keep + '.pass') is reused across runs: the rehearsal signs two
    versions with one key to test an in-place upgrade."""
    if keep is not None and keep.is_file():
        return SigningKey(keep, DEFAULT_ALIAS, Path(str(keep) + '.pass').read_text().strip(), 'ephemeral-test-key')
    keystore = keep or scratch / ('%s.p12' % name)
    password = secrets.token_urlsafe(24)
    if keep is not None:
        Path(str(keep) + '.pass').write_text(password)
        os.chmod(str(keep) + '.pass', 0o600)
    env = dict(os.environ, **{PASS_ENV: password})
    subprocess.run([keytool(), '-genkeypair', '-keystore', str(keystore), '-storetype', 'PKCS12', '-alias', DEFAULT_ALIAS, '-keyalg', 'RSA', '-keysize', '2048',
                    '-validity', '30', '-dname', 'CN=AsterLink throwaway %s' % name, '-storepass:env', PASS_ENV, '-keypass:env', PASS_ENV],
                   env=env, check=True, capture_output=True)
    return SigningKey(keystore, DEFAULT_ALIAS, password, 'ephemeral-test-key')


# ---------------------------------------------------------------- signing and checks

def sign(unsigned: Path, signed: Path, key: SigningKey, min_sdk: int):
    run([build_tool('apksigner'), 'sign', '--ks', key.keystore, '--ks-type', 'PKCS12', '--ks-key-alias', key.alias,
         '--ks-pass', 'env:' + PASS_ENV, '--key-pass', 'env:' + PASS_ENV, '--min-sdk-version', str(min_sdk),
         '--v1-signing-enabled', 'false', '--v2-signing-enabled', 'true', '--v3-signing-enabled', 'true', '--v4-signing-enabled', 'false',
         '--out', signed, unsigned], env=key.env)


def verify_signature(apk: Path) -> dict:
    """apksigner's own verdict: schemes and the signer certificate."""
    out = capture([build_tool('apksigner'), 'verify', '--verbose', '--print-certs', apk])
    schemes = {s: re.search(r'Verified using %s scheme.*?: (true|false)' % s, out) for s in ('v1', 'v2', 'v3')}
    cert = re.search(r'Signer #1 certificate SHA-256 digest: ([0-9a-f]{64})', out)
    subject = re.search(r'Signer #1 certificate DN: (.+)', out)
    return {'v1': bool(schemes['v1'] and schemes['v1'].group(1) == 'true'), 'v2': bool(schemes['v2'] and schemes['v2'].group(1) == 'true'),
            'v3': bool(schemes['v3'] and schemes['v3'].group(1) == 'true'), 'certificate_sha256': cert.group(1) if cert else None,
            'certificate_subject': subject.group(1).strip() if subject else None, 'signers': len(re.findall(r'^Signer #\d+ certificate DN', out, re.M))}


def badging(apk: Path) -> dict:
    out = capture([build_tool('aapt2'), 'dump', 'badging', apk])
    package = re.search(r"package: name='([^']+)' versionCode='(\d+)' versionName='([^']+)'", out)
    return {'package': package.group(1), 'version_code': int(package.group(2)), 'version_name': package.group(3),
            'min_sdk': int(re.search(r"(?:sdkVersion|minSdkVersion):'(\d+)'", out).group(1)),
            'target_sdk': int(re.search(r"targetSdkVersion:'(\d+)'", out).group(1)),
            'debuggable': 'application-debuggable' in out, 'test_only': "testOnly='-1'" in out,
            'native_code': re.findall(r"native-code: (.+)", out)}


# ---------------------------------------------------------------- notices and inventory

def go_modules(goarch):
    env = dict(os.environ, GOOS='android', GOARCH=goarch, CGO_ENABLED='1', GOFLAGS='-buildvcs=false')
    text = capture(['go', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}{{end}}', './mobile', 'golang.org/x/mobile/bind/java'], cwd=CORE, env=env)
    modules = {}
    for line in text.splitlines():
        path, _, version = line.partition('\t')
        if path and path != 'asterlink/core' and not path.startswith('asterlink/'):
            modules[path] = version
    return modules


def maven_license(coordinate):
    group = coordinate.split(':')[0]
    for prefix, license_id in MAVEN_LICENSES.items():
        if group.startswith(prefix) if prefix.endswith('.') else group == prefix or group.startswith(prefix + '.'):
            return license_id
    return None


def write_notices(out: Path, abis, maven):
    import collect_licenses
    parts, missing, count = [], [], 0
    for abi in abis:
        text, absent, modules = collect_licenses.collect('android', GO_ARCH[abi])
        parts.append('#' * 78 + '\n# libgojni.so for %s (android/%s)\n' % (abi, GO_ARCH[abi]) + '#' * 78 + '\n\n' + text)
        missing += absent
        count = max(count, modules)
    unknown = [c for c in maven if maven_license(c) is None]
    apache = (ROOT / 'scripts' / 'third_party_licenses' / 'Apache-2.0.txt').read_text(encoding='utf-8')
    parts.append('#' * 78 + '\n# Java/Kotlin libraries in the APK (Maven artifacts, resolved release classpath)\n' + '#' * 78 + '\n\n'
                 + '\n'.join('%s  (%s)' % (c, maven_license(c) or 'LICENSE NOT VERIFIED') for c in maven) + '\n\n' + apache)
    (out / 'THIRD_PARTY_LICENSES.txt').write_text('\n'.join(parts), encoding='utf-8')
    return sorted(set(missing)), count, unknown


def write_inventory(out: Path, version, commit, abis, maven, mihomo_commit):
    components = []
    for abi in abis:
        for path, ver in sorted(go_modules(GO_ARCH[abi]).items()):
            components.append({'type': 'library', 'name': path, 'version': ver or 'local', 'purl': 'pkg:golang/%s@%s' % (path, ver) if ver else None,
                               'scope': 'required', 'origin': 'lib/%s/libgojni.so' % abi})
    for coordinate in maven:
        group, name, ver = coordinate.split(':')
        components.append({'type': 'library', 'name': '%s:%s' % (group, name), 'version': ver, 'purl': 'pkg:maven/%s/%s@%s' % (group, name, ver),
                           'scope': 'required', 'origin': 'classes.dex', 'license': maven_license(coordinate)})
    components.append({'type': 'application', 'name': 'Mihomo (Clash.Meta fork)', 'version': mihomo_commit, 'origin': 'embedded in libgojni.so', 'license': 'GPL-3.0'})
    inventory = {'bomFormat': 'CycloneDX', 'specVersion': '1.5', 'version': 1,
                 'metadata': {'component': {'type': 'application', 'name': 'AsterLink for Android', 'version': version},
                              'properties': [{'name': 'source_commit', 'value': commit}]},
                 'components': [{k: v for k, v in c.items() if v is not None} for c in components]}
    (out / 'dependencies.cdx.json').write_text(json.dumps(inventory, indent=2) + '\n')


def write_source_notice(out: Path, commit, mihomo_commit, dirty, version):
    import prepare_mihomo
    patch = prepare_mihomo.prepare(check=True)
    (out / 'SOURCE.txt').write_text(
        'AsterLink for Android %s - corresponding source\n\n'
        'lib/<abi>/libgojni.so inside each APK embeds Mihomo (Clash.Meta fork, GNU GPL v3).\n'
        'This repository commit:  %s%s\nMihomo commit:           %s\n\n'
        'The two tar.gz archives next to this file are `git archive` outputs of exactly these commits.\n'
        'The Mihomo archive is the unchanged upstream base; the AsterLink archive includes the reviewed patch.\n'
        'Mihomo patch: %s (SHA-256 %s).\n'
        'To rebuild: unpack both, place the mihomo tree at native/third_party/Clash.Meta, then at repository root run\n'
        '  python3 scripts/prepare_mihomo.py\n'
        '  python3 scripts/build-native-android.py --target apk --configuration Release --abi arm64-v8a\n'
        'The Go, NDK, x/mobile, Gradle and library versions are pinned in apps/android/toolchain.lock.json,\n'
        'apps/android/gradle/libs.versions.toml, native/core/go.sum; the build steps are in docs/android-release.md.\n'
        % (version, commit, ' (WORKING TREE HAD UNCOMMITTED CHANGES)' if dirty else '', mihomo_commit, patch['id'], patch['patch_sha256']), encoding='utf-8')


# ---------------------------------------------------------------- the release

def env_channel(env_file):
    path = Path(env_file) if env_file else ANDROID / '.env'
    if not path.is_file():
        raise Blocked('apps/android/.env is missing: copy apps/android/.env.example, set APP_ENV=release and the production URLs')
    values = native_env.load_env_file(path)
    return values


def package(args) -> int:
    scratch = Path(tempfile.mkdtemp(prefix='asterlink-android-release-'))
    try:
        env_values = env_channel(args.env_file)
        if args.key == 'release' and args.ephemeral_key:
            raise RuntimeError('--ephemeral-key belongs to --key ephemeral')
        if args.key == 'release' and (env_values['APP_ENV'] != 'release' or args.env_file):
            raise RuntimeError('the release key signs only the production build: apps/android/.env must say APP_ENV=release (it says %s)%s'
                               % (env_values['APP_ENV'], '; --env-file is for rehearsals' if args.env_file else ''))
        key = user_key() if args.key == 'release' else ephemeral_key(scratch, keep=Path(args.ephemeral_key) if args.ephemeral_key else None)
        version = args.app_version or re.search(r'^asterlink\.appVersion=(\S+)$', (ANDROID / 'gradle.properties').read_text(), re.M).group(1)
        if not args.skip_build:
            command = [sys.executable, str(ROOT / 'scripts' / 'build-native-android.py'), '--target', 'apk', '--configuration', 'Release', '--app-version', version]
            for abi in args.abi:
                command += ['--abi', abi]
            if args.env_file:
                command += ['--env-file', args.env_file]
            build = subprocess.run(command)
            if build.returncode == EXIT_BLOCKED:
                raise Blocked('the release build is blocked (see above)')
            if build.returncode != 0:
                raise RuntimeError('the release build failed (see above)')
        report = json.loads((ROOT / 'artifacts' / 'native-android' / 'release-build-report.json').read_text())
        if report['app_version'] != version or sorted(report['apks']) != sorted(args.abi):
            raise RuntimeError('release-build-report.json describes %s %s, not %s %s; build again' % (report['app_version'], sorted(report['apks']), version, args.abi))

        out = Path(args.release_dir).resolve() if args.release_dir else ROOT / 'artifacts' / 'native-android' / 'release'
        private = out.parent / (out.name + '-private')
        for directory in (out, private):
            shutil.rmtree(directory, ignore_errors=True)
            directory.mkdir(parents=True)
        zipalign = build_tool('zipalign')
        apks = []
        for abi in args.abi:
            unsigned = ROOT / report['apks'][abi]['apk']
            if sha256(unsigned) != report['apks'][abi]['sha256']:
                raise RuntimeError('%s changed after the build report was written' % unsigned.name)
            signed = out / ('AsterLink-%s-android-%s.apk' % (version, abi))
            sign(unsigned, signed, key, report['core']['androidapi'])
            run([zipalign, '-c', '-P', '16', '4', signed])
            verdict = verify_signature(signed)
            if not (verdict['v2'] and verdict['v3']) or verdict['signers'] != 1 or verdict['certificate_sha256'] != key.certificate:
                raise RuntimeError('%s: signature check failed: %s' % (signed.name, verdict))
            facts = badging(signed)
            if (facts['package'] != PACKAGE or facts['version_name'] != version or facts['debuggable'] or facts['test_only']
                    or facts['native_code'] != ["'%s'" % abi]):
                raise RuntimeError('%s: unexpected manifest facts %s' % (signed.name, facts))
            with zipfile.ZipFile(signed) as archive:
                gojni = archive.getinfo('lib/%s/libgojni.so' % abi)
                if gojni.compress_type != zipfile.ZIP_STORED:
                    raise RuntimeError('%s: libgojni.so is compressed' % signed.name)
            apks.append({'file': signed.name, 'abi': abi, 'bytes': signed.stat().st_size, 'sha256': sha256(signed),
                         'version_code': facts['version_code'], 'min_sdk': facts['min_sdk'], 'target_sdk': facts['target_sdk'],
                         'libgojni_sha256': report['core']['abis'][abi]['sha256'], 'signature': verdict})
            print('signed %s (%.1f MiB) v2=%s v3=%s cert %s' % (signed.name, signed.stat().st_size / 2 ** 20, verdict['v2'], verdict['v3'], key.certificate[:16]))

        maven = json.loads((ANDROID / 'app' / 'build' / 'outputs' / 'release-dependencies.json').read_text())
        missing, modules, unknown = write_notices(out, args.abi, maven)
        if unknown:
            raise RuntimeError('Maven artifacts with an unverified license (add the group to MAVEN_LICENSES after checking it): %s' % unknown)
        commit, dirty = windows_release.source_state()
        archives, mihomo_commit = ([], report['core']['mihomo_revision'])
        if not args.skip_source_archives:
            archives, mihomo_commit = windows_release.source_archives(out, commit)
        write_inventory(out, version, commit, args.abi, maven, mihomo_commit)
        write_source_notice(out, commit, mihomo_commit, dirty, version)
        info = {
            'format_version': 1, 'product': 'AsterLink for Android', 'package': PACKAGE, 'version': version,
            'version_code': apks[0]['version_code'], 'channel': 'internal', 'api_base': env_values['API_BASE_URL'],
            'website': env_values['WEBSITE_BASE_URL'], 'app_env': env_values['APP_ENV'],
            'built_at': datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0).isoformat(),
            'apks': apks,
            'signing': {'key': key.mode, 'schemes': ['v2', 'v3'], 'certificate_sha256': key.certificate,
                        'self_signed': True, 'note': 'Android trusts the key, not a certificate authority: every later version must use this same key.'},
            # Only true when every gate below is closed; an ephemeral key never makes a trusted release.
            'trusted_release': False,
            'core': {k: report['core'].get(k) for k in ('core_version', 'mihomo_revision', 'go', 'x_mobile', 'ndk', 'androidapi')},
            'mihomo_patch': report['core'].get('mihomo_patch'),
            'source_commit': commit, 'source_dirty': dirty, 'mihomo_commit': mihomo_commit,
            'third_party_go_modules': modules, 'go_modules_without_license_file': missing, 'maven_artifacts': len(maven),
            'source_archives': [a.name for a in archives], 'open_gates': OPEN_GATES,
        }
        (out / 'release-info.json').write_text(json.dumps(info, indent=2) + '\n')
        windows_release.write_checksums(out)
        problems = windows_release.verify_checksums(out)
        if problems:
            raise RuntimeError('SHA256SUMS does not verify: ' + '; '.join(problems))
        mapping = ANDROID / 'app' / 'build' / 'outputs' / 'mapping' / 'release' / 'mapping.txt'
        if mapping.is_file():
            shutil.copy2(mapping, private / ('mapping-%s.txt' % version))
        main_apk = next((a for a in apks if a['abi'] == 'arm64-v8a'), apks[0])
        website = ('# frontend/.env (server-side; restart Next.js after editing). Upload the APK to the URL first.\n'
                   'ANDROID_APK_URL=%s/downloads/%s\nANDROID_APK_VERSION=%s\nANDROID_APK_SHA256=%s\nANDROID_APK_SIZE_BYTES=%d\nANDROID_CERT_SHA256=%s\n'
                   % (env_values['WEBSITE_BASE_URL'].rstrip('/'), main_apk['file'], version, main_apk['sha256'], main_apk['bytes'], key.certificate))
        (private / 'website-env.txt').write_text(website)
        print('\nrelease folder: %s (%d APK, signing=%s, trusted_release=false)\nprivate (do not publish): %s\n\n%s'
              % (out, len(apks), key.mode, private, website))
        return EXIT_OK
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--channel', choices=('internal', 'release'), help='always internal until the open gates are closed')
    parser.add_argument('--create-keystore', action='store_true', help='create the release key once (refuses to replace one)')
    parser.add_argument('--key', choices=('release', 'ephemeral'), default='release', help='ephemeral = a throwaway key (CI, rehearsal)')
    parser.add_argument('--abi', action='append', choices=sorted(GO_ARCH), help='repeatable; default arm64-v8a')
    parser.add_argument('--app-version', help='default: apps/android/gradle.properties asterlink.appVersion')
    parser.add_argument('--release-dir', help='default artifacts/native-android/release')
    parser.add_argument('--env-file', help='ephemeral key only: build with this env-v1 file instead of apps/android/.env')
    parser.add_argument('--ephemeral-key', help='ephemeral key only: create or reuse the throwaway key at this path (tests)')
    parser.add_argument('--skip-build', action='store_true', help='sign the APKs of the last release build')
    parser.add_argument('--skip-source-archives', action='store_true', help='faster local iteration; never for a published release')
    args = parser.parse_args()
    args.abi = sorted(set(args.abi or ['arm64-v8a']))
    try:
        if args.create_keystore:
            path = Path(os.environ.get('ASTERLINK_ANDROID_KEYSTORE') or DEFAULT_KEYSTORE)
            if ROOT in path.resolve().parents:
                raise RuntimeError('the release key must live outside the repository')
            return create_keystore(path)
        if args.channel != 'internal':
            print('FAIL: only --channel internal exists until the gates in release-info.json are closed (trusted_release stays false)')
            return EXIT_FAIL
        return package(args)
    except Blocked as reason:
        print('BLOCKED: %s' % reason)
        return EXIT_BLOCKED
    except (RuntimeError, subprocess.CalledProcessError, native_env.EnvError, OSError) as error:
        print('FAIL: %s' % error)
        return EXIT_FAIL


if __name__ == '__main__':
    sys.exit(main())

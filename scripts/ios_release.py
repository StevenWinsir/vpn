"""Shared pieces of the iOS release chain (handoff_app_ios.md round 6): reading an IPA, scanning a
build for what must not ship, and re-signing an unsigned IPA with a team's certificate and
provisioning profiles. Used by scripts/package-native-ios.py, scripts/ios/resign-ipa.py and the
`--suite release` audit in scripts/test-native-ios.py. Nothing here talks to the network, and no
key, password or profile is ever written to the repository or to the IPA beyond the profile Apple
requires inside the bundle (embedded.mobileprovision)."""
import datetime
import hashlib
import os
from pathlib import Path
import plistlib
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
IOS = ROOT / 'apps' / 'ios'

APP_NAME = 'AsterLink'
EXTENSION_PATH = 'PlugIns/AsterLinkTunnel.appex'
NETWORK_EXTENSION_KEY = 'com.apple.developer.networking.networkextension'
NETWORK_EXTENSION_VALUE = 'packet-tunnel-provider'


class ReleaseError(Exception):
    pass


def run(cmd, **kwargs):
    return subprocess.run([str(c) for c in cmd], check=True, capture_output=True, text=True, **kwargs)


def sha256(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


# ---------------------------------------------------------------- IPA and bundles

def unpack_ipa(ipa: Path, dest: Path) -> Path:
    """Unzips an IPA and returns its single Payload/*.app. Refuses paths that climb out of `dest`."""
    import zipfile
    with zipfile.ZipFile(ipa) as archive:
        for name in archive.namelist():
            target = (dest / name).resolve()
            if dest.resolve() not in target.parents and target != dest.resolve():
                raise ReleaseError('the IPA holds a path outside itself: %s' % name)
        # `unzip` keeps the executable bits that zipfile.extractall drops.
        run(['unzip', '-q', '-o', ipa, '-d', dest])
    apps = sorted((dest / 'Payload').glob('*.app'))
    if len(apps) != 1:
        raise ReleaseError('expected exactly one Payload/*.app, found %d' % len(apps))
    return apps[0]


def pack_ipa(root: Path, ipa: Path):
    """Zips `root`/Payload into an IPA (Payload first, no extra attributes)."""
    if ipa.exists():
        ipa.unlink()
    subprocess.run(['zip', '-qry', str(ipa.resolve()), 'Payload'], cwd=root, check=True)


def read_plist(path: Path) -> dict:
    with open(path, 'rb') as handle:
        return plistlib.load(handle)


def write_plist(path: Path, value: dict):
    with open(path, 'wb') as handle:
        plistlib.dump(value, handle, fmt=plistlib.FMT_BINARY)


def bundle_ids(app: Path) -> tuple:
    return (read_plist(app / 'Info.plist')['CFBundleIdentifier'],
            read_plist(app / EXTENSION_PATH / 'Info.plist')['CFBundleIdentifier'])


# ---------------------------------------------------------------- what a build must not contain

# Apple's required-reason API categories, detected from the undefined symbols a binary imports
# (https://developer.apple.com/documentation/bundleresources/describing-use-of-required-reason-api).
REQUIRED_REASON_SYMBOLS = {
    'NSPrivacyAccessedAPICategoryFileTimestamp': re.compile(r'^_(f?l?stat(at)?|f?getattrlist(bulk)?|getattrlistat)$'),
    'NSPrivacyAccessedAPICategorySystemBootTime': re.compile(r'^_(mach_absolute_time|mach_continuous_time)$|NSProcessInfo.*systemUptime'),
    'NSPrivacyAccessedAPICategoryDiskSpace': re.compile(r'^_(f?statfs|f?statvfs)$'),
    'NSPrivacyAccessedAPICategoryUserDefaults': re.compile(r'^_OBJC_CLASS_\$_NSUserDefaults$'),
}


def required_reason_categories(binary: Path) -> set:
    symbols = run(['nm', '-u', binary]).stdout.splitlines()
    found = set()
    for category, pattern in REQUIRED_REASON_SYMBOLS.items():
        if any(pattern.search(line.strip()) for line in symbols):
            found.add(category)
    # systemUptime is an Objective-C selector, not a symbol: look for it in the method names.
    if b'systemUptime' in binary.read_bytes():
        found.add('NSPrivacyAccessedAPICategorySystemBootTime')
    return found


def declared_categories(manifest: Path) -> set:
    data = read_plist(manifest)
    return {entry['NSPrivacyAccessedAPIType'] for entry in data.get('NSPrivacyAccessedAPITypes', [])}


# Strings that exist only in debug or test code (#if DEBUG, the screenshot fixtures, the billing harness).
DEBUG_MARKERS = (b'ASTER_UI_FIXTURE', b'UIFixtures', b'TunnelDebugView', b'HarnessRun', b'asterlink_harness', b'ASTER_APPEARANCE')

SECRET_PATTERNS = {
    'database url': re.compile(rb'postgres(?:ql)?://[^\s"\']{3,}'),
    'private key': re.compile(rb'-----BEGIN [A-Z ]*PRIVATE KEY-----'),
    'cloud access key': re.compile(rb'AKIA[0-9A-Z]{16}'),
    'bearer-looking token': re.compile(rb'eyJ[A-Za-z0-9_-]{16,}\.eyJ[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{8,}'),
    'api secret assignment': re.compile(rb'(?i)(?:secret|passwd|password|api[_-]?key)["\']?\s*[:=]\s*["\'][^"\'\s]{8,}["\']'),
}


def secret_findings(root: Path) -> list:
    """(file, kind) for every secret-looking string in a tree of files. Binaries are read raw."""
    found = []
    for path in sorted(root.rglob('*')):
        if not path.is_file():
            continue
        data = path.read_bytes()
        for kind, pattern in SECRET_PATTERNS.items():
            if pattern.search(data):
                found.append((str(path.relative_to(root)), kind))
    return found


# ---------------------------------------------------------------- provisioning profiles

def profile_contents(profile: Path) -> dict:
    """The decoded plist of a .mobileprovision (CMS-signed; `security cms` verifies and strips)."""
    decoded = subprocess.run(['security', 'cms', '-D', '-i', str(profile)], capture_output=True)
    if decoded.returncode != 0:
        raise ReleaseError('%s is not a readable provisioning profile: %s' % (profile, decoded.stderr.decode(errors='replace').strip()))
    return plistlib.loads(decoded.stdout)


def check_profile(profile: Path, bundle_id: str, today=None) -> dict:
    """Returns the profile and raises ReleaseError unless it can sign `bundle_id` with the tunnel capability."""
    content = profile_contents(profile)
    entitlements = content.get('Entitlements', {})
    app_id = entitlements.get('application-identifier', '')
    teams = content.get('TeamIdentifier', [])
    if not teams or not app_id.startswith(teams[0] + '.'):
        raise ReleaseError('%s has no team-prefixed application-identifier' % profile.name)
    pattern = app_id[len(teams[0]) + 1:]
    exact = pattern == bundle_id
    wildcard = pattern.endswith('*') and bundle_id.startswith(pattern[:-1])
    if not (exact or wildcard):
        raise ReleaseError('%s is for %s, not for %s' % (profile.name, pattern, bundle_id))
    if NETWORK_EXTENSION_VALUE not in entitlements.get(NETWORK_EXTENSION_KEY, []):
        raise ReleaseError('%s does not carry the Network Extension (packet-tunnel-provider) capability: enable it on the App ID, '
                           'then regenerate the profile' % profile.name)
    expires = content.get('ExpirationDate')
    now = today or datetime.datetime.now(datetime.timezone.utc).replace(tzinfo=None)
    if expires and expires.replace(tzinfo=None) < now:
        raise ReleaseError('%s expired on %s' % (profile.name, expires))
    return content


# ---------------------------------------------------------------- re-signing

def default_entitlements(which: str) -> dict:
    return read_plist(IOS / 'Config' / ('App.entitlements' if which == 'app' else 'Tunnel.entitlements'))


def rewrite_bundle_ids(app: Path, prefix: str):
    """Moves the app and its extension to `<prefix>.asterlink` / `<prefix>.asterlink.tunnel`. The app derives the
    extension's id from its own (TunnelController.providerBundleID), so nothing else needs to change."""
    for bundle, suffix in ((app, 'asterlink'), (app / EXTENSION_PATH, 'asterlink.tunnel')):
        info = read_plist(bundle / 'Info.plist')
        info['CFBundleIdentifier'] = '%s.%s' % (prefix, suffix)
        write_plist(bundle / 'Info.plist', info)


def _codesign(target: Path, identity: str, entitlements: dict, workdir: Path, keychain=None):
    plist = workdir / (target.name + '.entitlements.plist')
    with open(plist, 'wb') as handle:
        plistlib.dump(entitlements, handle)
    cmd = ['codesign', '--force', '--sign', identity, '--entitlements', plist, '--timestamp=none', '--generate-entitlement-der']
    if keychain:
        cmd += ['--keychain', keychain]
    run(cmd + [target])


def resign(ipa: Path, output: Path, identity: str, app_profile: Path = None, tunnel_profile: Path = None, bundle_prefix: str = None,
           keychain: str = None) -> dict:
    """Re-signs an unsigned IPA. With both profiles the entitlements are the profiles' own (what Apple will
    accept); without them (structure test only, identity '-') the repository's entitlement files are used and
    the result will not install on a device."""
    if bool(app_profile) != bool(tunnel_profile):
        raise ReleaseError('give both profiles (app and tunnel extension) or neither')
    if not app_profile and identity != '-':
        raise ReleaseError('a real identity needs the two provisioning profiles; only the ad-hoc identity "-" signs without them')
    with tempfile.TemporaryDirectory(prefix='asterlink-resign-') as scratch:
        scratch = Path(scratch)
        root = scratch / 'ipa'
        root.mkdir()
        app = unpack_ipa(ipa, root)
        extension = app / EXTENSION_PATH
        if bundle_prefix:
            if not re.fullmatch(r'[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+', bundle_prefix):
                raise ReleaseError('bundle prefix must be a reverse-DNS name such as cn.example')
            rewrite_bundle_ids(app, bundle_prefix)
        app_id, tunnel_id = bundle_ids(app)
        if not tunnel_id.startswith(app_id + '.'):
            raise ReleaseError('the extension id %s must extend the app id %s' % (tunnel_id, app_id))
        if app_profile:
            app_content = check_profile(app_profile, app_id)
            tunnel_content = check_profile(tunnel_profile, tunnel_id)
            if app_content['TeamIdentifier'] != tunnel_content['TeamIdentifier']:
                raise ReleaseError('the two profiles belong to different teams')
            app_entitlements, tunnel_entitlements = app_content['Entitlements'], tunnel_content['Entitlements']
        else:
            app_content = tunnel_content = None
            app_entitlements, tunnel_entitlements = default_entitlements('app'), default_entitlements('tunnel')
        for bundle in (app, extension):
            for stale in (bundle / '_CodeSignature', bundle / 'embedded.mobileprovision'):
                if stale.is_dir():
                    shutil.rmtree(stale)
                elif stale.exists():
                    stale.unlink()
        if app_profile:
            shutil.copy(app_profile, app / 'embedded.mobileprovision')
            shutil.copy(tunnel_profile, extension / 'embedded.mobileprovision')
        # The extension first: signing the app seals the extension's signature into its resources.
        _codesign(extension, identity, tunnel_entitlements, scratch, keychain)
        _codesign(app, identity, app_entitlements, scratch, keychain)
        verify_signed_app(app)
        pack_ipa(root, output)
    return {'app_id': app_id, 'tunnel_id': tunnel_id, 'identity': identity,
            'team': (app_content or {}).get('TeamIdentifier', [None])[0],
            'profile_kind': profile_kind(app_content) if app_content else 'none (structure test)',
            'profile_expires': str((app_content or {}).get('ExpirationDate', '')) or None}


def profile_kind(content: dict) -> str:
    if content.get('ProvisionsAllDevices'):
        return 'enterprise (in-house)'
    entitlements = content.get('Entitlements', {})
    if entitlements.get('get-task-allow'):
        return 'development'
    return 'ad hoc' if content.get('ProvisionedDevices') else 'app store'


def signed_entitlements(bundle: Path) -> dict:
    out = subprocess.run(['codesign', '-d', '--entitlements', ':-', str(bundle)], capture_output=True)
    if out.returncode != 0 or not out.stdout.strip():
        raise ReleaseError('no entitlements in the signature of %s: %s' % (bundle.name, out.stderr.decode(errors='replace').strip()))
    return plistlib.loads(out.stdout)


def verify_signed_app(app: Path):
    run(['codesign', '--verify', '--deep', '--strict', app])
    for bundle in (app, app / EXTENSION_PATH):
        entitlements = signed_entitlements(bundle)
        if NETWORK_EXTENSION_VALUE not in entitlements.get(NETWORK_EXTENSION_KEY, []):
            raise ReleaseError('%s lost the Network Extension entitlement while signing' % bundle.name)

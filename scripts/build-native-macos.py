#!/usr/bin/env python3
"""Build, assemble, sign and verify AsterLink.app (App + Helper + Go Core).

    python3 scripts/build-native-macos.py --configuration Debug

Reads only apps/macos/.env (public build parameters, strictly validated). It never
reads backend/.env or any database/node secret. Release additionally requires a real
signing identity and Team ID from the environment; notarization is stage 5 and is
refused here rather than faked.
"""
from pathlib import Path
import argparse
import hashlib
import json
import os
import plistlib
import re
import shutil
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parent))
import native_env  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
APP_ID = 'com.asterlink.vpn'
HELPER_ID = 'com.asterlink.vpn.helper'
CORE_ID = 'com.asterlink.vpn.core'
MIN_MACOS = '15.0'


def run(command, **kwargs):
    print('+', ' '.join(str(c) for c in command), flush=True)
    return subprocess.run(command, check=True, **kwargs)


def capture(command, **kwargs):
    return subprocess.run(command, check=True, capture_output=True, text=True, **kwargs).stdout.strip()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open('rb') as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def mihomo_revision() -> str:
    line = capture(['git', 'ls-files', '-s', 'native/third_party/Clash.Meta'], cwd=ROOT)
    match = re.match(r'160000 ([0-9a-f]{40}) 0\t', line)
    if not match:
        raise SystemExit('Pinned Mihomo gitlink not found')
    return match.group(1)


def write_plist(path: Path, data: dict):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('wb') as handle:
        plistlib.dump(data, handle, fmt=plistlib.FMT_XML)


def codesign(target: Path, identifier: str, identity: str, release: bool):
    command = ['codesign', '--force', '--sign', identity, '--identifier', identifier]
    if release:
        command += ['--options', 'runtime', '--timestamp']
    run(command + [str(target)])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--configuration', choices=['Debug', 'Release'], default='Debug')
    parser.add_argument('--env-file', type=Path, default=ROOT / 'apps/macos/.env')
    parser.add_argument('--run-id', default=time.strftime('%Y%m%d-%H%M%S'))
    parser.add_argument('--app-version', default='0.1.0')
    parser.add_argument('--notarize', action='store_true', help='stage 5; refused until implemented')
    args = parser.parse_args()
    release = args.configuration == 'Release'

    if args.notarize:
        raise SystemExit('BLOCKED: notarization is a stage-5 deliverable and is not implemented')
    if not re.fullmatch(r'\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?', args.app_version):
        raise SystemExit('Invalid --app-version')

    env_file = args.env_file
    if not env_file.exists() and not release:
        env_file = ROOT / 'apps/macos/.env.example'  # public sample values; Debug only
    values = native_env.load_env_file(env_file)
    if release and values['APP_ENV'] != 'release':
        raise SystemExit('Release builds require APP_ENV=release')
    if not release and values['APP_ENV'] != 'development':
        raise SystemExit('Debug builds require APP_ENV=development')

    identity = os.environ.get('CODESIGN_IDENTITY', '-')
    team = os.environ.get('ASTERLINK_TEAM_ID', '')
    if release and (identity == '-' or not re.fullmatch(r'[A-Z0-9]{10}', team)):
        raise SystemExit('BLOCKED: Release needs CODESIGN_IDENTITY and ASTERLINK_TEAM_ID (never ad-hoc)')
    if team and not re.fullmatch(r'[A-Z0-9]{10}', team):
        raise SystemExit('Invalid ASTERLINK_TEAM_ID')

    out = ROOT / 'artifacts/native-macos' / args.run_id
    if out.exists():
        raise SystemExit('Run directory exists; use a new --run-id to preserve evidence: ' + str(out))
    out.mkdir(parents=True)
    revision = mihomo_revision()
    config = args.configuration.lower()

    # 1. Go Core (CGO off, arm64, trimmed). The TUN proof of concept is a separate module and is not built here.
    core = out / 'asterlink-core'
    ldflags = '-s -w -X main.version=%s -X main.mihomoRevision=%s' % (args.app_version, revision)
    run(['go', 'build', '-trimpath', '-ldflags', ldflags, '-o', str(core), './cmd/asterlink-core'],
        cwd=ROOT / 'native/core', env=dict(os.environ, CGO_ENABLED='0', GOOS='darwin', GOARCH='arm64'))

    # 2. Swift products. The Helper carries its identity/channel in an embedded Info.plist.
    macos = ROOT / 'apps/macos'
    helper_info = out / 'helper-Info.plist'
    write_plist(helper_info, {
        'CFBundleIdentifier': HELPER_ID, 'CFBundleName': 'AsterLinkHelper', 'CFBundleVersion': args.app_version,
        'CFBundleShortVersionString': args.app_version, 'CFBundleInfoDictionaryVersion': '6.0',
        'AsterLinkChannel': values['APP_ENV'], 'AsterLinkTeamID': team,
    })
    run(['swift', 'build', '-c', config, '--product', 'AsterLinkHelper',
         '-Xlinker', '-sectcreate', '-Xlinker', '__TEXT', '-Xlinker', '__info_plist', '-Xlinker', str(helper_info)], cwd=macos)
    run(['swift', 'build', '-c', config, '--product', 'AsterLink'], cwd=macos)
    bin_path = Path(capture(['swift', 'build', '-c', config, '--show-bin-path'], cwd=macos))

    # 3. Bundle layout (SMAppService daemon plist lives inside the bundle).
    app = out / 'AsterLink.app'
    contents = app / 'Contents'
    (contents / 'MacOS').mkdir(parents=True)
    (contents / 'Resources').mkdir()
    shutil.copy2(bin_path / 'AsterLink', contents / 'MacOS/AsterLink')
    shutil.copy2(bin_path / 'AsterLinkHelper', contents / 'MacOS/AsterLinkHelper')
    shutil.copy2(core, contents / 'Resources/asterlink-core')
    write_plist(contents / 'Info.plist', {
        'CFBundleIdentifier': APP_ID, 'CFBundleName': 'AsterLink', 'CFBundleDisplayName': 'AsterLink',
        'CFBundleExecutable': 'AsterLink', 'CFBundlePackageType': 'APPL', 'CFBundleVersion': args.app_version,
        'CFBundleShortVersionString': args.app_version, 'LSMinimumSystemVersion': MIN_MACOS,
        'LSApplicationCategoryType': 'public.app-category.utilities', 'NSPrincipalClass': 'NSApplication',
        'AsterLinkChannel': values['APP_ENV'], 'AsterLinkTeamID': team,
        'AsterLinkAPIBase': values['API_BASE_URL'], 'AsterLinkWebsiteBase': values['WEBSITE_BASE_URL'],
    })
    write_plist(contents / 'Library/LaunchDaemons' / (HELPER_ID + '.plist'), {
        'Label': HELPER_ID, 'BundleProgram': 'Contents/MacOS/AsterLinkHelper',
        'MachServices': {HELPER_ID: True}, 'AssociatedBundleIdentifiers': [APP_ID],
    })
    core_hash = sha256(contents / 'Resources/asterlink-core')
    (contents / 'Resources/worker-manifest.json').write_text(json.dumps({'version': args.app_version, 'sha256': core_hash}, indent=2) + '\n')

    # 4. Sign inside-out, then verify. Manifest is written before signing so it is sealed.
    codesign(contents / 'Resources/asterlink-core', CORE_ID, identity, release)
    codesign(contents / 'MacOS/AsterLinkHelper', HELPER_ID, identity, release)
    # Signing rewrites the core's bytes; refresh the manifest hash before the bundle seal.
    core_hash = sha256(contents / 'Resources/asterlink-core')
    (contents / 'Resources/worker-manifest.json').write_text(json.dumps({'version': args.app_version, 'sha256': core_hash}, indent=2) + '\n')
    codesign(contents / 'MacOS/AsterLink', APP_ID, identity, release)
    codesign(app, APP_ID, identity, release)
    run(['codesign', '--verify', '--deep', '--strict', '--verbose=2', str(app)])

    for binary in (contents / 'MacOS/AsterLink', contents / 'MacOS/AsterLinkHelper', contents / 'Resources/asterlink-core'):
        result = subprocess.run(['codesign', '-d', '--entitlements', '-', str(binary)], capture_output=True, text=True)
        if 'get-task-allow' in result.stdout + result.stderr:
            raise SystemExit('get-task-allow found in ' + binary.name)

    manifest = {
        'configuration': args.configuration, 'app_version': args.app_version, 'mihomo_revision': revision,
        'channel': values['APP_ENV'], 'signing': 'developer-id' if release else ('ad-hoc' if identity == '-' else 'development-identity'),
        'sha256': {p.name: sha256(p) for p in (contents / 'MacOS/AsterLink', contents / 'MacOS/AsterLinkHelper', contents / 'Resources/asterlink-core')},
        'notarized': False,
    }
    (out / 'build-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print('BUILT', app)
    print(json.dumps(manifest, indent=2))


if __name__ == '__main__':
    main()

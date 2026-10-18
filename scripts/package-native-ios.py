#!/usr/bin/env python3
"""Builds the UNSIGNED iOS release package (handoff_app_ios.md round 6).

    python3 scripts/package-native-ios.py --channel internal [--version 0.1.0] [--build-number 1]

There is no Apple Developer team yet, and nothing here needs one. The run
  1. checks the env file says APP_ENV=release over HTTPS (default apps/ios/.env.release.example),
  2. binds the Go Core + pinned Mihomo into AsterCore.xcframework if that is not built yet,
  3. archives the app and its packet tunnel extension for a real iPhone (generic/platform=iOS, Release,
     CODE_SIGNING_ALLOWED=NO: stripped, optimised, no debug code, no signature),
  4. zips Payload/AsterLink.app into AsterLink-<version>-ios-unsigned.ipa,
  5. writes artifacts/native-ios/release/ (or --release-dir):
       AsterLink-<version>-ios-unsigned.ipa   what scripts/ios/resign-ipa.py signs once you have a team
       THIRD_PARTY_LICENSES.txt               Go modules linked into the Core
       dependencies.cdx.json                  CycloneDX-shaped inventory (Go modules, Mihomo, Apple frameworks)
       SOURCE.txt + two source archives       the GPL corresponding source (Mihomo + this repository)
       release-info.json                      version, hashes, bundle ids, Core identity, open gates
       SHA256SUMS                             of every file above
     and, beside it in release-private/ (NOT for publishing): the dSYMs (crash symbolication).

The package is always channel "internal" with trusted_release=false and signed=false; --channel release is
refused until the open gates in release-info.json are closed. Then run
    python3 scripts/test-native-ios.py --suite release
to audit it.

Exit codes: 0 = packaged, 1 = failed, 3 = BLOCKED (a prerequisite is missing).
"""
import argparse
import datetime
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
IOS = ROOT / 'apps' / 'ios'
sys.path.insert(0, str(ROOT / 'scripts'))
import ios_release  # noqa: E402
import native_env  # noqa: E402
import windows_release  # noqa: E402  (platform-neutral helpers: source archives, SHA256SUMS)

EXIT_OK, EXIT_FAIL, EXIT_BLOCKED = 0, 1, 3
RUNTIME = ROOT / '.runtime' / 'ios'
DEFAULT_ENV = IOS / '.env.release.example'
LOCK = json.loads((IOS / 'toolchain.lock.json').read_text(encoding='utf-8'))

OPEN_GATES = [
    'signing: the IPA is unsigned. A paid Apple Developer Program team, two App IDs with Network Extension (Packet Tunnel) and provisioning profiles are needed, then scripts/ios/resign-ipa.py (docs/ios-release.md)',
    'device acceptance: D-T01..D-T09 and D-CYCLES (round 2), device-billing (round 4), D-L01..D-L07 and D-MEM (round 5) are all BLOCKED until a signed build runs on an iPhone',
    'R01/R02/R04: clean install, upgrade over an installed copy and a second device/iOS combination, all on real iPhones',
    'B04-live: a public managed node with authoritative node_reported metering (client_reported is development metering)',
    'legal review: Mihomo GPL-3.0 obligations for the chosen distribution channel, and the go-lzo (GPL-2.0) combination',
    'export compliance: ITSAppUsesNonExemptEncryption is true; the App Store Connect questionnaire / self-classification is still to be answered',
    'placeholder art: the app icon and launch mark are vectors drawn for development; the real logo is not supplied',
    'website: /privacy and /terms pages do not exist yet (required before any store submission)',
    'bundle id prefix com.asterlink.dev is a placeholder; re-sign with --bundle-prefix <your registered prefix>',
]


class Blocked(Exception):
    pass


def build_module():
    spec = importlib.util.spec_from_file_location('build_native_ios', ROOT / 'scripts' / 'build-native-ios.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def xcode_facts():
    out = subprocess.run(['xcodebuild', '-version'], capture_output=True, text=True, check=True).stdout.split()
    sdk = subprocess.run(['xcrun', '--sdk', 'iphoneos', '--show-sdk-version'], capture_output=True, text=True, check=True).stdout.strip()
    return {'xcode': ' '.join(out[1:4]), 'iphoneos_sdk': sdk}


def base_setting(name):
    match = re.search(r'^%s\s*=\s*(\S+)' % name, (IOS / 'Config' / 'Base.xcconfig').read_text(), re.M)
    if not match:
        raise RuntimeError('Base.xcconfig has no %s' % name)
    return match.group(1)


def dependency_inventory(version, commit, mihomo_commit):
    sys.path.insert(0, str(ROOT / 'scripts'))
    import collect_licenses
    components = []
    for path, (ver, _) in sorted(collect_licenses.linked_modules('ios', 'arm64').items()):
        if path.startswith('asterlink/'):
            continue
        components.append({'type': 'library', 'name': path, 'version': ver or 'local',
                           'purl': 'pkg:golang/%s@%s' % (path, ver) if ver else None, 'scope': 'required',
                           'origin': 'AsterCore (app and tunnel extension)'})
    components.append({'type': 'application', 'name': 'Mihomo (Clash.Meta fork)', 'version': mihomo_commit,
                       'origin': 'embedded in AsterCore', 'license': 'GPL-3.0'})
    for framework in ('Security', 'CoreFoundation', 'NetworkExtension', 'Network', 'UserNotifications', 'libresolv'):
        components.append({'type': 'framework', 'name': framework, 'version': 'iOS system', 'origin': 'Apple SDK, not shipped'})
    return {'bomFormat': 'CycloneDX', 'specVersion': '1.5', 'version': 1,
            'metadata': {'component': {'type': 'application', 'name': 'AsterLink for iOS', 'version': version},
                         'properties': [{'name': 'source_commit', 'value': commit}]},
            'components': [{k: v for k, v in c.items() if v is not None} for c in components]}


def write_source_notice(out, commit, mihomo_commit, dirty, version):
    import prepare_mihomo
    patch = prepare_mihomo.prepare(check=True)
    (out / 'SOURCE.txt').write_text(
        'AsterLink for iOS %s - corresponding source\n\n'
        'The app and its tunnel extension each embed AsterCore, which contains Mihomo (Clash.Meta fork, GNU GPL v3).\n'
        'This repository commit:  %s%s\nMihomo commit:           %s\n\n'
        'The two tar.gz archives next to this file are `git archive` outputs of exactly these commits.\n'
        'The Mihomo archive is the unchanged upstream base; the AsterLink archive includes the reviewed patch.\n'
        'Mihomo patch: %s (SHA-256 %s).\n'
        'To rebuild: unpack both, place the mihomo tree at native/third_party/Clash.Meta, then at repository root run\n'
        '  python3 scripts/prepare_mihomo.py\n'
        '  python3 scripts/package-native-ios.py --channel internal\n'
        'The Go, x/mobile and Xcode versions are pinned in apps/ios/toolchain.lock.json and native/core/go.sum;\n'
        'the build steps are in docs/ios-release.md.\n'
        % (version, commit, ' (WORKING TREE HAD UNCOMMITTED CHANGES)' if dirty else '', mihomo_commit, patch['id'], patch['patch_sha256']),
        encoding='utf-8')


def package(args) -> int:
    if sys.platform != 'darwin':
        raise Blocked('the iOS package is built on a Mac with Xcode')
    env_path = Path(args.env_file).resolve() if args.env_file else DEFAULT_ENV
    if not env_path.is_file():
        raise Blocked('env file %s does not exist' % env_path)
    values = native_env.load_env_file(env_path)
    if values['APP_ENV'] != 'release':
        raise RuntimeError('a release package needs APP_ENV=release; %s says %s' % (env_path.name, values['APP_ENV']))
    version = args.version or base_setting('MARKETING_VERSION')
    prefix = args.bundle_prefix or base_setting('ASTER_BUNDLE_PREFIX')
    if not re.fullmatch(r'\d+\.\d+\.\d+', version) or not re.fullmatch(r'\d+', args.build_number):
        raise RuntimeError('version must look like 1.2.3 and the build number must be an integer')

    os.environ['ASTERLINK_IOS_ENV_FILE'] = str(env_path)
    build = build_module()
    build.write_xcconfig()
    framework_info = IOS / 'Frameworks' / 'core-build.json'
    if not framework_info.is_file():
        build.bind(version)
    core = json.loads(framework_info.read_text())
    if core.get('harness'):
        raise RuntimeError('Frameworks/core-build.json belongs to the test harness build; rebuild the production Core')

    archive = RUNTIME / 'release' / 'AsterLink.xcarchive'
    shutil.rmtree(archive.parent, ignore_errors=True)
    archive.parent.mkdir(parents=True)
    subprocess.run(['xcodebuild', 'archive', '-project', str(IOS / 'AsterLink.xcodeproj'), '-scheme', 'AsterLink', '-configuration', 'Release',
                    '-destination', 'generic/platform=iOS', '-archivePath', str(archive), '-derivedDataPath', str(RUNTIME / 'DerivedDataRelease'),
                    'CODE_SIGNING_ALLOWED=NO', 'MARKETING_VERSION=' + version, 'CURRENT_PROJECT_VERSION=' + args.build_number,
                    'ASTER_BUNDLE_PREFIX=' + prefix], check=True)
    built_app = archive / 'Products' / 'Applications' / 'AsterLink.app'
    if not built_app.is_dir():
        raise RuntimeError('the archive holds no AsterLink.app')

    out = Path(args.release_dir).resolve() if args.release_dir else ROOT / 'artifacts' / 'native-ios' / 'release'
    private = out.parent / (out.name + '-private')
    for directory in (out, private):
        shutil.rmtree(directory, ignore_errors=True)
        directory.mkdir(parents=True)
    stage = RUNTIME / 'release' / 'ipa'
    (stage / 'Payload').mkdir(parents=True)
    subprocess.run(['ditto', str(built_app), str(stage / 'Payload' / 'AsterLink.app')], check=True)
    ipa = out / ('AsterLink-%s-ios-unsigned.ipa' % version)
    ios_release.pack_ipa(stage, ipa)
    app_id, tunnel_id = ios_release.bundle_ids(stage / 'Payload' / 'AsterLink.app')
    print('IPA %s (%.1f MiB) %s / %s' % (ipa.name, ipa.stat().st_size / 2 ** 20, app_id, tunnel_id))
    dsyms = archive / 'dSYMs'
    if dsyms.is_dir():
        subprocess.run(['ditto', '-c', '-k', '--keepParent', str(dsyms), str(private / ('dSYMs-%s.zip' % version))], check=True)

    import collect_licenses
    text, missing, modules = collect_licenses.collect('ios', 'arm64')
    (out / 'THIRD_PARTY_LICENSES.txt').write_text(text, encoding='utf-8')
    commit, dirty = windows_release.source_state()
    archives, mihomo_commit = ([], core['mihomo_revision'])
    if not args.skip_source_archives:
        archives, mihomo_commit = windows_release.source_archives(out, commit)
    (out / 'dependencies.cdx.json').write_text(json.dumps(dependency_inventory(version, commit, mihomo_commit), indent=2) + '\n')
    write_source_notice(out, commit, mihomo_commit, dirty, version)
    info = {
        'format_version': 1, 'product': 'AsterLink for iOS', 'version': version, 'build': int(args.build_number), 'channel': 'internal',
        'app_env': values['APP_ENV'], 'api_base': values['API_BASE_URL'], 'website': values['WEBSITE_BASE_URL'],
        'built_at': datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0).isoformat(),
        'ipa': {'file': ipa.name, 'bytes': ipa.stat().st_size, 'sha256': ios_release.sha256(ipa)},
        'bundle_ids': {'app': app_id, 'tunnel': tunnel_id, 'placeholder_prefix': prefix == 'com.asterlink.dev'},
        'signed': False,
        'signing': {'state': 'unsigned', 'how': 'python3 scripts/ios/resign-ipa.py <this ipa> --identity ... --app-profile ... --tunnel-profile ... (docs/ios-release.md)'},
        'trusted_release': False,
        'minimum_ios': LOCK['ios']['deployment_target'], 'toolchain': xcode_facts(),
        'core': {k: core.get(k) for k in ('core_version', 'mihomo_revision', 'x_mobile')}, 'mihomo_patch': core.get('mihomo_patch'),
        'core_slices': core.get('slices'),
        'source_commit': commit, 'source_dirty': dirty, 'mihomo_commit': mihomo_commit,
        'third_party_go_modules': modules, 'go_modules_without_license_file': missing,
        'source_archives': [a.name for a in archives], 'open_gates': OPEN_GATES,
    }
    (out / 'release-info.json').write_text(json.dumps(info, indent=2) + '\n')
    windows_release.write_checksums(out)
    problems = windows_release.verify_checksums(out)
    if problems:
        raise RuntimeError('SHA256SUMS does not verify: ' + '; '.join(problems))
    print('\nrelease folder: %s (unsigned, trusted_release=false)\nprivate (do not publish): %s' % (out, private))
    return EXIT_OK


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--channel', choices=('internal', 'release'), required=True, help='always internal until the open gates are closed')
    parser.add_argument('--version', help='default: MARKETING_VERSION in apps/ios/Config/Base.xcconfig')
    parser.add_argument('--build-number', default='1', help='CFBundleVersion; must grow with every upload to a store')
    parser.add_argument('--bundle-prefix', help='default: ASTER_BUNDLE_PREFIX in Base.xcconfig (a placeholder). You can also change it later with resign-ipa.py')
    parser.add_argument('--env-file', help='default: apps/ios/.env.release.example')
    parser.add_argument('--release-dir', help='default artifacts/native-ios/release')
    parser.add_argument('--skip-source-archives', action='store_true', help='faster local iteration; never for a published release')
    args = parser.parse_args()
    if args.channel != 'internal':
        print('FAIL: only --channel internal exists until the gates in release-info.json are closed (trusted_release stays false)')
        return EXIT_FAIL
    try:
        return package(args)
    except Blocked as reason:
        print('BLOCKED: %s' % reason)
        return EXIT_BLOCKED
    except (RuntimeError, subprocess.CalledProcessError, native_env.EnvError, OSError, ios_release.ReleaseError) as error:
        print('FAIL: %s' % error)
        return EXIT_FAIL


if __name__ == '__main__':
    sys.exit(main())

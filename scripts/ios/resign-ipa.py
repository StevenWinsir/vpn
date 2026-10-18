#!/usr/bin/env python3
"""Re-sign the unsigned AsterLink IPA with your own Apple team (handoff_app_ios.md round 6).

The IPA that scripts/package-native-ios.py builds is unsigned, because signing needs a paid Apple
Developer Program team and its certificate, neither of which belongs in this repository. Once you have
them, this tool turns that file into an installable one without rebuilding:

    python3 scripts/ios/resign-ipa.py AsterLink-0.1.0-ios-unsigned.ipa \\
        --identity "Apple Distribution: Your Name (TEAMID1234)" \\
        --app-profile AsterLink_AdHoc.mobileprovision \\
        --tunnel-profile AsterLinkTunnel_AdHoc.mobileprovision \\
        --bundle-prefix cn.example \\
        --ipa-url https://downloads.example.cn/ios/AsterLink-0.1.0-ios.ipa

What you need first (details and screenshots-free steps in docs/ios-release.md):
  * a paid Apple Developer Program membership and a signing certificate in your login Keychain
    ("Apple Distribution" for Ad Hoc / App Store, "iPhone Distribution" for Enterprise, or
    "Apple Development" for testing on your own devices);
  * two App IDs, <prefix>.asterlink and <prefix>.asterlink.tunnel, both with the Network Extension
    capability (Packet Tunnel) enabled;
  * for each App ID a provisioning profile of the same kind (Ad Hoc profiles list the iPhones' UDIDs).

--bundle-prefix replaces the placeholder prefix baked into the IPA (com.asterlink.dev) in both Info.plists;
the app finds its extension as "<its own id>.tunnel", so nothing else changes. The entitlements come from
the profiles (Apple's own answer to "what may this app do"), the extension is signed first, and the
result is verified with codesign before the output file is written.

With --ipa-url the tool also writes manifest.plist next to the output: the file iOS needs to install an
IPA from a web page (itms-services). Upload both files over HTTPS, then open
itms-services://?action=download-manifest&url=<https url of manifest.plist> on the iPhone, which is what
the website's /download page does once the IOS_* lines printed below are in frontend/.env. Over-the-air
install only works for Ad Hoc (registered devices) and Enterprise signing.

--identity - signs ad hoc with no profiles. That output installs nowhere; it exists to test this tool.

Exit codes: 0 = signed, 1 = failed, 3 = BLOCKED (a prerequisite is missing).
"""
import argparse
import hashlib
import plistlib
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import ios_release  # noqa: E402

EXIT_OK, EXIT_FAIL, EXIT_BLOCKED = 0, 1, 3


def write_manifest(path: Path, ipa_url: str, bundle_id: str, version: str):
    manifest = {'items': [{
        'assets': [{'kind': 'software-package', 'url': ipa_url}],
        'metadata': {'bundle-identifier': bundle_id, 'bundle-version': version, 'kind': 'software', 'title': 'AsterLink'},
    }]}
    with open(path, 'wb') as handle:
        plistlib.dump(manifest, handle)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('ipa', type=Path, help='the unsigned IPA from package-native-ios.py')
    parser.add_argument('--identity', required=True, help='codesign identity: certificate name or SHA-1, or - for an ad hoc structure test')
    parser.add_argument('--app-profile', type=Path)
    parser.add_argument('--tunnel-profile', type=Path)
    parser.add_argument('--bundle-prefix', help='reverse-DNS prefix registered in your team, for example cn.example')
    parser.add_argument('--keychain', help='keychain holding the identity (default: the search list)')
    parser.add_argument('--output', type=Path, help='default: <name>-signed.ipa beside the input')
    parser.add_argument('--ipa-url', help='https URL the signed IPA will be published at; also writes manifest.plist')
    args = parser.parse_args()
    if not args.ipa.is_file():
        print('FAIL: %s does not exist' % args.ipa)
        return EXIT_FAIL
    for profile in (args.app_profile, args.tunnel_profile):
        if profile and not profile.is_file():
            print('BLOCKED: %s does not exist' % profile)
            return EXIT_BLOCKED
    if args.ipa_url and not args.ipa_url.startswith('https://'):
        print('FAIL: --ipa-url must be an https URL (iOS refuses over-the-air installs over plain http)')
        return EXIT_FAIL
    name = args.ipa.name.replace('-unsigned', '')
    output = args.output or args.ipa.with_name(name[:-4] + '-signed.ipa' if name == args.ipa.name else name)
    try:
        result = ios_release.resign(args.ipa, output, args.identity, args.app_profile, args.tunnel_profile, args.bundle_prefix, args.keychain)
    except ios_release.ReleaseError as error:
        print('FAIL: %s' % error)
        return EXIT_FAIL
    except Exception as error:  # a codesign or unzip failure carries its own message
        print('FAIL: %s' % (getattr(error, 'stderr', None) or error))
        return EXIT_FAIL
    digest = ios_release.sha256(output)
    print('signed %s' % output)
    print('  app %s, extension %s, team %s, profile kind: %s' % (result['app_id'], result['tunnel_id'], result['team'], result['profile_kind']))
    print('  sha256 %s, %d bytes' % (digest, output.stat().st_size))
    if args.identity == '-':
        print('  NOTE: ad hoc, no profiles: this file cannot be installed on an iPhone.')
        return EXIT_OK
    if args.ipa_url:
        import tempfile
        with tempfile.TemporaryDirectory() as scratch:
            app = ios_release.unpack_ipa(output, Path(scratch))
            version = ios_release.read_plist(app / 'Info.plist')['CFBundleShortVersionString']
        manifest = output.with_name('manifest.plist')
        write_manifest(manifest, args.ipa_url, result['app_id'], version)
        base = args.ipa_url.rsplit('/', 1)[0] + '/manifest.plist'
        print('  wrote %s (upload it next to the IPA)' % manifest)
        print('\nfrontend/.env lines for the /download page:')
        print('IOS_IPA_URL=%s' % args.ipa_url)
        print('IOS_MANIFEST_URL=%s' % base)
        print('IOS_IPA_VERSION=%s' % version)
        print('IOS_IPA_SHA256=%s' % digest)
        print('IOS_IPA_SIZE_BYTES=%d' % output.stat().st_size)
    return EXIT_OK


if __name__ == '__main__':
    sys.exit(main())

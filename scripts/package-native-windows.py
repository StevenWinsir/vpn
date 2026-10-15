#!/usr/bin/env python3
"""Builds the INTERNAL-channel stage tree of the Windows client (round 3 development installer input).

    py -3 scripts/package-native-windows.py --channel internal --stage artifacts/native-windows/stage

The stage has the same layout as the installed product (apps/windows/AsterLink.Shared/Service/InstallLayout.cs):

    app/      GUI, self-contained win-x64
    service/  Windows Service + installer verbs, self-contained win-x64
    core/     asterlink-core.exe (windows/amd64, GOAMD64=v1, CGO off, Wintun embedded)
    build-config.json   the three public build keys from apps/windows/.env (or .env.example)
    manifest.json       SHA-256 of every file above, channel "internal"

Round 8 adds, on top of the stage:

    --sign none|test|thumbprint|pfx   Authenticode-sign our own binaries BEFORE the manifest is written (so the manifest
                                      hashes the signed files). "test" uses a self-signed certificate: it proves the pipeline,
                                      it is trusted by nobody. thumbprint/pfx need the secrets from the environment
                                      (scripts/windows/sign-files.ps1); signing needs Windows.
    --installer                       build AsterLink-Setup-x64-<version>.exe with Inno Setup (apps/windows/Packaging/setup.iss)
    --release-dir <dir>               collect Setup.exe, SHA256SUMS, release-info.json, dependency inventory, third-party
                                      notices, SOURCE.txt and the GPL source archives

The channel is always "internal": the service refuses a "release" manifest until it verifies Authenticode itself, and a
test signature must never be mistaken for one. `--channel release` is therefore still refused.
The .NET publish needs Windows; --no-dotnet and --no-core exist so each half can be tested on its own.
"""
import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WINDOWS = ROOT / 'apps' / 'windows'
CORE = ROOT / 'native' / 'core'
sys.path.insert(0, str(ROOT / 'scripts'))
import native_env  # noqa: E402
import windows_release  # noqa: E402

PROTOCOL_VERSION = 1
ROLES = {
    'app/AsterLink.exe': 'gui',
    'service/AsterLink.Service.exe': 'service',
    'core/asterlink-core.exe': 'core',
    'build-config.json': 'config',
}


def version():
    text = (WINDOWS / 'Directory.Build.props').read_text()
    match = re.search(r'<Version>(\d+\.\d+\.\d+)</Version>', text)
    if not match:
        sys.exit('cannot read <Version> from Directory.Build.props')
    return match.group(1)


def run(cmd, **kwargs):
    print('+', ' '.join(str(c) for c in cmd), flush=True)
    subprocess.run(cmd, check=True, **kwargs)


def publish_dotnet(stage):
    dotnet = shutil.which('dotnet')
    if not dotnet:
        sys.exit('dotnet SDK not found')
    for project, target in (('AsterLink.App', 'app'), ('AsterLink.Service', 'service')):
        run([dotnet, 'publish', str(WINDOWS / project), '-c', 'Release', '--self-contained', 'true',
             '-p:RestoreLockedMode=true', '-o', str(stage / target)])


def build_core(stage, ver):
    import prepare_mihomo
    prepare_mihomo.prepare()
    target = stage / 'core'
    target.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOOS='windows', GOARCH='amd64', GOAMD64='v1', CGO_ENABLED='0')
    # The Core reports this version in hello; the service refuses a Core that differs from the manifest.
    run(['go', 'build', '-trimpath', '-ldflags', '-X main.version=' + ver,
         '-o', str(target / 'asterlink-core.exe'), './cmd/asterlink-core'], cwd=CORE, env=env)


def write_build_config(stage):
    source = WINDOWS / '.env'
    if not source.is_file():
        source = WINDOWS / '.env.example'
    values = native_env.load_env_file(source)
    config = {key: values[key] for key in native_env.ALLOWED}
    (stage / 'build-config.json').write_text(json.dumps(config, indent=2) + '\n')
    print('build-config.json from', source.name, '->', config['API_BASE_URL'])


def write_manifest(stage, ver, channel):
    files = []
    for top in ('app', 'service', 'core'):
        for path in sorted((stage / top).rglob('*')):
            if path.is_file():
                files.append(path)
    files.append(stage / 'build-config.json')
    entries = []
    for path in files:
        relative = path.relative_to(stage).as_posix()
        entry = {'path': relative, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'size': path.stat().st_size}
        if relative in ROLES:
            entry['role'] = ROLES[relative]
        entries.append(entry)
    missing = set(ROLES) - {e['path'] for e in entries}
    if missing:
        sys.exit('stage is incomplete, missing: ' + ', '.join(sorted(missing)))
    manifest = {'format_version': 1, 'version': ver, 'channel': channel, 'protocol_version': PROTOCOL_VERSION, 'files': entries}
    (stage / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print('manifest.json: %d files, version %s, channel %s' % (len(entries), ver, channel))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--channel', required=True)
    parser.add_argument('--stage', default=str(ROOT / 'artifacts' / 'native-windows' / 'stage'))
    parser.add_argument('--no-dotnet', action='store_true', help='keep an existing app/ and service/')
    parser.add_argument('--no-core', action='store_true', help='keep an existing core/')
    parser.add_argument('--sign', choices=windows_release.SIGN_MODES, default='none')
    parser.add_argument('--installer', action='store_true')
    parser.add_argument('--release-dir', help='collect the distributable files here (needs --installer)')
    parser.add_argument('--skip-source-archives', action='store_true', help='faster local iteration; never for a published release')
    args = parser.parse_args()

    if args.channel != 'internal':
        sys.exit('only --channel internal exists: the service refuses a release manifest until it verifies Authenticode itself, and no trusted certificate exists yet')
    if args.release_dir and not args.installer:
        sys.exit('--release-dir needs --installer')
    stage = Path(args.stage).resolve()
    ver = version()
    if not (args.no_dotnet and args.no_core):
        shutil.rmtree(stage, ignore_errors=True)
    stage.mkdir(parents=True, exist_ok=True)
    if not args.no_dotnet:
        publish_dotnet(stage)
    if not args.no_core:
        build_core(stage, ver)
    write_build_config(stage)
    # Sign first, hash second: the manifest must describe the files exactly as they will be installed.
    windows_release.sign(windows_release.own_binaries(stage), args.sign)
    write_manifest(stage, ver, args.channel)
    print('stage ready:', stage)
    if args.installer:
        release(args, stage, ver)


def release(args, stage, ver):
    out = Path(args.release_dir).resolve() if args.release_dir else stage.parent / 'installer'
    shutil.rmtree(out, ignore_errors=True)
    out.mkdir(parents=True)
    setup = windows_release.build_installer(stage, out, ver, args.sign)
    commit, dirty = windows_release.source_state()
    shutil.copy2(stage / 'manifest.json', out / 'manifest.json')
    missing, modules = windows_release.write_notices(out)
    inventory = windows_release.dependency_inventory(stage, ver, commit)
    (out / 'dependencies.cdx.json').write_text(json.dumps(inventory, indent=2) + '\n')
    archives = []
    mihomo_commit = 'not archived (--skip-source-archives)'
    if not args.skip_source_archives:
        archives, mihomo_commit = windows_release.source_archives(out, commit)
    windows_release.write_source_notice(out, commit, mihomo_commit, dirty)
    info = {
        'format_version': 1, 'version': ver, 'channel': 'internal', 'installer': setup.name,
        'signing': args.sign,
        # Only a certificate chained to a public root, with a time stamp, counts; a test signature never does.
        'trusted_signature': False,
        'source_commit': commit, 'source_dirty': dirty, 'mihomo_commit': mihomo_commit,
        'third_party_modules': modules, 'modules_without_license_file': missing,
        'source_archives': [a.name for a in archives],
        'open_gates': ['trusted code-signing certificate and time stamp', 'clean Windows 10 and 11 machines (R01/R02)',
                       'managed node with authoritative metering (round 7)', 'Wintun redistribution legal review', 'website operations'],
    }
    (out / 'release-info.json').write_text(json.dumps(info, indent=2) + '\n')
    lines = windows_release.write_checksums(out)
    problems = windows_release.verify_checksums(out)
    if problems:
        sys.exit('SHA256SUMS does not verify: ' + '; '.join(problems))
    print('release folder: %s (%d files, signing=%s, trusted_signature=false)' % (out, len(lines), args.sign))


if __name__ == '__main__':
    main()

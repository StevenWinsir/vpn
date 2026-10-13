#!/usr/bin/env python3
"""Builds the INTERNAL-channel stage tree of the Windows client (round 3 development installer input).

    py -3 scripts/package-native-windows.py --channel internal --stage artifacts/native-windows/stage

The stage has the same layout as the installed product (apps/windows/AsterLink.Shared/Service/InstallLayout.cs):

    app/      GUI, self-contained win-x64
    service/  Windows Service + installer verbs, self-contained win-x64
    core/     asterlink-core.exe (windows/amd64, GOAMD64=v1, CGO off, Wintun embedded)
    build-config.json   the three public build keys from apps/windows/.env (or .env.example)
    manifest.json       SHA-256 of every file above, channel "internal"

Nothing here is code-signed: the internal channel is a development installer, not a trusted release
(handoff_app_windows.md round 3). `--channel release` is refused until Authenticode signing exists (round 8).
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
    args = parser.parse_args()

    if args.channel != 'internal':
        sys.exit('only --channel internal exists: a release channel needs Authenticode signing (round 8)')
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
    write_manifest(stage, ver, args.channel)
    print('stage ready:', stage)


if __name__ == '__main__':
    main()

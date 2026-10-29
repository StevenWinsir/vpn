#!/usr/bin/env python3
"""Turn a built AsterLink.app into release/ (the DMG and its SHA256SUMS, what customers get) and release-private/
(licences, source notice and archives, release manifest: internal only, never published).

    python3 scripts/build-native-macos.py --configuration Release --internal --run-id R
    python3 scripts/package-native-macos.py --build-run-id R                 # internal ad-hoc DMG

    # Later, with a Developer ID (secrets come from the keychain / environment, never argv):
    CODESIGN_IDENTITY="Developer ID Application: ..." ASTERLINK_TEAM_ID=ABCDE12345 \\
        python3 scripts/build-native-macos.py --configuration Release --run-id R
    ASTERLINK_NOTARY_PROFILE=asterlink-notary \\
        python3 scripts/package-native-macos.py --build-run-id R --notarize

An internal ad-hoc package is labelled as such everywhere and can never be notarized:
`--notarize` on it exits 3 (BLOCKED) rather than pretending. Gatekeeper rejecting it is
the expected outcome, not a defect to be worked around by stripping quarantine.
"""
from pathlib import Path
import argparse
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
import release_scrub  # noqa: E402
VOLUME = 'AsterLink'

# The note on the disk image is read by customers: no internals, flags or document paths (handoff 5.8).
INSTALL_README = """AsterLink 安装说明

一、安装
把 AsterLink 拖到“应用程序”文件夹，再从“应用程序”中打开 AsterLink。请不要直接在本安装盘中运行。

二、首次打开时允许后台服务
AsterLink 需要一项后台服务来建立安全连接，只需允许一次：按窗口中的提示点击“启用”，在自动打开的
“系统设置 › 通用 › 登录项与扩展”中，打开“允许在后台”里 AsterLink 的开关，然后回到 AsterLink 登录即可。
关闭窗口后，AsterLink 会继续在屏幕右上角的菜单栏中运行，连接不会中断；点击菜单栏中的 AsterLink 图标
可以重新打开窗口或退出。

三、升级与卸载
升级：先点击菜单栏中的 AsterLink 图标并选择“退出”，再把新版本拖到“应用程序”文件夹替换旧版本。
卸载：在 AsterLink 的“账户”页，展开“后台服务”中的“故障排查”，点击“移除后台服务”；然后退出 AsterLink，
把它从“应用程序”拖到废纸篓。也可以双击本盘中的“卸载 AsterLink.command”，按窗口中的提示操作。
"""


def run(command, **kwargs):
    print('+', ' '.join(str(c) for c in command), flush=True)
    return subprocess.run(command, check=True, **kwargs)


def create_dmg(stage, dmg, attempts=4):
    """hdiutil create fails intermittently on CI runners ("Resource busy" while the
    system scans the fresh image), so retry a few times with a growing pause before
    giving up with the last error. A partial image from a failed attempt is removed.
    The image is made writable first so the volume icon flag can be set on its root,
    then converted to the compressed read-only format that ships."""
    writable = dmg.with_name(dmg.stem + '-rw.dmg')
    command = ['hdiutil', 'create', '-volname', VOLUME, '-srcfolder', str(stage), '-ov', '-format', 'UDRW', '-fs', 'HFS+', str(writable)]
    for attempt in range(1, attempts + 1):
        try:
            run(command, stdout=subprocess.DEVNULL)
            break
        except subprocess.CalledProcessError:
            if attempt == attempts:
                raise
            writable.unlink(missing_ok=True)
            print(f'hdiutil create failed (attempt {attempt}/{attempts}); retrying', file=sys.stderr, flush=True)
            time.sleep(5 * attempt)
    with tempfile.TemporaryDirectory(prefix='asterlink-dmg-mount-') as mount:
        run(['hdiutil', 'attach', str(writable), '-readwrite', '-nobrowse', '-noautoopen', '-mountpoint', mount], stdout=subprocess.DEVNULL)
        try:
            set_custom_icon_flag(Path(mount))
        finally:
            for attempt in range(1, attempts + 1):
                if subprocess.run(['hdiutil', 'detach', mount] + (['-force'] if attempt > 1 else []), capture_output=True).returncode == 0:
                    break
                time.sleep(2 * attempt)
    run(['hdiutil', 'convert', str(writable), '-format', 'UDZO', '-ov', '-o', str(dmg)], stdout=subprocess.DEVNULL)
    writable.unlink(missing_ok=True)


def capture(command, **kwargs):
    return subprocess.run(command, check=True, capture_output=True, text=True, **kwargs).stdout.strip()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open('rb') as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def blocked(message: str):
    print('BLOCKED: ' + message)
    sys.exit(3)


def set_custom_icon_flag(folder: Path):
    """Sets the "has custom icon" Finder flag on a mounted volume's root (what SetFile -a C does),
    so the .VolumeIcon.icns in it becomes the icon of the volume."""
    info = bytearray(32)
    info[8] = 0x04  # kHasCustomIcon, high byte of the Finder flags
    run(['xattr', '-wx', 'com.apple.FinderInfo', info.hex(), str(folder)])


def make_dmg(app: Path, dmg: Path, identity: str, hardened_timestamp: bool):
    with tempfile.TemporaryDirectory(prefix='asterlink-dmg-') as stage_name:
        stage = Path(stage_name)
        run(['ditto', str(app), str(stage / app.name)])  # preserves signatures, xattrs, symlinks
        (stage / 'Applications').symlink_to('/Applications')
        (stage / '安装说明.txt').write_text(INSTALL_README, encoding='utf-8')
        # Volume icon: the app icon, flagged on the volume root so Finder shows it for the mounted disk.
        shutil.copy2(app / 'Contents/Resources/AppIcon.icns', stage / '.VolumeIcon.icns')
        uninstall = stage / '卸载 AsterLink.command'
        shutil.copy2(ROOT / 'scripts/uninstall-asterlink.sh', uninstall)
        uninstall.chmod(0o755)
        create_dmg(stage, dmg)
    if identity != '-':
        command = ['codesign', '--force', '--sign', identity]
        if hardened_timestamp:
            command.append('--timestamp')
        run(command + [str(dmg)])


def notarize(dmg: Path, profile: str, out: Path):
    result = subprocess.run(['xcrun', 'notarytool', 'submit', str(dmg), '--keychain-profile', profile, '--wait', '--output-format', 'json'],
                            capture_output=True, text=True)
    (out / 'notarytool-submit.json').write_text(result.stdout)
    try:
        submission = json.loads(result.stdout)
    except ValueError:
        sys.exit('notarytool returned no JSON (exit %d): %s' % (result.returncode, result.stderr[-400:]))
    if submission.get('status') != 'Accepted':
        log = subprocess.run(['xcrun', 'notarytool', 'log', submission.get('id', ''), '--keychain-profile', profile],
                             capture_output=True, text=True)
        (out / 'notarytool-log.json').write_text(log.stdout)
        sys.exit('Notarization %s; see %s' % (submission.get('status'), out / 'notarytool-log.json'))
    run(['xcrun', 'stapler', 'staple', str(dmg)])
    run(['xcrun', 'stapler', 'validate', str(dmg)])
    return submission.get('id', '')


def source_archives(out: Path, commit: str):
    """GPL corresponding source: this repository and the pinned Mihomo tree, at the built commits."""
    archives = []
    main = out / ('asterlink-source-%s.tar.gz' % commit[:12])
    run(['git', 'archive', '--format=tar.gz', '--prefix=asterlink/', '-o', str(main), 'HEAD'], cwd=ROOT)
    archives.append(main)
    sub = ROOT / 'native/third_party/Clash.Meta'
    mihomo_commit = capture(['git', 'rev-parse', 'HEAD'], cwd=sub)
    mihomo = out / ('mihomo-source-%s.tar.gz' % mihomo_commit[:12])
    run(['git', 'archive', '--format=tar.gz', '--prefix=mihomo/', '-o', str(mihomo), 'HEAD'], cwd=sub)
    archives.append(mihomo)
    return archives


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--build-run-id', required=True, help='run id passed to build-native-macos.py')
    parser.add_argument('--notarize', action='store_true')
    parser.add_argument('--skip-source-archives', action='store_true', help='faster local iteration; never for a published release')
    args = parser.parse_args()
    if not re.fullmatch(r'[0-9A-Za-z._-]+', args.build_run_id):
        sys.exit('Invalid --build-run-id')

    build = ROOT / 'artifacts/native-macos' / args.build_run_id
    app = build / 'AsterLink.app'
    manifest_path = build / 'build-manifest.json'
    if not app.is_dir() or not manifest_path.is_file():
        sys.exit('No built app/manifest at %s; run build-native-macos.py first' % build)
    built = json.loads(manifest_path.read_text())
    distribution = built.get('distribution')
    if distribution not in ('internal-adhoc', 'developer-id'):
        sys.exit('Only --internal or Developer ID builds can be packaged (this one is %r)' % distribution)
    developer_id = distribution == 'developer-id'
    if built.get('source_dirty') and developer_id:
        sys.exit('Refusing to package a Developer ID build from a dirty working tree')
    if args.notarize and not developer_id:
        blocked('notarization needs a Developer ID build; this is an internal ad-hoc package (Gatekeeper will reject it by design)')
    profile = os.environ.get('ASTERLINK_NOTARY_PROFILE', '')
    if args.notarize and not profile:
        blocked('ASTERLINK_NOTARY_PROFILE (a notarytool keychain profile) is not set')

    # Public: the DMG and a SHA256SUMS of it, nothing else. Private (never published): licences, source notice and
    # archives, the release manifest (handoff_app_standardization.md 8.2, D15).
    out = build / 'release'
    private = release_scrub.private_dir(out)
    if out.exists() or private.exists():
        sys.exit('Release folder exists; use a new build run id: ' + str(out))
    out.mkdir()
    private.mkdir()
    version = built['app_version']
    suffix = '' if developer_id else '-INTERNAL-adhoc'
    dmg = out / ('AsterLink-%s%s-arm64.dmg' % (version, suffix))
    identity = os.environ.get('CODESIGN_IDENTITY', '-') if developer_id else '-'
    make_dmg(app, dmg, identity, hardened_timestamp=developer_id)
    run(['hdiutil', 'verify', str(dmg)], stdout=subprocess.DEVNULL)

    notary_id = ''
    if args.notarize:
        notary_id = notarize(dmg, profile, private)

    (out / 'SHA256SUMS').write_text('%s  %s\n' % (sha256(dmg), dmg.name))
    files = [dmg]
    for name in ('THIRD_PARTY_LICENSES.txt', 'SOURCE.txt'):
        shutil.copy2(build / 'legal' / name, private / name)
        files.append(private / name)
    if not args.skip_source_archives:
        files += source_archives(private, built['source_commit'])
    release = {
        'name': 'AsterLink', 'version': version, 'distribution': distribution, 'channel': built['channel'],
        'dmg': dmg.name, 'dmg_sha256': sha256(dmg), 'dmg_bytes': dmg.stat().st_size,
        'signing': built['signing'], 'hardened_runtime': built['hardened_runtime'],
        'notarized': bool(args.notarize), 'notary_submission_id': notary_id,
        'source_commit': built['source_commit'], 'source_dirty': built['source_dirty'],
        'mihomo_revision': built['mihomo_revision'], 'binaries_sha256': built['sha256'],
        'licenses_without_file': built.get('licenses_without_file', []),
        'min_macos': '15.0', 'architecture': 'arm64',
        'build_machine': {'macos': platform.mac_ver()[0], 'arch': platform.machine(), 'go': capture(['go', 'version']),
                          'xcode': capture(['xcodebuild', '-version']).replace('\n', ' ')},
        'known_limits': [
            'No system-level kill switch; no automatic node selection; no seamless node switching.',
            'Internal ad-hoc packages are not notarized and are rejected by Gatekeeper by design.' if not developer_id else 'See docs/macos-release.md section 10.',
        ],
    }
    (private / 'release-manifest.json').write_text(json.dumps(release, indent=2) + '\n')
    release_scrub.write_private_note(private)
    # Integrity of the internal copy (the DMG is listed in the public SHA256SUMS and in the manifest).
    (private / 'SHA256SUMS').write_text(''.join('%s  %s\n' % (sha256(f), f.name) for f in files[1:] + [private / 'release-manifest.json']))
    print('PACKAGED', out)
    print('internal, do not publish:', private)
    print(json.dumps({k: release[k] for k in ('dmg', 'dmg_sha256', 'distribution', 'notarized')}, indent=2))


if __name__ == '__main__':
    main()

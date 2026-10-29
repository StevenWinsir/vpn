"""`test-native-macos.py --suite release`: audit the packaged macOS build.

Imported by scripts/test-native-macos.py, which supplies `record` (case reporting), `sh`
(subprocess helper) and ROOT. Every case ends pass / fail / blocked; a check that needs a
Developer ID and notarization reports BLOCKED on an ad-hoc package, never a quiet pass.
"""
from pathlib import Path
import hashlib
import json
import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parent))
import release_scrub  # noqa: E402

SECRET_PATTERNS = (b'postgres://', b'postgresql://', b'DATABASE_URL', b'DB_PASSWORD',
                   b'-----BEGIN RSA PRIVATE KEY-----\n', b'-----BEGIN OPENSSH PRIVATE KEY-----\n',
                   b'-----BEGIN EC PRIVATE KEY-----\n', b'-----BEGIN PRIVATE KEY-----\n')
DEBUG_HOOKS = (b'--self-test-root', b'ASTERLINK_DEV_CORE', b'ASTERLINK_DEV_API', b'ASTERLINK_DEV_SITE',
               b'asterlink-tuntest', b'ASTERLINK_POC_')
ENTITLEMENT_ALLOWLIST = set()  # App, Helper and Core need none; adding one is a deliberate review.


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def env_secret_values(root, sh):
    """Secret-looking values from local .env files (this checkout and the main one). Searched for, never printed."""
    common = Path(sh(['git', 'rev-parse', '--git-common-dir']).stdout.strip())
    roots = {root, (common if common.is_absolute() else root / common).resolve().parent}
    values = set()
    for base in roots:
        for rel in ('backend/.env', 'frontend/.env', 'native/nodeagent/.env'):
            path = base / rel
            if not path.is_file():
                continue
            for line in path.read_text(errors='replace').splitlines():
                if '=' in line and not line.lstrip().startswith('#'):
                    key, value = line.split('=', 1)
                    value = value.strip().strip('"\'')
                    if len(value) >= 10 and re.search(r'PASSWORD|SECRET|TOKEN|KEY|DATABASE_URL|DSN', key.upper()):
                        values.add(value.encode())
    # Extra values to hunt for (e.g. a password that is not in any local .env), comma separated.
    for extra in os.environ.get('ASTERLINK_SCAN_SECRETS', '').split(','):
        if len(extra.strip()) >= 8:
            values.add(extra.strip().encode())
    return values


def scan_tree(tree, needles):
    hits = []
    for path in Path(tree).rglob('*'):
        if path.is_symlink() or not path.is_file():
            continue
        data = path.read_bytes()
        if any(needle in data for needle in needles):
            hits.append(path.name)
    return hits


def run(ctx, run_id, build_run_id):
    record, sh, root = ctx['record'], ctx['sh'], ctx['ROOT']
    build_id = build_run_id or (run_id + '-release')
    if not build_run_id:
        build = sh([sys.executable, 'scripts/build-native-macos.py', '--configuration', 'Release', '--internal', '--run-id', build_id])
        if build.returncode != 0:
            record('R-BUILD', 'internal Release app builds, signs inside-out and verifies', 'build failed', 'fail', (build.stdout + build.stderr)[-800:])
            return
        package = sh([sys.executable, 'scripts/package-native-macos.py', '--build-run-id', build_id])
        if package.returncode != 0:
            record('R-PACKAGE', 'DMG, checksums, manifest and source archives are produced', 'packaging failed', 'fail', (package.stdout + package.stderr)[-800:])
            return
    base = root / 'artifacts/native-macos' / build_id
    app = base / 'AsterLink.app'
    release = base / 'release'
    private = release_scrub.private_dir(release)
    try:
        built = json.loads((base / 'build-manifest.json').read_text())
        manifest = json.loads((private / 'release-manifest.json').read_text())
    except (OSError, ValueError):
        record('R-BUILD', 'build and release manifests exist', 'missing under ' + str(base), 'fail')
        return
    contents = app / 'Contents'
    binaries = {'AsterLink': (contents / 'MacOS/AsterLink', 'com.asterlink.vpn'),
                'AsterLinkHelper': (contents / 'MacOS/AsterLinkHelper', 'com.asterlink.vpn.helper'),
                'asterlink-core': (contents / 'Resources/asterlink-core', 'com.asterlink.vpn.core')}
    developer_id = built['distribution'] == 'developer-id'
    record('R-BUILD', 'optimized app + DMG packaged', '%s %s (%s)' % (built['distribution'], built['app_version'], built['signing']), 'pass')

    # Signature, identifiers and Hardened Runtime on every Mach-O.
    verify = sh(['codesign', '--verify', '--deep', '--strict', str(app)])
    problems = [] if verify.returncode == 0 else ['verify: ' + verify.stderr.strip()[-120:]]
    for name, (path, identifier) in binaries.items():
        text = (lambda r: r.stdout + r.stderr)(sh(['codesign', '-dvv', str(path)]))
        if 'Identifier=' + identifier not in text:
            problems.append(name + ' identifier')
        if 'runtime' not in text.split('flags=')[-1].split('\n')[0]:
            problems.append(name + ' hardened runtime missing')
        if developer_id and 'Authority=Developer ID Application' not in text:
            problems.append(name + ' not Developer ID')
    record('R-SIGN', 'deep strict verify; exact identifiers; Hardened Runtime on App, Helper and Core', '; '.join(problems) or 'all verified',
           'fail' if problems else 'pass')

    bad = []
    for name, (path, _) in binaries.items():
        keys = set(re.findall(r'<key>([^<]+)</key>', sh(['codesign', '-d', '--entitlements', ':-', str(path)]).stdout))
        if 'com.apple.security.get-task-allow' in keys or keys - ENTITLEMENT_ALLOWLIST:
            bad.append('%s:%s' % (name, ','.join(sorted(keys - ENTITLEMENT_ALLOWLIST))))
    record('R-ENT', 'no entitlements (no get-task-allow, no JIT/unsigned-memory exceptions)', '; '.join(bad) or 'none present', 'fail' if bad else 'pass')

    leaks = [name for name, (path, _) in binaries.items() if any(h in path.read_bytes() for h in DEBUG_HOOKS)]
    record('R-NOHOOKS', 'no self-test root, dev-core/API overrides or TUN harness in shipped binaries', ','.join(leaks) or 'clean',
           'fail' if leaks else 'pass')

    needles = list(SECRET_PATTERNS) + sorted(env_secret_values(root, sh))
    compared = len(needles) - len(SECRET_PATTERNS)
    hits = scan_tree(app, needles)
    record('R-SECRET', 'no database/node secret or private key in the bundle (compared with %d local .env value(s))' % compared,
           ','.join(hits) or 'clean', 'fail' if hits else 'pass')

    # The scanner itself must be able to fail: plant a marker in a throwaway tree and demand a hit.
    probe = Path(tempfile.mkdtemp(prefix='asterlink-probe-'))
    try:
        (probe / 'planted.bin').write_bytes(b'\x00junk postgres://user:pw@host/db junk')
        detected = bool(scan_tree(probe, needles))
    finally:
        shutil.rmtree(probe, ignore_errors=True)
    record('R-SECRET-SELF', 'the secret scanner detects a planted connection string', 'detected' if detected else 'MISSED', 'pass' if detected else 'fail')

    info = json.loads(sh(['plutil', '-convert', 'json', '-o', '-', str(contents / 'Info.plist')]).stdout)
    problems = []
    for key in ('AsterLinkAPIBase', 'AsterLinkWebsiteBase'):
        if not info.get(key, '').startswith('https://'):
            problems.append(key + ' not https')
    if 'NSAppTransportSecurity' in info:
        problems.append('ATS exceptions present')
    if info.get('LSMinimumSystemVersion') != '15.0':
        problems.append('min macOS')
    if info.get('CFBundleDevelopmentRegion') != 'zh_CN' or info.get('CFBundleLocalizations') != ['zh_CN']:
        problems.append('system menus not declared Chinese')
    if info.get('AsterLinkChannel') != built['channel'] or (developer_id and not info.get('AsterLinkTeamID')):
        problems.append('channel/team')
    if any('mihomo' in key.lower() or 'revision' in key.lower() for key in info):
        problems.append('engine revision key in Info.plist')
    helper_info = sh(['launchctl', 'plist', '__TEXT,__info_plist', str(binaries['AsterLinkHelper'][0])]).stdout
    if '"CFBundleShortVersionString" = "%s"' % built['app_version'] not in helper_info:
        problems.append('Helper embedded version != app version')
    record('R-PLIST', 'https-only endpoints, no ATS exceptions, macOS 15, Chinese system menus, Helper/App versions equal, no engine revision key', '; '.join(problems) or 'ok',
           'fail' if problems else 'pass')

    # The app icon is part of the sealed bundle: the plist names it, it is a valid ICNS and it reaches 1024 px.
    problems = []
    icon_name = info.get('CFBundleIconFile')
    icon_path = contents / 'Resources' / (icon_name if (icon_name or '').endswith('.icns') else '%s.icns' % icon_name)
    if not icon_name or not icon_path.is_file():
        problems.append('CFBundleIconFile missing or file absent')
    else:
        data = icon_path.read_bytes()
        kinds, offset = [], 8
        if data[:4] != b'icns' or struct.unpack('>I', data[4:8])[0] != len(data):
            problems.append('not an ICNS container')
        else:
            while offset + 8 <= len(data):
                kind, length = data[offset:offset + 4], struct.unpack('>I', data[offset + 4:offset + 8])[0]
                if length < 8 or offset + length > len(data):
                    problems.append('truncated ICNS entry')
                    break
                kinds.append(kind)
                offset += length
            if b'ic10' not in kinds:
                problems.append('no 1024 px image')
    for template in ('Idle', 'Active', 'Error'):
        if not (contents / 'Resources' / ('MenuBar%sTemplate.png' % template)).is_file():
            problems.append('menu-bar image %s' % template)
    record('R-ICON', 'Info.plist names the app icon; it is a valid ICNS with a 1024 px image; menu-bar images ship',
           '; '.join(problems) or 'ok', 'fail' if problems else 'pass')

    problems = [n for n, (path, _) in binaries.items() if built['sha256'][n] != sha256_file(path)]
    worker = json.loads((contents / 'Resources/worker-manifest.json').read_text())
    if worker['sha256'] != sha256_file(binaries['asterlink-core'][0]) or worker['version'] != built['app_version']:
        problems.append('worker-manifest')
    if manifest['dmg_sha256'] != sha256_file(release / manifest['dmg']):
        problems.append('dmg hash')
    for folder in (release, private):
        if sh(['shasum', '-a', '256', '-c', 'SHA256SUMS'], cwd=folder).returncode != 0:
            problems.append('SHA256SUMS of ' + folder.name)
    pin = sh(['git', 'ls-files', '-s', 'native/third_party/Clash.Meta']).stdout.split()
    if not pin or manifest['mihomo_revision'] != pin[1]:
        problems.append('mihomo revision != pinned submodule')
    record('R-MANIFEST', 'manifests, worker hash, both SHA256SUMS and the pinned engine revision (kept in release-private) match the bytes shipped',
           '; '.join(problems) or 'consistent', 'fail' if problems else 'pass')

    # Handoff_app_standardization.md 8.2: the public folder is the DMG and its checksum; everything else is internal.
    problems = release_scrub.public_problems(release, [manifest['dmg']])
    record('R-PUBLIC', 'release/ holds only the DMG and a SHA256SUMS that lists only it',
           '; '.join(problems) or 'only %s and SHA256SUMS' % manifest['dmg'], 'fail' if problems else 'pass')

    problems = release_scrub.private_problems(private, ('THIRD_PARTY_LICENSES.txt', 'SOURCE.txt', 'release-manifest.json'))
    licenses = (private / 'THIRD_PARTY_LICENSES.txt').read_text(errors='replace') if (private / 'THIRD_PARTY_LICENSES.txt').is_file() else ''
    if 'GNU GENERAL PUBLIC LICENSE' not in licenses or 'metacubex/mihomo' not in licenses:
        problems.append('engine licence text missing')
    source_text = (private / 'SOURCE.txt').read_text() if (private / 'SOURCE.txt').is_file() else ''
    if built['source_commit'] not in source_text or built['mihomo_revision'] not in source_text:
        problems.append('SOURCE.txt commits')
    archives = sorted(private.glob('*-source-*.tar.gz'))
    if len(archives) != 2:
        problems.append('source archives: %d' % len(archives))
    problems += [a.name + ' unreadable' for a in archives if sh(['tar', '-tzf', str(a)]).returncode != 0]
    missing = built.get('licenses_without_file', [])
    record('R-PRIVATE', 'release-private/ keeps the licence texts, SOURCE.txt, both source archives, the release manifest and the internal note',
           '; '.join(problems) or 'ok; %d module(s) without a license file: %s' % (len(missing), ','.join(missing) or '-'),
           'fail' if problems else 'pass')

    # The shipped Core answers with an empty engine revision; Debug builds still stamp it.
    version = sh([str(binaries['asterlink-core'][0]), '--version']).stdout.split()
    stamped = built.get('core_revision_stamped', True)
    record('R-UNSTAMPED', 'the optimized Core reports no engine revision (Debug builds keep it)',
           'asterlink-core --version: %s' % ' '.join(version), 'pass' if version == [built['app_version']] and not stamped else 'fail')

    # Nothing inside the bundle: no private-only file, no marker in any text file or plist, binaries within the D16 baseline.
    rules = release_scrub.load_baseline('macos')
    revision = built['mihomo_revision']
    found = release_scrub.scan_tree(app, revision, 'AsterLink.app/')
    problems, residue = release_scrub.judge(found, rules)
    problems += ['private-only file: ' + n for n in release_scrub.named_leaks(found)]
    record('R-SCRUB-BUNDLE', 'the app bundle carries no licence, source notice, release info or engine revision; binary residue within the D16 baseline',
           '; '.join(problems[:8]) or '%d files clean; accepted residue: %s' % (len(found), release_scrub.summary(residue)), 'fail' if problems else 'pass')

    failures = release_scrub.self_test('macos')
    record('R-SCRUB-SELF', 'the scrub audit catches a planted licence file, plist key, text and binary marker and the engine revision',
           '; '.join(failures) or 'every plant detected', 'fail' if failures else 'pass')

    dmg = release / manifest['dmg']
    problems = [] if sh(['hdiutil', 'verify', str(dmg)]).returncode == 0 else ['hdiutil verify']
    mount = Path(tempfile.mkdtemp(prefix='asterlink-mount-'))
    attach = sh(['hdiutil', 'attach', str(dmg), '-readonly', '-nobrowse', '-noautoopen', '-mountpoint', str(mount)])
    if attach.returncode != 0:
        problems.append('attach: ' + attach.stderr.strip()[-120:])
    else:
        try:
            if (mount / 'Applications').resolve() != Path('/Applications'):
                problems.append('Applications link')
            if sh(['codesign', '--verify', '--deep', '--strict', str(mount / 'AsterLink.app')]).returncode != 0:
                problems.append('mounted app signature')
            if sha256_file(mount / 'AsterLink.app/Contents/Resources/asterlink-core') != built['sha256']['asterlink-core']:
                problems.append('mounted core differs')
            if scan_tree(mount, needles):
                problems.append('secret in DMG')
            if not os.access(mount / '卸载 AsterLink.command', os.X_OK):
                problems.append('uninstall script missing')
            if not (mount / '.VolumeIcon.icns').is_file():
                problems.append('volume icon missing')
            finder = sh(['xattr', '-px', 'com.apple.FinderInfo', str(mount)]).stdout.split()
            if len(finder) < 9 or not int(finder[8], 16) & 0x04:
                problems.append('volume icon flag not set')
            readme = (mount / '安装说明.txt').read_text(encoding='utf-8') if (mount / '安装说明.txt').is_file() else ''
            if not readme or any(word in readme for word in ('特权服务', '--apply', 'docs/', '预演', 'Helper')):
                problems.append('install note missing or not customer copy')
            copy_guard = release_scrub._copy_guard()
            for label, content in (('install note', readme), ('uninstaller', (mount / '卸载 AsterLink.command').read_text(encoding='utf-8')
                                                                 if (mount / '卸载 AsterLink.command').is_file() else '')):
                words = sorted({w for line in content.splitlines() if copy_guard.CJK.search(line)
                                for w in copy_guard.hits_in(line, copy_guard.FORBIDDEN_DESKTOP_EN)})
                if words:
                    problems.append('copy guard (%s): %s' % (label, ', '.join(words)))
            found = release_scrub.scan_tree(mount, revision)
            leaks, _ = release_scrub.judge(found, rules)
            leaks += ['private-only file: ' + n for n in release_scrub.named_leaks(found)]
            uninstaller = (mount / '卸载 AsterLink.command').read_text(encoding='utf-8') if (mount / '卸载 AsterLink.command').is_file() else ''
            if any(word in uninstaller for word in ('handoff', 'native/', 'docs/', 'Test hook', 'worker', 'privileged')):
                leaks.append('developer notes in the uninstaller')
            problems += ['scrub: ' + leak for leak in leaks[:6]]
        finally:
            sh(['hdiutil', 'detach', str(mount), '-force'])
            shutil.rmtree(mount, ignore_errors=True)
    record('R-DMG', 'DMG verifies, mounts read-only, carries the byte-identical signed app, Applications link, uninstaller, volume icon and customer install note; nothing in it carries licence, engine or developer notes',
           '; '.join(problems) or 'ok', 'fail' if problems else 'pass')

    sandbox = Path(tempfile.mkdtemp(prefix='asterlink-uninstall-'))
    try:
        (sandbox / 'home/Library/Caches/com.asterlink.vpn').mkdir(parents=True)
        (sandbox / 'home/Library/Caches/other.vpn').mkdir(parents=True)
        (sandbox / 'root/Applications/AsterLink.app').mkdir(parents=True)
        (sandbox / 'root/Applications/OtherVPN.app').mkdir(parents=True)
        env = dict(os.environ, HOME=str(sandbox / 'home'), ASTERLINK_UNINSTALL_ROOT=str(sandbox / 'root'), ASTERLINK_UNINSTALL_SKIP_KEYCHAIN='1')
        script = str(root / 'scripts/uninstall-asterlink.sh')
        dry = subprocess.run([script], env=env, capture_output=True, text=True, encoding='utf-8', errors='replace')
        survived = (sandbox / 'root/Applications/AsterLink.app').exists()
        applied = subprocess.run([script, '--apply'], env=env, capture_output=True, text=True, encoding='utf-8', errors='replace')
        ok = (dry.returncode == 0 and survived and applied.returncode == 0
              and not (sandbox / 'root/Applications/AsterLink.app').exists()
              and not (sandbox / 'home/Library/Caches/com.asterlink.vpn').exists()
              and (sandbox / 'root/Applications/OtherVPN.app').exists()
              and (sandbox / 'home/Library/Caches/other.vpn').exists())
        record('R02-UNINSTALL', 'dry run changes nothing; --apply removes only AsterLink resources and leaves other VPNs',
               'ok' if ok else (dry.stderr + applied.stderr)[-200:], 'pass' if ok else 'fail')
    finally:
        shutil.rmtree(sandbox, ignore_errors=True)

    assess = sh(['spctl', '--assess', '--type', 'execute', '-vv', str(app)])
    if developer_id and manifest['notarized']:
        record('R01-GATEKEEPER', 'Gatekeeper accepts the notarized app', (assess.stderr or assess.stdout).strip()[:120],
               'pass' if assess.returncode == 0 else 'fail')
        staple = sh(['xcrun', 'stapler', 'validate', str(dmg)])
        record('R01-STAPLE', 'notarization ticket is stapled to the DMG', 'valid' if staple.returncode == 0 else staple.stderr.strip()[:120],
               'pass' if staple.returncode == 0 else 'fail')
    else:
        verdict = 'rejected, as expected for an ad-hoc un-notarized build' if assess.returncode != 0 else 'UNEXPECTEDLY accepted'
        record('R01-GATEKEEPER', 'Gatekeeper accepts a notarized Developer ID build', 'not applicable to %s: %s' % (built['distribution'], verdict), 'blocked')
        record('R01-STAPLE', 'notarization ticket is stapled', 'not notarized: needs an Apple Developer ID account', 'blocked')

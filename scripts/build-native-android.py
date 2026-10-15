#!/usr/bin/env python3
"""Android client build entry point (handoff_app_android.md 4, 12).

  --target aar   gomobile-binds native/core/mobile (Go Core + pinned Mihomo) into
                 apps/android/build/core/asterlink-core.aar and writes core-build.json next to it.
                 Every libgojni.so must be an ELF for its ABI with all LOAD segments aligned to at
                 least 16 KB (Android 15+ 16 KB page devices).
  --target apk   builds the Core AAR if it is missing (or with --rebuild-core), then the Debug APK
                 with the Gradle wrapper, and checks the packaged .so is stored uncompressed and
                 16 KB aligned (zipalign -c -P 16).

The versions come from apps/android/toolchain.lock.json; a different Go, NDK or x/mobile is a
FAIL, never a silent substitute. gomobile/gobind are built from the version native/core/go.mod
pins (its `tool` block) into .runtime/android/bin. Public build parameters come from
apps/android/.env (or .env.example for Debug), parsed by the Gradle build itself.
Release packaging (signing, ABI splits, R8) is round 6 and refused here.

  --tun-test-hooks  binds the debug-only TUN acceptance hooks (-tags asterlink_tuntest) and also
                    builds the test APK with -Pasterlink.tunTest=true. core-build.json records
                    tuntest_hooks, and the app build refuses an AAR whose hooks do not match.

Exit codes: 0 = built, 1 = failed, 3 = BLOCKED (a prerequisite is missing).
"""
import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import zipfile
from pathlib import Path

import prepare_mihomo

ROOT = Path(__file__).resolve().parent.parent
ANDROID = ROOT / 'apps' / 'android'
CORE = ROOT / 'native' / 'core'
OUT = ANDROID / 'build' / 'core'
TOOLS = ROOT / '.runtime' / 'android' / 'bin'
LOCK = json.loads((ANDROID / 'toolchain.lock.json').read_text())

# ABI -> (gomobile target, ELF machine as llvm-readelf prints it)
ABIS = {
    'arm64-v8a': ('android/arm64', 'AArch64'),
    'x86_64': ('android/amd64', 'Advanced Micro Devices X86-64'),
    'armeabi-v7a': ('android/arm', 'ARM'),
}
MIN_PAGE_ALIGN = 16384
EXIT_OK, EXIT_FAIL, EXIT_BLOCKED = 0, 1, 3


class Blocked(Exception):
    pass


def run(cmd, **kwargs):
    print('+ ' + ' '.join(str(c) for c in cmd), flush=True)
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


# gomobile bind runs `go mod tidy` in a scratch module, which consults sum.golang.org on every
# bind. A hosted runner's dropped HTTP/2 stream must not fail the build; a checksum MISMATCH,
# or any compile error, is not a network error and fails at once (GOSUMDB stays on).
TRANSIENT_GO_NETWORK = re.compile(
    r'(reading|fetching) https://\S+: (stream error|unexpected EOF|.*(i/o timeout|connection reset|TLS handshake timeout))'
    r'|Get "https://[^"]+": (dial tcp|unexpected EOF|.*(i/o timeout|connection reset|TLS handshake timeout))'
    r'|https://\S+: 50[234] ')


def run_with_network_retry(cmd, attempts=3, **kwargs):
    for attempt in range(1, attempts + 1):
        print('+ ' + ' '.join(str(c) for c in cmd), flush=True)
        process = subprocess.run([str(c) for c in cmd], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, **kwargs)
        print(process.stdout, end='' if process.stdout.endswith('\n') or not process.stdout else '\n', flush=True)
        if process.returncode == 0:
            return
        if attempt == attempts or not TRANSIENT_GO_NETWORK.search(process.stdout):
            raise subprocess.CalledProcessError(process.returncode, [str(c) for c in cmd], process.stdout)
        print('transient Go module network error; retrying (%d/%d) in %ds' % (attempt + 1, attempts, 15 * attempt), flush=True)
        time.sleep(15 * attempt)


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
    raise Blocked('ANDROID_HOME is not set (one SDK for everything; see docs/android-foundation.md)')


def ndk_dir() -> Path:
    ndk = sdk_root() / 'ndk' / LOCK['ndk']['version']
    if not (ndk / 'source.properties').is_file():
        raise Blocked('NDK %s is not installed in %s' % (LOCK['ndk']['version'], sdk_root()))
    return ndk


def llvm_tool(name: str) -> Path:
    prebuilt = ndk_dir() / 'toolchains' / 'llvm' / 'prebuilt'
    for host in sorted(prebuilt.iterdir()):
        candidate = host / 'bin' / name
        if candidate.is_file():
            return candidate
    raise Blocked('%s not found in the NDK' % name)


def mihomo_revision() -> str:
    out = subprocess.run(['git', 'submodule', 'status', 'native/third_party/Clash.Meta'], cwd=ROOT,
                         capture_output=True, text=True, check=True).stdout.strip()
    match = re.match(r'^[-+U ]?([0-9a-f]{40}) ', out)
    if not match or out.startswith('-'):
        raise Blocked('the Mihomo submodule is not checked out (git submodule update --init)')
    if match.group(1) != LOCK['mihomo']['commit']:
        raise RuntimeError('Mihomo submodule is at %s, the lock says %s' % (match.group(1), LOCK['mihomo']['commit']))
    return match.group(1)


def check_go():
    if not shutil.which('go'):
        raise Blocked('go not found')
    version = subprocess.run(['go', 'env', 'GOVERSION'], capture_output=True, text=True, check=True).stdout.strip()
    if version != 'go' + LOCK['go']['version']:
        raise RuntimeError('Go %s is installed, the lock says go%s' % (version, LOCK['go']['version']))


def go_env(**extra):
    env = dict(os.environ)
    # Reproducible output: no VCS stamp, module path-relative file names (-trimpath below).
    env['GOFLAGS'] = '-buildvcs=false'
    env.update(extra)
    return env


def build_gomobile():
    """gomobile and gobind at the version native/core/go.mod pins, never a global install."""
    TOOLS.mkdir(parents=True, exist_ok=True)
    run(['go', 'build', '-o', str(TOOLS) + os.sep, 'golang.org/x/mobile/cmd/gomobile', 'golang.org/x/mobile/cmd/gobind'],
        cwd=CORE, env=go_env())
    info = subprocess.run(['go', 'version', '-m', str(TOOLS / 'gomobile')], capture_output=True, text=True, check=True).stdout
    match = re.search(r'^\s+mod\s+golang.org/x/mobile\s+(\S+)', info, re.M)
    if not match or match.group(1) != LOCK['x_mobile']['version']:
        raise RuntimeError('gomobile was built from x/mobile %s, the lock says %s' % (match and match.group(1), LOCK['x_mobile']['version']))


def check_so(so: Path, abi: str) -> dict:
    readelf = llvm_tool('llvm-readelf')
    header = subprocess.run([str(readelf), '-h', str(so)], capture_output=True, text=True, check=True).stdout
    machine = re.search(r'Machine:\s+(.+)', header).group(1).strip()
    if machine != ABIS[abi][1]:
        raise RuntimeError('%s is %s, expected %s' % (so, machine, ABIS[abi][1]))
    program = subprocess.run([str(readelf), '-lW', str(so)], capture_output=True, text=True, check=True).stdout
    aligns = [int(line.split()[-1], 16) for line in program.splitlines() if line.strip().startswith('LOAD')]
    if not aligns or min(aligns) < MIN_PAGE_ALIGN:
        raise RuntimeError('%s LOAD alignment %s is below 16 KB' % (so, [hex(a) for a in aligns]))
    return {'bytes': so.stat().st_size, 'sha256': sha256(so), 'machine': machine,
            'load_segments': len(aligns), 'min_load_align': min(aligns)}


def bind(abis, app_version, out_dir: Path, hooks=False) -> dict:
    patch = prepare_mihomo.prepare()
    ndk = ndk_dir()
    revision = mihomo_revision()
    out_dir.mkdir(parents=True, exist_ok=True)
    aar = out_dir / 'asterlink-core.aar'
    ldflags = '-s -w -X asterlink/core/mobile.version=%s -X asterlink/core/mobile.mihomoRevision=%s' % (app_version, revision)
    env = go_env(ANDROID_HOME=str(sdk_root()), ANDROID_NDK_HOME=str(ndk),
                 PATH=str(TOOLS) + os.pathsep + os.environ.get('PATH', ''))
    run_with_network_retry([TOOLS / 'gomobile', 'bind', '-target=' + ','.join(ABIS[a][0] for a in abis),
         '-androidapi', str(LOCK['android']['min_sdk']), '-javapkg', 'com.asterlink.core',
         '-trimpath', *(['-tags', 'asterlink_tuntest'] if hooks else []), '-ldflags', ldflags, '-o', aar, './mobile'], cwd=CORE, env=env)
    libraries = {}
    with tempfile.TemporaryDirectory() as scratch, zipfile.ZipFile(aar) as archive:
        names = archive.namelist()
        found = sorted(n.split('/')[1] for n in names if n.startswith('jni/') and n.endswith('/libgojni.so'))
        if found != sorted(abis):
            raise RuntimeError('AAR has ABIs %s, expected %s' % (found, sorted(abis)))
        for abi in abis:
            so = Path(archive.extract('jni/%s/libgojni.so' % abi, scratch))
            libraries[abi] = check_so(so, abi)
        if 'classes.jar' not in names:
            raise RuntimeError('AAR has no classes.jar')
    return {
        'app_version': app_version, 'core_version': app_version, 'mihomo_revision': revision,
        'mihomo_patch': patch,
        'go': LOCK['go']['version'], 'x_mobile': LOCK['x_mobile']['version'], 'ndk': LOCK['ndk']['version'],
        'androidapi': LOCK['android']['min_sdk'], 'abis': libraries,
        'aar_bytes': aar.stat().st_size, 'aar_sha256': sha256(aar),
        # Debug-only TUN acceptance hooks (native/core/mobile/tuntest.go). The app build
        # refuses an AAR whose hooks do not match what it was asked to build.
        'tuntest_hooks': hooks,
    }


def build_aar(args) -> dict:
    prepare_mihomo.prepare()
    check_go()
    build_gomobile()
    record = bind(args.abi, args.app_version, OUT, args.tun_test_hooks)
    (OUT / 'core-build.json').write_text(json.dumps(record, indent=2) + '\n')
    for abi, lib in record['abis'].items():
        print('libgojni.so %-12s %6.1f MiB  min LOAD align 0x%x  %s' % (abi, lib['bytes'] / 2 ** 20, lib['min_load_align'], lib['machine']))
    print('AAR %s (%.1f MiB) sha256 %s' % (OUT / 'asterlink-core.aar', record['aar_bytes'] / 2 ** 20, record['aar_sha256']))
    if args.check_reproducible:
        with tempfile.TemporaryDirectory() as again:
            second = bind(args.abi, args.app_version, Path(again), args.tun_test_hooks)
        differ = [abi for abi in args.abi if second['abis'][abi]['sha256'] != record['abis'][abi]['sha256']]
        if differ:
            raise RuntimeError('libgojni.so is not reproducible for %s' % differ)
        print('reproducible: a second bind produced byte-identical libgojni.so for %s' % ', '.join(args.abi))
        record['reproducible_so'] = True
        (OUT / 'core-build.json').write_text(json.dumps(record, indent=2) + '\n')
    return record


def java_env() -> dict:
    env = dict(os.environ, ANDROID_HOME=str(sdk_root()))
    if not env.get('JAVA_HOME') and not shutil.which('java'):
        raise Blocked('no JDK (set JAVA_HOME to a JDK %s)' % LOCK['jdk']['major'])
    return env


def gradle(tasks, app_version, env):
    wrapper = ANDROID / ('gradlew.bat' if os.name == 'nt' else 'gradlew')
    run([wrapper, '--console=plain', '-Pasterlink.appVersion=' + app_version, *tasks], cwd=ANDROID, env=env)


def build_apk(args) -> dict:
    patch = prepare_mihomo.prepare()
    record_file = OUT / 'core-build.json'
    old = json.loads(record_file.read_text()) if record_file.is_file() else {}
    stale = old.get('tuntest_hooks', False) != args.tun_test_hooks or old.get('mihomo_patch') != patch
    if args.rebuild_core or stale or not record_file.is_file() or not (OUT / 'asterlink-core.aar').is_file():
        build_aar(args)
    record = json.loads(record_file.read_text())
    if record['app_version'] != args.app_version:
        raise RuntimeError('the Core AAR was built as %s, the APK would be %s; rebuild with --rebuild-core' % (record['app_version'], args.app_version))
    env = java_env()
    tasks = [':app:assembleDebug'] + ([':app:assembleDebugAndroidTest', '-Pasterlink.tunTest=true'] if args.tun_test_hooks else [])
    gradle(tasks, args.app_version, env)
    apk = ANDROID / 'app' / 'build' / 'outputs' / 'apk' / 'debug' / 'app-debug.apk'
    zipalign = sorted((sdk_root() / 'build-tools').glob('*/zipalign'))
    if not zipalign:
        raise Blocked('zipalign not found in the SDK build-tools')
    run([zipalign[-1], '-c', '-P', '16', '4', apk])
    with zipfile.ZipFile(apk) as archive:
        packaged = {i.filename: i for i in archive.infolist() if i.filename.endswith('libgojni.so')}
    if sorted(packaged) != sorted('lib/%s/libgojni.so' % abi for abi in record['abis']):
        raise RuntimeError('APK packages %s, the Core AAR has %s' % (sorted(packaged), sorted(record['abis'])))
    compressed = [name for name, info in packaged.items() if info.compress_type != zipfile.ZIP_STORED]
    if compressed:
        raise RuntimeError('native libraries must be stored uncompressed: %s' % compressed)
    report = {'apk': str(apk.relative_to(ROOT)), 'apk_bytes': apk.stat().st_size, 'apk_sha256': sha256(apk),
              'so_stored_uncompressed': True, 'zipalign_16k': True, 'core': record}
    reports = ROOT / 'artifacts' / 'native-android'
    reports.mkdir(parents=True, exist_ok=True)
    (reports / 'build-report.json').write_text(json.dumps(report, indent=2) + '\n')
    print('APK %s (%.1f MiB)' % (apk, report['apk_bytes'] / 2 ** 20))
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--target', choices=('aar', 'apk'), required=True)
    parser.add_argument('--configuration', choices=('Debug', 'Release'), default='Debug')
    parser.add_argument('--abi', action='append', choices=sorted(ABIS), help='repeatable; default arm64-v8a')
    parser.add_argument('--app-version', default=None, help='default: apps/android/gradle.properties asterlink.appVersion')
    parser.add_argument('--rebuild-core', action='store_true', help='apk: rebuild the Core AAR even if one exists')
    parser.add_argument('--check-reproducible', action='store_true', help='aar: bind twice and require identical libgojni.so')
    parser.add_argument('--tun-test-hooks', action='store_true',
                        help='aar/apk: bind the debug-only TUN acceptance hooks (scripts/test-native-android.py --suite tun only)')
    args = parser.parse_args()
    args.abi = sorted(set(args.abi or ['arm64-v8a']))
    if args.app_version is None:
        props = (ANDROID / 'gradle.properties').read_text()
        args.app_version = re.search(r'^asterlink\.appVersion=(\S+)$', props, re.M).group(1)
    if not re.fullmatch(r'\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?', args.app_version):
        raise SystemExit('Invalid --app-version')
    if args.configuration == 'Release':
        print('BLOCKED: Release packaging (signing, R8, ABI splits) is round 6')
        return EXIT_BLOCKED
    try:
        build_aar(args) if args.target == 'aar' else build_apk(args)
    except Blocked as reason:
        print('BLOCKED: %s' % reason)
        return EXIT_BLOCKED
    except (RuntimeError, subprocess.CalledProcessError) as error:
        print('FAIL: %s' % error)
        return EXIT_FAIL
    return EXIT_OK


if __name__ == '__main__':
    sys.exit(main())

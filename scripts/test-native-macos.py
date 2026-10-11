#!/usr/bin/env python3
"""Single entry for native macOS client tests.

    python3 scripts/test-native-macos.py --suite unit
    python3 scripts/test-native-macos.py --suite bundle          # builds a Debug app, no network changes
    sudo -E python3 scripts/test-native-macos.py --suite tun --allow-network-changes

Every case records case_id / expected / observed / result (pass|fail|blocked). A suite
that cannot run reports BLOCKED and exits 3; it never substitutes a mock and reports
success. Passwords are read only from the environment, never from argv.
"""
from pathlib import Path
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
RESULTS = []


def record(case_id, expected, observed, result, evidence=''):
    RESULTS.append({'case_id': case_id, 'expected': expected, 'observed': observed, 'result': result, 'evidence': evidence})
    print('%-8s %-6s %s' % (case_id, result.upper(), observed), flush=True)


def sh(command, cwd=ROOT, env=None, check=False):
    return subprocess.run(command, cwd=cwd, env=env, capture_output=True, text=True, check=check)


def count_tests(text, pattern):
    return len(re.findall(pattern, text))


def suite_unit():
    go_env = dict(os.environ, CGO_ENABLED='0')
    for case_id, directory in (('U-GO-IPC', 'native/core'), ('U-GO-POLICY', 'shared/nodepolicy')):
        result = sh(['go', 'test', '-count=1', '-v', './...'], cwd=ROOT / directory, env=go_env)
        passed = count_tests(result.stdout, r'(?m)^\s*--- PASS')
        failed = count_tests(result.stdout, r'(?m)^\s*--- FAIL')
        ok = result.returncode == 0 and passed > 0 and failed == 0
        record(case_id, 'go tests pass', '%d passed, %d failed' % (passed, failed), 'pass' if ok else 'fail', result.stdout[-400:] if not ok else '')
    result = sh(['go', 'vet', './...'], cwd=ROOT / 'native/core', env=go_env)
    record('U-GO-VET', 'go vet clean', result.stderr.strip() or 'clean', 'pass' if result.returncode == 0 else 'fail')
    # test-native-env imports native_env from its own directory.
    result = sh([sys.executable, 'test-native-env.py'], cwd=ROOT / 'scripts')
    ran = re.search(r'Ran (\d+) tests', result.stderr)
    record('U-ENV-PY', 'shared env fixtures accepted/rejected identically', 'ran %s tests' % (ran.group(1) if ran else '0'),
           'pass' if result.returncode == 0 and ran else 'fail', result.stderr[-400:] if result.returncode else '')
    result = sh(['swift', 'test'], cwd=ROOT / 'apps/macos')
    summary = re.findall(r'Executed (\d+) tests, with (\d+) failures', result.stdout + result.stderr)
    executed = max((int(a) for a, _ in summary), default=0)
    failures = max((int(b) for _, b in summary), default=0)
    ok = result.returncode == 0 and executed > 0 and failures == 0
    record('U-SWIFT', 'swift tests pass', '%d executed, %d failures' % (executed, failures), 'pass' if ok else 'fail',
           (result.stdout + result.stderr)[-600:] if not ok else '')


def helper_self_test(app: Path):
    root = Path(tempfile.mkdtemp(prefix='asterlink-selftest-')) / 'Workers'
    result = subprocess.run([str(app / 'Contents/MacOS/AsterLinkHelper'), '--self-test-root', str(root)],
                            capture_output=True, text=True, timeout=60)
    shutil.rmtree(root.parent, ignore_errors=True)
    try:
        return result.returncode, json.loads(result.stdout.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return result.returncode, {}


def suite_bundle(run_id):
    build = sh([sys.executable, 'scripts/build-native-macos.py', '--configuration', 'Debug', '--run-id', run_id + '-bundle'])
    app = ROOT / 'artifacts/native-macos' / (run_id + '-bundle') / 'AsterLink.app'
    if build.returncode != 0 or not app.exists():
        record('S-BUILD', 'Debug app builds and verifies', 'build failed', 'fail', (build.stdout + build.stderr)[-800:])
        return
    record('S-BUILD', 'Debug app builds and verifies', 'built and codesign --verify passed', 'pass')

    code, reply = helper_self_test(app)
    ok = code == 0 and reply.get('ok') and reply.get('core', {}).get('protocol_version') == 1
    record('S-HELLO', 'Helper installs verified worker, App<-Helper<-Core hello, second worker refused', json.dumps(reply)[:160], 'pass' if ok else 'fail')

    def mutated(name, mutate, expect_code):
        copy = app.parent / name
        shutil.copytree(app, copy, symlinks=True)
        mutate(copy)
        code, reply = helper_self_test(copy)
        shutil.rmtree(copy)
        return code != 0 and reply.get('error_code') == expect_code, reply

    def flip_byte(copy):
        core = copy / 'Contents/Resources/asterlink-core'
        data = bytearray(core.read_bytes())
        data[-1] ^= 0xFF
        core.write_bytes(bytes(data))

    ok, reply = mutated('tampered', flip_byte, 'worker_invalid')
    record('S01-HASH', 'modified worker is refused', reply.get('error_code', 'started!'), 'pass' if ok else 'fail')

    def resign_other_identity(copy):
        core = copy / 'Contents/Resources/asterlink-core'
        subprocess.run(['codesign', '--force', '--sign', '-', '--identifier', 'com.example.evil', str(core)], check=True, capture_output=True)
        manifest = copy / 'Contents/Resources/worker-manifest.json'
        import hashlib
        data = json.loads(manifest.read_text())
        data['sha256'] = hashlib.sha256(core.read_bytes()).hexdigest()  # attacker keeps the manifest consistent
        manifest.write_text(json.dumps(data))

    ok, reply = mutated('wrong-identity', resign_other_identity, 'worker_invalid')
    record('S01-SIG', 'worker with a different code-signing identity is refused even with a matching hash', reply.get('error_code', 'started!'), 'pass' if ok else 'fail')

    def unsafe_version(copy):
        manifest = copy / 'Contents/Resources/worker-manifest.json'
        data = json.loads(manifest.read_text())
        data['version'] = '../../escape'
        manifest.write_text(json.dumps(data))

    ok, reply = mutated('path-escape', unsafe_version, 'worker_invalid')
    record('S01-PATH', 'path traversal in version is refused', reply.get('error_code', 'started!'), 'pass' if ok else 'fail')

    helper_plist = app / 'Contents/Library/LaunchDaemons/com.asterlink.vpn.helper.plist'
    text = sh(['plutil', '-p', str(helper_plist)]).stdout
    ok = 'BundleProgram' in text and 'Contents/MacOS/AsterLinkHelper' in text and 'com.asterlink.vpn.helper' in text
    record('S-PLIST', 'launchd plist uses BundleProgram + one Mach service', 'present' if ok else text[:120], 'pass' if ok else 'fail')


def suite_tun(allow):
    reasons = []
    if not allow:
        reasons.append('--allow-network-changes not given')
    if os.geteuid() != 0:
        reasons.append('not running as root (utun needs root)')
    for key in ('ASTERLINK_POC_SS_PASSWORD', 'ASTERLINK_POC_SS_SERVER', 'ASTERLINK_POC_SS_PORT'):
        if not os.environ.get(key):
            reasons.append(key + ' not set')
    if reasons:
        record('T01', 'real TUN carries a no-proxy TCP probe and leaves nothing behind', '; '.join(reasons), 'blocked')
        return
    run_id = time.strftime('%Y%m%d-%H%M%S')
    output = ROOT / 'artifacts/native-macos' / ('tun-' + run_id)
    binary = Path(tempfile.mkdtemp(prefix='tunpoc-')) / 'tunpoc'
    build = sh(['go', 'build', '-o', str(binary), '.'], cwd=ROOT / 'native/tunpoc', env=dict(os.environ, CGO_ENABLED='0'))
    if build.returncode != 0:
        record('T01', 'tunpoc builds', build.stderr[-300:], 'fail')
        return
    command = [str(binary), '--allow-network-changes', '--server', os.environ['ASTERLINK_POC_SS_SERVER'],
               '--port', os.environ['ASTERLINK_POC_SS_PORT'], '--output', str(output)]
    if os.environ.get('ASTERLINK_POC_EXPECT_EXIT'):
        command += ['--expect-exit', os.environ['ASTERLINK_POC_EXPECT_EXIT']]
    result = subprocess.run(command, capture_output=True, text=True)
    try:
        report = json.loads((output / 'tun-poc-report.json').read_text())
    except (OSError, ValueError):
        report = {}
    ok = result.returncode == 0 and report.get('pass')
    record('T01', 'exit address changes without proxy settings; no utun/route/DNS left after shutdown',
           'exit_changed=%s leftovers=%s problems=%s' % (report.get('exit_changed'), report.get('cleanup_leftovers'), report.get('problems')),
           'pass' if ok else 'fail', str(output))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--suite', choices=['unit', 'bundle', 'tun'], required=True)
    parser.add_argument('--allow-network-changes', action='store_true')
    parser.add_argument('--run-id', default=time.strftime('%Y%m%d-%H%M%S'))
    args = parser.parse_args()
    if args.suite == 'unit':
        suite_unit()
    elif args.suite == 'bundle':
        suite_bundle(args.run_id)
    else:
        suite_tun(args.allow_network_changes)
    report_dir = ROOT / 'artifacts/native-macos' / (args.run_id + '-report-' + args.suite)
    report_dir.mkdir(parents=True, exist_ok=True)
    (report_dir / 'test-report.json').write_text(json.dumps({'suite': args.suite, 'machine': os.uname().machine, 'cases': RESULTS}, indent=2))
    failed = [r for r in RESULTS if r['result'] == 'fail']
    blocked = [r for r in RESULTS if r['result'] == 'blocked']
    print('%d cases: %d pass, %d fail, %d blocked' % (len(RESULTS), len(RESULTS) - len(failed) - len(blocked), len(failed), len(blocked)))
    sys.exit(1 if failed else 3 if blocked else 0)


if __name__ == '__main__':
    main()

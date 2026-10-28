#!/usr/bin/env python3
"""Single entry for native macOS client tests.

    python3 scripts/test-native-macos.py --suite unit
    python3 scripts/test-native-macos.py --suite bundle          # builds a Debug app, no network changes
    python3 scripts/test-native-macos.py --suite release         # builds + packages the internal ad-hoc DMG and audits it
    python3 scripts/test-native-macos.py --suite ui              # design preview: every scenario to PNG + runtime copy guard
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
sys.path.insert(0, str(Path(__file__).resolve().parent))
RESULTS = []


def record(case_id, expected, observed, result, evidence=''):
    RESULTS.append({'case_id': case_id, 'expected': expected, 'observed': observed, 'result': result, 'evidence': evidence})
    print('%-8s %-6s %s' % (case_id, result.upper(), observed), flush=True)


def sh(command, cwd=ROOT, env=None, check=False):
    return subprocess.run(command, cwd=cwd, env=env, capture_output=True, text=True, check=check)


def count_tests(text, pattern):
    return len(re.findall(pattern, text))


def suite_unit():
    result = sh([sys.executable, 'scripts/test-mihomo-patches.py'])
    record('U-MIHOMO-PATCH', 'hash-locked patch and real NAT lifecycle regressions pass',
           'passed' if result.returncode == 0 else (result.stdout + result.stderr)[-1200:],
           'pass' if result.returncode == 0 else 'fail')
    go_env = dict(os.environ, CGO_ENABLED='0')
    for case_id, directory in (('U-GO-IPC', 'native/core'), ('U-GO-POLICY', 'shared/nodepolicy'), ('U-GO-NODEAGENT', 'native/nodeagent')):
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
    # S02: no production log/print statement may mention a credential-bearing value.
    log_call = re.compile(r'\b(os\.Stderr|log\.(Print|Fatal|Panic)\w*|slog\.\w+|NSLog|os_log|Logger\(|print\(|debugPrint\()')
    sensitive = re.compile(r'password|token|yaml|secret|passwd|uuid|credential', re.IGNORECASE)
    offenders, scanned = [], 0
    for base, suffix in ((ROOT / 'native/core', '.go'), (ROOT / 'native/nodeagent', '.go'), (ROOT / 'apps/macos/Sources', '.swift')):
        for path in base.rglob('*' + suffix):
            if path.name.endswith('_test.go') or 'asterlink-tuntest' in path.parts:
                continue
            scanned += 1
            for number, line in enumerate(path.read_text(errors='replace').splitlines(), 1):
                if log_call.search(line) and sensitive.search(line):
                    offenders.append('%s:%d' % (path.relative_to(ROOT), number))
    record('U-LOGLINT', 'no production log/print line mentions password/token/yaml/secret/uuid', '%d files, offenders: %s' % (scanned, ', '.join(offenders) or 'none'),
           'fail' if offenders else 'pass')
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


# The design preview scenarios (handoff_app_standardization.md 5.10 and the round 3 list in section 10).
UI_SCENARIOS = [
    'login-light', 'login-dark', 'login-service-setup',
    'connect-ready', 'connect-no-line', 'connect-connected-light', 'connect-connected-dark', 'connect-no-plan',
    'connect-plan-expired', 'connect-quota-exhausted', 'connect-failed', 'connect-minimum-size',
    'lines-light', 'lines-dark', 'lines-empty', 'lines-no-match', 'lines-connected', 'lines-minimum-size',
    'account-light', 'account-dark', 'account-service-problem', 'account-service-not-allowed',
    'menubar-signed-out-light', 'menubar-signed-out-dark', 'menubar-ready-light', 'menubar-ready-dark',
    'menubar-connected-light', 'menubar-connected-dark',
]
# Words each scenario must show: proves the scripted state reached the screen, not just that a PNG exists.
UI_EXPECT = {
    'login-light': ['登录 AsterLink', '记住密码'], 'login-service-setup': ['首次使用：允许 AsterLink 在后台运行', '启用'],
    'connect-ready': ['未连接', '香港专线 01'], 'connect-no-line': ['请先选择一条线路'],
    'connect-connected-light': ['已连接', '实时速率', '本次已用'], 'connect-no-plan': ['当前账号没有有效套餐，请前往网站购买', '购买套餐'],
    'connect-plan-expired': ['套餐已到期，请前往网站续费'], 'connect-quota-exhausted': ['套餐流量已用完，请前往网站续费或升级'],
    'connect-failed': ['无法连接', '连接失败，请更换线路或稍后重试，或联系客服'],
    'lines-light': ['香港 · 3', '已选'], 'lines-empty': ['暂无可用线路，请稍后刷新'], 'lines-no-match': ['没有符合条件的线路'],
    'account-light': ['后台服务运行正常', '复制诊断信息', '退出 AsterLink'],
    'account-service-problem': ['后台服务需要更新，请在“账户”页重新安装后台服务', '重新安装'],
    'account-service-not-allowed': ['需要在系统设置中允许 AsterLink 在后台运行'],
    'menubar-signed-out-light': ['未登录', '打开 AsterLink 登录'], 'menubar-ready-light': ['连接', '当前线路', '剩余流量'],
    'menubar-connected-light': ['已连接', '断开'],
}


def copy_guard_words():
    """The word lists of scripts/test-app-copy.py (one source for the static and the runtime guard)."""
    import importlib.util
    spec = importlib.util.spec_from_file_location('app_copy', ROOT / 'scripts/test-app-copy.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.FORBIDDEN_ZH, module.FORBIDDEN_EN + module.FORBIDDEN_DESKTOP_EN


def suite_ui(run_id):
    """Builds the Debug app with SwiftPM and runs its design preview (`AsterLink --preview DIR`): every
    scenario rendered to PNG plus the text of its accessibility tree, which the copy guard checks."""
    macos = ROOT / 'apps/macos'
    build = sh(['swift', 'build', '--product', 'AsterLink'], cwd=macos)
    if build.returncode != 0:
        record('UI-BUILD', 'Debug app builds', 'build failed', 'fail', (build.stdout + build.stderr)[-800:])
        return
    binary = Path(sh(['swift', 'build', '--show-bin-path'], cwd=macos).stdout.strip()) / 'AsterLink'
    out = ROOT / 'artifacts/native-macos' / (run_id + '-ui')
    shutil.rmtree(out, ignore_errors=True)
    try:
        result = subprocess.run([str(binary), '--preview', str(out)], capture_output=True, text=True, timeout=600)
    except subprocess.TimeoutExpired:
        record('UI-RENDER', 'preview renders every scenario', 'timed out after 600 s', 'fail')
        return
    manifest_path = out / 'manifest.json'
    if result.returncode != 0 or not manifest_path.is_file():
        record('UI-RENDER', 'preview renders every scenario', 'exit %d' % result.returncode, 'fail', (result.stdout + result.stderr)[-800:])
        return
    manifest = json.loads(manifest_path.read_text())['scenarios']
    texts = json.loads((out / 'texts.json').read_text())
    rendered = [entry['scenario'] for entry in manifest]
    missing = [name for name in UI_SCENARIOS if name not in rendered]
    extra = [name for name in rendered if name not in UI_SCENARIOS]
    bad_png = []
    for entry in manifest:
        data = (out / entry['file']).read_bytes() if (out / entry['file']).is_file() else b''
        if not data.startswith(b'\x89PNG\r\n\x1a\n') or len(data) < 10_000:
            bad_png.append(entry['scenario'])
    ok = not missing and not extra and not bad_png
    record('UI-RENDER', 'all %d scenarios render to a real PNG (light and dark, minimum size, menu bar panel)' % len(UI_SCENARIOS),
           '%d rendered; missing %s; unexpected %s; bad PNG %s' % (len(rendered), missing or '-', extra or '-', bad_png or '-'),
           'pass' if ok else 'fail', str(out))

    sizes = {entry['scenario']: (entry['width'], entry['height']) for entry in manifest}
    # 820 x 560 of content; the window frame adds the title bar on top.
    small = [name for name in rendered if 'minimum-size' in name
             and not (sizes[name][0] == 820 and 560 <= sizes[name][1] <= 600)]
    record('UI-SIZE', 'minimum-size scenarios are drawn at the 820 x 560 minimum', 'ok' if not small else 'wrong size: %s' % small,
           'fail' if small else 'pass')

    absent = []
    for name, words in UI_EXPECT.items():
        seen = '\n'.join(texts.get(name, []))
        absent += ['%s: %s' % (name, word) for word in words if word not in seen]
    empty = [name for name in rendered if len(texts.get(name, [])) < 3]
    record('UI-TEXT', 'each scenario exposes its text to the accessibility tree, with the expected words',
           'missing: %s; empty: %s' % (absent or '-', empty or '-'), 'fail' if absent or empty else 'pass')

    zh, en = copy_guard_words()
    hits = []
    for name, strings in sorted(texts.items()):
        for text in strings:
            for word in zh:
                if re.search(word, text):
                    hits.append('%s: "%s" in %r' % (name, word, text))
            for word in en:
                if re.search(r'(?<![A-Za-z])%s(?![a-z])' % re.escape(word), text):
                    hits.append('%s: "%s" in %r' % (name, word, text))
    total = sum(len(v) for v in texts.values())
    record('UI-COPY', 'no forbidden word in any visible text (runtime copy guard, same lists as test-app-copy.py)',
           '%d strings, %d hit(s)%s' % (total, len(hits), (': ' + '; '.join(hits[:8])) if hits else ''), 'fail' if hits else 'pass')


def suite_tun(allow):
    """Privileged acceptance of the production engine on a controlled node (stage 3).

    BLOCKED (exit 3) unless: --allow-network-changes, root, and a test node in the
    environment (ASTERLINK_TUN_PROXY_JSON, or ASTERLINK_POC_SS_SERVER/_PORT/_PASSWORD).
    Optional controlled targets widen coverage: ASTERLINK_TUN_DOWNLOAD_URL + _BYTES (counter
    reconciliation), ASTERLINK_TUN_UDP_ECHO (host:port), ASTERLINK_TUN_API_HOST (host:port).
    """
    reasons = []
    if not allow:
        reasons.append('--allow-network-changes not given')
    if os.geteuid() != 0:
        reasons.append('not running as root (utun needs root)')
    if not os.environ.get('ASTERLINK_TUN_PROXY_JSON'):
        for key in ('ASTERLINK_POC_SS_PASSWORD', 'ASTERLINK_POC_SS_SERVER', 'ASTERLINK_POC_SS_PORT'):
            if not os.environ.get(key):
                reasons.append(key + ' not set')
    if reasons:
        record('T01', 'real TUN carries a no-proxy TCP probe and leaves nothing behind', '; '.join(reasons), 'blocked')
        return
    run_id = time.strftime('%Y%m%d-%H%M%S')
    output = ROOT / 'artifacts/native-macos' / ('tun-' + run_id)
    binary = Path(tempfile.mkdtemp(prefix='asterlink-tuntest-')) / 'asterlink-tuntest'
    build = sh(['go', 'build', '-o', str(binary), './cmd/asterlink-tuntest'], cwd=ROOT / 'native/core', env=dict(os.environ, CGO_ENABLED='0'))
    if build.returncode != 0:
        record('T01', 'asterlink-tuntest builds', build.stderr[-300:], 'fail')
        return
    command = [str(binary), '--allow-network-changes', '--output', str(output)]
    for flag, key in (('--expect-exit', 'ASTERLINK_POC_EXPECT_EXIT'), ('--download-url', 'ASTERLINK_TUN_DOWNLOAD_URL'),
                      ('--download-bytes', 'ASTERLINK_TUN_DOWNLOAD_BYTES'), ('--udp-echo', 'ASTERLINK_TUN_UDP_ECHO'),
                      ('--api-host', 'ASTERLINK_TUN_API_HOST')):
        if os.environ.get(key):
            command += [flag, os.environ[key]]
    result = subprocess.run(command, text=True)
    try:
        report = json.loads((output / 'tun-report.json').read_text())
    except (OSError, ValueError):
        record('T01', 'harness produced a report', 'no report (exit %d)' % result.returncode, 'fail')
        return
    for case in report.get('cases', []):
        record(case['case_id'], case['expected'], case['observed'], case['result'], str(output))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--suite', choices=['unit', 'bundle', 'release', 'soak', 'tun', 'ui'], required=True)
    parser.add_argument('--allow-network-changes', action='store_true')
    parser.add_argument('--build-run-id', help='release suite: audit an existing build/package instead of building one')
    parser.add_argument('--soak-minutes', type=float, default=30.0)
    parser.add_argument('--soak-interval', type=float, default=5.0)
    parser.add_argument('--max-rss-growth-pct', type=float, default=20.0, help='soak: fail if resident memory grows more than this')
    parser.add_argument('--run-id', default=time.strftime('%Y%m%d-%H%M%S'))
    args = parser.parse_args()
    if args.suite != 'ui':  # the preview builds only the Swift app; it needs no Mihomo tree
        import prepare_mihomo
        prepare_mihomo.prepare()
    if args.suite == 'unit':
        suite_unit()
    elif args.suite == 'soak':
        import soak_suite
        soak_suite.run({'record': record}, args.soak_minutes, args.soak_interval, args.max_rss_growth_pct,
                       ROOT / 'artifacts/native-macos' / (args.run_id + '-soak'))
    elif args.suite == 'release':
        import release_suite
        release_suite.run({'record': record, 'sh': sh, 'ROOT': ROOT}, args.run_id, args.build_run_id)
    elif args.suite == 'bundle':
        suite_bundle(args.run_id)
    elif args.suite == 'ui':
        suite_ui(args.run_id)
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

#!/usr/bin/env python3
"""Windows client test entry point.

Round 1 implements only the `foundation` suite, which needs no Windows machine:
  * apps/windows/toolchain.lock.json agrees with global.json and the CI workflow
  * apps/windows/.env.example is accepted by the Python parser
  * the C# contract suite runs and reports a non-zero test count

Exit codes: 0 = PASS, 1 = FAIL, 3 = BLOCKED (a prerequisite is missing, so nothing was proven).
Suites that need a real Windows host (unit/service/tun/...) arrive in later rounds and refuse
to run here instead of reporting a pass.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WINDOWS = ROOT / 'apps' / 'windows'
sys.path.insert(0, str(ROOT / 'scripts'))
import native_env  # noqa: E402

EXIT_PASS, EXIT_FAIL, EXIT_BLOCKED = 0, 1, 3
results = []


def record(case_id, ok, observed, status=None):
    status = status or ('PASS' if ok else 'FAIL')
    results.append({'case_id': case_id, 'status': status, 'observed': observed})
    print('%-8s %-28s %s' % (status, case_id, observed))


def check_lock_file():
    lock = json.loads((WINDOWS / 'toolchain.lock.json').read_text())
    sdk = json.loads((WINDOWS / 'global.json').read_text())['sdk']['version']
    record('lock.sdk', lock['dotnet']['sdk'] == sdk,
           'lock=%s global.json=%s' % (lock['dotnet']['sdk'], sdk))

    workflow = (ROOT / '.github' / 'workflows' / 'windows.yml').read_text()
    label = lock['ci']['runner_label']
    record('lock.runner', 'runs-on: ' + label in workflow and 'windows-latest' not in workflow,
           'workflow pins %s and avoids the moving windows-latest alias' % label)

    wintun = lock['wintun']
    record('lock.wintun_hash', bool(re.fullmatch(r'[0-9a-f]{64}', wintun['zip_sha256_published'])),
           'published sha256 recorded; not yet verified against a download (round 2)')

    go_version = lock['go']['version']
    ci = (ROOT / '.github' / 'workflows' / 'ci.yml').read_text()
    record('lock.go', "GO_VERSION: '%s'" % go_version in ci, 'lock=%s ci.yml' % go_version)

    mihomo = subprocess.run(['git', 'ls-tree', 'HEAD', 'native/third_party/Clash.Meta'],
                            cwd=ROOT, capture_output=True, text=True).stdout.split()
    record('lock.mihomo', len(mihomo) >= 3 and mihomo[2] == lock['mihomo']['commit'],
           'lock=%s tree=%s' % (lock['mihomo']['commit'][:12], mihomo[2][:12] if len(mihomo) >= 3 else 'n/a'))


def check_env_example():
    try:
        values = native_env.load_env_file(WINDOWS / '.env.example')
        record('env.example', True, 'accepted: ' + ', '.join(sorted(values)))
    except native_env.EnvError as error:
        record('env.example', False, str(error))
    macos = (ROOT / 'apps' / 'macos' / '.env.example').read_text()
    windows = (WINDOWS / '.env.example').read_text()
    strip = lambda text: [l for l in text.splitlines() if l and not l.startswith('#')]
    record('env.same_keys_as_macos', strip(macos) == strip(windows), 'public build keys identical')


def dotnet_command():
    path = shutil.which('dotnet')
    if path:
        return path
    candidate = Path(os.environ.get('DOTNET_ROOT', '')) / 'dotnet'
    return str(candidate) if candidate.is_file() else None


def run_dotnet_tests():
    dotnet = dotnet_command()
    if not dotnet:
        record('csharp.contract_tests', False, 'dotnet SDK not found; nothing was tested', status='BLOCKED')
        return
    process = subprocess.run([dotnet, 'test', str(WINDOWS / 'AsterLink.Tests'), '-c', 'Release',
                              '--nologo', '--verbosity', 'minimal'],
                             cwd=WINDOWS, capture_output=True, text=True)
    output = process.stdout + process.stderr
    summary = re.search(r'Failed:\s*(\d+), Passed:\s*(\d+), Skipped:\s*(\d+), Total:\s*(\d+)', output)
    if not summary:
        record('csharp.contract_tests', False, 'no test summary in output:\n' + output[-1500:])
        return
    failed, passed, skipped, total = map(int, summary.groups())
    ok = process.returncode == 0 and failed == 0 and total > 0 and passed == total - skipped
    record('csharp.contract_tests', ok, 'passed=%d failed=%d skipped=%d total=%d' % (passed, failed, skipped, total))
    if not ok:
        print(output[-3000:])


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--suite', required=True)
    parser.add_argument('--report', help='write a JSON report here')
    args = parser.parse_args()

    if args.suite != 'foundation':
        print('suite %r needs a Windows x64 host and is implemented in a later round' % args.suite)
        return EXIT_BLOCKED

    check_lock_file()
    check_env_example()
    run_dotnet_tests()

    if args.report:
        Path(args.report).write_text(json.dumps(results, indent=2))
    statuses = {r['status'] for r in results}
    print('\n%d cases: %s' % (len(results), ', '.join('%s=%d' % (s, sum(r['status'] == s for r in results)) for s in sorted(statuses))))
    if 'FAIL' in statuses:
        return EXIT_FAIL
    return EXIT_BLOCKED if 'BLOCKED' in statuses else EXIT_PASS


if __name__ == '__main__':
    sys.exit(main())

#!/usr/bin/env python3
"""Compile/test only tracked backend files in an unrelated renamed directory, without FlClash."""
from pathlib import Path
import hashlib
import json
import os
import shutil
import stat
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main() -> None:
    # No database secrets, DSNs, compiler flag injection, workspace or user Go
    # settings. Dependencies must already be downloaded by the caller/CI.
    allowed = ('PATH', 'HOME', 'TMPDIR', 'GOCACHE', 'GOMODCACHE', 'GOPATH',
               'SYSTEMROOT', 'DEVELOPER_DIR', 'SDKROOT')
    env = {key: os.environ[key] for key in allowed if key in os.environ}
    env.update(GOWORK='off', GOENV='off', GOTOOLCHAIN='local', GOPROXY='off',
               GOFLAGS='-mod=readonly', RUN_DB_TESTS='0', NATIVE_TEST_DSN='',
               CGO_ENABLED='1', LC_ALL='C', LANG='C')
    if shutil.which('go', path=env.get('PATH')) is None:
        raise SystemExit('Go must be on PATH')
    records = subprocess.check_output(['git', 'ls-files', '--stage', '-z', '--', 'backend'], cwd=ROOT)
    with tempfile.TemporaryDirectory(prefix='vpn-backend-only-') as temporary:
        # macOS /var may alias /private/var; absolute-file builds must use
        # the same physical path as Go's resolved module root.
        stage = (Path(temporary) / 'test.hyshentou.cn_backend').resolve()
        stage.mkdir()
        count = 0
        for record in records.split(b'\0'):
            if not record:
                continue
            metadata, name = record.decode('utf-8').split('\t', 1)
            mode, _sha, index_stage = metadata.split()
            relative = Path(name).relative_to('backend')
            if mode not in ('100644', '100755') or index_stage != '0':
                raise RuntimeError('Only tracked regular files are allowed: ' + name)
            if any(part == '.git' or (part.startswith('.env') and part != '.env.example')
                   for part in relative.parts):
                raise RuntimeError('Private configuration must never be staged: ' + name)
            source = ROOT / name
            if source.is_symlink() or not stat.S_ISREG(source.stat().st_mode):
                raise RuntimeError('Non-regular backend source: ' + name)
            destination = stage / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, destination)
            count += 1
        if not (stage / 'nodepolicy/policy.go').is_file():
            raise RuntimeError('Stage/commit the bundled nodepolicy files before running this check')
        # Only backend is copied. There is no .git, parent FlClash or go.work.
        if (stage.parent / 'FlClash').exists() or (stage / '.env').exists():
            raise RuntimeError('Isolation failed')
        original = {name: hashlib.sha256((stage / name).read_bytes()).hexdigest()
                    for name in ('go.mod', 'go.sum', 'nodepolicy/go.mod')}

        def run(args, directory=stage, environment=env):
            print('+ ' + ' '.join(args), flush=True)
            subprocess.run(args, cwd=directory, env=environment, check=True, timeout=600)

        module = json.loads(subprocess.check_output(
            ['go', 'list', '-m', '-json', 'vpn/nodepolicy'], cwd=stage, env=env))
        if Path(module['Replace']['Dir']).resolve() != (stage / 'nodepolicy').resolve():
            raise RuntimeError('nodepolicy resolved outside standalone backend')
        run(['go', 'test', '-race', '-count=1', './...'])
        run(['go', 'vet', './...'])
        run(['go', 'test', '-race', '-count=1', './...'], stage / 'nodepolicy')
        run(['go', 'vet', './...'], stage / 'nodepolicy')
        out = stage / 'bin'
        out.mkdir()
        run(['go', 'build', '-trimpath', '-o', str(out / 'api'), './cmd/api'])
        run(['go', 'build', '-trimpath', '-o', str(out / 'doctor'), './cmd/doctor'])
        # Reproduce the user's absolute main.go invocation too.
        run(['go', 'build', '-o', str(out / 'api-file'), str(stage / 'cmd/api/main.go')])
        for arch in ('amd64', 'arm64'):
            linux = dict(env, CGO_ENABLED='0', GOOS='linux', GOARCH=arch)
            run(['go', 'build', '-trimpath', '-o', str(out / ('api-linux-' + arch)), './cmd/api'], environment=linux)
            run(['go', 'build', '-trimpath', '-o', str(out / ('doctor-linux-' + arch)), './cmd/doctor'], environment=linux)
        for name, digest in original.items():
            if hashlib.sha256((stage / name).read_bytes()).hexdigest() != digest:
                raise RuntimeError('Go modified a locked module file: ' + name)
        print('PASS: %d backend-only files; tests/vet, package and absolute-file builds, Linux amd64/arm64; no database or sibling checkout' % count)


if __name__ == '__main__':
    main()

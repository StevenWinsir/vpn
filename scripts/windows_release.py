"""Release-folder helpers for the Windows client (round 8): signing, the installer, checksums, the dependency
inventory, notices and the corresponding-source archives. Imported by scripts/package-native-windows.py.

Nothing here decides that something is releasable. It produces files and records exactly what was and was not
done (signing mode, whether the signature can be trusted, which gates are still open).
"""
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WINDOWS = ROOT / 'apps' / 'windows'
CORE = ROOT / 'native' / 'core'
MIHOMO = ROOT / 'native' / 'third_party' / 'Clash.Meta'
SIGN_SCRIPT = ROOT / 'scripts' / 'windows' / 'sign-files.ps1'
SETUP_SCRIPT = WINDOWS / 'Packaging' / 'setup.iss'

SIGN_MODES = ('none', 'test', 'thumbprint', 'pfx')


def run(cmd, **kwargs):
    print('+', ' '.join(str(c) for c in cmd), flush=True)
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def capture(cmd, **kwargs):
    return subprocess.run([str(c) for c in cmd], check=True, capture_output=True, text=True, **kwargs).stdout.strip()


def sha256(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        for block in iter(lambda: handle.read(1 << 20), b''):
            digest.update(block)
    return digest.hexdigest()


def powershell():
    for name in ('pwsh', 'powershell'):
        found = shutil.which(name)
        if found:
            return found
    sys.exit('PowerShell is required for signing')


# ---- signing -----------------------------------------------------------------------------------

def own_binaries(stage):
    """Our own executables and libraries. Microsoft's runtime files are already signed by Microsoft and stay as shipped."""
    files = []
    for top, names in (('app', ('AsterLink*.exe', 'AsterLink*.dll')), ('service', ('AsterLink*.exe', 'AsterLink*.dll')), ('core', ('asterlink-core.exe',))):
        for pattern in names:
            files += sorted((stage / top).glob(pattern))
    return files


def sign(files, mode):
    if mode == 'none':
        return
    if sys.platform != 'win32':
        sys.exit('Authenticode signing needs Windows (signing mode %r)' % mode)
    files = [str(f) for f in files]
    run([powershell(), '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', SIGN_SCRIPT, '-Mode', mode, '-Files', ','.join(files)])


# ---- installer ---------------------------------------------------------------------------------

def find_iscc():
    candidates = [os.environ.get('ISCC'), shutil.which('ISCC'), shutil.which('iscc')]
    for base in (os.environ.get('ProgramFiles(x86)'), os.environ.get('ProgramFiles'), os.environ.get('LOCALAPPDATA')):
        if base:
            candidates += [str(Path(base) / 'Inno Setup 6' / 'ISCC.exe'), str(Path(base) / 'Programs' / 'Inno Setup 6' / 'ISCC.exe')]
    for candidate in candidates:
        if candidate and Path(candidate).is_file():
            return candidate
    sys.exit('ISCC.exe (Inno Setup 6.7.x) was not found; set ISCC or install it (docs/windows-release.md)')


def short_path(path):
    """The 8.3 form of an existing Windows path (unchanged if it has no spaces or 8.3 names are off)."""
    text = str(path)
    if ' ' not in text or sys.platform != 'win32':
        return text
    import ctypes
    buffer = ctypes.create_unicode_buffer(1024)
    if ctypes.windll.kernel32.GetShortPathNameW(text, buffer, 1024):
        return buffer.value
    return text


def build_installer(stage, out, version, sign_mode):
    out.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, ASTERLINK_VERSION=version, ASTERLINK_STAGE=str(stage), ASTERLINK_OUTPUT=str(out))
    cmd = [find_iscc(), '/Qp']
    if sign_mode != 'none':
        # ISCC splits its own /S argument and cannot take paths with spaces or quotes, so the sign tool is a small .cmd
        # wrapper reached through a space-free (8.3) path. Inno substitutes $f with the quoted file name; the setup and
        # the uninstaller are both signed this way.
        wrapper_dir = Path(tempfile.mkdtemp(prefix='asterlink-sign-'))
        wrapper = wrapper_dir / 'sign-tool.cmd'
        wrapper.write_text('@echo off\r\n"%s" -NoProfile -ExecutionPolicy Bypass -File "%s" -Mode %s -Files %%1\r\nexit /b %%ERRORLEVEL%%\r\n'
                           % (powershell(), SIGN_SCRIPT, sign_mode))
        cmd += ['/DSign=1', '/Sasterlink=' + short_path(wrapper) + ' $f']
    cmd.append(str(SETUP_SCRIPT))
    try:
        run(cmd, env=env)
    finally:
        if sign_mode != 'none':
            shutil.rmtree(wrapper_dir, ignore_errors=True)
    setup = out / ('AsterLink-Setup-x64-%s.exe' % version)
    if not setup.is_file():
        sys.exit('ISCC finished but %s does not exist' % setup)
    return setup


# ---- inventory, notices, source ----------------------------------------------------------------

def go_modules():
    env = dict(os.environ, GOOS='windows', GOARCH='amd64', GOAMD64='v1', CGO_ENABLED='0')
    text = capture(['go', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}{{end}}', './cmd/asterlink-core'], cwd=CORE, env=env)
    modules = {}
    for line in text.splitlines():
        path, _, version = line.partition('\t')
        if path:
            modules[path] = version
    return modules


def dependency_inventory(stage, version, commit):
    """A CycloneDX-shaped list of what is inside the package. NuGet has no third-party packages (project references only);
    the .NET runtime is listed from the published runtimeconfig."""
    lock = json.loads((WINDOWS / 'toolchain.lock.json').read_text())
    components = []
    for path, ver in sorted(go_modules().items()):
        if path.startswith('github.com/StevenWinsir') or path == 'asterlink/core':
            continue
        components.append({'type': 'library', 'name': path, 'version': ver or 'local', 'purl': 'pkg:golang/%s@%s' % (path, ver) if ver else None, 'scope': 'required', 'origin': 'asterlink-core.exe'})
    runtime = None
    for config in sorted((stage / 'app').glob('*.runtimeconfig.json')):
        runtime = json.loads(config.read_text()).get('runtimeOptions', {}).get('includedFrameworks', [])
    components.append({'type': 'framework', 'name': '.NET runtime (self-contained)', 'version': lock['dotnet']['runtime'], 'origin': 'app/, service/', 'frameworks': runtime})
    components.append({'type': 'library', 'name': 'Wintun', 'version': lock['wintun']['version'], 'hashes': [{'alg': 'SHA-256', 'content': lock['wintun']['amd64_dll_sha256']}], 'origin': 'embedded in asterlink-core.exe by sing-tun'})
    components.append({'type': 'application', 'name': 'Mihomo (Clash.Meta fork)', 'version': lock['mihomo']['commit'], 'origin': 'embedded in asterlink-core.exe', 'license': 'GPL-3.0'})
    components.append({'type': 'application', 'name': 'Inno Setup', 'version': os.environ.get('ASTERLINK_INNO_VERSION', 'see toolchain.lock.json'), 'origin': 'build tool, not shipped; the installer carries its runtime stub'})
    return {
        'bomFormat': 'CycloneDX', 'specVersion': '1.5', 'version': 1,
        'metadata': {'component': {'type': 'application', 'name': 'AsterLink for Windows x64', 'version': version}, 'properties': [{'name': 'source_commit', 'value': commit}]},
        'components': [{k: v for k, v in c.items() if v is not None} for c in components],
    }


def write_notices(out):
    sys.path.insert(0, str(ROOT / 'scripts'))
    import collect_licenses
    text, missing, count = collect_licenses.collect('windows', 'amd64')
    text += ('\n' + '=' * 78 + '\nWintun\n' + '=' * 78 +
             '\nWintun is embedded unmodified in asterlink-core.exe (via github.com/metacubex/sing-tun). Copyright (C) 2018-2021 WireGuard LLC.\n'
             'Prebuilt signed binaries are distributed under the "Prebuilt Binaries License" published at https://www.wintun.net/ ; its terms on\n'
             'redistribution must be confirmed by the legal review before a public release (docs/windows-release.md, section 7).\n')
    (out / 'THIRD_PARTY_LICENSES.txt').write_text(text, encoding='utf-8')
    return missing, count


def write_source_notice(out, commit, mihomo_commit, dirty):
    (out / 'SOURCE.txt').write_text(
        'AsterLink for Windows x64 - corresponding source\n\n'
        'asterlink-core.exe embeds Mihomo (Clash.Meta fork, GNU GPL v3).\n'
        'This repository commit:  %s%s\nMihomo commit:           %s\n\n'
        'The two tar.gz archives next to this file are `git archive` outputs of exactly these commits.\n'
        'To rebuild the Core:  unpack both, place the mihomo tree at native/third_party/Clash.Meta, then in native/core run\n'
        '  GOOS=windows GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=<version>" ./cmd/asterlink-core\n'
        'The Go toolchain and dependency versions are pinned in apps/windows/toolchain.lock.json and native/core/go.sum.\n'
        % (commit, ' (WORKING TREE HAD UNCOMMITTED CHANGES)' if dirty else '', mihomo_commit), encoding='utf-8')


def source_archives(out, commit):
    archives = []
    main = out / ('asterlink-source-%s.tar.gz' % commit[:12])
    run(['git', 'archive', '--format=tar.gz', '--prefix=asterlink/', '-o', main, 'HEAD'], cwd=ROOT)
    archives.append(main)
    if not (MIHOMO / '.git').exists():
        sys.exit('the Mihomo submodule is not checked out (git submodule update --init); the GPL source archive cannot be made')
    mihomo_commit = capture(['git', 'rev-parse', 'HEAD'], cwd=MIHOMO)
    mihomo = out / ('mihomo-source-%s.tar.gz' % mihomo_commit[:12])
    run(['git', 'archive', '--format=tar.gz', '--prefix=mihomo/', '-o', mihomo, 'HEAD'], cwd=MIHOMO)
    archives.append(mihomo)
    return archives, mihomo_commit


def source_state():
    commit = capture(['git', 'rev-parse', 'HEAD'], cwd=ROOT)
    dirty = bool(capture(['git', 'status', '--porcelain'], cwd=ROOT))
    return commit, dirty


# ---- checksums ---------------------------------------------------------------------------------

def write_checksums(out):
    lines = []
    for path in sorted(out.iterdir()):
        if path.is_file() and path.name != 'SHA256SUMS':
            lines.append('%s *%s' % (sha256(path), path.name))
    (out / 'SHA256SUMS').write_text('\n'.join(lines) + '\n', encoding='utf-8')
    return lines


def verify_checksums(out):
    """Independent re-read of SHA256SUMS; returns a list of problems."""
    problems = []
    listed = set()
    for line in (out / 'SHA256SUMS').read_text(encoding='utf-8').splitlines():
        match = re.fullmatch(r'([0-9a-f]{64}) \*(.+)', line)
        if not match:
            problems.append('malformed line: %r' % line)
            continue
        digest, name = match.groups()
        listed.add(name)
        target = out / name
        if not target.is_file():
            problems.append('missing: ' + name)
        elif sha256(target) != digest:
            problems.append('hash mismatch: ' + name)
    for path in out.iterdir():
        if path.is_file() and path.name != 'SHA256SUMS' and path.name not in listed:
            problems.append('not listed: ' + path.name)
    return problems


def tar_is_readable(path):
    with tarfile.open(path) as archive:
        return len(archive.getnames()) > 0

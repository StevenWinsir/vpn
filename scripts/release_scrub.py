#!/usr/bin/env python3
"""Release-package scrub (handoff_app_standardization.md section 8, D15/D16): shared by the four platform audits.

What a customer receives (the public release folder and everything inside an installer or app package) must not carry
licence texts, source notices, source archives, component inventories, release information or the engine revision. Those
files keep their content and live in the platform's internal `<release folder>-private` directory instead.

    python3 scripts/release_scrub.py --self-test          # the audit catches planted leaks (CI, native-core job)
    python3 scripts/release_scrub.py --scan <dir> --platform macos   # report what a tree contains

Text files (and binary property lists, decoded) must contain none of TEXT_MARKERS. Binaries may contain the residue that
D16 accepts and nothing else: release-scrub-baseline.json lists, per platform and per path pattern, which markers a binary
may carry (Go package paths inside the Core, the IPC field name `mihomo_revision` in the clients). A marker in a binary the
baseline does not name, or a marker the baseline does not allow for it, fails. The pinned engine revision is never allowed.
Only the standard library is used, so the scanner runs on Linux, macOS and Windows alike.
"""
import argparse
import fnmatch
import json
import os
import plistlib
import re
import shutil
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BASELINE = ROOT / 'scripts' / 'release-scrub-baseline.json'

# Files that belong in the private folder only (any platform). The source archives are matched by pattern.
PRIVATE_ONLY_NAMES = ('THIRD_PARTY_LICENSES.txt', 'SOURCE.txt', 'dependencies.cdx.json', 'release-info.json',
                      'release-manifest.json', 'release.json', 'manifest.json', 'build-manifest.json')
SOURCE_ARCHIVE = re.compile(r'.*-source-[0-9a-f]+\.tar\.gz$')
PRIVATE_NOTE = 'README-内部资料.txt'
PRIVATE_NOTE_TEXT = (
    '内部资料，不得发布。\n\n'
    '本目录保存与同一次构建对应的许可文本、源码说明、源码归档、组件清单与发布信息，供编写纸质说明书、版本追溯与崩溃分析使用。\n'
    '不要上传到官网，不要随安装包分发，也不要转发给用户。对外发布只使用同级公开目录中的安装包与 SHA256SUMS。\n'
)

# Case-insensitive words, plus GPL as a whole word (case-sensitive: "gpl" inside ordinary words is not a licence name).
TEXT_WORDS = ('mihomo', 'clash', 'metacubex', 'sing-tun', 'gnu general public')
TEXT_GPL = re.compile(r'(?<![A-Za-z])GPL(?![A-Za-z])')
# Binaries: byte markers of five bytes or more, so random machine code cannot match by chance (a three-byte "GPL" would,
# about twice per 40 MB). Each is searched as ASCII/UTF-8 and as UTF-16LE (.NET string literals).
BINARY_MARKERS = ('mihomo', 'Mihomo', 'MIHOMO', 'clash', 'Clash', 'metacubex', 'sing-tun', 'GNU GENERAL PUBLIC')
REVISION_MARKER = 'engine-revision'
TEXT_SUFFIXES = {'.txt', '.json', '.plist', '.xml', '.strings', '.md', '.html', '.htm', '.js', '.css', '.config', '.ini',
                 '.command', '.sh', '.xcprivacy', '.properties', '.version', '.pro', '.isl', '.iss', '.yml', '.yaml'}


def pinned_revision():
    """The engine commit the build pins (the gitlink). Searched for everywhere; allowed nowhere."""
    try:
        line = subprocess.run(['git', 'ls-files', '-s', 'native/third_party/Clash.Meta'], cwd=ROOT, capture_output=True,
                              text=True, check=True).stdout
    except (OSError, subprocess.CalledProcessError):
        return None
    match = re.match(r'160000 ([0-9a-f]{40}) ', line)
    return match.group(1) if match else None


NEUTRAL_CHECKOUT = Path('/tmp/asterlink-src')


def neutral_checkout():
    """(this checkout's native/core seen through NEUTRAL_CHECKOUT, environment) for gomobile bind.

    gomobile binds through a scratch module whose replace directives are absolute, and Go writes those directories into
    the module information of the binary even with -trimpath: the build machine's home and checkout path would ship in
    every app. Binding through a fixed symlink (and a PWD naming it, which Go's working-directory lookup honours) puts a
    neutral path there instead, the same on every machine. Windows has no such step (no gomobile)."""
    if os.name == 'nt':
        return ROOT / 'native' / 'core', {}
    link = NEUTRAL_CHECKOUT
    if link.is_symlink() and Path(os.readlink(link)) != ROOT:
        link.unlink()
    if not link.is_symlink():
        if link.exists():
            raise RuntimeError('%s exists and is not a symlink; remove it so release binds can use it' % link)
        link.symlink_to(ROOT, target_is_directory=True)
    core = link / 'native' / 'core'
    return core, {'PWD': str(core)}


def private_dir(public: Path) -> Path:
    return public.parent / (public.name + '-private')


def write_private_note(private: Path):
    (private / PRIVATE_NOTE).write_text(PRIVATE_NOTE_TEXT, encoding='utf-8')


def _decode_text(name, data):
    """The text of a file that is text (or a binary property list), else None."""
    if data.startswith(b'bplist00'):
        try:
            return json.dumps(plistlib.loads(data), default=str, ensure_ascii=False)
        except Exception:  # an unreadable plist is treated as a binary
            return None
    utf16 = data[:2] in (b'\xff\xfe', b'\xfe\xff')
    if b'\x00' in data[:65536] and not utf16:
        return None
    try:
        return data.decode('utf-16' if utf16 else 'utf-8-sig')
    except UnicodeDecodeError:
        pass
    if Path(name).suffix.lower() in TEXT_SUFFIXES:
        return data.decode('utf-8', errors='replace')
    return None


def text_hits(text, revision=None):
    hits = {}
    lower = text.lower()
    for word in TEXT_WORDS:
        count = lower.count(word)
        if count:
            hits[word] = count
    gpl = len(TEXT_GPL.findall(text))
    if gpl:
        hits['GPL'] = gpl
    if revision and revision in text:
        hits[REVISION_MARKER] = text.count(revision)
    paths = sum(len(pattern.findall(text.encode('utf-8', errors='replace'))) for pattern in _BUILD_PATHS)
    if paths:
        hits[BUILD_PATH_MARKER] = paths
    return hits


def _binary_pattern(marker):
    """A marker that starts a word: not preceded by a letter, so ordinary identifiers that merely end in it (the .NET
    runtime's "Xslt_ScriptAndExtensionClash") are not engine residue. Both ASCII and UTF-16LE."""
    ascii_, wide = re.escape(marker.encode()), re.escape(marker.encode('utf-16-le'))
    return re.compile(rb'(?<![A-Za-z])' + ascii_), re.compile(rb'(?<![A-Za-z]\x00)' + wide)


_BINARY_PATTERNS = {marker: _binary_pattern(marker) for marker in BINARY_MARKERS}
# The directories of the machine that built the package (this checkout and the home folder, e.g. /Users/runner or
# D:\a\vpn\vpn): release builds leave them out (-trimpath, strip -S -x, no PDBs). Allowed nowhere, like the engine
# revision. Third-party build paths inside Microsoft's runtime files are not ours and do not match.
BUILD_PATH_MARKER = 'build-path'


def _build_paths():
    roots = {str(ROOT), str(Path.home())}
    needles = []
    for root in sorted(r for r in roots if len(r) > 3):
        for form in {root, root.replace('\\', '/'), root.replace('/', '\\')}:
            needles += [form.encode(), form.encode('utf-16-le')]
    return [re.compile(re.escape(n)) for n in needles]


_BUILD_PATHS = _build_paths()


def binary_hits(data, revision=None):
    hits = {}
    for marker, patterns in _BINARY_PATTERNS.items():
        count = sum(len(pattern.findall(data)) for pattern in patterns)
        if count:
            hits[marker] = count
    if revision and (revision.encode() in data or revision.encode('utf-16-le') in data):
        hits[REVISION_MARKER] = data.count(revision.encode()) + data.count(revision.encode('utf-16-le'))
    paths = sum(len(pattern.findall(data)) for pattern in _BUILD_PATHS)
    if paths:
        hits[BUILD_PATH_MARKER] = paths
    return hits


def scan_bytes(name, data, revision=None):
    text = _decode_text(name, data)
    if text is not None:
        return 'text', text_hits(text, revision)
    return 'binary', binary_hits(data, revision)


def scan_tree(root: Path, revision=None, prefix=''):
    """{relative path: (kind, hits)} for every regular file under root; zip archives (.ipa, .apk, .aar, .zip, .jar) are
    opened and their entries reported as '<archive>!<entry>'."""
    found = {}
    for path in sorted(Path(root).rglob('*')):
        if path.is_symlink() or not path.is_file():
            continue
        relative = prefix + path.relative_to(root).as_posix()
        data = path.read_bytes()
        found[relative] = scan_bytes(path.name, data, revision)
        if path.suffix.lower() in ('.ipa', '.apk', '.aar', '.zip', '.jar') and zipfile.is_zipfile(path):
            found.update(scan_zip(path, revision, relative + '!'))
    return found


def scan_zip(path: Path, revision=None, prefix=''):
    found = {}
    with zipfile.ZipFile(path) as archive:
        for info in archive.infolist():
            if info.is_dir():
                continue
            data = archive.read(info)
            name = prefix + info.filename
            found[name] = scan_bytes(info.filename, data, revision)
            if info.filename.lower().endswith(('.zip', '.jar', '.aar')):
                with tempfile.TemporaryDirectory() as scratch:
                    inner = Path(scratch) / Path(info.filename).name
                    inner.write_bytes(data)
                    if zipfile.is_zipfile(inner):
                        found.update(scan_zip(inner, revision, name + '!'))
    return found


def load_baseline(platform):
    data = json.loads(BASELINE.read_text(encoding='utf-8'))
    rules = data['platforms'][platform]
    for pattern, markers in rules.items():
        if REVISION_MARKER in markers or BUILD_PATH_MARKER in markers:
            raise ValueError('the baseline may never allow the engine revision or a build path (%s)' % pattern)
        unknown = set(markers) - set(BINARY_MARKERS)
        if unknown:
            raise ValueError('unknown markers %s for %s' % (sorted(unknown), pattern))
    return rules


def allowed_for(path, rules):
    allowed, matched = set(), False
    for pattern, markers in rules.items():
        if fnmatch.fnmatchcase(path, pattern):
            allowed.update(markers)
            matched = True
    return allowed, matched


def judge(found, rules):
    """(problems, residue): problems are leaks; residue lists the accepted D16 hits per binary for the record."""
    problems, residue = [], {}
    for path, (kind, hits) in sorted(found.items()):
        if not hits:
            continue
        if kind == 'text':
            problems.append('%s (text): %s' % (path, ', '.join('%s×%d' % h for h in sorted(hits.items()))))
            continue
        allowed, matched = allowed_for(path, rules)
        extra = {m: c for m, c in hits.items() if m not in allowed}
        if extra:
            problems.append('%s (binary%s): %s' % (path, '' if matched else ', not in the baseline',
                                                   ', '.join('%s×%d' % h for h in sorted(extra.items()))))
        else:
            residue[path] = hits
    return problems, residue


def public_problems(public: Path, allowed_files):
    """The public folder holds exactly the installers and a SHA256SUMS that lists only them."""
    if not public.is_dir():
        return ['%s does not exist' % public]
    problems = []
    names = sorted(p.name for p in public.iterdir())
    allowed = set(allowed_files) | {'SHA256SUMS'}
    extra = [n for n in names if n not in allowed]
    if extra:
        problems.append('not an installer: %s' % ', '.join(extra))
    missing = [n for n in allowed if n not in names]
    if missing:
        problems.append('missing: %s' % ', '.join(sorted(missing)))
    sums = public / 'SHA256SUMS'
    if sums.is_file():
        listed = sorted(re.split(r'\s+\*?', line.strip(), maxsplit=1)[1] for line in sums.read_text(encoding='utf-8').splitlines() if line.strip())
        if listed != sorted(allowed_files):
            problems.append('SHA256SUMS lists %s, want only %s' % (listed, sorted(allowed_files)))
    return problems


def private_problems(private: Path, required):
    problems = []
    if not private.is_dir():
        return ['%s does not exist' % private]
    for name in list(required) + [PRIVATE_NOTE]:
        if not (private / name).is_file() or (private / name).stat().st_size == 0:
            problems.append('%s missing from %s' % (name, private.name))
    return problems


def named_leaks(names):
    """Private-only file names among the given (relative) paths."""
    return sorted(n for n in names if Path(n.split('!')[-1]).name in PRIVATE_ONLY_NAMES or SOURCE_ARCHIVE.match(Path(n).name))


def summary(residue):
    """A short, stable description of the accepted residue: binary -> markers."""
    return '; '.join('%s: %s' % (path, ','.join(sorted(hits))) for path, hits in sorted(residue.items())) or 'none'


# ---------------------------------------------------------------- self-test

def self_test(platform='macos'):
    """Plants each kind of leak and demands that the audit sees it; a clean tree must pass. Returns problems."""
    failures = []
    rules = load_baseline(platform)
    revision = pinned_revision() or ('0' * 40)
    with tempfile.TemporaryDirectory(prefix='asterlink-scrub-self-') as scratch:
        base = Path(scratch)
        public = base / 'release'
        public.mkdir()
        (public / 'App.pkg').write_bytes(b'installer')
        (public / 'SHA256SUMS').write_text('%s  App.pkg\n' % ('0' * 64))
        if public_problems(public, ['App.pkg']):
            failures.append('a clean public folder was refused')
        (public / 'THIRD_PARTY_LICENSES.txt').write_text('GNU GENERAL PUBLIC LICENSE')
        if not public_problems(public, ['App.pkg']):
            failures.append('a licence file in the public folder was not seen')

        tree = base / 'bundle'
        (tree / 'bin').mkdir(parents=True)
        (tree / 'Info.plist').write_bytes(plistlib.dumps({'CFBundleName': 'AsterLink'}, fmt=plistlib.FMT_BINARY))
        (tree / 'bin' / 'unknown-binary').write_bytes(b'\x00\x01code\x00')
        problems, _ = judge(scan_tree(tree, revision), rules)
        if problems:
            failures.append('a clean tree was refused: %s' % problems)
        plants = {
            'binary plist key': lambda: (tree / 'Info.plist').write_bytes(plistlib.dumps({'AsterLinkMihomoRevision': revision}, fmt=plistlib.FMT_BINARY)),
            'licence text': lambda: (tree / 'notes.txt').write_text('Licensed under the GPL v3'),
            'marker in a binary outside the baseline': lambda: (tree / 'bin' / 'unknown-binary').write_bytes(b'\x00github.com/metacubex/mihomo\x00'),
            'UTF-16 marker': lambda: (tree / 'bin' / 'unknown-binary').write_bytes(b'\x00' + 'Mihomo'.encode('utf-16-le') + b'\x00'),
            'engine revision in a binary': lambda: (tree / 'bin' / 'unknown-binary').write_bytes(b'\x00' + revision.encode() + b'\x00'),
            'build path in a binary': lambda: (tree / 'bin' / 'unknown-binary').write_bytes(b'\x00' + str(ROOT / 'apps/x.swift').encode() + b'\x00'),
            'licence inside a zip': lambda: _zip(tree / 'inner.zip', 'LICENSE', 'Clash is GNU General Public'),
        }
        for label, plant in plants.items():
            shutil.rmtree(tree)
            (tree / 'bin').mkdir(parents=True)
            (tree / 'bin' / 'unknown-binary').write_bytes(b'\x00\x01code\x00')
            plant()
            problems, _ = judge(scan_tree(tree, revision), rules)
            if not problems:
                failures.append('missed: %s' % label)
        if named_leaks(['Payload/AsterLink.app/SOURCE.txt', 'x/asterlink-source-0123abcd.tar.gz']) != ['Payload/AsterLink.app/SOURCE.txt', 'x/asterlink-source-0123abcd.tar.gz']:
            failures.append('private-only names inside a package were not seen')
    return failures


def _zip(path, name, text):
    with zipfile.ZipFile(path, 'w') as archive:
        archive.writestr(name, text)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--self-test', action='store_true')
    parser.add_argument('--scan', help='report the markers in this tree')
    parser.add_argument('--platform', choices=('macos', 'windows', 'ios', 'android'), default='macos')
    args = parser.parse_args()
    if args.self_test:
        failures = []
        for platform in ('macos', 'windows', 'ios', 'android'):
            failures += ['%s: %s' % (platform, f) for f in self_test(platform)]
        for failure in failures:
            print('FAIL', failure)
        print('release scrub self-test: %s' % ('FAIL' if failures else 'PASS'))
        return 1 if failures else 0
    if args.scan:
        found = scan_tree(Path(args.scan), pinned_revision())
        problems, residue = judge(found, load_baseline(args.platform))
        for path, (kind, hits) in sorted(found.items()):
            if hits:
                print('%-6s %s %s' % (kind, path, hits))
        print('\n%d problem(s)' % len(problems))
        for problem in problems:
            print('  ' + problem)
        print('accepted residue: ' + summary(residue))
        return 1 if problems else 0
    parser.print_help()
    return 2


if __name__ == '__main__':
    sys.exit(main())

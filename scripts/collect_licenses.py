#!/usr/bin/env python3
"""Collect the license texts of every Go module linked into asterlink-core.

    python3 scripts/collect_licenses.py --output THIRD_PARTY_LICENSES.txt

The module list comes from `go list -deps` on the exact command that is shipped, so it
cannot drift from the binary. A module without a license file is reported (and fails
`--strict`) instead of being skipped silently. A module whose published archive carries no license file
(the upstream added one later) is covered by a reviewed copy in scripts/third_party_licenses/, which names
its source. The module set depends on the target platform (--goos/--goarch); for --goos android the shipped code is the
gomobile binding (native/core/mobile plus x/mobile's bind runtime, cgo on) rather than cmd/asterlink-core. Mihomo (GPL-3.0) is called out first: its
license is why the package also carries SOURCE.txt and the corresponding-source archives
(docs/macos-release.md section 8).
"""
from pathlib import Path
import argparse
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
LICENSE_NAMES = ('LICENSE', 'LICENSE.md', 'LICENSE.txt', 'LICENCE', 'COPYING', 'COPYING.md', 'UNLICENSE')
# Variants such as LICENSE.gpl or LICENSE-MIT, tried (sorted) when none of the names above exists.
LICENSE_PREFIXES = ('license', 'licence', 'copying', 'unlicense')
MIHOMO_DIR = ROOT / 'native/third_party/Clash.Meta'
OVERRIDES_DIR = ROOT / 'scripts/third_party_licenses'


def shipped_packages(goos):
    """What is linked into the product for this platform, and whether cgo is on (gomobile needs it)."""
    if goos == 'android':
        return ['./mobile', 'golang.org/x/mobile/bind/java'], '1'
    return ['./cmd/asterlink-core'], '0'


def linked_modules(goos, goarch):
    packages, cgo = shipped_packages(goos)
    out = subprocess.run(
        ['go', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}{{end}}', *packages],
        cwd=ROOT / 'native/core', env={'GOOS': goos, 'GOARCH': goarch, 'CGO_ENABLED': cgo, **_base_env()},
        capture_output=True, text=True, check=True).stdout
    modules = {}
    for line in out.splitlines():
        path, version, directory = (line.split('\t') + ['', ''])[:3]
        if path and directory:
            modules[path] = (version, Path(directory))
    return modules


def _base_env():
    import os
    return {k: v for k, v in os.environ.items() if k not in ('GOOS', 'GOARCH', 'CGO_ENABLED')}


def license_file(directory: Path):
    for name in LICENSE_NAMES:
        candidate = directory / name
        if candidate.is_file():
            return candidate
    for candidate in sorted(directory.iterdir()) if directory.is_dir() else []:
        if candidate.is_file() and candidate.name.lower().startswith(LICENSE_PREFIXES):
            return candidate
    return None


def override_file(module_path):
    candidate = OVERRIDES_DIR / (module_path.replace('/', '_') + '.txt')
    return candidate if candidate.is_file() else None


def collect(goos='darwin', goarch='arm64'):
    """Returns (rendered_text, missing_module_paths, module_count) for the Core built for goos/goarch."""
    modules = linked_modules(goos, goarch)
    missing = []
    sections = []
    for path in sorted(modules, key=lambda p: (p != 'github.com/metacubex/mihomo' and 'Clash.Meta' not in str(modules[p][1]), p)):
        version, directory = modules[path]
        # Main module and the in-repo shared policy are this product's own code.
        if directory.is_relative_to(ROOT) and not directory.is_relative_to(MIHOMO_DIR):
            continue
        found = license_file(directory) or override_file(path)
        header = '=' * 78 + '\n%s %s\n' % (path, version or '(local replace)') + '=' * 78
        if found is None:
            missing.append(path)
            sections.append(header + '\nNO LICENSE FILE FOUND IN THIS MODULE; see its upstream repository.\n')
        else:
            sections.append(header + '\n' + found.read_text(encoding='utf-8', errors='replace').strip() + '\n')
    preface = (
        'AsterLink third-party notices\n\n'
        'The proxy core embeds Mihomo (Clash.Meta fork, GNU GPL v3). The corresponding source of the\n'
        'exact build, with the instructions to rebuild it, is described in SOURCE.txt and shipped\n'
        'beside the installer. The other modules below are linked into %s under their own licenses.\n\n'
        % ('libgojni.so (the Android Core)' if goos == 'android' else 'asterlink-core'))
    return preface + '\n'.join(sections), missing, len(sections)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--strict', action='store_true', help='fail when a linked module has no license file')
    parser.add_argument('--goos', default='darwin')
    parser.add_argument('--goarch', default='arm64')
    args = parser.parse_args()
    text, missing, count = collect(args.goos, args.goarch)
    args.output.write_text(text, encoding='utf-8')
    print('licenses: %d third-party modules, %d without a license file' % (count, len(missing)))
    for path in missing:
        print('  missing:', path)
    if args.strict and missing:
        sys.exit(1)


if __name__ == '__main__':
    main()

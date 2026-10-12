#!/usr/bin/env python3
"""Collect the license texts of every Go module linked into asterlink-core.

    python3 scripts/collect_licenses.py --output THIRD_PARTY_LICENSES.txt

The module list comes from `go list -deps` on the exact command that is shipped, so it
cannot drift from the binary. A module without a license file is reported (and fails
`--strict`) instead of being skipped silently. Mihomo (GPL-3.0) is called out first: its
license is why the package also carries SOURCE.txt and the corresponding-source archives
(docs/macos-release.md section 8).
"""
from pathlib import Path
import argparse
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
LICENSE_NAMES = ('LICENSE', 'LICENSE.md', 'LICENSE.txt', 'LICENCE', 'COPYING', 'COPYING.md', 'UNLICENSE')
MIHOMO_DIR = ROOT / 'native/third_party/Clash.Meta'


def linked_modules():
    out = subprocess.run(
        ['go', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}{{end}}', './cmd/asterlink-core'],
        cwd=ROOT / 'native/core', env={'GOOS': 'darwin', 'GOARCH': 'arm64', 'CGO_ENABLED': '0', **_base_env()},
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
    return None


def collect():
    """Returns (rendered_text, missing_module_paths, module_count)."""
    modules = linked_modules()
    missing = []
    sections = []
    for path in sorted(modules, key=lambda p: (p != 'github.com/metacubex/mihomo' and 'Clash.Meta' not in str(modules[p][1]), p)):
        version, directory = modules[path]
        # Main module and the in-repo shared policy are this product's own code.
        if directory.is_relative_to(ROOT) and not directory.is_relative_to(MIHOMO_DIR):
            continue
        found = license_file(directory)
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
        'beside the installer. The other modules below are linked into asterlink-core under their own licenses.\n\n')
    return preface + '\n'.join(sections), missing, len(sections)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--strict', action='store_true', help='fail when a linked module has no license file')
    args = parser.parse_args()
    text, missing, count = collect()
    args.output.write_text(text, encoding='utf-8')
    print('licenses: %d third-party modules, %d without a license file' % (count, len(missing)))
    for path in missing:
        print('  missing:', path)
    if args.strict and missing:
        sys.exit(1)


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Bundle reviewed shared sources for standalone backend deployment; never relax validation."""
from pathlib import Path
import argparse
import hashlib
import json
import os
import stat
import tempfile

ROOT = Path(__file__).resolve().parents[1]
POLICY = Path('shared/nodepolicy')
BUNDLE = Path('backend/nodepolicy')
POLICY_FILES = ('go.mod', 'policy.go', 'policy_test.go', 'testdata/proxies.yaml')
# Requires the actual pinned Mihomo source. It stays in the Core module and CI.
CORE_ONLY = {'pinned_schema_test.go'}
CONTRACT = Path('shared/contracts/native-v1.json')
CONTRACT_COPY = Path('backend/internal/api/testdata/native_client_contract.json')
MANIFEST = Path('backend/shared-sources.json')
README = b'''# Generated standalone dependency\n\nDo not edit this directory independently. Its Go source, tests and fixtures are\nbyte-identical snapshots of shared/nodepolicy. From the repository root:\n\n    python3 scripts/sync-backend-shared.py --write\n    python3 scripts/sync-backend-shared.py --check\n\nCommit the regenerated files together with canonical changes. CI rejects drift,\nmissing files, unexpected files and symlinks. The pinned-schema compatibility\ntest remains in the canonical Core module because it needs Mihomo source.\nServers need only the complete backend directory; Python, Flutter and Mihomo\nsource are not required to build the API. See ../DEPLOYMENT.md.\n'''


def checked_path(root: Path, relative: Path) -> Path:
    """No links, absolute paths or parent escapes, including directory ancestors."""
    if relative.is_absolute() or '..' in relative.parts:
        raise ValueError('Unsafe shared-source path')
    path = root
    for part in relative.parts:
        path = path / part
        if path.is_symlink():
            raise ValueError('Symlink not allowed: ' + str(relative))
    return path


def read_file(root: Path, relative: Path) -> bytes:
    path = checked_path(root, relative)
    info = path.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_size > 1024 * 1024:
        raise ValueError('Shared input must be a regular file <= 1 MiB: ' + str(relative))
    return path.read_bytes()


def inventory(root: Path, relative: Path) -> set:
    directory = checked_path(root, relative)
    if not directory.exists():
        return set()
    result = set()
    for path in directory.rglob('*'):
        checked_path(root, path.relative_to(root))
        if path.is_file():
            result.add(path.relative_to(directory).as_posix())
        elif not path.is_dir():
            raise ValueError('Non-regular shared input')
    return result


def expected_files(root: Path) -> dict:
    # New implementation files fail closed until the bundle list is reviewed.
    if inventory(root, POLICY) != set(POLICY_FILES) | CORE_ONLY:
        raise ValueError('Core policy inventory changed; review POLICY_FILES and CORE_ONLY')
    mapping = {BUNDLE / name: POLICY / name for name in POLICY_FILES}
    mapping[CONTRACT_COPY] = CONTRACT
    result, entries = {}, []
    for destination, source in sorted(mapping.items()):
        data = read_file(root, source)
        result[destination] = data
        entries.append({'source': source.as_posix(), 'destination': destination.as_posix(),
                        'sha256': hashlib.sha256(data).hexdigest()})
    result[BUNDLE / 'README.md'] = README
    result[MANIFEST] = (json.dumps({'format': 1, 'files': entries}, indent=2) + '\n').encode()
    return result


def synchronize(root: Path, check: bool) -> list:
    expected = expected_files(root)
    extras = inventory(root, BUNDLE) - set(POLICY_FILES) - {'README.md'}
    if extras:
        raise ValueError('Unexpected bundled files; review instead of deleting: ' + ', '.join(sorted(extras)))
    changed = []
    # Preflight before writing; never print source contents or credentials.
    for relative, data in expected.items():
        path = checked_path(root, relative)
        if not path.exists() or read_file(root, relative) != data:
            changed.append(relative)
    if check:
        if changed:
            raise ValueError('Shared sources out of sync: ' + ', '.join(map(str, changed)) +
                             '; run python3 scripts/sync-backend-shared.py --write')
        return []
    for relative in changed:
        path = checked_path(root, relative)
        path.parent.mkdir(parents=True, exist_ok=True)
        fd, name = tempfile.mkstemp(prefix='.sync-', dir=str(path.parent))
        try:
            with os.fdopen(fd, 'wb') as output:
                output.write(expected[relative])
            os.chmod(name, 0o644)
            os.replace(name, path)
        finally:
            if os.path.exists(name):
                os.unlink(name)
    return changed


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--check', action='store_true', help='Read-only CI drift check')
    mode.add_argument('--write', action='store_true', help='Regenerate reviewed backend snapshots')
    args = parser.parse_args()
    try:
        changed = synchronize(ROOT, check=args.check)
    except (OSError, ValueError) as error:
        parser.exit(1, str(error) + '\n')
    print('PASS: shared backend snapshots match Core' if args.check else
          'Updated %d bundled files' % len(changed))


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Apply the reviewed, hash-locked patch to the unchanged Mihomo gitlink.

Build entry points call prepare(). A manual Go build needs this command first.
--check is read-only. Source archives without .git are also supported: the
original and resulting source bytes, not just a branch name, are verified.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SUBMODULE = Path('native/third_party/Clash.Meta')
PATCHES = Path('native/third_party/patches')


def normalized(path):
    # Git's Windows checkout may use CRLF. Patch/source identity is canonical LF.
    return path.read_bytes().replace(b'\r\n', b'\n')


def digest(data):
    return hashlib.sha256(data).hexdigest() if data is not None else None


def capture(args, cwd):
    return subprocess.run(args, cwd=cwd, check=True, capture_output=True, text=True).stdout.strip()


def metadata(root=ROOT):
    lock = json.loads((root / PATCHES / 'manifest.json').read_text())
    return {'id': lock['id'], 'base_commit': lock['base_commit'], 'patch_sha256': lock['patch_sha256']}


def prepare(root=ROOT, *, check=False):
    root = Path(root).resolve()
    tree = root / SUBMODULE
    patch_dir = root / PATCHES
    lock = json.loads((patch_dir / 'manifest.json').read_text())
    patch = normalized(patch_dir / lock['patch'])
    if digest(patch) != lock['patch_sha256']:
        raise RuntimeError('Mihomo patch hash mismatch; refusing an unreviewed patch')
    if not tree.is_dir() or tree.is_symlink():
        raise RuntimeError('initialize the Mihomo submodule with git submodule update --init')

    paths = set(lock['files'])
    if (tree / '.git').exists():
        head = capture(['git', 'rev-parse', 'HEAD'], tree)
        if head != lock['base_commit']:
            raise RuntimeError('Mihomo HEAD differs from the patch base; do not apply to a different pin')
        changed = set(capture(['git', 'diff', '--name-only', 'HEAD'], tree).splitlines())
        untracked = set(capture(['git', 'ls-files', '--others', '--exclude-standard'], tree).splitlines())
        if (changed | untracked) - paths:
            raise RuntimeError('Mihomo has changes outside the reviewed patch; refusing to overwrite or build them')

    before = {}
    states = []
    for name, hashes in lock['files'].items():
        target = tree / name
        if target.is_symlink() or not target.resolve().is_relative_to(tree.resolve()):
            raise RuntimeError('Mihomo patch target escapes its own worktree')
        content = normalized(target) if target.is_file() else None
        before[name] = content
        sha = digest(content)
        if sha == hashes['patched_sha256']:
            states.append('patched')
        elif sha == hashes['original_sha256']:
            states.append('original')
        else:
            raise RuntimeError('Mihomo source hash mismatch: ' + name + '; no files changed')
    if all(state == 'patched' for state in states):
        return metadata(root)
    if check:
        raise RuntimeError('Mihomo is not patched; run python3 scripts/prepare_mihomo.py')
    if not all(state == 'original' for state in states):
        raise RuntimeError('Mihomo patch is partially applied; inspect it instead of overwriting it')

    # Apply to a disposable non-repository directory first. This works for git
    # worktrees AND unpacked source archives, without touching either git index.
    with tempfile.TemporaryDirectory(prefix='asterlink-mihomo-patch-') as temporary:
        staging = Path(temporary)
        for name, content in before.items():
            if content is not None:
                target = staging / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(content)
        patch_file = staging / 'reviewed.patch'
        patch_file.write_bytes(patch)
        env = {k: v for k, v in os.environ.items() if k not in ('GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE')}
        for args in (['--check'], []):
            subprocess.run(['git', 'apply', *args, '--whitespace=error-all', str(patch_file)],
                           cwd=staging, env=env, check=True, capture_output=True)
        after = {name: normalized(staging / name) for name in paths}
        for name, content in after.items():
            if digest(content) != lock['files'][name]['patched_sha256']:
                raise RuntimeError('Mihomo patched source hash mismatch: ' + name)
        # Recheck every input before publishing any output. Never clobber edits
        # made while the patch was being checked. Atomic per-file replacement.
        for name, content in before.items():
            target = tree / name
            current = normalized(target) if target.is_file() else None
            if current != content:
                raise RuntimeError('Mihomo source changed during preparation; retry after inspection')
        for name, content in after.items():
            target = tree / name
            target.parent.mkdir(parents=True, exist_ok=True)
            with tempfile.NamedTemporaryFile(dir=target.parent, delete=False) as output:
                temporary_path = Path(output.name)
                output.write(content)
            try:
                os.chmod(temporary_path, 0o644)
                os.replace(temporary_path, target)
            finally:
                temporary_path.unlink(missing_ok=True)
    return prepare(root, check=True)


def source_dirty(root=ROOT):
    # Expected, verified patch bytes are corresponding source, not unknown dirt.
    # --ignore-submodules=dirty still reports a different submodule commit.
    prepare(root, check=True)
    return bool(capture(['git', 'status', '--porcelain', '--ignore-submodules=dirty'], root))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='verify only; never modify source')
    args = parser.parse_args()
    try:
        print(json.dumps(prepare(check=args.check), sort_keys=True))
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        parser.exit(1, 'Mihomo preparation failed: %s\n' % error)


if __name__ == '__main__':
    main()

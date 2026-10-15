#!/usr/bin/env python3
"""Hash-locked patch application and the real sing-tun NAT lifecycle regression.

No root, TUN, database or network changes. Run from any of the three host OSes.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import prepare_mihomo as patch


class PreparationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='asterlink-patch-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        shutil.copytree(patch.ROOT / patch.PATCHES, self.root / patch.PATCHES)
        self.lock = json.loads((self.root / patch.PATCHES / 'manifest.json').read_text())
        self.tree = self.root / patch.SUBMODULE
        self.tree.mkdir(parents=True)
        for name, hashes in self.lock['files'].items():
            if hashes['original_sha256'] is not None:
                content = subprocess.run(['git', 'show', self.lock['base_commit'] + ':' + name],
                                         cwd=patch.ROOT / patch.SUBMODULE, check=True, capture_output=True).stdout
                target = self.tree / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(content)
        self.source = self.tree / 'listener/sing_tun/server.go'

    def test_source_archive_and_idempotence(self):
        record = patch.prepare(self.root)
        self.assertEqual(record, patch.prepare(self.root))
        self.assertEqual(record, patch.prepare(self.root, check=True))
        for name, hashes in self.lock['files'].items():
            self.assertEqual(hashes['patched_sha256'], patch.digest(patch.normalized(self.tree / name)))

    def test_check_does_not_apply(self):
        before = self.source.read_bytes()
        with self.assertRaises(RuntimeError):
            patch.prepare(self.root, check=True)
        self.assertEqual(before, self.source.read_bytes())

    def test_windows_crlf(self):
        self.source.write_bytes(self.source.read_bytes().replace(b'\n', b'\r\n'))
        patch.prepare(self.root)
        patch.prepare(self.root, check=True)

    def test_unknown_source_is_not_overwritten(self):
        self.source.write_bytes(self.source.read_bytes() + b'// local edit\n')
        before = self.source.read_bytes()
        with self.assertRaisesRegex(RuntimeError, 'source hash mismatch'):
            patch.prepare(self.root)
        self.assertEqual(before, self.source.read_bytes())

    def test_patch_drift(self):
        file = self.root / patch.PATCHES / self.lock['patch']
        file.write_bytes(file.read_bytes() + b'\n')
        with self.assertRaisesRegex(RuntimeError, 'patch hash mismatch'):
            patch.prepare(self.root)

    def test_partial_patch_is_not_overwritten(self):
        original = self.source.read_bytes()
        patch.prepare(self.root)
        self.source.write_bytes(original)
        with self.assertRaisesRegex(RuntimeError, 'partially applied'):
            patch.prepare(self.root)
        self.assertEqual(original, self.source.read_bytes())


if __name__ == '__main__':
    patch.prepare()
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(PreparationTests))
    if not result.wasSuccessful():
        raise SystemExit(1)
    subprocess.run(['go', 'test', '-count=1', '-v', 'github.com/metacubex/mihomo/listener/sing_tun', '-run', '^TestAsterLink'],
                   cwd=patch.ROOT / 'native/core', env=dict(os.environ), check=True)

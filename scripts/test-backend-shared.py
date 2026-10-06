#!/usr/bin/env python3
"""Offline regression checks for generated backend shared-source snapshots."""
from pathlib import Path
import importlib.util
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('backend_shared', Path(__file__).with_name('sync-backend-shared.py'))
shared = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shared)


class SharedSourcesTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='vpn-shared-')
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        for name in set(shared.POLICY_FILES) | shared.CORE_ONLY:
            relative = shared.POLICY / name
            self.write(relative, shared.read_file(shared.ROOT, relative))
        self.write(shared.CONTRACT, shared.read_file(shared.ROOT, shared.CONTRACT))

    def write(self, relative, data):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def test_missing_check_is_read_only_and_write_is_idempotent(self):
        with self.assertRaises(ValueError):
            shared.synchronize(self.root, check=True)
        self.assertFalse((self.root / 'backend').exists())
        self.assertEqual(len(shared.synchronize(self.root, check=False)), 7)
        self.assertEqual(shared.synchronize(self.root, check=True), [])
        self.assertEqual(shared.synchronize(self.root, check=False), [])
        for name in shared.POLICY_FILES:
            self.assertEqual((self.root / shared.POLICY / name).read_bytes(),
                             (self.root / shared.BUNDLE / name).read_bytes())
        self.assertFalse((self.root / shared.BUNDLE / 'pinned_schema_test.go').exists())

    def test_source_drift_does_not_silently_update_snapshot(self):
        shared.synchronize(self.root, check=False)
        target = self.root / shared.BUNDLE / 'policy.go'
        original = target.read_bytes()
        source = self.root / shared.POLICY / 'policy.go'
        source.write_bytes(original + b'\n// source changed\n')
        with self.assertRaises(ValueError):
            shared.synchronize(self.root, check=True)
        self.assertEqual(target.read_bytes(), original)
        shared.synchronize(self.root, check=False)
        self.assertEqual(target.read_bytes(), source.read_bytes())

    def test_every_bundled_file_and_manifest_is_checked(self):
        shared.synchronize(self.root, check=False)
        for relative in shared.expected_files(self.root):
            with self.subTest(path=str(relative)):
                path = self.root / relative
                original = path.read_bytes()
                path.write_bytes(original + b'changed')
                with self.assertRaises(ValueError):
                    shared.synchronize(self.root, check=True)
                path.write_bytes(original)

    def test_new_source_and_unexpected_bundle_require_review(self):
        shared.synchronize(self.root, check=False)
        source = shared.POLICY / 'new.go'
        self.write(source, b'package nodepolicy\n')
        with self.assertRaises(ValueError):
            shared.synchronize(self.root, check=False)
        (self.root / source).unlink()
        extra = shared.BUNDLE / 'unexpected.go'
        self.write(extra, b'package nodepolicy\n')
        with self.assertRaises(ValueError):
            shared.synchronize(self.root, check=False)
        self.assertTrue((self.root / extra).exists())

    def test_symlink_file_or_parent_is_rejected(self):
        shared.synchronize(self.root, check=False)
        target = self.root / shared.BUNDLE / 'policy.go'
        target.unlink()
        target.symlink_to(self.root / shared.POLICY / 'policy.go')
        with self.assertRaises(ValueError):
            shared.synchronize(self.root, check=False)
        target.unlink()
        directory = self.root / shared.BUNDLE / 'testdata'
        directory.rename(directory.with_name('saved'))
        directory.symlink_to(directory.with_name('saved'), target_is_directory=True)
        with self.assertRaises(ValueError):
            shared.synchronize(self.root, check=False)


if __name__ == '__main__':
    unittest.main()

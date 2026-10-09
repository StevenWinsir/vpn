#!/usr/bin/env python3
"""Run the shared env-v1 fixtures against the Python parser (Swift runs the same file)."""
from pathlib import Path
import json
import unittest

import native_env

ROOT = Path(__file__).resolve().parents[1]
CASES = json.loads((ROOT / 'shared/contracts/env-v1.json').read_text())['cases']


class EnvContract(unittest.TestCase):
    def test_fixture_cases(self):
        self.assertGreaterEqual(len(CASES), 16)
        for case in CASES:
            with self.subTest(case['name']):
                if case['ok']:
                    native_env.parse_env(case['env'])
                else:
                    with self.assertRaises(native_env.EnvError):
                        native_env.parse_env(case['env'])

    def test_example_file_is_valid_and_has_no_secrets(self):
        values = native_env.load_env_file(ROOT / 'apps/macos/.env.example')
        self.assertEqual(set(values), set(native_env.ALLOWED))

    def test_symlink_is_refused(self):
        import tempfile
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'real.env'
            target.write_text((ROOT / 'apps/macos/.env.example').read_text())
            link = Path(directory) / '.env'
            link.symlink_to(target)
            with self.assertRaises(native_env.EnvError):
                native_env.load_env_file(link)


if __name__ == '__main__':
    import sys
    sys.path.insert(0, str(Path(__file__).parent))
    unittest.main()

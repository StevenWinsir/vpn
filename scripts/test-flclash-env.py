#!/usr/bin/env python3
"""Test the command-scoped development environment without changing host settings."""
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest


ROOT = Path(__file__).resolve().parents[1]
WRAPPER = ROOT / "scripts" / "flclash-env.sh"


class FlClashEnvironmentTest(unittest.TestCase):
    def run_wrapper(self, args, extra_env=None):
        env = dict(os.environ)
        for key in ("FLUTTER_ROOT", "JAVA_HOME", "ANDROID_HOME", "ANDROID_SDK_ROOT",
                    "RUSTUP_TOOLCHAIN", "DEVELOPER_DIR"):
            env.pop(key, None)
        env.update(extra_env or {})
        return subprocess.run(
            ["bash", str(WRAPPER), *args], cwd="/tmp", env=env,
            text=True, capture_output=True, timeout=15, check=False,
        )

    def probe(self, extra_env=None, args=()):
        code = (
            "import json,os,sys; "
            "print(json.dumps({'cwd':os.getcwd(),'argv':sys.argv[1:],"
            "'env':{k:os.environ.get(k) for k in ['FLUTTER_ROOT','JAVA_HOME',"
            "'ANDROID_HOME','ANDROID_SDK_ROOT','RUSTUP_TOOLCHAIN','DEVELOPER_DIR']}}))"
        )
        result = self.run_wrapper([sys.executable, "-c", code, *args], extra_env)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_shell_syntax_and_usage(self):
        result = subprocess.run(["bash", "-n", str(WRAPPER)], capture_output=True)
        self.assertEqual(result.returncode, 0)
        result = self.run_wrapper([])
        self.assertEqual(result.returncode, 64)
        self.assertIn("Usage:", result.stderr)

    def test_arguments_and_working_directory(self):
        args = ["space argument", "$(must-not-run)", "semi;colon", "", "中文"]
        result = self.probe(args=args)
        self.assertEqual(result["argv"], args)
        self.assertEqual(Path(result["cwd"]).resolve(), ROOT / "FlClash")

    def test_explicit_environment_wins(self):
        explicit = {
            "FLUTTER_ROOT": "/tmp/fixture flutter",
            "JAVA_HOME": "/tmp/fixture java",
            "ANDROID_HOME": "/tmp/fixture android",
            "ANDROID_SDK_ROOT": "/tmp/ignored android",
            "RUSTUP_TOOLCHAIN": "fixture-toolchain",
            "DEVELOPER_DIR": "/tmp/fixture developer",
        }
        result = self.probe(explicit)["env"]
        for key, value in explicit.items():
            self.assertEqual(result[key], explicit["ANDROID_HOME"] if key == "ANDROID_SDK_ROOT" else value)

    def test_android_sdk_root_fallback_and_rust_pin(self):
        result = self.probe({"ANDROID_SDK_ROOT": "/tmp/sdk root"})["env"]
        self.assertEqual(result["ANDROID_HOME"], "/tmp/sdk root")
        pin = ROOT / "FlClash/plugins/rust_api/rust/rust-toolchain.toml"
        channel = next(line.split('"')[1] for line in pin.read_text().splitlines()
                       if line.startswith('channel = "'))
        self.assertEqual(result["RUSTUP_TOOLCHAIN"], channel)

    def test_child_exit_code_is_preserved(self):
        result = self.run_wrapper([sys.executable, "-c", "raise SystemExit(37)"])
        self.assertEqual(result.returncode, 37)


if __name__ == "__main__":
    unittest.main(verbosity=2)

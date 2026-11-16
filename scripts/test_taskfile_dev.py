"""Check dev port arguments and forwarding without building or starting media."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class DevPortTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="camstream-dev-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "config").mkdir()
        (self.root / "bin").mkdir()
        (self.root / "scripts").mkdir()
        (self.root / "scripts/dev-runtime.py").write_text("print('/test/go2rtc')\n")
        shutil.copy(ROOT / "config/dev.yaml", self.root / "config/dev.yaml")
        self.custom = self.root / "custom config.yaml"
        self.custom.write_text("listen: 127.0.0.1:9000\n")
        for name, body in {
            "camstream": 'printf "VALIDATE:%s\\n" "$*"',
            "camstream-dev": 'printf "PORT:%s\\nMEDIA:%s\\nARGS:%s\\n" "$CAMSTREAM_DEV_PORT" "$CAMSTREAM_DEV_MEDIA_ADDRESS" "$*"',
        }.items():
            path = self.root / "bin" / name
            path.write_text("#!/bin/bash\n" + body + "\n")
            path.chmod(0o755)

    def task(self, *args, platform="Linux"):
        return subprocess.run(
            ["bash", "-c", 'source "$1"; SCRIPT_DIR="$2"; shift 2; '
             'uname() { echo "$TEST_PLATFORM"; }; go() { echo 1; }; dev:build() { echo BUILD; }; dev:serve "$@"',
             "test", str(ROOT / "taskfile"), str(self.root), *args],
            cwd=self.root, text=True, capture_output=True,
            env={**os.environ, "CAMSTREAM_CONFIG": "", "CAMSTREAM_DEV_PORT": "9999", "CAMSTREAM_DEV_MEDIA_ADDRESS": "192.168.99.99", "TEST_PLATFORM": platform},
            check=False,
        )

    def test_port_forms_and_custom_config(self):
        for args in [("8087",), ("--port", "8087"), ("--port=8087",), ("08087",)]:
            for config in [(), ("--config", str(self.custom))]:
                with self.subTest(args=args, config=config):
                    result = self.task(*config, *args)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn("PORT:8087\n", result.stdout)
                    self.assertIn("VALIDATE:config validate --config", result.stdout)
                    if config:
                        self.assertIn("ARGS:serve --config " + str(self.custom), result.stdout)
                    self.assertEqual(self.custom.read_text(), "listen: 127.0.0.1:9000\n")

    def test_no_override_clears_inherited_port(self):
        result = self.task()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("PORT:\n", result.stdout)
        self.assertIn("MEDIA:\n", result.stdout)

    def test_mac_media_address_forwarding(self):
        for args in [("--media-address", "192.168.1.70"), ("--media-address=192.168.1.70",)]:
            result = self.task("--config", str(self.custom), *args, platform="Darwin")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("MEDIA:192.168.1.70\n", result.stdout)
            self.assertIn("HTTP and worker control remain loopback", result.stdout)
            self.assertEqual(self.custom.read_text(), "listen: 127.0.0.1:9000\n")

    def test_invalid_or_non_mac_media_address_fails_before_build(self):
        for args in [("--media-address",), ("--media-address=",),
                     ("--media-address", "0.0.0.0:123"), ("--media-address", "localhost"),
                     ("--media-address", "192.168.1.70"),
                     ("--media-address=192.168.1.70", "--media-address=192.168.1.70")]:
            result = self.task(*args)
            self.assertEqual(result.returncode, 2, result.stderr)
            self.assertNotIn("BUILD", result.stdout)

    def test_invalid_or_duplicate_ports_fail_before_build(self):
        for args in [("0",), ("65536",), ("8x",), ("--port",), ("--port=",),
                     ("--port", "-1"), ("--port", "+80"), ("--port", " 80"),
                     ("8087", "8088"), ("--port=8087", "8088"),
                     ("8087", "--port", "8088")]:
            with self.subTest(args=args):
                result = self.task(*args)
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertNotIn("BUILD", result.stdout)
                self.assertNotIn("PORT:", result.stdout)


if __name__ == "__main__":
    unittest.main()

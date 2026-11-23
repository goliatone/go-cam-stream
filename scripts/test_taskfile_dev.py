"""Check development Taskfile commands without building or starting media."""
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
            "ffmpeg": ":",
        }.items():
            path = self.root / "bin" / name
            path.write_text("#!/bin/bash\n" + body + "\n")
            path.chmod(0o755)

    def task(self, *args, platform="Linux"):
        return subprocess.run(
            ["bash", "-c", 'source "$1"; SCRIPT_DIR="$2"; shift 2; '
             'uname() { echo "$TEST_PLATFORM"; }; go() { echo 1; }; dev:build() { echo BUILD; }; '
             'CAMSTREAM_DEV_FFMPEG="$SCRIPT_DIR/bin/ffmpeg"; dev:serve "$@"',
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

    def test_mac_auto_media_applies_only_to_the_bundled_config(self):
        for args, expected in [
            ((), "auto"),
            (("8087",), "auto"),
            (("--config", str(self.custom)), ""),
            (("--webcam",), "auto"),
            (("--webcam=1",), "auto"),
            (("--media-address=127.0.0.1",), "127.0.0.1"),
            (("--webcam", "--media-address=127.0.0.1"), "127.0.0.1"),
            (("--webcam=1", "--media-address", "192.168.1.70"), "192.168.1.70"),
        ]:
            with self.subTest(args=args):
                result = self.task(*args, platform="Darwin")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(f"MEDIA:{expected}\n", result.stdout)

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


class MacBuildTests(unittest.TestCase):
    def test_conversation_proof_builds_and_tests_the_same_fresh_app(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            chrome = root / "Google Chrome"
            chrome.touch(mode=0o700)
            command = (
                'source "$1"; SCRIPT_DIR="$2"; '
                'uname() { if [[ "$1" == -m ]]; then echo arm64; else echo Darwin; fi; }; '
                'mac:build() { [[ "$BUILD_STATUS" == 0 ]] || return "$BUILD_STATUS"; mkdir "$1"; }; '
                'go() { [[ -d "$CAMSTREAM_PROOF_APP" && "$CAMSTREAM_PROOF_CHROME" == "$EXPECTED_CHROME" '
                '&& "$*" == *"-count=1"* ]]; }; test:conversation'
            )
            env = {**os.environ, "CAMSTREAM_PROOF_APP": "/old.app",
                   "EXPECTED_CHROME": str(chrome), "CAMSTREAM_PROOF_CHROME": str(chrome), "BUILD_STATUS": "0"}
            args = ["bash", "-c", command, "test", str(ROOT / "taskfile"), temp]
            for _ in range(2):
                result = subprocess.run(args, text=True, capture_output=True, env=env, check=False)
                self.assertEqual(result.returncode, 0, result.stderr)
            builds = root / "dist/conversation-test"
            self.assertEqual(len(list(builds.glob("build.*/Camstream Camera.app"))), 2)

            result = subprocess.run(args, text=True, capture_output=True,
                                    env={**env, "BUILD_STATUS": "7"}, check=False)
            self.assertEqual(result.returncode, 7, result.stderr)
            self.assertEqual(len(list(builds.glob("build.*"))), 2)

            result = subprocess.run(args, text=True, capture_output=True,
                                    env={**env, "CAMSTREAM_PROOF_CHROME": str(root / "missing")}, check=False)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Set CAMSTREAM_PROOF_CHROME", result.stderr)
            self.assertEqual(len(list(builds.glob("build.*"))), 2)

    def test_default_builds_use_fresh_paths(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "frontend").mkdir()
            (root / ".version").write_text("0.1.4\n")
            builds = root / "dist/macos/camstream-camera-0.1.4"
            existing = builds / "Camstream Camera.app"
            existing.mkdir(parents=True)
            marker = existing / "original"
            marker.touch()

            for _ in range(2):
                result = subprocess.run(
                    ["bash", "-c", 'source "$1"; SCRIPT_DIR="$2"; VERSION_FILE="$2/.version"; '
                     'npm() { :; }; python3() { mkdir "$5"; }; mac:build',
                     "test", str(ROOT / "taskfile"), str(root)],
                    cwd=root, text=True, capture_output=True, check=False,
                )
                self.assertEqual(result.returncode, 0, result.stderr)

            self.assertTrue(marker.is_file())
            self.assertEqual(len(list(builds.glob("build.*/Camstream Camera.app"))), 2)

    def test_default_packages_use_fresh_paths(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "frontend").mkdir()
            (root / ".version").write_text("0.1.4\n")
            releases = root / "dist/releases"
            name = "camstream-camera-0.1.4-darwin-arm64-development"
            existing = releases / name
            existing.mkdir(parents=True)
            marker = existing / "original"
            marker.touch()

            for _ in range(2):
                result = subprocess.run(
                    ["bash", "-c", 'source "$1"; SCRIPT_DIR="$2"; VERSION_FILE="$2/.version"; '
                     'npm() { :; }; python3() { [[ "$3" == --development ]] && '
                     'mkdir "$5/camstream-camera-$2-darwin-arm64-development"; }; mac:package',
                     "test", str(ROOT / "taskfile"), str(root)],
                    cwd=root, text=True, capture_output=True, check=False,
                )
                self.assertEqual(result.returncode, 0, result.stderr)

            self.assertTrue(marker.is_file())
            self.assertEqual(len(list(releases.glob(f"camstream-camera-0.1.4/build.*/{name}"))), 2)


if __name__ == "__main__":
    unittest.main()

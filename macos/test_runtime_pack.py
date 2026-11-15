"""Camera-free regressions for the runtime pack trust boundary."""
import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import runtime_pack as pack


class RuntimePackTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.lock = pack.load_lock()

    def source_record(self):
        return {"file": "source.tar", "sha256": hashlib.sha256(b"source").hexdigest(), "url": "https://example.invalid/source.tar"}

    def test_offline_cache_does_not_download(self):
        record = self.source_record()
        with patch("urllib.request.urlopen", side_effect=AssertionError("unexpected download")):
            with self.assertRaisesRegex(ValueError, "missing cached"):
                pack.fetch(record, self.root, True)
            (self.root / record["file"]).write_bytes(b"source")
            self.assertEqual(pack.fetch(record, self.root, True).read_bytes(), b"source")
            (self.root / record["file"]).write_bytes(b"corrupt")
            with self.assertRaisesRegex(ValueError, "digest mismatch"):
                pack.fetch(record, self.root, False)

    def test_download_digest_mismatch_never_enters_cache(self):
        with patch("urllib.request.urlopen", return_value=io.BytesIO(b"wrong")):
            with self.assertRaisesRegex(ValueError, "digest mismatch"):
                pack.fetch(self.source_record(), self.root, False)
        self.assertEqual(list(self.root.iterdir()), [])

    def test_archive_paths_links_and_duplicates_are_rejected_before_writes(self):
        for name, kind in [("../escape", "file"), ("/absolute", "file"), ("link", "link"), ("twice", "duplicate")]:
            with self.subTest(name=name):
                archive = self.root / "unsafe.tar"
                with tarfile.open(archive, "w") as tar:
                    member = tarfile.TarInfo(name)
                    if kind == "link":
                        member.type, member.linkname = tarfile.SYMTYPE, "/tmp"
                    tar.addfile(member)
                    if kind == "duplicate":
                        tar.addfile(member)
                target = self.root / "unpack"
                with self.assertRaises(ValueError):
                    pack.extract(archive, target)
                self.assertEqual(list(target.iterdir()), [])

    def test_source_release_timestamps_survive_extraction(self):
        archive = self.root / "source.tar"
        with tarfile.open(archive, "w") as tar:
            for name, timestamp in [("configure", 200), ("configure.ac", 100)]:
                member = tarfile.TarInfo(name)
                member.mtime = timestamp
                tar.addfile(member)
        destination = self.root / "source"
        pack.extract(archive, destination)
        self.assertGreater((destination / "configure").stat().st_mtime, (destination / "configure.ac").stat().st_mtime)

    def fixture(self, target=None, corrupt=False, extra=False, mode=0o755):
        files = {"tools/ffmpeg": b"ffmpeg", "tools/mediamtx": b"mediamtx",
                 "licenses/NOTICE.txt": b"notices", "BUILDINFO.json": b"{}"}
        manifest = {"schema": 1, "target": target or self.lock["target"], "recipe_sha256": pack.recipe_hash(self.lock),
                    "files": {name: {"sha256": hashlib.sha256(data).hexdigest(), "mode": 0o755 if name.startswith("tools/") else 0o644}
                              for name, data in files.items()}}
        if corrupt:
            files["tools/ffmpeg"] = b"modified executable"
        if extra:
            files["tools/unlisted"] = b"unlisted executable"
        files["MANIFEST.json"] = json.dumps(manifest).encode()
        archive = self.root / "pack.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            for name, data in files.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                member.mode = mode if name.startswith("tools/") else 0o644
                tar.addfile(member, io.BytesIO(data))
        self.lock["packs"]["darwin-arm64"] = {"file": archive.name, "sha256": pack.sha(archive), "url": None}
        return archive

    def test_valid_prepared_pack(self):
        archive = self.fixture()
        manifest = pack.unpack_locked(archive, self.root / "valid", self.lock, macho=False)
        self.assertIn("tools/ffmpeg", manifest["files"])

    def test_archive_digest_is_checked_before_extraction(self):
        archive = self.fixture()
        archive.write_bytes(b"corrupt")
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            pack.unpack_locked(archive, self.root / "bad", self.lock, macho=False)
        self.assertFalse((self.root / "bad").exists())

    def test_wrong_target_or_recipe_never_runs_binaries(self):
        wrong = {**self.lock["target"], "arch": "amd64"}
        archive = self.fixture(target=wrong)
        with patch.object(pack, "inspect_macho", side_effect=AssertionError("untrusted binary inspection")):
            with self.assertRaisesRegex(ValueError, "target/recipe mismatch"):
                pack.unpack_locked(archive, self.root / "bad-target", self.lock)
            archive = self.fixture()
            self.lock["runtime_id"] = "another-recipe"
            with self.assertRaisesRegex(ValueError, "target/recipe mismatch"):
                pack.unpack_locked(archive, self.root / "bad-recipe", self.lock)

    def test_modified_file_inventory_and_permissions(self):
        for name, options in [("content", {"corrupt": True}), ("extra", {"extra": True}), ("mode", {"mode": 0o644})]:
            with self.subTest(name=name):
                archive = self.fixture(**options)
                with self.assertRaisesRegex(ValueError, "mismatch"):
                    pack.unpack_locked(archive, self.root / name, self.lock, macho=False)

    def test_pack_digest_is_not_part_of_its_recipe_hash(self):
        before = pack.recipe_hash(self.lock)
        self.fixture()
        self.assertEqual(pack.recipe_hash(self.lock), before)
        self.lock["configure"]["ffmpeg"].append("--changed")
        self.assertNotEqual(pack.recipe_hash(self.lock), before)

    def test_macho_architecture_target_and_external_dependencies(self):
        def check(arch="arm64", minimum="26.0", dependency="/usr/lib/libSystem.B.dylib"):
            def command(argv, **_):
                if "lipo" in argv[0]:
                    return arch
                if "-l" in argv:
                    return "cmd LC_BUILD_VERSION\nminos " + minimum
                return "ffmpeg:\n\t" + dependency + " (compatibility version 1.0.0)"
            with patch("subprocess.check_output", side_effect=command):
                return pack.inspect_macho(Path("ffmpeg"), self.lock["target"])
        self.assertEqual(check()["minimum_macos"], "26.0")
        for options in [{"arch": "x86_64"}, {"minimum": "27.0"}, {"dependency": "/opt/homebrew/lib/libx264.dylib"},
                        {"dependency": "@rpath/libx264.dylib"}]:
            with self.assertRaises(ValueError):
                check(**options)


if __name__ == "__main__":
    unittest.main()

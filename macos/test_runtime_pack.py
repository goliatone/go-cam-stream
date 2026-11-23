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

    def fixture(self, target=None, corrupt=False, extra=False, mode=0o755, omit=None):
        files = {"tools/ffmpeg": b"ffmpeg", "tools/mediamtx": b"mediamtx",
                 "licenses/NOTICE.txt": b"notices", "BUILDINFO.json": b"{}"}
        for name in pack.runtime_tools(self.lock):
            files["tools/" + name] = name.encode()
        for source in self.lock["sources"].values():
            if not source.get("prebuilt"):
                data = ("source-" + source["file"]).encode()
                source["sha256"] = hashlib.sha256(data).hexdigest()
                files["sources/" + source["file"]] = data
        for record in self.lock.get("patches", []):
            data = record["file"].encode()
            record["sha256"] = hashlib.sha256(data).hexdigest()
            files["recipe/" + record["file"]] = data
        files["recipe/macos/runtime.lock.json"] = json.dumps({**self.lock, "packs": {}}).encode()
        if omit:
            files.pop(omit)
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
        self.fixture()
        before = pack.recipe_hash(self.lock)
        self.fixture()
        self.assertEqual(pack.recipe_hash(self.lock), before)
        self.lock["configure"]["ffmpeg"].append("--changed")
        self.assertNotEqual(pack.recipe_hash(self.lock), before)

    def test_audio_pack_requires_probe_patch_and_sources(self):
        names = ["tools/ffprobe", "recipe/" + self.lock["patches"][0]["file"],
                 "sources/" + self.lock["sources"]["opus"]["file"]]
        for i, name in enumerate(names):
            with self.subTest(name=name):
                archive = self.fixture(omit=name)
                with self.assertRaisesRegex(ValueError, "inventory mismatch"):
                    pack.unpack_locked(archive, self.root / str(i), self.lock, macho=False)

    def test_patch_content_and_order_are_recipe_inputs(self):
        before = pack.recipe_hash(self.lock)
        paths = pack.verified_patches(self.lock)
        self.assertEqual([str(path.relative_to(pack.ROOT)) for path in paths],
                         [record["file"] for record in self.lock["patches"]])
        self.lock["patches"].reverse()
        self.assertNotEqual(pack.recipe_hash(self.lock), before)
        record = self.lock["patches"][0]
        path = self.root / record["file"]
        path.parent.mkdir(parents=True)
        path.write_bytes(b"changed patch")
        with self.assertRaisesRegex(ValueError, "patch digest mismatch"):
            pack.verified_patches(self.lock, self.root)

    def test_candidate_promotion_rejects_changed_inputs_and_tools(self):
        (self.root / "tools").mkdir()
        tools = {}
        for name in pack.runtime_tools(self.lock):
            path = self.root / "tools" / name
            path.write_text(name)
            tools[name] = {"sha256": pack.sha(path)}
        info = {"lock": self.lock, "tools": tools}
        info_path = self.root / "BUILDINFO.json"
        info_path.write_text(json.dumps(info))
        self.lock["proof_candidate"] = {"buildinfo_sha256": pack.sha(info_path)}
        pack.verified_candidate(self.root, self.lock)
        (self.root / "tools/ffmpeg").write_text("changed")
        with self.assertRaisesRegex(ValueError, "tool digest mismatch"):
            pack.verified_candidate(self.root, self.lock)
        (self.root / "tools/ffmpeg").write_text("ffmpeg")
        self.lock["configure"]["ffmpeg"].append("--changed")
        with self.assertRaisesRegex(ValueError, "inputs differ"):
            pack.verified_candidate(self.root, self.lock)
        info_path.write_text("{}")
        with self.assertRaisesRegex(ValueError, "BUILDINFO digest mismatch"):
            pack.verified_candidate(self.root, self.lock)

    def test_missing_device_selectors_and_encoder_are_rejected(self):
        names = "avfoundation lavfi libx264 wrapped_avframe rawvideo h264 rtsp null scale pad setsar fps format concat tcp pipe file libopus pcm_s16le pcm_s32le pcm_f32le pcm_f64le opus aresample aformat anullsrc sine s16le"
        def output(argv, **kwargs):
            if "-h" in argv:
                return "-camstream_audio_uid -camstream_list_audio_devices -camstream_video_uid -camstream_list_video_devices"
            if "-version" in argv:
                return "ffprobe version " + self.lock["sources"]["ffmpeg"]["version"]
            return names
        with patch("subprocess.check_output", side_effect=output):
            pack.inspect_capabilities(self.root, self.lock)
        for missing in ("-camstream_audio_uid", "-camstream_video_uid", "-camstream_list_video_devices", "libopus"):
            with patch("subprocess.check_output", side_effect=lambda argv, **kw: output(argv, **kw).replace(missing, "")):
                with self.assertRaisesRegex(ValueError, "missing"):
                    pack.inspect_capabilities(self.root, self.lock)

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

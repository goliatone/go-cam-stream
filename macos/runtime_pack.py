"""Shared pinned runtime download, extraction and validation. No installation."""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_LOCK = ROOT / "macos/runtime.lock.json"
DEFAULT_CACHE = ROOT / ".cache/camera-runtime"


def sha(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def load_lock(path=DEFAULT_LOCK):
    lock = json.loads(Path(path).read_text())
    if lock.get("schema") != 1 or lock.get("target") != {"os": "darwin", "arch": "arm64", "minimum_macos": "26.0"}:
        raise ValueError("unsupported runtime lock schema/target")
    for source in lock["sources"].values():
        if not re.fullmatch(r"[a-zA-Z0-9._-]+", source["file"]) or not re.fullmatch(r"[0-9a-f]{64}", source["sha256"]):
            raise ValueError("invalid locked filename/digest")
    seen = set()
    for patch in lock.get("patches", []):
        name = patch["file"]
        if not re.fullmatch(r"macos/patches/ffmpeg/[a-zA-Z0-9._-]+\.patch", name) or name in seen or not re.fullmatch(r"[0-9a-f]{64}", patch["sha256"]):
            raise ValueError("invalid locked patch path/digest")
        seen.add(name)
    return lock


def runtime_tools(lock):
    return ("ffmpeg", "ffprobe", "mediamtx") if "opus" in lock["sources"] else ("ffmpeg", "mediamtx")


def verified_patches(lock, root=ROOT):
    paths = []
    for record in lock.get("patches", []):
        path = Path(root) / safe_path(record["file"])
        if not path.is_file() or sha(path) != record["sha256"]:
            raise ValueError("runtime patch digest mismatch: " + record["file"])
        paths.append(path)
    return paths


def verified_candidate(candidate, lock):
    candidate = Path(candidate)
    info_path = candidate / "BUILDINFO.json"
    expected = lock.get("proof_candidate", {}).get("buildinfo_sha256")
    if not expected or sha(info_path) != expected:
        raise ValueError("proof candidate BUILDINFO digest mismatch")
    info = json.loads(info_path.read_text())
    for field in ("target", "sources", "configure", "patches"):
        if info["lock"].get(field) != lock.get(field):
            raise ValueError("proof candidate inputs differ: " + field)
    for name in runtime_tools(lock):
        if sha(candidate / "tools" / name) != info["tools"][name]["sha256"]:
            raise ValueError("proof candidate tool digest mismatch: " + name)
    return info


def inspect_capabilities(tools, lock):
    required = {"-devices": ["avfoundation", "lavfi"], "-encoders": ["libx264", "wrapped_avframe", "rawvideo"],
                "-decoders": ["h264", "rawvideo", "wrapped_avframe"], "-muxers": ["rtsp", "null", "rawvideo"],
                "-filters": ["scale", "fps", "format"], "-demuxers": ["rtsp", "concat"],
                "-protocols": ["tcp", "pipe", "file"]}
    audio = "opus" in lock["sources"]
    if audio:
        required["-encoders"] += ["libopus", "pcm_s16le"]
        required["-decoders"] += ["opus", "pcm_s16le", "pcm_s32le", "pcm_f32le", "pcm_f64le"]
        required["-filters"] += ["aresample", "aformat", "anullsrc", "sine"]
        required["-muxers"] += ["s16le"]
    ffmpeg = str(Path(tools) / "ffmpeg")
    capabilities = {}
    for option, names in required.items():
        result = subprocess.check_output([ffmpeg, "-hide_banner", option], text=True, stderr=subprocess.DEVNULL, timeout=20)
        if any(name not in result.split() for name in names):
            raise ValueError("runtime missing required capabilities: " + option)
        capabilities[option] = result
    video_identity = lock.get("video_device_identity", False)
    if audio or video_identity:
        result = subprocess.check_output([ffmpeg, "-hide_banner", "-h", "demuxer=avfoundation"], text=True, stderr=subprocess.DEVNULL, timeout=20)
        if audio and any(name not in result.split() for name in ("-camstream_audio_uid", "-camstream_list_audio_devices")):
            raise ValueError("runtime missing exact audio UID/discovery capabilities")
        if video_identity and any(name not in result.split() for name in ("-camstream_video_uid", "-camstream_list_video_devices")):
            raise ValueError("runtime missing exact video UID/discovery capabilities")
        capabilities["avfoundation_options"] = result
    if video_identity:
        inspect_video_inventory(ffmpeg)
    if audio:
        version = subprocess.check_output([str(Path(tools) / "ffprobe"), "-version"], text=True, stderr=subprocess.DEVNULL, timeout=20)
        if not version.startswith("ffprobe version " + lock["sources"]["ffmpeg"]["version"]):
            raise ValueError("runtime ffprobe version mismatch")
    return capabilities


def inspect_video_inventory(ffmpeg):
    # Exercise the same inventory command as the Go helper. An empty list is
    # valid on build hosts without cameras. Keep names and IDs out of build info.
    result = subprocess.run([ffmpeg, "-hide_banner", "-f", "avfoundation", "-list_devices", "true",
                             "-camstream_list_video_devices", "true", "-i", ""], stdout=subprocess.PIPE,
                            stderr=subprocess.DEVNULL, timeout=20)
    if result.returncode != 251 or len(result.stdout) > 128 * 1024:
        raise ValueError("runtime video inventory did not complete")
    try:
        inventory = json.loads(result.stdout.decode("utf-8"))
    except (UnicodeError, ValueError):
        raise ValueError("invalid runtime video inventory JSON") from None
    if (not isinstance(inventory, dict) or set(inventory) != {"version", "devices"}
            or type(inventory["version"]) is not int or inventory["version"] != 1
            or not isinstance(inventory["devices"], list)):
        raise ValueError("invalid runtime video inventory envelope")
    ids = set()
    for device in inventory["devices"]:
        if (not isinstance(device, dict) or set(device) != {"id", "name", "builtin"}
                or not isinstance(device["id"], str) or not device["id"] or device["id"] in ids
                or not isinstance(device["name"], str) or not device["name"] or type(device["builtin"]) is not bool):
            raise ValueError("invalid runtime video inventory device")
        ids.add(device["id"])


def recipe_hash(lock):
    return hashlib.sha256(json.dumps({k: v for k, v in lock.items() if k != "packs"}, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def fetch(record, cache, offline):
    cache = Path(cache)
    cache.mkdir(parents=True, exist_ok=True)
    name = record["file"]
    if Path(name).name != name or name in (".", ".."):
        raise ValueError("invalid download filename")
    path = cache / name
    if not path.exists():
        if offline or not record.get("url"):
            raise ValueError(f"missing cached artifact: {path}; preseed it or supply an approved download URL in the lock")
        if not record["url"].startswith("https://"):
            raise ValueError("artifact URL must use HTTPS")
        with tempfile.NamedTemporaryFile(dir=cache, delete=False) as temp:
            temporary = Path(temp.name)
            try:
                with urllib.request.urlopen(record["url"], timeout=60) as response:
                    shutil.copyfileobj(response, temp)
                temp.flush()
                if sha(temporary) != record["sha256"]:
                    raise ValueError(f"download digest mismatch: {name}")
                temporary.replace(path)
            finally:
                temporary.unlink(missing_ok=True)
    if not path.is_file() or sha(path) != record["sha256"]:
        raise ValueError(f"cached artifact digest mismatch: {name}")
    return path


def safe_path(name):
    path = PurePosixPath(name)
    if path.is_absolute() or ".." in path.parts or "\\" in name or any(ord(c) < 32 for c in name):
        raise ValueError("unsafe archive path")
    return path


def extract(archive, destination):
    """Extract regular files/directories only; never follow tar links/devices."""
    destination = Path(destination)
    if destination.exists() and any(destination.iterdir()):
        raise ValueError("extraction destination must be empty")
    destination.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive) as tar:
        members = tar.getmembers()
        if len(members) > 50000 or sum(m.size for m in members) > 1024**3:
            raise ValueError("archive exceeds extraction limits")
        seen = set()
        for member in members:
            path = safe_path(member.name)
            if not (member.isfile() or member.isdir()) or str(path) in seen:
                raise ValueError("archive has duplicate paths or unsupported entry types")
            seen.add(str(path))
        for member in members:
            path = destination / safe_path(member.name)
            if member.isdir():
                path.mkdir(parents=True, exist_ok=True)
            else:
                path.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(member) as source, path.open("xb") as output:
                    shutil.copyfileobj(source, output)
                path.chmod(0o755 if member.mode & 0o111 else 0o644)
                # Preserve release timestamps so generated configure/Makefile.in
                # do not spuriously require autotools during offline builds.
                os.utime(path, (member.mtime, member.mtime))


def inspect_macho(path, target):
    def output(*args):
        return subprocess.check_output(args, text=True).strip()
    if output("/usr/bin/lipo", "-archs", str(path)) != target["arch"]:
        raise ValueError(f"wrong Mach-O architecture: {path.name}")
    loads = output("/usr/bin/otool", "-l", str(path))
    versions = re.findall(r"\bminos ([0-9.]+)", loads)
    if not versions:
        versions = re.findall(r"\bversion ([0-9.]+)", loads.split("LC_VERSION_MIN_MACOSX")[-1])[:1] if "LC_VERSION_MIN_MACOSX" in loads else []
    version = lambda v: tuple(int(x) for x in v.split("."))
    if not versions or any(version(v) > version(target["minimum_macos"]) for v in versions):
        raise ValueError(f"unsupported Mach-O deployment target: {path.name}")
    dependencies = [line.strip().split(" (", 1)[0] for line in output("/usr/bin/otool", "-L", str(path)).splitlines()[1:]]
    if any(not d.startswith(("/usr/lib/", "/System/Library/")) for d in dependencies) or "LC_RPATH" in loads:
        raise ValueError(f"non-system runtime dependency: {path.name}")
    return {"minimum_macos": versions[0], "dependencies": dependencies}


def unpack_locked(archive, destination, lock, macho=True):
    record = lock["packs"].get("darwin-arm64")
    if not record or sha(archive) != record["sha256"]:
        raise ValueError("runtime pack digest mismatch or no approved pack in lock")
    extract(archive, destination)
    destination = Path(destination)
    manifest = json.loads((destination / "MANIFEST.json").read_text())
    if manifest.get("schema") != 1 or manifest.get("target") != lock["target"] or manifest.get("recipe_sha256") != recipe_hash(lock):
        raise ValueError("runtime pack target/recipe mismatch")
    actual = {p.relative_to(destination).as_posix() for p in destination.rglob("*") if p.is_file()} - {"MANIFEST.json"}
    files = manifest["files"]
    required = {"tools/" + name for name in runtime_tools(lock)} | {"licenses/NOTICE.txt", "BUILDINFO.json"}
    if lock.get("patches"):
        required |= {"recipe/" + p["file"] for p in lock["patches"]}
        required |= {"recipe/macos/runtime.lock.json"}
        required |= {"sources/" + s["file"] for s in lock["sources"].values() if not s.get("prebuilt")}
    if actual != set(files) or not required.issubset(actual):
        raise ValueError("runtime pack inventory mismatch")
    for name, entry in files.items():
        path = destination / safe_path(name)
        if sha(path) != entry["sha256"] or path.stat().st_mode & 0o777 != entry["mode"]:
            raise ValueError(f"runtime pack file mismatch: {name}")
    if lock.get("patches"):
        verified_patches(lock, destination / "recipe")
        embedded = load_lock(destination / "recipe/macos/runtime.lock.json")
        if recipe_hash(embedded) != recipe_hash(lock):
            raise ValueError("runtime embedded recipe mismatch")
        for source in lock["sources"].values():
            if not source.get("prebuilt") and sha(destination / "sources" / source["file"]) != source["sha256"]:
                raise ValueError("runtime source archive digest mismatch")
    for name in runtime_tools(lock):
        path = destination / "tools" / name
        if path.stat().st_mode & 0o111 == 0:
            raise ValueError("runtime tool is not executable")
        if macho:
            inspect_macho(path, lock["target"])
    if macho:
        inspect_capabilities(destination / "tools", lock)
    return manifest


def selected_pack(lock, cache=DEFAULT_CACHE, supplied=None, offline=True):
    if supplied:
        return Path(supplied).resolve()
    record = lock["packs"].get("darwin-arm64")
    if not record:
        raise ValueError("no approved prepared runtime pack; build it first")
    return fetch(record, Path(cache) / "packs", offline)

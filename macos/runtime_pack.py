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
    return lock


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
    if actual != set(files) or not {"tools/ffmpeg", "tools/mediamtx", "licenses/NOTICE.txt", "BUILDINFO.json"}.issubset(actual):
        raise ValueError("runtime pack inventory mismatch")
    for name, entry in files.items():
        path = destination / safe_path(name)
        if sha(path) != entry["sha256"] or path.stat().st_mode & 0o777 != entry["mode"]:
            raise ValueError(f"runtime pack file mismatch: {name}")
    for name in ("ffmpeg", "mediamtx"):
        path = destination / "tools" / name
        if path.stat().st_mode & 0o111 == 0:
            raise ValueError("runtime tool is not executable")
        if macho:
            inspect_macho(path, lock["target"])
    return manifest


def selected_pack(lock, cache=DEFAULT_CACHE, supplied=None, offline=True):
    if supplied:
        return Path(supplied).resolve()
    record = lock["packs"].get("darwin-arm64")
    if not record:
        raise ValueError("no approved prepared runtime pack; build it first")
    return fetch(record, Path(cache) / "packs", offline)

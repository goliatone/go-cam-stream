#!/usr/bin/env python3
"""Resolve the pinned native development runtime without changing system tools."""
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
PIN = json.loads((ROOT / "data/runtime/go2rtc.lock.json").read_text())["artifacts"]["darwin/arm64"]
ARCHIVE_SHA256 = PIN["archive_sha256"]
BINARY_SHA256 = PIN["sha256"]


def main():
    if platform.system() != "Darwin" or platform.machine() != "arm64":
        raise SystemExit("Native dev playback currently requires an Apple Silicon Mac.")
    dest = ROOT / ".cache" / "dev-tools" / "go2rtc"
    if dest.is_file() and hashlib.sha256(dest.read_bytes()).hexdigest() == BINARY_SHA256:
        print(dest)
        return
    url = PIN["url"]
    with urllib.request.urlopen(url, timeout=60) as response:
        archive = response.read(64 << 20)
    if hashlib.sha256(archive).hexdigest() != ARCHIVE_SHA256:
        raise SystemExit("Development go2rtc archive checksum mismatch")
    with zipfile.ZipFile(io.BytesIO(archive)) as zipped:
        binary = zipped.read("go2rtc")
    if hashlib.sha256(binary).hexdigest() != BINARY_SHA256:
        raise SystemExit("Development go2rtc executable checksum mismatch")
    dest.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=dest.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            output.write(binary)
            output.flush()
            os.fchmod(output.fileno(), 0o755)
            os.replace(temporary, dest)
        finally:
            temporary.unlink(missing_ok=True)
    print(dest)


if __name__ == "__main__":
    main()

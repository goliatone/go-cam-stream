#!/usr/bin/env python3
"""Build the native camera app or proof offline from a hash-locked runtime pack."""
import argparse
import os
import platform
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile

from runtime_pack import DEFAULT_CACHE, DEFAULT_LOCK, ROOT, inspect_macho, load_lock, selected_pack, unpack_locked


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def assemble(runtime, app, lock, production=False, version="0.1.0"):
    executable = "CamstreamCamera" if production else "CameraProof"
    bundle_name = "Camstream Camera" if production else "Camstream Camera Proof"
    root = ROOT
    contents = app / "Contents"
    for name in ("MacOS", "Helpers", "Frameworks", "Resources"):
        (contents / name).mkdir(parents=True)
    for name in ("ffmpeg", "mediamtx"):
        shutil.copy2(runtime / "tools" / name, contents / "Helpers" / name)
    for name in ("licenses", "sources", "recipe"):
        shutil.copytree(runtime / name, contents / "Resources" / name)
    for name in ("BUILDINFO.json", "MANIFEST.json"):
        shutil.copy2(runtime / name, contents / "Resources" / ("runtime-" + name))
    run("go", "build", "-trimpath", "-buildvcs=false", "-o", str(contents / "Helpers" / "camstream-camera"),
        "./cmd/camstream-camera", cwd=root,
        env={**os.environ, "CGO_ENABLED": "0", "GOOS": "darwin", "GOARCH": "arm64", "GOWORK": "off", "GOFLAGS": ""})
    sources = sorted((root / "macos/CameraApp").glob("*.swift")) if production else [
        root / "macos/CameraProof/StartIntent.swift", root / "macos/CameraProof/main.swift"]
    run("xcrun", "swiftc", "-swift-version", "5", "-target", "arm64-apple-macosx" + lock["target"]["minimum_macos"],
        "-framework", "AppKit", "-framework", "AVFoundation",
        *[str(p) for p in sources], "-o", str(contents / "MacOS" / executable))
    with (contents / "Info.plist").open("wb") as file:
        plistlib.dump({"CFBundleExecutable": executable,
            "CFBundleIdentifier": "com.goliatone.camstream.camera" + ("" if production else ".proof"),
            "CFBundleName": bundle_name, "CFBundleDisplayName": bundle_name, "LSUIElement": production,
            "CFBundlePackageType": "APPL", "CFBundleVersion": "1", "CFBundleShortVersionString": version,
            "LSMinimumSystemVersion": lock["target"]["minimum_macos"], "NSHighResolutionCapable": True,
            "NSCameraUsageDescription": "Share the built-in webcam as a private RTSP camera while this app is running."
        }, file)
    entitlements = str(root / "macos/CameraProof/entitlements.plist")
    inspect_macho(contents / "MacOS" / executable, lock["target"])
    for binary in sorted((contents / "Helpers").iterdir()):
        inspect_macho(binary, lock["target"])
        run("codesign", "--force", "--sign", "-", "--options", "runtime", "--entitlements", entitlements, str(binary))
    run("codesign", "--force", "--sign", "-", "--options", "runtime", "--entitlements", entitlements, str(app))
    run("codesign", "--verify", "--deep", "--strict", str(app))


def main(production=False):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default="0.1.0", help="application version (local development build)")
    parser.add_argument("--runtime-pack", type=Path, help="preseeded pack (must match the lock)")
    parser.add_argument("--lock", type=Path, default=DEFAULT_LOCK)
    parser.add_argument("--cache", type=Path, default=DEFAULT_CACHE)
    parser.add_argument("--download", action="store_true", help="allow the exact URL in the pack lock when cache is empty")
    parser.add_argument("--output", type=Path, default=ROOT / ("dist/Camstream Camera.app" if production else "dist/Camstream Camera Proof.app"))
    args = parser.parse_args()
    if platform.system() != "Darwin" or platform.machine() != "arm64":
        parser.error("this proof builder requires Apple Silicon macOS")
    app = args.output.resolve()
    if app.exists() or app.suffix != ".app":
        parser.error("output must be a new .app path")
    try:
        lock = load_lock(args.lock)
        archive = selected_pack(lock, args.cache, args.runtime_pack, offline=not args.download)
        app.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix=".camera-app-", dir=app.parent) as temp:
            runtime = Path(temp) / "runtime"
            unpack_locked(archive, runtime, lock)
            staged = Path(temp) / app.name
            assemble(runtime, staged, lock, production, args.version)
            staged.rename(app)
        print(f"Local ad-hoc hardened app (macOS 26+, arm64): {app}")
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"App build failed: {error}\n")


if __name__ == "__main__":
    main()

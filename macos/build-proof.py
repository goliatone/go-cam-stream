#!/usr/bin/env python3
"""Build the native camera app or proof offline from a hash-locked runtime pack."""
import argparse
import os
import platform
import json
import re
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
from uuid import uuid4

from conversation_build import BUILD_ID_KEY, stamp_conversation_page
from runtime_pack import DEFAULT_CACHE, DEFAULT_LOCK, ROOT, inspect_macho, load_lock, selected_pack, unpack_locked, runtime_tools


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def validate_version(version):
    if not re.fullmatch(r"(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,1})\.(0|[1-9][0-9]{0,1})", version):
        raise ValueError("version must be MAJOR.MINOR.PATCH (up to 4/2/2 digits, no leading zeros)")
    return version


def sign(path, identity, entitlements=None):
    args = ["codesign", "--force", "--sign", identity, "--options", "runtime"]
    if identity != "-":
        args.append("--timestamp")
    if entitlements:
        args.extend(["--entitlements", str(entitlements)])
    run(*args, str(path))


def assemble(runtime, app, lock, production=False, version="0.1.0", identity="-"):
    validate_version(version)
    if identity != "-" and not identity.startswith("Developer ID Application: "):
        raise ValueError("use a Developer ID Application signing identity")
    executable = "CamstreamCamera" if production else "CameraProof"
    bundle_name = "Camstream Camera" if production else "Camstream Camera Proof"
    build_id = uuid4().hex if production else None
    root = ROOT
    contents = app / "Contents"
    for name in ("MacOS", "Helpers", "Frameworks", "Resources"):
        (contents / name).mkdir(parents=True)
    if production:
        # Build from the same checkout and validate the exact bytes that will
        # be signed. A shared dist directory can contain stale assets.
        guest = contents / "Resources" / "Sharing"
        run("npm", "run", "build:share", cwd=root / "frontend",
            env={**os.environ, "CAMSTREAM_SHARE_OUT_DIR": str(guest)})
        if not (guest / "index.html").is_file():
            raise ValueError("Camera Sharing guest artifact is missing")
        conversations = contents / "Resources" / "Conversations"
        run("npm", "run", "build:conversation", cwd=root / "frontend",
            env={**os.environ, "CAMSTREAM_CONVERSATION_OUT_DIR": str(conversations)})
        if not (conversations / "index.html").is_file():
            raise ValueError("Conversation guest artifact is missing")
        stamp_conversation_page(conversations / "index.html", build_id)
    for name in runtime_tools(lock):
        shutil.copy2(runtime / "tools" / name, contents / "Helpers" / name)
    for name in ("licenses", "sources", "recipe"):
        shutil.copytree(runtime / name, contents / "Resources" / name)
    for name in ("BUILDINFO.json", "MANIFEST.json"):
        shutil.copy2(runtime / name, contents / "Resources" / ("runtime-" + name))
    shutil.copy2(root / "LICENSE", contents / "Resources/licenses/camstream.LICENSE")
    info = {"schema": 1, "version": version, "target": lock["target"],
            "runtime_id": lock["runtime_id"],
            "runtime_pack_sha256": lock["packs"]["darwin-arm64"]["sha256"],
            "signing": "ad-hoc" if identity == "-" else "Developer ID"}
    (contents / "Resources/BUILDINFO.json").write_text(json.dumps(info, indent=2) + "\n")
    run("go", "build", "-trimpath", "-buildvcs=false", "-o", str(contents / "Helpers" / "camstream-camera"),
        "./cmd/camstream-camera", cwd=root,
        env={**os.environ, "CGO_ENABLED": "0", "GOOS": "darwin", "GOARCH": "arm64", "GOWORK": "off", "GOFLAGS": ""})
    sources = sorted((root / "macos/CameraApp").glob("*.swift")) if production else [
        root / "macos/CameraProof/StartIntent.swift", root / "macos/CameraProof/main.swift"]
    native_flags = ["-import-objc-header", str(root / "macos/CameraApp/HostPowerMessages.h")] if production else []
    run("xcrun", "swiftc", "-swift-version", "5", "-target", "arm64-apple-macosx" + lock["target"]["minimum_macos"],
        "-framework", "AppKit", "-framework", "AVFoundation", *native_flags,
        *[str(p) for p in sources], "-o", str(contents / "MacOS" / executable))
    permission_text = {"NSCameraUsageDescription": "Share the built-in webcam as a private RTSP camera while this app is running."}
    if production:
        permission_text["NSCameraUsageDescription"] = "Use your webcam when you start a private stream or explicitly enable it in a conversation."
        permission_text["NSMicrophoneUsageDescription"] = "Use your microphone when you enable it for a private stream or explicitly enable it in a conversation."
    url_types = [{"CFBundleURLName": "com.goliatone.camstream.camera.conversation",
                  "CFBundleURLSchemes": ["camstream"], "CFBundleTypeRole": "Viewer"}] if production else []
    with (contents / "Info.plist").open("wb") as file:
        plistlib.dump({"CFBundleExecutable": executable,
            "CFBundleIdentifier": "com.goliatone.camstream.camera" + ("" if production else ".proof"),
            "CFBundleName": bundle_name, "CFBundleDisplayName": bundle_name, "LSUIElement": False,
            "CFBundlePackageType": "APPL", "CFBundleVersion": version, "CFBundleShortVersionString": version,
            "LSMinimumSystemVersion": lock["target"]["minimum_macos"], "NSHighResolutionCapable": True,
            "CFBundleURLTypes": url_types, **permission_text,
            **({BUILD_ID_KEY: build_id} if build_id else {}),
        }, file)
    entitlements = str(root / ("macos/CameraApp/entitlements.plist" if production else "macos/CameraProof/entitlements.plist"))
    inspect_macho(contents / "MacOS" / executable, lock["target"])
    for binary in sorted((contents / "Helpers").iterdir()):
        inspect_macho(binary, lock["target"])
        sign(binary, identity, entitlements)
    sign(app, identity, entitlements)
    run("codesign", "--verify", "--deep", "--strict", str(app))
    return build_id


def main(production=False):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default="0.1.0", help="application version (local development build)")
    parser.add_argument("--sign-identity", default="-", help="Developer ID Application identity; defaults to local ad-hoc")
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
            build_id = assemble(runtime, staged, lock, production, args.version, args.sign_identity)
            staged.rename(app)
        print(f"Hardened app ({'ad-hoc' if args.sign_identity == '-' else 'Developer ID; not yet notarized'}): {app}")
        if build_id:
            print(f"Build ID: {build_id}")
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"App build failed: {error}\n")


if __name__ == "__main__":
    main()

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


def compile_app_icon(resources, minimum_macos):
    """Compile the Icon Composer document into Resources and return its Info.plist keys.

    actool ships with Xcode 26 or later; it writes Assets.car for the system icon styles and an
    AppIcon.icns fallback that release packaging reuses as the DMG volume icon.
    """
    with tempfile.TemporaryDirectory() as temp:
        partial = Path(temp) / "icon.plist"
        try:
            run("xcrun", "actool", str(ROOT / "macos/CameraApp/AppIcon.icon"), "--compile", str(resources),
                "--platform", "macosx", "--minimum-deployment-target", minimum_macos, "--app-icon", "AppIcon",
                "--output-partial-info-plist", str(partial), "--output-format", "human-readable-text",
                "--errors", "--warnings", capture_output=True, text=True)
        except subprocess.CalledProcessError as error:
            detail = (error.stdout or "") + (error.stderr or "")
            raise ValueError("app icon compilation failed (needs Xcode 26 or later): " + detail.strip()[-2000:]) from None
        keys = plistlib.loads(partial.read_bytes()) if partial.is_file() else {}
    icon = {key: keys.get(key) for key in ("CFBundleIconFile", "CFBundleIconName")}
    if icon != {"CFBundleIconFile": "AppIcon", "CFBundleIconName": "AppIcon"} or not all(
            (resources / name).is_file() for name in ("Assets.car", "AppIcon.icns")):
        raise ValueError("app icon compilation did not produce Assets.car, AppIcon.icns and the icon keys")
    return icon


def assemble(runtime, app, lock, production=False, version="0.1.0", identity="-"):
    validate_version(version)
    if identity != "-" and not identity.startswith("Developer ID Application: "):
        raise ValueError("use a Developer ID Application signing identity")
    executable = "CamstreamCamera" if production else "CameraProof"
    bundle_name = "Camstream Camera" if production else "Camstream Camera Proof"
    root = ROOT
    contents = app / "Contents"
    for name in ("MacOS", "Helpers", "Frameworks", "Resources"):
        (contents / name).mkdir(parents=True)
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
    run("xcrun", "swiftc", "-swift-version", "5", "-target", "arm64-apple-macosx" + lock["target"]["minimum_macos"],
        "-framework", "AppKit", "-framework", "AVFoundation",
        *[str(p) for p in sources], "-o", str(contents / "MacOS" / executable))
    permission_text = {"NSCameraUsageDescription": "Share the built-in webcam as a private RTSP camera while this app is running."}
    if production:
        permission_text["NSCameraUsageDescription"] = "Share the webcam in a private stream only when you choose Start Camera."
        permission_text["NSMicrophoneUsageDescription"] = "Include microphone sound in your private stream only when you enable microphone audio and choose Start Camera."
    icon = compile_app_icon(contents / "Resources", lock["target"]["minimum_macos"]) if production else {}
    with (contents / "Info.plist").open("wb") as file:
        plistlib.dump({"CFBundleExecutable": executable,
            "CFBundleIdentifier": "com.goliatone.camstream.camera" + ("" if production else ".proof"),
            "CFBundleName": bundle_name, "CFBundleDisplayName": bundle_name, "LSUIElement": False,
            "CFBundlePackageType": "APPL", "CFBundleVersion": version, "CFBundleShortVersionString": version,
            "LSMinimumSystemVersion": lock["target"]["minimum_macos"], "NSHighResolutionCapable": True,
            **icon, **permission_text,
        }, file)
    entitlements = str(root / ("macos/CameraApp/entitlements.plist" if production else "macos/CameraProof/entitlements.plist"))
    inspect_macho(contents / "MacOS" / executable, lock["target"])
    for binary in sorted((contents / "Helpers").iterdir()):
        inspect_macho(binary, lock["target"])
        sign(binary, identity, entitlements)
    sign(app, identity, entitlements)
    run("codesign", "--verify", "--deep", "--strict", str(app))


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
            assemble(runtime, staged, lock, production, args.version, args.sign_identity)
            staged.rename(app)
        print(f"Hardened app ({'ad-hoc' if args.sign_identity == '-' else 'Developer ID; not yet notarized'}): {app}")
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"App build failed: {error}\n")


if __name__ == "__main__":
    main()

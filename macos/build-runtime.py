#!/usr/bin/env python3
"""Build a pinned, static-dependency macOS camera runtime pack; never publish."""
import argparse
import gzip
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile

from runtime_pack import DEFAULT_CACHE, DEFAULT_LOCK, ROOT, extract, fetch, inspect_macho, load_lock, recipe_hash, sha, runtime_tools, verified_patches, verified_candidate, inspect_capabilities


def capture(*args):
    return subprocess.check_output(args, text=True).strip()


def build(args):
    if platform.system() != "Darwin" or platform.machine() != "arm64":
        raise ValueError("runtime build requires Apple Silicon macOS and Xcode tools")
    lock = load_lock(args.lock)
    patches = verified_patches(lock)
    candidate_info = verified_candidate(args.candidate, lock) if args.candidate else None
    tool_names = runtime_tools(lock)
    work = args.work.resolve()
    if work.exists():
        raise ValueError("work directory exists; select a fresh path for a clean build")
    work.mkdir(parents=True)
    prefix = work / "prefix"
    sdk = capture("/usr/bin/xcrun", "--sdk", "macosx", "--show-sdk-path")
    cc = capture("/usr/bin/xcrun", "--find", "clang")
    flags = f"-arch arm64 -mmacosx-version-min={lock['target']['minimum_macos']} -isysroot {sdk}"
    env = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(work), "TMPDIR": str(work), "LC_ALL": "C",
           "MACOSX_DEPLOYMENT_TARGET": lock["target"]["minimum_macos"], "SDKROOT": sdk,
           "CC": cc, "CFLAGS": flags, "LDFLAGS": flags, "SOURCE_DATE_EPOCH": "0",
           "PKG_CONFIG_LIBDIR": str(prefix / "lib/pkgconfig"), "PKG_CONFIG_PATH": "",
           "PKG_CONFIG_ALLOW_SYSTEM_CFLAGS": "1", "PKG_CONFIG_ALLOW_SYSTEM_LIBS": "1"}
    archives, source_dirs, commands = {}, {}, []
    for name, record in lock["sources"].items():
        archives[name] = fetch(record, args.cache / "sources", args.offline)
        folder = work / "source" / name
        extract(archives[name], folder)
        source_dirs[name] = folder if record.get("prebuilt") else next(folder.iterdir())

    def run(name, argv, cwd):
        commands.append({"component": name, "argv": [str(a) for a in argv], "cwd": str(cwd)})
        print(f"{name}: {argv[0]}", flush=True)
        with (work / (name + ".log")).open("ab") as log:
            subprocess.run(argv, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, check=True)

    for patch in patches:
        run("patch", ["/usr/bin/patch", "--batch", "--fuzz=0", "-p1", "-i", str(patch)], source_dirs["ffmpeg"])

    inventory_test = source_dirs["ffmpeg"] / "libavdevice/tests/camstream_video_inventory.m"
    if inventory_test.is_file():
        inventory_binary = work / "video-inventory-test"
        run("video-inventory-smoke", [cc, "-arch", "arm64", "-isysroot", sdk,
            "-mmacosx-version-min=" + lock["target"]["minimum_macos"], "-framework", "Foundation",
            str(inventory_test), "-o", str(inventory_binary)], work)
        run("video-inventory-smoke", [str(inventory_binary)], work)

    for name in (() if candidate_info else ("pkgconf", "x264", *(("opus",) if "opus" in lock["sources"] else ()), "ffmpeg")):
        directory = source_dirs[name]
        options = [str(directory / "configure"), f"--prefix={prefix}", *lock["configure"][name]]
        if name == "ffmpeg":
            options += [f"--cc={cc}", f"--pkg-config={prefix / 'bin/pkgconf'}", "--pkg-config-flags=--static",
                        f"--extra-cflags={flags}", f"--extra-ldflags={flags}", "--arch=aarch64", "--target-os=darwin"]
        run(name, options, directory)
        run(name, ["/usr/bin/make", f"-j{args.jobs}"], directory)
        run(name, ["/usr/bin/make", "install"], directory)

    stage = work / "pack"
    for name in ("tools", "licenses", "sources", "recipe"):
        (stage / name).mkdir(parents=True)
    for name in tool_names:
        source = args.candidate / "tools" / name if candidate_info else (source_dirs["mediamtx"] / name if name == "mediamtx" else prefix / "bin" / name)
        shutil.copy2(source, stage / "tools" / name)
    entitlements = ROOT / ("macos/runtime-entitlements.plist" if "opus" in lock["sources"] else "macos/CameraProof/entitlements.plist")
    for name in tool_names:
        binary = stage / "tools" / name
        binary.chmod(0o755)
        subprocess.run(["/usr/bin/codesign", "--force", "--sign", "-", "--options", "runtime",
                        "--entitlements", str(entitlements), str(binary)], check=True)
    macho = {name: inspect_macho(stage / "tools" / name, lock["target"]) for name in tool_names}
    versions = {name: capture(str(stage / "tools" / name), "--version" if name == "mediamtx" else "-version").splitlines()[0]
                for name in tool_names}
    if not versions["ffmpeg"].startswith("ffmpeg version " + lock["sources"]["ffmpeg"]["version"]) or versions["mediamtx"] != "v" + lock["sources"]["mediamtx"]["version"]:
        raise ValueError("built runtime version mismatch")
    capabilities = inspect_capabilities(stage / "tools", lock)
    run("encode-smoke", [str(stage / "tools/ffmpeg"), "-hide_banner", "-loglevel", "error",
        "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30", "-an", "-frames:v", "30",
        "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-f", "null", "-"], work)
    preview = work / "synthetic-preview.bgra"
    run("preview-smoke", [str(stage / "tools/ffmpeg"), "-hide_banner", "-loglevel", "error",
        "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30", "-an", "-frames:v", "3",
        "-vf", "fps=15,scale=640:360", "-pix_fmt", "bgra", "-c:v", "rawvideo",
        "-f", "rawvideo", str(preview)], work)
    if preview.stat().st_size != 3 * 640 * 360 * 4:
        raise ValueError("preview output size mismatch")
    preview.unlink()
    if any("scale,pad,setsar" in option for option in lock["configure"]["ffmpeg"]):
        run("video-format-smoke", [str(stage / "tools/ffmpeg"), "-hide_banner", "-loglevel", "error",
            "-f", "lavfi", "-i", "testsrc2=size=1920x1440:rate=30", "-an", "-frames:v", "3",
            "-vf", "scale=1280:720:force_original_aspect_ratio=decrease:force_divisible_by=2,pad=1280:720:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30",
            "-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "null", "-"], work)
    for name, directory in source_dirs.items():
        notice_dir = stage / "licenses" / name
        notice_dir.mkdir()
        notices = [p for p in directory.iterdir() if p.is_file() and p.name.upper().startswith(("COPYING", "LICENSE", "AUTHORS"))]
        if not notices:
            raise ValueError(f"no upstream license found for {name}")
        for notice in notices:
            shutil.copy2(notice, notice_dir / notice.name)
        if not lock["sources"][name].get("prebuilt"):
            shutil.copy2(archives[name], stage / "sources" / archives[name].name)
    (stage / "licenses/NOTICE.txt").write_text(
        "Camstream Camera runtime pack\n\n"
        "FFmpeg is built with GPL libx264 and distributed under GPL-2.0-or-later.\n"
        "Exact FFmpeg/x264 sources and build recipes accompany this pack.\n"
        "FFmpeg is modified by the ordered, hash-pinned patches in the recipe for device identity and audio capture.\n"
        "Static libopus (BSD-3-Clause) source and upstream license accompany audio-capable packs.\n"
        "pkgconf (ISC) is a build-only dependency; its source accompanies the recipe.\n"
        "MediaMTX is an unmodified, hash-pinned official MIT-licensed release binary.\n"
        "Upstream origins, versions and hashes: recipe/macos/runtime.lock.json.\n"
        "Preserve these notices and accompanying sources when distributing the pack/app.\n")
    recipe = {k: v for k, v in lock.items() if k != "packs"}
    recipe["packs"] = {}
    recipe_dir = stage / "recipe/macos"
    (recipe_dir / "CameraProof").mkdir(parents=True)
    (recipe_dir / "runtime.lock.json").write_text(json.dumps(recipe, indent=2) + "\n")
    for name in ("build-runtime.py", "runtime_pack.py"):
        shutil.copy2(ROOT / "macos" / name, recipe_dir / name)
    shutil.copy2(ROOT / "macos/CameraProof/entitlements.plist", recipe_dir / "CameraProof/entitlements.plist")
    if "opus" in lock["sources"]:
        shutil.copy2(ROOT / "macos/runtime-entitlements.plist", recipe_dir / "runtime-entitlements.plist")
    for record, patch in zip(lock.get("patches", []), patches):
        destination = stage / "recipe" / record["file"]
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(patch, destination)
    if candidate_info:
        shutil.copy2(args.candidate / "BUILDINFO.json", stage / "recipe/proof-candidate-BUILDINFO.json")
    info = {"runtime_id": lock["runtime_id"], "target": lock["target"], "recipe_sha256": recipe_hash(lock),
            "versions": versions, "macho": macho, "capabilities": capabilities, "sources": lock["sources"], "commands": commands, "patches": lock.get("patches", []),
            "candidate_promotion": {"buildinfo_sha256": lock["proof_candidate"]["buildinfo_sha256"], "tools_before_signing": candidate_info["tools"]} if candidate_info else None,
            "build_environment": env, "toolchain": {"clang": capture(cc, "--version"),
            "sdk": capture("/usr/bin/xcrun", "--show-sdk-version"), "macos": platform.mac_ver()[0]},
            "signing": "ad-hoc hardened runtime; not Developer ID or notarized",
            "reproducibility": "locked inputs/recipe; not a bit-identical output guarantee across toolchains"}
    (stage / "BUILDINFO.json").write_text(json.dumps(info, indent=2) + "\n")
    for p in stage.rglob("*"):
        if p.is_file():
            p.chmod(0o755 if p.parent.name == "tools" else 0o644)
    manifest = {"schema": 1, "target": lock["target"], "recipe_sha256": recipe_hash(lock),
                "files": {p.relative_to(stage).as_posix(): {"sha256": sha(p), "mode": p.stat().st_mode & 0o777}
                          for p in sorted(stage.rglob("*")) if p.is_file()}}
    (stage / "MANIFEST.json").write_text(json.dumps(manifest, indent=2) + "\n")
    args.output.mkdir(parents=True, exist_ok=True)
    filename = f"{lock['runtime_id']}-darwin-arm64-macos{lock['target']['minimum_macos']}.tar.gz"
    destination = args.output / filename
    if destination.exists():
        raise ValueError("pack exists; use another output directory or runtime ID")
    # Stable tar ordering/metadata; compiler/SDK outputs are separately recorded.
    with destination.open("xb") as raw, gzip.GzipFile(filename="", fileobj=raw, mode="wb", mtime=0) as zipped:
        with tarfile.open(fileobj=zipped, mode="w") as tar:
            for p in sorted(stage.rglob("*")):
                meta = tar.gettarinfo(str(p), arcname=p.relative_to(stage).as_posix())
                meta.uid = meta.gid = meta.mtime = 0
                meta.uname = meta.gname = ""
                if p.is_file():
                    with p.open("rb") as data:
                        tar.addfile(meta, data)
                else:
                    tar.addfile(meta)
    record = {"file": filename, "sha256": sha(destination), "url": None}
    destination.with_suffix(destination.suffix + ".record.json").write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps(record, indent=2))
    print("Review this record into runtime.lock.json packs.darwin-arm64; no lock mutation or publication performed.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--lock", type=Path, default=DEFAULT_LOCK)
    parser.add_argument("--cache", type=Path, default=DEFAULT_CACHE)
    parser.add_argument("--offline", action="store_true")
    parser.add_argument("--candidate", type=Path, help="promote the exact proof candidate pinned by BUILDINFO digest; validates inputs and tool hashes")
    parser.add_argument("--work", type=Path, required=True)
    parser.add_argument("--output", type=Path, default=DEFAULT_CACHE / "packs")
    parser.add_argument("--jobs", type=int, default=min(8, os.cpu_count() or 1))
    options = parser.parse_args()
    if not 1 <= options.jobs <= 64:
        parser.error("jobs must be 1–64")
    try:
        build(options)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Runtime build failed: {error}\nInspect component logs in {options.work}.\n")

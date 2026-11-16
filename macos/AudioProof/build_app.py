#!/usr/bin/env python3
"""Assemble only the isolated T01 development app; never a production release."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'macos'))
from runtime_pack import inspect_macho


def build(candidate, app):
    info = json.loads((candidate / 'BUILDINFO.json').read_text())
    lock = json.loads((ROOT / 'macos/AudioProof/candidate.lock.json').read_text())
    if info['lock'] != lock:
        raise ValueError('candidate recipe differs from current proof lock')
    if app.exists() or app.suffix != '.app':
        raise ValueError('output must be a new .app path')
    contents = app / 'Contents'
    for name in ('MacOS', 'Helpers', 'Resources'):
        (contents / name).mkdir(parents=True)
    for name, record in info['tools'].items():
        source = candidate / 'tools' / name
        if hashlib.sha256(source.read_bytes()).hexdigest() != record['sha256']:
            raise ValueError('candidate binary digest mismatch')
        shutil.copy2(source, contents / 'Helpers' / ('ffmpeg-real' if name == 'ffmpeg' else name))
    for name in ('licenses', 'sources', 'recipe'):
        shutil.copytree(candidate / name, contents / 'Resources' / name)
    shutil.copy2(candidate / 'BUILDINFO.json', contents / 'Resources/candidate-BUILDINFO.json')
    env = {**os.environ, 'CGO_ENABLED': '0', 'GOOS': 'darwin', 'GOARCH': 'arm64', 'GOWORK': 'off', 'GOFLAGS': '', 'GOCACHE': str(app.parent / '.audio-proof-go-cache')}
    for name, package in [('camstream-camera', './cmd/camstream-camera'), ('ffmpeg', './macos/AudioProof/proxy')]:
        subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(contents / 'Helpers' / name), package], cwd=ROOT, env=env, check=True)
    subprocess.run(['xcrun', 'swiftc', '-swift-version', '5', '-target', 'arm64-apple-macosx26.0',
                    '-module-cache-path', str(app.parent / '.audio-proof-swift-cache'), '-framework', 'AppKit', '-framework', 'AVFoundation',
                    str(ROOT / 'macos/CameraProof/StartIntent.swift'), str(ROOT / 'macos/AudioProof/main.swift'),
                    '-o', str(contents / 'MacOS/AudioProof')], check=True)
    with (contents / 'Info.plist').open('wb') as out:
        plistlib.dump({'CFBundleExecutable': 'AudioProof', 'CFBundleIdentifier': 'com.goliatone.camstream.camera.audio-proof',
                      'CFBundleName': 'Camstream Audio Proof', 'CFBundleDisplayName': 'Camstream Audio Proof',
                      'CFBundlePackageType': 'APPL', 'CFBundleVersion': '0.1.0', 'CFBundleShortVersionString': '0.1.0',
                      'LSMinimumSystemVersion': '26.0', 'LSUIElement': False, 'NSHighResolutionCapable': True,
                      'NSCameraUsageDescription': 'Test optional microphone and camera streaming only after explicit Start.',
                      'NSMicrophoneUsageDescription': 'Test optional microphone streaming only after explicit Start.'}, out)
    for binary in [*sorted((contents / 'Helpers').iterdir()), contents / 'MacOS/AudioProof']:
        inspect_macho(binary, lock['target'])
        subprocess.run(['codesign', '--force', '--sign', '-', '--options', 'runtime', '--entitlements',
                        str(ROOT / 'macos/AudioProof/entitlements.plist'), str(binary)], check=True)
    subprocess.run(['codesign', '--force', '--sign', '-', '--options', 'runtime', '--entitlements',
                    str(ROOT / 'macos/AudioProof/entitlements.plist'), str(app)], check=True)
    subprocess.run(['codesign', '--verify', '--deep', '--strict', str(app)], check=True)
    print(app)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--candidate', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    build(args.candidate.resolve(), args.output.resolve())

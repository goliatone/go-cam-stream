#!/usr/bin/env python3
"""Build a portable diagnostic CLI folder using the approved camera runtime pack."""
import argparse
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / 'macos'))
from runtime_pack import DEFAULT_CACHE, DEFAULT_LOCK, inspect_macho, load_lock, selected_pack, sha, unpack_locked


def package(args):
    if platform.system() != 'Darwin' or platform.machine() != 'arm64':
        raise ValueError('build on Apple Silicon macOS with Go and Xcode tools')
    if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9._-]*', args.version):
        raise ValueError('invalid version')
    lock = load_lock(args.lock)
    archive = selected_pack(lock, args.cache, args.runtime_pack, offline=not args.download)
    name = f'camstream-camera-{args.version}-darwin-arm64'
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    if (out / name).exists() or (out / (name + '.tar.gz')).exists():
        raise ValueError('artifact exists; choose a new version or output directory')
    with tempfile.TemporaryDirectory(prefix='.camera-package-', dir=out) as temp:
        stage = Path(temp)
        runtime = stage / 'runtime'
        unpack_locked(archive, runtime, lock)
        folder = stage / name
        shutil.copytree(runtime, folder)
        (folder / 'MANIFEST.json').rename(folder / 'RUNTIME_MANIFEST.json')
        (folder / 'BUILDINFO.json').rename(folder / 'RUNTIME_BUILDINFO.json')
        env = dict(os.environ, CGO_ENABLED='0', GOOS='darwin', GOARCH='arm64', GOWORK='off', GOFLAGS='')
        subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w', '-o',
                        str(folder / 'camstream-camera'), './cmd/camstream-camera'], cwd=ROOT, env=env, check=True)
        inspect_macho(folder / 'camstream-camera', lock['target'])
        shutil.copy2(ROOT / 'LICENSE', folder / 'licenses/camstream.LICENSE')
        shutil.copy2(ROOT / 'docs/GUIDE_MAC_CAMERA.md', folder / 'README.md')
        info = {'version': args.version, 'target': lock['target'], 'runtime_pack_sha256': sha(archive),
                'runtime_id': lock['runtime_id'], 'signing': 'local ad-hoc; not Developer ID signed or notarized'}
        (folder / 'BUILDINFO.json').write_text(json.dumps(info, indent=2) + '\n')
        (folder / 'SHA256SUMS').write_text(''.join(f'{sha(p)}  {p.relative_to(folder)}\n'
            for p in sorted(folder.rglob('*')) if p.is_file()))
        with tarfile.open(stage / (name + '.tar.gz'), 'w:gz') as tar:
            tar.add(folder, arcname=name)
        (stage / (name + '.tar.gz')).rename(out / (name + '.tar.gz'))
        folder.rename(out / name)
    (out / (name + '.tar.gz.sha256')).write_text(f"{sha(out / (name + '.tar.gz'))}  {name}.tar.gz\n")
    print(out / (name + '.tar.gz'))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version')
    parser.add_argument('--runtime-pack', type=Path)
    parser.add_argument('--lock', type=Path, default=DEFAULT_LOCK)
    parser.add_argument('--cache', type=Path, default=DEFAULT_CACHE)
    parser.add_argument('--download', action='store_true')
    parser.add_argument('--output', type=Path, default=ROOT / 'dist')
    try:
        package(parser.parse_args())
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f'Camera packaging failed: {error}\n')

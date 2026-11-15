#!/usr/bin/env python3
"""Build a versioned camera DMG. Development is explicit; distribution fails closed."""
import argparse
import json
from pathlib import Path
import platform
import plistlib
import runpy
import shutil
import subprocess
import tempfile

from runtime_pack import DEFAULT_CACHE, DEFAULT_LOCK, ROOT, load_lock, selected_pack, sha, unpack_locked

builder = runpy.run_path(str(Path(__file__).with_name('build-proof.py')))


def run(*args, **kwargs):
    return subprocess.run([str(a) for a in args], check=True, **kwargs)


def source_state():
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=normal'], cwd=ROOT, text=True).strip())
    return {'commit': commit, 'dirty': dirty}


def options(version, development, identity, profile):
    builder['validate_version'](version)
    if development:
        if identity or profile:
            raise ValueError('development mode cannot use signing/notarization credentials')
    elif not identity or not identity.startswith('Developer ID Application: ') or not profile:
        raise ValueError('distribution requires --sign-identity and --notary-profile; use --development for local checks')


def notarize(artifact, profile, result_path, keychain=None):
    keychain_args = ['--keychain', keychain] if keychain else []
    result = run('xcrun', 'notarytool', 'submit', artifact, '--keychain-profile', profile, *keychain_args,
                 '--wait', '--timeout', '30m', '--output-format', 'json', capture_output=True, text=True)
    report = json.loads(result.stdout)
    result_path.write_text(json.dumps(report, indent=2) + '\n')
    if report.get('status') != 'Accepted':
        raise ValueError('Apple notarization did not return Accepted; no release output produced')
    return report['id']


def check_signature(app, distribution):
    run('codesign', '--verify', '--deep', '--strict', app)
    if distribution:
        # Verify every nested executable's identity, hardened runtime and timestamp.
        teams = set()
        for path in [app, *sorted((app / 'Contents/Helpers').iterdir())]:
            result = run('codesign', '--display', '--verbose=4', path, capture_output=True, text=True)
            details = result.stdout + result.stderr
            if 'Authority=Developer ID Application:' not in details or 'Timestamp=' not in details or 'runtime' not in details:
                raise ValueError('distribution executable lacks Developer ID, hardened runtime or timestamp')
            teams.update(line for line in details.splitlines() if line.startswith('TeamIdentifier='))
        if len(teams) != 1 or 'TeamIdentifier=not set' in teams:
            raise ValueError('distribution executables must share one Developer ID team')


def inventory(app):
    files = {}
    for path in sorted(app.rglob('*')):
        if path.is_symlink():
            raise ValueError('unexpected symlink inside the self-contained app')
        if path.is_file():
            files[str(path.relative_to(app))] = {'sha256': sha(path), 'mode': path.stat().st_mode & 0o777}
    return files


def verify(directory, require_notarized=False):
    directory = Path(directory)
    report = json.loads((directory / 'release.json').read_text())
    if report.get('schema') != 1 or report.get('distribution') not in ('development', 'developer-id-notarized'):
        raise ValueError('unsupported release metadata')
    builder['validate_version'](report['version'])
    if require_notarized and report['distribution'] != 'developer-id-notarized':
        raise ValueError('development artifacts are not distribution releases')
    app = directory / 'Camstream Camera.app'
    info = plistlib.loads((app / 'Contents/Info.plist').read_bytes())
    if info['CFBundleShortVersionString'] != report['version'] or info['CFBundleVersion'] != report['version']:
        raise ValueError('bundle/release version mismatch')
    if info['CFBundleIdentifier'] != 'com.goliatone.camstream.camera':
        raise ValueError('unexpected app identity')
    build = json.loads((app / 'Contents/Resources/BUILDINFO.json').read_text())
    if any(build[key] != report[key] for key in ('version', 'target', 'runtime_id', 'runtime_pack_sha256')):
        raise ValueError('bundle/release runtime metadata mismatch')
    if inventory(app) != report['app_files']:
        raise ValueError('app file hash/mode inventory mismatch')
    dmg_name = report['dmg']['file']
    if Path(dmg_name).name != dmg_name or not dmg_name.endswith('.dmg'):
        raise ValueError('invalid DMG filename')
    if sha(directory / dmg_name) != report['dmg']['sha256']:
        raise ValueError('DMG hash mismatch')
    expected = f"{sha(directory / dmg_name)}  {dmg_name}\n{sha(directory / 'release.json')}  release.json\n"
    if (directory / 'SHA256SUMS').read_text() != expected:
        raise ValueError('release checksums mismatch')
    signed = report['distribution'] == 'developer-id-notarized'
    check_signature(app, signed)
    run('hdiutil', 'verify', directory / dmg_name)
    if signed:
        for path in (app, directory / dmg_name):
            run('xcrun', 'stapler', 'validate', path)
        run('spctl', '--assess', '--type', 'execute', '--verbose=2', app)
        run('spctl', '--assess', '--type', 'open', '--context', 'context:primary-signature', '--verbose=2', directory / dmg_name)
    return report


def package(args):
    options(args.version, args.development, args.sign_identity, args.notary_profile)
    if platform.system() != 'Darwin' or platform.machine() != 'arm64':
        raise ValueError('build on Apple Silicon macOS')
    source = source_state()
    if not args.development and source['dirty']:
        raise ValueError('distribution builds require a clean checkout; use --development for local changes')
    lock = load_lock(args.lock)
    archive = selected_pack(lock, args.cache, args.runtime_pack, offline=not args.download)
    suffix = '-development' if args.development else ''
    name = f'camstream-camera-{args.version}-darwin-arm64{suffix}'
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    destination = out / name
    if destination.exists():
        raise ValueError('release output already exists; use a new output directory or version')
    # No partially built or rejected release is promoted into the output directory.
    with tempfile.TemporaryDirectory(prefix='.camera-release-', dir=out) as temp:
        stage = Path(temp)
        runtime = stage / 'runtime'
        unpack_locked(archive, runtime, lock)
        result = stage / name
        result.mkdir()
        app = result / 'Camstream Camera.app'
        builder['assemble'](runtime, app, lock, production=True, version=args.version,
                            identity='-' if args.development else args.sign_identity)
        check_signature(app, not args.development)
        tickets = {}
        if not args.development:
            zipped = stage / 'notary-app.zip'
            run('ditto', '-c', '-k', '--keepParent', app, zipped)
            tickets['app'] = notarize(zipped, args.notary_profile, result / 'notary-app.json', args.notary_keychain)
            run('xcrun', 'stapler', 'staple', app)
            run('xcrun', 'stapler', 'validate', app)
        payload = stage / 'image'
        payload.mkdir()
        run('ditto', app, payload / app.name)
        (payload / 'Applications').symlink_to('/Applications')
        instructions = 'Camstream Camera ' + args.version + '\n\nCopy Camstream Camera to Applications. Eject this disk, then open the app from Applications.\nAllow camera access, save settings, then choose Start Camera. Closing the window keeps capture running; Quit stops it.\nTo update or roll back: Quit, replace the app, then reopen. Settings and credentials are preserved.\nRequires Apple Silicon and macOS ' + lock['target']['minimum_macos'] + ' or later.\n'
        if args.development:
            instructions += '\nDEVELOPMENT BUILD: ad-hoc signed, not notarized or approved for external distribution.\n'
        (payload / 'Install.txt').write_text(instructions)
        dmg = result / (name + '.dmg')
        run('hdiutil', 'create', '-volname', 'Camstream Camera ' + args.version,
            '-srcfolder', payload, '-format', 'UDZO', dmg)
        if not args.development:
            run('codesign', '--force', '--sign', args.sign_identity, '--timestamp', dmg)
            tickets['dmg'] = notarize(dmg, args.notary_profile, result / 'notary-dmg.json', args.notary_keychain)
            run('xcrun', 'stapler', 'staple', dmg)
        report = {'schema': 1, 'version': args.version, 'source': source, 'target': lock['target'],
                  'runtime_id': lock['runtime_id'], 'runtime_pack_sha256': sha(archive),
                  'distribution': 'development' if args.development else 'developer-id-notarized',
                  'notarization_ids': tickets, 'app_files': inventory(app),
                  'dmg': {'file': dmg.name, 'sha256': sha(dmg)}}
        (result / 'release.json').write_text(json.dumps(report, indent=2, sort_keys=True) + '\n')
        (result / 'SHA256SUMS').write_text(f"{sha(dmg)}  {dmg.name}\n{sha(result / 'release.json')}  release.json\n")
        verify(result, require_notarized=not args.development)
        result.rename(destination)
    print(destination)
    return destination


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version', nargs='?')
    parser.add_argument('--development', action='store_true')
    parser.add_argument('--sign-identity')
    parser.add_argument('--notary-profile', help='existing notarytool Keychain profile; never a password')
    parser.add_argument('--notary-keychain', type=Path, help='optional Keychain containing the profile (CI uses a temporary Keychain)')
    parser.add_argument('--runtime-pack', type=Path)
    parser.add_argument('--lock', type=Path, default=DEFAULT_LOCK)
    parser.add_argument('--cache', type=Path, default=DEFAULT_CACHE)
    parser.add_argument('--download', action='store_true')
    parser.add_argument('--output', type=Path, default=ROOT / 'dist/releases')
    parser.add_argument('--verify', type=Path, help='verify an existing release directory instead of building')
    parser.add_argument('--require-notarized', action='store_true', help='with --verify, reject development artifacts')
    args = parser.parse_args()
    try:
        if args.verify:
            verify(args.verify, args.require_notarized)
            print('Release verification passed')
        elif args.version:
            if args.require_notarized:
                parser.error('--require-notarized is only valid with --verify')
            package(args)
        else:
            parser.error('a version or --verify is required')
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError, json.JSONDecodeError) as error:
        parser.exit(1, f'Camera release failed: {error}\n')


if __name__ == '__main__':
    main()

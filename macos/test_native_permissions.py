"""Check T05 production permission wiring without building or launching an app."""
import json
from pathlib import Path
import plistlib
import runpy
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from conversation_build import BUILD_ID_KEY, page_build_id

builder = runpy.run_path(str(Path(__file__).with_name('build-proof.py')))


def actool_outputs(args, files=('Assets.car', 'AppIcon.icns'),
                   keys=(('CFBundleIconFile', 'AppIcon'), ('CFBundleIconName', 'AppIcon'))):
    """Write what actool writes for the app icon: compiled files and the partial Info.plist."""
    resources = Path(args[args.index('--compile') + 1])
    for name in files:
        (resources / name).write_bytes(b'fixture')
    Path(args[args.index('--output-partial-info-plist') + 1]).write_bytes(plistlib.dumps(dict(keys)))


class NativePermissionTests(unittest.TestCase):
    def test_icon_compilation_fails_closed(self):
        compile_app_icon = builder['compile_app_icon']
        def failed(*args, **kwargs):
            raise subprocess.CalledProcessError(72, args, output='', stderr='xcrun: error: unable to find utility "actool"')
        cases = {'missing actool': failed,
                 'no icns fallback': lambda *args, **kwargs: actool_outputs(args, files=('Assets.car',)),
                 'no icon keys': lambda *args, **kwargs: actool_outputs(args, keys=())}
        for name, run in cases.items():
            with self.subTest(name), tempfile.TemporaryDirectory() as temp, \
                    patch.dict(compile_app_icon.__globals__, run=run):
                with self.assertRaises(ValueError) as raised:
                    compile_app_icon(Path(temp), '26.0')
                if name == 'missing actool':
                    self.assertIn('unable to find utility "actool"', str(raised.exception))

    def test_assembly_selects_production_permissions_without_changing_proof(self):
        for production in (False, True):
            with self.subTest(production=production), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                runtime = root / 'runtime'
                (runtime / 'tools').mkdir(parents=True)
                for name in ('ffmpeg', 'ffprobe', 'mediamtx'):
                    (runtime / 'tools' / name).write_bytes(b'fixture')
                for name in ('licenses', 'sources', 'recipe'):
                    (runtime / name).mkdir()
                for name in ('BUILDINFO.json', 'MANIFEST.json'):
                    (runtime / name).write_text(json.dumps({'fixture': True}))
                lock = {'target': {'minimum_macos': '26.0'}, 'runtime_id': 'fixture',
                        'packs': {'darwin-arm64': {'sha256': 'fixture'}}, 'sources': {'opus': {}},
                        'video_device_identity': True}
                app = root / 'Fixture.app'
                signed = []
                icon_builds = []
                inventories = []
                assemble = builder['assemble']
                def run_fixture(*args, **kwargs):
                    if args[-1] == 'devices':
                        inventories.append(args[0])
                        return subprocess.CompletedProcess(args, 0, b'[]')
                    if args[:2] == ('xcrun', 'actool'):
                        icon_builds.append(args)
                        actool_outputs(args)
                    if args[:3] == ('npm', 'run', 'build:share'):
                        guest = Path(kwargs['env']['CAMSTREAM_SHARE_OUT_DIR'])
                        guest.mkdir()
                        (guest / 'index.html').write_text('fixture guest entry')
                    if args[:3] == ('npm', 'run', 'build:conversation'):
                        guest = Path(kwargs['env']['CAMSTREAM_CONVERSATION_OUT_DIR'])
                        guest.mkdir()
                        (guest / 'index.html').write_text('<head></head><body>fixture conversation entry</body>')

                # Fixture filesystem follows the real assembler; tool execution is disabled.
                with patch.dict(assemble.__globals__, run=run_fixture,
                                inspect_macho=lambda *args: None,
                                sign=lambda path, identity, entitlements: signed.append((path, entitlements))):
                    build_id = assemble(runtime, app, lock, production=production)
                info = plistlib.loads((app / 'Contents/Info.plist').read_bytes())
                self.assertIn('NSCameraUsageDescription', info)
                self.assertEqual(inventories, [str(app / 'Contents/Helpers/camstream-camera')] if production else [])
                self.assertEqual('NSMicrophoneUsageDescription' in info, production)
                schemes = [scheme for entry in info.get('CFBundleURLTypes', []) for scheme in entry['CFBundleURLSchemes']]
                self.assertEqual(schemes, ['camstream'] if production else [])
                self.assertEqual((app / 'Contents/Resources/Sharing/index.html').is_file(), production)
                conversation = app / 'Contents/Resources/Conversations/index.html'
                self.assertEqual(conversation.is_file(), production)
                if production:
                    self.assertRegex(build_id, r'^[0-9a-f]{32}$')
                    self.assertEqual(info[BUILD_ID_KEY], build_id)
                    self.assertEqual(page_build_id(conversation.read_bytes()), build_id)
                    self.assertEqual(len(icon_builds), 1)
                    args = icon_builds[0]
                    self.assertTrue(args[2].endswith('macos/CameraApp/AppIcon.icon'))
                    for flag, value in (('--compile', str(app / 'Contents/Resources')), ('--platform', 'macosx'),
                                        ('--minimum-deployment-target', '26.0'), ('--app-icon', 'AppIcon')):
                        self.assertEqual(args[args.index(flag) + 1], value)
                    self.assertEqual((info['CFBundleIconFile'], info['CFBundleIconName']), ('AppIcon', 'AppIcon'))
                    for name in ('Assets.car', 'AppIcon.icns'):
                        self.assertTrue((app / 'Contents/Resources' / name).is_file())
                else:
                    self.assertNotIn(BUILD_ID_KEY, info)
                    self.assertEqual(icon_builds, [])
                    self.assertNotIn('CFBundleIconFile', info)
                    self.assertNotIn('CFBundleIconName', info)
                self.assertTrue(signed)
                for _, entitlement in signed:
                    values = plistlib.loads(Path(entitlement).read_bytes())
                    self.assertTrue(values['com.apple.security.device.camera'])
                    self.assertEqual(values.get('com.apple.security.device.audio-input', False), production)

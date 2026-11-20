"""Check T05 production permission wiring without building or launching an app."""
import json
from pathlib import Path
import plistlib
import runpy
import tempfile
import unittest
from unittest.mock import patch

from conversation_build import BUILD_ID_KEY, page_build_id

builder = runpy.run_path(str(Path(__file__).with_name('build-proof.py')))


class NativePermissionTests(unittest.TestCase):
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
                        'packs': {'darwin-arm64': {'sha256': 'fixture'}}, 'sources': {'opus': {}}}
                app = root / 'Fixture.app'
                signed = []
                assemble = builder['assemble']
                def run_fixture(*args, **kwargs):
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
                else:
                    self.assertNotIn(BUILD_ID_KEY, info)
                self.assertTrue(signed)
                for _, entitlement in signed:
                    values = plistlib.loads(Path(entitlement).read_bytes())
                    self.assertTrue(values['com.apple.security.device.camera'])
                    self.assertEqual(values.get('com.apple.security.device.audio-input', False), production)

"""Release admission and post-signing artifact integrity; no network or camera."""
import importlib.util
import json
from pathlib import Path
import plistlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('camera_release', Path(__file__).with_name('package-release.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseTests(unittest.TestCase):
    def test_version_is_safe_and_apple_compatible(self):
        for value in ('0.1.0', '1.23.45', '9999.99.99'):
            self.assertEqual(release.builder['validate_version'](value), value)
        for value in ('', 'v1.2.3', '../bad', '1.2.3;echo bad', '1.2.3-rc.1', '01.2.3', '1.200.3'):
            with self.assertRaises(ValueError):
                release.builder['validate_version'](value)

    def test_distribution_requires_both_credentials(self):
        for identity, profile in ((None, None), ('-', 'profile'), ('Apple Development: Test', 'profile'),
                                  ('Developer ID Application: Test', None)):
            with self.assertRaises(ValueError):
                release.options('1.2.3', False, identity, profile)
        release.options('1.2.3', False, 'Developer ID Application: Test', 'profile')

    def test_development_never_silently_uses_credentials(self):
        release.options('1.2.3', True, None, None)
        with self.assertRaises(ValueError):
            release.options('1.2.3', True, 'Developer ID Application: Test', 'profile')

    def test_notary_rejection_is_not_success(self):
        with tempfile.TemporaryDirectory() as temp:
            for status in ('Invalid', 'In Progress', 'Rejected'):
                result = subprocess.CompletedProcess([], 0, json.dumps({'status': status, 'id': 'test'}))
                with patch.object(release, 'run', return_value=result), self.assertRaises(ValueError):
                    release.notarize(Path(temp) / 'app.zip', 'profile', Path(temp) / 'result.json')
            result = subprocess.CompletedProcess([], 0, json.dumps({'status': 'Accepted', 'id': 'test'}))
            with patch.object(release, 'run', return_value=result):
                self.assertEqual(release.notarize(Path(temp) / 'app.zip', 'profile', Path(temp) / 'result.json'), 'test')

    def test_signing_uses_timestamps_only_for_developer_id(self):
        calls = []
        sign = release.builder['sign']
        with patch.dict(sign.__globals__, run=lambda *args: calls.append(args)):
            sign(Path('app'), '-', Path('camera.plist'))
            sign(Path('app'), 'Developer ID Application: Test', Path('camera.plist'))
        self.assertNotIn('--timestamp', calls[0])
        self.assertIn('--timestamp', calls[1])
        self.assertIn('runtime', calls[1])

    def fixture(self, root):
        app = root / 'Camstream Camera.app'
        (app / 'Contents/Resources').mkdir(parents=True)
        info = {'CFBundleIdentifier': 'com.goliatone.camstream.camera',
                'CFBundleVersion': '1.2.3', 'CFBundleShortVersionString': '1.2.3'}
        (app / 'Contents/Info.plist').write_bytes(plistlib.dumps(info))
        meta = {'version': '1.2.3', 'target': {'arch': 'arm64'}, 'runtime_id': 'locked', 'runtime_pack_sha256': 'a' * 64}
        (app / 'Contents/Resources/BUILDINFO.json').write_text(json.dumps(meta))
        dmg = root / 'test.dmg'
        dmg.write_bytes(b'fake DMG; platform checks are mocked')
        report = dict(meta, schema=1, distribution='development', app_files=release.inventory(app),
                      dmg={'file': dmg.name, 'sha256': release.sha(dmg)})
        (root / 'release.json').write_text(json.dumps(report))
        self.checksums(root)
        return app

    def checksums(self, root):
        (root / 'SHA256SUMS').write_text(f"{release.sha(root / 'test.dmg')}  test.dmg\n{release.sha(root / 'release.json')}  release.json\n")

    def test_integrity_and_no_unsigned_promotion(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(release, 'run'), patch.object(release, 'check_signature'):
            root = Path(temp)
            app = self.fixture(root)
            release.verify(root)
            with self.assertRaises(ValueError):
                release.verify(root, require_notarized=True)
            (app / 'Contents/unexpected').write_text('injected')
            with self.assertRaises(ValueError):
                release.verify(root)

    def test_dmg_tampering_rejected(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(release, 'run'), patch.object(release, 'check_signature'):
            root = Path(temp)
            self.fixture(root)
            (root / 'test.dmg').write_bytes(b'changed')
            with self.assertRaises(ValueError):
                release.verify(root)

    def test_metadata_version_mismatch_rejected(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(release, 'run'), patch.object(release, 'check_signature'):
            root = Path(temp)
            self.fixture(root)
            report = json.loads((root / 'release.json').read_text())
            report['version'] = '1.2.4'
            (root / 'release.json').write_text(json.dumps(report))
            self.checksums(root)
            with self.assertRaises(ValueError):
                release.verify(root)

    def test_inventory_rejects_links_to_external_runtime(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'ffmpeg').symlink_to('/opt/homebrew/bin/ffmpeg')
            with self.assertRaises(ValueError):
                release.inventory(root)


if __name__ == '__main__':
    unittest.main()

import json
from pathlib import Path
import plistlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from conversation_build import BUILD_ID_KEY, stamp_conversation_page, verify_browser, verify_running_app


class ConversationBuildTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.app = Path(temp.name) / "Camstream Camera.app"
        self.assets = self.app / "Contents/Resources/Conversations"
        self.assets.mkdir(parents=True)
        self.expected = "a" * 32
        self.html = self.assets / "index.html"
        self.html.write_text('<head><script src="/conversation-assets/app-test.js"></script>'
                             '<link rel="stylesheet" href="/conversation-assets/app-test.css"></head>')
        stamp_conversation_page(self.html, self.expected)
        (self.assets / "app-test.js").write_bytes(b"javascript")
        (self.assets / "app-test.css").write_bytes(b"css")

    def test_verify_matching_page_and_reject_stale_asset(self):
        for javascript, failure in ((b"javascript", False), (b"stale code", True)):
            with self.subTest(failure=failure), patch("conversation_build.fetch", side_effect=[
                self.html.read_bytes(), b"css", javascript,
            ]) as fetch:
                if failure:
                    with self.assertRaisesRegex(ValueError, "asset differs.*app-test.js"):
                        verify_browser(self.app, self.expected, "http://127.0.0.1:8080")
                else:
                    verify_browser(self.app, self.expected, "http://127.0.0.1:8080")
                self.assertRegex(fetch.call_args_list[0].args[1], r"^/r/[A-Za-z0-9_-]{43}$")
                self.assertEqual([call.args[1] for call in fetch.call_args_list[1:]], [
                    "/conversation-assets/app-test.css", "/conversation-assets/app-test.js",
                ])

    def test_reject_old_browser_page_before_fetching_assets(self):
        with patch("conversation_build.fetch", return_value=b"<head></head>") as fetch:
            with self.assertRaisesRegex(ValueError, "Browser page has build ID missing"):
                verify_browser(self.app, self.expected, "http://127.0.0.1:8080")
            fetch.assert_called_once()

    def test_compare_actual_running_bundle_id(self):
        result = subprocess.CompletedProcess([], 0, stdout=json.dumps([str(self.app)]))
        with patch("conversation_build.subprocess.run", return_value=result):
            info = self.app / "Contents/Info.plist"
            info.write_bytes(plistlib.dumps({BUILD_ID_KEY: self.expected}))
            self.assertEqual(verify_running_app(self.expected), self.app)
            with self.assertRaisesRegex(ValueError, "Running Mac app has build ID"):
                verify_running_app("b" * 32)

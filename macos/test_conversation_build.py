import json
from pathlib import Path
import plistlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from conversation_build import BUILD_ID_KEY, stamp_conversation_page, verify_browser, verify_running_app


SCRIPT_POLICY = "default-src 'none'; script-src 'self'; style-src 'self'"
BEACON = (
    b'<script type="module" src="https://static.cloudflareinsights.com/beacon.min.js/v0123456789abcdef"'
    b' integrity="sha512-' + b'A' * 86 + b'==" data-cf-beacon=\'{"version":"2024.11.0","token":"fixture"}\''
    b' crossorigin="anonymous"></script>\n'
)


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
                             '<link rel="stylesheet" href="/conversation-assets/app-test.css"></head>'
                             '<body><main>Conversation</main>\n</body>')
        stamp_conversation_page(self.html, self.expected)
        (self.assets / "app-test.js").write_bytes(b"javascript")
        (self.assets / "app-test.css").write_bytes(b"css")

    def test_verify_matching_page_and_reject_stale_asset(self):
        for javascript, failure in ((b"javascript", False), (b"stale code", True)):
            with self.subTest(failure=failure), patch("conversation_build.fetch", side_effect=[
                (self.html.read_bytes(), ""), (b"css", ""), (javascript, ""),
            ]) as fetch:
                if failure:
                    with self.assertRaisesRegex(ValueError, "asset differs.*app-test.js"):
                        verify_browser(self.app, self.expected, "http://127.0.0.1:8080")
                else:
                    self.assertFalse(verify_browser(self.app, self.expected, "http://127.0.0.1:8080"))
                self.assertRegex(fetch.call_args_list[0].args[1], r"^/r/[A-Za-z0-9_-]{43}$")
                self.assertEqual([call.args[1] for call in fetch.call_args_list[1:]], [
                    "/conversation-assets/app-test.css", "/conversation-assets/app-test.js",
                ])

    def test_reject_old_browser_page_before_fetching_assets(self):
        with patch("conversation_build.fetch", return_value=(b"<head></head>", "")) as fetch:
            with self.assertRaisesRegex(ValueError, "Browser page has build ID missing"):
                verify_browser(self.app, self.expected, "http://127.0.0.1:8080")
            fetch.assert_called_once()

    def test_allows_one_blocked_beacon_and_still_compares_the_assets(self):
        html = self.html.read_bytes().replace(b"</body>", BEACON + b"</body>")
        with patch("conversation_build.fetch", side_effect=[(html, SCRIPT_POLICY), (b"css", ""), (b"javascript", "")]):
            self.assertTrue(verify_browser(self.app, self.expected, "https://calls.example.com"))
        with patch("conversation_build.fetch", side_effect=[(html, SCRIPT_POLICY), (b"stale css", "")]):
            with self.assertRaisesRegex(ValueError, "asset differs.*app-test.css"):
                verify_browser(self.app, self.expected, "https://calls.example.com")

    def test_rejects_other_changes_even_with_a_recognized_beacon(self):
        html = self.html.read_bytes()
        served = html.replace(b"</body>", BEACON + b"</body>")
        cases = {
            "wrong build ID": (served.replace(b'a' * 32, b'b' * 32), SCRIPT_POLICY, "Browser page has build ID"),
            "changed markup": (served.replace(b"<main>Conversation", b"<main>Old call"), SCRIPT_POLICY, "HTML differs"),
            "different local asset reference": (served.replace(b"app-test.js", b"app-old.js"), SCRIPT_POLICY, "HTML differs"),
            "extra script": (served.replace(BEACON, b"<script>alert(0)</script>\n" + BEACON), SCRIPT_POLICY, "HTML differs"),
            "duplicate beacon": (served.replace(BEACON, BEACON * 2), SCRIPT_POLICY, "HTML differs"),
            "different script origin": (served.replace(b"static.cloudflareinsights.com", b"example.com"), SCRIPT_POLICY, "HTML differs"),
            "missing policy": (served, "", "HTML differs"),
            "permissive script policy": (served, "script-src 'self' https://static.cloudflareinsights.com", "HTML differs"),
            "script element override": (served, SCRIPT_POLICY + "; script-src-elem https:", "HTML differs"),
        }
        for name, (document, policy, error) in cases.items():
            with self.subTest(name=name), patch("conversation_build.fetch", return_value=(document, policy)) as fetch:
                with self.assertRaisesRegex(ValueError, error):
                    verify_browser(self.app, self.expected, "https://calls.example.com")
                fetch.assert_called_once()

    def test_compare_actual_running_bundle_id(self):
        result = subprocess.CompletedProcess([], 0, stdout=json.dumps([str(self.app)]))
        with patch("conversation_build.subprocess.run", return_value=result):
            info = self.app / "Contents/Info.plist"
            info.write_bytes(plistlib.dumps({BUILD_ID_KEY: self.expected}))
            self.assertEqual(verify_running_app(self.expected), self.app)
            with self.assertRaisesRegex(ValueError, "Running Mac app has build ID"):
                verify_running_app("b" * 32)

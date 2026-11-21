"""Check the committed Optic icon set against its generator without rendering anything."""
import json
import re
import struct
import unittest
import xml.etree.ElementTree as ET

import app_icon

SVG = "{http://www.w3.org/2000/svg}"


class AppIconTests(unittest.TestCase):
    def test_committed_files_match_the_generator(self):
        for path, text in app_icon.text_outputs().items():
            with self.subTest(path=str(path.relative_to(app_icon.ROOT))):
                self.assertTrue(path.is_file(), "missing; run python3 macos/app_icon.py")
                self.assertEqual(path.read_text(), text, "out of date; run python3 macos/app_icon.py")

    def test_generated_svgs_parse(self):
        for path, text in app_icon.text_outputs().items():
            if path.suffix == ".svg":
                with self.subTest(path=path.name):
                    self.assertEqual(ET.fromstring(text).tag, SVG + "svg")

    def test_icon_document_names_committed_layers(self):
        document = json.loads((app_icon.ICON / "icon.json").read_text())
        self.assertEqual(document["supported-platforms"], {"squares": ["macOS"]})
        self.assertEqual(len(document["fill"]["linear-gradient"]), 2)
        images = [layer["image-name"] for group in document["groups"] for layer in group["layers"]]
        self.assertEqual(sorted(images), ["lens.png", "light.svg"])
        for image in images:
            self.assertTrue((app_icon.ICON / "Assets" / image).is_file(), image)

    def test_lens_layer_fills_the_icon_canvas(self):
        data = (app_icon.ICON / "Assets/lens.png").read_bytes()
        self.assertEqual(data[:8], b"\x89PNG\r\n\x1a\n")
        self.assertEqual(data[12:16], b"IHDR")
        self.assertEqual(struct.unpack(">II", data[16:24]), (1024, 1024))

    def test_favicons_follow_the_badge_contract(self):
        for tone, colour in app_icon.BADGES.items():
            with self.subTest(tone=tone):
                circles = ET.fromstring(app_icon.favicon(tone)).iter(SVG + "circle")
                badges = [c for c in circles if c.get("fill") == colour]
                self.assertEqual(len(badges), 1)
                self.assertEqual({k: badges[0].get(k) for k in ("cx", "cy", "r", "stroke", "stroke-width")},
                                 {"cx": "25", "cy": "8", "r": "6", "stroke": "#fff", "stroke-width": "2"})
        idle = app_icon.favicon()
        for colour in app_icon.BADGES.values():
            self.assertNotIn(colour, idle)

    def test_tile_outline_is_closed_and_touches_each_edge(self):
        for size, radius in ((1024, app_icon.TILE_RADIUS), (32, 7.2)):
            with self.subTest(size=size):
                path = app_icon.squircle(size, radius)
                self.assertTrue(path.startswith("M") and path.endswith("Z"))
                # One straight edge starts on each side; the corners are relative curves between them.
                points = [(float(x), float(y)) for x, y in re.findall(r"[ML](-?[\d.]+) (-?[\d.]+)", path)]
                self.assertEqual(len(points), 4)
                for axis in (0, 1):
                    values = [point[axis] for point in points]
                    self.assertEqual((min(values), max(values)), (0, size))


if __name__ == "__main__":
    unittest.main()

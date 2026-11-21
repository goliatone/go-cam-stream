#!/usr/bin/env python3
"""Generate Camstream's two icons: the Mac app's and the one its web pages show.

The Mac app uses direction B "Optic", a camera lens with the camera-in-use light. The camera
dashboard and the shared-camera guest page use direction C "Continuity", the camcorder of the
original dashboard favicon with its white lens.

  macos/CameraApp/AppIcon.icon/icon.json          Optic fill and layer groups
  macos/CameraApp/AppIcon.icon/Assets/lens.png    Optic lens layer; raster because its reflections use blur filters
  macos/CameraApp/AppIcon.icon/Assets/light.svg   Optic camera-in-use light layer
  frontend/src/components/app-icon.svg            Continuity on a continuous-corner tile (dashboard header)
  frontend/src/components/favicon-*.svg           simplified Continuity for browser tabs, with status badges
  frontend/share/favicon.svg                      the idle tab icon again, for the guest page

The app build compiles the Icon Composer document with actool. Generated files are committed, so
only regenerating needs rsvg-convert (brew install librsvg). --preview DIR renders the six macOS
appearances with Icon Composer's ictool for review; --check reports committed text files that
differ from these definitions. The dashboard HTML and application code use the same idle SVG.
"""
import argparse
import json
import math
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ICON = ROOT / "macos/CameraApp/AppIcon.icon"
WEB = ROOT / "frontend/src/components"
GUEST_FAVICON = ROOT / "frontend/share/favicon.svg"  # shared-camera guest page
APPEARANCES = ("Default", "Dark", "ClearLight", "ClearDark", "TintedLight", "TintedDark")

# The artwork is drawn on Apple's 1024 pt canvas around the classic 824 pt icon body. Icon
# Composer layers span the whole tile and the system applies the mask, so layers scale about
# the centre by 1024/824.
CENTRE = 512
LAYER_SCALE = 1024 / 824
TILE_RADIUS = 185.4 * LAYER_SCALE  # continuous-corner radius of the full-bleed tile

FIELD = ("#2f5a4d", "#0b1915")  # Optic's emerald field, top to bottom
CAMCORDER_FIELD = ("#183c32", "#06120e")  # Continuity's deeper green field, top to bottom
BADGES = {"live": "#1e9e5a", "busy": "#d4900f", "danger": "#d23f3f"}  # dashboard tab states
SVG_NS = 'xmlns="http://www.w3.org/2000/svg"'


def num(value):
    text = f"{value:.2f}".rstrip("0").rstrip(".")
    return "0" if text in ("", "-0") else text


def point(radius, degrees, centre=CENTRE):
    angle = math.radians(degrees)
    return centre + radius * math.cos(angle), centre + radius * math.sin(angle)


def arc(radius, start, end, centre=CENTRE):
    x0, y0 = point(radius, start, centre)
    x1, y1 = point(radius, end, centre)
    large = 1 if (end - start) % 360 > 180 else 0
    return f"M{num(x0)} {num(y0)}A{num(radius)} {num(radius)} 0 {large} 1 {num(x1)} {num(y1)}"


def squircle(size, radius, smoothing=0.6):
    """Square tile from (0, 0) with Apple-style continuous corners (Figma corner smoothing)."""
    p = min((1 + smoothing) * radius, size / 2)
    sweep = 90 * (1 - smoothing)
    chord = math.sin(math.radians(sweep / 2)) * radius * math.sqrt(2)
    tangent = radius * math.tan(math.radians((90 - sweep) / 4))
    beta = math.radians(45 * smoothing)
    c = tangent * math.cos(beta)
    d = c * math.tan(beta)
    b = (p - chord - c - d) / 3
    a = 2 * b
    corner = lambda sx, sy, flip: (
        f"c{num(sx * a)} 0 {num(sx * (a + b))} 0 {num(sx * (a + b + c))} {num(sy * d)}"
        f"a{num(radius)} {num(radius)} 0 0 1 {num(sx * chord)} {num(sy * chord)}"
        f"c{num(sx * d)} {num(sy * c)} {num(sx * d)} {num(sy * (b + c))} {num(sx * d)} {num(sy * (a + b + c))}"
    ) if not flip else (
        f"c0 {num(sy * a)} 0 {num(sy * (a + b))} {num(sx * d)} {num(sy * (a + b + c))}"
        f"a{num(radius)} {num(radius)} 0 0 1 {num(sx * chord)} {num(sy * chord)}"
        f"c{num(sx * c)} {num(sy * d)} {num(sx * (b + c))} {num(sy * d)} {num(sx * (a + b + c))} {num(sy * d)}"
    )
    return (f"M{num(size - p)} 0{corner(1, 1, False)}L{num(size)} {num(size - p)}{corner(-1, 1, True)}"
            f"L{num(p)} {num(size)}{corner(-1, -1, False)}L0 {num(p)}{corner(1, -1, True)}Z")


def aperture(outer, inner, count=6, rotation=-75, centre=CENTRE):
    """Blade outlines and the hexagonal opening of a lens iris."""
    half = inner * math.tan(math.pi / count)
    reach = math.sqrt(outer * outer - inner * inner)
    edges = []
    for index in range(count):
        phi = math.radians(rotation + index * 360 / count)
        nx, ny, dx, dy = math.cos(phi), math.sin(phi), -math.sin(phi), math.cos(phi)
        at = lambda t: (centre + inner * nx + t * dx, centre + inner * ny + t * dy)
        edges.append((at(-half), at(half), at(reach)))
    blades = []
    for index, (_, corner, tip) in enumerate(edges):
        next_start, next_corner, next_tip = edges[(index + 1) % count]
        blades.append(f"M{num(corner[0])} {num(corner[1])}L{num(tip[0])} {num(tip[1])}"
                      f"A{num(outer)} {num(outer)} 0 0 1 {num(next_tip[0])} {num(next_tip[1])}"
                      f"L{num(next_corner[0])} {num(next_corner[1])}L{num(next_start[0])} {num(next_start[1])}Z")
    opening = "M" + "L".join(f"{num(start[0])} {num(start[1])}" for start, _, _ in edges) + "Z"
    return blades, opening


def rounded_polygon(points, radius):
    """Closed polygon whose corners are rounded with quadratic curves."""
    corners = []
    for index, corner in enumerate(points):
        towards = lambda other: tuple(c + (o - c) / math.dist(other, corner) * radius for c, o in zip(corner, other))
        corners.append((towards(points[index - 1]), corner, towards(points[(index + 1) % len(points)])))
    path = f"M{num(corners[0][2][0])} {num(corners[0][2][1])}"
    for start, corner, end in corners[1:] + corners[:1]:
        path += f"L{num(start[0])} {num(start[1])}Q{num(corner[0])} {num(corner[1])} {num(end[0])} {num(end[1])}"
    return path + "Z"


def blur(name, deviation):
    return (f'<filter id="{name}" x="-50%" y="-50%" width="200%" height="200%">'
            f'<feGaussianBlur stdDeviation="{deviation}"/></filter>')


LENS_DEFS = (
    '<linearGradient id="bezel" x1="0.15" y1="0.05" x2="0.85" y2="0.95">'
    '<stop offset="0" stop-color="#f4f7f6"/><stop offset="0.35" stop-color="#b9c3c0"/>'
    '<stop offset="0.7" stop-color="#59625f"/><stop offset="1" stop-color="#2a302e"/></linearGradient>'
    '<linearGradient id="bezelEdge" x1="0.85" y1="0.95" x2="0.15" y2="0.05">'
    '<stop offset="0" stop-color="#e9eeec"/><stop offset="0.5" stop-color="#7d8784"/>'
    '<stop offset="1" stop-color="#1b201f"/></linearGradient>'
    '<radialGradient id="glass" cx="0.56" cy="0.6" r="0.62"><stop offset="0" stop-color="#18463a"/>'
    '<stop offset="0.45" stop-color="#0a211b"/><stop offset="1" stop-color="#020806"/></radialGradient>'
    '<radialGradient id="pupil"><stop offset="0" stop-color="#000"/><stop offset="0.75" stop-color="#020504"/>'
    '<stop offset="1" stop-color="#0c2a21"/></radialGradient>'
    '<linearGradient id="spec" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#fff" stop-opacity="0.95"/>'
    '<stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient>'
    + blur("soft6", 6) + blur("soft14", 14)
)

LIGHT_DEFS = (
    '<radialGradient id="halo"><stop offset="0" stop-color="#3ccf7f" stop-opacity="0.6"/>'
    '<stop offset="1" stop-color="#3ccf7f" stop-opacity="0"/></radialGradient>'
    '<radialGradient id="core" cx="0.42" cy="0.38" r="0.6"><stop offset="0" stop-color="#c9ffe4"/>'
    '<stop offset="0.5" stop-color="#7ff5bb"/><stop offset="1" stop-color="#2fbf7a"/></radialGradient>'
)


def lens():
    """Bezel, knurled grip, glass, iris and coating reflections, in body coordinates."""
    blades, opening = aperture(150, 70)
    ring = lambda r, attrs: f'<circle cx="{CENTRE}" cy="{CENTRE}" r="{r}" {attrs}/>'
    return "".join((
        ring(338, 'fill="url(#bezel)"'),
        ring(322, 'fill="url(#bezelEdge)"'),
        ring(314, 'fill="#151a19"'),
        ring(303, 'fill="none" stroke="#2b3331" stroke-width="20" stroke-dasharray="6.5 6.5"'),
        ring(290, 'fill="#0b0f0e"'),
        ring(282, 'fill="none" stroke="#3d4644" stroke-width="3"'),
        ring(266, 'fill="url(#glass)"'),
        ring(212, 'fill="none" stroke="#1f3a32" stroke-width="3" stroke-opacity="0.8"'),
        ring(160, 'fill="#030907"'),
        '<g fill="#163a2f" stroke="#020605" stroke-width="7">', *(f'<path d="{d}"/>' for d in blades), "</g>",
        f'<path d="{opening}" fill="url(#pupil)"/>',
        ring(160, 'fill="none" stroke="#2f8f6e" stroke-width="3" stroke-opacity="0.55"'),
        '<g filter="url(#soft14)" fill="none" stroke-linecap="round">',
        f'<path d="{arc(230, 192, 272)}" stroke="#5fe7b0" stroke-width="34" stroke-opacity="0.62"/>',
        f'<path d="{arc(200, 14, 76)}" stroke="#8a72ff" stroke-width="26" stroke-opacity="0.55"/>',
        f'<path d="{arc(124, 118, 172)}" stroke="#46d7ff" stroke-width="14" stroke-opacity="0.5"/>',
        "</g>",
        ring(58, 'fill="#3ccf7f" fill-opacity="0.18" filter="url(#soft14)"'),
        '<ellipse cx="408" cy="392" rx="66" ry="34" transform="rotate(-38 408 392)" fill="url(#spec)" filter="url(#soft6)"/>',
        '<ellipse cx="404" cy="388" rx="34" ry="14" transform="rotate(-38 404 388)" fill="#fff" fill-opacity="0.9"/>',
        '<circle cx="458" cy="450" r="9" fill="#fff" fill-opacity="0.75"/>',
        '<circle cx="640" cy="652" r="14" fill="#fff" fill-opacity="0.18" filter="url(#soft6)"/>',
    ))


def light(x=806, y=218):
    """The green camera-in-use light, top right, in body coordinates."""
    return (f'<circle cx="{x}" cy="{y}" r="44" fill="url(#halo)"/>'
            f'<circle cx="{x}" cy="{y}" r="15" fill="url(#core)"/>'
            f'<circle cx="{x - 4}" cy="{y - 5}" r="6" fill="#fff" fill-opacity="0.85"/>')


def svg(view, defs, body):
    return f'<svg {SVG_NS} viewBox="0 0 {view} {view}"><defs>{defs}</defs>{body}</svg>\n'


def full_bleed(body, size=1024):
    """Scale body-coordinate artwork to a full-bleed tile of the given size."""
    return (f'<g transform="translate({num(size / 2)} {num(size / 2)}) scale({size / 824:.6f}) translate(-512 -512)">'
            f'{body}</g>')


def layer(defs, body):
    return svg(1024, defs, full_bleed(body)).replace("<svg ", '<svg width="1024" height="1024" ', 1)


# The dashboard's Continuity camcorder, in the same body coordinates as the Optic artwork.
CAMCORDER = ('<rect x="176" y="322" width="456" height="380" rx="114"/>'
             f'<path d="{rounded_polygon(((654, 458), (846, 352), (846, 672), (654, 566)), 34)}"/>')
CAMCORDER_LENS = (404, 512)

CAMCORDER_DEFS = (
    f'<linearGradient id="field" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{CAMCORDER_FIELD[0]}"/>'
    f'<stop offset="1" stop-color="{CAMCORDER_FIELD[1]}"/></linearGradient>'
    '<linearGradient id="glyph" x1="0" y1="322" x2="0" y2="702" gradientUnits="userSpaceOnUse">'
    '<stop offset="0" stop-color="#80f1c2"/><stop offset="0.5" stop-color="#31c088"/>'
    '<stop offset="1" stop-color="#128a5e"/></linearGradient>'
    '<radialGradient id="lensGlass" cx="0.38" cy="0.34" r="0.75"><stop offset="0" stop-color="#1f5a48"/>'
    '<stop offset="0.6" stop-color="#0a2620"/><stop offset="1" stop-color="#04110d"/></radialGradient>'
)

# Depth for the header icon; browser tabs are too small to show it.
CAMCORDER_DEPTH_DEFS = (
    '<radialGradient id="halo" cx="0.5" cy="0.56" r="0.5"><stop offset="0" stop-color="#2fd18c" stop-opacity="0.3"/>'
    '<stop offset="1" stop-color="#2fd18c" stop-opacity="0"/></radialGradient>'
    '<linearGradient id="gloss" x1="0" y1="322" x2="0" y2="560" gradientUnits="userSpaceOnUse">'
    '<stop offset="0" stop-color="#fff" stop-opacity="0.4"/><stop offset="1" stop-color="#fff" stop-opacity="0"/>'
    '</linearGradient>'
    '<linearGradient id="edge" x1="0" y1="322" x2="0" y2="702" gradientUnits="userSpaceOnUse">'
    '<stop offset="0" stop-color="#fff" stop-opacity="0.55"/><stop offset="0.4" stop-color="#fff" stop-opacity="0.08"/>'
    '<stop offset="1" stop-color="#003b26" stop-opacity="0.35"/></linearGradient>'
    '<linearGradient id="sheen" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity="0.1"/>'
    '<stop offset="0.45" stop-color="#fff" stop-opacity="0"/><stop offset="1" stop-color="#000" stop-opacity="0.1"/>'
    '</linearGradient>'
    '<linearGradient id="rim" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity="0.3"/>'
    '<stop offset="0.16" stop-color="#fff" stop-opacity="0.1"/><stop offset="0.5" stop-color="#fff" stop-opacity="0"/>'
    '<stop offset="1" stop-color="#000" stop-opacity="0.2"/></linearGradient>'
    f'<clipPath id="camcorder">{CAMCORDER}</clipPath>'
    '<filter id="lift" x="-25%" y="-25%" width="150%" height="160%" color-interpolation-filters="sRGB">'
    '<feGaussianBlur in="SourceAlpha" stdDeviation="20"/><feOffset dy="18" result="o"/>'
    '<feFlood flood-color="#020a07" flood-opacity="0.55"/><feComposite in2="o" operator="in" result="s"/>'
    '<feMerge><feMergeNode in="s"/><feMergeNode in="SourceGraphic"/></feMerge></filter>'
    + blur("soft10", 10)
)


def camcorder_lens(ring=118, glass=82, pupil=22, glow='r="34" fill="#3ee59a" fill-opacity="0.55" filter="url(#soft10)"'):
    """The original white lens: ring, dark glass, green glow, pupil and highlight, in body coordinates."""
    x, y = CAMCORDER_LENS
    circle = lambda r, attrs: f'<circle cx="{x}" cy="{y}" r="{num(r)}" {attrs}/>'
    return "".join((
        circle(ring, 'fill="#f4fff9"'),
        circle(glass, 'fill="url(#lensGlass)"'),
        circle(glass, 'fill="none" stroke="#0b3a2d" stroke-width="4"'),
        f'<circle cx="{x}" cy="{y}" {glow}/>',
        circle(pupil, 'fill="#03100c"'),
        '<ellipse cx="376" cy="482" rx="22" ry="13" transform="rotate(-35 376 482)" fill="#fff" fill-opacity="0.85"/>',
    ))


def web_icon():
    """Continuity as the dashboard header shows it: the camcorder on its full-bleed field with a soft rim."""
    tile = squircle(1024, TILE_RADIUS)
    art = "".join((
        '<rect width="1024" height="1024" fill="url(#field)"/>',
        '<rect width="1024" height="1024" fill="url(#halo)"/>',
        f'<g filter="url(#lift)" fill="url(#glyph)">{CAMCORDER}</g>',
        '<g clip-path="url(#camcorder)"><rect x="100" y="300" width="824" height="280" fill="url(#gloss)"/>',
        f'<g fill="none" stroke="url(#edge)" stroke-width="7">{CAMCORDER}</g></g>',
        camcorder_lens(),
        '<rect width="1024" height="1024" fill="url(#sheen)"/>',
    ))
    return svg(1024, CAMCORDER_DEFS + CAMCORDER_DEPTH_DEFS + f'<clipPath id="tile"><path d="{tile}"/></clipPath>',
               f'<g clip-path="url(#tile)">{full_bleed(art)}</g>'
               f'<path d="{tile}" fill="none" stroke="url(#rim)" stroke-width="{num(7 * LAYER_SCALE)}" '
               'clip-path="url(#tile)"/>')


def favicon(tone=None):
    """Continuity simplified for 16-32 px browser tabs; live, busy and danger add a status badge.

    The lens ring is thicker than the header's so it stays white, not grey, at 16 px.
    """
    defs = CAMCORDER_DEFS + ('<radialGradient id="glow"><stop offset="0" stop-color="#3ee59a" stop-opacity="0.6"/>'
                             '<stop offset="1" stop-color="#3ee59a" stop-opacity="0"/></radialGradient>')
    art = f'<g fill="url(#glyph)">{CAMCORDER}</g>' + camcorder_lens(130, 76, 26, glow='r="44" fill="url(#glow)"')
    body = f'<path d="{squircle(32, 7.2)}" fill="url(#field)"/>{full_bleed(art, 32)}'
    if tone:
        body += f'<circle cx="25" cy="8" r="6" fill="{BADGES[tone]}" stroke="#fff" stroke-width="2"/>'
    return svg(32, defs, body)


def icon_document():
    group = lambda name, image, shadow: {
        "layers": [{"glass": False, "image-name": image, "name": name.lower()}],
        "name": name,
        "shadow": shadow,
        "specular": False,
        "translucency": {"enabled": False, "value": 0.5},
    }
    srgb = lambda colour: "srgb:" + ",".join(f"{int(colour[i:i + 2], 16) / 255:.5f}" for i in (1, 3, 5)) + ",1.00000"
    return {
        "fill": {"linear-gradient": [srgb(FIELD[0]), srgb(FIELD[1])]},
        "groups": [
            group("Light", "light.svg", {"kind": "none", "opacity": 0.5}),
            group("Lens", "lens.png", {"kind": "neutral", "opacity": 0.6}),
        ],
        "supported-platforms": {"squares": ["macOS"]},
    }


def text_outputs():
    """Every generated text file and its exact content."""
    outputs = {
        ICON / "icon.json": json.dumps(icon_document(), indent=2, sort_keys=True) + "\n",
        ICON / "Assets/light.svg": layer(LIGHT_DEFS, light()),
        WEB / "app-icon.svg": web_icon(),
        WEB / "favicon-idle.svg": favicon(),
        GUEST_FAVICON: favicon(),
    }
    for tone in BADGES:
        outputs[WEB / f"favicon-{tone}.svg"] = favicon(tone)
    return outputs


def generate():
    rsvg = shutil.which("rsvg-convert")
    if not rsvg:
        raise SystemExit("rsvg-convert is required to render the lens layer: brew install librsvg")
    for path, text in text_outputs().items():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
    with tempfile.TemporaryDirectory() as temp:
        source = Path(temp) / "lens.svg"
        source.write_text(layer(LENS_DEFS, lens()))
        subprocess.run([rsvg, "--width", "1024", "--height", "1024", "--output", str(ICON / "Assets/lens.png"),
                        str(source)], check=True)


def stale():
    return [path for path, text in text_outputs().items() if not path.is_file() or path.read_text() != text]


def preview(destination, size=512):
    developer = subprocess.run(["xcode-select", "-p"], check=True, capture_output=True, text=True).stdout.strip()
    ictool = Path(developer).parent / "Applications/Icon Composer.app/Contents/Executables/ictool"
    if not ictool.is_file():
        raise SystemExit("previews need Icon Composer's ictool from Xcode 26 or later")
    destination.mkdir(parents=True, exist_ok=True)
    for rendition in APPEARANCES:
        subprocess.run([str(ictool), str(ICON), "--export-image", "--output-file", str(destination / f"{rendition}.png"),
                        "--platform", "macOS", "--rendition", rendition, "--width", str(size), "--height", str(size),
                        "--scale", "1"], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--check", action="store_true", help="report generated text files that differ; write nothing")
    parser.add_argument("--preview", type=Path, metavar="DIR", help="also render the macOS appearances into DIR")
    args = parser.parse_args()
    if args.check:
        outdated = stale()
        for path in outdated:
            print(f"out of date: {path.relative_to(ROOT)}", file=sys.stderr)
        sys.exit(1 if outdated else 0)
    generate()
    print(f"Icons written: {ICON.relative_to(ROOT)}, {WEB.relative_to(ROOT)} and {GUEST_FAVICON.relative_to(ROOT)}")
    if args.preview:
        preview(args.preview)
        print(f"Previews: {args.preview}")


if __name__ == "__main__":
    main()

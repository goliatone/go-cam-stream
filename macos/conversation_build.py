#!/usr/bin/env python3
"""Verify the running Mac app and served conversation page against one fresh bundle."""
import argparse
from http.client import HTTPConnection, HTTPException, HTTPSConnection
import json
from pathlib import Path
import plistlib
import re
from secrets import token_urlsafe
import subprocess
from urllib.parse import urlsplit


BUILD_ID_KEY = "CamstreamBuildID"
BUILD_ID = re.compile(r"[0-9a-f]{32}")
BUILD_META = re.compile(rb'<meta name="camstream-build-id" content="([0-9a-f]{32})">')
ASSET_PREFIX = "/conversation-assets/"
ASSET_REF = re.compile(rb'["\']/conversation-assets/(app-[A-Za-z0-9_-]+\.(?:js|css))["\']')
RUNNING_APPS = """
ObjC.import('AppKit')
const apps = $.NSRunningApplication.runningApplicationsWithBundleIdentifier('com.goliatone.camstream.camera')
const paths = []
for (let i = 0; i < apps.count; i++) {
    paths.push(ObjC.unwrap(apps.objectAtIndex(i).bundleURL.path))
}
JSON.stringify(paths)
"""


def stamp_conversation_page(page, build_id):
    html = page.read_bytes()
    if html.count(b"</head>") != 1 or b"camstream-build-id" in html:
        raise ValueError("Conversation HTML must have one head and no existing build ID")
    meta = f'<meta name="camstream-build-id" content="{build_id}">'.encode("ascii")
    page.write_bytes(html.replace(b"</head>", b"  " + meta + b"\n</head>"))


def read_build_id(app):
    with (app / "Contents/Info.plist").open("rb") as info_file:
        build_id = plistlib.load(info_file).get(BUILD_ID_KEY)
    if not isinstance(build_id, str) or not BUILD_ID.fullmatch(build_id):
        raise ValueError(f"{app} has no valid build ID. Build a fresh app with ./taskfile mac:build.")
    return build_id


def verify_running_app(expected):
    result = subprocess.run(
        ["/usr/bin/osascript", "-l", "JavaScript"], input=RUNNING_APPS,
        capture_output=True, text=True, timeout=10, check=True,
    )
    paths = json.loads(result.stdout)
    if len(paths) != 1:
        raise ValueError(f"Found {len(paths)} running Camstream Camera apps. Open only the expected bundle.")
    running = Path(paths[0])
    try:
        running_id = read_build_id(running)
    except ValueError as error:
        raise ValueError(f"Running Mac app has no valid build ID: {running}. Open the expected bundle.") from error
    if running_id != expected:
        raise ValueError(f"Running Mac app has build ID {running_id}, expected {expected}: {running}")
    return running


def page_build_id(html):
    matches = BUILD_META.findall(html)
    return matches[0].decode("ascii") if len(matches) == 1 else None


def browser_connection(origin):
    url = urlsplit(origin)
    if (not url.hostname or url.path not in ("", "/") or url.query or url.fragment
            or url.username is not None or url.password is not None):
        raise ValueError("Use an origin such as https://calls.example.com without a room path or invitation.")
    if url.scheme == "https":
        return HTTPSConnection(url.hostname, url.port, timeout=10)
    if url.scheme == "http" and url.hostname == "127.0.0.1":
        return HTTPConnection(url.hostname, url.port, timeout=10)
    raise ValueError("Use HTTPS or a local http://127.0.0.1 origin.")


def fetch(connection, path, size):
    connection.request("GET", path, headers={"Cache-Control": "no-cache"})
    with connection.getresponse() as response:
        if response.status != 200:
            raise ValueError(f"{path} returned HTTP {response.status}. Check that the expected app is hosting on this origin.")
        return response.read(size + 1)


def verify_browser(app, expected, origin):
    assets = app / "Contents/Resources/Conversations"
    html = (assets / "index.html").read_bytes()
    if page_build_id(html) != expected:
        raise ValueError("The bundle's conversation HTML and Mac app have different build IDs.")
    names = sorted({name.decode("ascii") for name in ASSET_REF.findall(html)})
    if {Path(name).suffix for name in names} != {".js", ".css"}:
        raise ValueError("The conversation HTML must reference its bundled JavaScript and CSS.")

    connection = browser_connection(origin)
    try:
        # A valid-shaped synthetic room path serves the real page without sending an invitation or joining.
        served = fetch(connection, "/r/" + token_urlsafe(32), max(len(html), 65536))
        served_id = page_build_id(served)
        if served_id != expected:
            raise ValueError(f"Browser page has build ID {served_id or 'missing'}, expected {expected}.")
        if served != html:
            raise ValueError("Served conversation HTML differs from the expected bundle.")
        for name in names:
            local = (assets / name).read_bytes()
            if not local or fetch(connection, ASSET_PREFIX + name, len(local)) != local:
                raise ValueError(f"Served conversation asset differs from the expected bundle: {name}")
    finally:
        connection.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("app", type=Path, metavar="APP_PATH", help="app path printed by test:conversation or mac:build")
    parser.add_argument("origin", metavar="ORIGIN", help="browser origin without a room path or invitation")
    args = parser.parse_args()
    try:
        app = args.app.resolve()
        expected = read_build_id(app)
        running = verify_running_app(expected)
        verify_browser(app, expected, args.origin)
    except (OSError, ValueError, HTTPException, plistlib.InvalidFileException, subprocess.SubprocessError) as error:
        parser.exit(1, f"Conversation build verification failed: {error}\n")
    print(f"Build ID: {expected}")
    print(f"Running Mac app: {running}")
    print("Browser conversation HTML, JavaScript and CSS match the bundle.")


if __name__ == "__main__":
    main()

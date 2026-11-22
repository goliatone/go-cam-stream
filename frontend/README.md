# Camera viewer

The Preact/TypeScript viewer at `/` uses the published [HTTP contract](../data/api/README.md)
and T07 API for catalog, status, WebRTC negotiation and owned-session release.
It starts muted and offers Play when autoplay is denied or live video is paused.
Fullscreen covers the viewer, video and controls, where the browser supports
element fullscreen, and stays on when cameras switch; picture in picture uses
detected browser APIs. See [browser behavior](../docs/browser-client-compatibility.md).

The page puts the video first. A 56 px top bar names the camera, and the camera
rail collapses to icons, starting that way below 1024 px until someone opens or
collapses it; the browser then keeps that choice in local storage. The 16:9 stage takes
the largest size that fits the window, so desktop views do not scroll. On a phone
in portrait the player stays pinned while the stream details and camera list
scroll beneath it. On a touch screen in landscape it fills the screen, and its
**Cameras** button opens a sheet with the camera list and the stream choice; a
zoomed or short desktop window keeps the regular layout and scrolls instead.
Controls sit over the video and fade after 3 seconds of playback; moving the
pointer, a tap or keyboard focus brings them back. **Stop** ends this page's
session and frees its viewer slot, also while a view is still connecting, and
Play starts a new one.
Press <kbd>?</kbd> for keyboard shortcuts. Loading shows a skeleton of the same
layout.

When the connection drops, the last frame stays in view under a compact
**Reconnecting** badge that counts down its 15-second recovery window. If it does
not recover, the player says **Connection lost** and tries again on its own, up
to twice, counting down each wait; **Try again** starts at once and **Stop** ends
the wait. The stream
details say how fresh the status is ("Updated 4 s ago") and warn when updates stop
while the page is visible. Each failure names its error code, camera, stream and
time. Where the server reports a failure of this camera or stream and another
camera can play, **Choose another camera** opens the camera list; a lost
connection or a timeout offers **Try again** only, because this device's network
may be the cause. The selected camera row shows this page's own state. Other rows
show the server's status of that camera when it says something: **Live**,
**Connecting**, **Reconnecting**, **Offline** or **Unavailable**. An offline or
failed camera is dimmed but stays a link. An idle camera shows no status, because
idle does not establish that it can play. In the collapsed rail, each camera's
tooltip adds that status to its name.

Each view has an address: `?camera=<id>&stream=<id>`, with the stream omitted for a
camera's first stream. Opening it selects that view when the catalog lists it.
Otherwise an unknown stream falls back to the camera's first stream, and an
unknown camera to the first playable camera, without an error. A plain click on a
camera switches in place and updates only these parameters without adding
history; Back across the page's own fragment entries keeps the address on the
view shown. A modified or middle click opens that camera in a new tab, which
connects only to that camera. The tab title and icon show the camera and its state.

## Run locally

Use Node 22.22.2+, 24.15.0+, or 26+ (supported even-numbered lines):

```sh
cd frontend
npm ci
CAMSTREAM_API_ORIGIN=https://your-authorized-serve-origin npm run dev
```

Open <http://127.0.0.1:5173/>. The optional loopback development proxy verifies TLS
and forwards `/api` to that exact HTTPS origin. It supplies no identity header;
the authorized ingress must establish identity. `CAMSTREAM_DEV_CA=/absolute/ca.pem`
adds a CA for a controlled local test ingress without changing browser/system trust.
No proxy is included in the production bundle. Serve must expose the eventual
production page and API at the same origin; the production Go handler now embeds the assets via T09.

For a presentation-only preview, open `/preview.html`. It uses labeled synthetic
inputs and callbacks, never live media or operator requests. The preview and test
fixtures are excluded from `npm run build` output.

The separate `/prototype.html` entry explores a proposed player UI using a local
simulator and canvas-generated video. Start `npm run dev` without an API origin,
then open `http://127.0.0.1:5173/prototype.html`. Use **Prototype controls** to
simulate catalog errors, connection failures, source reconnects, themes and
operator controls. `?camera=livingroom&stream=compact` selects a synthetic stream.
It makes no camera/API requests, opens no capture device and persists no settings.
Its broader automatic retries and richer camera status are proposals; the viewer
itself retries only a lost connection, as described under Ownership and recovery.
The prototype is type-checked and tested but excluded from production build/embed
output; it does not establish live playback or browser/hardware compatibility.

```sh
npm run typecheck
npm test
npm run build
node scripts/embed.mjs # refresh data/web for the Go binary
```

`frontend/dist` and `data/web` are generated and ignored by Git. From the repository
root, `./scripts/build-web.sh` installs locked dependencies and performs both build
and embed steps. Run it before compiling or testing packages that import the Go
host; missing embedded assets intentionally fail compilation. The controlled-media
runner builds these assets inside its Docker image automatically.

## Styles

`src/styles.css` defines each colour, font and font size once, as a custom
property in its first `:root` rule, and its other rules use only these tokens. The
dialog backdrop's token is set on `::backdrop` itself, because older browsers do
not let a backdrop inherit custom properties. The page tokens (`--text`,
`--surface`, `--accent` and the rest) set the page's look. The stage tokens
(`--stage`, `--on-stage` and the rest) and the backings for what sits on the video
(`--glass`, `--scrim`, `--tip`, `--toolbar-shade`) stay dark, because they sit
around and on the video.

The page follows the system's light or dark setting. When the system prefers
dark, a `:root` rule inside `@media (prefers-color-scheme: dark)` replaces the
page tokens, and `--stage-frame` draws a line around the stage, which a light page
does not need. `color-scheme: light dark` on `:root`, and a `color-scheme` meta in
`index.html`, let the browser draw scrollbars and its other parts in the same
scheme; the stage is always `color-scheme: dark`. There is no toggle, and nothing
is stored.

`src/styles.test.ts` fails when a rule uses a colour, font or font size directly,
named colours included, or uses a token that does not exist, or when a token goes
unused or another rule redefines one. It checks WCAG AA contrast for each text,
icon and focus ring against its background, in both themes. Anything on the video
is checked over a white frame: its rule must declare the backing the check
assumes, and no other rule may change that backing. The test also keeps the
toolbar's icons on the solid part of its shade, a focused control's halo inside
the stage and clear of its tooltip, the skip link's focus ring on its own margin,
and the stream select's text at 16 px, below which iOS Safari zooms in. When a new
colour pair appears, add it to that test; a new page token needs a dark value too.

Every button takes a role class: `primary-button`, `secondary-button`,
`ghost-button` or `danger-button` for text, `icon-button` for an icon on the page
and `control-button` on the video. Each role is drawn for the page. Inside
`.video-stage`, primary and secondary take the stage's colours, and the style test
checks that those rules win the cascade there in every state. Camera management's
styles live in the same stylesheet, and its tests fail when a management,
reconnect or rail button has no role.

## Preview snapshots

`npm run snapshots` captures every state of `/preview.html` at a git ref and in the
working tree, and lists the views that differ. It keeps no baseline images. The
ref is read with `git archive` into a temporary folder, so the checkout, index and
branches stay as they are.

```sh
npm run snapshots                              # the working tree against HEAD
npm run snapshots -- --base main               # a branch's work against main
npm run snapshots -- --base HEAD~3 --head HEAD --only phone --scheme dark
```

Each state is captured at three sizes, in light and in dark:

- 1440×900;
- a 375×812 phone's whole page;
- a 667×330 phone held sideways.

The shortcuts dialog and the collapsed rail are captured too, and the camera sheet
at all three sizes. That makes 136 views of each version, in about a minute for
both. The preview's own bar is hidden, so the app fills each size.

The project's Vite serves both versions with the working tree's dependencies; the
script notes when the ref's lock file differs. A local Chrome or Chromium captures
them, headless. However the run ends, Ctrl-C included, the browser closes and the
temporary folder is removed. Set `CHROME_PATH`, or pass `--chrome`, if Chrome is
not in the usual place.

To keep runs repeatable:

- the preview's clock stands still, and timers of a second or more never fire;
- the time zone is UTC and the locale en-US;
- each view starts with the page's local storage cleared, as on a first visit;
- animations and transitions are off, and reduced motion is set;
- colours are sRGB, and one device pixel is one CSS pixel;
- after the page loads, after the state is chosen and after each click, the
  script waits until nothing is loading and two frames in a row pass without a
  change to the page;
- each version runs in a browser of its own.

The report goes to `.snapshots/`, which Git ignores. `index.html` shows each
differing view's two captures and a diff, with pixels over the tolerance in red
and the others that differ in amber; `report.json` lists every view. Views still
changing when captured are listed with the reasons, since they may differ by
chance. An address that takes longer than 10 s to load is named, and later views
do not wait for it. The command exits 0 when no view differs, 1 when one does or
a state throws, and 2 when it cannot run.

A view differs when a pixel differs by more than 3 levels in a colour channel, or
when more than 1,000 pixels differ at all. From run to run, Chrome has drawn a
large blurred shadow or a gradient a level differently in up to about 750 pixels,
and an anti-aliased corner up to 3 levels differently; the eye can see neither.
`--tolerance` and `--area` change these limits, and `--tolerance 0` compares
exactly.

## Ownership and recovery

`useViewer` owns one `PlaybackController` per page. It receives filtered catalog
entries, selects only published profiles, gathers a complete receive-only offer,
and attaches the returned answer to that page's peer. Browser media events,
not catalog state or successful signaling, establish playing state.

Switching immediately detaches local media. A serialized cleanup/create chain
releases a late session response before admitting the newest selection. Failed
cleanup retains its session ID and withholds allocation until an explicit retry
confirms release. **Stop** releases the session through the same chain and keeps
the selection, so Play negotiates again; a failed release shows the cleanup error
instead of reporting the slot free. The stopped state is not stored: a reload, or
a return through the browser's back-forward cache, starts the view again as a
fresh page does. The viewer never replays a request whose outcome is uncertain,
and has no upstream reconnect, credential storage, session lease renewal or
foreground heartbeat.

Catalog and status observations are read-only and pause while hidden. Visible
pages refresh full camera membership, access and availability every four seconds,
and immediately when shown or after Save. Generation/request fencing rejects
older responses. Additions preserve the selected video and playback controller;
network failures preserve membership with unknown availability and a stale-list
message. An explicit access denial clears the authorized view. Freshness is timed on this device from when each
newer observation of a known state arrives; the server's observation time is
only compared with its earlier values, never with the device clock. A page shown
again does not warn while its first new request runs. Native ICE may recover
brief interruptions. Each new disconnection receives its own 15-second recovery
window, which the player counts down on screen; the initial connection deadline
is cleared on connection.

A peer that fails, or stays disconnected past its window, is a lost connection.
If its video played, the controller releases the session and, once the release
is confirmed, tries again on its own up to twice, 3 and then 10 seconds after
each loss. An automatic retry that cannot connect within its 20-second deadline
counts as lost too. A retry that comes due while the page is hidden waits until
the page is shown, so a page nobody sees does not take a viewer slot again.
**Try again**, another selection, **Stop** and failed cleanup cancel a pending
retry. A minute of video without a disconnection earns a new pair; an outage does
not count as video, so a link that keeps dropping stays bounded. Every other
failure, a create whose outcome is uncertain and video that never played wait for
**Try again**.
If session cleanup is denied after a controller restart or ownership change, the
page retains the old session and offers **Reload page**, a link to the same camera
and stream, instead of endlessly retrying that ID. Reloading checks the current
catalog/access and server readiness.
A control-plane outage alone does not end flowing media. Package CSRF is fetched afresh for normal writes.
Page exit attempts one keepalive DELETE with the last in-memory token, closes the
peer immediately, and relies on bounded server peer-loss cleanup if exit delivery
or an expired token prevents confirmation. Back-forward cache entry releases the
viewer; restoration reloads authorized scope.

The UI uses fixed error copy and projected public fields. The copy for each state
and error code lives in `src/services/copy.ts`, and the API client rejects a
state or code without an entry there. It never displays raw
server messages, SDP, candidates, private source URLs or secret references; a
failure's details line shows only its safe error code, the camera and stream
names and the time it was first shown. The
optional transport badge shows only the selected WebRTC protocol (`TCP`/`UDP`).

[Controlled browser evidence](../.ctx/specs/camera-streaming/support/evidence/t12-playback.md)
distinguishes tested behavior from pending target-browser/deployment gates.


## Add a camera

For Firefox with the native Mac development host, use
`./taskfile dev:serve PORT --media-address YOUR_MAC_PRIVATE_IPV4`. The browser
page and same-Mac camera URL remain localhost; only WebRTC media uses the
explicit private interface. [Verified flow and constraints](../docs/browser-client-compatibility.md).

Enable the configured principal in [management settings](../config/README.md#managed-camera-state).
The native development host enables its fixed local principal automatically.
Choose **Add Camera**, enter an RTSP address and optional credentials, or import
the Mac companion's exported YAML and optional credentials file. In the Mac app,
open **Connection Details → Export Connection Files → Show Files in Finder**
while capture runs. Import both files; localhost also requires credentials. YAML alone
requires entering credentials in **Edit details**. Exported grants are ignored.

Review the address and access guidance, test until decoded live video is verified
and preview cleanup completes, then choose **Save Camera**. Save leaves the current
view playing; **Watch Camera** selects the addition. Other visible authorized pages
refresh automatically. `localhost` is the server's machine. Imported hosts are
never silently changed to make a remote server reach the Mac.

Browser site storage is required before mutations. It retains only scoped IDs,
revisions and expiry, never endpoint, name, files or credentials. Lost replies and
reloads check the original operation. **Check status** resolves uncertainty;
**Retry original Save** preserves its key and original inputs when available.
Unknown saves cannot be abandoned or silently retried as new cameras. Unsaved
drafts lost at server restart require re-entry; durable saves recover by receipt.

**Reconnect Camera** appears for an authorized source fault and confirms its effect
on other viewers of that source. It remains available with management disabled
when reconnect grants remain. **Try again** only retries this page's session.

To replace a dashboard-added camera’s password, select it and open **Camera
settings**. An authorized duplicate-address error offers the same action.
Leave the username blank to reuse the saved one, or supply it for a camera
that used anonymous access. Stop any playback of that camera, then choose
**Review credentials → Test connection → Save credentials → Watch Camera**.
The change is applied to the existing camera after a successful test. Cameras
configured by the server use the [service’s private environment](../config/README.md#mixed-setup).

`npm run build` enforces gzip budgets from the manifest: 20,000 bytes for the
initial JavaScript dependency closure, 12,000 for separately loaded management,
and 6,000 for all CSS. Total JavaScript and SVG sizes are reported separately.
The management smoke drivers under `scripts/` require an explicitly isolated host,
an unsaved RTSP source and a locally installed browser driver; they never start
camera capture or store screenshots. Safari is also checked with its native
controls; a WebDriver setup failure is not browser playback evidence.
`management-import-smoke.mjs` accepts exact `SMOKE_YAML` and `SMOKE_ENV` export
paths and verifies lost import recovery and the YAML-only credential gate without
starting capture. It intercepts the response after Chromium uploads the files.


For Linux browser integration, run `./scripts/controlled-media.sh
--management-browser --output NEW_DIRECTORY` from the repository root. It exposes
only localhost HTTPS/media ports and writes the owned container names to `run.txt`.
Run `management-smoke.mjs` with `PLAYWRIGHT_MODULE`, `SMOKE_ORIGIN` and
`SMOKE_ENDPOINT`; the fixture authenticated source uses its documented synthetic
credentials through `SMOKE_USERNAME`/`SMOKE_PASSWORD`. After onboarding, send HUP
to test PID 7 in the owned app container to restart the host with management
disabled. `management-recovery-smoke.mjs` takes `SMOKE_ORIGIN_CONTAINER` and checks
restored grants, failed Watch, explicit reset, lost reset response and viewer
denial. Stop the owned app container gracefully to finish the harness and verify
`cleanup=true`. The fixture identity header and restart signal are test-only.

## Shared camera guest page

Camera Sharing's guest page is a separate entry (`share/index.html` and
`src/share/`) built into `dist-share/`: an `index.html` plus a flat `assets/`
directory served under `/share-assets/`. The helper's guest listener serves the
page at `/s/{share_id}`, and packaging copies `dist-share/` to the app's
`Contents/Resources/Sharing`. The page has no management controls and no
secret-bearing diagnostics.

- Opening the link reads the invitation from the URL fragment, removes it from
  the address bar and history entry, then exchanges it over HTTPS for an
  HttpOnly guest cookie. Opening the link again in the same tab removes the new
  fragment too. Nothing is kept in browser storage, so a reload without the
  fragment asks for the original link.
- Opening the page holds no viewer place. **Play** admits one viewer and
  negotiates receive-only video. **Live** appears once frames are presented.
  The page says **Video only**, attaches no audio, shows no sound or microphone
  controls and never asks for camera or microphone permission.
- A dropped connection keeps the last frame under a **Reconnecting** badge and
  keeps this tab's place for 30 seconds. The page then releases the place and
  offers **Retry**, which admits again if the link is still valid and a place
  is free. Video that stops arriving shows **Video paused** with **Retry**.
- An expired, stopped or invalid link ends playback, clears the video and
  offers no action. A full camera, an unreachable camera and an offline device
  offer **Retry**. Leaving the page releases its place.
- In-app browsers get a hint to open the link in Safari or Chrome. A browser
  without WebRTC video keeps the link in the address bar, so opening it
  elsewhere carries the complete link. Background playback is not promised.

```sh
npm run build:share   # typecheck, build dist-share/ and validate it
npm run preview:share # serve dist-share/ with local fixtures on port 4174
```

`scripts/share-artifact.mjs` checks the listener's layout and limits: an
`index.html` of at most 256 KiB and allowlisted flat asset names of at most
4 MiB each. It also checks the guest CSP (no inline scripts, styles or event
handlers, no `data:` URIs, only `/share-assets/` references), rejects storage,
cookie and capture APIs, and enforces gzip budgets of 16 kB of JavaScript and
4 kB of CSS. `scripts/share-fixture-server.mjs` serves the built page with the
listener's headers and answers the guest API with the published example shapes.
The first letter of the share ID selects a scenario, and `/` links each one. The
fixtures carry no media: an accepted offer gets an answer whose connection
cannot complete. Live playback is verified by the combined local smoke and the
external HTTPS proof. `dist-share/` is generated and ignored by Git.

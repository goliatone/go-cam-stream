# Camera viewer

The Preact/TypeScript viewer at `/` uses the published [HTTP contract](../data/api/README.md)
and T07 API for catalog, status, WebRTC negotiation and owned-session release.
It starts muted and offers Play when autoplay is denied or live video is paused.
Fullscreen covers the player and its controls where the browser supports element
fullscreen, and picture in picture uses detected browser APIs. See [browser behavior](../docs/browser-client-compatibility.md).

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
Automatic retries and richer camera status are proposals, not production behavior.
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

## Ownership and recovery

`useViewer` owns one `PlaybackController` per page. It receives filtered catalog
entries, selects only published profiles, gathers a complete receive-only offer,
and attaches the returned answer to that page's peer. Browser media events,
not catalog state or successful signaling, establish playing state.

Switching immediately detaches local media. A serialized cleanup/create chain
releases a late session response before admitting the newest selection. Failed
cleanup retains its session ID and withholds allocation until an explicit retry
confirms release. No automatic mutation replay, upstream reconnect, credential
storage, session lease renewal or foreground heartbeat exists in the viewer.

Status observations are read-only and pause when hidden. Native ICE may recover
brief interruptions; a failed peer exposes **Try again**. Each new disconnection
receives its own 15-second recovery window; the initial connection deadline is
cleared on connection.
If session cleanup is denied after a controller restart or ownership change, the
page retains the old session and offers **Reload page**, a link to the same camera
and stream, instead of endlessly retrying that ID. Reloading checks the current
catalog/access and server readiness.
A control-plane outage alone does not end flowing media. Package CSRF is fetched afresh for normal writes.
Page exit attempts one keepalive DELETE with the last in-memory token, closes the
peer immediately, and relies on bounded server peer-loss cleanup if exit delivery
or an expired token prevents confirmation. Back-forward cache entry releases the
viewer; restoration reloads authorized scope.

The UI uses fixed error copy and projected public fields. It never displays raw
server messages, SDP, candidates, private source URLs or secret references. The
optional transport badge shows only the selected WebRTC protocol (`TCP`/`UDP`).

[Controlled browser evidence](../.ctx/specs/camera-streaming/support/evidence/t12-playback.md)
distinguishes tested behavior from pending target-browser/deployment gates.

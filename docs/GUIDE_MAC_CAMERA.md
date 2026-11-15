# MacBook RTSP camera

`camstream-camera` turns a macOS webcam into an authenticated H.264 RTSP source.
For double-click installation, use the versioned camera DMG: copy the app to
Applications, eject the disk, then open it and explicitly Start Camera. See
[Mac app instructions](../macos/README.md#menu-bar-app) and
[release/install status](../macos/README.md#versioned-releases). Local development
DMGs are not signed/notarized releases. The commands below remain the diagnostic CLI workflow.
It runs separately from the Linux camstream viewer/server. The exported folder
includes FFmpeg, MediaMTX and their non-system libraries; target Macs need neither
Homebrew nor Go. Keep the entire folder together. No microphone capture or recording.

## Configure and run

Extract the archive for your Mac architecture and open Terminal in that folder:

```sh
./camstream-camera devices
./camstream-camera configure --listen 0.0.0.0 --host YOUR_MAC_LAN_IP
./camstream-camera start
```

`configure` chooses the built-in MacBook/FaceTime camera. Use `--device "EXACT NAME"`
from `devices` to select another webcam. The default is 1280x720 at 30fps; use
`--width`, `--height`, `--fps` if the camera requires another mode. Configuration
flags describe the complete desired configuration; omitted flags use defaults.
Credentials are generated once and preserved across configuration changes.

macOS may request camera permission for Terminal or the application launching the
helper. Grant Camera access in System Settings → Privacy & Security and rerun if
necessary. Camera capture begins only with `start`. Keep that terminal open.
`start` reports streaming only after FFmpeg publishes frames. Closing the owner,
Ctrl-C, a child failure or `stop` shuts down both capture and RTSP processes.

In another terminal, from the same exported folder:

```sh
./camstream-camera status
./camstream-camera stop
```

`stop` is idempotent and waits for cleanup. Configuration changes require stopping
first. There is no launch-at-login service. A camera already in use, denied
permission or unsupported mode produces a bounded startup failure.

Without `--listen 0.0.0.0`, RTSP binds only to `127.0.0.1`. Network mode listens on
IPv4 interfaces and requires reader credentials. Use the Mac's LAN address/DNS
name reachable from the Linux host; `localhost` on that host is not the Mac.
Allow the chosen TCP port (default 8554) through the Mac firewall for the private
test network. RTSP is unencrypted; keep it on a trusted LAN or private VPN, with
no public port forwarding. A sleeping Mac or closed lid can interrupt capture.

## Add to camstream

Settings are in `~/Library/Application Support/camstream-camera` (directory 0700,
files 0600). All commands accept `--dir /absolute/private/directory` for a separate
instance. Generated files:

- `camera.json`: webcam, dimensions, binding and advertised endpoint.
- `credentials.json`: generated reader account; do not share in logs or chat.
- `camstream-camera.yaml`: camera/profile and operator-grant snippet to merge
  into the Linux server's configuration. Replace the Serve login and ensure
  unique camera/physical IDs if deploying multiple Macs.
- `camstream-camera.env`: `MAC_CAMERA_USERNAME` and `MAC_CAMERA_PASSWORD` values
  to transfer privately into the Linux service's environment. Do not commit it.

Validate and restart the Linux installation using its existing procedure. Select
the MacBook camera's `default` profile in the viewer. The endpoint is
`rtsp://YOUR_MAC_LAN_IP:8554/macbook`; credentials use separate environment
references. Multiple viewers should share the upstream via camstream. If a source
fault latched while the Mac was offline, restore capture then use the authorized
camstream operator reconnect action. Viewer retry alone does not clear that latch.

The helper reads credentials only at startup. Logs/status omit passwords, and
the publisher uses an anonymous loopback-only permission. Read access always
requires authentication, including from loopback. No management, WebRTC, HLS,
RTMP, SRT or recording service is enabled on the Mac bridge.

## Build an export

On an Apple Silicon Mac with Go, Xcode tools, Python 3.10+ and the prepared runtime
pack pinned in `macos/runtime.lock.json`:

```sh
python3 scripts/package-camera.py 0.1.0
# Or provide the complete locked runtime pack from offline storage:
python3 scripts/package-camera.py 0.1.0 --runtime-pack /path/to/camera-runtime-1-darwin-arm64-macos26.0.tar.gz
```

The packager verifies the complete archive, recipe/target and file inventory,
then builds the small Go binary with CGO disabled. It uses the cache offline by
default and creates a folder and `.tar.gz` under `dist/`, including checksums,
build information, upstream notices and corresponding source/build recipes.
No Homebrew runtime is selected. See [native build instructions](../macos/README.md)
for preparing the source-built dependency pack and the double-click proof app.
The Go helper can also be built alone with `go build ./cmd/camstream-camera`;
that diagnostic mode requires FFmpeg and the pinned MediaMTX on PATH.

The initial lock targets arm64/macOS 26.0. `RUNTIME_BUILDINFO.json` retains inspected
deployment targets, source versions/hashes and SDK/compiler provenance. FFmpeg
statically includes non-system dependencies; only Apple system libraries remain
dynamic. Local artifacts are ad-hoc signed, not Developer ID signed or notarized.
Final release/oldest-OS checks remain separate. No global Gatekeeper change is
needed. Keep the included sources and notices with distributed runtime assets.

Real Mac webcam validation establishes this bridge only. Concurrent real Kasa,
remote/private browser playback and hardware compatibility remain generic-rtsp
T06 and its EC70 prerequisites; synthetic video never substitutes for those checks.

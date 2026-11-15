# Native camera proof and pinned runtime

This is the small permission/lifetime harness used before building the T06 setup
and menu-bar app. It is a local development artifact, not an installable release.
It consumes the prepared runtime pack pinned in `runtime.lock.json`.

On the tested Apple Silicon/macOS 26 build host:

```sh
python3 macos/build-proof.py
# Or supply the exact locked archive from offline storage:
python3 macos/build-proof.py --runtime-pack /path/to/camera-runtime-1-darwin-arm64-macos26.0.tar.gz
```

Open `dist/Camstream Camera Proof.app` in **Finder**. Request Permission, then
Start. It selects the built-in webcam, publishes video only on
`rtsp://localhost:18554/macbook`, and shows actual producer frame progress.
Launch always starts stopped. Stop cancels a pending Start as well as active capture; closing the window preserves
the app; Quit waits for helper cleanup. Denial requires changing this app's
Camera permission in System Settings and relaunching. Never substitute a
Terminal-launched FFmpeg capture for this permission check.

After settings have been saved once, **Probe Denial** is available only when
AVFoundation reports denied access. This test-only action attempts the bundled
capture path directly: expect `capture_stopped` / `capture_failed`, zero frames,
and cleanup of the temporary RTSP listener. It checks OS enforcement beyond the
normal Start button's permission guard. Re-enable permission and relaunch to
verify recovery. An ad-hoc rebuild can request permission again despite keeping
the bundle identifier; stable signed upgrade identity is a T05 check.

The proof identity is `com.goliatone.camstream.camera.proof`. Its private settings
and sanitized `proof.jsonl` are under
`~/Library/Application Support/camstream-camera-native-proof`. Credentials and
connection exports are mode 0600; they are not packaged in the app or logged.
The production CLI's settings and port are separate.

The builder verifies the archive against the repository lock, checks its complete
file inventory/target, copies runtime, sources and notices, builds Swift/Go, and
signs the nested bundle. FFmpeg statically includes its non-system dependencies;
only Apple system libraries/frameworks remain dynamically linked.
It refuses an existing output. Use `--output PATH.app` for another build.
All app runtime lookup is bundle-relative with no PATH fallback or downloads.
Ordinary app builds are offline by default and never rebuild dependencies. The
cache is `.cache/camera-runtime/packs`; `--cache DIR` or `--runtime-pack FILE`
supports preseeded storage. `--download` permits fetching only a URL recorded in
the reviewed lock. No release URL is configured yet; the current pack is local.

**Signing boundary:** all executable code now uses ad-hoc hardened signatures,
including FFmpeg. Static non-system dependencies remove the T04 dylib Team ID
problem. No system security setting or Gatekeeper policy is changed. T05 still
must apply Developer ID signing and repeat permission/capture checks on the
notarized artifact. This proof does not certify that path, Intel, or macOS older
than 26. The build baseline is 26.0; the actual tested host version is recorded
separately in the ctx evidence.

## Building a dependency pack

Prerequisites: Apple Silicon Mac, Xcode tools/SDK and Python 3.10+.
Go is needed for app/CLI builds, not the dependency build. No Homebrew binaries,
libraries, pkg-config, autoconf or automake are selected from the host. The build
bootstraps the pinned pkgconf source and uses a system-only PATH and private prefix.

```sh
# Download exact locked inputs if missing, then build from a fresh work directory.
python3 macos/build-runtime.py --work /private/tmp/camera-runtime-build
# Offline build: preseed every filename in runtime.lock.json sources first.
python3 macos/build-runtime.py --offline --cache /path/to/cache --work /private/tmp/camera-runtime-offline
```

The source cache is `CACHE/sources`. A wrong checksum is an error, never silently
replaced. The builder compiles FFmpeg 9.0.2 and pinned x264 with a fixed reduced
feature set and static libraries, and copies the exact official MediaMTX 1.21.3
arm64 release. Its capability gate requires AVFoundation, libx264, H.264 decoding,
RTSP/TCP and null-sink smoke support. Lavfi/testsrc2 and concat are retained for
camera-free validation and private-input reader probes. Upstream-selected RTSP
dependencies are recorded in BUILDINFO; this is not a general FFmpeg distribution.

The resulting archive and candidate `.record.json` go into `CACHE/packs` by
default. Review the candidate hash into `runtime.lock.json` → `packs.darwin-arm64`
after validating it. The builder does not change the lock, publish an asset or
guess a release URL. A dependency/recipe change requires a rebuilt pack and new
approved archive hash; a cached pack with a different recipe/target is rejected.
Choose a new runtime ID for a distributed dependency revision. App versions and
runtime IDs remain independent.

Pack layout: `tools/`, `licenses/`, `sources/`, `recipe/`, `BUILDINFO.json` and
`MANIFEST.json`. It includes the exact FFmpeg/x264/pkgconf source archives, build
scripts, configuration, toolchain/SDK provenance, dependency inspection and
upstream notices. FFmpeg/libx264 use GPL-2.0-or-later; MediaMTX is MIT; pkgconf is
an ISC build tool. Keep the accompanying sources/notices with distributed assets.
See [FFmpeg's upstream license guidance](https://ffmpeg.org/legal.html) and the
pack's license texts. Fixed inputs do not promise byte-identical output across
different SDKs/compiler versions; exact prepared archive hashes select artifacts.

Camera-free pack boundary checks:

```sh
python3 -m unittest discover -s macos -p 'test_*.py' -v
```

## Native/helper contract v1

Launch `Contents/Helpers/camstream-camera app-session --dir PRIVATE_DIRECTORY`
with inherited stdin/stdout pipes. Keep the stdin writer alive for the entire
app session; close unused pipe ends in the parent. Do not detach CLI `start`.
Use one reader to continuously drain stdout and correlate replies by `id`.
Stderr and process exit are startup/transport failures, not protocol messages.

Requests/replies are UTF-8 JSON objects separated by newlines. Each request has
`version: 1`, a unique caller-assigned `id` (1–64 alphanumeric, `_` or `-`, first
character alphanumeric) and `method`. Only `configure` accepts `config`.
Unknown fields/methods/versions, invalid config and extra JSON values are rejected.
Send one request at a time; there is no replay cache or retry deduplication.
Maximum request line is less than 16 KiB; queue capacity is eight. Oversized input,
queue overflow, EOF/read failure, signal or output failure ends the session.

```json
{"version":1,"id":"start-1","method":"start"}
{"version":1,"id":"start-1","result":{"state":"starting"}}
```

| Method | Result / behavior |
| --- | --- |
| `devices` | Array of `{index,name}` video devices; no screen/audio entries. Native shell obtains permission first. |
| `config` | Non-secret `{device,listen,host,port,path,width,height,fps}`. |
| `configure` | Requires the complete `config` object with those fields; uses the existing configure service, preserves credentials and returns resolved settings. Refused during capture. |
| `start` | Acknowledges `starting`; poll `status` for actual readiness. Refuses an existing owned or external capture. |
| `status` | `{state,url?,frames?}`; URL never contains credentials. `streaming` requires real frame progress. |
| `stop` | Cancels and reaps capture owned by this session, then replies `stopped`. Does not stop another CLI owner's capture. |
| `export` | Recreates private connection files while stopped; returns `{configuration,environment}` file paths, never their contents. |
| `quit` | Same owned cleanup as stop, replies, then exits. Closing stdin may end the session before the queued reply; helper exit is the Quit completion boundary. |

On open, the helper emits `{"version":1,"event":"ready","result":{"protocol":1}}`.
Unexpected capture exit emits `capture_stopped` with `state: stopped` and an
optional classified error. No automatic restart. A second session using the same
state directory emits `session_error` / `busy` and exits without disturbing the
owner. The session lock lasts until owned cleanup finishes.

Errors are `{code,message}` with fixed messages: `invalid_request`, `busy`,
`not_configured`, `configuration_failed`, `device_unavailable`, `capture_failed`,
`status_unavailable`, `export_failed`. Malformed requests receive no echoed ID.
Raw FFmpeg/MediaMTX/config errors and reader secrets never cross this protocol.
Camera permission status remains native AVFoundation state; `capture_failed`
alone does not diagnose permission denial. The UI must combine these signals.
Status reads the supervisor snapshot; an immediate poll after `start` can still
be `stopped` before its control socket exists. Keep the acknowledged starting
state until the supervisor responds or `capture_stopped` arrives. There is no
unsolicited streaming event; poll status to observe progress.

EOF cancels in-flight operations and owned capture. The app-session process owns
the existing supervisor's lifetime pipe; killing either the native app or its
app-session helper closes that chain and the supervisor reaps both runtime tools.
This does not promise cleanup after SIGKILL of the supervisor itself or a frozen
OS. Closing the setup window must leave the app and its pipe alive.

Camera-free native ordering check (uses a temporary executable):

```sh
proof_check_dir=$(mktemp -d)
xcrun swiftc macos/CameraProof/StartIntent.swift macos/CameraProofTests/main.swift -o "$proof_check_dir/start-intent-tests"
"$proof_check_dir/start-intent-tests"
rm -rf "$proof_check_dir"
```

Focused Go checks (leave `CAMSTREAM_CAMERA_BUNDLE` unset for camera-free tests):

```sh
go test -race ./internal/maccamera ./internal/cameracli ./cmd/camstream-camera
go vet ./internal/maccamera ./internal/cameracli ./cmd/camstream-camera
golangci-lint run ./internal/maccamera/... ./internal/cameracli/... ./cmd/camstream-camera/...
```

Native hardware evidence is recorded in the macos-camera ctx spec. The proof
has no preview or recording and is independent of the mixed Kasa playback gate.

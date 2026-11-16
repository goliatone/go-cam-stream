# go-cam-stream

Camera-agnostic Go streaming library and a private multi-camera CLI/server viewer.
The reference application uses EC70 strategies, isolated stock-go2rtc workers,
Tailscale Serve identity and an embedded Preact player.

- [Installation, native-first validation and rollback](docs/GUIDE_INSTALLATION.md)
- [Controller and CLI](internal/app/README.md)
- [Reproducible controlled-media validation](internal/app/README.md#reproducible-controlled-media-validation)
- [Configuration](config/README.md)
- [MacBook webcam RTSP companion and export](docs/GUIDE_MAC_CAMERA.md)
- [Frontend development](frontend/README.md)
- [Browser compatibility](docs/browser-client-compatibility.md)
- [Public library](pkg/camstream/README.md)

Native systemd, real Serve/access policy and Compose validation pass with
controlled media on a Debian 12 arm64 VM. EC70 compatibility and Raspberry Pi
capacity remain unverified; no hardware or product acceptance is claimed. Build browser assets with `./scripts/build-web.sh`, then prepare the Linux
arm64 release with `./scripts/package.sh VERSION /absolute/pinned-go2rtc` on a
compatible Linux build host. Runtime requires no Node or source tree.

## Build from source

Use Go 1.27.4 and a Node version supported by [frontend/package.json](frontend/package.json).
From the repository root:

```sh
./scripts/build-web.sh
go build -o /tmp/camstream ./cmd/camstream
/tmp/camstream config validate --config config/config.example.yaml
```

The web step is required before Go host builds/tests: `data/web` is generated and
ignored, along with frontend dependencies/output, release archives and local
binaries. The [controlled-media runner](internal/app/README.md#reproducible-controlled-media-validation)
performs its own build in Docker. Local credentials belong outside the source
tree; `.env` variants and `secrets.env`/`*.secrets.env` are ignored, while sanitized
`.env.example` files remain eligible for review.

`.ctx/` is a separate local context repository and is excluded from application
commits. Links into it refer to development evidence available in that repository;
an application checkout does not include it or require it to build and run.

## Development server

Start with the included [development config](config/dev.yaml):

```sh
./taskfile dev:serve
```

Open <http://127.0.0.1:8081/> (or <http://localhost:8081/>). No Tailscale or
authentication setup is needed in this development mode.

Each invocation installs locked frontend dependencies, type-checks and builds the
viewer, embeds its assets, compiles `bin/camstream` and `bin/camstream-dev`, validates the configuration,
and starts the server in the foreground. Ctrl-C shuts it down; rerun after source
changes. `./taskfile dev:build` performs only the build steps.

On an Apple Silicon Mac, the default plays a labeled 1280×720 moving test pattern
through real H.264/WebRTC. No camera is opened. The task downloads and verifies
the pinned macOS go2rtc into `.cache/dev-tools`; FFmpeg must be on PATH
(`brew install ffmpeg`) or selected with `CAMSTREAM_DEV_FFMPEG=/absolute/path/to/ffmpeg`.
Use Go with CGO enabled and Xcode command-line tools for native socket inspection.
The default binds `127.0.0.1:8081`.
The task renders the config to ignored `.cache/dev/config.yaml`, expanding its
runtime path to this checkout's `.cache/dev/runtime`; no privileged directory is
needed. Check startup with `curl http://127.0.0.1:8081/healthz`.

To use your Mac webcam instead of the test pattern:

```sh
./taskfile dev:serve --webcam       # video device 0, no microphone
./taskfile dev:serve --webcam=1     # another AVFoundation video device
```

Open the same local URL; the selected **Mac webcam** starts playing automatically.
Allow Camera access for the launching terminal/app if macOS prompts (System
Settings → Privacy & Security → Camera). Opening/selecting the camera or pressing
Play starts capture. Stop releases your viewer session; capture ends after the
last viewer stops. Ctrl-C stops all helpers. The webcam must support 1280×720 at
30 fps.
List available webcams and their device indices without starting capture or
building the app:

```sh
./taskfile dev:webcams
./taskfile dev:serve --webcam=INDEX
```

The list excludes microphone and screen inputs and honors `CAMSTREAM_DEV_FFMPEG`.
Indices come from AVFoundation; rerun the list after connecting/disconnecting devices.
The webcam option requires macOS and the bundled config; omit it to return to the
test pattern. Custom configurations cannot be combined with `--webcam`.

For your own network cameras, use the [configuration guide](config/README.md)
and run `./taskfile dev:serve --config /absolute/path/to/dev.yaml`, or set
`CAMSTREAM_CONFIG` in the environment or ignored `.taskenv` in your working
directory. Precedence is `--config`, then `CAMSTREAM_CONFIG`, then the bundled
default. Relative custom config paths resolve from your working directory.

`camstream-dev` is compiled with the `camstream_dev` tag and grants the fixed
local developer access to all configured cameras. It accepts only loopback peers
and the local host/port, rejects cross-origin browser requests, and retains CSRF
checks. Its configured HTTPS origin is internal to the normal handler; the browser
uses the local HTTP URL. Normal `camstream serve` and release builds retain Serve
authentication and configured access grants; no environment or config switch
enables anonymous access in those builds.

macOS dev playback runs its own media worker groups. Stop releases the page's
session; Ctrl-C stops the server and helpers. An owner-lifetime pipe also stops
helpers after an abrupt server exit, and inherited locks block unsafe restart
cleanup. Custom configs use their actual RTSP/Kasa sources; Mac media candidates
are loopback-only. Production playback still requires its supported Linux
boundary; see [controller requirements](internal/app/README.md). For frontend
hot reload or the presentation-only preview/prototype, use the separate
[Vite development workflow](frontend/README.md#run-locally).

## Release

The checked-in `.version` is the shared camera/server version. With Git and
`git-cliff` installed, review and commit pending work on `main`, then run:

```sh
./taskfile version:get
./taskfile release patch --dry-run
./taskfile release patch       # or minor / major
```

The release command updates `.version`, generates `CHANGELOG.md`, commits those
files, creates an annotated `vVERSION` tag, and atomically pushes the branch and
tag to `origin`. It requires a clean working tree and a branch containing remote
`main`; it never merges automatically. Dry run reads remote refs but changes no
files or refs. Use `./taskfile version:bump patch` for a local version preview
while the working tree is dirty. Versions follow the native app's limits: up to
four major digits and two minor/patch digits, without leading zeros.

The tag starts **Versioned release artifacts** in GitHub Actions: checks → signed,
notarized Mac DMG + Linux server → unpublished draft → **release-publish** approval.
Download/review the draft, then open **Review deployments**, select
**release-publish**, and approve to publish. Until approval it stays unpublished.
CI requires the tag to match the committed `.version`.
[Signing setup and approval details](macos/README.md#shared-cameraserver-ci).

Optional `.release-notes.md` in the checkout is ignored by Git and included in the
changelog; it is removed only after a successful push and only if its contents
have not changed during release preparation. Use `--notes /path/to/notes.md` or
`--no-release-notes`. Explicit relative note paths resolve from your current
directory; missing explicit files fail before release. Version commands always
use this checkout's `.version`, even when invoked elsewhere.
A rejected push retains the local commit/tag and prints the
exact atomic push retry; inspect the remote and retry that push, rather than
bumping again. Earlier preparation failures leave any local edits visible for
review; nothing is reset automatically.

Existing manual drafts (including v0.1.3) are not retroactively enrolled in the
new approval job. This command prepares a new version and leaves those drafts
unchanged. The first patch from the current baseline is v0.1.4.

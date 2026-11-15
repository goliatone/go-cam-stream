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

Prepare a local startup YAML using the [configuration guide](config/README.md),
then run:

```sh
./taskfile dev:serve --config /absolute/path/to/dev.yaml
```

Each invocation installs locked frontend dependencies, type-checks and builds the
viewer, embeds its assets, compiles `bin/camstream`, validates the configuration,
and starts the server in the foreground. Ctrl-C shuts it down; rerun after source
changes. `./taskfile dev:build` performs only the build steps.

Alternatively, export `CAMSTREAM_CONFIG` (or set it in the ignored `.taskenv` in
your working directory) and run `./taskfile dev:serve`. Relative config paths are
resolved from your working directory. Use a private, writable
`backend.runtime_dir` for your development installation and open the configured
HTTPS origin through Serve. The example YAML contains placeholders.

The task uses the normal authentication and runtime settings. On macOS or outside
the supported Linux process boundary, HTTP diagnostics can run but playback stays
unready; see [controller requirements](internal/app/README.md). For frontend hot
reload or the synthetic preview/prototype, use the separate
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

Optional `.release-notes.md` is ignored by Git and included in the changelog; it
is removed only after a successful push. Use `--notes /path/to/notes.md` or
`--no-release-notes`. A rejected push retains the local commit/tag and prints the
exact atomic push retry; inspect the remote and retry that push, rather than
bumping again. Earlier preparation failures leave any local edits visible for
review; nothing is reset automatically.

Existing manual drafts (including v0.1.3) are not retroactively enrolled in the
new approval job. This command prepares a new version and leaves those drafts
unchanged. The first patch from the current baseline is v0.1.4.

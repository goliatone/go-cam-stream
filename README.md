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

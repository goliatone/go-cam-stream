# Native-first installation and rollback

Native systemd, real Tailscale Serve/access policy, and Compose deployment checks
pass with controlled media on a Debian 12 arm64 Lima VM. Native validation preceded
Compose. See the [T09 evidence](../.ctx/specs/camera-streaming/support/evidence/t09-compose.md).
VM results do not establish EC70 compatibility, Raspberry Pi capacity or product
acceptance. EC70 and representative Pi hardware validation (T08/T10) remains
deferred until the required devices and access are available.

## Artifact and host requirements

The initial target is Linux arm64 on Debian 12 or a compatible glibc 2.36+ system,
with systemd 252+ and unified cgroup v2. The container runtime is pinned Debian 12.
The host graph requires CGO; a CGO-disabled build is unsupported. Node, npm, Go,
GCC and source files are build-time dependencies only. Runtime needs the release's
`bin/camstream`, separate unmodified `bin/go2rtc` v1.9.15, libc/libgcc dependencies
listed in `RUNTIME_LIBRARIES`, a CA trust store, and host-installed Tailscale.

Prepare locally; these commands do not publish a release:

```sh
./scripts/build-web.sh
# On Linux arm64, with Go 1.27.4 and GCC/libc development tools:
./scripts/package.sh 0.0.0-local /absolute/go2rtc-linux-arm64
```

For macOS development, run the web step locally, then run `package.sh` in the
pinned `golang:1.27.4-bookworm` Linux arm64 image with read-only repository/module
mounts, a writable Go cache/output directory, and the supplied go2rtc file. The
script rejects wrong architectures, Go versions, go2rtc hashes and existing
artifact versions. Obtain go2rtc from its [official v1.9.15 release](https://github.com/AlexxIT/go2rtc/releases/tag/v1.9.15).
Expected Linux arm64 executable SHA256:
`af416f22b21a24ab83b8d78642190b4932ae695b4e386853f11d1b0319781763`.

`dist/camstream-VERSION-linux-arm64.tar.gz` contains both executables, configs,
unit/Compose files, licenses, build metadata, embedded asset manifest, runtime
library evidence and `SHA256SUMS`. Verify the archive checksum and then run
`sha256sum -c SHA256SUMS` inside its extracted directory. Application assets are
compiled into the Go binary; deleting Node/source/build directories does not affect
serving. A missing asset fails compilation or startup validation. Unknown
API/admin/probe/asset paths return 404, never a successful HTML fallback.

## Native installation (authorized test host first)

Use a dedicated `camstream` system account/group with no login shell. A trusted
administrator performs these steps after reviewing the artifact:

1. Extract a verified release under `/opt/camstream/releases/VERSION` (root-owned,
   not writable by camstream). Point `/opt/camstream/current` at that directory.
2. Create `/etc/camstream` mode 0750, root:camstream. Copy
   `deploy/config.example.yaml` to `config.yaml` mode 0640. Configure the actual
   Serve HTTPS origin, exact principal allowlist, camera endpoints/secret refs,
   installation ID, and the host's **Tailscale IPv4** for both media/candidate
   addresses. Leave HTTP on `127.0.0.1:8080` and private ports on loopback.
3. Create `/var/lib/camstream` owned by camstream, mode 0700. Use
   `/var/lib/camstream/runtime` in both installation modes. Never overlap native
   and Compose instances, and never change runtime paths to bypass a held lock.
   The state filesystem must permit execution of adapter-owned worker copies;
   a `noexec` mount prevents playback startup.
4. Create root-owned `/etc/camstream/secrets.env` mode 0600 using systemd
   `EnvironmentFile` syntax, with `KASA_USERNAME`/`KASA_PASSWORD` and, for the mixed deployment example,
   `DRIVEWAY_USERNAME`/`DRIVEWAY_PASSWORD`. Values are
   private; do not paste them into commands, diagnostics, source files or logs.
   Do not use shell `source` on this file. Configuration contains references only.
5. Install `deploy/systemd/camstream.service` as
   `/etc/systemd/system/camstream.service`, then `systemctl daemon-reload` and
   `systemctl enable --now camstream.service`. Review logs and GET
   `http://127.0.0.1:8080/readyz`; HTTP 503 is explicitly unready, not success.

The verifier requires the exact `camstream.service` system cgroup, matching MainPID,
`Type=exec`, `ExitType=main`, User=camstream, `KillMode=control-group`, SIGKILL
escalation, nondelegation, Restart=on-failure and a 60-second stop limit. Before old
worker files are retired, every process in that cgroup and its descendants must
be the controller itself. Unknown/lingering processes fail closed; nothing is
killed from a stale PID file. No environment flag bypasses these checks. Systemd
owns crash cleanup; application checks do not substitute for the required SIGKILL
experiment. See [kill behavior](https://www.freedesktop.org/software/systemd/man/latest/systemd.kill.html)
and [service restart behavior](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html).

Keep `AF_NETLINK` in `RestrictAddressFamilies`: Go/WebRTC enumerates Linux network
interfaces through route netlink during ICE negotiation. Omitting it allows idle
readiness but causes live negotiation to fail. The unit still grants no capabilities.

The shipped limits are 2 sources, 4 viewers, 4 viewers/source, 256 session records,
256 tasks, 512 MiB memory and 4096 file descriptors. At most six go2rtc workers are
needed for this configuration. These are provisional **configured ceilings**, not
measured Pi capacity. No transcoding/helper pipeline is enabled by production
configuration. Shutdown allows 60 seconds, exceeding the default application
cleanup budget (15+6+5 seconds). If changing timeouts/capacity, review the whole
budget and repeat deployment checks before changing the supported envelope.

## Private ingress and media

Configure host Tailscale Serve to proxy HTTPS to `http://127.0.0.1:8080`, following
[the official command documentation](https://tailscale.com/docs/reference/tailscale-cli/serve).
Use user identity, exact `Tailscale-User-Login` values, and static viewer/operator
allowlists. No Funnel, custom client identity header, anonymous route or alternate
LAN application listener is supported. The app checks raw loopback peer, external
Host, Origin and principal before serving UI/API; mutating requests also require
package CSRF. Verify this with the actual Serve installation before admission.

Review `deploy/media.nft` and integrate its rules into the existing host firewall
without flushing other rules. The media pool 35100–35103 TCP/UDP must be reachable
only on `tailscale0` and allowed by the intended Tailnet grants. Keep management
35000–35015 private. Firewall persistence is host-specific; prove it survives a
reboot. Retain host administrator/local-process trust. A listening HTTPS page does
not prove either media protocol or identity isolation.

## Native validation gate

On an authorized disposable host, install the artifact without Node/source files.
Use controlled media via the test-only provider or the existing integration
fixture; production has no synthetic-source/authentication switch. Record OS,
systemd, architecture, libc, exact artifact hashes and source conditions.

- Verify assets/API/CSRF via actual Serve, denied identity and direct ingress;
  unknown API/static paths; missing/wrong go2rtc; missing/corrupt build assets;
  TCP and UDP candidate selection and tailnet-only media reachability.
- With two live viewers and an owned helper, record the unit cgroup/process tree,
  installation generation and a separate unrelated sentinel process. Kill only
  the unit's MainPID with SIGKILL. Prove all old descendants are gone **before**
  restarted readiness/new generation, and the unrelated process survives. Repeat
  bounded graceful stop, automatic restart, reboot and viewer/ingest fault checks.
- Exercise the configured viewer/port limit, a conflicting second controller and
  failed cleanup. Record peak cgroup memory/task counts and measured startup/stop
  times under capacity. Unknown cleanup must withhold admission.
- Perform the rollback below. Preserve evidence and do not begin Compose
  deployment validation until the native gate passes.

Record native results separately from actual Serve/media-policy results. Static
verification and mocked cgroup tests alone do not establish crash containment.

## Compose (after native validation)

Use Linux arm64 Docker Engine and Compose supporting raw env files (2.30+).
Docker Desktop is not the validated deployment topology. Build from the extracted
archive, without a source tree or Node:

```sh
docker build --platform linux/arm64 -f RELEASE/deploy/Dockerfile -t camstream:VERSION RELEASE
```

Set `CAMSTREAM_IMAGE` to that immutable version tag or digest and
`CAMSTREAM_UID`/`CAMSTREAM_GID` to the native account's numeric IDs. Create
`/etc/camstream/compose.secrets.env` mode 0600 using Compose **raw** env-file format:
`KEY=value` with literal values, no surrounding quotes. This is deliberately a
separate file from systemd's differently parsed EnvironmentFile. Never use real
secrets for `compose config` output; use `--no-env-resolution` when inspecting it.

Stop and disable the native service; confirm its cgroup is empty and all listeners
are gone. Then use `docker compose -f RELEASE/deploy/compose.yaml up -d` on the
same host. The container uses host networking for host Serve's raw-loopback path,
a private PID namespace, Docker init, read-only root, no capabilities, bounded
memory/tasks and the same host installation directory. Do not add published
ports, `pid: host`, a shared worker container, a backend API proxy or an anonymous
ingress. External management/RTSP endpoints remain private.

Repeat the native gate's equivalent container cases: actual Serve, network policy,
controller SIGKILL with helpers, full old namespace exit before restart/readmission,
unrelated-container preservation, capacity, clean install, reboot, stop and rollback.
Do not rely on a healthcheck `exec` during clean-boundary admission: extra processes
in the namespace intentionally block it. Probe from the host instead. The container
requires CA trust and glibc but no Go/Node/compiler/source tree at runtime.

## Rollback and mode changes

Keep the prior verified release and a private backup of configuration/secrets.
Stop the current mode and confirm complete cleanup first. Never run two modes or
versions concurrently. Do not remove lock/installation/unknown worker files to
force startup. Switch `/opt/camstream/current` atomically to the previous immutable
release and restore its compatible configuration if necessary, then start the
native service and verify readiness/authorized playback. For Compose, recreate
with the prior immutable image and compatible configuration only after the old
container has completely stopped. Restoring native also requires stopping/removing
Compose before enabling the service. Keep the stable installation ID/runtime root.

Missing or malformed installation metadata/unknown files require operator review
while stopped; there is no automatic adoption or destructive repair. No automatic
updater, remote installation or release publication is performed by build scripts.

## Add an RTSP camera or Raspberry Pi bridge

The default server accepts RTSP and Kasa in the same startup catalog. Follow
[the mixed configuration steps](../config/README.md#mixed-setup) for fields,
profiles, grants, limits and troubleshooting. Generic RTSP requires a documented
local RTSP endpoint over TCP. A cloud-only camera without that endpoint cannot be
added through this provider; the seller/brand name alone does not establish support.

For a conventional IP camera, enable its local RTSP service and obtain the exact
port, path, query and camera account from its documentation/settings. Choose an
H.264 stream (often a substream) and begin video-only. Add both environment
references if authentication is enabled. Anonymous mode is only appropriate when
the separately managed upstream and network policy permit it.

For a Pi camera, first run a separately managed capture-to-RTSP bridge on the
camera Pi. Camstream neither opens CSI/USB devices nor installs/starts that bridge.
[MediaMTX's official Pi camera guide](https://github.com/bluenviron/mediamtx/blob/main/docs/4-static-sources/9-raspberry-pi-cameras.md)
describes a native setup on supported Pi OS versions. Its minimal path example is:

```yaml
# In the bridge's mediamtx.yml, not camstream config:
paths:
  cam:
    source: rpiCamera
```

With that bridge listening on port 8554, the camstream `workshop` example maps to
host `CAMERA_PI_ADDRESS`, port `8554`, path `/cam`. Verify the chosen bridge release,
OS/camera support, actual port, authentication and H.264 output before use. A USB
camera may need a different capture/publish pipeline supplied by its bridge.
Bridge capture/encoding CPU is additional to camstream relay work. Multiple
independent cameras behind one shared RTSP listening endpoint are outside the
current identity model; use separate listening endpoints. Keep the bridge in its
own service/container outside `camstream.service`'s cgroup or private PID namespace.
Never add an exec/transcoder input to camstream to start capture.

After updating `/etc/camstream/config.yaml` and the private environment file:

```sh
/opt/camstream/current/bin/camstream config validate --config /etc/camstream/config.yaml
# Native installation; run during the intended playback interruption:
sudo systemctl restart camstream.service
curl --fail http://127.0.0.1:8080/readyz
```

For Compose, follow the stop/recreate ordering above so the updated environment
is read; a simple process restart does not reload container environment values.
Do not overlap native and Compose owners or skip the empty-boundary check. Test
local/private remote playback through the existing authenticated Serve origin,
then verify simultaneous separate pages, profile selection and operator recovery.
Offline validation never proves playback. Controlled fixtures do not establish
support for the actual Pi/IP camera, simultaneous real Kasa media or Pi capacity.

# Startup configuration

`Load(ctx, path)` reads an explicit YAML file through go-config with defaults and
strict field decoding. URI/expression/environment expansion and input logging
are disabled. `Prepare(ctx, raw, DefaultProviders()...)` validates options, catalog,
identities, supported profiles and grants, returning typed provider configs.
Neither step resolves credentials, creates runtime directories or dials media.
The example mixes two Kasa cameras, a Pi RTSP bridge and a two-profile IP camera.
Replace its reserved addresses, origin and exact Serve logins before composing
an installation. An empty catalog is valid.

The default server and offline CLI share EC70 and RTSP providers. Explicit
`Prepare`/controller injection still uses only the supplied provider list.

The EC70 decoder accepts only `host`, optional `port`, `username_ref` and
`password_ref`. Its initial secret policy allows `env://NAME` references only.
Missing values are detected later by the injected go-secrets environment resolver
at the Kasa encoder boundary. The reusable EC70 strategy/encoder support other
resolvers; a host can inject another `Provider` without changing the core. Custom
decoders must perform no I/O and return immutable typed config and a canonical,
credential-free device endpoint. Provider strategies must honor context.

The RTSP decoder accepts `host`, optional `port` (default 554), optional paired
`username_ref`/`password_ref`, and required `profiles`. Each profile has `id`,
`path`, optional `name` (defaults to ID), raw non-secret `query`, and `audio`
(defaults false). Both references must be absent for anonymous access or valid
`env://NAME` references for authentication. Explicit zero/null ports, empty/null
references, unknown fields and invalid profiles fail offline validation. Paths
are decoded absolute paths; queries preserve their raw encoding. See the
[RTSP strategy](../pkg/camstream/strategies/rtsp/README.md) for syntax and limits.

Aliases of the same endpoint use the same physical ID. Different endpoints must
not reuse that ID; normalized endpoint duplicates cannot claim different devices.
DNS aliases are not resolved. Omitted camera `streams` selects all supported
profiles (EC70 currently has only `default`). Grants always list explicit profiles;
view, diagnose and reconnect are separate permissions. Unknown principals/scopes
are denied, and logins are exact and case-sensitive. There are no wildcard grants.

`ServiceOptions()` and `BackendConfig(verifyBoundary)` map a single capacity owner
into both layers. Private ports must cover two per concurrent source plus one per
viewer; media ports cover one per viewer. Ranges must be disjoint and exclude the
loopback application listener. Limits, observation and cleanup windows are checked
together. `operation_timeout` bounds status, diagnose and reconnect; the public
service bounds negotiation and close with its own configured deadlines. These are
operation limits, never active viewing TTLs. Defaults are not validated Pi capacity.

The [composition root](../internal/app/README.md) supplies the verified containment
boundary, installation lock and runtime lifecycle. Configuration cannot enable
exec sources or provide a successful verifier. The default host requires the
verified dedicated Linux init-container or native systemd boundary described in
the installation guide.
The configured runtime directory owns controller metadata and a `workers/`
subdirectory; preserve its installation identity across controlled restarts.
Startup configuration is immutable for a controller lifetime. Parser/provider
errors expose fixed classified messages only.

Run offline validation with:

```sh
go run ./cmd/camstream config validate --config config/config.example.yaml
```

Configuration tests: `go test ./config`.

## Mixed setup

Start from [config.example.yaml](config.example.yaml). The smaller
[deployment example](../deploy/config.example.yaml) keeps the existing two-source,
four-viewer ceiling with one Kasa and one RTSP camera. Replace all reserved
addresses and obtain exact paths/profiles from each device; examples are not
vendor compatibility claims. Keep H.264 video and `audio: false` initially.

1. Set a unique camera `id` and stable `physical_id`. Repeated aliases of one
   endpoint must share its physical ID. Profiles share a one-source device budget;
   close all viewers before switching to a different source/profile. One NVR or
   bridge host/port is one device here; separate channels are not independent budgets.
2. Enter a bare `host`, optional port and decoded absolute profile `path`. Put
   the original encoded query in `query`, without `?`. For example `/cam one`
   becomes `/cam%20one` on the wire; entering `/cam%20one` means a literal percent
   sign and becomes `/cam%2520one`. Never put passwords/tokens in paths/queries or
   supply a whole RTSP URL, `#` options, exec source or alternate transport.
3. Omit both references for anonymous RTSP, or configure both environment
   references and supply their values through the service's private environment.
   `DRIVEWAY_USERNAME`/`DRIVEWAY_PASSWORD` are separate from Kasa credentials.
   Validation checks reference syntax, not values or camera connectivity.
4. Select profiles with `streams` (omission selects all); grant each exact
   camera/profile to each Serve login. Operator diagnose/reconnect rights are
   separate from view rights. A viewer granted only `sub` cannot view `main`.
5. Run `camstream config validate --config /etc/camstream/config.yaml` before a
   controlled restart. `limits.max_sources` must cover every distinct configured
   selected source, including inactive profiles; equivalent aliases count once.
   Do not copy larger development limits into a deployment without checking its
   process/media port budgets and resource envelope.
6. Restart the owning service after config or credential changes; there is no
   live reload. Follow the [installation guide](../docs/GUIDE_INSTALLATION.md)
   for native/Compose stop ordering and private ingress. Open separate viewer
   pages for concurrent cameras; each page has one player.

Use `camstream list --origin https://YOUR-HOST.ts.net` to check visible grants,
then select the camera/profile in the viewer. `status` and `diagnose` expose
classified state only. After repairing a failed source, an authorized operator
runs the following command; a viewer retry alone cannot clear a source fault:

```sh
camstream reconnect --origin https://YOUR-HOST.ts.net --camera driveway --stream sub
```

That command uses the development example's `driveway/sub` IDs. The smaller
deployment example uses `room2/default`.

| Symptom | Check |
| --- | --- |
| Offline `invalid_argument` | Unknown keys, zero port, incomplete references, duplicate identity, undeclared selection/grants or source capacity. |
| `source_unavailable` | Service environment values, camera reachability, TCP RTSP enablement, exact path/query, credentials and upstream health. This code does not prove authentication failure. Repair first, then operator reconnect. |
| `camera_busy` | Another distinct profile of the same physical camera remains active. Close its viewers; aliases cannot bypass the budget. |
| Signaling/unsupported media | Use an H.264 profile. Audio is opt-in and must negotiate a supported codec; there is no transcoding. H.265/MJPEG support is not established. |
| Video starts then stalls | Upstream/network health; bounded source faults latch. Inspect classified diagnostics and perform an authorized reconnect after repair. |
| Unauthorized camera/profile | Exact Serve login, configured `streams` and explicit grants. Do not add wildcard grants or bypass identity. |

The controlled fixture establishes RTSP media and host behavior, not support for
every IP camera, Pi model or real EC70 firmware. Exact-device mixed playback and
representative Pi capacity remain separate hardware gates.

# Isolated go2rtc backend

This package implements `camstream.Backend`, `Runtime` and `SourceResetter` with the unmodified go2rtc **v1.9.15 Linux arm64** executable proven in T15. It shares one ingest worker per SourceKey and allocates one playback worker per session. Private RTSP/TCP connects the workers; go2rtc handles all media. This adapter has no camera-specific imports, application service, HTTP controller or command handlers.

See `ExampleNew` for external package construction. Supply a provider-owned `camstream.SourceEncoder[go2rtc.Input]`; `Input.URL` is private backend syntax, never application JSON. For the default EC70 pairing, inject `kasa.NewEncoder(secretResolver)` from the optional `go2rtc/kasa` subpackage; see the [EC70 integration instructions](../../strategies/ec70/README.md). No implicit encoder registration occurs.

## macOS development builds

`darwin && camstream_dev` adds an Apple Silicon development path using the pinned
macOS executable in [runtime metadata](../../../../data/runtime/go2rtc.lock.json).
Use `cmd/camstream-dev`, which implements the private worker entry point. Each
worker group has an owner-lifetime pipe and retains the runtime lock through
cleanup; native libproc socket inspection verifies assigned loopback listeners.
This requires CGO/Xcode tools. Ordinary builds keep the Linux-only target and
digest checks below. See [local development](../../../../README.md#development-server).

## Host responsibilities

1. Install the stock executable separately and configure its absolute path. `Start` copies it into a private generation directory and verifies SHA256 `af416f22b21a24ab83b8d78642190b4932ae695b4e386853f11d1b0319781763` before executing that snapshot. Imports and `New` do no I/O. Other runtime platforms currently return `unsupported_capability`.
2. Provide a private, exclusive `RuntimeDir` (0700), disjoint private/media port ranges, an explicit local IPv4 media bind address and a reachable candidate IPv4 address. Management/RTSP bind loopback. No STUN, public discovery or HTTP proxy environment is used. `Network` selects `tcp`, `udp` or the default `tcp+udp`; deployment must route the configured TCP/UDP media ports.
3. Supply `VerifyBoundary`. It must verify dedicated crash containment and old-generation cleanup before admission. `ContainerBoundary` checks that the controller is the direct child of `docker-init`/`tini`; use the tested dedicated `--init` container with the controller as its main command. This structural check does not create the containment boundary. Native systemd verification remains T09, with host composition in T07. A permissive callback is not deployment validation.
4. Call `Start` with the host's runtime-lifetime context. Use request contexts for operations and independent bounded contexts for cleanup. The backend owns workers after a successful negotiation; completion of an HTTP request does not terminate playback. `Close` stops admission, cancels pending creation and drains owned groups. Retry `Close` if it reports incomplete cleanup.

The runtime directory is locked exclusively. Unexpected/stale contents fail closed with `cleanup_incomplete`; the adapter never adopts or kills PIDs from files and never removes unknown directories. After a crash, the host must establish that the prior boundary is gone before retiring its known private generation files or assigning a clean generation directory. The crash test uses a fresh private directory after container restart. Full same-installation host restart/recovery and packaging remain T07/T09.

The generic service in T05 owns physical-camera/profile admission. This backend supplies source sharing and bounded worker/session capacity; it does not implement catalog, permissions or physical-camera policy.

## Definitions, signaling and cleanup

`EnsureStream` encodes and validates an immutable private source definition; it does not dial media. Secret references may be resolved by the injected encoder at this trusted boundary. The backend stores the private input and a fingerprint for conflict detection, discarding the caller's mutable Source object. Aliases with the same SourceKey must yield identical encoding, physical identity, capabilities and constraints. Credential/config changes require controlled definition replacement/restart; viewer retry does not modify a definition.

Create a unique SessionID before calling `CreateSession`. Offers use complete gathered SDP with receive-only audio/video media. The SDP parser rejects malformed/non-viewer offers before worker allocation; go2rtc negotiates codecs and ICE. Only a complete HTTP offer/answer exchange is used. Duplicate IDs reuse the same result or conflict; a changed offer/source cannot create another peer. Status calls omit the SDP answer. An answer is not evidence of playing media.

Readiness requires Linux `/proc` proof that the owned worker holds every assigned listener, followed by API and actual media observations. HTTP readiness alone is insufficient because the pinned backend silently ignores some media bind failures. Conflicts fail the bounded launch attempt; there is no unbounded port retry loop or adoption of another process's listener.

The observer uses native producer/consumer counters, including the pinned `webrtc/whep` HTTP consumer format. Unknown observations remain unknown. Source no-progress windows stop dependent playback/ingest workers and latch `source_unavailable`; only explicit `ResetSource` clears the latch after confirmed cleanup. No authentication failure is inferred from a stalled stream. A viewer failure closes its own worker; an ingest failure affects its source only. Native helper recovery may succeed within the source failure window; it does not introduce a second reconnect scheduler.

Close sends TERM then bounded KILL escalation to the owned process group. The leader remains unreaped until all live group members have exited, preventing PID reuse while signaling; `Wait` has one owner. Ports, records and files release only after confirmed cleanup. Uncertain cleanup retains reservations and blocks affected source admission. Active playback has no fixed TTL or heartbeat; abandoned negotiation and peer loss are reclaimed from observed state/progress.

Defaults are 10 s startup/no-progress windows, 5 s cleanup, 1 s TERM grace and 250 ms observation interval. Cleanup and polling add bounded observation/termination time; these settings are not target-device latency guarantees. Resource caps default to 16 source definitions and 32 active sessions. Closed session tombstones preserve duplicate-ID rejection up to `MaxSessionRecords` (default 10,000); reaching that bound returns `capacity_exceeded` until a fresh runtime. Tombstones retain an offer digest, not the SDP body or request context. These are configurable safety caps, not validated Raspberry Pi capacity.

Configs are 0600 in private worker directories. Source URLs never appear in argv; worker stdout/stderr is discarded because native output can contain credentials. Application boundaries should use `camstream.DescribeError`/their safe error projection, never serialize backend internals. `exec:`/`ffmpeg:` input is disabled unless `AllowExec` explicitly permits trusted, verified helpers that remain in the worker process group. The controlled helper tests do not authorize arbitrary executable input from clients.

## Verification

Ordinary `go test ./...` covers construction, definitions, pin consistency and the external-package example. Linux integration tests are opt-in and require the pinned T15 fixture image with synthetic H.264/Opus media; no camera credentials are used. Pion PeerConnection appears only in test peers. Production uses the small SDP parser for signaling validation, not a parallel media implementation.

From the module root, compile a Linux arm64 test binary and run it as the container's main command:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/camstream-backend-tests ./pkg/camstream/backends/go2rtc
docker run --rm --init --network none --cpus 2 --memory 768m \
  -e CAMSTREAM_GO2RTC_INTEGRATION=1 \
  -v /tmp/camstream-backend-tests:/tests:ro \
  camstream-t15-feasibility:1.9.15 /tests -test.v -test.run 'TestIntegration|ExampleNew' -test.timeout 90s
```

The controller crash test is separate: `python3 testdata/crash.py <linux-test-binary> <T15-host-peer-binary> <output-directory>`. Run from this package directory. It uses localhost test ports 35200 and 35100, kills only its dedicated container's controller, verifies external media stops and fresh restart media works, then removes the container. The public fixture endpoint is test-only and must never become application ingress.

Detailed reproducibility, immutable captures and limitations are recorded in `.ctx/specs/camera-streaming/support/evidence/t03-backend.md`. Native systemd crash cleanup, actual Serve/browser paths, EC70 compatibility, Pi capacity and sustained playback remain their existing delivery tasks.

# Streaming service

`NewService(backend, options, strategies...)` composes the public contracts without
I/O, global registration or application dependencies. It coordinates one exclusively
owned backend. Camera configuration remains provider-owned and immutable during
calls. The host owns the catalog, principal/session ownership and access policy.

For mixed Kasa/RTSP, construct `go2rtc.New(backendConfig, kasa.NewEncoder(resolver),
rtspencoder.NewEncoder(resolver))` and inject that backend plus `ec70.Strategy{}`
and `rtsp.Strategy{}` into `NewService`. Alias the RTSP encoder import to
`rtspencoder`; it is separate from the strategy package. The shipped host and
offline CLI use `config.DefaultProviders()` for both. See the
[EC70 instructions](strategies/ec70/README.md),
[RTSP instructions](strategies/rtsp/README.md), [mixed host setup](../../config/README.md#mixed-setup) and
[backend lifecycle requirements](backends/go2rtc/README.md).

1. Start the backend's optional `Runtime` with a host-lifetime context. Managed
   go2rtc requires its verified containment boundary and private runtime directory.
2. Call `service.Start(runtimeContext)` to enable admission and observation.
3. Use `Streams`, `StreamStatus`, `CreateSession`, `SessionStatus` and `CloseSession`.
   `SessionBinding` returns camera/profile identifiers for host access checks.
4. Authorize `ResetSource` as a separate operator action. It cancels affected
   negotiations, delegates cleanup/reset, and returns affected session IDs; the
   host maps catalog aliases. It is never part of viewer retry.
5. Drain with `service.Close(cleanupContext)`, then close the backend Runtime, even
   if draining reports incomplete cleanup. Keep cleanup contexts independent of
   canceled request/runtime contexts. Failed service cleanup can be retried. A
   service instance is single-use; cancellation alone does not prove cleanup.

The service generates a unique ID before backend allocation and atomically reserves
total viewer, per-source viewer and physical-device capacity. Aliases share the
per-source limit. MVP allows one distinct active SourceKey per PhysicalCameraID;
another profile returns `camera_busy`. All aliases must agree on constraints;
opaque source definitions are compared by the backend, not reflection in the core.
The host must provide consistent physical IDs and reject duplicate endpoints; the
generic service does not infer device identity from opaque provider values.

Negotiation has a deadline. A failed create can return an ID whose resources are
still owned; do not automatically retry uncertain POSTs. Cleanup uses an independent
bounded context. Successful backend close or fresh absence observations release
viewer slots; device release additionally requires no pending viewers and confirmed
idle/absent upstream. Unknown/stale state is never zero. If a definition was recorded
but no resource ever allocated, successful `RemoveStream` can prove absence; it
cannot erase a fault latch or close a live source. Repeated close of an owned ID is
idempotent; an ID never owned by this instance returns `not_found`.

The observer consumes backend state, including peer loss without client DELETE.
Active viewers have no TTL or heartbeat. Each source has independent serialization,
observation and drain work so slow/failing cameras do not hold a global I/O lock.
Only failed cleanup is retried; the backend alone owns media health/retry deadlines.
Source faults persist until explicit successful reset; failed reset remains blocked
until a successful reset retry. Neither viewer retry nor polling clears faults.

Defaults: 32 viewers total, 8 per source (across aliases), 16 source definitions,
10,000 session records; 10 s negotiation, 5 s cleanup, 250 ms observation, 5 s maximum
observation age. Configure these alongside backend limits. Source definitions/faults
and closed session records are bounded and retained for the service lifetime;
record exhaustion requires a fresh service/backend lifetime. Records retain no SDP,
request contexts, source values or secret bindings. These limits are safety defaults,
not validated Raspberry Pi capacity. Providers/backends must honor contexts and
their ownership/observation contracts; this service cannot make a noncompliant
backend safe by inventing liveness.

## Try the external consumer

```sh
go run ./examples/embedded
go test -race ./pkg/camstream ./examples/embedded
```

[The example](../../examples/embedded/main.go) owns its typed config/source/strategy
and matching backend. It demonstrates alias sharing and isolated close without
starting an application host or claiming real media playback. No encoder is needed
when a backend directly understands the typed source.

The opt-in Linux arm64 `TestServicePinnedMedia` runs this service over the actual
stock go2rtc backend in the existing synthetic fixture container. Set
`CAMSTREAM_SERVICE_INTEGRATION=1` on its compiled test binary, run directly under
`--init --network none`, and mount the pinned executable at `/usr/local/bin/go2rtc`.
It verifies C=2/V=3, isolated close, profile conflicts/reacquisition, observed peer
loss, operator reset and malformed-offer rollback. Hardware, browser, native service
and Raspberry Pi acceptance remain separate deployment checks.

# Shared application contracts (v1)

`contracts.go` and `validation.go` define the reference application's HTTP/CLI payloads; `frontend/src/services/contracts.ts` defines their TypeScript counterparts. JSON uses snake_case. `examples.json` supplies representative inputs for an empty catalog, two cameras, video-only media, offline/unknown observations, denied access, negotiation and operator operations. Its abbreviated SDP and CSRF strings are shape fixtures, not playable/valid credentials.

The reusable library lives in `pkg/camstream`. It does not import this application package, go-admin, Fiber or a media backend. Provider config, source values, SourceKey, physical-device identity, worker handles and secret references do not appear in these wire types. No routes, camera strategy, controller or streaming backend are implemented here.

## HTTP surface

Paths are relative to the configured Tailscale Serve HTTPS origin. The same origin is used by browsers and the CLI. Authentication precedes all application routes, including CSRF bootstrap; static/probe exceptions are explicitly host-owned. Request bodies use `application/json`, reject unknown fields and have a bounded size. The host takes camera/stream/session IDs from the route, never authority from JSON.

| Method/path | Input | Success | Permission |
| --- | --- | --- | --- |
| GET `/api/csrf` | none | 200 `CSRFResponse` | authenticated principal |
| GET `/api/cameras` | none | 200 `CameraList` | filtered viewer catalog |
| GET `/api/cameras/:camera_id` | none | 200 `CameraDetail` | viewer |
| GET `/api/cameras/:camera_id/status` | none | 200 `CameraStatus` | viewer |
| GET `/api/cameras/:camera_id/streams` | none | 200 `StreamList` | viewer |
| GET `/api/cameras/:camera_id/streams/:stream_id/status` | none | 200 `StreamStatus` | viewer |
| POST `/api/cameras/:camera_id/streams/:stream_id/sessions` | `CreateSessionRequest` | 201 `Session` | viewer + CSRF |
| DELETE `/api/playback/sessions/:session_id` | none | 204 only after confirmed cleanup | session owner or operator + CSRF |
| POST `/api/cameras/:camera_id/diagnose` | `DiagnoseRequest` | 200 `DiagnoseResponse` | operator + CSRF |
| POST `/api/cameras/:camera_id/streams/:stream_id/reconnect` | none; `StreamRef` identifies route parameters | 200 `ReconnectResponse` | operator + CSRF |

Reconnect targets the canonical source selected by the route and reports all affected aliases. No redundant camera/stream IDs are accepted in its body. Diagnose without `stream_id` covers the authorized camera's configured streams and returns capabilities plus sanitized source/backend/media checks. Checks distinguish passed, failed and unknown; dispatch success does not establish a passing diagnosis. Operator requests map to `camera.diagnose.v1` and `camera.reconnect.v1` service-backed commands in T06; no handlers are implemented in T02. Read requests use focused services. The CLI uses these exact payloads through Serve and has no direct-backend fallback.

A camera's `availability` summarizes the profiles the principal may view, from the service's latest background observations: `playing` when any plays, then `failed` (including a latched source fault), `offline`, `reconnecting` or `connecting` when any is; `idle` when all are idle; otherwise `unknown`, including an observation too old to trust. Listing performs no backend I/O and does not wait for a busy source. `idle` means no session or upstream, not that the camera can play.

Success collections are `[]`, never `null`. `viewers: null` and `observed_at: null` mean unknown; observed zero is distinct. Status errors use the safe `ErrorBody`, not raw backend text. Unknown/denied resources must not reveal catalog or session ownership; T07 must normalize those responses consistently. Unauthenticated or invalid-principal requests return 403 without a login redirect. Errors use `ErrorResponse` and `ErrorStatus`; a canceled transport may have no response at all. Cleanup failure returns `cleanup_incomplete` (503), retains reservations and is never a successful 204. A repeat DELETE is successful only when no owned resources remain; process-local bookkeeping alone cannot establish that.

## Negotiation and lifecycle

MVP uses one complete SDP offer/answer exchange per session, with ICE gathering complete before POST. Offer type is `offer`, answer type `answer`, playback protocol `webrtc`. Audio/video media in viewer offers uses `recvonly`; the T03 backend parses and rejects malformed/non-viewer SDP before allocating workers. The request envelope limits SDP to 64 KiB; the backend validates the actual SDP. No trickle-ICE or WebSocket signaling endpoint is required by the proven path. Media candidates may contain approved reachable media addresses; no camera credentials, upstream URLs or management listeners may escape in SDP.

The trusted service creates a unique `SessionID` and reserves capacity before `Backend.CreateSession`; that same ID remains usable for cleanup even if creation fails or the request is canceled. Backend duplicates reuse the same result or return conflict; changed source/offer under one ID conflicts. Every new HTTP create is a new logical session: clients must not automatically retry an uncertain POST. Canceled HTTP creation triggers bounded cleanup through the assigned ID; uncertain cleanup retains capacity. Retry after a known failure follows the service admission policy and cannot reset a latched source fault.

The initial session normally reports `connecting`; an SDP answer does not establish live media. Backend observations establish state and resource presence; browser peer state provides presentation feedback. Sessions have no fixed viewing TTL or foreground heartbeat. CSRF token expiration affects subsequent writes, not ongoing playback. Only explicit operator reset may clear a source fault after owned cleanup. A reset result lists affected camera/stream aliases and closed application session IDs.

## Serve identity and package-managed CSRF

The host accepts `Tailscale-User-Login` only on its trusted Serve ingress. Require one nonempty well-formed value; decode RFC2047 Q-encoded values with Go's MIME header decoder when present, then compare the exact login to the startup allowlist. Do not lowercase or substitute display names. Missing, duplicate, malformed and unknown principals are denied; tagged/headless devices have no implicit bypass. The installation has an operator-configured stable ID; principal/session ownership remains application-internal. [Tailscale's identity-header documentation](https://tailscale.com/docs/features/tailscale-serve#identity-headers) describes encoding, tagged-device limitations and the localhost trust boundary. Host-local administrators/services are trusted; a header alone cannot prove Serve provenance on a public listener.

Selected public APIs:

- go-admin `admin.Authenticator`, `HandlerAuthenticator` and `Authorizer` allow the host-owned Serve adapter; no password account database or go-auth login flow is required.
- go-auth `middleware/csrf.New(Config)` and `csrf.RegisterRoutes(router, RouteConfig{Path: "/api/csrf"})` provide token generation, verification and bootstrap. Run authenticated-principal middleware first. Set `TokenLookup` to `header:X-CSRF-Token`, an explicit expiration (initially 30 minutes), and safe methods GET/HEAD/OPTIONS. TRACE is not enabled.
- Use stateless mode with a single middleware instance/key per application runtime. Its `SessionKeyResolver` returns the hex SHA-256 of `installationID + "\x00" + authenticatedPrincipal`; fail authentication before reaching CSRF if either identity is absent. This fixed-format binding avoids exposing login names and the package token's colon-delimited payload constraint. The package owns token signing/validation; this is only identity binding, not a second CSRF implementation.
- The package generates a cryptographic key if none is supplied; constructing its middleware once shares that key across routes. Restart invalidates old tokens, so clients bootstrap again. No cookie, session store or custom token generator is required for this selected flow.
- GET bootstrap returns `token`, `field_name`, `header_name` with no-store headers. Browser and CLI send the returned token in the configured header on every unsafe request. They must not log/cache token bodies. Map package validation failures to the safe `csrf_invalid` envelope; obtain a fresh token rather than silently repeating mutations.
- Enforce the configured browser Origin and reject cross-origin writes; CLI requests may omit Origin but still require trusted identity and a valid token. Do not enable cross-origin credential access or an anonymous development bypass.

T02 verifies public API compilation and a synthetic authenticated GET/POST flow, valid/missing/invalid/principal-mismatched tokens and lack of a cookie requirement. Real Serve boundary, allowlist, origin and session-ownership tests remain T07. Earlier planning assumed a bootstrap cookie; the verified stateless public API supersedes that assumption, with the same identity and CSRF requirements.

## Dependency and executable pins

`go.mod`/`go.sum` pin published modules, with no local replacements/workspace. go-admin and quickstart use coordinated `v0.145.1`; other selected public modules are go-command `v0.27.1`, go-config `v0.15.1`, go-secrets `v0.2.0`, go-errors `v0.12.0`, go-logger `v0.10.1`, go-auth `v0.45.7`, and go-router `v0.65.0`. Module toolchain remains Go `1.27.4`. `internal/dependencies/contracts_test.go` verifies host dependency seams without installing a host; the generic library imports only go-errors beyond the standard library.

The selected host dependency graph requires CGO: `go-repository-bun v0.16.2` references `go-sqlite3` types unavailable with `CGO_ENABLED=0`. Linux arm64 tests pass with Go 1.27.4 and GCC 12.2 in Debian bookworm. Build the host with CGO enabled on the target architecture or a compatible cross toolchain; validate the packaged libc/runtime in T09. Pure-Go core/API consumption remains supported. No dependency patch or local replacement is required.

`../runtime/go2rtc.lock.json` pins the exact stock Linux arm64 runtime proven by T15. It is deployment metadata, not a browser payload or Go module dependency. This slice installs no executable and implements no worker launcher. T03 consumes the pin for the adapter; T09 packages/verifies it. Native systemd and target-Pi acceptance remain T09/T10 gates.

## Verification

Run `GOWORK=off go test ./...` and the repository lint command. For external consumer and frontend verification, install TypeScript `5.9.3` in a temporary tools directory, then run:

```sh
python3 data/api/check_contracts.py --tsc /absolute/path/to/typescript/bin/tsc
```

The checker publishes only a synthetic local module version to a temporary file proxy, compiles/runs the external typed provider/config/encoder and alternate backend fixture without `replace` or `go.work`, checks the core import boundary, and compiles all JSON examples against the TypeScript contracts. It publishes nothing remotely and creates no persistent nested module. Runtime/backend conformance, concurrency and actual media tests remain T03/T05; a fake consumer is not evidence of those implementations.

## T07 transport implementation

The [controller and CLI](../../internal/app/README.md) implement these routes with
loopback peer verification, exact Serve identity/Host/Origin checks and package
CSRF. The minimal go-admin module exposes the same diagnose/reconnect handlers at
`/admin/api/camera/cameras/:camera_id/diagnose` and
`/admin/api/camera/cameras/:camera_id/streams/:stream_id/reconnect`.
`/healthz` and `/readyz` are loopback-only GET probes; API health is authenticated.
T09 mounts the embedded frontend and exact manifest-listed assets inside the same
authenticated ingress; unknown paths return 404 without SPA fallback. Controlled Linux media and a
synthetic Serve HTTPS proxy verify the integration; actual Tailnet/deployment and
hardware/browser acceptance remain later tasks.

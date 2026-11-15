# Application service and commands

`NewCameraService(core, catalog, timeout)` snapshots a `config.Prepare` catalog and
adds access policy to the public streaming service. The composition root owns the
core/backend runtime and must route all application mutations through this service.
Constructors perform no backend or secret I/O. Provider-owned config is immutable.

A trusted ingress adapter calls `WithPrincipal` only after authenticating the exact
Serve login. This function does not validate headers or establish a trust boundary.
List/detail return only viewable profiles; status/create require view permission.
Session close requires its creating principal with view permission, or an operator
with reconnect permission for that session's scope. Unknown sessions are denied;
known owned closes remain idempotent. Principal bindings, like core session records,
are bounded by `MaxSessionRecords` for the controller lifetime.

Diagnose requires its own permission for every requested profile. It observes the
core without acquiring sources or resolving credentials. Idle/unobserved checks
remain unknown; passed media checks reflect backend observations, not browser
playback acceptance. Status and diagnose return safe operational errors in their
typed results. Reconnect requires permission on **every configured alias** sharing
the source, then delegates reset and reports affected aliases and confirmed closed
IDs. Failed cleanup reports no confirmed closed IDs. Reset of an unadmitted source
is a no-op; new media acquisition waits for a viewer.

`Register(bus, service)` installs `camera.diagnose.v1` and `camera.reconnect.v1` in
one owned go-admin registration set. The host retains/closes its returned handle
and uses synchronous `DispatchByNameWithOutcome` on that same bus. Payloads contain
only `camera_id` and optional/required `stream_id`; actor data comes from context.
Handlers implement `Run` and `Execute` with go-command typed result collectors.
Inspect `admin.CommandResultFailure(outcome.Result)` as well as dispatch errors:
a `DiagnoseResult` can preserve failed checks even when dispatch itself succeeds.
Unknown checks are not passing checks. No global bus, HTTP route, CLI, retry loop
or process supervisor is installed by this package.

`go test ./internal/viewer` exercises actual owned named dispatch and the public
coordinator with a controlled backend, including denial, aliases, cancellation,
cleanup and redaction. Use `go test -race ./config ./internal/viewer` for concurrency.
CGO is required by the selected go-admin dependency graph. Real Serve ingress,
CSRF, controller composition and transport validation remain T07.

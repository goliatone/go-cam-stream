# Controller and transports

Build the CLI/server with Go 1.27.4 and CGO enabled:

```sh
./scripts/build-web.sh # required on a fresh checkout before building the Go host
go build -o /tmp/camstream ./cmd/camstream
/tmp/camstream config validate --config config/config.example.yaml
/tmp/camstream list --origin https://camera-pi.example.ts.net
/tmp/camstream status --origin https://camera-pi.example.ts.net --camera room1
/tmp/camstream diagnose --origin https://camera-pi.example.ts.net --camera room1
/tmp/camstream reconnect --origin https://camera-pi.example.ts.net --camera room1 --stream default
```

Remote commands use TLS verification and the configured Serve HTTPS origin. Serve
supplies the login header; the client cannot select an identity, bypass TLS or
fall back to a local backend. Writes bootstrap package-managed CSRF tokens and
are never automatically retried. Results are JSON; errors are sanitized JSON on
stderr with nonzero exit status. Failed diagnostic checks retain their result on
stdout and also fail the command. Offline validation reads only startup config.

`camstream serve --config FILE` composes the default EC70 and RTSP strategies,
their Kasa and RTSP encoders, environment secret resolver, pinned go2rtc backend,
public coordinator, application policy,
owned command bus and minimal go-admin operator module. HTTP and admin actions
share the same service-backed commands. No database, global command dispatcher,
or account system is added. Embedded browser assets share the authenticated ingress. The host temporarily disables its bus while
go-admin initializes to avoid that pinned version's legacy global-registry start;
owned registrations remain committed and the bus is enabled before serving.

The installation root (`backend.runtime_dir`) must be private (0700). It holds
`controller.lock`, a persistent `installation.json`, and the backend's `workers/`
subtree. All controllers for the same installation must share this root. The lock
is acquired before binding HTTP; a conflicting controller/installation fails.
A verified empty boundary permits retirement of old `workers/generation-*`
files before readiness. Unknown files fail closed and remain untouched. Damaged
installation metadata requires operator repair after stopping the installation;
startup never adopts unverified processes or signals PIDs from files.

The default boundary supports a dedicated private Linux PID namespace directly
below Docker init/tini, or the shipped nondelegated cgroup-v2 camstream.service
with matching systemd policy/MainPID and no old descendants. Deployment must
fully stop the old boundary before restarting the same installation. Native
systemd crash/restart passes controlled Lima validation. Actual Serve HTTPS and
media pass from a separate Mac; restricted Tailnet grants and Compose remain T09 gates.
See [installation/rollback](../../docs/GUIDE_INSTALLATION.md). Unsupported or dirty boundaries keep
playback unready; the authenticated catalog/status/diagnostic server stays live.

HTTP binds only the configured loopback address and trusts only the raw loopback
peer. Local host processes are trusted. With Serve on the Linux host, container
packaging must use host networking (and a private PID namespace); ordinary bridge
port publication cannot reach or authenticate this listener. T09 must verify
that topology and tailnet restrictions on the separate bounded media port pool.
Both the exact external Host and any supplied Origin must match configuration.
Forwarded headers never establish peer trust. Missing, duplicate, malformed or
unknown Serve identities are denied. Unsafe requests require the principal-bound
CSRF header, including the minimal operator routes under `/admin/api/camera/`.

`/healthz` and `/readyz` are loopback-only GET probes; readiness stays false until
boundary cleanup, backend and service start succeed. `/api/health` requires an
allowed principal. Unknown API/admin/static paths return 404; the embedded SPA and
assets use the same authenticated ingress. API routes and payloads are documented
in [the contract](../../data/api/README.md).

Cancel `Controller.Run`'s context to stop admission and drain HTTP, sessions,
workers and owned command resources. A failed cleanup retains the installation
lock; `Close` can retry after Run returns. Do not call Close concurrently with
startup. A process supervisor still owns descendant cleanup on abrupt exit.

Validation uses real loopback HTTPS, package CSRF, owned commands and a synthetic
Serve equivalent. Linux tests additionally exercise pinned go2rtc, separate CLI
processes, worker faults and SIGKILL/restart with an external media peer. They do
not establish EC70 or Raspberry Pi acceptance. The opt-in `TestT09Tailnet` runs on
a separate enrolled client with `CAMSTREAM_T09_ORIGIN=https://HOST.ts.net`; set
`CAMSTREAM_T09_MEDIA=1`, `CAMSTREAM_T09_PROTOCOL=udp` (or `tcp`),
`CAMSTREAM_T09_CLIENT_IP` and `CAMSTREAM_T09_SERVER_IP` for four controlled viewers.
It uses real Serve identity and normal TLS verification. The controlled source
requires the disposable VM test service, never production credentials or a shipped
authentication bypass. See [T09 Serve evidence](../../.ctx/specs/camera-streaming/support/evidence/t09-serve.md).

## Reproducible controlled-media validation

From a checkout, run:

```sh
./scripts/controlled-media.sh --fresh --output /tmp/camstream-check
```

The output directory must not already exist. Omit `--output` for a new temporary
evidence directory; omit `--fresh` to allow Docker build-layer reuse. Requirements
are a local Docker daemon capable of running Linux arm64 containers, a POSIX shell,
`tar`, and network access to Docker Hub, Debian, GitHub, npm and Go module proxies.
Native arm64 Docker on Apple Silicon/Linux is the target; emulated execution is
not a performance baseline. No host Go, Node, FFmpeg, module caches, camera, Tailnet
enrollment or prebuilt fixture image is required. Initial dependency downloads and
compilation can take several minutes.

The runner snapshots repository build inputs, rebuilds embedded web assets,
verifies the pinned go2rtc and linter downloads, generates a small H.264 test clip,
runs config/app/CLI/viewer regressions plus Linux CGO vet/lint, and runs the real
`TestT05ProductionRTSP` API/media test. A skipped/missing live test fails the run.
Go/Node base images are digest-pinned; Go/npm dependencies use the repository
lockfiles. Debian FFmpeg/packages are resolved at build time and their versions
are recorded: this is reproducible test setup, not a byte-identical build claim.
The image resolves lint paths from the copied config directory instead of Git
metadata; both refer to the source root and the lint rules are unchanged.

Fixture origins and the default controller have separate PID namespaces; only
their isolated loopback network is shared. No host ports are published. Production
exec inputs stay disabled; only the separate synthetic origins run FFmpeg. The
smoke checks sharing, profile conflicts, final-close/reopen, source fault latching,
same-profile operator recovery after upstream repair, unrelated-camera survival,
authorization and safe responses. Synthetic Kasa checks auth routing/failure only.
This command does not run a desktop browser, native installation, real Serve or
hardware validation; those evidence boundaries remain separate.

`result.txt` records PASS/FAIL, exit status and verified cleanup. Build/API logs,
tool versions, clip/binary digests, image identity and run ownership remain in the
evidence directory. Success, test/build failure and HUP/INT/TERM remove only the
run's labeled containers, image tag and scratch directory. Shared base images and
Docker build caches remain; the runner never prunes other resources. If Docker is
unavailable during cleanup, the run fails and retains scratch plus exact resource
names in `run.txt` for operator cleanup. SIGKILL or a daemon/host crash cannot run
shell traps; use those names to inspect/remove this run's resources afterward.

### Manual CI validation

[Controlled media](../../.github/workflows/controlled-media.yml) runs the same
fresh command on GitHub Actions `ubuntu-24.04-arm`. It is manual-only: no push/PR
trigger, deployment, camera credentials or Tailnet enrollment. The workflow uses
commit-pinned actions, a read-only repository token and checkout with credentials
not persisted. Concurrent runs on the same ref are serialized without cancelling
the active run.

After reviewing and committing the application sources and workflow, push them to
the configured GitHub repository. The workflow must exist on its default branch
for [manual dispatch](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow).
Enable Actions and ensure repository policy permits the pinned actions and
[`ubuntu-24.04-arm`](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).
Choose **Actions → Controlled media → Run workflow** and select the desired ref.
No self-hosted runner or native Lima service is used.

Validation has a 25-minute GNU timeout with a 30-second termination grace; the
40-minute job budget leaves time for result checks and artifact upload. Normal
success/failure/TERM uses the runner's ownership-scoped cleanup. The job requires
`result=PASS`, `exit_status=0` and `cleanup=true`; missing evidence fails closed.
The `controlled-media-<run-id>-<attempt>` artifact retains environment/commit,
build/API/runner logs, versions and cleanup results for seven days, including
failed runs. Hard cancellation, runner loss or job timeout can prevent final
checks/upload; those outcomes do not count as a pass. GitHub disposes of the
hosted VM, and no cleanup of unrelated Docker resources is attempted.

Local workflow validation uses `actionlint .github/workflows/controlled-media.yml`
(with ShellCheck on PATH) plus the runner command above. Hosted checkout, runner
capacity and artifact upload require a first dispatched run; local checks cannot
establish those results. Automatic PR gating is a separate follow-up after that
run is reviewed.

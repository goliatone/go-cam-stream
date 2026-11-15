# EC70 strategy and go2rtc encoder

`ec70.Strategy{}` implements the public strategy contract without I/O. Give each
catalog alias its own `Camera.ID` and all aliases of one device the same stable
`Camera.PhysicalID`. Use an **ec70.Config value**, a bare DNS name/IP, optional port
(default 19443), and two `go-secrets.Reference` values for the TP-Link username and
original password. URLs, embedded ports, missing identities/references and unknown
profiles fail validation. Only the `default` profile is supported.

Resolve normalizes DNS case/trailing dot and IP spelling. Its opaque SHA256 source
key includes physical identity, endpoint, profile, transport and complete reference
bindings; it excludes alias names/IDs and secret values. Two equivalent aliases
share a source. Different reference bindings remain distinct even if they currently
resolve to identical credentials. DNS aliases are not resolved or deduplicated.
The declared one-source device limit is enforced by the coordinating service (T05).
H.264 video and PCMU audio are expected protocol tracks, not verified EC70 features.

The optional pair-specific adapter is
`github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/kasa`:

```go
provider := environment.New(environment.Config{}) // go-secrets/providers/environment
encoder := kasa.NewEncoder(provider)
backend, err := go2rtc.New(backendConfig, encoder)
```

[The executable example](../../backends/go2rtc/kasa/example_test.go) shows typed
references and strategy resolution. The generic backend does not import EC70; the
strategy does not import go2rtc. Other pairs can supply their own typed encoders.
There is no global registration or implicit provider discovery.

The encoder checks the concrete source type, resolves credentials with the caller's
context, and destroys caller-owned `go-secrets.Value` buffers on success/failure.
Resolvers must return fresh owned values and honor context. A go-secrets provider
already satisfies the small interface; hosts using several providers can inject
scheme routing. Constructors and strategy methods never resolve credentials.
The encoder performs no camera/media I/O.

For pinned go2rtc 1.9.15, the private input is a `kasa` URL at
`/https/stream/mixed`. `net/url` escapes userinfo; the original password is encoded
with standard base64 first. Do not pre-encode either credential. Usernames with
colon, quote, backslash or control characters are rejected because the pinned
Basic/Digest path cannot safely represent them. Empty, expired, unavailable or
oversized credentials fail closed. Provider errors are discarded rather than
retained as potentially sensitive causes; they are not camera authentication
evidence. Cancellation remains identifiable through `errors.Is`.

The pinned Kasa implementation uses HTTPS with certificate verification disabled
(`httpx` internally), including for DNS hosts. This adapter preserves that stock
behavior; it does not claim authenticated camera TLS. Use a trusted camera network.
Encoded URLs/base64 passwords are credentials: keep them out of logs, catalog
responses and diagnostics. The backend retains its private input in memory and
0600 worker config; Go string copies cannot be reliably zeroed.

Credential rotation preserves source identity. T03 rejects a changed encoded
definition under an existing key; stop/remove the old source with confirmed cleanup
before re-registering it, or restart the controller cleanly. ResetSource alone does
not replace credentials. Live configuration editing/rotation orchestration remains
outside this slice.

## Validation

Run `go test ./pkg/camstream/strategies/ec70 ./pkg/camstream/backends/go2rtc/kasa`.
Tests cover profiles, normalization, alias identity, binding changes, concrete
types, reserved credential characters, cleanup/cancellation and T03 reconciliation.

The opt-in Linux arm64 `TestPinnedKasaAuthentication` uses the real encoder,
backend and pinned executable against a local synthetic HTTPS camera. It checks
the request path and Basic/Digest credentials with special characters; the fixture
returns no media. Run its compiled test binary directly under a Docker `--init`
boundary with `CAMSTREAM_KASA_INTEGRATION=1`, the pinned executable mounted at
`/usr/local/bin/go2rtc`, and networking disabled (loopback only).

EC70 hardware revision/firmware, actual media/audio, account behavior and upstream
connection limits remain unverified until T08/T10. Synthetic auth success is not
hardware compatibility or playback acceptance.

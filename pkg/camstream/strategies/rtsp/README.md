# RTSP strategy

`rtsp.Strategy{}` describes explicitly configured RTSP video profiles without
network access, secret resolution, worker processes or a backend import. Pass a
`rtsp.Config` value as `camstream.Camera.Config`; pointer configs are rejected.
See the [executable example](example_test.go).

Each camera needs stable camera/physical IDs, a bare DNS name or IP, an optional
port (Go zero value means 554), and 1–32 profiles. Profile IDs must be unique.
Names default to their IDs. Video is always expected; `Audio: true` opts into
expected audio tracks. Neither capability proves actual codecs/browser support.

Set both `UsernameRef` and `PasswordRef` to parsed `go-secrets.Reference` values,
or leave both zero for anonymous sources. This package validates reference syntax
without checking provider availability or reading values. The default host accepts
only paired environment references. The [go2rtc encoder](../../backends/go2rtc/rtsp/README.md)
resolves credentials and enforces the complete encoded-URL size limit. The host
decoder distinguishes an omitted port from explicitly invalid zero; the reusable
Go config uses zero as a default.

`Config.Normalize()` returns a validated copy with independent profile storage,
canonical host/IP spelling, effective port and default names. `Validate()` performs
the same checks without returning the copy. `Endpoint()` returns the confidential
canonical host:port for catalog device-identity checks; it must not be logged.
DNS/IP aliases are not resolved. Unspecified/multicast IPs and IPv6 zones are rejected.

Paths are decoded absolute strings, up to 2,048 bytes, with no control characters
or raw `?`/`#`. Repeated slashes and dot segments are retained. Spaces/Unicode are
valid decoded path content; an encoder must escape them once with `net/url`.
A percent sign is literal: `/cam%20one` means that exact path text and will be
encoded as `/cam%2520one`; use `/cam one` for a camera path containing a space.

Queries are already escaped raw query strings, up to 4,096 bytes. Ordering,
duplicate keys, `+` and percent-escape spelling are preserved. Use percent escapes
for spaces/Unicode and reserved values. Malformed escapes, raw fragments,
unescaped semicolons, non-query characters, invalid UTF-8 and decoded controls
are rejected. Do not put credentials/tokens in paths or queries: secret-bearing
URL authentication is outside this provider's contract. It does not guess secret
fields by their names. IDs/names are bounded to 256 bytes with valid UTF-8, no
control characters or surrounding whitespace.

`Resolve` returns an immutable `Source`. `Source.Config()` returns a defensive
copy containing only the selected profile. Names, camera alias IDs and profile
IDs do not affect source identity. Physical ID, normalized host/port, path, exact
query, fixed TCP/video selection, audio flag and full reference URIs do. Equivalent
profiles share a source; different reference bindings remain distinct even when
their resolved values happen to match. Keys contain a versioned SHA256 fingerprint,
never raw endpoints or secret references. Changing an environment value does not
change the key. Controlled restart/cleanup is needed for credential rotation.

The source declares one distinct active source per physical device; enforcement
belongs to the existing coordinator. Two viewers/aliases may share one source;
different active profiles compete for that budget. One host/port is one device
under the current catalog policy, including a multi-channel bridge/NVR endpoint.

Confidential config/profile fields are excluded from JSON and standard formatting
is redacted. Sources expose no JSON fields. These values are trusted integration
objects, not public DTOs; explicit accessors still return private information.
Validation errors have fixed classified messages without rejected input/causes.

Run `go test ./pkg/camstream/strategies/rtsp ./pkg/camstream/strategies/ec70`.
Tests cover validation, normalization, identity, defensive copies, cancellation,
privacy and the public strategy example. They perform no camera/media smoke.
The [RTSP encoder](../../backends/go2rtc/rtsp/README.md) has passed controlled pinned-backend
ingestion checks (T03). The default application/CLI register RTSP alongside Kasa. See
[mixed setup](../../../../config/README.md#mixed-setup) and the
[Pi bridge/IP camera guide](../../../../docs/GUIDE_INSTALLATION.md#add-an-rtsp-camera-or-raspberry-pi-bridge).
Controlled host/player checks and actual mixed-camera hardware compatibility
remain separate evidence; no universal device support is implied.

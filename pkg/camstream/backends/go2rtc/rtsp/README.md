# RTSP source encoder

This adapter connects the [generic RTSP strategy](../../../strategies/rtsp/README.md)
to stock go2rtc v1.9.15. It performs no dialing or process creation. Construct it
with `NewEncoder(resolver)` and inject it alongside the Kasa encoder into
`go2rtc.New(backendConfig, encoders...)`. A go-secrets environment provider satisfies
the resolver interface. `NewEncoder(nil)` supports anonymous sources only.
The [executable example](example_test.go) resolves and encodes an anonymous profile.

Only a concrete `strategies/rtsp.Source` with one valid selected profile is accepted.
The encoder preserves its raw query and escapes its decoded path/userinfo with
`net/url`. Ingest uses the pinned client's default TCP transport. Provider-owned
`#backchannel=0` always disables talkback negotiation; video-only profiles also
get `#media=video`. User-supplied go2rtc expressions/options are not accepted.

Authentication resolves both references at this trusted boundary. Resolvers must
honor cancellation and transfer fresh owned `go-secrets.Value` instances; all
returned Values are destroyed on success/failure, including value-plus-error and
cancellation cases. Nil, empty, expired or failed values produce sanitized
`source_unavailable`; missing resolver/invalid source inputs produce
`invalid_argument`. Errors retain no resolver text or cause. Cancellation remains
identifiable through `errors.Is`.

The pinned RTSP implementation supports Basic and legacy MD5 Digest without qop;
these are the exercised fixture modes, not a promise for every camera auth variant.
Usernames containing colon, quote, backslash or controls are rejected because Basic
uses colon as a delimiter and pinned Digest interpolates a quoted username without
escaping. Passwords are not base64-transformed by this encoder: RTSP authentication
uses their original bytes. URL encoding protects reserved characters.

The final encoded input must be at most 16,384 bytes, including escaped credentials
and backend options. Encoded URLs remain credentials even after owned secret Values
are destroyed; Go strings cannot be reliably zeroed. Never log or expose Inputs,
source accessors or worker configurations to clients. Existing backend protections
keep credential-bearing configuration private. Ordinary RTSP is not encrypted;
use a trusted camera network. RTSPS policy is outside this slice.

Changing secret values under an existing source key does not silently replace its
registered backend definition. Confirm removal/cleanup before re-registration, or
restart the controller cleanly. ResetSource alone is not credential rotation.

## Validation

```sh
go test -race ./pkg/camstream/backends/go2rtc/rtsp ./pkg/camstream/backends/go2rtc/kasa
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/rtsp.test ./pkg/camstream/backends/go2rtc/rtsp
```

Live tests require the existing `camstream-t15-feasibility:1.9.15` fixture image
(synthetic H.264/Opus clip and FFmpeg), verified pinned Linux arm64 executable and
a dedicated init container. Use absolute host paths for both mounts:

```sh
docker run --rm --init --network none --cpus 2 --memory 768m \
  -e CAMSTREAM_RTSP_INTEGRATION=1 \
  -v /absolute/rtsp.test:/test/rtsp.test:ro \
  -v /absolute/go2rtc-linux-arm64:/usr/local/bin/go2rtc:ro \
  --entrypoint /test/rtsp.test camstream-t15-feasibility:1.9.15 \
  -test.v -test.timeout 75s
```

Run the binary directly as the container command so the real `ContainerBoundary`
check applies. No ports or external network are needed. The controlled origin uses
fixture-only FFmpeg with codec copy; the production backend has exec inputs disabled.
An authenticating RTSP proxy checks exact camera paths/queries, Basic/Digest
credentials, TCP SETUP and absent backchannel requests, then forwards real media.
An independent Pion receiver counts nonempty H.264/Opus RTP from the production
backend; the video-only case confirms no audio delivery. Wrong-password testing
must reject access without media or credential leakage. Fixtures clean up their
owned processes/connections, and `--rm` removes the container.

These checks establish controlled ingestion, not actual camera compatibility or
browser playback. The default server/CLI now register this encoder alongside Kasa;
see [mixed setup](../../../../../config/README.md#mixed-setup). Mixed-catalog
host/player results are recorded separately in
[T05 evidence](../../../../../.ctx/specs/generic-rtsp/support/evidence/controlled-rtsp.md).
Actual RTSP/Kasa devices remain T06 and the existing hardware gates.

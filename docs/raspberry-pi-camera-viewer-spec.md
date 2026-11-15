# Camera Streaming Library and Raspberry Pi Viewer

Historical planning baseline, retained for provenance. Status, task ordering and
configuration examples below describe that baseline, not the current application.
Use [the installation guide](GUIDE_INSTALLATION.md), [configuration reference](../config/README.md)
and canonical `.ctx` documents for current behavior and validation state.

**Technical Requirements, Architecture, and End-to-End Delivery Plan**

Version: 2.2  
Date: November 13, 2026  
Status: Managed-process go2rtc approach selected; new feasibility pass pending; implementation and EC70 compatibility unverified

---

## 1. Executive Summary

Build a camera-agnostic Go streaming library and an installable reference application for secure, low-latency browser viewing. The reference installation runs on a Raspberry Pi and supports multiple cameras, including room1, room2, livingroom, hall and front-porch.

Deliver two consumption paths from the existing root Go module:

- A public library that external Go applications can configure and use without starting this application's server or importing its internals.
- A `camstream` CLI/server binary with embedded Preact assets, native Linux service installation and Docker Compose deployment.

Camera strategies and streaming backends are independently pluggable through small Go interfaces and explicit constructor injection. Ship an EC70 strategy and select go2rtc as the reference application's default backend. Alternate implementations are supplied through ordinary Go packages; dynamic plugins and live backend migration are outside MVP.

The initial stack remains Raspberry Pi OS Lite, Go, Preact/TypeScript/Vite, WebRTC and private Tailscale connectivity. Reuse appropriate Goliatone packages for application hosting, typed commands, configuration, secrets, errors, logging and lifecycle. The default adapter launches an unmodified pinned go2rtc executable as owned child processes: one shared ingest per active source and one playback worker per viewer. An installable application binary does not imply an embedded complete media stack. No upstream changes, patches or forks are a delivery dependency. A production Pion implementation remains outside this approach.

**Separate acceptance gates:** library consumption, controlled-source backend integration, exact EC70 compatibility, and installed application operation. EC70 V4 compatibility remains unverified and blocks acceptance of the EC70 deployment, while generic library and controlled-source work can proceed independently.

This proposal records context and delivery direction. Canonical execution documents are [requirements](../.ctx/specs/camera-streaming/REQUIREMENTS.md), [design](../.ctx/specs/camera-streaming/DESIGN.md), and [tasks](../.ctx/specs/camera-streaming/TASKS.md). This revision authorizes planning only; it does not start implementation or record product acceptance.

## 2. Project Context

### 2.1 Existing environment

The reference installation contains multiple cameras at the owner's residence in a different US state from the primary viewer. Logical IDs include room1, room2, livingroom, hall and front-porch. Record the actual model, hardware revision and firmware per device; the initial compatibility target below is EC70 V4.0.

| Attribute | Value |
|---|---|
| Manufacturer | TP-Link |
| Product | Kasa Spot Pan Tilt |
| Model | EC70 |
| Hardware version | 4.0 |
| Resolution | 1080p |
| Connectivity | Wi-Fi |
| Official RTSP | Not supported |
| Official ONVIF | Not supported |
| Proprietary local streaming | Potentially available; unverified for V4 |

The owner currently accesses the camera through the Kasa/Tapo ecosystem.

The remote viewer has:

- Their own TP-Link account.
- Device access through Tapo's sharing/invitation functionality.
- A MacBook Pro with Apple Silicon.
- An iPhone.
- Experience developing applications with Go, TypeScript, Preact, Docker, and networking technologies.

The shared Tapo account has working remote camera access. However, this does **not** establish that the account can authenticate to the camera's local proprietary streaming protocol.

### 2.2 Existing problems

The official mobile application presents several limitations:

1. No satisfactory native Mac desktop experience.
2. No system-level picture-in-picture for live camera viewing on iPhone.
3. Viewing video prevents comfortable use of other apps on the same phone.
4. Limited ability to customize the interface.
5. Dependence on proprietary remote-stream viewing behavior.

The Raspberry Pi application provides the viewing experience while preserving the existing TP-Link provisioning/device-management ecosystem. The public library is reusable by other hosts and is not tied to the Raspberry Pi or Tailscale.

### 2.3 Deployment location

A Raspberry Pi will be installed at the camera owner's residence.

The Raspberry Pi and camera will be connected to the same local network, or to network segments with explicitly permitted camera connectivity.

The remote viewer will connect to the Raspberry Pi over Tailscale.

The camera must not be exposed directly to the public internet.

## 3. Objectives

### 3.1 Primary requirements

| ID | Requirement | Priority |
|---|---|---|
| R1 | View EC70 live stream remotely from a Mac browser | Must |
| R2 | View the same stream from an iPhone browser | Must |
| R3 | Keep the stream playing while other Mac apps are used | Must |
| R4 | Support iPhone system PiP when technically possible | Must attempt |
| R5 | Avoid public camera port forwarding | Must |
| R6 | Automatic recovery following network interruptions | Must |
| R7 | Automatic restart after Raspberry Pi reboot | Must |
| R8 | Low-latency video over WebRTC | Must |
| R9 | Responsive mobile and desktop interface | Must |
| R10 | Protect camera and account credentials | Must |
| R11 | Avoid unnecessary transcoding | Should |
| R12 | Support multiple viewers sharing an upstream stream with isolated viewer lifecycle | Must |
| R13 | Show connection state and diagnostic errors | Should |
| R14 | Support multiple configured cameras and streams in MVP | Must |
| R15 | Export an independently usable Go streaming library | Must |
| R16 | Deliver an installable CLI/server with embedded browser assets | Must |
| R17 | Inject camera strategies; ship an EC70 implementation | Must |
| R18 | Inject streaming backends; select go2rtc by default in host wiring | Must |
| R19 | Reuse Goliatone packages with service-backed typed application commands | Must |
| R20 | Isolate camera failures, shared upstream resources and viewer sessions | Must |

The R identifiers above summarize the proposal; canonical requirement IDs and acceptance criteria are maintained in `.ctx/specs/camera-streaming/REQUIREMENTS.md`.

### 3.2 Target performance

Initial engineering targets, subject to measurement:

- Local stream startup: under 3 seconds.
- Remote stream startup: under 5 seconds.
- Typical remote glass-to-glass latency: under 2 seconds.
- Video resolution: up to the camera's supported 1080p.
- Stream available after Raspberry Pi restart without manual intervention.
- Continuous playback for at least 60 minutes without an application-imposed timeout.
- Two simultaneous viewers sharing one upstream, plus two distinct camera streams playing concurrently in separate pages/devices.
- Measure aggregate resource use with the configured camera set and enforce explicit session limits; do not assume unlimited concurrent capacity.

These are acceptance targets, not guaranteed characteristics of the camera.

### 3.3 Non-goals for MVP

- Continuous video recording.
- Cloud storage.
- AI-powered object detection.
- Motion-event notifications.
- Camera firmware management.
- Replacing Kasa/Tapo device provisioning.
- Two-way voice communication.
- Pan/tilt control.
- Native iOS or macOS GUI applications; the installable app is a CLI/server plus browser UI.
- A production second backend, runtime Go plugins, live backend swapping or per-camera backend routing.
- Simultaneous multi-tile dashboard UI, automatic camera discovery and runtime configuration editing/reload.
- Database, CMS or queue infrastructure without a concrete need.
- Upstream go2rtc changes, patches/forks, importing internal orchestration or building a custom media pipeline to unblock delivery.
- Public, password-based internet access and account-management UI.
- MSE/HLS fallback, custom idle-grace timers, always-on/preload and shared-backend multi-controller operation.

Pan/tilt controls, recording, notifications, and Apple Home integration may be considered after MVP completion.

## 4. Technical Feasibility and Risks

### 4.1 Proprietary Kasa streaming protocol

Many older Kasa cameras expose an undocumented HTTPS streaming endpoint on TCP port 19443.

The documented URL pattern is:

```text
https://CAMERA_IP:19443/https/stream/mixed
```

On supported models, it returns multiplexed H.264 video and G.711 audio.

The go2rtc project implements a `kasa://` source that handles the proprietary stream.

Example:

```yaml
streams:
  camera1: "kasa://EMAIL:PASSWORD@CAMERA_IP:19443/https/stream/mixed"
```

This is a conceptual example. The credential fields require particular encoding:

- Email: URL-encoded.
- Password: Base64-encoded.
- The resulting URL is sensitive, even though the password is encoded.

The project documentation explicitly lists these models as tested:

- KD110
- KC200
- KC401
- KC420WS
- EC71

**EC70 V4.0 is not explicitly listed.**

This creates a hardware acceptance gate for the EC70 strategy and reference installation, not for independent library development.

Reference: https://github.com/AlexxIT/go2rtc/blob/master/internal/kasa/README.md

### 4.2 Alternative protocol implementation

A separate Home Assistant integration documents the KC100 local camera protocol in more detail.

It identifies:

- Port 19443: media streaming.
- Port 10443: control API.
- HTTPS transport.
- Proprietary multiplexed H.264/G.711 payload.
- Nonstandard authentication behavior.

It also documents a possible single-stream-client limitation on KC100 hardware.

These characteristics must not be assumed to apply identically to EC70 V4.

Reference: https://github.com/jaysoffian/ha-kc100

### 4.3 Credentials

The existing Tapo sharing arrangement may not provide the local streaming permissions needed by go2rtc.

Evaluate:

1. Whether the invited account authenticates successfully.
2. Whether the owner's original Kasa account is required.
3. Whether account credentials are validated locally.
4. Whether firmware updates have altered authentication.
5. Whether authentication can use restricted or camera-specific credentials.

Prefer a dedicated account with minimal necessary permissions where technically supported.

Do not store account credentials in the Git repository.

Do not log authentication headers, stream URLs containing credentials, or tokens.

### 4.4 Unsupported camera outcome

If the EC70 V4 lacks an accessible, usable local media stream, EC70 deployment acceptance is blocked. Keep its compatibility status explicit; the generic library and backend can still be validated with controlled sources or other supported cameras.

Do not proceed by assuming that Tapo's cloud streaming API is equivalent to a local streaming endpoint.

Fallback decisions:

1. Investigate other documented local EC70 protocol implementations.
2. Evaluate Homebridge Kasa camera compatibility for this exact hardware revision.
3. Evaluate a narrowly scoped, permissioned compatibility adapter if the protocol can be understood safely.
4. Consider replacing the camera with a model providing documented RTSP/ONVIF support.

The EC70 should not be reverse-engineered through invasive firmware modification as part of the MVP.

## 5. Proposed Architecture

### 5.1 Composition and media paths

```text
Preact / CLI HTTPS client / operator action
        -> Tailscale Serve -> server auth -> typed command -> application service
                                  -> public streaming service
                                       -> injected camera strategy
                                       -> injected streaming backend

Browser -> authorized playback adapter -> streaming service/backend
Browser <- WebRTC media over reachable private network <- backend <- camera LAN
```

Read-only metadata/status uses focused read services. Media packets and ongoing signaling do not traverse the command bus. EC70 and go2rtc are default implementations chosen by the host, not hard-coded branches in the core.

### 5.2 Ownership

| Component | Responsibility |
| --- | --- |
| Public library | Camera/stream/session identity, strategy/backend coordination, lifecycle, concurrency, cancellation and classified errors |
| Camera strategy | Validate device-specific configuration, select supported profiles, describe sources and model constraints |
| Backend adapter | Reconcile sources, negotiate playback, inspect health and own cleanup; default go2rtc adapter privately supervises ingest/playback workers |
| Application service | Configured access, operator policy and thin diagnose/reconnect delegation; no duplicate lifecycle engine |
| Typed command | Stable request identity, shape validation, service delegation and typed result publication |
| HTTP/CLI/admin adapter | Decode input, establish trusted authority, dispatch and render |
| Composition root | Configuration, secrets, logging, concrete implementations, registration, routes and process lifecycle |
| Preact frontend | Camera/stream selection, one active player, viewer recovery and supported media controls |
| Tailscale/HTTPS ingress | Private remote connectivity and restricted entrypoint; application permissions remain explicit |

### 5.3 Idiomatic Go extension contracts

Use small interfaces, concrete constructors and explicit dependency injection. Illustrative backend/service shape:

```go
type Backend interface {
    EnsureStream(context.Context, StreamSpec) (StreamState, error)
    RemoveStream(context.Context, StreamID) error
    StreamStatus(context.Context, StreamID) (StreamState, error)
}

func NewService(backend Backend, opts ...Option) (*Service, error)
```

`EnsureStream` reconciles source definitions; it does not create a connection per viewer. `RemoveStream` removes a definition only after consumers are confirmed closed; source deletion is not viewer termination. Keep backend-native demand, source configuration and viewer control separate. Public signatures contain no go2rtc, Pion, Fiber or go-admin objects.

**T15 precedes T02:** the original v1.9.15 external API probe failed viewer close/cancellation/retry requirements. Its [recorded evidence](../.ctx/specs/camera-streaming/support/evidence/backend-contract.md) remains valid. The replacement plan uses process ownership rather than missing upstream APIs. T15 must now prove that stock go2rtc workers can satisfy those same outcomes before public interfaces are published. No upstream update, patch or release is a prerequisite, and the new topology is not yet tested.

After that evidence, T02 publishes backend-neutral negotiation, observation, close and lifecycle capabilities. An application SessionID maps to an adapter-private worker handle. PIDs, executable options, internal RTSP addresses and port allocation do not enter core domain types, commands or frontend contracts. Alternate implementations can replace this adapter; deployment/composition settings may differ.

The host constructs `go2rtc.New(...)`, injects it into `camstream.NewService(...)`, and registers `ec70.New(...)` explicitly. Constructors neither dial nor spawn. Explicit backend startup/cleanup owns children through ordinary Go context, `os/exec`, synchronized registries and bounded wait/termination. No global registries, reflection plugins, process-name kills, Docker API controller or second command bus. One injected backend object may own multiple processes.

Providers own typed configuration and source implementations through an exported, unsealed `Source` interface such as `Kind() string`. The core treats provider payloads as opaque and consumes safe identity/constraints separately. Host-injected decoders and backend-injected encoders use provider-owned concrete types; default wiring supplies EC70/Kasa. An external package must add its own source, config and encoder or matching backend without editing a central vendor union or core package. Reject unknown kinds, duplicate registrations and mismatched concrete types. New protocols may still need backend ingestion support; camera-agnostic does not imply universal protocol support.

### 5.4 Camera, stream and viewer identity

| Resource | Meaning | Example |
| --- | --- | --- |
| Camera | Public ID/display name, strategy/configuration and secret binding | `livingroom` |
| Physical camera | Operator-declared device identity shared by all aliases | `device-livingroom` |
| Source | Canonical device/profile/transport/credential-binding key; excludes secret values | Same profile aliases share one key |
| Stream | Supported output/profile belonging to a camera | `livingroom/main` |
| Viewer session | One authorized playback connection to a stream | Opaque generated session ID |

Camera IDs are unique; stream IDs are unique within their camera. Display names may change without changing identity. Backend resource names are collision-safe and owner-namespaced. Public responses do not disclose backend handles, addresses, secret references or privileged source URLs.

Declare the same physical camera ID for all aliases and reject obvious conflicting duplicate endpoints. MVP admits one distinct active source per physical camera; aliases share that source. A conflicting profile returns `camera_busy` without evicting viewers. Per-stream/total viewer limits remain separate.

A camera can expose multiple streams only when its strategy supports those profiles. EC70 starts with the validated default source; no substream or quality-selection capability is presumed. Multiple viewers can share each stream; multiple cameras can play concurrently in separate pages/devices. A grid UI remains a future enhancement.

### 5.5 Isolated go2rtc implementation

```text
camera -> shared ingest go2rtc worker
               | private RTSP/TCP, codec passthrough
               +-> playback worker A -> WebRTC -> viewer A
               +-> playback worker B -> WebRTC -> viewer B
```

Use one ingest worker per active SourceKey and one playback worker per session. Aliases share the ingest; playback workers never connect directly to cameras. Terminating worker A closes only A. Final-viewer cleanup stops/reaps the ingest before freeing the physical-camera slot. A single process per camera cannot isolate viewer termination; direct camera ingestion per viewer would multiply camera connections.

All process/config/port mechanics stay in `pkg/camstream/backends/go2rtc`. Prefer one complete HTTP SDP exchange per playback worker; duplicate negotiation cannot create extra peers in it. Do not expose a generic go2rtc management/signaling proxy. Use immutable private worker config, process handles and observed media state. Never use source DELETE or HTTP signaling closure as proof of cleanup. A live PID alone is not a live stream. Failure/cleanup uncertainty retains reservations and reports a safe error.

The cost is C+V go2rtc processes for C active sources and V viewers, plus any explicitly enabled helpers. Measure memory, CPU, startup, passthrough and media ports on representative Linux arm64 before adopting defaults. Apply strict viewer/process/port limits; do not hide resource exhaustion by evicting other viewers. The core service and frontend remain independent of that cost model.

## 6. Raspberry Pi Hardware and OS

### 6.1 Recommended equipment

- Raspberry Pi 5, 4 GB RAM or better.
- Official-quality power supply.
- High-endurance microSD card, 32 GB minimum, or SSD.
- Ethernet preferred.
- Appropriate case and cooling.
- Optional UPS if unattended reliability becomes important.

A Raspberry Pi 4 may also be sufficient for a single camera with direct H.264 forwarding.

Avoid making a transcoding-heavy design dependent on low-end hardware.

### 6.2 Operating system

Use Raspberry Pi OS Lite, 64-bit.

Install:

- Native app/go2rtc executables with a dedicated systemd control-group service; alternatively Docker/Compose with both executables and owned workers inside one app container.
- Tailscale.
- Git.
- Basic system administration utilities.

Configure:

- SSH key authentication.
- A non-root administrative user.
- Automatic security updates where appropriate.
- Correct time synchronization.
- Persistent startup through the selected systemd/Compose deployment.
- A stable network connection.
- DHCP reservations or stable addressing for each camera.

Keep camera and Pi IP addresses configurable.

### 6.3 Deployment constraints

- The Pi must be able to reach the camera's LAN address.
- Port 19443 must be reachable if that protocol is used.
- The Pi must have outbound internet connectivity for Tailscale.
- No inbound WAN port forwarding is required.
- Avoid privileged containers unless justified.

## 7. Remote Networking

### 7.1 Recommended: Tailscale

Install Tailscale on:

- Raspberry Pi.
- Mac.
- iPhone.

The Raspberry Pi joins a private tailnet under an approved administrative identity.

The other devices are granted limited access to the Pi's streaming services.

Use access-control policies to restrict unnecessary reachability.

Do not make the entire home LAN accessible unless it is necessary.

### 7.2 HTTP and WebRTC separation

WebRTC uses separate signaling and media connections.

This matters because successfully loading the web application over HTTPS does not guarantee that the browser can establish the WebRTC media path.

The managed backend needs private API/RTSP listeners for its workers and a bounded WebRTC media port pool. Assign one numeric port per playback worker for its TCP/UDP pair; expose only the configured range through tailnet policy. Keep API/RTSP ports private. Example port ranges are deployment choices, not validated capacity.

Reserve ports atomically and release only after worker cleanup. Detect bind conflicts and fail boundedly; report exhausted capacity without interrupting existing viewers. Advertise the assigned tailnet-reachable address/port in ICE candidates. Never assume a single shared 8555 listener or advertise only inaccessible container bridge addresses. Host-networked Compose uses the bounded TCP/UDP media pool directly; restrict it through the approved host/tailnet policy. T15 verifies container feasibility; native systemd validation remains a mandatory T09 gate.

### 7.3 HTTPS and identity

Use Tailscale Serve HTTPS for browser and CLI, forwarding to a loopback-only app listener. On Linux, Compose uses host networking with a private PID namespace so host Serve reaches the same loopback-only listener. Ordinary bridge publication does not satisfy the listener/trusted-peer boundary. T09 verifies this topology and tailnet-only media access. No alternate LAN/tailnet app ingress or Funnel. Backend management remains private; WebRTC media has its own permitted tailnet path.

Trust Serve's decoded exact `Tailscale-User-Login` only within this deployment boundary. A static allowlist assigns camera/stream viewing and operator scopes. Reject missing, malformed, duplicate or unknown identity, including tagged-device requests without user identity. Display names and mere tailnet reachability do not grant permission. Local host administrators are trusted. Details and source evidence are in the canonical design.

Use supported go-admin authentication/authorization seams and package-managed CSRF for both browser and CLI writes. An authenticated token/cookie bootstrap and CLI cookie jar avoid a separate bypass or login/account database. T02 verifies the actual public middleware APIs.

### 7.4 Required TCP media path

MVP uses WebRTC over UDP normally and must also demonstrate actual WebRTC/TCP media with UDP media blocked while tailnet connectivity remains available. Check ICE candidates, firewall/media ports and the selected candidate pair on target Mac browsers and iPhone Safari. HTTPS signaling alone is insufficient evidence.

MSE/HLS are deferred. An explanatory error does not satisfy the no-UDP acceptance case; a TCP failure is a blocker to resolve before claiming networking support.

## 8. Streaming Technology Evaluation

### 8.1 Primary candidate: go2rtc

Repository: https://github.com/AlexxIT/go2rtc

Strengths:

- Already implements a Kasa-specific streaming protocol.
- Supports WebRTC.
- Provides existing browser players.
- Supports several fallback streaming technologies.
- Written in Go.
- Designed for embedded and self-hosted camera streaming.

Weaknesses:

- EC70 V4 compatibility is unconfirmed.
- Proprietary Kasa protocol can break following firmware changes.
- WebRTC networking needs careful configuration.
- The embedded API is not intended to replace a complete application authorization layer.

**Recommendation:** Use the injected managed-process go2rtc adapter with stock pinned executables, shared ingest workers and isolated playback workers. Encapsulate supervision and relay/configuration details; do not wait for or patch upstream control APIs.

### 8.2 Alternative Go library: Pion WebRTC

Repository: https://github.com/pion/webrtc

Pion WebRTC is a Go implementation of the WebRTC stack.

It can be used to build a custom streaming server with:

- WebRTC peer connections.
- ICE negotiation.
- Video tracks.
- Custom signaling.
- Detailed lifecycle control.
- Integration with an existing Go server.

However, **Pion does not implement the proprietary Kasa camera protocol**.

To use Pion as a replacement for go2rtc, a separate component must:

1. Connect to the camera.
2. Authenticate.
3. Parse the proprietary media stream.
4. Extract video and audio.
5. Package media into suitable WebRTC tracks.
6. Handle timing and codec compatibility.
7. Serve multiple consumers.

This adds significant scope.

### 8.3 Existing Go Kasa implementation

Before implementing any custom protocol support, evaluate the Kasa-related source code inside go2rtc:

https://github.com/AlexxIT/go2rtc/tree/master/internal/kasa

This is existing Go code and a useful implementation reference, rather than a verified standalone public library suitable for direct import.

If required later, investigate a separately scoped reusable ingestion adapter with compatible licensing and a public contract. Do not make custom protocol parsing an implicit requirement of the initial EC70 strategy.

Do not import an `internal` Go package from an unrelated module as though it were a supported public API.

### 8.4 Comparison

| Criterion | go2rtc | Pion + custom ingest |
|---|---|---|
| Existing Kasa implementation | Yes | No |
| Browser WebRTC output | Yes | Yes |
| Setup complexity | Lower | Higher |
| Control over media pipeline | Moderate | High |
| Custom Go integration | Via API or source | Native library |
| MVP suitability | Preferred | Alternative |
| EC70 V4 compatibility | Unknown | Unknown; requires ingest |

### 8.5 Decision rule

Select go2rtc in default application wiring. Other Go consumers may inject a backend implementing the required capabilities. A second production implementation is deferred; prove substitutability with a test backend and external consumer.

Only select a full Pion implementation if the custom media pipeline is justified by clear requirements or compatibility findings.

## 9. Video Format and Playback Strategy

### 9.1 Preferred media path

```text
Kasa proprietary HTTPS
        ↓
go2rtc Kasa ingest worker
        ↓
Private RTSP/TCP to viewer playback worker
        ↓
H.264 video (passthrough where supported)
        ↓
WebRTC packetization
        ↓
Browser RTCPeerConnection
        ↓
HTMLVideoElement
```

Preserve the camera's H.264 stream when possible.

Avoid re-encoding unless codec incompatibility requires it.

Audio may require transcoding or omission depending on the source format and target browser support.

### 9.2 Playback scope

Use WebRTC with verified UDP and TCP media paths. MSE/HLS are future options, not MVP fallback promises. The independent T08 EC70 smoke uses a pinned standalone go2rtc and its built-in viewer before production strategy/backend code exists; it proves camera ingestion only. T15 separately proves backend lifecycle/control capabilities using controlled media.

### 9.3 Multiple cameras and viewers

Maintain a startup-configured catalog of independent cameras and supported streams. Atomically reserve capacity per physical camera/source before acquisition. Allow one distinct active source per device, sharing aliases and viewers; reject a different active profile as `camera_busy`. Validate actual camera limits and Tapo coexistence. Two independent cameras may stream concurrently; grid UI is deferred.

Create shared ingest/playback workers on demand; go2rtc owns native media transport within them. Defer custom idle-grace timers and always-on/preload. The public service owns admission and device budgets; the concrete adapter owns workers and failure bounds; the application service adds access policy only.

A negotiation reservation has a bounded deadline. An active viewer has no fixed viewing TTL or mandatory browser heartbeat: backend media observation establishes liveness, including PiP/background playback. On missing DELETE, reconcile observed closure. Stale observation yields `unknown`; retain capacity conservatively instead of assuming cleanup. Explicit close/cancellation terminates that viewer's exclusively owned worker and releases counts/ports only after confirmed process/transport cleanup. A timeout reports `cleanup_incomplete`. Stop/reap the ingest after final-consumer release before freeing the device source reservation.

Closing/retrying one viewer affects only that viewer. Operator upstream reconnect is separately authorized and reports affected aliases. One offline camera/ingest fault does not interrupt others. A playback-worker failure affects one viewer; controller failure affects the installation. Single-controller restart and cleanup rules appear in Section 12.5.

## 10. Picture-in-Picture Requirements

### 10.1 macOS

The web application should support:

- Video in a browser tab.
- Fullscreen.
- Browser-supported native PiP.
- A floating video window where the browser provides the capability.

A video element should use the browser's standard APIs where available.

### 10.2 iOS

The target is Safari on iPhone.

Implementation requirements:

- Use a normal HTML video element for playback.
- Apply `playsinline`.
- Implement native PiP entry where supported.
- Avoid unnecessary canvas-based rendering.
- Keep video playback initiated through an appropriate user interaction.
- Handle Safari's permission and media lifecycle behavior.

Browser-level PiP is not equivalent to the original Tapo app implementing PiP.

A custom web player may enable PiP, but it must be tested on the intended iPhone and iOS version.

**Do not promise that WebRTC video will continue playing in the background on every iOS release.**

The implementation is successful only if a real-device test demonstrates that video continues while another iPhone application is in the foreground.

If native Safari PiP cannot be made reliable, document that limitation rather than claiming support.

## 11. Frontend Application

### 11.1 Technology

- TypeScript.
- Preact.
- Vite.
- CSS with Tailwind optional.
- Minimal dependencies.
- No unnecessary frontend state framework.

### 11.2 Main screen

The MVP application should contain:

- Video player.
- Camera and supported-stream selector with stable IDs and display names.
- One active player per page; other pages/devices can select different cameras.
- Connected/disconnected state.
- Loading indicator.
- Reconnect button.
- Mute/unmute control if audio is available.
- Fullscreen button.
- Picture-in-picture button when supported.
- Error details accessible without exposing secrets.

### 11.3 Layout behavior

**Desktop**

- Video occupies the primary content area.
- Responsive aspect ratio.
- Minimal persistent UI.
- Mouse and keyboard controls.
- Native browser PiP where supported.

**Mobile**

- Video fills the available viewport width.
- Touch-friendly controls.
- Portrait and landscape layouts.
- No dependence on hover interactions.
- Respect safe-area insets.

### 11.4 Player state machine

```text
idle
  ↓
connecting
  ↓
playing
  ↓
reconnecting
  ↓
playing

Failure states:
- authentication_failed
- camera_unreachable
- stream_unavailable
- signaling_failed
- media_connection_failed
- unsupported_browser
- unknown_error
```

Reconnect with exponential backoff and jitter.

Differentiate retryable viewer failures from latched source failures, backend outages, denied access and capacity limits. Viewer retry recreates only that session; it cannot clear a source fault or invoke operator upstream reconnect. Switching streams releases the previous session. Show empty catalogs and individual offline cameras without disabling healthy cameras.

Provide manual retry after persistent failure.

### 11.5 Browser support

MVP browsers:

- Safari on current macOS.
- Chrome on macOS.
- Safari on iPhone.

Test actual iPhone behavior rather than relying only on desktop browser emulation.

## 12. Backend Application

### 12.1 Library and application boundaries

Use one root Go module. Put generic streaming services and contracts in `pkg/camstream`, concrete integrations in its strategy/backend subpackages, and application composition/policy in `internal/`. External callers use the library directly; they do not need the reference application's CLI, HTTP server, admin module or global configuration.

Use a minimal go-admin host, preserving Preact and the selected private deployment topology. Host routes, operator modules, static assets and SPA history fallback have distinct ownership. Enable only required features; avoid CMS, generic CRUD or database scaffolding. Start with a validated startup-config catalog and transient session state. Any later repository owns persistence mechanics only.

### 12.2 Shared services and commands

| Operation | Path |
| --- | --- |
| List cameras/streams and status | Thin adapter -> focused read service |
| Diagnose a configured camera | CLI or operator HTTP/admin -> `camera.diagnose.v1` -> application service |
| Reconnect a shared upstream | CLI or operator HTTP/admin -> `camera.reconnect.v1` -> application service |
| Create/negotiate/release viewer session | Authorized playback adapter -> focused streaming service/backend |

Commands validate request shape and delegate; services enforce access, catalog and lifecycle invariants even for direct calls. Use stable Type identities, typed requests/results, `Run` plus `Execute`, and explicit result collectors. Register handlers and context-aware result factories in an owned command set. Only `camstream serve` owns the bus and mutable streaming state. A separately invoked CLI calls the same authenticated Serve HTTPS endpoints; the server dispatches through its owned bus. An unavailable server returns `server_unavailable`, with no local service or direct-backend fallback. Offline `config validate` performs parsing/validation only, without camera/backend I/O. Check registration and diagnostic outcomes; stop/drain invocations before closing registration handles.

Runtime configuration mutation is deferred. If introduced later, use a separate `camera.configure.v1` workflow with its own validation, authorization and conflict semantics.

### 12.3 API and playback contract

```text
GET    /api/health
GET    /api/csrf
GET    /api/cameras
GET    /api/cameras/:cameraID
GET    /api/cameras/:cameraID/status
GET    /api/cameras/:cameraID/streams
GET    /api/cameras/:cameraID/streams/:streamID/status
POST   /api/cameras/:cameraID/streams/:streamID/sessions
DELETE /api/playback/sessions/:sessionID
POST   /api/cameras/:cameraID/diagnose
POST   /api/cameras/:cameraID/streams/:streamID/reconnect
```

Separate process liveness, host/backend readiness and camera availability. Define safe typed read models, session ownership, limits, error codes and empty/denied states in the shared contract task. Playback uses the selected backend's supported signaling; finalize any ICE/WebSocket route in T02 only after T15 proves the pinned backend control path. Keep media outside command dispatch and do not invent a proprietary media protocol.

### 12.4 Goliatone package selection

| Package | Use |
| --- | --- |
| `go-admin`, coordinated `go-admin/quickstart` | Minimal host, modules, route ownership and owned command registration |
| `go-command` | Shared typed commands and result bridge |
| `go-config/config` | Layered host configuration and validation |
| `go-secrets` | Secret references resolved at trusted adapter boundaries |
| `go-errors` | Classified, sanitized errors/results |
| `go-logger/glog` | One host-owned provider with component loggers |
| `go-admin/pkg/go-lifecycle` | Startup, readiness, supervision and bounded shutdown |

The supplied Goliatone catalog guides selection; actual public APIs and tests determine integration. Sibling sources have been inspected but no versions are adopted yet. Select compatible published versions and verify external-module builds without local replacements. Keep the generic core independent of go-admin and application transport types.

### 12.5 Authorization and resource ownership

The browser sees authorized camera/stream/session identifiers, never privileged source URLs or camera credentials. Resolve secret bindings only where backend I/O needs them; protect any generated credential-bearing go2rtc configuration and sanitize downstream errors.

Enforce camera/stream access and separate operator permissions using Serve identity and startup-configured allowlists. Reject arbitrary source URLs, unknown IDs and unauthorized session operations. Browser and CLI obtain package-managed CSRF token/cookie state through authenticated `GET /api/csrf`; unsafe calls send the matching token header. Enforce the configured browser origin and reject cross-origin credentialed access. No password accounts, user database, headless/tagged identity bypass or generic management proxy is included.

`serve` acquires an installation OS lock before backend mutations. Native and Compose share the configured host runtime/lock path. A persistent installation ID names backend resources; each process run gets a new generation. A duplicate controller fails with `controller_conflict`. Multi-host/shared-backend controllers are deferred.

The concrete backend owns its worker processes and may terminate only those workers. Per-viewer close stops its playback process group; source failure/final release stops the affected source's workers. Confirm exits/transport cleanup before freeing reservations. Never signal a PID reconstructed from an unverified stale file or kill an unrelated/shared external backend.

After controller crash, reset rather than adopt old sessions: the verified systemd/container lifecycle boundary must remove old children/helpers before restart admission. Confirm a clean boundary before creating new workers; unknown cleanup keeps playback unready with diagnostics available. A session-worker crash invalidates only that viewer; an ingest crash fails/latches that source and cleans its viewers. No session database or surviving-shared-backend peer deletion is required.

Native deployment uses a dedicated systemd control group; Compose uses one app container with the executable and all children, init/reaping and bounded stop. Ordinary Go request cancellation alone does not prove parent-death cleanup. External Go hosts using this managed backend must supply equivalent verified containment and explicit lifecycle wiring; unsupported containment blocks readiness. Other backend implementations need not use processes.

Bound upstream startup/recovery by observed media progress. Reliable terminal evidence stops early; otherwise a finite no-media/stall window stops the source's workers, latches `source_unavailable`, and requires operator reconnect/config correction. Do not depend on log parsing or classify every stall as bad credentials. Browser retries cannot reopen a latched ingest. These failure bounds do not expire healthy long-lived/PiP viewing. The new T15 pass must demonstrate all cleanup and retry behavior before implementation contracts are finalized.

## 13. Deployment Design

### 13.1 Repository layout

```text
go-cam-stream/
├── go.mod
├── cmd/camstream/main.go
├── pkg/camstream/
│   ├── camera.go, strategy.go, source.go, backend.go, playback.go
│   ├── service.go, errors.go
│   ├── strategies/ec70/
│   └── backends/go2rtc/
├── internal/
│   ├── app/
│   ├── viewer/                 # application service, commands, registration
│   ├── admin/
│   └── transport/{http,cli}/
├── config/
├── data/                       # app-owned embedded frontend resources
├── frontend/
├── deploy/{compose.yaml,systemd/}
├── examples/embedded/
├── docs/
└── .ctx/specs/camera-streaming/
```

Use explicit configuration and constructor injection. Reusable packages own any resources needed for independent operation. The external-consumer example proves public API usability without application internals or working-directory assumptions.

### 13.2 Installable application

Build `camstream` as a foreground CLI/server with compiled frontend assets embedded at build time. Runtime installation requires neither Node nor the source checkout. Source installation must document the frontend generation step; do not claim bare `go install` produces assets unless that path is actually supported.

Provide native Linux arm64 installation first: install both executables, place the app/workers in one dedicated systemd control group and verify child cleanup before restart. Then validate Compose: one app container contains the pinned go2rtc executable and all managed workers, with init/reaping, bounded stop, Linux host networking, a private PID namespace and a bounded tailnet-restricted media port pool. No separate long-lived go2rtc sidecar or per-viewer container is needed. The adapter explicitly launches/stops its own workers; the deployment supervisor owns installation-wide restart. Tailscale remains on the Pi host.

Prepare reproducible build/install instructions, pinned dependencies/images, private secret locations, restart ordering, readiness, upgrade and rollback procedures. Publication and installation on a remote machine require their own authorization.

### 13.3 Multiple-camera configuration

Illustrative application schema, to be finalized in the shared configuration contract:

```yaml
installation:
  id: home-viewer
  runtime_dir: /run/camstream # same host lock path for native and Compose

access:
  users:
    - login: owner@example.com # exact Serve login; illustrative syntax
      view: [room1/main, room2/main, livingroom/main, hall/main, front-porch/main]
      operate: [room1/main, room2/main, livingroom/main, hall/main, front-porch/main]

streaming:
  backend: go2rtc
  mode: on-demand
  max_viewers: 4 # illustrative; T15 must measure a viable default
  go2rtc:
    executable: /usr/local/libexec/go2rtc
    runtime_dir: /run/camstream/workers
    media_port_start: 20000
    media_port_end: 20003 # one TCP/UDP pair per viewer; illustrative
    # startup/recovery/cleanup limits are selected from T15 evidence

cameras:
  - id: room1
    physical_camera_id: device-room1
    name: Room 1
    strategy: ec70
    address: 192.168.1.101
    credentials_ref: cameras/room1
    streams:
      - id: main
        profile: default
  - id: room2
    physical_camera_id: device-room2
    name: Room 2
    strategy: ec70
    address: 192.168.1.102
    credentials_ref: cameras/room2
    streams:
      - id: main
        profile: default
  - id: livingroom
    physical_camera_id: device-livingroom
    name: Living Room
    strategy: ec70
    address: 192.168.1.103
    credentials_ref: cameras/livingroom
    streams:
      - id: main
        profile: default
  - id: hall
    physical_camera_id: device-hall
    name: Hall
    strategy: ec70
    address: 192.168.1.104
    credentials_ref: cameras/hall
    streams:
      - id: main
        profile: default
  - id: front-porch
    physical_camera_id: device-front-porch
    name: Front Porch
    strategy: ec70
    address: 192.168.1.105
    credentials_ref: cameras/front-porch
    streams:
      - id: main
        profile: default
```

Names/addresses are examples, not an assertion that all installed devices are EC70s. `credentials_ref` is a host-managed binding resolved through go-secrets; it is not a password or necessarily a literal provider URI. Each binding may refer to the same authorized account or a dedicated credential where supported. Profile `default` does not assert multiple EC70 outputs. Validate unknown strategy/backend IDs, duplicates, unsupported profiles and session limits before serving. Aliases of one device must reuse its physical camera ID; same-source aliases share normalized identity. Reserve one distinct active source per device and configure total/per-stream viewer limits. Always-on and application idle-grace configuration are deferred. The worker topology is an adapter implementation detail; executable/runtime/media options are backend-specific configuration. Validate viewer/port/process limits together and fail readiness if lifecycle containment cannot be established. No camera or deployment capacity is certified by these sample values.

### 13.4 go2rtc and secrets

The adapter renders immutable owner-namespaced worker configuration and maps private RTSP relays to session workers. It does not replace active definitions using PUT or delete them as a teardown shortcut. A Kasa source may require a credential-bearing URL with URL-safe email and Base64 password, as described in Section 4. Treat that representation as secret material, never encryption.

Use 0700 runtime directories and 0600 configs; resolve camera credentials only for ingest workers, never into process arguments, playback configs or browser responses. API/RTSP traffic stays private within the host/container; worker-to-worker loopback is inside the same app container. Configure the public media port pool/candidates separately. Remove owned runtime files after confirmed cleanup and document rotation. Optional helper processes belong to the same verified cleanup boundary.

## 14. Development and Delivery Plan

Canonical tasks and their exact dependencies live in [TASKS.md](../.ctx/specs/camera-streaming/TASKS.md). This proposal does not maintain a competing completion checklist. Planning is the only currently authorized work.

| Delivery slice | Canonical task | Exit evidence |
| --- | --- | --- |
| Planning baseline | T01 | Aligned proposal, requirements, design and dependency-ordered tasks |
| Planning review and resolution | T13, T14 | Six findings resolved in documents; runtime evidence still pending |
| Isolation planning | T16 | Shared ingest/per-viewer processes; encapsulated lifecycle; no upstream dependency |
| Backend feasibility gate | T15 | Preserve failed external-API evidence; prove the new stock-process topology before T02 |
| Dependency and shared contracts | T02 | T15-backed public APIs, external typed source/config/encoder and transport contracts |
| Default backend | T03 | go2rtc controlled-source negotiation, reconciliation and ownership smoke |
| Default camera strategy | T04 | EC70 config/source mapping and reproducible authorized feasibility procedure |
| Public streaming service | T05 | External consumer, injected test backend, shared viewers and isolated multi-camera lifecycle |
| Application workflows | T06 | Configured catalog, services, typed commands and real owned-bus dispatch |
| Host and adapters | T07 | CLI/HTTP/admin integration, authorization, readiness and shutdown |
| Independent hardware feasibility | T08 | Standalone pinned go2rtc/built-in viewer; no production adapter dependency |
| Presentation | T11 | Camera/stream selection and supported controls on agreed sample states |
| Live playback | T12 | Controlled-source playback, session cleanup and viewer recovery through real API |
| Packaging | T09 | Embedded binary plus pinned executable; native control-group and single-container cleanup/media-pool smoke |
| Reference deployment | T10 | Real multi-camera remote viewing, recovery, performance and device PiP evidence |

T08 can run first when hardware access is authorized, independently of T15/T02/T03/T04. EC70 incompatibility blocks T10 acceptance, not generic controlled-source work. After this planning revision and separate feasibility authorization, T15 must pass the new managed-process checks before T02 publishes interfaces; T03 then implements that exact supported path. Packaging depends on the frontend build. No task labels authorize parallel agents, implementation, deployment or acceptance.

No upstream patch/fork/update or production Pion backend is part of this approach. If worker containment, relay codecs or resource costs fail T15, record the evidence and revise the local design without silently weakening viewer isolation or camera-sharing requirements.

## 15. Testing Strategy

### 15.1 Public library and service boundaries

- External-package/consumer compile with injected strategy and backend, without application internals or host working-directory assumptions.
- Typed config/source/profile validation, unknown IDs, classified errors and cancellation.
- Per-device/source concurrency, aliases, conflicting profiles, first-subscriber races, viewer limits, observed closure and canceled-negotiation rollback; no fixed active TTL.
- One camera failure preserving another, viewer release preserving other viewers, and cleanup preserving unrelated backend resources.

### 15.2 Commands and adapters

Use focused service tests for invariants plus a second CLI process calling the live server and actual registered HTTP/admin dispatch for message factories, typed results and safe failures. Verify Serve identity/static scopes, unknown-resource handling and browser/CLI CSRF. An offline server must not trigger direct-backend fallback. A direct handler call is insufficient evidence of registration or shared adapter behavior.

### 15.3 Controlled media and packaging

Before relying on EC70 hardware, test the production go2rtc adapter with controlled media: source reconciliation, supported signaling, ICE/media connectivity, session cleanup, backend outage and capability errors. Verify the real embedded app's routes/assets, native-first then Compose startup, duplicate-controller rejection, controller SIGKILL/descendant cleanup, viewer/ingest worker failures, port exhaustion, conservative cleanup timeouts and actual TCP media with UDP media blocked. Mocks and frontend samples do not prove media compatibility.

### 15.4 Hardware and browser acceptance

Record exact camera firmware, hardware and browser/device versions. Exercise EC70 -> injected default backend -> authorized playback API -> WebRTC/Tailscale -> Mac/iPhone browser.

Validate two different camera streams concurrently, two viewers on one upstream, Tapo coexistence, stream switching, viewer retry isolation, camera reboot, Pi reboot, router/internet recovery, Wi-Fi/cellular changes and sleep/resume. Measure startup, latency, CPU/RAM and 60-minute continuity. Check exercised responses/logs for secret leakage and verify real iPhone PiP/background behavior.

Unproven or failed hardware behavior stays explicit. Passing checks provide delivery evidence; they do not constitute product acceptance. Keep broader stabilization and exhaustive device/load matrices out of scope.

## 16. Operational Requirements

### 16.1 Monitoring

Minimum diagnostics:

- Process/container health.
- Camera reachability.
- Per-camera and per-stream connection state, separated from backend availability.
- Active viewer sessions and configured capacity limits.
- Last successful media connection.
- Recent sanitized errors.
- Raspberry Pi resource consumption.

Avoid recording the video itself or retaining unnecessarily sensitive diagnostics.

### 16.2 Logging

Use structured logging.

Include relevant component, event, timestamp, and error classification.

Never log:

- Camera passwords.
- Encoded passwords.
- Authentication tokens.
- Complete secret-bearing source URLs.
- Private video payloads.

### 16.3 Recovery

The service must restart automatically after process failure or reboot.

Viewer connectivity, backend upstream recovery and process supervision have distinct owners. Transient failures trigger bounded reconnect attempts with backoff at the appropriate layer; avoid nested retry loops. An offline camera must not restart the entire host.

The default adapter stops a source's owned workers when its finite startup/recovery budget expires, preventing unlimited native retries without a patched API. Known terminal failures may stop earlier; unknown failures remain accurately classified as source unavailable. Source faults require explicit operator reset. Controller crashes require verified child cleanup before readmission; individual worker faults respect their viewer/source boundaries. T15 must prove these bounds and restart behavior.

### 16.4 Maintenance

Document:

- Updating the application.
- Updating go2rtc.
- Updating the Raspberry Pi.
- Rotating credentials.
- Restoring from configuration backup.
- Diagnosing offline streams.
- Reviewing Tailscale permissions.

## 17. Security Model

Security is particularly important because this application grants access to a private indoor camera.

Requirements:

- Camera owner explicitly approves installation and access.
- Only authorized users can view the stream.
- No public port forwarding.
- No unauthenticated public streaming endpoints.
- Tailscale ACLs limit remote access.
- Secrets are not committed to Git.
- Credentials are not included in browser responses.
- Administrative endpoints are inaccessible to unauthorized viewers.
- HTTPS is used for remote web access.
- Logs are scrubbed of sensitive credentials.
- Recordings are disabled by default.
- No third-party cloud media relay unless explicitly approved.

Review access permissions when a device is lost or access should be revoked.

Separate viewing and operator permissions in MVP, even when one trusted identity holds both. A richer per-user administration UI can follow later.

## 18. Known Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| EC70 V4 protocol incompatible | EC70 deployment acceptance blocked | Early exact-device spike; continue independent library/controlled-source work |
| Shared account cannot authenticate | Stream unavailable | Test authorized credentials |
| Firmware changes proprietary protocol | Future outage | Pin versions, monitor changes |
| Camera limits upstream connections | Conflict with Tapo | Validate single go2rtc ingest |
| Incorrect WebRTC ICE candidates | Remote playback fails | Tailscale-aware configuration |
| iOS PiP incompatible | Key UX goal unmet | Test early on real device |
| Transcoding overloads Pi | Poor performance | Prefer H.264 passthrough |
| Network interruptions | Video freezes | Reconnect/backoff logic |
| Credentials exposed in configs/logs | Privacy/security risk | Secret handling and auditing |
| Power or storage failure | Service unavailable | Quality power, storage, restart |
| Managed workers exceed lifecycle/resource limits | Cleanup, ports or Pi capacity fail | New T15 isolation/containment/arm64 probes before T02; no upstream dependency |
| Resource naming/cleanup crosses owners | Other streams interrupted | Owner namespace, per-stream coordination, isolated cleanup |
| Multi-camera load exceeds capacity | Playback degradation | Measure aggregate use, passthrough and explicit session limits |

## 19. Future Enhancements

Following successful MVP delivery, consider:

- Simultaneous multi-tile camera dashboard; multi-camera catalog and selection are already MVP.
- Pan/tilt control through supported proprietary APIs.
- Snapshot capture.
- Motion event integration.
- Optional local recording.
- HomeKit integration.
- Apple Watch or native iOS companion.
- Custom desktop menubar viewer.
- Home-screen PWA experience.
- Rich per-user permission administration; explicit camera/stream and operator authorization already belongs to MVP.
- Event-based notifications.
- Rich stream-quality controls beyond the configured supported-stream selector.
- Additional production backends, mixed per-camera backend routing and runtime configuration workflows.
- Camera discovery.
- Raspberry Pi management interface.

These enhancements should remain out of scope until the core streaming experience is stable.

## 20. Definition of Done

The canonical acceptance criteria are in [REQUIREMENTS.md](../.ctx/specs/camera-streaming/REQUIREMENTS.md). Required evidence includes:

1. An external Go consumer supplies its own typed config/source/strategy and encoder or matching backend without core edits, and exercises cancellation/errors without the reference host.
2. T15 proves stock go2rtc shared-ingest/per-viewer workers before API publication; production tests reproduce isolated worker close, observed media, cancellation, bounded retries and crash containment. No upstream patch is required.
3. Two cameras stream concurrently; viewers and source aliases share without cross-viewer teardown. A conflicting profile on the same device is rejected without eviction.
4. A second-process CLI uses Serve HTTPS and the running server's typed commands, identity/scopes and CSRF. Stopped-server operations fail without composing another controller.
5. The packaged CLI/server embeds the UI; native service and Compose installs document go2rtc as an external runtime dependency and recover after reboot.
6. Exact EC70 hardware/firmware and authorized credentials produce a reproducible local and remote stream, with Tapo coexistence and limits documented.
7. Mac/iPhone playback, supported Mac PiP, viewer recovery and 60-minute operation pass measured targets; iPhone PiP is demonstrated or its precise limitation is explicitly accepted as an exception.
8. Actual TCP media passes with UDP media blocked on target browsers. Controller crash, viewer/source worker failures, duplicate-controller rejection, port/resource exhaustion, incomplete cleanup and long-lived/PiP playback meet the specified boundaries.
9. A second developer can reproduce installation and troubleshooting from repository instructions and authorized credentials.

Library/backend gates can pass without EC70 compatibility. The full reference deployment cannot be claimed complete while its hardware gate is unresolved. Task completion and passing checks do not establish acceptance; explicit authorized closeout remains required.

## 21. Reference Material

Primary sources:

**go2rtc**

- https://github.com/AlexxIT/go2rtc
- https://github.com/AlexxIT/go2rtc/blob/master/internal/kasa/README.md
- https://github.com/AlexxIT/go2rtc/tree/master/internal/kasa
- https://go2rtc.org/internal/webrtc/
- https://github.com/AlexxIT/go2rtc/blob/master/www/README.md

**Alternative implementation references**

- https://github.com/pion/webrtc
- https://github.com/jaysoffian/ha-kc100
- https://github.com/jayantak/homebridge-kasa-camera

**Networking**

- https://tailscale.com/docs/features/tailscale-serve
- https://tailscale.com/docs/reference/tailscale-cli/serve

**Goliatone integration references**

- Original external planning reference: `../../GOLIATONE.md` (package catalog
  outside this repository; not required to build or install the application).
- [Canonical package boundaries and dependency plan](../.ctx/specs/camera-streaming/DESIGN.md).
- Apply the installed `structure-application-commands` and `go-admin-application-builder` skills during implementation. Catalog and dependency-source instructions do not authorize unrelated edits or publication.

**Other**

- https://docs.docker.com/compose/
- https://developer.mozilla.org/en-US/docs/Web/API/Picture-in-Picture_API
- https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API

Verify current project documentation and dependency versions before implementation.

---

## 22. Continuation and Open Questions

Read the canonical [requirements](../.ctx/specs/camera-streaming/REQUIREMENTS.md), [design](../.ctx/specs/camera-streaming/DESIGN.md), [tasks](../.ctx/specs/camera-streaming/TASKS.md), and newest [handoff](../.ctx/specs/camera-streaming/HANDOFFS.md) before material work. This proposal supplies product/deployment context and does not override repository policy or start implementation.

Implementation remains unstarted. The original T15 external-API experiment failed and is preserved. T16 selects encapsulated stock-process isolation; after separate authorization, resume T15 against that topology before T02. T08 can run independently when camera access is authorized. A failed new T15 gate still blocks API publication; upstream changes are not a prerequisite or fallback. Keep library, backend, hardware and installed-app evidence separate.

Open questions requiring implementation or hardware evidence:

- Exact model/hardware/firmware inventory for room1, room2, livingroom, hall and front-porch.
- EC70 local protocol exposure, invited-versus-owner credentials, codecs and simultaneous Tapo behavior.
- Stock-worker private RTSP passthrough, per-viewer termination, bounded failures and Linux/container parent-death cleanup against the revised backend contract.
- Compatible published Goliatone versions/toolchain and the public middleware seams for the selected Serve/static-allowlist and CSRF integration.
- Measured C+V worker memory/CPU/startup, media-port capacity, observation/cleanup windows and camera constraints for the target Pi.
- Real iPhone PiP/background behavior and Tailscale ICE routing, including required WebRTC/TCP media.

No hardware compatibility, package adoption, deployment, implementation completion or product acceptance is implied by this planning baseline.

# Browser playback behavior

The viewer uses WebRTC with receive-only video/audio transceivers and no third-party
ICE servers. It supports the server's UDP/TCP media candidates; it has no MSE/HLS
fallback. A successful HTTPS answer does not establish media connectivity. The
transport badge comes from the selected remote candidate in `RTCPeerConnection`
statistics, and playing state comes from the media element.

| Control or event | Behavior |
| --- | --- |
| Initial playback | Muted, inline video; a rejected `play()` promise exposes Play video. |
| Paused live video | A pause from picture-in-picture controls, media keys or the OS shows Paused and offers Play video. The peer keeps receiving media; the viewer does not resume on its own. |
| Unmute | User gesture changes mute and resumes playback; rejection restores muted state and safe feedback. |
| Fullscreen | The player container (video and controls) through the standard element API, then the WebKit-prefixed one; otherwise Safari video fullscreen once metadata has loaded. Exit uses the matching API. Rejections remain visible. |
| Picture in picture | Standard API or supported Safari presentation mode, only after live video arrives. Unsupported cases explain why. |
| Selection change | Detach/pause old video, close peer/stop tracks, confirm server release, then negotiate the latest selection. |
| Brief transport loss | Show reconnecting while native ICE recovers; sustained disconnection/failed media releases the viewer and offers explicit retry. |
| Hidden page/PiP | No lease renewal or mandatory foreground polling. Hiding pauses observations, not the peer. Browser/OS suspension policy still applies. |
| Controller restart / lost session ownership | Denied cleanup preserves the old session locally and offers Reload page, which reopens the same camera and stream. A fresh page rechecks catalog/access and readiness; denial is never treated as successful cleanup. |
| Source failure | Safe source error; viewer retry never performs operator reconnect or clears a source latch. |
| Page exit | Best-effort keepalive release plus immediate peer close; backend peer-loss handling provides bounded cleanup when delivery is unavailable. |

Capability detection is runtime-based, not a user-agent compatibility promise.
The [play API](https://developer.mozilla.org/en-US/docs/Web/API/HTMLMediaElement/play)
requires handling promise rejection. [Fullscreen](https://developer.mozilla.org/en-US/docs/Web/API/Element/requestFullscreen)
and [PiP](https://developer.mozilla.org/en-US/docs/Web/API/HTMLVideoElement/requestPictureInPicture)
can reject for activation or policy reasons even when APIs exist.
WebKit's [inline/muted video guidance](https://webkit.org/blog/6784/new-video-policies-for-ios/)
informs the initial presentation, but does not substitute for testing a current iPhone.

## Validation boundary

T12 uses synthetic H.264/Opus through the actual T07 controller/API and stock-go2rtc
backend in a disposable Linux container, with a TLS-verifying local development
proxy. Desktop Chromium playback, independent viewers, switching, TCP with UDP
unpublished, and PiP are exercised directly. See the [dated validation record](../.ctx/specs/camera-streaming/support/evidence/t12-playback.md)
for exact outcomes, long-session evidence and any unresolved checks.
The [T18 implementation review](../.ctx/specs/camera-streaming/support/evidence/t19-review.md)
validates the regenerated embedded viewer in foreground Brave 157.1.99.55 with
controlled H.264 over TCP. Container fullscreen keeps the controls visible and
exits correctly; PiP opens/closes, native browser pause shows Paused and Play video
resumes playback. Camera/stream links, reload, independent tabs and safe source
failure copy also pass. Background automation rejected fullscreen, while a normal
foreground tab passed. This does not establish Safari/iPhone behavior or every
OS/media-key pause source. The automation extension replaces the favicon, so its
state colors retain unit-test coverage rather than a visual browser claim.
Firefox Developer Edition fails
ICE in both the TCP-only and TCP+UDP local fixtures (owner-reported). Its exact
version and cause are unverified; Firefox support is not established. The generic
STUN suggestion in its diagnostic is not evidence that a STUN/TURN service is
required. No relay, browser-policy override or broader port exposure was added.
ICE transport support varies ([connectivity guidance](https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Connectivity)).

Real Safari/iPhone behavior, EC70 codecs, remote Tailnet UDP/TCP and sustained target
hardware capacity remain T08/T10 acceptance gates. T09 owns production asset
embedding and native-first installation. None are established by mocked capability
tests or the local browser fixture.

# Browser playback behavior

The viewer uses WebRTC with receive-only video/audio transceivers and no third-party
ICE servers. It supports the server's UDP/TCP media candidates; it has no MSE/HLS
fallback. A successful HTTPS answer does not establish media connectivity. The
transport badge comes from the selected remote candidate in `RTCPeerConnection`
statistics, and playing state comes from the media element.

| Control or event | Behavior |
| --- | --- |
| Initial playback | Muted, inline video; a rejected `play()` promise exposes Play video. |
| Paused live video | Pause (Space or K), or a pause from picture-in-picture controls, media keys or the OS, shows Paused and offers Play video, also while a brief transport loss recovers. A paused element reports no playback, so Play is what restores the Live state. The peer keeps receiving media; the viewer does not resume on its own. |
| Unmute | User gesture changes mute and resumes playback; rejection restores muted state and safe feedback. |
| Stop | Releases this page's session through the controller's serialized cleanup and keeps the selection, also while a Play or Try again is still connecting; the stopped view offers Play video, which negotiates a new session. A failed release shows the cleanup error and keeps the session for an explicit retry. Nothing restarts on its own within the page; a reload or a back-forward cache restore starts the view again, as a fresh page load does. |
| Fullscreen | The viewer root (video, controls and overlays) through the standard element API, then the WebKit-prefixed one, so fullscreen persists when cameras switch; otherwise Safari video fullscreen once metadata has loaded, disabled again when the media is removed. Exit uses the matching API. Standard API rejections remain visible; the older WebKit-prefixed request reports failure only by event, which the viewer does not surface. |
| Picture in picture | Standard API or supported Safari presentation mode, only after live video arrives. Unsupported cases explain why. |
| Overlay controls | Fade after 3 s of playback without pointer, touch or keyboard activity, and stay while paused, connecting, failed, a hint shows, a mouse or pen rests on the toolbar or it holds keyboard focus. Touch leaves `:hover` behind and does not hold them. Hidden controls take no pointer events, so the first tap or pointer move reveals them. Unavailable controls stay focusable and say why when pressed. |
| Keyboard shortcuts | Single keys without Ctrl, Alt or Meta; held keys do not repeat. Ignored while a form field has focus or a dialog is open, and Space still activates a focused button. R starts a stopped view and otherwise says when there is nothing to retry. Layouts that need AltGr or Option for `[` and `]` cannot use those two; the rail and the camera sheet still switch cameras. |
| Phone layouts | Portrait pins the player under the top bar. Landscape on touch screens at 500 px height or less fills the screen, pads for safe areas (`viewport-fit=cover`), scrolls a long message above the controls and opens cameras and the stream choice in a modal sheet. The camera name stays the page heading for screen readers. A zoomed or short desktop window keeps the regular layout, which scrolls instead of clipping. |
| Selection change | Detach/pause old video, close peer/stop tracks, confirm server release, then negotiate the latest selection. |
| Brief transport loss | A compact Reconnecting badge over the last frame counts down the 15 s recovery window while native ICE recovers; a media stall without a peer disconnection shows no countdown. The seconds are visual only; a status region that is always in the page announces the reconnect and any automatic retry. A sustained disconnection or failed peer releases the viewer. Video that played is then tried again on its own, up to twice, 3 s and then 10 s after each loss, while the page is visible; Try again starts at once and Stop ends the wait. Other failures offer explicit retry. |
| Status freshness | Timed on this device, with page-local time, from when each status answer with a newer observation of a known state arrives. The server's observation time is only compared with its earlier values, never with the device clock, so a stale server observation counts as no update. After 10 s of asking without an update, the stream details warn "No update for N s". |
| Camera status | Rows other than the selected one show the server's status of that camera (Live, Connecting, Reconnecting, Offline or Unavailable) and dim an offline or failed camera, which stays a link; an idle camera shows none. In the collapsed rail a dimmed icon keeps 3:1 contrast and the tooltip adds the status to the camera name. The statuses refresh every 10 s while the page is visible and at once when it is shown again; until that refresh answers, at most 20 s, the rows keep their last statuses. A failed refresh shows no status rather than an old one, and a reload overrides an older refresh. |
| Failure details | A safe error code, camera, stream and the time first shown; never the server's message. Choose another camera opens the camera sheet when the server reports a failure of this camera or stream and another camera can play. Lost connections and timeouts, which this device's network may cause, offer Try again only, as do failures every camera shares. |
| Hidden page/PiP | No lease renewal or mandatory foreground polling. Hiding pauses observations and the camera status refresh, not the peer, and pauses the freshness warning; a page opened in a background tab starts without observation. An automatic retry that comes due while the page is hidden waits until it is shown. Browser/OS suspension policy still applies. |
| Controller restart / lost session ownership | Denied cleanup preserves the old session locally and offers Reload page, which reopens the same camera and stream. A fresh page rechecks catalog/access and readiness; denial is never treated as successful cleanup. |
| Source failure | Safe source error; viewer retry never performs operator reconnect or clears a source latch. |
| Page exit | Best-effort keepalive release plus immediate peer close; backend peer-loss handling provides bounded cleanup when delivery is unavailable. |

The supported browsers are current Safari and Chrome on macOS and Safari on iPhone.
The viewer's script uses ES2022 private class methods, so it does not run at all
before Safari 15, Chrome 85 or Firefox 90. Its layout uses dynamic viewport units
and a container query where available and falls back without them.

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
T21's video-first layouts were checked in the presentation preview, with synthetic
inputs and viewport emulation in a desktop browser, at 1440×900, 960×450, 900×700
and 375×812, and with touch emulation at 667×330 and 640×260. Overlay fade, Stop,
the camera sheet and its stream choice, scrolling messages, focus after switching
and the shortcuts dialog behaved as specified. No live media, real phone, notch
safe area or touch hardware was exercised.
T22's reconnect countdown, stale-status warning, failure details and Choose
another camera sheet were checked the same way at 1440×900 and 375×812, and with
touch emulation at 667×330 and 640×260. Live disconnection timing, status outages
against a real server and screen-reader announcements were not exercised.
T23's automatic retry countdown was checked the same way at 1440×900 and 375×812,
and with touch emulation at 667×330; controller tests cover its delays, limit,
visibility wait and cancellation. A real lost connection was not exercised.
T24's camera row statuses, dimming and collapsed-rail tooltips were checked in
the synthetic preview; interaction tests cover the refresh, its schedule across
visibility changes, failed refreshes, closing the page and a reload overtaking a
refresh, and Go tests cover the server's summary. Statuses from a live server were
not exercised in a browser.
T25's smaller production bundle was loaded in a desktop Chromium browser with a
stubbed API: the camera list, a session's creation and release, a failed
connection's view and a dimmed camera row behaved as before, with no console
errors. Older Safari, Chrome and Firefox versions were not run.
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

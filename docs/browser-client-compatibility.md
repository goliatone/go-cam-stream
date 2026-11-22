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
| Overlay controls | Fade after 3 s of playback without pointer, touch or keyboard activity, and stay while paused, connecting, failed, a hint shows, a mouse or pen rests on the toolbar or it holds keyboard focus. Touch leaves `:hover` behind and does not hold them. Hidden controls take no pointer events, so the first tap or pointer move reveals them. Unavailable controls stay focusable and say why when pressed. A solid shade behind the icons and a dark halo around the focus ring keep both visible on a bright frame. |
| Keyboard shortcuts | Single keys without Ctrl, Alt or Meta; held keys do not repeat. Ignored while a form field has focus or a dialog is open, and Space still activates a focused button. R starts a stopped view and otherwise says when there is nothing to retry. Layouts that need AltGr or Option for `[` and `]` cannot use those two; the rail and the camera sheet still switch cameras. |
| Phone layouts | Portrait pins the player under the top bar. Landscape on touch screens at 500 px height or less fills the screen, pads for safe areas (`viewport-fit=cover`), scrolls a long message above the controls and opens cameras and the stream choice in a modal sheet. The camera name stays the page heading for screen readers. A zoomed or short desktop window keeps the regular layout, which scrolls instead of clipping. |
| Light or dark | The page follows the system's appearance setting, as do scrollbars and the browser's other parts. The video stage and its overlays look the same in both; a dark page draws a line around the stage. There is no toggle, and nothing is stored. |
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
T26's tokens were compared rule by rule with the previous stylesheet. The preview's
21 states were captured in headless Chromium at 1440×900 and 375×812 before and
after, and the pixel differences are the intended ones: shades merged within one
just-noticeable difference, the select's text, the 18 px narrow title, the toolbar
shade, the prompt's backing, an unplayable camera's darker name and, at phone
width, toolbar icons 3 px further from the stage's edges. Short desktop windows
keep the previous page and stage sizes. Over a simulated white frame, the prompt,
failure copy, toolbar icons and keyboard focus ring were compared before and
after; on a phone the focus ring's halo stays inside the stage. A dialog's
backdrop takes its colour from a token set on `::backdrop` itself, because Safari
before 17.4, Chrome before 122 and Firefox before 120 do not pass custom
properties to a backdrop; those versions were not run. Real video frames, Safari
and iPhone were not exercised.
T27's dark theme was captured the same way, with Chrome started in dark mode, and
the browser pane's dark emulation showed the built page, the stream select and its
focus ring. With touch emulation at 667×330, a focused skip link over the video
keeps its ring on its own margin in both themes. With a light setting the 47
captures are pixel-identical to T26's.
Safari, iPhone and Firefox were not run; Firefox before 96 has no `color-scheme`,
so there scrollbars stay light on a dark page.
Firefox Developer Edition's localhost failure was reproduced on 2026-11-16 with
the real Mac camera and authenticated RTSP input. Camera media reached the server,
but `about:webrtc` showed only LAN local candidates against remote
`127.0.0.1:35101` candidates, no ICE candidate pairs and no received video frames.
Firefox excludes loopback ICE by default ([Mozilla explanation](https://bugzilla.mozilla.org/show_bug.cgi?id=1659672#c1)).
Enabling `media.peerconnection.ice.loopback` and restarting was tested, but did
not resolve this failure. After restart, Firefox reported the preference as true
and continued gathering LAN-only candidates. A fresh import of both native
connection files and an uninterrupted, visible test again received source media
then timed out without browser playback. The restart also installed Developer
Edition 161.0a2 (the prior update page reported 160.0), so this is not a controlled
same-build comparison. Do not present this preference as a verified fix.

Mozilla's implementation also restricts candidates based on capture permission
([candidate filtering](https://github.com/mozilla/gecko-dev/blob/master/dom/media/webrtc/jsapi/PeerConnectionImpl.cpp#L4055-L4078)).
Which additional filter causes the post-restart failure remains unisolated.
The bundled Mac configuration uses this media path in both `./taskfile dev:serve`
and `./taskfile dev:serve --webcam`. It selects a private IPv4 from an active Wi-Fi
or Ethernet interface at startup and keeps HTTP and worker control on loopback.
If none is available, it falls back to loopback media. Custom runs use loopback
by default. `--media-address PRIVATE_IPV4` selects an assigned address explicitly,
and `--media-address 127.0.0.1` forces loopback. Wildcard, public, IPv6 and
unassigned addresses are rejected. The test-pattern path needs no STUN/TURN
service or camera permission.

On 2026-11-16 Firefox Developer Edition 161.0a2 completed real Mac live export →
import both files → decoded preview test/cleanup → save → Watch Camera on the
patched native host. H.264 1280×720 playback used a selected UDP pair to
192.168.1.70:35900; decoded frames advanced 932 → 1,759 over 27.6 seconds with
zero dropped frames/lost packets. Native capture and localhost RTSP remained
running throughout. The earlier authorized loopback preference remained true;
the successful pair uses the non-loopback interface, and no additional browser
policy change was made. This establishes this tested Mac/Firefox flow, not a
new general minimum Firefox version or remote/network compatibility guarantee.
ICE transport support varies ([connectivity guidance](https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API/Connectivity)).

Real Safari/iPhone behavior, EC70 codecs, remote Tailnet UDP/TCP and sustained target
hardware capacity remain T08/T10 acceptance gates. T09 owns production asset
embedding and native-first installation. None are established by mocked capability
tests or the local browser fixture.

import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import type { InputKind } from "../services/conversation-media";
import type { RoomRole } from "../services/contracts";
import {
  capitalized, deviceEntries, dockKinds, failureCopy, inputNames, routeSpeaker, speakerRouting,
  type Choices, type InputErrors, type Pending,
} from "./devices";
import { Icon } from "./icons";
import {
  cornerFor, cornerNames, duration, handOff, insets, placeOverlays, useAutoHide, useClock, useHeldIdle, useTileDrag, type Corner,
} from "./layout";
import { Menu, Popover, type MenuEntry } from "./overlays";

export type CallPresence = "waiting" | "connecting" | "connected" | "reconnecting";

export type CallModel = {
  role: RoomRole; native: boolean; canCopyInvitation: boolean; phase: CallPresence;
  remoteCamera: boolean; remoteMic: boolean; incoming: MediaStream | null; incomingAudio: boolean | null;
  playbackMuted: boolean; local: MediaStream | null; micOn: boolean; cameraOn: boolean;
  pending: Pending; inputErrors: InputErrors; busy: boolean; leaving: boolean;
  devices: MediaDeviceInfo[]; choices: Choices; speaker: string; expiresAt: string; connectedSince: number | null;
};

export type CallActions = {
  toggle(kind: InputKind): void; enableBoth(): void; retry(kind: InputKind): void;
  changeDevice(kind: InputKind, id: string): void; chooseSpeaker(id: string): void; refreshDevices(): void;
  setPlaybackMuted(muted: boolean): void; copyInvitation(): void; leave(): void;
  toast(text: string, icon?: "check" | "eye" | "eye-off" | "leave" | "alert"): void; announce(text: string): void;
};

type Overlay = { type: "menu"; menu: InputKind | "more"; anchor: HTMLElement } | { type: "error"; kind: InputKind } | { type: "end" } | null;

/** "until 14:07", or a weekday as well when the invitation outlives today. */
export function invitationUntil(expiresAt: string) {
  const end = new Date(expiresAt);
  if (!expiresAt || Number.isNaN(end.getTime())) return "";
  const sameDay = end.toDateString() === new Date().toDateString();
  return end.toLocaleString([], sameDay ? { hour: "2-digit", minute: "2-digit" } : { weekday: "short", hour: "2-digit", minute: "2-digit" });
}

/** The End confirmation, which matches the Mac app's own wording. */
export function EndConfirm({ anchor, root, peerPresent, close, end }: {
  anchor: HTMLElement; root: HTMLElement; peerPresent: boolean; close(returnFocus: boolean): void; end(): void;
}) {
  const title = peerPresent ? "End this call for both of you?" : "End this call?";
  return <Popover anchor={anchor} root={root} title={title} describedBy="end-detail" close={close} class="confirm">
    <strong class="pop-title">{title}</strong>
    <p id="end-detail">{peerPresent ? "Your guest leaves too, and the invitation stops working." : "The invitation stops working."}</p>
    <div class="pop-actions">
      <button id="end-keep" class="btn quiet sm" type="button" data-autofocus onClick={() => close(true)}>Keep call</button>
      <button id="end-conversation" class="btn danger sm" type="button" onClick={end}>End call</button>
    </div>
  </Popover>;
}

/** A video element that shows `stream`, or nothing. */
export function useVideo(stream: MediaStream | null) {
  const ref = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    const video = ref.current;
    if (!video) return;
    video.srcObject = stream;
    return () => { video.srcObject = null; };
  }, [stream]);
  return ref;
}

/** Width over height of a video's frames, kept within portrait 9:16 and wide 2:1. */
function useAspect(ref: { current: HTMLVideoElement | null }) {
  const [aspect, setAspect] = useState(16 / 9);
  useEffect(() => {
    const video = ref.current;
    if (!video) return;
    const measure = () => {
      if (video.videoWidth && video.videoHeight) setAspect(Math.min(2, Math.max(9 / 16, video.videoWidth / video.videoHeight)));
    };
    video.addEventListener("loadedmetadata", measure);
    video.addEventListener("resize", measure);
    return () => { video.removeEventListener("loadedmetadata", measure); video.removeEventListener("resize", measure); };
  }, []);
  return aspect;
}

function AudioBars({ active }: { active: boolean }) {
  return <span class="audio-activity" data-active={active} aria-hidden="true"><i /><i /><i /></span>;
}

export function CallView({ model, actions, page }: { model: CallModel; actions: CallActions; page: HTMLElement | null }) {
  const { role, phase, remoteCamera, remoteMic, micOn, cameraOn, pending, inputErrors } = model;
  const peerName = role === "host" ? "Guest" : "Host";
  const peer = peerName.toLowerCase();
  const present = phase === "connected" || phase === "reconnecting";
  const [corner, setCorner] = useState<Corner>("br");
  const [minimized, setMinimized] = useState(false);
  const [hidden, setHidden] = useState(false);
  const [swapped, setSwapped] = useState(false);
  const [tools, setTools] = useState(false);
  const [overlay, setOverlay] = useState<Overlay>(null);
  const [playRequired, setPlayRequired] = useState(false);
  const [, setResized] = useState(0);
  const call = useRef<HTMLDivElement>(null);
  const peerSurface = useRef<HTMLElement>(null);
  const selfSurface = useRef<HTMLElement>(null);
  const pill = useRef<HTMLButtonElement>(null);
  const dock = useRef<HTMLDivElement>(null);
  const topbar = useRef<HTMLElement>(null);
  const tags = useRef<HTMLDivElement>(null);
  const safeFrame = useRef<HTMLDivElement>(null);
  const segs = { audio: useRef<HTMLDivElement>(null), video: useRef<HTMLDivElement>(null) };
  const toggles = { audio: useRef<HTMLButtonElement>(null), video: useRef<HTMLButtonElement>(null) };
  const chevrons = { audio: useRef<HTMLButtonElement>(null), video: useRef<HTMLButtonElement>(null) };
  const more = useRef<HTMLButtonElement>(null);
  const exit = useRef<HTMLButtonElement>(null);
  const refocus = useRef<(() => HTMLElement | null | undefined) | null>(null);
  const remoteVideo = useVideo(model.incoming);
  const localVideo = useVideo(cameraOn ? model.local : null);
  const remoteAspect = useAspect(remoteVideo);
  const localAspect = useAspect(localVideo);
  const showSwap = swapped && present;
  const tileOpen = !hidden && !minimized;

  const { dragging, handlers } = useTileDrag(call, (next) => { setCorner(next); }, () => setTools((value) => !value));
  // Controls stay up while they are in use: a menu or popover, a drag, the
  // pointer over a control, or keyboard focus in the dock or top bar.
  const idle = useAutoHide(phase === "connected" && !playRequired, () => overlay !== null || dragging ||
    hovered(dock.current) || hovered(topbar.current) || hovered(activeTile()) ||
    keyboardFocus(dock.current) || keyboardFocus(topbar.current));
  // The self view and name tags slide clear of the returning dock once a press on them ends.
  const placedIdle = useHeldIdle(call, ".surface.tile, .pill, .tags", idle);
  const now = useClock(present && model.connectedSince !== null);

  function activeTile(): HTMLElement | null {
    if (hidden) return null;
    if (minimized) return pill.current;
    return showSwap ? peerSurface.current : selfSurface.current;
  }

  // The other person leaving returns the stage to waiting.
  const wasPresent = useRef(present);
  useEffect(() => {
    if (wasPresent.current && !present) {
      setSwapped(false);
      if (phase === "waiting") actions.toast(`The ${peer} left`, "leave");
    }
    wasPresent.current = present;
  }, [present]);

  // Remote playback, with a play control only after an autoplay refusal.
  const playback = remoteCamera || remoteMic;
  useEffect(() => {
    const video = remoteVideo.current;
    const stream = model.incoming;
    setPlayRequired(false);
    if (!video || !stream || !playback) return;
    let current = true;
    function play() {
      if (!video?.paused || !stream?.getTracks().length) return;
      void video.play().catch((failure) => {
        if (current && video.paused && failure.name === "NotAllowedError") setPlayRequired(true);
      });
    }
    play();
    stream.addEventListener("addtrack", play);
    return () => { current = false; stream.removeEventListener("addtrack", play); };
  }, [model.incoming, playback]);

  // Playback starts on the system default; a speaker chosen in prejoin or in
  // the microphone menu is applied here, including a return to the default.
  const routed = useRef("default");
  useEffect(() => {
    if (model.speaker === routed.current) return;
    routed.current = model.speaker;
    void routeSpeaker(remoteVideo.current, model.speaker).then((done) => {
      if (!done) actions.toast("That speaker isn’t available. Sound plays through the system default.", "alert");
    });
  }, [model.speaker]);

  // A new input failure opens its popover; a cleared one closes it.
  const shown = useRef<InputErrors>({});
  useEffect(() => {
    const fresh = dockKinds.find((kind) => inputErrors[kind] && inputErrors[kind] !== shown.current[kind]);
    shown.current = { ...inputErrors };
    if (fresh) setOverlay({ type: "error", kind: fresh });
    else if (overlay?.type === "error" && !inputErrors[overlay.kind]) setOverlay(null);
  }, [inputErrors.audio, inputErrors.video]);

  useLayoutEffect(() => {
    const frame = call.current;
    if (!frame) return;
    for (const surface of [peerSurface.current, selfSurface.current]) {
      // A surface that moved to the stage drops its corner position.
      if (surface && surface.classList.contains("stage")) {
        surface.style.left = surface.style.top = "";
        delete surface.dataset.placed;
      }
    }
    if (dragging) return;
    const width = frame.clientWidth;
    placeOverlays({ root: frame, tile: activeTile(), dock: dock.current, tags: tags.current,
      safe: insets(safeFrame.current, width, frame.clientHeight), corner, idle: placedIdle, compact: width <= 600 });
  });
  // Focus follows a control that the last render moved, such as Swap, which
  // is rebuilt in the other video.
  useLayoutEffect(() => {
    const find = refocus.current;
    refocus.current = null;
    find?.()?.focus({ preventScroll: true });
  });
  useEffect(() => {
    const frame = call.current;
    if (!frame || typeof ResizeObserver === "undefined") return;
    // Size changes reflow through the layout effect above.
    const observer = new ResizeObserver(() => setResized((value) => value + 1));
    observer.observe(frame);
    if (dock.current) observer.observe(dock.current);
    return () => observer.disconnect();
  }, []);

  function closeOverlay(returnFocus: boolean, focus?: HTMLElement | null) {
    setOverlay(null);
    if (returnFocus) focus?.focus({ preventScroll: true });
  }

  function pressInput(kind: InputKind) {
    // Input controls stay focusable while devices open, and ignore presses.
    if (model.busy) return;
    const on = kind === "audio" ? micOn : cameraOn;
    // A failed control explains itself first; Try again or a second press retries.
    if (inputErrors[kind] && !on && !(overlay?.type === "error" && overlay.kind === kind)) {
      setOverlay({ type: "error", kind });
      return;
    }
    if (overlay?.type === "error" && overlay.kind === kind) setOverlay(null);
    actions.toggle(kind);
  }

  function openMenu(menu: InputKind | "more", anchor: HTMLElement | null) {
    if (!anchor) return;
    if (overlay?.type === "menu" && overlay.menu === menu) { closeOverlay(true, anchor); return; }
    // Device menus wait while an input opens, since that request already names its device.
    if (menu !== "more" && model.busy) return;
    if (menu !== "more") actions.refreshDevices();
    setOverlay({ type: "menu", menu, anchor });
  }

  function moveTo(next: Corner) {
    setCorner(next);
    actions.announce(`Self view moved to the ${cornerNames[next]}`);
  }

  function toggleSelfView() {
    const hide = !hidden;
    setHidden(hide);
    setMinimized(false);
    if (hide) setSwapped(false);
    actions.toast(hide ? "Self view hidden. Show it again from More." : "Self view shown", hide ? "eye-off" : "eye");
  }

  function minimize() {
    setSwapped(false);
    setMinimized(true);
    refocus.current = () => pill.current;
  }

  function swap(event: Event) {
    setSwapped((value) => !value);
    if (event.currentTarget instanceof Node && event.currentTarget.contains(document.activeElement)) {
      refocus.current = () => call.current?.querySelector<HTMLElement>(".surface.tile .tile-tools button");
    }
  }

  const exitName = role === "host" ? "End call" : "Leave";
  const routing = speakerRouting(model.native);
  const outputs = model.devices.filter((device) => device.kind === "audiooutput");
  const menuEntries = (menu: InputKind | "more"): MenuEntry[] => {
    if (menu === "video") return deviceEntries(model.devices, "videoinput", model.choices.video.device, (id) => actions.changeDevice("video", id));
    if (menu === "audio") {
      const entries = deviceEntries(model.devices, "audioinput", model.choices.audio.device, (id) => actions.changeDevice("audio", id));
      if (routing && outputs.length) return [...entries, { kind: "separator" }, ...deviceEntries(model.devices, "audiooutput", model.speaker, actions.chooseSpeaker)];
      return model.native ? [...entries, { kind: "note", text: "Sound plays through the system output." }] : entries;
    }
    const entries: MenuEntry[] = [];
    if (role === "host" && model.canCopyInvitation) entries.push({ kind: "item", label: "Copy invitation", icon: "copy", select: actions.copyInvitation });
    entries.push({ kind: "item", label: hidden ? "Show self view" : "Hide self view", icon: hidden ? "eye" : "eye-off", select: toggleSelfView });
    return entries;
  };

  const inputState = `Camera ${cameraOn ? "on" : "off"} · Mic ${micOn ? "on" : "off"}`;
  const remoteState = present ? `Camera ${remoteCamera ? "on" : "off"} · Mic ${remoteMic ? "on" : "off"}${model.playbackMuted ? " · Sound off" : ""}` : "";
  const bothOff = !micOn && !cameraOn && !pending.audio && !pending.video;
  const until = invitationUntil(model.expiresAt);
  const status = phase === "waiting" ? (role === "host" ? "Waiting for your guest" : "Waiting for the host")
    : phase === "connecting" ? "Connecting…" : phase === "reconnecting" ? "Reconnecting…"
    : model.connectedSince === null ? "Connected" : duration(Math.max(0, Math.round((now - model.connectedSince) / 1000)));

  const tileTools = <div class="tile-tools">
    {present && <button type="button" aria-label="Swap with the other video" data-tip="Swap videos"
      onClick={swap}><Icon name="swap" /></button>}
    <button type="button" aria-label="Minimize self view" data-tip="Minimize" onClick={minimize}><Icon name="collapse" /></button>
  </div>;
  const tileProps = (small: boolean) => small ? {
    tabIndex: 0, "aria-describedby": "tile-hint", "data-tools": tools ? "on" : undefined, ...handlers,
    onKeyDown: (event: KeyboardEvent) => {
      if (event.target !== event.currentTarget) return;
      const next = cornerFor(event.key, corner);
      if (!next) return;
      event.preventDefault();
      if (next !== corner) moveTo(next);
    },
  } : {};
  const peerSmall = showSwap;
  const selfSmall = !showSwap;

  return <div ref={call} class="call" data-phase={phase} data-idle={idle ? "" : undefined} data-dragging={dragging ? "" : undefined}>
    <p class="visually-hidden" id="tile-hint">Drag it, or use the arrow keys, to move it to another corner.</p>

    <section ref={peerSurface} class={`surface peer ${peerSmall ? "tile" : "stage"}`} aria-label={peerName}
      data-camera={remoteCamera && present ? "on" : "off"} data-frozen={phase === "reconnecting"}
      hidden={peerSmall && !tileOpen} style={{ "--tile-aspect": String(remoteAspect) }} {...tileProps(peerSmall)}>
      {remoteState && <p class="visually-hidden">{remoteState}</p>}
      <video id="remote-video" ref={remoteVideo} autoPlay playsInline muted={model.playbackMuted} aria-hidden={!remoteCamera}
        aria-label={`${peerName}'s video`} onPlaying={() => setPlayRequired(false)} />
      {present && !remoteCamera && (peerSmall
        ? <div class="tile-off"><span class="avatar" aria-hidden="true">{peerName[0]}</span><p>Camera off</p></div>
        : <div class="stage-avatar" data-speaking={incomingActive(model)}>
          <span class="avatar" aria-hidden="true">{peerName[0]}</span>
          <span class="avatar-name">{peerName}</span><span class="avatar-sub">Camera off</span>
        </div>)}
      {!present && <div class="stage-card"><div class="card">
        {phase === "connecting" ? <>
          <span class="spinner large" aria-hidden="true" />
          <p class="card-title" aria-hidden="true">Connecting to {role === "host" ? "your guest" : "the host"}…</p>
          <p>This usually takes a few seconds.</p>
        </> : role === "host" ? <>
          <span class="badge-icon" aria-hidden="true"><Icon name="link" /></span>
          <p class="card-title" aria-hidden="true">Waiting for your guest</p>
          {model.canCopyInvitation ? <>
            <p>Send them the invitation link.{until && ` It works until ${until}.`}</p>
            <button id="copy-invitation" class="btn primary" type="button" onClick={actions.copyInvitation}>
              <Icon name="copy" /><span>Copy invitation</span>
            </button>
          </> : model.native ? <p>Check the guest link in Camstream before sharing it.</p>
            : <p>Copy the invitation in Camstream on your Mac and send it to your guest.{until && ` It works until ${until}.`}</p>}
        </> : <>
          <span class="badge-icon muted" aria-hidden="true"><Icon name="clock" /></span>
          <p class="card-title" aria-hidden="true">Waiting for the host</p>
          <p>They’ll appear here as soon as they join.</p>
        </>}
      </div></div>}
      {peerSmall && <>
        <span class="tile-label">{remoteMic && <AudioBars active={model.incomingAudio === true} />}<span>{peerName}</span>
          {!remoteMic && <Icon name="mic-off" class="mic-off" label="Microphone off" />}</span>
        {tileTools}
      </>}
    </section>

    <section ref={selfSurface} class={`surface self ${selfSmall ? "tile" : "stage"}`} aria-label="You"
      data-camera={cameraOn ? "on" : "off"} hidden={selfSmall && !tileOpen}
      style={{ "--tile-aspect": String(localAspect) }} {...tileProps(selfSmall)}>
      <p class="visually-hidden">{inputState}</p>
      <video id="local-video" ref={localVideo} autoPlay playsInline muted aria-hidden={!cameraOn} aria-label="Your video" />
      {!cameraOn && (selfSmall ? <div class="tile-off">
        {pending.video ? <><span class="spinner" aria-hidden="true" /><p>Starting camera…</p></>
          : bothOff ? <><p>Camera and mic are off</p>
            <button id="call-enable-inputs" class="btn glass xs" type="button" aria-label="Turn on camera and mic"
              aria-disabled={model.busy} onClick={(event) => {
                if (model.busy) return;
                // The button gives way to progress, so focus stays on your video.
                handOff(event.currentTarget, selfSurface.current);
                actions.enableBoth();
              }}>Turn on</button></>
          : <><span class="avatar you" aria-hidden="true"><Icon name="user" /></span><p>Camera off</p></>}
      </div> : <div class="stage-avatar">
        <span class="avatar you" aria-hidden="true"><Icon name="user" /></span>
        <span class="avatar-name">You</span><span class="avatar-sub">Your camera is off</span>
      </div>)}
      {selfSmall && <>
        <span class="tile-label"><span>You</span>{!micOn && <Icon name="mic-off" class="mic-off" label="Microphone off" />}</span>
        {tileTools}
      </>}
    </section>

    {minimized && !hidden && <button ref={pill} type="button" class="pill" aria-label="Show self view"
      onClick={() => { setMinimized(false); refocus.current = () => selfSurface.current; }}>
      <span class="mini" aria-hidden="true"><Icon name="user" /></span><span>You</span>
      {!micOn && <Icon name="mic-off" class="mic-off" />}<Icon name="expand" />
    </button>}

    {playRequired && <div class="stage-play">
      <button type="button" aria-labelledby="play-label" onClick={() => {
        void remoteVideo.current?.play().then(() => setPlayRequired(false)).catch(() => {});
      }}><Icon name="play" /></button>
      <strong id="play-label">Play incoming audio and video</strong>
      <span>Your browser paused incoming sound until you interact with the page.</span>
    </div>}

    {present && <div ref={tags} class="tags">
      {showSwap ? <span class="tag"><span>You</span>{!micOn && <Icon name="mic-off" class="mic-off" label="Microphone off" />}</span>
        : <span class="tag">{remoteMic && <AudioBars active={model.incomingAudio === true} />}<span>{peerName}</span>
          {!remoteMic && <Icon name="mic-off" class="mic-off" label="Microphone off" />}</span>}
      {model.playbackMuted && <button type="button" class="chip-warn" onClick={() => actions.setPlaybackMuted(false)}
        aria-label="Sound is off on this device. Turn it on."><Icon name="sound-off" /><span>Sound off on this device</span></button>}
    </div>}

    <header ref={topbar} class="topbar">
      <div class="topbar-start">
        {!model.native && <span class="wordmark">Camstream</span>}
        <span class="status" data-kind={phase} title={phase === "connected" ? `Connected with the ${peer}` : undefined}>
          <span class="dot" aria-hidden="true" /><span>{status}</span>
        </span>
      </div>
      <div class="topbar-end">
        {present && <button id="call-sound" type="button" class={`icon-button${model.playbackMuted ? " is-off" : ""}`}
          aria-label="Sound" aria-pressed={!model.playbackMuted}
          data-tip={model.playbackMuted ? "Turn sound on" : `Mute the ${peer} on this device`}
          onClick={() => actions.setPlaybackMuted(!model.playbackMuted)}>
          <Icon name={model.playbackMuted ? "sound-off" : "sound"} />
        </button>}
      </div>
    </header>

    <div ref={dock} class="dock" role="group" aria-label="Call controls">
      {dockKinds.map((kind) => {
        const on = kind === "audio" ? micOn : cameraOn;
        const name = inputNames[kind];
        const failed = !!inputErrors[kind] && !on;
        const options = kind === "audio" ? (routing && outputs.length ? "Microphone and speaker options" : "Microphone options") : "Camera options";
        return <div key={kind} ref={segs[kind]} class="seg split" data-on={on || !!pending[kind]} data-busy={!!pending[kind]} data-error={failed}>
          <button ref={toggles[kind]} id={kind === "audio" ? "call-mic" : "call-camera"} type="button" class="control toggle"
            aria-pressed={on} aria-label={capitalized(name)} aria-disabled={model.busy}
            data-tip={pending[kind] ? `Starting ${name}…` : failed ? `${capitalized(name)} needs attention` : `Turn ${on ? "off" : "on"} ${name}`}
            onClick={() => pressInput(kind)}>
            <Icon name={kind === "audio" ? (on ? "mic" : "mic-off") : (on ? "camera" : "camera-off")} />
            {failed && <span class="badge" aria-hidden="true">!</span>}
          </button>
          <button ref={chevrons[kind]} type="button" class="control chevron" aria-label={options} aria-haspopup="menu"
            aria-expanded={overlay?.type === "menu" && overlay.menu === kind} aria-disabled={model.busy}
            data-tip={kind === "audio" ? "Microphone" : "Camera"}
            onClick={() => openMenu(kind, chevrons[kind].current)}><Icon name="chev-up" /></button>
        </div>;
      })}
      <div class="seg">
        <button ref={more} type="button" class="control" aria-label="More options" aria-haspopup="menu"
          aria-expanded={overlay?.type === "menu" && overlay.menu === "more"} data-tip="More"
          onClick={() => openMenu("more", more.current)}><Icon name="more" /></button>
      </div>
      <span class="rule" aria-hidden="true" />
      <button ref={exit} id="leave-call" type="button" class="end" aria-label={exitName} disabled={model.leaving}
        data-tip={role === "host" ? "End for both of you" : "Leave the call"}
        onClick={() => role === "host" ? setOverlay(overlay?.type === "end" ? null : { type: "end" }) : actions.leave()}>
        <Icon name="end" /><span>{role === "host" ? "End" : "Leave"}</span>
      </button>
    </div>

    <div ref={safeFrame} class="safe-frame" aria-hidden="true" />

    {page && overlay?.type === "menu" && <Menu label={overlay.anchor.getAttribute("aria-label") ?? ""} anchor={overlay.anchor}
      root={page} placement="above" entries={menuEntries(overlay.menu)} close={(back) => closeOverlay(back, overlay.anchor)} />}
    {page && overlay?.type === "error" && segs[overlay.kind].current && inputErrors[overlay.kind] && (() => {
      const kind = overlay.kind;
      const copy = failureCopy(kind, inputErrors[kind]!, model.native);
      return <Popover anchor={segs[kind].current!} root={page} title={copy.title} describedBy="input-problem"
        close={(back) => closeOverlay(back, toggles[kind].current)}>
        <p class="pop-head"><Icon name="alert" /><strong>{copy.title}</strong></p>
        <p id="input-problem">{copy.text}</p>
        <div class="pop-actions">
          <button class="btn primary sm" type="button" aria-disabled={model.busy} onClick={() => {
            if (model.busy) return;
            closeOverlay(true, toggles[kind].current);
            actions.retry(kind);
          }}>Try again</button>
          <button class="btn quiet sm" type="button" aria-disabled={model.busy} onClick={() => {
            if (model.busy) return;
            actions.refreshDevices();
            setOverlay(chevrons[kind].current ? { type: "menu", menu: kind, anchor: chevrons[kind].current } : null);
          }}>Choose {inputNames[kind]}</button>
        </div>
      </Popover>;
    })()}
    {page && overlay?.type === "end" && exit.current && <EndConfirm anchor={exit.current} root={page} peerPresent={present}
      close={(back) => closeOverlay(back, exit.current)} end={() => { setOverlay(null); actions.leave(); }} />}
  </div>;
}

function incomingActive(model: CallModel) {
  return model.remoteMic && model.incomingAudio === true;
}

function hovered(element: Element | null | undefined) {
  try { return !!element?.matches(":hover"); }
  catch { return false; }
}

/** Focus that came from the keyboard; a clicked button does not hold the controls up. */
function keyboardFocus(element: Element | null | undefined) {
  const focused = document.activeElement;
  if (!element || !focused || !element.contains(focused)) return false;
  try { return focused.matches(":focus-visible"); }
  catch { return true; }
}

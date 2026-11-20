import { useRef, useState } from "preact/hooks";
import type { RoomDownload, RoomRole } from "../services/contracts";
import type { InputKind } from "../services/conversation-media";
import { EndConfirm, useVideo } from "./call-view";
import {
  capitalized, deviceEntries, deviceName, dockKinds, failureCopy, inputNames, speakerRouting,
  type Choices, type InputErrors, type Pending,
} from "./devices";
import { Icon, type IconName } from "./icons";
import { handOff } from "./layout";
import { Menu, type MenuEntry } from "./overlays";

export type PrejoinModel = {
  role: RoomRole; native: boolean; headline: string;
  /** Set when the room is full: the reason Join now reads Try again. */
  notice: string; origin: string; peerPresent: boolean;
  choices: Choices; devices: MediaDeviceInfo[]; speaker: string;
  local: MediaStream | null; cameraLive: boolean; micLive: boolean;
  pending: Pending; inputErrors: InputErrors; busy: boolean; joining: boolean; error: string;
  exit: boolean; leaving: boolean;
  mac: { appURL: string; download: RoomDownload | null } | null;
};

export type PrejoinActions = {
  toggle(kind: InputKind): void; preview(): void; retry(kind: InputKind): void; join(): void;
  changeDevice(kind: InputKind, id: string): void; chooseSpeaker(id: string): void; refreshDevices(): void;
  leave(): void; openApp(): void; copyLink(): void;
};

/** What Join will do. It describes the Join button. */
export function joinSummary(camera: boolean, mic: boolean) {
  if (camera && mic) return "You’ll join with your camera and mic on.";
  if (camera) return "You’ll join with your camera on and mic off.";
  if (mic) return "You’ll join with your mic on and camera off.";
  return "You’ll join with your camera and mic off. You can turn them on in the call.";
}

type Picker = InputKind | "speaker";
const pickerKinds: Record<Picker, MediaDeviceKind> = { video: "videoinput", audio: "audioinput", speaker: "audiooutput" };
const pickerNames: Record<Picker, string> = { video: "Camera", audio: "Microphone", speaker: "Speaker" };
const pickerIcons: Record<Picker, IconName> = { video: "camera", audio: "mic", speaker: "sound" };

/**
 * One preview with the inputs Join will use, the devices behind them and a
 * single Join. Nothing is captured until Preview or Join.
 */
export function Prejoin({ model, actions, page }: { model: PrejoinModel; actions: PrejoinActions; page: HTMLElement | null }) {
  const { choices, pending, inputErrors, busy } = model;
  const [menu, setMenu] = useState<{ picker: Picker; anchor: HTMLElement } | null>(null);
  const [confirm, setConfirm] = useState(false);
  const exit = useRef<HTMLButtonElement>(null);
  const toggles = { video: useRef<HTMLButtonElement>(null), audio: useRef<HTMLButtonElement>(null) };
  const video = useVideo(model.cameraLive ? model.local : null);
  const camera = choices.video.selected;
  const mic = choices.audio.selected;
  const cameraProblem = camera && inputErrors.video ? failureCopy("video", inputErrors.video, model.native) : null;
  const micProblem = mic && inputErrors.audio ? failureCopy("audio", inputErrors.audio, model.native) : null;
  const starting = pending.video ? "Starting camera…" : pending.audio && !model.cameraLive ? "Starting microphone…" : "";
  const state = cameraProblem ? "error" : starting ? "busy" : !camera ? "off" : model.cameraLive ? "live" : "idle";
  const pickers: Picker[] = speakerRouting(model.native) ? ["video", "audio", "speaker"] : ["video", "audio"];

  function choose(picker: Picker, id: string) {
    if (picker === "speaker") actions.chooseSpeaker(id);
    else actions.changeDevice(picker, id);
  }

  function chosen(picker: Picker) {
    return picker === "speaker" ? model.speaker : choices[picker].device;
  }

  function openPicker(picker: Picker, anchor: HTMLElement) {
    if (menu?.picker === picker) { setMenu(null); return; }
    if (busy) return;
    actions.refreshDevices();
    setMenu({ picker, anchor });
  }

  /**
   * Busy controls stay focusable and ignore presses. One that is replaced
   * while its action runs first hands focus to the input toggle it affects.
   */
  function press(event: Event, kind: InputKind | null, action: () => void) {
    if (busy) return;
    if (kind) handOff(event.currentTarget, toggles[kind].current);
    action();
  }

  const entries = (picker: Picker): MenuEntry[] =>
    deviceEntries(model.devices, pickerKinds[picker], chosen(picker), (id) => choose(picker, id));

  // Floating parts sit outside the scrolling page, which closes them when it scrolls.
  return <>
    <div class="prejoin" onScroll={() => { setMenu(null); setConfirm(false); }}>
      {!model.native && <header class="pj-top"><span class="wordmark">Camstream</span></header>}
      <div class="pj-body">
        <div class="pj-preview-col">
          <section class="preview" aria-label="You" data-state={state}>
            <video id="local-video" ref={video} autoPlay playsInline muted aria-hidden={!model.cameraLive} aria-label="Your video" />
            {state !== "live" && <div class="preview-empty">
              {state === "busy" ? <span class="spinner large" aria-hidden="true" /> :
                <span class="avatar you" aria-hidden="true"><Icon name="user" /></span>}
              {state === "error" && cameraProblem ? <div role="alert">
                <p class="preview-msg">{cameraProblem.title}</p>
                <p class="preview-sub" id="preview-problem">{cameraProblem.text}</p>
              </div> : <>
                <p class="preview-msg">{state === "busy" ? starting : state === "off" ? "Your camera is off" : "Camera preview is off"}</p>
                {state === "idle" && <p class="preview-sub">Nothing is captured until you preview or join.</p>}
                {state === "off" && model.micLive && <p class="preview-sub">Your microphone is on.</p>}
              </>}
              {state === "error" && <button class="btn glass sm" type="button" aria-disabled={busy} aria-describedby="preview-problem"
                onClick={(event) => press(event, "video", () => actions.retry("video"))}>Try again</button>}
              {state === "idle" && <button class="btn glass sm" type="button" aria-disabled={busy}
                onClick={(event) => press(event, "video", actions.preview)}>
                <Icon name="camera" /><span>{mic ? "Preview camera and mic" : "Preview camera"}</span>
              </button>}
            </div>}
            <span class="preview-you" aria-hidden="true">You</span>
            <div class="preview-toggles" role="group" aria-label="Join with">
              {dockKinds.map((kind) => {
                const on = choices[kind].selected;
                const name = inputNames[kind];
                const icon: IconName = kind === "audio" ? (on ? "mic" : "mic-off") : (on ? "camera" : "camera-off");
                return <button key={kind} ref={toggles[kind]} id={kind === "audio" ? "prejoin-mic" : "prejoin-camera"} type="button"
                  class="round" aria-pressed={on} aria-label={capitalized(name)} aria-disabled={busy} data-error={on && !!inputErrors[kind]}
                  data-tip={`Turn ${on ? "off" : "on"} ${name}`} onClick={(event) => press(event, null, () => actions.toggle(kind))}>
                  <Icon name={icon} />{on && inputErrors[kind] && <span class="badge" aria-hidden="true">!</span>}
                </button>;
              })}
            </div>
          </section>
          {micProblem && <p class="pj-problem" role="alert">
            <Icon name="alert" /><span><strong>{micProblem.title}.</strong> {micProblem.text}</span>
            <button class="linkbtn" type="button" aria-disabled={busy}
              onClick={(event) => press(event, "audio", () => actions.retry("audio"))}>Try again</button>
          </p>}
          <div class="pickers" role="group" aria-label="Devices">
            {pickers.map((picker) => <button key={picker} type="button" class="picker" aria-haspopup="menu"
              aria-expanded={menu?.picker === picker} aria-disabled={busy}
              onClick={(event) => openPicker(picker, event.currentTarget)}>
              <Icon name={pickerIcons[picker]} />
              <span class="visually-hidden">{pickerNames[picker]}:</span>{" "}
              <span class="picker-name">{deviceName(model.devices, pickerKinds[picker], chosen(picker))}</span>
              <Icon name="chev-down" />
            </button>)}
          </div>
        </div>
        <div class="pj-join">
          <p class="pj-title" aria-hidden="true">{model.headline}</p>
          {model.notice ? <p class="pj-context warn" role="alert">{model.notice}</p> :
            <p class="pj-context">One-to-one conversation{model.origin && <> · invitation from <strong>{model.origin}</strong></>}</p>}
          {model.error && <p class="pj-error" role="alert">{model.error}</p>}
          <button id="join-call" class="btn primary xl" type="button" aria-disabled={busy} aria-describedby="join-summary"
            onClick={(event) => press(event, null, actions.join)}>
            {model.joining ? "Joining…" : model.notice ? "Try again" : "Join"}
          </button>
          <p class="pj-summary" id="join-summary">{joinSummary(camera, mic)}</p>
          {model.exit && <button ref={exit} id="leave-call" class="btn quiet sm" type="button" disabled={model.leaving}
            onClick={() => model.role === "host" ? setConfirm((open) => !open) : actions.leave()}>
            {model.role === "host" ? "End conversation" : "Leave"}
          </button>}
        </div>
      </div>
      {model.mac && <section class="pj-foot" aria-label="Mac app options">
        <span>Prefer the Mac app?</span>
        <a class="linkbtn" href={model.mac.appURL} onClick={actions.openApp}>Open in Camstream</a>
        {model.mac.download && <>
          <span class="sep" aria-hidden="true">·</span>
          <a class="linkbtn" href={model.mac.download.url} rel="noreferrer" referrerPolicy="no-referrer">
            Download Camstream for Mac {model.mac.download.version}
          </a>
        </>}
        <span class="sep" aria-hidden="true">·</span>
        <button class="linkbtn" type="button" onClick={actions.copyLink}>Copy invitation link</button>
      </section>}
    </div>
    {page && menu && <Menu label={pickerNames[menu.picker]} anchor={menu.anchor} root={page} placement="below"
      entries={entries(menu.picker)} close={(back) => { setMenu(null); if (back) menu.anchor.focus({ preventScroll: true }); }} />}
    {page && confirm && exit.current && <EndConfirm anchor={exit.current} root={page} peerPresent={model.peerPresent}
      close={(back) => { setConfirm(false); if (back) exit.current?.focus({ preventScroll: true }); }}
      end={() => { setConfirm(false); actions.leave(); }} />}
  </>;
}

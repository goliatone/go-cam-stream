import { useEffect, useRef, useState } from "preact/hooks";
import type { CameraSummary, State } from "../services/contracts";
import { draftWarnings, managementMessage } from "../services/management-copy";
import { openDialog } from "../services/shortcuts";
import type { ManagementDialogProps } from "./camera-management";
import { ConnectionCheck } from "./connection-check";
import { CloseIcon } from "./icon";
import { describeView, mediaLabel } from "./status";

/** What the page knows about the camera the drawer changes. */
export interface CameraSettingsContext {
  camera?: CameraSummary | undefined;
  /** People watching any of its streams, once the server says. */
  viewers?: number | undefined;
  /** This page plays the camera, in one of those viewer slots. */
  watching?: boolean | undefined;
  /** The camera's state as this page plays it, or as the server last saw it. */
  state?: State | undefined;
  /** Ends this page's playback of the camera. */
  stopWatching?: (() => void) | undefined;
  /** Opens Reconnect for the camera's source; set only when the source needs it. */
  reconnect?: (() => void) | undefined;
}

type Section = "name" | "connection" | "remove";
const people = (n: number) => n === 1 ? "1 person" : `${n} people`;
/** "3 people are watching", or "You’re watching" when this page is the only viewer. */
const watchers = (n: number, mine?: boolean) =>
  mine && n === 1 ? "You’re watching" : `${people(n)} ${n === 1 ? "is" : "are"} watching`;
const LOCAL = /^rtsps?:\/\/(localhost|127\.0\.0\.1|\[::1\])([:/]|$)/i;

// A saved camera's settings, in a drawer beside the video it shows. The manager
// who added the camera renames it, moves it to another address, changes its
// sign-in or removes it; anyone else reads its details. A new address or
// sign-in and a removal replace the camera's source, so they wait until no one
// watches it in any browser, or stop everyone's playback after a confirmation.
// The drawer says how many watch before anything is tried.
export function SettingsDrawer({ view, actions, settings = {} }: ManagementDialogProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const video = useRef<HTMLVideoElement>(null);
  const prompt = useRef<HTMLDivElement>(null);
  const asked = useRef<string | undefined>(undefined);
  const { camera, viewers, watching, state, stopWatching, reconnect } = settings;
  const draft = view.draft, saved = view.settings;
  // The section whose action ran last shows its progress and refusals.
  const [last, setLast] = useState<Section>("connection");
  // The confirmation on show: stopping everyone's playback, or the removal.
  const [ask, setAsk] = useState<"stop" | "remove">();
  const [address, setAddress] = useState<string>();
  const name = camera?.name ?? draft?.name ?? view.credentialTarget?.name ?? saved?.name;
  const error = !view.storageReady ? "recovery_storage_unavailable" : view.error;
  // A change whose outcome is unknown replaces the forms until it is known.
  const stuck = Boolean(view.unresolved && !view.busy) || view.phase === "recovering" && !draft;
  const testing = view.phase === "testing" || draft?.state === "cleaning" || draft?.state === "cleanup_failed";
  const blocked = Boolean(view.busy || view.unresolved || !view.storageReady) || testing;
  // Server cameras and other managers' are read-only. A restored change shows
  // before the camera list does.
  const editable = camera ? Boolean(camera.can_update_credentials) : Boolean(draft?.credential_update || view.saved);
  const editing = editable && !stuck && !view.saved;
  // This page counts among the viewers even before the server's count shows it.
  const count = viewers === undefined ? undefined : Math.max(viewers, watching ? 1 : 0);
  const fenced = Boolean(draft?.fenced);
  // The address on show: a draft's new one, else the saved one. What is sent
  // is the new address, or empty while the saved one stays.
  const shownAddress = draft?.credential_update && draft.endpoint || saved?.endpoint;
  const typed = (address ?? shownAddress ?? "").trim();
  const endpoint = typed !== (saved?.endpoint ?? "") ? typed : "";
  // A draft for this address that holds a sign-in is checked again as is.
  const kept = Boolean(draft?.credentials_present) && draft?.endpoint === endpoint;
  const anonymous = saved?.authentication === "anonymous";
  const pass = kept ? "Leave blank to check the password you entered again." : endpoint && !anonymous ? "Leave blank to keep the saved password." : "";
  const status = testing ? "Checking the connection…" : view.phase === "saving" ? "Saving the change…"
    : view.phase === "recovering" ? "Checking your last change…" : !view.busy ? ""
    : last === "name" ? "Saving the name…" : last === "remove" ? "Removing the camera…" : "Starting the check…";
  const message = error && (error === "camera_busy"
    ? "Someone is watching this camera, or another change to it is running. Wait until it’s free, or stop everyone’s playback."
    : managementMessage(error));
  // Progress and refusals show beside the action that caused them.
  const note = (at: Section, done = "") => <>
    <div role="status" aria-live="polite">{last === at && status || done}</div>
    {last === at && message && <p class="management-error" role="alert">{message}</p>}
  </>;
  useEffect(() => {
    const before = document.activeElement;
    openDialog(dialog.current);
    return () => { if (before instanceof HTMLElement && before.isConnected) before.focus(); };
  }, []);
  // A confirmation replaces the button that opens it, takes focus, and gives
  // it back to that button once it closes.
  useEffect(() => {
    if (ask) { asked.current = ask; prompt.current?.querySelector("button")?.focus(); }
    else if (asked.current) dialog.current?.querySelector<HTMLElement>(`[data-ask="${asked.current}"]`)?.focus();
  }, [ask]);
  // At the end, focus moves on to the drawer's next step.
  const end = Boolean(view.saved || view.change?.removed);
  useEffect(() => { if (end) dialog.current?.querySelector<HTMLElement>(".dialog-inner > .dialog-actions button")?.focus(); }, [end]);
  const clearSecrets = () => {
    for (const field of ["username", "password"]) {
      const input = form.current?.elements.namedItem(field);
      if (input instanceof HTMLInputElement) input.value = "";
    }
  };
  const close = () => { clearSecrets(); actions.close(); };
  // One press checks and saves what was typed. With stop, everyone's playback
  // ends first.
  const run = (stop = false) => {
    const f = form.current;
    if (!f || blocked || !video.current || !f.reportValidity()) return;
    const data = new FormData(f);
    const username = String(data.get("username") ?? ""), password = String(data.get("password") ?? "");
    setLast("connection");
    setAsk(undefined);
    actions.checkAndSave?.(video.current, username || password ? { username, password } : undefined, endpoint, stop);
    clearSecrets();
  };
  const others = Boolean(count && count > (watching ? 1 : 0));
  return <dialog ref={dialog} class="dialog management-dialog drawer" aria-labelledby="settings-title settings-camera" onCancel={(event) => { event.preventDefault(); if (ask) setAsk(undefined); else close(); }}>
    <div class="dialog-inner">
      <div class="dialog-head"><h2 id="settings-title">Settings</h2><button type="button" class="icon-button" aria-label="Close settings" onClick={close}><CloseIcon /></button></div>
      {name && <p id="settings-camera" class="management-intro">{name}</p>}
      {view.change?.removed ? <p class="callout" data-tone="success" role="status">The camera is removed, with its saved address and sign-in.</p> : <>
        {camera && !editable && <p class="callout">Only the server’s configuration, or the manager who added this camera, can change it.</p>}
        {stuck && <>
          <div role="status" aria-live="polite">{status}</div>
          {message && <p class="management-error" role="alert">{message}</p>}
          {view.unresolved && !error && !view.busy && <p>Your last change isn’t confirmed yet. Check again before making another.</p>}
        </>}
        {editing && <section class="drawer-section" aria-labelledby="settings-name">
          <h3 id="settings-name">Name</h3>
          <form class="drawer-row" onSubmit={(event) => {
            event.preventDefault();
            if (blocked) return;
            setLast("name");
            actions.rename?.(String(new FormData(event.currentTarget).get("name") ?? ""));
          }}>
            <input name="name" aria-labelledby="settings-name" required maxLength={120} autoComplete="off" defaultValue={saved?.name ?? name} disabled={blocked || !saved} />
            <button type="submit" class="secondary-button" disabled={blocked || !saved}>Save name</button>
          </form>
          {note("name", view.change ? "Name saved." : "")}
        </section>}
        <section class="drawer-section" aria-labelledby="settings-connection">
          <h3 id="settings-connection">Connection</h3>
          {camera && <dl class="drawer-facts">
            <div><dt>Status</dt><dd>{describeView({ state: state ?? camera.availability }).label}</dd></div>
            {saved?.dial_address && <div><dt>Connects to</dt><dd>{saved.dial_address}</dd></div>}
          </dl>}
          {reconnect && <button type="button" class="secondary-button" onClick={reconnect}>Reconnect</button>}
          {view.saved ? <p class="callout" data-tone="success" role="status">{`The camera now uses the new ${draft?.endpoint ? "address" : draft ? "sign-in" : "settings"}.`}</p> : editing && <>
            {ask === "stop" && count ? <div ref={prompt} class="callout" data-tone="warning" role="group" aria-labelledby="settings-stop-ask">
              <p id="settings-stop-ask">{`Stop playback for ${people(count)}${watching ? ", including you" : ""}? Their video stops in every browser and can start again once this change is saved or canceled.`}</p>
              <div class="drawer-row">
                <button type="button" class="danger-button" disabled={blocked} onClick={() => run(true)}>Stop playback and save</button>
                <button type="button" class="secondary-button" onClick={() => setAsk(undefined)}>Cancel</button>
              </div>
            </div> : <div class="callout" data-tone={count && !fenced ? "warning" : "info"}>
              <p id="settings-watchers" role="status">{fenced ? "Playback is stopped in every browser until this change is saved or canceled."
                : count === undefined ? "A new address or sign-in can be saved only while no one is watching this camera, in any browser."
                : !count ? "No one is watching this camera, so a new address or sign-in can be saved now."
                : others ? `${watchers(count, watching)} this camera${watching ? ", including you" : ""}. A new address or sign-in can be saved once no one is, or after you stop everyone’s playback.`
                : "You’re watching this camera. Stop watching to save a new address or sign-in."}</p>
              {/* The button goes once playback stops; focus moves on to the form. */}
              {watching && stopWatching && !fenced && <button type="button" class="secondary-button" onClick={() => { stopWatching(); form.current?.querySelector<HTMLElement>("input:not(:disabled)")?.focus(); }}>Stop watching here</button>}
              {others && !fenced && <button type="button" class="danger-button" data-ask="stop" disabled={blocked} onClick={() => setAsk("stop")}>Stop everyone’s playback…</button>}
            </div>}
            <form id="settings-form" ref={form} onSubmit={(event) => { event.preventDefault(); if (!count || fenced) run(); }}>
              <fieldset disabled={blocked}>
                <label>Address <input name="endpoint" maxLength={2048} autoComplete="off" spellcheck={false} defaultValue={shownAddress} disabled={!saved}
                  aria-describedby={LOCAL.test(endpoint) ? "settings-address-hint settings-local-hint" : "settings-address-hint"} onInput={(event) => setAddress(event.currentTarget.value)} /></label>
                <p id="settings-address-hint" class="hint">An address the server can reach, without a username or password.</p>
                {LOCAL.test(endpoint) && <p id="settings-local-hint" class="hint">{draftWarnings.server_relative_localhost}</p>}
                <label>Username <input name="username" autoComplete="off" maxLength={1024} aria-describedby="settings-username-hint" /></label>
                <p id="settings-username-hint" class="hint">{anonymous ? "This camera connects without a sign-in. Leave both blank to keep it that way." : "Leave blank to keep the saved username."}</p>
                <label>New password <input name="password" type="password" autoComplete="new-password" maxLength={4096} required={!kept && !endpoint} aria-describedby={pass ? "settings-password-hint" : undefined} /></label>
                {pass && <p id="settings-password-hint" class="hint">{pass}</p>}
              </fieldset>
            </form>
            <div class="drawer-row">
              <button class="primary-button" type="submit" form="settings-form" disabled={blocked || Boolean(count) && !fenced} aria-describedby="settings-watchers">{testing ? "Checking…" : view.phase === "saving" ? "Saving…" : "Check and save"}</button>
              {testing && <button type="button" class="secondary-button" disabled={view.unresolved} onClick={actions.cancelTest}>Stop check</button>}
              {/* A browser that blocks autoplay plays the preview on a press. */}
              {testing && view.test?.source_media_observed && <button type="button" class="secondary-button" onClick={() => { void video.current?.play().catch(() => {}); }}>Play preview</button>}
            </div>
            {note("connection")}
          </>}
          {editable && !view.saved && <ConnectionCheck test={view.test} active={testing} video={video} hidden={!testing && !view.test} />}
        </section>
        {camera && <section class="drawer-section" aria-labelledby="settings-about">
          <h3 id="settings-about">About</h3>
          <dl class="drawer-facts">
            <div><dt>{camera.streams.length > 1 ? "Streams" : "Stream"}</dt><dd>{camera.streams.map(item => item.name).join(", ")}</dd></div>
            <div><dt>Media</dt><dd>{[...new Set(camera.streams.map(item => mediaLabel(item.capabilities)))].join(", ")}</dd></div>
          </dl>
        </section>}
        {editing && <section class="drawer-section" aria-labelledby="settings-remove">
          <h3 id="settings-remove">Remove camera</h3>
          {ask === "remove" ? <div ref={prompt} class="callout" data-tone="warning" role="group" aria-labelledby="settings-remove-ask">
            <p id="settings-remove-ask">{`Remove ${name}? It leaves every camera list, and its saved address and sign-in are deleted.${count ? ` ${watchers(count, watching)} it${watching && count > 1 ? ", including you" : ""}, and removing it stops that playback.` : ""}`}</p>
            <div class="drawer-row">
              <button type="button" class="danger-button" disabled={blocked} onClick={() => { setLast("remove"); setAsk(undefined); actions.remove?.(Boolean(count)); }}>{count ? "Stop playback and remove" : "Remove camera"}</button>
              <button type="button" class="secondary-button" onClick={() => setAsk(undefined)}>Cancel</button>
            </div>
          </div> : <button type="button" class="danger-button" data-ask="remove" disabled={blocked || !saved} onClick={() => setAsk("remove")}>Remove camera…</button>}
          {note("remove")}
        </section>}
      </>}
      <div class="dialog-actions">
        {view.saved ? <>
          <button class="primary-button" type="button" onClick={() => actions.watch(view.saved!.camera_id)}>Watch now</button>
          <button type="button" class="secondary-button" onClick={close}>Close</button>
        </> : view.change?.removed ? <button type="button" class="secondary-button" onClick={close}>Close</button> : stuck && <>
          {view.storageReady && !view.busy && <button type="button" class="secondary-button" onClick={actions.recover}>Check again</button>}
          {view.canRetrySave && <button type="button" class="secondary-button" disabled={view.busy} onClick={actions.retrySave}>Try saving again</button>}
          {view.canAbandon && <button type="button" class="danger-button" onClick={actions.abandon}>Discard changes</button>}
        </>}
      </div>
    </div>
  </dialog>;
}

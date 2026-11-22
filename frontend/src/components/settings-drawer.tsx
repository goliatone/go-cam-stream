import { useEffect, useRef } from "preact/hooks";
import type { CameraSummary, State } from "../services/contracts";
import { managementMessage } from "../services/management-copy";
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

// A saved camera's settings, in a drawer beside the video it shows. Today only
// its sign-in changes; its name and address follow once the server supports
// them. The server refuses a sign-in change while anyone watches the camera,
// from any browser, so the drawer says how many do before anything is tried.
export function SettingsDrawer({ view, actions, settings = {} }: ManagementDialogProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const video = useRef<HTMLVideoElement>(null);
  const { camera, viewers, watching, state, stopWatching, reconnect } = settings;
  const draft = view.draft;
  const name = camera?.name ?? draft?.name ?? view.credentialTarget?.name;
  const error = !view.storageReady ? "recovery_storage_unavailable" : view.error;
  // A change whose outcome is unknown replaces the form until it is known.
  const stuck = Boolean(view.unresolved && !view.busy) || view.phase === "recovering" && !draft;
  const testing = view.phase === "testing" || draft?.state === "cleaning" || draft?.state === "cleanup_failed";
  const blocked = Boolean(view.busy || view.unresolved || !view.storageReady) || testing;
  // This page counts among the viewers even before the server's count shows it.
  const count = viewers === undefined ? undefined : Math.max(viewers, watching ? 1 : 0);
  const kept = Boolean(draft?.credentials_present);
  const status = testing ? "Checking the connection…" : view.phase === "saving" ? "Saving the new sign-in…"
    : view.phase === "recovering" ? "Checking your last change…" : view.busy ? "Starting the check…" : "";
  useEffect(() => {
    const before = document.activeElement;
    openDialog(dialog.current);
    return () => { if (before instanceof HTMLElement && before.isConnected) before.focus(); };
  }, []);
  const close = () => { form.current?.reset(); actions.close(); };
  return <dialog ref={dialog} class="dialog management-dialog drawer" aria-labelledby="settings-title settings-camera" onCancel={(event) => { event.preventDefault(); close(); }}>
    <div class="dialog-inner">
      <div class="dialog-head"><h2 id="settings-title">Settings</h2><button type="button" class="icon-button" aria-label="Close settings" onClick={close}><CloseIcon /></button></div>
      {name && <p id="settings-camera" class="management-intro">{name}</p>}
      <section class="drawer-section" aria-labelledby="settings-sign-in">
        <h3 id="settings-sign-in">Sign-in</h3>
        {view.saved ? <p class="callout" data-tone="success" role="status">The camera now uses the new sign-in.</p> : <>
          <div class="callout" data-tone={count ? "warning" : "info"}>
            <p id="settings-watchers" role="status">{count === undefined ? "The sign-in can change only while no one is watching this camera, in any browser."
              : !count ? "No one is watching this camera, so its sign-in can change now."
              : watching && count === 1 ? "You’re watching this camera here. Stop watching to change its sign-in."
              : `${count === 1 ? "1 person is" : `${count} people are`} watching this camera${watching ? ", including you" : ""}. Everyone must stop watching before its sign-in can change.`}</p>
            {/* The button goes once playback stops; focus moves on to the form. */}
            {watching && stopWatching && <button type="button" class="secondary-button" onClick={() => { stopWatching(); form.current?.querySelector("input")?.focus(); }}>Stop watching here</button>}
          </div>
          <div role="status" aria-live="polite">{status}</div>
          {error && <p class="management-error" role="alert">{error === "camera_busy" ? "Someone is watching this camera. Everyone must stop watching before its sign-in can change." : managementMessage(error)}</p>}
          {stuck && view.unresolved && !error && !view.busy && <p>Your last change isn’t confirmed yet. Check again before making another.</p>}
          {!stuck && <form id="settings-form" ref={form} onSubmit={(event) => {
            event.preventDefault();
            if (blocked || count || !video.current) return;
            const data = new FormData(event.currentTarget);
            const username = String(data.get("username") ?? ""), password = String(data.get("password") ?? "");
            actions.checkAndSave?.(video.current, username || password ? { username, password } : undefined);
            event.currentTarget.reset();
          }}>
            <fieldset disabled={blocked}>
              <label>Username <input name="username" autoComplete="off" maxLength={1024} aria-describedby="settings-username-hint" /></label>
              <p id="settings-username-hint" class="hint">Leave blank to keep the saved username.</p>
              <label>New password <input name="password" type="password" autoComplete="new-password" maxLength={4096} required={!kept} aria-describedby={kept ? "settings-password-hint" : undefined} /></label>
              {kept && <p id="settings-password-hint" class="hint">Leave blank to check the password you entered again.</p>}
            </fieldset>
          </form>}
          <ConnectionCheck test={view.test} active={testing} video={video} hidden={!testing && !view.test} />
        </>}
      </section>
      {camera && <>
        <section class="drawer-section" aria-labelledby="settings-connection">
          <h3 id="settings-connection">Connection</h3>
          <dl class="drawer-facts"><div><dt>Status</dt><dd>{describeView({ state: state ?? camera.availability }).label}</dd></div></dl>
          {reconnect && <button type="button" class="secondary-button" onClick={reconnect}>Reconnect</button>}
        </section>
        <section class="drawer-section" aria-labelledby="settings-about">
          <h3 id="settings-about">About</h3>
          <dl class="drawer-facts">
            <div><dt>{camera.streams.length > 1 ? "Streams" : "Stream"}</dt><dd>{camera.streams.map(item => item.name).join(", ")}</dd></div>
            <div><dt>Media</dt><dd>{[...new Set(camera.streams.map(item => mediaLabel(item.capabilities)))].join(", ")}</dd></div>
          </dl>
        </section>
      </>}
      <div class="dialog-actions">
        {view.saved ? <>
          <button class="primary-button" type="button" onClick={() => actions.watch(view.saved!.camera_id)}>Watch now</button>
          <button type="button" class="secondary-button" onClick={close}>Close</button>
        </> : stuck ? <>
          {view.storageReady && !view.busy && <button type="button" class="secondary-button" onClick={actions.recover}>Check again</button>}
          {view.canRetrySave && <button type="button" class="secondary-button" disabled={view.busy} onClick={actions.retrySave}>Try saving again</button>}
          {view.canAbandon && <button type="button" class="danger-button" onClick={actions.abandon}>Discard changes</button>}
        </> : <>
          <button class="primary-button" type="submit" form="settings-form" disabled={blocked || Boolean(count)} aria-describedby="settings-watchers">{testing ? "Checking…" : view.phase === "saving" ? "Saving…" : "Check and save"}</button>
          {testing && <button type="button" class="secondary-button" disabled={view.unresolved} onClick={actions.cancelTest}>Stop check</button>}
          {/* A browser that blocks autoplay plays the preview on a press. */}
          {testing && view.test?.source_media_observed && <button type="button" class="secondary-button" onClick={() => { void video.current?.play().catch(() => {}); }}>Play preview</button>}
        </>}
      </div>
    </div>
  </dialog>;
}

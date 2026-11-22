import { useEffect, useRef, useState } from "preact/hooks";
import type { Credentials, DraftInput, DraftReview, ErrorCode, ManagementErrorCode, SaveCameraResponse, TestResult } from "../services/contracts";
import { managementMessage, managementTestMessage } from "../services/management-copy";
import { openDialog } from "../services/shortcuts";
import { CloseIcon } from "./icon";
import { ErrorDetails } from "./status";

export type ManagementPhase = "editing" | "importing" | "review" | "testing" | "saving" | "saved" | "recovering" | "canceled";
export interface ManagementView {
  phase: ManagementPhase;
  draft?: DraftReview | undefined;
  test?: TestResult | undefined;
  saved?: SaveCameraResponse | undefined;
  error?: ManagementErrorCode | undefined;
  busy?: boolean;
  storageReady: boolean;
  unresolved?: boolean;
  /** Recovery is finding out whether a save went through. */
  checkingSave?: boolean;
  canAbandon?: boolean;
  canRetrySave?: boolean;
  credentialTarget?: { id: string; name?: string | undefined } | undefined;
  duplicateCamera?: string | undefined;
}
export interface ManagementActions {
  submit(input: DraftInput): void;
  importFiles(yaml: File, env?: File): void;
  test(video: HTMLVideoElement): void;
  cancelTest(): void;
  save(): void;
  recover(): void;
  cancel(): void;
  close(): void;
  abandon?(): void;
  retrySave?(): void;
  watch(cameraID: string): void;
  submitCredentials?(credentials: Credentials): void;
  openSettings?(cameraID: string): void;
  /** Starts another camera once one is saved. */
  reset?(): void;
}
/** How a new camera is connected: by its RTSP address or the Mac app's files. */
export type ManagementMode = "manual" | "import";
export interface ManagementDialogProps { view: ManagementView; actions: ManagementActions; mode?: ManagementMode | undefined }
const warnings = {
  server_relative_localhost: "Localhost is the server’s own machine. A remote or container server needs your Mac’s network address and Private network capture.",
  imported_grants_ignored: "Sharing settings in the file are not imported.",
  initial_address_only: "Use a camera you trust: the server checks only the address it first connects to and follows the camera’s redirects.",
  creator_only_access: "",
  fixed_address: "This address stays fixed after saving. Reconnect does not follow the camera to a new address.",
};
const ROWS = ["Reaching the camera", "Receiving video", "Playing in this browser"];
// Failures that stop the check before the camera is reached, and those in the
// browser's own playback. Any other failure means no video arrived.
const REACH: readonly string[] = ["authentication_failed", "camera_unreachable", "destination_denied", "address_unavailable", "identity_unverified", "duplicate_camera", "capacity_exceeded", "camera_busy", "signaling_failed"];
const PLAY: readonly string[] = ["media_connection_failed", "playback_evidence_unavailable", "playback_evidence_insufficient"];
export { managementMessage } from "../services/management-copy";

export default function CameraManagementDialog({ view, actions, mode: initialMode = "manual" }: ManagementDialogProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const video = useRef<HTMLVideoElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  // The draft revision Continue left behind: its successor starts the check.
  const autoCheck = useRef<string | undefined>(undefined);
  const draft = view.draft;
  const [mode, setMode] = useState(initialMode);
  const [editing, setEditing] = useState(false);
  const [signIn, setSignIn] = useState(draft?.authentication !== "anonymous");
  const [endpoint, setEndpoint] = useState(draft?.endpoint ?? "");
  const [touched, setTouched] = useState(false);
  const [files, setFiles] = useState<{ yaml?: File; env?: File }>({});
  const [fileError, setFileError] = useState("");
  const [over, setOver] = useState(false);
  const blocked = Boolean(view.busy || view.unresolved || !view.storageReady);
  const activeTest = view.phase === "testing" || draft?.state === "cleanup_failed" || draft?.state === "cleaning";
  const error = !view.storageReady ? "recovery_storage_unavailable" : view.error;
  const credentialUpdate = Boolean(view.credentialTarget || draft?.credential_update || view.saved?.credentials_updated);
  const cameraName = draft?.name ?? view.credentialTarget?.name;
  // A change whose outcome is unknown replaces the steps. So does the lookup
  // after a refresh, before the draft is back.
  const stuck = Boolean(view.unresolved && !view.busy) || view.phase === "recovering" && !draft;
  const open = !view.saved && !stuck && view.phase !== "canceled";
  const step = !draft || editing || draft.state === "incomplete" ? 0
    : (draft.state === "tested" || draft.state === "saving" || view.phase === "saving") && error !== "test_expired" ? 2 : 1;
  const key = `${draft?.id}:${draft?.revision}`;
  const address = endpoint.trim();
  const local = /^rtsps?:\/\/(localhost|127\.0\.0\.1|\[::1\])([:/]|$)/i.test(address);
  const badAddress = touched && Boolean(address) && !/^rtsps?:\/\/[^\s/@]+(\/\S*)?$/i.test(address);
  const keepHint = !credentialUpdate && draft?.credentials_present ? "management-keep-hint" : undefined;
  const busyText: Partial<Record<ManagementPhase, string>> = { importing: "Reading the files…", saving: "Saving…", recovering: view.checkingSave ? "Checking whether your camera was saved…" : "Checking your last change…" };
  const status = view.phase === "testing" ? "Checking the connection…"
    : view.busy ? busyText[view.phase] ?? "Updating…"
    : view.phase === "canceled" && !error ? "Canceled."
    : open && step === 2 && !error ? "Connection works. Ready to save." : "";
  useEffect(() => {
    const before = document.activeElement;
    openDialog(dialog.current);
    return () => { if (before instanceof HTMLElement && before.isConnected) before.focus(); };
  }, []);
  useEffect(() => { setEditing(false); setTouched(false); setSignIn(draft?.authentication !== "anonymous"); setEndpoint(draft?.endpoint ?? ""); }, [draft?.revision, draft?.id]);
  // Only Continue starts a check by itself. A restored or imported draft waits
  // for Check connection, so an imported localhost address can be fixed first.
  useEffect(() => {
    if (autoCheck.current === undefined) return;
    if (error || stuck) autoCheck.current = undefined;
    else if (draft?.state === "ready" && key !== autoCheck.current && !blocked && video.current) {
      autoCheck.current = undefined;
      actions.test(video.current);
    }
  });
  // A step change can remove the focused button. Focus then moves to the new
  // step's first field or action, once one is enabled. Only a step change does
  // this: focus is briefly outside the dialog while it moves between fields.
  const place = useRef(""), refocus = useRef(false);
  useEffect(() => {
    const root = dialog.current, where = `${step}:${open}:${stuck}:${Boolean(view.saved)}`;
    if (place.current !== where) { refocus.current = place.current !== ""; place.current = where; }
    if (!refocus.current || !root) return;
    const next = root.contains(document.activeElement) ? root : root.querySelector<HTMLElement>("input:not(:disabled), .dialog-actions button:not(:disabled)");
    if (next !== root) next?.focus();
    if (next) refocus.current = false;
  });
  const clearSecrets = () => {
    for (const name of ["username", "password"]) {
      const input = form.current?.elements.namedItem(name);
      if (input instanceof HTMLInputElement) input.value = "";
    }
    setFiles({});
  };
  const close = () => { clearSecrets(); actions.close(); };
  // Sorts chosen or dropped files into the export's two, checking each size.
  const take = (list?: FileList | null) => {
    const next = { ...files };
    let problem = "";
    for (const file of Array.from(list ?? [])) {
      const yaml = /\.ya?ml$/i.test(file.name);
      if (!yaml && !/\.env$/i.test(file.name)) problem = `${file.name} is not a Camstream export file.`;
      else if (!file.size || file.size > (yaml ? 32768 : 8192)) problem = `${file.name} is ${file.size ? "too large" : "empty"} for a Camstream export.`;
      else if (yaml) next.yaml = file;
      else next.env = file;
    }
    setFiles(next);
    setFileError(problem);
  };
  // A row is done only once the server reports camera video, or the check
  // passed; the row a failure points at is failed, and the rest stay waiting.
  const code = view.test?.error?.code ?? "";
  const media = view.test?.source_media_observed;
  const failed = !code ? -1 : media || PLAY.includes(code) ? 2 : REACH.includes(code) ? 0 : 1;
  const marks = ROWS.map((_, i) => view.test?.stage === "passed" || media && i < 2 ? "done" : i === failed ? "failed"
    : failed < 0 && activeTest && i === (media ? 2 : 0) ? "checking" : "waiting");
  const notes = draft ? [...draft.authentication === "anonymous" ? ["If this camera needs sign-in, edit the details. The Camstream Mac app always requires it."] : [], ...draft.warnings.map(warning => warnings[warning]).filter(Boolean)] : [];
  const summary = draft && <section class="management-review" aria-label="Camera summary">
    {!credentialUpdate && <h3>{draft.name}</h3>}
    <dl>
      {!credentialUpdate && <><dt>Address</dt><dd>{draft.endpoint}</dd></>}
      {draft.dial_address && <><dt>Connects to</dt><dd>{draft.dial_address}</dd></>}
      <dt>Sign-in</dt><dd>{draft.authentication === "anonymous" ? "Not provided (anonymous access)" : draft.credentials_present ? "Username and password (hidden)" : "Required"}</dd>
      {!credentialUpdate && <><dt>Video</dt><dd>Default stream, no audio</dd></>}
      {!credentialUpdate && draft.warnings.includes("creator_only_access") && <><dt>Visible to</dt><dd>Only you</dd></>}
    </dl>
    {notes.length > 0 && <ul>{notes.map(note => <li key={note}>{note}</li>)}</ul>}
  </section>;
  return <dialog ref={dialog} class="dialog management-dialog" aria-labelledby="management-title" onCancel={(event) => { event.preventDefault(); close(); }}>
    <div class="dialog-inner">
      <div class="dialog-head"><h2 id="management-title">{view.saved ? (view.saved.credentials_updated ? "Sign-in updated" : "Camera saved") : error === "receipt_expired" ? "Camera save" : credentialUpdate ? "Camera settings" : "Add Camera"}</h2><button type="button" class="icon-button" aria-label={credentialUpdate ? "Close camera settings" : "Close Add Camera"} onClick={close}><CloseIcon /></button></div>
      {credentialUpdate && cameraName && <p class="management-intro">{cameraName}</p>}
      {open && <ol class="stepper" aria-label="Steps">{[credentialUpdate ? "Sign-in" : "Connect", "Check", "Save"].map((label, i) => <li key={label} class={i < step ? "done" : undefined} aria-current={i === step ? "step" : undefined}>{label}{i < step && <span class="sr-only"> (done)</span>}</li>)}</ol>}
      <div role="status" aria-live="polite">{status}</div>
      {error && <p class="management-error" role="alert">{error === "camera_busy" && credentialUpdate ? "Stop playback of this camera, then try again." : managementMessage(error)}</p>}
      {view.duplicateCamera && actions.openSettings && <button type="button" class="secondary-button" disabled={blocked} onClick={() => { clearSecrets(); actions.openSettings?.(view.duplicateCamera!); }}>Open camera settings</button>}
      {stuck && view.unresolved && !error && !view.busy && <p>Your last change isn’t confirmed yet. Check again before making another.</p>}
      {open && step === 0 && <>
        {!draft && !credentialUpdate && <div class="segmented" role="radiogroup" aria-label="Camera type">{([["manual", "Network camera (RTSP)"], ["import", "Camstream Mac app"]] as const).map(([value, label]) => <label key={value} class="segment"><input type="radio" name="management-mode" checked={mode === value} disabled={blocked} onChange={() => { clearSecrets(); setFileError(""); setEndpoint(""); setTouched(false); setMode(value); }} /><span>{label}</span></label>)}</div>}
        <form id="management-form" ref={form} key={`${key}:${mode}`} onSubmit={(event) => {
          event.preventDefault(); if (blocked) return;
          const data = new FormData(event.currentTarget);
          const username = String(data.get("username") ?? ""), password = String(data.get("password") ?? "");
          if (mode === "import" && !draft && !credentialUpdate) {
            if (!files.yaml) { setFileError("Add the exported .yaml file."); return; }
            actions.importFiles(files.yaml, files.env);
          } else {
            autoCheck.current = key;
            if (credentialUpdate) actions.submitCredentials?.({ username, password });
            else actions.submit({ name: String(data.get("name") ?? ""), endpoint: String(data.get("endpoint") ?? ""), authentication: signIn ? "credentials" : "anonymous",
              ...(signIn && (username || password) ? { credentials: { username, password } } : {}) });
          }
          clearSecrets(); setFileError("");
        }}>
          <fieldset disabled={blocked}>
          {!credentialUpdate && (mode === "import" && !draft ? <>
            <div class={over ? "drop-zone over" : "drop-zone"} onDragOver={(event) => { event.preventDefault(); setOver(true); }} onDragLeave={() => setOver(false)} onDrop={(event) => { event.preventDefault(); setOver(false); take(event.dataTransfer?.files); }}>
              <p>Drop the exported .yaml and .env files here</p>
              <button type="button" class="secondary-button" aria-describedby={fileError ? "management-files-hint management-file-error" : "management-files-hint"} onClick={() => picker.current?.click()}>Choose files</button>
              <input ref={picker} type="file" multiple hidden onChange={(event) => { take(event.currentTarget.files); event.currentTarget.value = ""; }} />
              {(files.yaml || files.env) && <ul class="file-list">{files.yaml && <li>{files.yaml.name} · connection</li>}{files.env && <li>{files.env.name} · sign-in</li>}</ul>}
            </div>
            <p id="management-files-hint" class="hint">{files.yaml && !files.env ? "Without the .env file, you’ll enter the username and password next. " : ""}{warnings.imported_grants_ignored}</p>
            {fileError && <p id="management-file-error" class="field-error" role="alert">{fileError}</p>}
            <details class="disclosure"><summary>Where do I find these?</summary><p>In the Mac app, choose Connection Details → Export Connection Files → Show Files in Finder. Keep the camera running for the check.</p></details>
          </> : <>
            <label>Camera name <input name="name" required maxLength={120} defaultValue={draft?.name ?? ""} autoComplete="off" /></label>
            <label>RTSP address <input name="endpoint" required maxLength={2048} placeholder="rtsp://localhost:18556/macbook" defaultValue={draft?.endpoint ?? ""} autoComplete="off" spellcheck={false} aria-invalid={badAddress || undefined}
              aria-describedby={local ? "management-address-hint management-local-hint" : "management-address-hint"} onInput={(event) => setEndpoint(event.currentTarget.value)} onBlur={() => setTouched(true)} /></label>
            <p id="management-address-hint" class={badAddress ? "hint field-error" : "hint"}>{badAddress ? "Use rtsp://host:port/path, without a username or password." : "An address the server can reach, without a username or password."}</p>
            {local && <p id="management-local-hint" class="hint">{warnings.server_relative_localhost}</p>}
            <label class="switch"><input type="checkbox" role="switch" checked={signIn} aria-describedby={signIn ? undefined : "management-sign-in-hint"} onChange={(event) => { clearSecrets(); setSignIn(event.currentTarget.checked); }} />Requires sign-in</label>
            {!signIn && <p id="management-sign-in-hint" class="hint">Leave this on for the Camstream Mac app, which always requires sign-in.</p>}
          </>)}
          {(credentialUpdate || signIn && (mode === "manual" || draft)) && <>
            <label>Username <input name="username" autoComplete="off" maxLength={1024} required={!credentialUpdate && !draft?.credentials_present} aria-describedby={credentialUpdate ? "management-username-hint" : keepHint} /></label>
            {credentialUpdate && <p id="management-username-hint" class="hint">Leave blank to keep the saved username.</p>}
            <label>{credentialUpdate ? "New password" : "Password"} <input name="password" type="password" autoComplete="new-password" maxLength={4096} required={credentialUpdate || !draft?.credentials_present} aria-describedby={keepHint ?? (credentialUpdate ? undefined : "management-password-hint")} /></label>
            {keepHint ? <p id={keepHint} class="hint">Leave both blank to keep the ones already entered.</p> : !credentialUpdate && <p id="management-password-hint" class="hint">For the Camstream Mac app, both are in its private .env file.</p>}
          </>}
          </fieldset>
        </form>
      </>}
      {open && step > 0 && summary}
      {open && step === 1 && <section class="management-preview" aria-label="Connection check">
        <ol class="checklist">{ROWS.map((row, i) => <li key={row} data-mark={marks[i]}>{row}<span class="sr-only">: {marks[i]}</span></li>)}</ol>
        <video ref={video} muted playsInline aria-label="Camera preview" />
        {view.test?.error && <div class="management-error">
          <p role="alert">{managementTestMessage(view.test)}</p>
          <details class="disclosure"><summary>Details</summary><ErrorDetails code={view.test.error.code as ErrorCode} where={[ROWS[failed] ?? ""]} /></details>
        </div>}
        {view.test?.stage === "canceled" && !view.test.error && <p>The check stopped before it finished.</p>}
      </section>}
      {view.saved && <p>{view.saved.credentials_updated ? "The camera now uses the new sign-in." : "Your camera is in the camera list. The video you’re watching keeps playing until you switch."}</p>}
      {/* The close icon and Esc always close. Cancel shows only when there is a
          draft to discard, and Close only once nothing is left. */}
      <div class="dialog-actions">
        {view.saved ? <>
          <button class="primary-button" type="button" onClick={() => actions.watch(view.saved!.camera_id)}>Watch now</button>
          {!credentialUpdate && actions.reset && <button type="button" class="secondary-button" onClick={actions.reset}>Add another</button>}
          <button type="button" class="secondary-button" onClick={close}>Close</button>
        </> : stuck ? <>
          {view.storageReady && !view.busy && <button type="button" class="secondary-button" onClick={actions.recover}>Check again</button>}
          {view.canRetrySave && <button type="button" class="secondary-button" disabled={view.busy} onClick={actions.retrySave}>Try saving again</button>}
          {view.canAbandon && <button type="button" class="danger-button" onClick={actions.abandon}>Discard changes</button>}
        </> : view.phase === "canceled" ? <button type="button" class="secondary-button" onClick={close}>Close</button> : <>
          {step === 0 && <button class="primary-button" type="submit" form="management-form" disabled={blocked}>{editing ? "Update details" : "Continue"}</button>}
          {step === 0 && editing && <button type="button" class="secondary-button" onClick={() => setEditing(false)}>Back</button>}
          {step === 1 && (activeTest ? <>
            <button type="button" class="secondary-button" disabled={view.unresolved} onClick={actions.cancelTest}>Stop check</button>
            {view.test?.source_media_observed && <button type="button" class="secondary-button" onClick={() => { void video.current?.play().catch(() => {}); }}>Play preview</button>}
          </> : <button type="button" class="primary-button" disabled={blocked || draft?.state === "expired" || draft?.state === "canceled"} onClick={() => { if (video.current) actions.test(video.current); }}>{view.test ? "Check again" : "Check connection"}</button>)}
          {step === 2 && <button type="button" class="primary-button" disabled={blocked || draft?.state !== "tested" || draft.test?.cleanup !== "confirmed"} onClick={actions.save}>{credentialUpdate ? "Save sign-in" : "Save camera"}</button>}
          {step > 0 && <button type="button" class="secondary-button" disabled={blocked || activeTest || draft?.state === "saving"} onClick={() => setEditing(true)}>{credentialUpdate ? "Edit sign-in" : "Edit details"}</button>}
          {draft && <button type="button" class="ghost-button" disabled={blocked} onClick={() => { clearSecrets(); actions.cancel(); }}>Cancel</button>}
        </>}
      </div>
    </div>
  </dialog>;
}

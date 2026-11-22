import { useEffect, useRef, useState } from "preact/hooks";
import type { Credentials, DraftInput, DraftReview, ManagementErrorCode, SaveCameraResponse, TestResult } from "../services/contracts";
import { managementMessage, managementTestMessage } from "../services/management-copy";
import { openDialog } from "../services/shortcuts";
import { CloseIcon } from "./icon";

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
}
export interface ManagementDialogProps { view: ManagementView; actions: ManagementActions }
const warnings = {
  server_relative_localhost: "Localhost is the server’s machine. A remote or container server needs your Mac’s reachable address and Private network capture.",
  imported_grants_ignored: "Access settings in the file are ignored.",
  initial_address_only: "Use a trusted camera. Address checks cover the initial connection; camera redirects are trusted.",
  creator_only_access: "Only your account receives access to this camera.",
  fixed_address: "This address stays fixed after Save. Reconnect does not update it if the camera moves.",
};
export { managementMessage } from "../services/management-copy";

export default function CameraManagementDialog({ view, actions }: ManagementDialogProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const video = useRef<HTMLVideoElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const [mode, setMode] = useState<"manual" | "import">("manual");
  const [editing, setEditing] = useState(false);
  const [authentication, setAuthentication] = useState(view.draft?.authentication ?? "anonymous");
  const [fileError, setFileError] = useState(false);
  const draft = view.draft;
  const blocked = Boolean(view.busy || view.unresolved || !view.storageReady);
  const activeTest = view.phase === "testing" || draft?.state === "cleanup_failed" || draft?.state === "cleaning";
  const showForm = !view.saved && (!draft || editing) && view.phase !== "recovering" && view.phase !== "canceled";
  const error = !view.storageReady ? "recovery_storage_unavailable" : view.error;
  const credentialUpdate = Boolean(view.credentialTarget || draft?.credential_update || view.saved?.credentials_updated);
  const cameraName = draft?.name ?? view.credentialTarget?.name;
  // Hints and errors are linked to their fields by id.
  const fileHelp = fileError ? "management-limits management-file-error" : "management-limits";
  const keepHint = !credentialUpdate && draft?.credentials_present ? "management-keep-hint" : undefined;
  useEffect(() => {
    const before = document.activeElement;
    openDialog(dialog.current);
    return () => { if (before instanceof HTMLElement && before.isConnected) before.focus(); };
  }, []);
  useEffect(() => { setEditing(false); setAuthentication(draft?.authentication ?? "anonymous"); }, [draft?.revision, draft?.id]);
  const clearSecrets = () => {
    for (const name of ["username", "password", "yaml", "env"]) {
      const input = form.current?.elements.namedItem(name);
      if (input instanceof HTMLInputElement) input.value = "";
    }
  };
  const close = () => { clearSecrets(); actions.close(); };
  return <dialog ref={dialog} class="dialog management-dialog" aria-labelledby="management-title" onCancel={(event) => { event.preventDefault(); close(); }}>
    <div class="dialog-inner">
      <div class="dialog-head"><h2 id="management-title">{view.saved?.credentials_updated ? "Credentials updated" : view.saved ? "Camera saved" : error === "receipt_expired" ? "Camera save" : credentialUpdate ? "Camera settings" : "Add Camera"}</h2><button type="button" class="icon-button" aria-label={credentialUpdate ? "Close camera settings" : "Close Add Camera"} onClick={close}><CloseIcon /></button></div>
      {credentialUpdate && cameraName && <p class="management-intro">{cameraName}</p>}
      <div role="status" aria-live="polite">{view.busy ? ({ importing: "Importing connection files…", testing: "Checking live video…", saving: "Saving camera…", recovering: "Checking the original operation…" }[view.phase as "importing"] ?? "Applying changes…") : view.phase === "canceled" && !error ? "Draft canceled." : ""}</div>
      {error && <p class="management-error" role="alert">{error === "camera_busy" && credentialUpdate ? "Stop playback of this camera, then try again." : managementMessage(error)}</p>}
      {view.duplicateCamera && actions.openSettings && <button type="button" class="secondary-button" disabled={blocked} onClick={() => { clearSecrets(); actions.openSettings?.(view.duplicateCamera!); }}>Open camera settings</button>}
      {view.unresolved && <p>Check the original operation before making another change.</p>}
      {showForm && <>
        {!draft && !credentialUpdate && <div class="segmented" role="radiogroup" aria-label="Camera input">{([["manual", "Enter RTSP address"], ["import", "Import Mac files"]] as const).map(([value, label]) => <label key={value} class="segment"><input type="radio" name="management-mode" checked={mode === value} disabled={blocked} onChange={() => { clearSecrets(); setMode(value); }} /><span>{label}</span></label>)}</div>}
        <form ref={form} key={`${draft?.id ?? "new"}:${draft?.revision ?? 0}:${mode}`} onSubmit={(event) => {
          event.preventDefault(); if (blocked) return;
          const data = new FormData(event.currentTarget);
          const username = String(data.get("username") ?? ""), password = String(data.get("password") ?? "");
          if (credentialUpdate) {
            actions.submitCredentials?.({ username, password });
          } else if (mode === "import" && !draft) {
            const yaml = (event.currentTarget.elements.namedItem("yaml") as HTMLInputElement).files?.[0], env = (event.currentTarget.elements.namedItem("env") as HTMLInputElement).files?.[0];
            if (!(yaml instanceof File) || !yaml.size || yaml.size > 32768 || (env instanceof File && env.size > 8192)) { setFileError(true); clearSecrets(); return; }
            actions.importFiles(yaml, env instanceof File && env.size ? env : undefined);
          } else {
            actions.submit({name:String(data.get("name") ?? ""), endpoint:String(data.get("endpoint") ?? ""), authentication,
              ...(authentication === "credentials" && (username || password) ? {credentials:{username,password}} : {})});
          }
          clearSecrets(); setFileError(false);
        }}>
          <fieldset disabled={blocked}>
          {!credentialUpdate && (mode === "import" && !draft ? <>
            <p>In the Mac app, use Connection Details → Export Connection Files → Show Files in Finder. Select both files and keep the camera running for the test.</p>
            <label>Connection YAML <input name="yaml" type="file" accept=".yaml,.yml" required aria-describedby={fileHelp} aria-invalid={fileError || undefined} /></label>
            <label>Credentials file (optional) <input name="env" type="file" aria-describedby={fileHelp} aria-invalid={fileError || undefined} /></label>
            <p id="management-limits" class="hint">YAML up to 32 KiB and credentials up to 8 KiB. With YAML alone, enter credentials after import.</p>
            {fileError && <p id="management-file-error" class="field-error" role="alert">Choose files within these size limits.</p>}
          </> : <>
            <label>Camera name <input name="name" required maxLength={120} defaultValue={draft?.name ?? ""} autoComplete="off" /></label>
            <label>RTSP address <input name="endpoint" required maxLength={2048} placeholder="rtsp://localhost:18556/macbook" defaultValue={draft?.endpoint ?? ""} autoComplete="off" spellcheck={false} aria-describedby="management-address-hint" /></label>
            <p id="management-address-hint" class="hint">Localhost means this server. Use an address the server can reach and enter credentials separately.</p>
            <label>Authentication <select aria-describedby="management-auth-hint" value={authentication} onChange={(event) => { clearSecrets(); setAuthentication(event.currentTarget.value as "anonymous" | "credentials"); }}><option value="anonymous">No credentials</option><option value="credentials">Username and password</option></select></label>
            <p id="management-auth-hint" class="hint">For Camstream Camera on Mac, import both files or enter credentials from its private .env file.</p>
          </>)}
          {(credentialUpdate || authentication === "credentials" && (mode !== "import" || draft)) && <>
            <label>Username <input name="username" autoComplete="off" maxLength={1024} required={!credentialUpdate && !draft?.credentials_present} aria-describedby={credentialUpdate ? "management-username-hint" : keepHint} /></label>
            {credentialUpdate && <p id="management-username-hint" class="hint">Leave blank to keep the saved username.</p>}
            <label>{credentialUpdate ? "New password" : "Password"} <input name="password" type="password" autoComplete="new-password" maxLength={4096} required={credentialUpdate || !draft?.credentials_present} aria-describedby={keepHint} /></label>
            {keepHint && <p id={keepHint} class="hint">Leave both fields blank to keep the draft’s credentials.</p>}
          </>}
          <button class="primary-button" type="submit">{credentialUpdate ? "Review credentials" : draft ? "Apply changes" : mode === "import" ? "Import and review" : "Review camera"}</button>
          </fieldset>
        </form>
      </>}
      {draft && !editing && !view.saved && <section aria-label="Camera review" class="management-review">
        {!credentialUpdate && <h3>{draft.name}</h3>}<dl>{!credentialUpdate && <><dt>Address</dt><dd>{draft.endpoint}</dd><dt>Video</dt><dd>Default stream · No audio</dd></>}<dt>Credentials</dt><dd>{draft.authentication === "anonymous" ? "Not provided (anonymous access)" : draft.credentials_present ? "Provided (hidden)" : "Required"}</dd>{draft.dial_address && <><dt>Selected address</dt><dd>{draft.dial_address}</dd></>}</dl>
        {draft.authentication === "anonymous" && <p>If this camera requires sign-in, choose Edit details → Username and password. Camstream Camera on Mac always requires credentials.</p>}
        {draft.warnings.map(warning => <p key={warning}>{warnings[warning]}</p>)}
        <button type="button" class="secondary-button" disabled={blocked || activeTest || draft.state === "saving"} onClick={() => setEditing(true)}>{credentialUpdate ? "Edit credentials" : "Edit details"}</button>
      </section>}
      {draft && !view.saved && <section class="management-preview" aria-label="Connection test">
        <video ref={video} muted playsInline aria-label="Camera test preview" />
        {view.test && <p role="status">{view.test.stage === "passed" ? "Live video verified and preview closed. Ready to save." : view.test.stage === "failed" ? "Connection test failed." : view.test.stage === "canceled" ? "Test canceled." : view.test.source_media_observed ? "Camera media received. Checking browser playback…" : "Waiting for camera media…"}</p>}
        {view.test?.error && <p role="alert">{managementTestMessage(view.test)}</p>}
        {activeTest ? <div class="dialog-actions"><button type="button" class="secondary-button" onClick={() => { void video.current?.play().catch(() => {}); }}>Play preview</button><button type="button" class="secondary-button" disabled={Boolean(view.unresolved)} onClick={actions.cancelTest}>Cancel test</button></div> : <button type="button" class={draft.state === "tested" ? "secondary-button" : "primary-button"} disabled={blocked || editing || draft.state === "incomplete" || draft.state === "expired" || draft.state === "canceled"} onClick={() => { if (video.current) actions.test(video.current); }}>Test connection</button>}
      </section>}
      {view.saved && !view.saved.credentials_updated && <p>Your camera is saved. Current playback continues until you choose to watch it.</p>}
      {/* The close icon and Esc always close. Cancel draft shows only when
          there is a draft to discard, and Close only once nothing is left. */}
      <div class="dialog-actions">
        {view.saved && <button class="primary-button" type="button" onClick={() => actions.watch(view.saved!.camera_id)}>Watch Camera</button>}
        {draft && !view.saved && <button type="button" class="primary-button" disabled={blocked || editing || draft.state !== "tested" || draft.test?.cleanup !== "confirmed"} onClick={actions.save}>{credentialUpdate ? "Save credentials" : "Save Camera"}</button>}
        {(view.unresolved || error && error !== "receipt_expired") && view.storageReady && <button type="button" class="secondary-button" disabled={view.busy} onClick={actions.recover}>Check status</button>}
        {view.canRetrySave && <button type="button" class="secondary-button" disabled={view.busy} onClick={actions.retrySave}>Retry original Save</button>}
        {view.canAbandon && <button type="button" class="danger-button" onClick={actions.abandon}>Abandon unsaved draft</button>}
        {draft && !view.saved && !view.unresolved && view.phase !== "canceled" && <button type="button" class="ghost-button" disabled={blocked} onClick={() => { clearSecrets(); actions.cancel(); }}>Cancel draft</button>}
        {(view.saved || view.phase === "canceled") && <button type="button" class="secondary-button" onClick={close}>Close</button>}
      </div>
    </div>
  </dialog>;
}

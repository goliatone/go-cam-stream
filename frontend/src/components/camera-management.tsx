import { useEffect, useRef, useState } from "preact/hooks";
import type { DraftInput, DraftReview, ManagementErrorCode, SaveCameraResponse, TestResult } from "../services/contracts";
import { managementMessage } from "../services/management-copy";
import { openDialog } from "../services/shortcuts";

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
  const showForm = !view.saved && (!draft || editing) && view.phase !== "recovering";
  const error = !view.storageReady ? "recovery_storage_unavailable" : view.error;
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
      <div class="dialog-head"><h2 id="management-title">{view.saved ? "Camera saved" : "Add Camera"}</h2><button type="button" class="icon-button" aria-label="Close Add Camera" onClick={close}>×</button></div>
      <p class="management-intro">Connect an RTSP camera, check its live video, then save it for your account.</p>
      <div role="status" aria-live="polite">{view.busy ? ({ importing: "Importing connection files…", testing: "Checking live video…", saving: "Saving camera…", recovering: "Checking the original operation…" }[view.phase as "importing"] ?? "Applying changes…") : view.phase === "canceled" ? "Draft canceled." : ""}</div>
      {error && <p class="management-error" role="alert">{managementMessage(error)}</p>}
      {view.unresolved && <p>Keep this draft’s recovery record until its outcome is known. Closing this window does not start another change.</p>}
      {showForm && <>
        {!draft && <div class="management-tabs" role="group" aria-label="Camera input"><button type="button" disabled={blocked} aria-pressed={mode === "manual"} onClick={() => { clearSecrets(); setMode("manual"); }}>RTSP address</button><button type="button" disabled={blocked} aria-pressed={mode === "import"} onClick={() => { clearSecrets(); setMode("import"); }}>Import Mac files</button></div>}
        <form ref={form} key={`${draft?.id ?? "new"}:${draft?.revision ?? 0}:${mode}`} onSubmit={(event) => {
          event.preventDefault(); if (blocked) return;
          const data = new FormData(event.currentTarget);
          if (mode === "import" && !draft) {
            const yaml = (event.currentTarget.elements.namedItem("yaml") as HTMLInputElement).files?.[0], env = (event.currentTarget.elements.namedItem("env") as HTMLInputElement).files?.[0];
            if (!(yaml instanceof File) || !yaml.size || yaml.size > 32768 || (env instanceof File && env.size > 8192)) { setFileError(true); clearSecrets(); return; }
            actions.importFiles(yaml, env instanceof File && env.size ? env : undefined);
          } else {
            const username = String(data.get("username") ?? ""), password = String(data.get("password") ?? "");
            actions.submit({name:String(data.get("name") ?? ""), endpoint:String(data.get("endpoint") ?? ""), authentication,
              ...(authentication === "credentials" && (username || password) ? {credentials:{username,password}} : {})});
          }
          clearSecrets(); setFileError(false);
        }}>
          <fieldset disabled={blocked}>
          {mode === "import" && !draft ? <>
            <p>In the Mac camera app: Stop → Export Connection Files → Start. Upload the YAML and optionally its matching credentials file. Files are parsed as data; access grants are ignored.</p>
            <label>Connection YAML <input name="yaml" type="file" accept=".yaml,.yml" required /></label>
            <label>Credentials file (optional) <input name="env" type="file" /></label>
            <p>YAML up to 32 KiB; credentials up to 8 KiB. Without the credentials file, enter the username and password after import.</p>
            {fileError && <p role="alert">Choose files within these size limits.</p>}
          </> : <>
            <label>Camera name <input name="name" required maxLength={120} defaultValue={draft?.name ?? ""} autoComplete="off" /></label>
            <label>RTSP address <input name="endpoint" required maxLength={2048} placeholder="rtsp://localhost:18556/macbook" defaultValue={draft?.endpoint ?? ""} autoComplete="off" spellcheck={false} /></label>
            <p id="localhost-help">Localhost means this server, not necessarily this browser. For another machine, use a reachable camera address. Do not put passwords in the address.</p>
            <label>Authentication <select value={authentication} onChange={(event) => { clearSecrets(); setAuthentication(event.currentTarget.value as "anonymous" | "credentials"); }}><option value="anonymous">No credentials</option><option value="credentials">Username and password</option></select></label>
            {authentication === "credentials" && <><label>Username <input name="username" autoComplete="off" maxLength={1024} required={!draft?.credentials_present} /></label><label>Password <input name="password" type="password" autoComplete="new-password" maxLength={4096} required={!draft?.credentials_present} /></label>{draft?.credentials_present && <p>Credentials are already held for this draft. Leave both fields empty to keep them.</p>}</>}
          </>}
          <button class="primary-button" type="submit">{draft ? "Apply changes" : mode === "import" ? "Import and review" : "Review camera"}</button>
          </fieldset>
        </form>
      </>}
      {draft && !editing && !view.saved && <section aria-label="Camera review" class="management-review">
        <h3>{draft.name}</h3><dl><dt>Address</dt><dd>{draft.endpoint}</dd><dt>Video</dt><dd>Default stream · No audio</dd><dt>Credentials</dt><dd>{draft.authentication === "anonymous" ? "Not required" : draft.credentials_present ? "Provided (hidden)" : "Required"}</dd>{draft.dial_address && <><dt>Selected address</dt><dd>{draft.dial_address}</dd></>}</dl>
        {draft.warnings.map(warning => <p key={warning}>{warnings[warning]}</p>)}
        <button type="button" disabled={blocked || activeTest || draft.state === "saving"} onClick={() => setEditing(true)}>Edit details</button>
      </section>}
      {draft && !view.saved && <section class="management-preview" aria-label="Connection test">
        <video ref={video} muted playsInline aria-label="Camera test preview" />
        {view.test && <p role="status">{view.test.stage === "passed" ? "Live video verified and preview closed. Ready to save." : view.test.stage === "failed" ? "Connection test failed." : view.test.stage === "canceled" ? "Test canceled." : view.test.source_media_observed ? "Camera media received. Checking browser playback…" : "Waiting for camera media…"}</p>}
        {view.test?.error && <p role="alert">{managementMessage(view.test.error.code)}</p>}
        {activeTest ? <div class="management-actions"><button type="button" onClick={() => { void video.current?.play().catch(() => {}); }}>Play preview</button><button type="button" disabled={Boolean(view.unresolved)} onClick={actions.cancelTest}>Cancel test</button></div> : <button type="button" disabled={blocked || editing || draft.state === "incomplete" || draft.state === "expired" || draft.state === "canceled"} onClick={() => { if (video.current) actions.test(video.current); }}>Test connection</button>}
      </section>}
      {view.saved && <><p>Your camera is saved. Current playback continues until you choose to watch it.</p><button class="primary-button" type="button" onClick={() => actions.watch(view.saved!.camera_id)}>Watch Camera</button></>}
      <div class="management-actions">
        {draft && !view.saved && <button type="button" class="primary-button" disabled={blocked || editing || draft.state !== "tested" || draft.test?.cleanup !== "confirmed"} onClick={actions.save}>Save Camera</button>}
        {(view.unresolved || error) && view.storageReady && <button type="button" disabled={view.busy} onClick={actions.recover}>Check status</button>}
        {view.canRetrySave && <button type="button" disabled={view.busy} onClick={actions.retrySave}>Retry original Save</button>}
        {view.canAbandon && <button type="button" onClick={actions.abandon}>Abandon unsaved draft</button>}
        {!view.saved && <button type="button" disabled={blocked} onClick={() => { clearSecrets(); actions.cancel(); }}>Cancel draft</button>}
        <button type="button" onClick={close}>Close</button>
      </div>
    </div>
  </dialog>;
}

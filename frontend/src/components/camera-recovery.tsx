import { useEffect, useRef, useState } from "preact/hooks";
import type { ManagementErrorCode } from "../services/contracts";
import { openDialog, closeDialog } from "../services/shortcuts";
import { managementMessage } from "../services/management-copy";

export function CameraRecovery({ busy, unresolved, error, disabled, onConfirm, onCheck }: {
  busy: boolean; unresolved: boolean; error?: ManagementErrorCode | undefined;
  disabled: boolean; onConfirm(): void; onCheck(): void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => { if (open) openDialog(dialog.current); }, [open]);
  return <><button type="button" onClick={() => setOpen(true)}>Reconnect Camera</button>
    {open && <dialog ref={dialog} class="dialog" aria-labelledby="recovery-title" onClose={() => setOpen(false)}>
      <div class="dialog-inner"><h2 id="recovery-title">Reconnect this camera?</h2>
        <p>This stops all viewers of this source, including other camera entries that share it. Other sources keep playing. The saved address and credentials stay the same.</p>
        {busy && <p role="status">Checking camera recovery…</p>}
        {error && <p role="alert">{managementMessage(error)}</p>}
        {disabled && <p role="alert">Enable browser site storage before reconnecting so the operation can be recovered after a refresh.</p>}
        {unresolved ? <><p>The original reconnect may have completed. Check its status before starting another.</p><button type="button" disabled={busy || disabled} onClick={onCheck}>Check reconnect</button></> : <button type="button" class="primary-button" disabled={busy || disabled} onClick={onConfirm}>Reconnect Camera</button>}
        <button type="button" onClick={() => closeDialog(dialog.current)}>Close</button>
      </div>
    </dialog>}
  </>;
}

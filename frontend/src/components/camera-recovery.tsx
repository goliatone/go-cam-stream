import { useEffect, useRef } from "preact/hooks";
import type { ManagementErrorCode } from "../services/contracts";
import { openDialog, closeDialog } from "../services/shortcuts";
import { managementMessage } from "../services/management-copy";
import { CloseIcon } from "./icon";

/** Confirms a source reset for one camera; it opens as soon as it is shown. */
export function CameraRecovery({ name, busy, unresolved, error, disabled, onConfirm, onCheck, onClose }: {
  name: string; busy: boolean; unresolved: boolean; error?: ManagementErrorCode | undefined;
  disabled: boolean; onConfirm(): void; onCheck(): void; onClose(): void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const before = document.activeElement;
    openDialog(dialog.current);
    return () => { if (before instanceof HTMLElement && before.isConnected) before.focus(); };
  }, []);
  return <dialog ref={dialog} class="dialog" aria-labelledby="recovery-title" onClose={onClose}>
    <div class="dialog-inner"><div class="dialog-head"><h2 id="recovery-title">Reconnect {name}?</h2><button type="button" class="icon-button" aria-label="Close" onClick={() => closeDialog(dialog.current)}><CloseIcon /></button></div>
      <p>This stops all viewers of this source, including other camera entries that share it. Other sources keep playing. The saved address and sign-in stay the same.</p>
      {busy && <p role="status">Checking camera recovery…</p>}
      {error && <p role="alert">{managementMessage(error)}</p>}
      {disabled && <p role="alert">Enable browser site storage before reconnecting so the operation can be recovered after a refresh.</p>}
      {unresolved && <p>The original reconnect may have completed. Check again before starting another.</p>}
      <div class="dialog-actions"><button type="button" class="primary-button" disabled={busy || disabled} onClick={unresolved ? onCheck : onConfirm}>{unresolved ? "Check again" : "Reconnect camera"}</button></div>
    </div>
  </dialog>;
}

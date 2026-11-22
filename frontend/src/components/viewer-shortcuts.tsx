import { useEffect, useRef } from "preact/hooks";
import { CloseIcon } from "./icon";
import { openDialog, closeDialog } from "../services/shortcuts";
const SHORTCUTS: [keys: string[], action: string][] = [
  [["Space", "K"], "Play or pause"],
  [["M"], "Mute or unmute"],
  [["F"], "Fullscreen, or double-click the video"],
  [["P"], "Picture in picture"],
  [["S"], "Stop and free your viewer slot"],
  [["R"], "Try again"],
  [["[", "]"], "Previous or next camera"],
  [["1–9"], "Go to a camera in the list"],
  [["G"], "Switch between one camera and all cameras"],
  [["?"], "Show these shortcuts"],
];
export default function ViewerShortcuts({onClose}:{onClose():void}) {
 const shortcuts=useRef<HTMLDialogElement>(null);
 useEffect(()=>{openDialog(shortcuts.current)},[]);
 return (      <dialog
        ref={shortcuts}
        class="dialog"
        aria-labelledby="shortcuts-title"
        onClose={onClose}
        onClick={(event) => {
          if (event.target === event.currentTarget)
            closeDialog(shortcuts.current);
        }}
      >
        <div class="dialog-inner">
          <div class="dialog-head">
            <h2 id="shortcuts-title">Keyboard shortcuts</h2>
            <button
              type="button"
              class="icon-button"
              aria-label="Close"
              onClick={() => closeDialog(shortcuts.current)}
            >
              <CloseIcon />
            </button>
          </div>
          <dl class="key-list">
            {SHORTCUTS.map(([keys, action]) => (
              <div key={action}>
                <dt>{action}</dt>
                <dd>
                  {keys.map((key) => (
                    <kbd key={key}>{key}</kbd>
                  ))}
                </dd>
              </div>
            ))}
          </dl>
          <p class="dialog-note">Shortcuts are off while a field has focus.</p>
        </div>
      </dialog>);
}

import { useEffect, useRef } from "preact/hooks";

/**
 * The key of a page keyboard shortcut, lowercased, or undefined when the key
 * press belongs to the browser, the operating system or a focused field.
 */
export function shortcutKey(event: KeyboardEvent): string | undefined {
  if (
    event.defaultPrevented ||
    event.isComposing ||
    // A held key would toggle playback or step through cameras repeatedly.
    event.repeat ||
    event.metaKey ||
    event.ctrlKey ||
    event.altKey
  )
    return undefined;
  const target = event.target instanceof Element ? event.target : null;
  // Shortcuts pause while a form control has focus or a dialog is open.
  if (
    target?.closest("input, select, textarea, [contenteditable]") ||
    document.querySelector("dialog[open]")
  )
    return undefined;
  // Space and Enter keep activating the focused button or link.
  if (event.key === " " && target?.closest("button, a, summary"))
    return undefined;
  return event.key.toLowerCase();
}

/** Opens a modal dialog. Browsers without showModal still show it. */
export function openDialog(dialog: HTMLDialogElement | null) {
  if (!dialog || dialog.open) return;
  if (typeof dialog.showModal === "function") dialog.showModal();
  else dialog.setAttribute("open", "");
}
/** Closes a dialog; without close(), still fires its close event. */
export function closeDialog(dialog: HTMLDialogElement | null) {
  if (!dialog?.hasAttribute("open")) return;
  if (typeof dialog.close === "function") dialog.close();
  else {
    dialog.removeAttribute("open");
    dialog.dispatchEvent(new Event("close"));
  }
}

/**
 * Sends page shortcuts to `handle`, which returns true for the keys it uses.
 * The listener is registered once and always calls the latest `handle`, so a
 * key pressed right after a change never acts on the previous render.
 */
export function useShortcuts(handle: (key: string) => boolean) {
  const latest = useRef(handle);
  latest.current = handle;
  useEffect(() => {
    const press = (event: KeyboardEvent) => {
      const key = shortcutKey(event);
      if (key !== undefined && latest.current(key)) event.preventDefault();
    };
    window.addEventListener("keydown", press);
    return () => window.removeEventListener("keydown", press);
  }, []);
}

import type { ComponentChildren } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";
import { Icon, type IconName } from "./icons";

export type Placement = "above" | "below";

const clamp = (value: number, low: number, high: number) => Math.max(low, Math.min(high, value));

/** Places a floating element beside its anchor inside the page root. */
export function place(element: HTMLElement, anchor: HTMLElement, root: HTMLElement, prefer: Placement) {
  const frame = root.getBoundingClientRect();
  const target = anchor.getBoundingClientRect();
  const width = element.offsetWidth;
  const height = element.offsetHeight;
  const left = target.left - frame.left;
  const top = target.top - frame.top;
  const above = top - height - 10;
  const below = top + target.height + 10;
  const fitsBelow = below + height <= root.clientHeight - 8;
  const y = prefer === "below" ? (fitsBelow ? below : above) : (above >= 8 ? above : below);
  element.style.left = `${Math.round(clamp(left + target.width / 2 - width / 2, 8, root.clientWidth - width - 8))}px`;
  element.style.top = `${Math.round(clamp(y, 8, root.clientHeight - height - 8))}px`;
}

/**
 * Closes a floating element when a pointer goes down outside it and its anchor,
 * or on Escape when focus is elsewhere. Escape inside it is its own to handle.
 * The listeners start with the first frame that shows it.
 */
function useDismiss(element: { current: HTMLElement | null }, anchor: HTMLElement, close: (returnFocus: boolean) => void) {
  const latest = useRef(close);
  latest.current = close;
  useLayoutEffect(() => {
    const down = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!element.current?.contains(target) && !anchor.contains(target)) latest.current(false);
    };
    const key = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      event.preventDefault();
      latest.current(true);
    };
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", down, true);
      document.removeEventListener("keydown", key);
    };
  }, [anchor]);
}

/**
 * Places a floating element on every render, since device lists can arrive
 * after it opens, then focuses what `first` picks once it is in place, so
 * focus never scrolls the page toward its unplaced position.
 */
function usePlacedFocus(element: { current: HTMLElement | null }, anchor: HTMLElement, root: HTMLElement, placement: Placement,
  first: (element: HTMLElement) => HTMLElement | null) {
  const focused = useRef(false);
  useLayoutEffect(() => {
    const floating = element.current;
    if (!floating) return;
    place(floating, anchor, root, placement);
    if (focused.current) return;
    focused.current = true;
    (first(floating) ?? floating).focus({ preventScroll: true });
  });
}

export type MenuEntry =
  | { kind: "heading"; text: string }
  | { kind: "radio"; label: string; sub?: string; checked: boolean; select(): void }
  | { kind: "item"; label: string; icon: IconName; select(): void }
  | { kind: "separator" }
  | { kind: "note"; text: string };

const items = (menu: HTMLElement) => [...menu.querySelectorAll<HTMLElement>('[role^="menuitem"]')];

/**
 * A menu of actions or device choices. Arrow keys, Home and End move between
 * items; Escape closes it and returns focus to the button that opened it.
 */
export function Menu({ label, anchor, root, placement, entries, close }: {
  label: string; anchor: HTMLElement; root: HTMLElement; placement: Placement; entries: MenuEntry[];
  close(returnFocus: boolean): void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(ref, anchor, close);
  usePlacedFocus(ref, anchor, root, placement, (menu) => menu.querySelector<HTMLElement>('[aria-checked="true"]') ?? items(menu)[0] ?? null);
  function keydown(event: KeyboardEvent) {
    const menu = ref.current;
    if (!menu) return;
    const list = items(menu);
    const index = list.indexOf(document.activeElement as HTMLElement);
    const move = (next: number) => { event.preventDefault(); list[(next + list.length) % list.length]?.focus(); };
    switch (event.key) {
      case "ArrowDown": move(index + 1); break;
      case "ArrowUp": move(index < 0 ? -1 : index - 1); break;
      case "Home": move(0); break;
      case "End": move(-1); break;
      case "Escape": event.preventDefault(); event.stopPropagation(); close(true); break;
      // Focus returns to the menu button, then Tab continues from there.
      case "Tab": close(true); break;
    }
  }
  return <div ref={ref} class="menu" role="menu" aria-label={label} tabIndex={-1} onKeyDown={keydown}>
    {entries.map((entry, index) => {
      switch (entry.kind) {
        case "heading": return <p key={index} class="menu-heading" aria-hidden="true">{entry.text}</p>;
        case "separator": return <hr key={index} />;
        case "note": return <p key={index} class="menu-note">{entry.text}</p>;
        case "radio": return <button key={index} type="button" role="menuitemradio" aria-checked={entry.checked} tabIndex={-1}
          onClick={() => { close(true); entry.select(); }}>
          <Icon name="check" class="check" />
          <span class="menu-text"><span>{entry.label}</span>{entry.sub && <span class="menu-sub">{entry.sub}</span>}</span>
        </button>;
        case "item": return <button key={index} type="button" role="menuitem" tabIndex={-1}
          onClick={() => { close(true); entry.select(); }}>
          <Icon name={entry.icon} /><span class="menu-text"><span>{entry.label}</span></span>
        </button>;
      }
    })}
  </div>;
}

/**
 * A small dialog beside the control it explains. It takes focus, and Escape
 * closes it with `close(true)`, which returns focus to its control.
 */
export function Popover({ anchor, root, title, describedBy, close, children, class: extra }: {
  anchor: HTMLElement; root: HTMLElement; title: string; describedBy?: string; close(returnFocus: boolean): void;
  children: ComponentChildren; class?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(ref, anchor, close);
  usePlacedFocus(ref, anchor, root, "above",
    (pop) => pop.querySelector<HTMLElement>("[data-autofocus]") ?? pop.querySelector<HTMLElement>("button"));
  return <div ref={ref} class={extra ? `pop ${extra}` : "pop"} role="dialog" aria-label={title} aria-describedby={describedBy}
    tabIndex={-1} onKeyDown={(event) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      close(true);
    }}>
    {children}
  </div>;
}

export type Toast = { id: number; text: string; icon: IconName };

export function Notices({ toasts, children }: { toasts: Toast[]; children?: ComponentChildren }) {
  return <div class="notices">
    {children}
    {toasts.map((toast) => <div key={toast.id} class="toast" role="status"><Icon name={toast.icon} /><span>{toast.text}</span></div>)}
  </div>;
}

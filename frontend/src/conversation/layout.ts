import { useEffect, useRef, useState } from "preact/hooks";

export type Corner = "tl" | "tr" | "bl" | "br";
export const cornerNames: Record<Corner, string> = { tl: "top left", tr: "top right", bl: "bottom left", br: "bottom right" };

/** Formats a call length as 04:26 or 1:04:26. */
export function duration(seconds: number) {
  const pad = (value: number) => String(value).padStart(2, "0");
  const minutes = Math.floor(seconds / 60);
  return seconds >= 3600 ? `${Math.floor(seconds / 3600)}:${pad(minutes % 60)}:${pad(seconds % 60)}` : `${pad(minutes)}:${pad(seconds % 60)}`;
}

/** Runs `run` before the next paint, or on the next task where frames are unavailable. */
export function nextFrame(run: () => void) {
  if (typeof requestAnimationFrame === "function") requestAnimationFrame(run);
  else setTimeout(run, 0);
}

/**
 * Moves focus to `to` when the control `from` holds it, before `from` is
 * removed or rebuilt. A click that did not focus `from` leaves focus alone.
 */
export function handOff(from: EventTarget | null, to: HTMLElement | null | undefined) {
  if (to && from instanceof Node && from.contains(document.activeElement)) to.focus({ preventScroll: true });
}

/** The current time, refreshed every second while `active`. */
export function useClock(active: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active]);
  return now;
}

const wakeEvents = ["pointermove", "pointerdown", "keydown", "focusin", "wheel"] as const;

/**
 * True after `delay` ms without pointer, touch or keyboard input while
 * `active`. `hold` keeps the controls up, for example while a menu is open or
 * a control has focus, and is checked again each time the delay runs out.
 */
export function useAutoHide(active: boolean, hold: () => boolean, delay = 3000) {
  const [idle, setIdle] = useState(false);
  const holding = useRef(hold);
  holding.current = hold;
  useEffect(() => {
    setIdle(false);
    if (!active) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const arm = () => {
      clearTimeout(timer);
      timer = setTimeout(() => { if (holding.current()) arm(); else setIdle(true); }, delay);
    };
    const wake = () => { setIdle(false); arm(); };
    for (const type of wakeEvents) document.addEventListener(type, wake, { capture: true, passive: true });
    arm();
    return () => {
      clearTimeout(timer);
      for (const type of wakeEvents) document.removeEventListener(type, wake, { capture: true });
    };
  }, [active, delay]);
  return active && idle;
}

/**
 * The `idle` value to place overlays with. A press that starts on `selector`
 * inside `root` keeps the value it found until `settle` ms after the press
 * ends, so controls that return with the press cannot slide the pressed part
 * out from under the finger. It listens on window, ahead of the document
 * listeners in useAutoHide that bring the controls back.
 */
export function useHeldIdle(root: { current: HTMLElement | null }, selector: string, idle: boolean, settle = 150) {
  const latest = useRef(idle);
  latest.current = idle;
  const held = useRef<boolean | null>(null);
  const [, setSettled] = useState(0);
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const press = (event: PointerEvent) => {
      const part = (event.target as Element | null)?.closest?.(selector);
      if (!part || !root.current?.contains(part)) return;
      clearTimeout(timer);
      held.current ??= latest.current;
    };
    const release = () => {
      if (held.current === null) return;
      clearTimeout(timer);
      timer = setTimeout(() => { held.current = null; setSettled((value) => value + 1); }, settle);
    };
    window.addEventListener("pointerdown", press, true);
    window.addEventListener("pointerup", release, true);
    window.addEventListener("pointercancel", release, true);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("pointerdown", press, true);
      window.removeEventListener("pointerup", release, true);
      window.removeEventListener("pointercancel", release, true);
    };
  }, [selector, settle]);
  return held.current ?? idle;
}

export type Insets = { top: number; right: number; bottom: number; left: number };

/** Reads the safe-area insets from an element laid out with env() offsets. */
export function insets(frame: HTMLElement | null, width: number, height: number): Insets {
  if (!frame) return { top: 0, right: 0, bottom: 0, left: 0 };
  return {
    top: frame.offsetTop, left: frame.offsetLeft,
    right: Math.max(0, width - frame.offsetLeft - frame.offsetWidth),
    bottom: Math.max(0, height - frame.offsetTop - frame.offsetHeight),
  };
}

/**
 * Places the self view (or its pill) in its corner and the name tag beside it.
 * Corners stay clear of the top bar and, while it shows, the dock.
 */
export function placeOverlays({ root, tile, dock, tags, safe, corner, idle, compact }: {
  root: HTMLElement; tile: HTMLElement | null; dock: HTMLElement | null; tags: HTMLElement | null; safe: Insets;
  corner: Corner; idle: boolean; compact: boolean;
}) {
  const width = root.clientWidth;
  const height = root.clientHeight;
  const margin = compact ? 10 : 14;
  const top = safe.top + (idle ? margin : 64);
  const dockWidth = dock?.offsetWidth ?? 0;
  const dockLeft = (width - dockWidth) / 2;
  const dockTop = dock ? dock.offsetTop : height;
  // Anything bottom-aligned that would sit under the dock rises above it.
  const bottom = (x: number, w: number) => !idle && dock && x < dockLeft + dockWidth + 10 && x + w > dockLeft - 10
    ? height - dockTop + 10 : safe.bottom + margin;
  if (tile) {
    const w = tile.offsetWidth;
    const h = tile.offsetHeight;
    const x = corner[1] === "l" ? safe.left + margin : width - safe.right - margin - w;
    const y = corner[0] === "t" ? top : height - bottom(x, w) - h;
    tile.style.left = `${Math.round(x)}px`;
    tile.style.top = `${Math.round(y)}px`;
    // The first placement does not animate in from the origin.
    if (!tile.dataset.placed) nextFrame(() => { tile.dataset.placed = "true"; });
  }
  if (tags) {
    const left = safe.left + margin;
    tags.style.left = `${Math.round(left)}px`;
    if (tile && corner === "bl") {
      tags.style.top = `${Math.round(top)}px`;
      tags.style.bottom = "auto";
    } else {
      tags.style.top = "auto";
      tags.style.bottom = `${Math.round(bottom(left, tags.offsetWidth))}px`;
    }
  }
}

type Drag = { id: number; dx: number; dy: number; x0: number; y0: number; moved: boolean; touch: boolean };

/**
 * Pointer handlers that drag the self view and snap it to the nearest corner
 * on release. A tap without movement on a touch screen toggles its tools.
 */
export function useTileDrag(root: { current: HTMLElement | null }, settle: (corner: Corner) => void, tap: () => void) {
  const drag = useRef<Drag | null>(null);
  const [dragging, setDragging] = useState(false);
  function pointerDown(event: PointerEvent) {
    const tile = event.currentTarget as HTMLElement;
    if (event.button !== 0 || (event.target as Element).closest("button")) return;
    const box = tile.getBoundingClientRect();
    drag.current = { id: event.pointerId, dx: event.clientX - box.left, dy: event.clientY - box.top,
      x0: event.clientX, y0: event.clientY, moved: false, touch: event.pointerType !== "mouse" };
    tile.setPointerCapture?.(event.pointerId);
  }
  function pointerMove(event: PointerEvent) {
    const state = drag.current;
    const frame = root.current;
    if (!state || !frame || state.id !== event.pointerId) return;
    if (!state.moved && Math.hypot(event.clientX - state.x0, event.clientY - state.y0) < 5) return;
    if (!state.moved) { state.moved = true; setDragging(true); }
    const tile = event.currentTarget as HTMLElement;
    const box = frame.getBoundingClientRect();
    const limit = (value: number, size: number, room: number) => Math.max(6, Math.min(room - size - 6, value));
    tile.style.left = `${limit(event.clientX - box.left - state.dx, tile.offsetWidth, frame.clientWidth)}px`;
    tile.style.top = `${limit(event.clientY - box.top - state.dy, tile.offsetHeight, frame.clientHeight)}px`;
  }
  function pointerEnd(event: PointerEvent) {
    const state = drag.current;
    const frame = root.current;
    if (!state || state.id !== event.pointerId) return;
    drag.current = null;
    setDragging(false);
    if (!state.moved) { if (state.touch) tap(); return; }
    if (!frame) return;
    const tile = event.currentTarget as HTMLElement;
    const x = tile.offsetLeft + tile.offsetWidth / 2;
    const y = tile.offsetTop + tile.offsetHeight / 2;
    settle(`${y < frame.clientHeight / 2 ? "t" : "b"}${x < frame.clientWidth / 2 ? "l" : "r"}` as Corner);
  }
  return { dragging, handlers: { onPointerDown: pointerDown, onPointerMove: pointerMove, onPointerUp: pointerEnd, onPointerCancel: pointerEnd } };
}

/** The corner an arrow key moves the self view to, or null for other keys. */
export function cornerFor(key: string, corner: Corner): Corner | null {
  switch (key) {
    case "ArrowUp": return `t${corner[1]}` as Corner;
    case "ArrowDown": return `b${corner[1]}` as Corner;
    case "ArrowLeft": return `${corner[0]}l` as Corner;
    case "ArrowRight": return `${corner[0]}r` as Corner;
    default: return null;
  }
}

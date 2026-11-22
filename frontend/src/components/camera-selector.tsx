import type { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import type { CameraSummary, StreamRef } from "../services/contracts";
import { stateLabels } from "../services/copy";
import { linkFor } from "../services/links";
import { CameraIcon, MoreIcon } from "./icon";
import { describeView, StatusBadge, type ViewState } from "./status";

/** An entry of a camera's ⋯ menu: its label and what it does. */
export type CameraAction = readonly [label: string, run: () => void];

// Rows are real links to each camera's view. A plain click switches in place;
// modified and middle clicks keep the browser's behavior and open a new tab,
// which starts on that camera without first connecting to another.
const opensElsewhere = (event: MouseEvent) =>
  event.button !== 0 ||
  event.metaKey ||
  event.ctrlKey ||
  event.shiftKey ||
  event.altKey;

/**
 * The status a camera's row or tile shows, and whether it shows one. The
 * camera on screen shows this page's player state, never "Status unknown".
 * Others show the server's availability only when it says something: live,
 * starting or failed. Idle is not shown, because it does not establish that
 * the camera can play.
 */
export function cameraStatus(camera: CameraSummary, own?: ViewState) {
  const badge = own || describeView({ state: camera.availability });
  return {
    badge,
    shown: own ? badge.label !== stateLabels.unknown : badge.tone !== "idle",
    // A failed camera is dimmed.
    dim: !own && badge.tone === "danger",
  };
}

/**
 * A camera's row or tile: a link to its view, or a disabled box for a camera
 * without streams. The 1–9 keys choose the first nine cameras.
 */
export function CameraChoice({
  camera,
  index,
  current,
  dim,
  title,
  class: kind,
  onSelect,
  children,
}: {
  camera: CameraSummary;
  index: number;
  current: boolean;
  dim: boolean;
  title?: string | undefined;
  class: string;
  onSelect: (selection: StreamRef) => void;
  children: ComponentChildren;
}) {
  const first = camera.streams[0];
  if (!first)
    return (
      <div class={kind} aria-disabled="true">
        {children}
      </div>
    );
  return (
    <a
      class={dim ? `${kind} dim` : kind}
      href={linkFor(camera)}
      {...(current ? { "aria-current": "page" as const } : {})}
      {...(index < 9 ? { "aria-keyshortcuts": String(index + 1) } : {})}
      {...(title ? { title } : {})}
      onClick={(event) => {
        if (opensElsewhere(event)) return;
        event.preventDefault();
        onSelect({ camera_id: camera.id, stream_id: first.id });
      }}
    >
      {children}
    </a>
  );
}

export function CameraLinks({
  cameras,
  selected,
  view,
  onSelect,
  titles = false,
  actions,
}: {
  cameras: readonly CameraSummary[];
  selected: StreamRef | undefined;
  /** This page's player state, shown on the selected row. */
  view?: ViewState;
  onSelect: (selection: StreamRef) => void;
  /** Name each row and its status in a tooltip, for the collapsed rail that hides both. */
  titles?: boolean;
  /** Each camera's ⋯ menu; a camera without entries has none. */
  actions?: ((camera: CameraSummary) => readonly CameraAction[]) | undefined;
}) {
  return (
    <ul class="camera-list">
      {cameras.map((camera, i) => {
        const first = camera.streams[0];
        const current = selected?.camera_id === camera.id;
        const { badge, shown, dim } = cameraStatus(camera, current ? view : undefined);
        const items = first ? (actions?.(camera) ?? []) : [];
        // The tile's colour follows the camera's place in the list, so the
        // first eight never share one. Its dot repeats the status, which is all
        // the collapsed rail shows.
        const content = (
          <>
            <span class={`camera-tile tile-${i % 8} tone-${badge.tone}`}>
              <CameraIcon />
              {first && shown && <span class="status-dot" />}
            </span>
            <span class="camera-label">
              <strong>{camera.name}</strong>
              {!first ? (
                <span class="small">No supported streams</span>
              ) : (
                shown && <StatusBadge view={badge} />
              )}
            </span>
            {first && i < 9 && (
              <kbd class="camera-key" aria-hidden="true">
                {i + 1}
              </kbd>
            )}
          </>
        );
        return (
          <li key={camera.id} class={items.length ? "has-menu" : undefined}>
            <CameraChoice
              camera={camera}
              index={i}
              current={current}
              dim={dim}
              title={
                titles
                  ? shown
                    ? `${camera.name} · ${badge.label}`
                    : camera.name
                  : undefined
              }
              class="camera-choice"
              onSelect={onSelect}
            >
              {content}
            </CameraChoice>
            {items.length > 0 && <CameraMenu name={camera.name} items={items} />}
          </li>
        );
      })}
    </ul>
  );
}

// A ⋯ button and the menu it opens over the page. The menu takes focus, moves
// with the arrow keys, Home and End, and closes on Esc, Tab, a choice or a
// press elsewhere. It is placed from the button, since the rail scrolls and
// would clip it; scrolling or resizing closes it.
function CameraMenu({
  name,
  items,
}: {
  name: string;
  items: readonly CameraAction[];
}) {
  const [at, setAt] = useState<{ top: number; right: number }>();
  const button = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  // A press on the button of an open menu closes it, though focus leaving
  // the menu may have closed it before the click arrives.
  const closing = useRef(false);
  const open = Boolean(at);
  const close = (refocus?: boolean) => {
    setAt(undefined);
    if (refocus) button.current?.focus();
  };
  useEffect(() => {
    if (!open) return;
    menu.current?.querySelector("button")?.focus();
    const away = (event: Event) => {
      const target = event.target as Node;
      if (!menu.current?.contains(target) && !button.current?.contains(target))
        close();
    };
    const moved = () => close();
    document.addEventListener("pointerdown", away);
    addEventListener("resize", moved);
    addEventListener("scroll", moved, true);
    return () => {
      document.removeEventListener("pointerdown", away);
      removeEventListener("resize", moved);
      removeEventListener("scroll", moved, true);
    };
  }, [open]);
  return (
    <>
      <button
        ref={button}
        type="button"
        class="icon-button camera-more"
        aria-label={`${name} actions`}
        aria-haspopup="menu"
        aria-expanded={open}
        onPointerDown={() => {
          closing.current = open;
        }}
        onClick={(event) => {
          const box = event.currentTarget.getBoundingClientRect();
          if (open || closing.current) close();
          else
            setAt({
              top: box.bottom + 4,
              right: document.documentElement.clientWidth - box.right,
            });
          closing.current = false;
        }}
      >
        <MoreIcon />
      </button>
      {at && (
        <div
          ref={menu}
          class="menu"
          role="menu"
          aria-label={`${name} actions`}
          style={{ top: `${at.top}px`, right: `${at.right}px` }}
          onKeyDown={(event) => {
            const list = [...event.currentTarget.querySelectorAll("button")];
            const from = list.indexOf(document.activeElement as HTMLButtonElement);
            const to = { ArrowDown: from + 1, ArrowUp: from - 1, Home: 0, End: -1 }[
              event.key
            ];
            if (event.key === "Tab") return close();
            if (event.key === "Escape") close(true);
            else if (to === undefined) return;
            else list.at(to % list.length)?.focus();
            event.preventDefault();
          }}
          onFocusOut={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget as Node | null))
              close();
          }}
        >
          {items.map(([label, run]) => (
            <button
              key={label}
              type="button"
              class="menu-item"
              role="menuitem"
              tabIndex={-1}
              onClick={() => {
                close(true);
                run();
              }}
            >
              {label}
            </button>
          ))}
        </div>
      )}
    </>
  );
}

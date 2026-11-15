import type { ComponentChildren, RefObject } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import type {
  CameraSummary,
  StreamRef,
  StreamSummary,
} from "../services/contracts";
import { lookOf, sizeOf } from "./catalog";
import {
  describeCamera,
  describePhase,
  faviconHref,
  freshness,
  secs,
  type Describe,
} from "./copy";
import { Harness, type UISettings } from "./harness";
import { useMedia, useNow, useSnapshot } from "./hooks";
import {
  BrandMark,
  CameraIcon,
  CloseIcon,
  KeyboardIcon,
  LockIcon,
  OfflineIcon,
  RefreshIcon,
  SidebarIcon,
} from "./icons";
import {
  Player,
  type Alternative,
  type MediaReport,
  type PlayerCommands,
} from "./player";
import {
  linkFor,
  type CameraLive,
  type CatalogState,
  type Phase,
  type Sim,
} from "./sim";

const isApple = /Mac|iPhone|iPad/.test(navigator.userAgent);
const toneRank: Record<Describe["tone"], number> = {
  live: 0,
  idle: 1,
  warn: 2,
  unknown: 3,
  danger: 4,
};

export function PrototypeApp({ sim }: { sim: Sim }) {
  const snap = useSnapshot(sim);
  const { catalog, phase, selection } = snap;
  const [ui, setUI] = useState<UISettings>({
    theme: "system",
    operator: false,
    todayStatus: false,
  });
  const phone = useMedia("(max-width: 767px)");
  const immersive = useMedia("(orientation: landscape) and (max-height: 500px)");
  const [collapsed, setCollapsed] = useState(
    () => matchMedia("(max-width: 1023px)").matches,
  );
  const [media, setMedia] = useState<MediaReport>({
    paused: false,
    pip: false,
    fullscreen: false,
  });
  const ticking =
    phase.kind === "connecting" ||
    phase.kind === "reconnecting" ||
    phase.kind === "retrying" ||
    catalog.kind === "down";
  const now = useNow(ticking ? 250 : 1000);
  const commands = useRef<PlayerCommands | null>(null);
  const shortcuts = useRef<HTMLDialogElement>(null);
  const sheet = useRef<HTMLDialogElement>(null);
  const rail = useRef<HTMLElement>(null);

  const cameras = catalog.kind === "ready" ? catalog.cameras : [];
  const camera = cameras.find((c) => c.id === selection?.camera_id);
  const stream = camera?.streams.find((s) => s.id === selection?.stream_id);
  const state = describePhase(phase, media.paused, now);
  // The selected row shows this page's own state; the others show the
  // proposed server status (or today's "unknown").
  const statusOf = (c: CameraSummary): Describe =>
    c.id === camera?.id
      ? state
      : describeCamera(snap.cameras[c.id], ui.todayStatus);
  const alternatives: Alternative[] = cameras
    .filter((c) => c.id !== camera?.id && !snap.cameras[c.id]?.fault)
    .map((c) => ({ camera: c, status: statusOf(c) }))
    .sort((a, b) => toneRank[a.status.tone] - toneRank[b.status.tone])
    .slice(0, 3);

  useEffect(() => {
    const root = document.documentElement;
    if (ui.theme === "system") delete root.dataset.theme;
    else root.dataset.theme = ui.theme;
  }, [ui.theme]);

  // Deep link: the URL always names the camera and stream on screen.
  useEffect(() => {
    if (camera && stream)
      history.replaceState(history.state, "", linkFor(camera, stream.id));
  }, [camera?.id, stream?.id]);

  useEffect(() => {
    document.title = camera
      ? `${camera.name} · ${state.word} · camstream`
      : `${catalogWord(catalog)} · camstream`;
    const icon = document.querySelector<HTMLLinkElement>("link[rel='icon']");
    if (icon) icon.href = faviconHref(camera ? state.tone : null);
  }, [camera?.name, state.word, state.tone, catalog.kind]);

  useEffect(() => {
    const update = () => sim.setPresence(document.hidden, media.pip);
    update();
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, [sim, media.pip]);

  const choose = (ref: StreamRef) => {
    sheet.current?.close();
    sim.select(ref);
  };
  const chooseCamera = (c: CameraSummary) => {
    const first = c.streams[0];
    if (first) choose({ camera_id: c.id, stream_id: first.id });
  };
  // Plain click switches in place. Modified and middle clicks keep the
  // browser's behavior, so the deep link opens in a new tab.
  const onLink = (event: MouseEvent, c: CameraSummary) => {
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    )
      return;
    event.preventDefault();
    chooseCamera(c);
  };
  const openSheet = () => sheet.current?.showModal();
  const chooseAnother = () => {
    if (immersive || media.fullscreen) return openSheet();
    setCollapsed(false);
    requestAnimationFrame(() => {
      const links = Array.from(
        rail.current?.querySelectorAll<HTMLAnchorElement>("a.cam-row") ?? [],
      );
      const target =
        links.find((link) => !link.hasAttribute("aria-current")) ?? links[0];
      target?.focus({ preventScroll: true });
      if (phone) target?.scrollIntoView({ block: "center", behavior: "smooth" });
    });
  };

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey)
        return;
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest("input, select, textarea, [contenteditable]")) return;
      if (document.querySelector("dialog[open]")) return;
      const player = commands.current;
      const index = cameras.findIndex((c) => c.id === camera?.id);
      const go = (i: number) => {
        const next = cameras[(i + cameras.length) % cameras.length];
        if (next) chooseCamera(next);
      };
      switch (event.key) {
        case " ":
          if (target?.closest("button, a, summary")) return;
          player?.playPause();
          break;
        case "k":
        case "K":
          player?.playPause();
          break;
        case "m":
        case "M":
          player?.mute();
          break;
        case "f":
        case "F":
          player?.fullscreen();
          break;
        case "p":
        case "P":
          player?.pip();
          break;
        case "s":
        case "S":
          player?.stop();
          break;
        case "r":
        case "R":
          if (catalog.kind === "down") sim.retryCatalog();
          else sim.retry();
          break;
        case "[":
          if (cameras.length) go(index - 1);
          break;
        case "]":
          if (cameras.length) go(index + 1);
          break;
        case "?":
          shortcuts.current?.showModal();
          break;
        default:
          if (/^[1-9]$/.test(event.key) && cameras[Number(event.key) - 1]) {
            go(Number(event.key) - 1);
            break;
          }
          return;
      }
      event.preventDefault();
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  });

  const noRail =
    catalog.kind === "empty" || catalog.kind === "denied" || catalog.kind === "down";
  const ready = catalog.kind === "ready" && camera && stream;

  return (
    <div
      class="pt-app"
      data-rail={collapsed && !phone ? "collapsed" : "open"}
      data-norail={noRail ? "" : undefined}
    >
      <header class="pt-topbar">
        {!noRail && (
          <button
            type="button"
            class="icon-btn rail-toggle"
            aria-controls="pt-rail"
            aria-expanded={!collapsed}
            aria-label={collapsed ? "Expand camera list" : "Collapse camera list"}
            title={collapsed ? "Expand camera list" : "Collapse camera list"}
            onClick={() => setCollapsed(!collapsed)}
          >
            <SidebarIcon />
          </button>
        )}
        <span class="brand">
          <BrandMark />
          <span class="brand-name">camstream</span>
        </span>
        <span class="topbar-sep" aria-hidden="true" />
        {catalog.kind === "loading" ? (
          <span class="skeleton skeleton-title" aria-hidden="true" />
        ) : (
          <h1 class="pt-title">{camera?.name ?? "Cameras"}</h1>
        )}
        <div class="topbar-end">
          <button
            type="button"
            class="icon-btn kbd-btn"
            aria-label="Keyboard shortcuts"
            aria-keyshortcuts="?"
            title="Keyboard shortcuts (?)"
            onClick={() => shortcuts.current?.showModal()}
          >
            <KeyboardIcon />
          </button>
        </div>
      </header>

      <main class="pt-main">
        {ready ? (
          <Player
            sim={sim}
            camera={camera}
            stream={stream}
            phase={phase}
            state={state}
            operator={snap.operator}
            operatorMode={ui.operator}
            alternatives={alternatives}
            immersive={immersive}
            now={now}
            commands={commands}
            onMedia={setMedia}
            onSelect={choose}
            onChooseCamera={chooseAnother}
            onOpenCameras={openSheet}
          />
        ) : catalog.kind === "loading" || catalog.kind === "ready" ? (
          <div class="stage stage-skeleton" aria-busy="true">
            <span class="sr-only">Loading cameras…</span>
          </div>
        ) : (
          <PageMessage
            catalog={catalog}
            now={now}
            onRetry={() => sim.retryCatalog()}
          />
        )}
      </main>

      {ready ? (
        <StreamInfo
          camera={camera}
          stream={stream}
          phase={phase}
          live={snap.cameras[camera.id]}
          now={now}
          onStream={(id) => choose({ camera_id: camera.id, stream_id: id })}
        />
      ) : catalog.kind === "loading" ? (
        <section class="pt-info" aria-hidden="true">
          <div class="info-inner">
            <span class="skeleton skeleton-line" />
          </div>
        </section>
      ) : null}

      {!noRail && (
        <nav id="pt-rail" ref={rail} class="pt-rail" aria-label="Cameras">
          <div class="rail-head">
            <h2>Cameras</h2>
            {cameras.length > 0 && (
              <span class="rail-count">{cameras.length}</span>
            )}
          </div>
          {catalog.kind === "loading" ? (
            <SkeletonRows />
          ) : (
            <CameraLinks
              cameras={cameras}
              current={camera?.id}
              statusOf={statusOf}
              collapsed={collapsed && !phone}
              onLink={onLink}
            />
          )}
          <p class="rail-note">
            One camera per tab.{" "}
            {phone ? "Long-press" : `${isApple ? "⌘" : "Ctrl"}-click`} a camera
            to open it in a new tab.
          </p>
        </nav>
      )}

      <dialog
        ref={sheet}
        class="dialog sheet on-surface"
        aria-labelledby="pt-sheet-title"
        onClick={(e) => {
          if (e.target === e.currentTarget) e.currentTarget.close();
        }}
      >
        <div class="dialog-inner">
          <div class="dialog-head">
            <h2 id="pt-sheet-title">Cameras</h2>
            <button
              type="button"
              class="icon-btn"
              aria-label="Close"
              onClick={() => sheet.current?.close()}
            >
              <CloseIcon />
            </button>
          </div>
          <CameraLinks
            cameras={cameras}
            current={camera?.id}
            statusOf={statusOf}
            collapsed={false}
            onLink={onLink}
          />
        </div>
      </dialog>
      <ShortcutsDialog dialogRef={shortcuts} />
      <Harness sim={sim} snap={snap} ui={ui} onUI={setUI} />
    </div>
  );
}

function catalogWord(catalog: CatalogState) {
  switch (catalog.kind) {
    case "loading":
    case "ready":
      return "Loading";
    case "empty":
      return "No cameras";
    case "denied":
      return "No access";
    case "down":
      return "Can't connect";
  }
}

function CameraLinks(props: {
  cameras: readonly CameraSummary[];
  current: string | undefined;
  statusOf: (camera: CameraSummary) => Describe;
  collapsed: boolean;
  onLink: (event: MouseEvent, camera: CameraSummary) => void;
}) {
  return (
    <ul class="cam-list">
      {props.cameras.map((camera, i) => {
        const status = props.statusOf(camera);
        return (
          <li key={camera.id}>
            <a
              class="cam-row"
              href={linkFor(camera)}
              aria-current={camera.id === props.current ? "page" : undefined}
              title={
                props.collapsed ? `${camera.name} · ${status.label}` : undefined
              }
              onClick={(e) => props.onLink(e, camera)}
            >
              <span
                class="cam-tile"
                style={`--hue:${lookOf(camera.id).hue}`}
                aria-hidden="true"
              >
                <CameraIcon size={20} />
                <span class="dot" data-tone={status.tone} />
              </span>
              <span class="cam-text">
                <span class="cam-name">{camera.name}</span>
                <span class="cam-status">{status.label}</span>
              </span>
              {i < 9 && (
                <kbd class="cam-key" aria-hidden="true">
                  {i + 1}
                </kbd>
              )}
            </a>
          </li>
        );
      })}
    </ul>
  );
}

function SkeletonRows() {
  return (
    <div aria-hidden="true">
      {[0, 1, 2, 3].map((i) => (
        <div key={i} class="skeleton-row">
          <span class="skeleton" />
          <span class="skeleton" />
        </div>
      ))}
    </div>
  );
}

function StreamInfo(props: {
  camera: CameraSummary;
  stream: StreamSummary;
  phase: Phase;
  live: CameraLive | undefined;
  now: number;
  onStream: (id: string) => void;
}) {
  const { camera, stream, phase, live, now } = props;
  const size = sizeOf(stream.id);
  const transport = "transport" in phase ? phase.transport.toUpperCase() : null;
  const fresh = freshness(live?.observedAt ?? null, now);
  const mine =
    phase.kind === "live" ||
    phase.kind === "blocked" ||
    phase.kind === "reconnecting";
  const { video, audio } = stream.capabilities;
  const watching =
    live && live.observedAt !== null
      ? mine && live.viewers === 1
        ? "Only you watching"
        : `${live.viewers} watching`
      : null;
  return (
    <section class="pt-info" aria-label="Stream details">
      <div class="info-inner">
        {camera.streams.length > 1 ? (
          <div class="seg" role="radiogroup" aria-label="Stream">
            {camera.streams.map((s) => (
              <label key={s.id} class="seg-opt">
                <input
                  type="radio"
                  name="pt-stream"
                  value={s.id}
                  checked={s.id === stream.id}
                  onChange={() => props.onStream(s.id)}
                />
                <span>
                  {s.name}
                  <small>{sizeOf(s.id).height}p</small>
                </span>
              </label>
            ))}
          </div>
        ) : (
          <span class="info-stream">{stream.name} stream</span>
        )}
        <ul class="stats">
          <li>{video && audio ? "Video + audio" : video ? "Video only" : "Audio only"}</li>
          <li>
            {size.width}×{size.height}
          </li>
          {transport && <li>WebRTC · {transport}</li>}
          {watching && <li>{watching}</li>}
          <li class="fresh" data-stale={fresh.stale ? "" : undefined}>
            <span class="dot" aria-hidden="true" />
            {fresh.text}
          </li>
        </ul>
      </div>
    </section>
  );
}

function PageMessage(props: {
  catalog: CatalogState;
  now: number;
  onRetry: () => void;
}) {
  const { catalog } = props;
  if (catalog.kind === "denied")
    return (
      <Panel
        icon={<LockIcon />}
        title="You don't have access"
        actions={
          <button
            type="button"
            class="btn btn-primary"
            onClick={() => location.reload()}
          >
            Reload page
          </button>
        }
      >
        <p>
          This account can't view these cameras. Ask the person who runs this
          camstream to add you.
        </p>
      </Panel>
    );
  if (catalog.kind === "down")
    return (
      <Panel
        icon={<OfflineIcon />}
        title="Can't reach camstream"
        actions={
          <button type="button" class="btn btn-primary" onClick={props.onRetry}>
            <RefreshIcon size={18} />
            Try now
          </button>
        }
      >
        <p>Check that this device is connected to your private network.</p>
        <p class="panel-meta" aria-hidden="true">
          Trying again in {secs(catalog.at - props.now)} s · attempt{" "}
          {catalog.attempt}
        </p>
        <p class="sr-only">camstream keeps trying automatically.</p>
      </Panel>
    );
  return (
    <Panel icon={<CameraIcon />} title="No cameras yet">
      <p>Cameras show up here once they're added to camstream's configuration.</p>
    </Panel>
  );
}

function Panel(props: {
  icon: ComponentChildren;
  title: string;
  children: ComponentChildren;
  actions?: ComponentChildren;
}) {
  return (
    <div class="panel on-surface" role="status">
      <span class="panel-icon" aria-hidden="true">
        {props.icon}
      </span>
      <h2>{props.title}</h2>
      {props.children}
      {props.actions ? <div class="card-actions">{props.actions}</div> : null}
    </div>
  );
}

const SHORTCUTS: { keys: string[]; join: string; what: string }[] = [
  { keys: ["Space", "K"], join: "or", what: "Play or pause" },
  { keys: ["M"], join: "", what: "Mute or unmute" },
  { keys: ["F"], join: "", what: "Fullscreen" },
  { keys: ["P"], join: "", what: "Picture in picture" },
  { keys: ["S"], join: "", what: "Stop and free your viewer slot" },
  { keys: ["R"], join: "", what: "Try again" },
  { keys: ["[", "]"], join: "or", what: "Previous or next camera" },
  { keys: ["1", "9"], join: "to", what: "Jump to a camera" },
  { keys: ["?"], join: "", what: "Show these shortcuts" },
];

function ShortcutsDialog(props: {
  dialogRef: RefObject<HTMLDialogElement | null>;
}) {
  return (
    <dialog
      ref={props.dialogRef}
      class="dialog on-surface"
      aria-labelledby="pt-keys-title"
      onClick={(e) => {
        if (e.target === e.currentTarget) e.currentTarget.close();
      }}
    >
      <div class="dialog-inner">
        <div class="dialog-head">
          <h2 id="pt-keys-title">Keyboard shortcuts</h2>
          <button
            type="button"
            class="icon-btn"
            aria-label="Close"
            onClick={() => props.dialogRef.current?.close()}
          >
            <CloseIcon />
          </button>
        </div>
        <dl class="keys">
          {SHORTCUTS.map(({ keys, join, what }) => (
            <div key={what} class="key-row">
              <dt>{what}</dt>
              <dd>
                {keys.map((key, i) => (
                  <span key={key} class="key-group">
                    {i > 0 ? <span class="key-sep">{` ${join} `}</span> : null}
                    <kbd>{key}</kbd>
                  </span>
                ))}
              </dd>
            </div>
          ))}
        </dl>
        <p class="dialog-note">Shortcuts are ignored while you type in a field.</p>
      </div>
    </dialog>
  );
}

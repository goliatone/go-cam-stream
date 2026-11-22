import { useEffect, useRef, useState } from "preact/hooks";
import type {
  CameraList,
  CameraSummary,
  ErrorBody,
  StreamRef,
  StreamStatus,
  StreamSummary,
} from "./services/contracts";
import { LazyManagement, LazyShortcuts } from "./components/lazy-management";
import type { ManagementDialogProps, ManagementMode } from "./components/camera-management";
import { CameraLinks, type CameraAction } from "./components/camera-selector";
import { CameraGrid } from "./components/camera-grid";
import type { PreviewSource } from "./components/tile-preview";
import appIcon from "./components/app-icon.svg?no-inline";
import {
  CameraIcon,
  CloseIcon,
  FocusIcon,
  GridIcon,
  KeyboardIcon,
  PlusIcon,
  RefreshIcon,
  SettingsIcon,
  SidebarIcon,
} from "./components/icon";
import { Player, type PlayerPresentation } from "./components/player";
import {
  describeView,
  duration,
  ErrorDetails,
  StatusBadge,
  useTick,
  type ViewState,
} from "./components/status";
import { errorCopy, stateLabels } from "./services/copy";
import { linkFor, resolveSelection } from "./services/links";
import { pageTitle, showPageState } from "./services/page-meta";
import { closeDialog, openDialog, useShortcuts } from "./services/shortcuts";

export interface ViewerInput {
  selection: StreamRef;
  status?: StreamStatus | undefined;
  /** When this page last received a newer observation (performance.now()). */
  statusAt?: number | undefined;
  /** Since when this page has asked for status; unset while it does not. */
  observingSince?: number | undefined;
  presentation: PlayerPresentation;
}
export interface AppProps {
  catalog: CameraList;
  loading?: boolean;
  error?: ErrorBody | undefined;
  viewer?: ViewerInput | undefined;
  /** Selection requested by the page address; resolved like any other request. */
  initialSelection?: Partial<StreamRef>;
  onReload?: () => void;
  onAddCamera?: ((mode?: ManagementMode) => void) | undefined;
  onCameraSettings?: ((cameraID: string) => void) | undefined;
  watchSelection?: StreamRef | undefined;
  /** Asks to reset the source of a stream, after its own confirmation. */
  onReconnect?: ((ref: StreamRef) => void) | undefined;
  /** A reset whose outcome is not known yet keeps Reconnect on offer. */
  recoveryPending?: StreamRef | undefined;
  management?: ManagementDialogProps | undefined;
  catalogStale?: boolean | undefined;
  /** Asks for the camera list again; it rejects when the list stays stale. */
  refreshCatalog?: (() => Promise<unknown>) | undefined;
  /** The camera to play: the selection in Focus view, none in Grid view. */
  onSelect?: (selection: StreamRef | undefined) => void;
  /** What each camera's grid tile shows. */
  previewFor?: ((cameraID: string) => PreviewSource) | undefined;
}
/** Status updates that stop for this long, while the page asks, warn. */
const STALE_MS = 10000;
const sameStream = (a: StreamRef, b: StreamRef) =>
  a.camera_id === b.camera_id && a.stream_id === b.stream_id;
// Below 1024 px the rail starts as icons, leaving the width to the video,
// until someone opens or collapses it; this device then keeps that choice.
const narrow = () =>
  typeof matchMedia === "function" && matchMedia("(max-width: 1023px)").matches;
const RAIL_KEY = "camstream.rail";
const NO_PREVIEW = (): PreviewSource => ({ kind: "none" });
function initialRail() {
  try {
    const saved = localStorage.getItem(RAIL_KEY);
    if (saved) return saved === "open";
  } catch {}
  return !narrow();
}


export function App({
  catalog,
  loading = false,
  error,
  viewer,
  initialSelection,
  onSelect,
  onReload,
  onAddCamera, onCameraSettings, watchSelection, onReconnect, recoveryPending, management, catalogStale,
  refreshCatalog,
  previewFor = NO_PREVIEW,
}: AppProps) {
  const [requested, setRequested] = useState(initialSelection);
  // Focus shows one camera; Grid, offered with two or more, shows them all.
  const [gridChosen, setGrid] = useState(false);
  const [railOpen, setRailOpen] = useState(initialRail);
  const [sheetOpen, setSheetOpen] = useState(false);
  const sheet = useRef<HTMLDialogElement>(null);
  const [helpOpen, setHelpOpen] = useState(false);
  const [notice, setNotice] = useState("");
  const noticeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const viewerRoot = useRef<HTMLElement>(null);
  // Resolve against the current filtered catalog on every render. Never retain a removed scope.
  const cameras = error || loading ? [] : catalog.cameras;
  const selected = resolveSelection(cameras, requested);
  const camera = cameras.find((item) => item.id === selected?.camera_id);
  const stream = camera?.streams.find(
    (item) => item.id === selected?.stream_id,
  );
  const grid = gridChosen && cameras.length > 1;
  // The grid plays nothing, so it holds no viewer slot.
  const playing = grid ? undefined : selected;
  const current =
    playing && viewer && sameStream(playing, viewer.selection)
      ? viewer
      : undefined;
  const status =
    current?.status?.stream_id === stream?.id ? current?.status : undefined;
  const statusAt = status && current?.statusAt;
  const observingSince = current?.observingSince;
  const presentation = current?.presentation ?? { state: "unknown" as const };
  const view = describeView(presentation);
  // "Status unknown" adds nothing to a tab title.
  const word = view.label === stateLabels.unknown ? undefined : view.label;
  const noRail = Boolean(error) || (!loading && !cameras.length);
  const others = cameras.some(
    (item) => item.id !== camera?.id && item.streams.length > 0,
  );
  // This page holds one of the stream's viewer slots.
  const mine = presentation.state === "playing" || presentation.state === "reconnecting";

  // Notify the integration owner of the camera to play, including initial
  // selection, catalog revocation and the grid, which plays none. This
  // callback performs no transport work here.
  useEffect(() => {
    onSelect?.(playing);
  }, [playing?.camera_id, playing?.stream_id]);
  useEffect(() => {
    if (grid) showPageState(pageTitle("All cameras"), null);
    else if (camera) showPageState(pageTitle(camera.name, word), view.tone);
    else showPageState(pageTitle(), error ? "danger" : null);
  }, [grid, camera?.name, word, view.tone, Boolean(error)]);
  // A list down to one camera returns to Focus; a reload in between does not.
  useEffect(() => {
    if (!loading && !error && cameras.length < 2) setGrid(false);
  }, [cameras.length, loading, Boolean(error)]);

  useEffect(() => {
    if (!watchSelection) return;
    setRequested(watchSelection);
    setGrid(false);
  }, [watchSelection]);
  useEffect(() => () => clearTimeout(noticeTimer.current), []);
  // The camera on screen can leave the list, removed or no longer shared. The
  // next playable camera takes its place, and the page says why. A reload or a
  // failed list in between keeps the camera that was on screen.
  const shown = useRef<CameraSummary | undefined>(undefined);
  useEffect(() => {
    if (loading || error) return;
    const gone = shown.current;
    if (gone && !cameras.some((item) => item.id === gone.id))
      tell(`${gone.name} was removed from the camera list.`);
    shown.current = camera;
  }, [camera, cameras, loading, Boolean(error)]);

  function select(next: StreamRef) {
    if (selected && sameStream(selected, next)) return;
    setRequested(next);
  }
  // A row or tile names a camera, which opens in Focus view; choosing the one
  // on screen keeps its stream.
  function choose(next: StreamRef) {
    const root = viewerRoot.current;
    // Switching replaces the player or the grid. Focus on one of their
    // controls, or on the sheet the Cameras button opened, would fall back to
    // the page.
    const keepFocus =
      sheetOpen || Boolean(root?.contains(document.activeElement));
    const same = next.camera_id === selected?.camera_id;
    hideCameras();
    setGrid(false);
    if (same && !grid) return;
    if (!same) select(next);
    if (keepFocus) root?.focus({ preventScroll: true });
  }
  function showGrid(on: boolean) {
    const root = viewerRoot.current;
    const keepFocus = Boolean(root?.contains(document.activeElement));
    setGrid(on);
    if (keepFocus) root?.focus({ preventScroll: true });
  }
  function chooseCamera(item: CameraSummary | undefined) {
    const first = item?.streams[0];
    if (item && first) choose({ camera_id: item.id, stream_id: first.id });
  }
  function showCameras() {
    setSheetOpen(true);
    openDialog(sheet.current);
  }
  function hideCameras() {
    closeDialog(sheet.current);
    setSheetOpen(false);
  }
  // A closed dialog returns focus to the control that opened it. A camera or
  // stream switch may have replaced that control along with the player; keep
  // focus in the viewer rather than on the page.
  function dialogClosed() {
    const active = document.activeElement;
    if (!active || active === document.body)
      viewerRoot.current?.focus({ preventScroll: true });
  }
  const chooseStream = (stream_id: string) =>
    camera && select({ camera_id: camera.id, stream_id });
  // The stream whose source Reconnect resets: for the camera on screen, the
  // stream it shows; for another, the first whose source needs it.
  function needsReconnect(item: CameraSummary): StreamRef | undefined {
    const found = (item.id === camera?.id && stream ? [stream] : item.streams).find(
      (one) =>
        one.can_reconnect &&
        (one.recovery_required ||
          (recoveryPending?.camera_id === item.id &&
            recoveryPending.stream_id === one.id)),
    );
    return found && { camera_id: item.id, stream_id: found.id };
  }
  function tell(text: string) {
    clearTimeout(noticeTimer.current);
    setNotice(text);
    noticeTimer.current = setTimeout(() => setNotice(""), 4000);
  }
  function copyLink(item: CameraSummary) {
    const href = new URL(linkFor(item), location.href).href;
    // Pages without a secure origin have no clipboard.
    void (navigator.clipboard?.writeText(href) ?? Promise.reject()).then(
      () => tell(`Copied the link to ${item.name}.`),
      () => tell("Couldn’t copy the link. Open the camera and copy the page address."),
    );
  }
  const menu = (item: CameraSummary): CameraAction[] => {
    const ref = onReconnect && needsReconnect(item);
    return [
      ...(onCameraSettings
        ? [["Settings", () => onCameraSettings(item.id)] as const]
        : []),
      ...(ref ? [["Reconnect", () => onReconnect(ref)] as const] : []),
      ["Copy link", () => copyLink(item)],
    ];
  };
  // Reconnect and Settings end the details in fixed slots, so nothing else
  // moves as they come and go.
  const reconnect = camera && onReconnect && needsReconnect(camera);
  const actions = () => (
    <div class="info-actions">
      {reconnect && (
        <button type="button" class="secondary-button" onClick={() => { hideCameras(); onReconnect(reconnect); }}>
          <RefreshIcon />
          Reconnect
        </button>
      )}
      {camera && onCameraSettings && (
        <button type="button" class="secondary-button" onClick={() => { hideCameras(); onCameraSettings(camera.id); }}>
          <SettingsIcon />
          Settings
        </button>
      )}
    </div>
  );

  // Camera shortcuts: [ and ] step through playable cameras, 1–9 pick a row
  // and G switches between Focus and Grid. The player owns its own keys.
  useShortcuts((key) => {
    if (key === "?") setHelpOpen(true);
    else if (key === "g" && cameras.length > 1) showGrid(!grid);
    else if (key === "[" || key === "]") {
      const playable = cameras.filter((item) => item.streams.length);
      const step = key === "]" ? 1 : -1;
      const at = playable.findIndex((item) => item.id === camera?.id);
      const from = at < 0 ? (step > 0 ? -1 : 0) : at;
      chooseCamera(playable[(from + step + playable.length) % playable.length]);
    } else if (/^[1-9]$/.test(key)) chooseCamera(cameras[Number(key) - 1]);
    else return false;
    return true;
  });

  return (
    <div
      class="app"
      data-rail={railOpen ? "open" : "collapsed"}
      {...(noRail ? { "data-norail": "" } : {})}
      {...(grid ? { "data-view": "grid" } : {})}
    >
      {management && <LazyManagement {...management} />}
      <a class="skip-link" href="#viewer">
        Skip to viewer
      </a>
      <header class={cameras.length > 1 ? "topbar has-views" : "topbar"}>
        {!noRail && (
          <button
            type="button"
            class="icon-button rail-toggle"
            aria-label="Camera list"
            aria-controls="camera-rail"
            aria-expanded={railOpen}
            title={railOpen ? "Hide camera names" : "Show camera names"}
            onClick={() => {
              setRailOpen(!railOpen);
              try {
                localStorage.setItem(RAIL_KEY, railOpen ? "collapsed" : "open");
              } catch {}
            }}
          >
            <SidebarIcon />
          </button>
        )}
        <a class="brand" href="#viewer" aria-label="Camstream viewer">
          <img class="brand-mark" src={appIcon} alt="" width={32} height={32} />
          <span class="brand-name">camstream</span>
        </a>
        <span class="topbar-divider" aria-hidden="true" />
        <h1 class="view-title">
          {loading ? (
            <>
              <span class="skeleton title-skeleton" aria-hidden="true" />
              <span class="sr-only">Camera viewer</span>
            </>
          ) : grid ? (
            "All cameras"
          ) : (
            (camera?.name ?? "Camera viewer")
          )}
        </h1>
        {cameras.length > 1 && (
          <div class="view-switch" role="group" aria-label="View">
            <button
              type="button"
              class="icon-button"
              aria-label="One camera"
              aria-pressed={!grid}
              aria-keyshortcuts="g"
              title="One camera (G)"
              onClick={() => showGrid(false)}
            >
              <FocusIcon />
            </button>
            <button
              type="button"
              class="icon-button"
              aria-label="All cameras"
              aria-pressed={grid}
              aria-keyshortcuts="g"
              title="All cameras (G)"
              onClick={() => showGrid(true)}
            >
              <GridIcon />
            </button>
          </div>
        )}
        <button
          type="button"
          class="icon-button shortcuts-button"
          aria-label="Keyboard shortcuts"
          aria-keyshortcuts="?"
          aria-haspopup="dialog"
          title="Keyboard shortcuts (?)"
          onClick={() => setHelpOpen(true)}
        >
          <KeyboardIcon />
        </button>
      </header>
      <main id="viewer" ref={viewerRoot} tabIndex={-1} data-player-root="">
        {error ? (
          <div class="catalog-message" role="alert">
            <CameraIcon />
            <h2>{errorCopy[error.code].title}</h2>
            <p>{errorCopy[error.code].detail}</p>
            {error.retryable && onReload && (
              <button class="primary-button" type="button" onClick={onReload}>
                Try again
              </button>
            )}
            <ErrorDetails code={error.code} where={[]} />
          </div>
        ) : loading ? (
          // The same layout as the player, so nothing jumps when cameras arrive.
          <div class="stage-skeleton" role="status" aria-busy="true">
            <div class="sr-only">
              <h2>Loading cameras</h2>
              <p>Finding the cameras you can watch.</p>
            </div>
          </div>
        ) : !cameras.length ? (
          <div class="catalog-message" role="status">
            <CameraIcon />
            {onAddCamera ? (
              <>
                <h2>Add your first camera</h2>
                <p>
                  Connect a network camera by its RTSP address, or import the
                  files the Camstream Mac app exports.
                </p>
                <div class="catalog-actions">
                  <button type="button" class="secondary-button" onClick={() => onAddCamera("manual")}>Network camera (RTSP)</button>
                  <button type="button" class="secondary-button" onClick={() => onAddCamera("import")}>Camstream Mac app</button>
                </div>
              </>
            ) : (
              <>
                <h2>No cameras yet</h2>
                <p>
                  No cameras are set up for you. Ask the person who runs
                  camstream to add cameras or give you access.
                </p>
              </>
            )}
            {catalogStale && <StaleNotice refresh={refreshCatalog} />}
          </div>
        ) : grid ? (
          <CameraGrid
            cameras={cameras}
            selected={selected}
            previewFor={previewFor}
            onSelect={choose}
          />
        ) : camera && stream ? (
          <Player
            key={JSON.stringify([camera.id, stream.id])}
            cameraName={camera.name}
            stream={stream}
            presentation={presentation}
            reloadHref={linkFor(camera, stream.id)}
            onCameras={showCameras}
            {...(others ? { onChooseCamera: showCameras } : {})}
          />
        ) : (
          <div class="catalog-message" role="status">
            <h2>No supported streams</h2>
            <p>These cameras have no streams this viewer can play.</p>
          </div>
        )}
      </main>
      {!grid && camera && stream ? (
        <section class="info-bar" aria-label="Camera details">
          <div class="info-bar-inner">
            <CameraDetails
              id="stream-select"
              camera={camera}
              stream={stream}
              view={view}
              mine={mine}
              status={status}
              statusAt={statusAt}
              observingSince={observingSince}
              onChange={chooseStream}
            />
            {actions()}
          </div>
        </section>
      ) : loading ? (
        <div class="info-bar" aria-hidden="true">
          <div class="info-bar-inner">
            <span class="skeleton line-skeleton" />
          </div>
        </div>
      ) : null}
      {!noRail && (
        <nav id="camera-rail" class="rail" aria-labelledby="rail-title">
          <div class="rail-heading">
            <h2 id="rail-title">Cameras</h2>
            {!loading && <span class="count">{cameras.length}</span>}
          </div>
          {catalogStale && <StaleNotice refresh={refreshCatalog} titled={!railOpen} />}
          {loading ? (
            <div aria-hidden="true">
              {[0, 1, 2].map((row) => (
                <div key={row} class="row-skeleton">
                  <span class="skeleton" />
                  <span class="skeleton" />
                </div>
              ))}
            </div>
          ) : (
            <CameraLinks
              cameras={cameras}
              selected={selected}
              {...(grid ? {} : { view })}
              onSelect={choose}
              titles={!railOpen}
              actions={menu}
            />
          )}
          {onAddCamera && (
            <button
              type="button"
              class="add-camera"
              title={railOpen ? undefined : "Add Camera"}
              onClick={() => onAddCamera()}
            >
              <span class="add-camera-icon">
                <PlusIcon />
              </span>
              <span class="add-camera-label">Add Camera</span>
            </button>
          )}
        </nav>
      )}
      <dialog
        ref={sheet}
        class="dialog camera-sheet"
        aria-labelledby="sheet-title"
        onClose={() => {
          setSheetOpen(false);
          dialogClosed();
        }}
        onClick={(event) => {
          if (event.target === event.currentTarget) hideCameras();
        }}
      >
        <div class="dialog-inner">
          <div class="dialog-head">
            <h2 id="sheet-title">Cameras</h2>
            <button
              type="button"
              class="icon-button"
              aria-label="Close"
              onClick={hideCameras}
            >
              <CloseIcon />
            </button>
          </div>
          {/* Where the page shows only the player, the stream choice and
              its details live here too. */}
          {sheetOpen && camera && stream && (
            <div class="sheet-stream">
              <CameraDetails
                id="sheet-stream-select"
                camera={camera}
                stream={stream}
                view={view}
                mine={mine}
                status={status}
                statusAt={statusAt}
                observingSince={observingSince}
                onChange={chooseStream}
              />
              {actions()}
            </div>
          )}
          {sheetOpen && catalogStale && <StaleNotice refresh={refreshCatalog} />}
          {sheetOpen && (
            <CameraLinks
              cameras={cameras}
              selected={selected}
              view={view}
              onSelect={choose}
            />
          )}
        </div>
      </dialog>
      {helpOpen && <LazyShortcuts onClose={() => { setHelpOpen(false); dialogClosed(); }} />}
      <p class="toast" role="status">{notice}</p>
    </div>
  );
}

// A failed refresh keeps the last list. The collapsed rail shows only the
// refresh icon, so its tooltip names the problem.
function StaleNotice({
  refresh,
  titled = false,
}: {
  refresh: (() => Promise<unknown>) | undefined;
  titled?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <div class="rail-notice">
      <p role="status">Camera list may be out of date.</p>
      {refresh && (
        <button
          type="button"
          class="secondary-button"
          title={titled ? "Camera list may be out of date. Refresh" : undefined}
          disabled={busy}
          onClick={() => {
            setBusy(true);
            void refresh()
              .catch(() => {})
              .finally(() => setBusy(false));
          }}
        >
          <RefreshIcon />
          <span>{busy ? "Refreshing…" : "Refresh"}</span>
        </button>
      )}
    </div>
  );
}

// The camera on screen: its name and status, its viewers once the server
// reports them, how fresh that is, and the stream choice when there is one.
function CameraDetails({
  id,
  camera,
  stream,
  view,
  mine,
  status,
  statusAt,
  observingSince,
  onChange,
}: {
  id: string;
  camera: CameraSummary;
  stream: StreamSummary;
  view: ViewState;
  mine: boolean;
  status: StreamStatus | undefined;
  statusAt: number | undefined;
  observingSince: number | undefined;
  onChange: (stream_id: string) => void;
}) {
  // A count observed before this page connected still includes it.
  const viewers =
    status?.viewers == null
      ? undefined
      : mine
        ? Math.max(status.viewers, 1)
        : status.viewers;
  return (
    <>
      <div class="camera-facts">
        <strong>{camera.name}</strong>
        {view.label !== stateLabels.unknown && <StatusBadge view={view} />}
        {viewers !== undefined && (
          <span>
            {!viewers
              ? "No one watching"
              : mine && viewers === 1
                ? "Only you watching"
                : `${viewers} watching`}
          </span>
        )}
        <Freshness at={statusAt} since={observingSince} />
      </div>
      {camera.streams.length > 1 && (
        <div class="stream-select">
          <label for={id}>Stream</label>
          <select
            id={id}
            value={stream.id}
            onChange={(event) => onChange(event.currentTarget.value)}
          >
            {camera.streams.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </select>
        </div>
      )}
    </>
  );
}

// How current the status is, timed on this device: server observation times
// may be skewed against its clock. It warns only once the page has asked for a
// whole window without an update, so a page shown again does not warn while
// its first new request runs.
function Freshness({
  at,
  since,
}: {
  at: number | undefined;
  since: number | undefined;
}) {
  useTick(at !== undefined || since !== undefined);
  const now = performance.now();
  const age = at === undefined ? undefined : now - at;
  const quiet = since === undefined ? 0 : now - Math.max(at ?? since, since);
  if (quiet >= STALE_MS)
    return <span class="stale">No update for {duration(age ?? quiet)}</span>;
  if (age === undefined) return null;
  return <span>Updated {age < 3000 ? "just now" : `${duration(age)} ago`}</span>;
}

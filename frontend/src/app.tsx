import type { ComponentChildren } from "preact";
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
import type { ManagementDialogProps } from "./components/camera-management";
import { CameraLinks } from "./components/camera-selector";
import {
  CameraIcon,
  CloseIcon,
  KeyboardIcon,
  SidebarIcon,
} from "./components/icon";
import { Player, type PlayerPresentation } from "./components/player";
import {
  describeView,
  duration,
  ErrorDetails,
  useTick,
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
  onAddCamera?: (() => void) | undefined;
  watchSelection?: StreamRef | undefined;
  recoveryControl?: ComponentChildren;
  management?: ManagementDialogProps | undefined;
  catalogStale?: boolean | undefined;
  onSelect?: (selection: StreamRef | undefined) => void;
}
/** Status updates that stop for this long, while the page asks, warn. */
const STALE_MS = 10000;
const sameStream = (a: StreamRef, b: StreamRef) =>
  a.camera_id === b.camera_id && a.stream_id === b.stream_id;
// Below 1024 px the rail starts as icons, leaving the width to the video.
const narrow = () =>
  typeof matchMedia === "function" && matchMedia("(max-width: 1023px)").matches;


export function App({
  catalog,
  loading = false,
  error,
  viewer,
  initialSelection,
  onSelect,
  onReload,
  onAddCamera, watchSelection, recoveryControl, management, catalogStale,
}: AppProps) {
  const [requested, setRequested] = useState(initialSelection);
  const [railOpen, setRailOpen] = useState(() => !narrow());
  const [sheetOpen, setSheetOpen] = useState(false);
  const sheet = useRef<HTMLDialogElement>(null);
  const [helpOpen, setHelpOpen] = useState(false);
  const viewerRoot = useRef<HTMLElement>(null);
  // Resolve against the current filtered catalog on every render. Never retain a removed scope.
  const cameras = error || loading ? [] : catalog.cameras;
  const selected = resolveSelection(cameras, requested);
  const camera = cameras.find((item) => item.id === selected?.camera_id);
  const stream = camera?.streams.find(
    (item) => item.id === selected?.stream_id,
  );
  const current =
    selected && viewer && sameStream(selected, viewer.selection)
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

  // Notify the integration owner of the effective selection, including initial
  // selection and catalog revocation. This callback performs no transport work here.
  useEffect(() => {
    onSelect?.(selected);
  }, [selected?.camera_id, selected?.stream_id]);
  useEffect(() => {
    if (camera) showPageState(pageTitle(camera.name, word), view.tone);
    else showPageState(pageTitle(), error ? "danger" : null);
  }, [camera?.name, word, view.tone, Boolean(error)]);

  useEffect(() => { if (watchSelection) setRequested(watchSelection); }, [watchSelection]);

  function select(next: StreamRef) {
    if (selected && sameStream(selected, next)) return;
    setRequested(next);
  }
  // A row names a camera; choosing the one on screen keeps its stream.
  function choose(next: StreamRef) {
    const root = viewerRoot.current;
    // Switching replaces the player. Focus on one of its controls, or on the
    // sheet that its Cameras button opened, would fall back to the page.
    const keepFocus =
      sheetOpen || Boolean(root?.contains(document.activeElement));
    hideCameras();
    if (next.camera_id === selected?.camera_id) return;
    select(next);
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

  // Camera shortcuts: [ and ] step through playable cameras and 1–9 pick a
  // row. The player owns its own keys.
  useShortcuts((key) => {
    if (key === "?") setHelpOpen(true);
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
    >
      {management && <LazyManagement {...management} />}
      <a class="skip-link" href="#viewer">
        Skip to viewer
      </a>
      <header class="topbar">
        {!noRail && (
          <button
            type="button"
            class="icon-button rail-toggle"
            aria-label="Camera list"
            aria-controls="camera-rail"
            aria-expanded={railOpen}
            title={railOpen ? "Hide camera names" : "Show camera names"}
            onClick={() => setRailOpen(!railOpen)}
          >
            <SidebarIcon />
          </button>
        )}
        <a class="brand" href="#viewer" aria-label="Camstream viewer">
          <span class="brand-mark">
            <CameraIcon />
          </span>
          <span class="brand-name">camstream</span>
        </a>
        <span class="topbar-divider" aria-hidden="true" />
        <h1 class="view-title">
          {loading ? (
            <>
              <span class="skeleton title-skeleton" aria-hidden="true" />
              <span class="sr-only">Camera viewer</span>
            </>
          ) : (
            (camera?.name ?? "Camera viewer")
          )}
        </h1>
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
      {catalogStale && <p class="catalog-stale" role="status">Camera list may be out of date. Current playback is preserved.</p>}
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
            <h2>No cameras yet</h2>
            <p>
              No cameras are set up for you. Ask the person who runs camstream
              to add cameras or give you access.
            </p>
            {onAddCamera && <button type="button" class="primary-button" onClick={onAddCamera}>Add Camera</button>}
          </div>
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
      {camera && stream ? (
        <section class="stream-bar" aria-label="Stream details">
          <div class="stream-bar-inner">
            {recoveryControl}
            <StreamDetails
              id="stream-select"
              camera={camera}
              stream={stream}
              status={status}
              statusAt={statusAt}
              observingSince={observingSince}
              onChange={chooseStream}
            />
          </div>
        </section>
      ) : loading ? (
        <div class="stream-bar" aria-hidden="true">
          <div class="stream-bar-inner">
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
              view={view}
              onSelect={choose}
              titles={!railOpen}
            />
          )}
          {onAddCamera && <button type="button" class="add-camera" onClick={onAddCamera} aria-label="Add Camera">+ Add Camera</button>}
          <p class="rail-note">Switching cameras changes only this page.</p>
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
              <StreamDetails
                id="sheet-stream-select"
                camera={camera}
                stream={stream}
                status={status}
                statusAt={statusAt}
                observingSince={observingSince}
                onChange={chooseStream}
              />
            </div>
          )}
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
    </div>
  );
}

function StreamDetails({
  id,
  camera,
  stream,
  status,
  statusAt,
  observingSince,
  onChange,
}: {
  id: string;
  camera: CameraSummary;
  stream: StreamSummary;
  status: StreamStatus | undefined;
  statusAt: number | undefined;
  observingSince: number | undefined;
  onChange: (stream_id: string) => void;
}) {
  const { audio, video } = stream.capabilities;
  return (
    <>
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
      <dl class="stream-facts">
        <div>
          <dt>Media</dt>
          <dd>
            {video
              ? audio
                ? "Video + audio"
                : "Video only"
              : audio
                ? "Audio only"
                : "No media"}
          </dd>
        </div>
        <div>
          <dt>Viewers</dt>
          <dd>{status?.viewers == null ? "Unknown" : status.viewers}</dd>
        </div>
        <div>
          <dt>Status</dt>
          <dd>
            <Freshness at={statusAt} since={observingSince} />
          </dd>
        </div>
      </dl>
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
  if (age === undefined) return <>No update yet</>;
  return <>Updated {age < 3000 ? "just now" : `${duration(age)} ago`}</>;
}

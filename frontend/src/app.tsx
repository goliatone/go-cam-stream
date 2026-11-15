import { useEffect, useState } from "preact/hooks";
import type {
  CameraList,
  ErrorBody,
  StreamRef,
  StreamStatus,
} from "./services/contracts";
import { CameraSelector } from "./components/camera-selector";
import { CameraIcon } from "./components/icon";
import { Player, type PlayerPresentation } from "./components/player";
import { describeView, errorCopy, stateLabels } from "./components/status";
import { linkFor, resolveSelection } from "./services/links";
import { pageTitle, showPageState } from "./services/page-meta";

export interface ViewerInput {
  selection: StreamRef;
  status?: StreamStatus;
  presentation: PlayerPresentation;
}
export interface AppProps {
  catalog: CameraList;
  loading?: boolean;
  error?: ErrorBody;
  viewer?: ViewerInput;
  /** Selection requested by the page address; resolved like any other request. */
  initialSelection?: Partial<StreamRef>;
  onReload?: () => void;
  onSelect?: (selection: StreamRef | undefined) => void;
}
const sameStream = (a: StreamRef, b: StreamRef) =>
  a.camera_id === b.camera_id && a.stream_id === b.stream_id;

export function App({
  catalog,
  loading = false,
  error,
  viewer,
  initialSelection,
  onSelect,
  onReload,
}: AppProps) {
  const [requested, setRequested] = useState(initialSelection);
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
  const presentation = current?.presentation ?? { state: "unknown" as const };
  const view = describeView(presentation);
  // "Status unknown" adds nothing to a tab title.
  const word = view.label === stateLabels.unknown ? undefined : view.label;

  // Notify the integration owner of the effective selection, including initial
  // selection and catalog revocation. This callback performs no transport work here.
  useEffect(() => {
    onSelect?.(selected);
  }, [selected?.camera_id, selected?.stream_id]);
  useEffect(() => {
    if (camera) showPageState(pageTitle(camera.name, word), view.tone);
    else showPageState(pageTitle(), error ? "danger" : null);
  }, [camera?.name, word, view.tone, Boolean(error)]);

  function select(next: StreamRef) {
    if (selected && sameStream(selected, next)) return;
    setRequested(next);
  }

  return (
    <div class="app-shell">
      <a class="skip-link" href="#viewer">
        Skip to viewer
      </a>
      <header class="site-header">
        <a class="brand" href="#viewer" aria-label="Camstream viewer">
          <span class="brand-mark">
            <CameraIcon />
          </span>
          camstream
          <span class="brand-divider" />{" "}
          <span class="brand-caption">Camera viewer</span>
        </a>
        <span class="header-note">A closer look, wherever you are.</span>
      </header>
      <main id="viewer" tabIndex={-1}>
        <div class="page-heading">
          <div>
            <p class="eyebrow">YOUR SPACE, IN VIEW</p>
            <h1>Camera viewer</h1>
            <p class="page-description">
              Choose a camera. Keep what matters in view.
            </p>
          </div>
          <span class="view-mode">Single view</span>
        </div>
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
          </div>
        ) : loading ? (
          <div class="catalog-message" role="status">
            <CameraIcon />
            <h2>Loading cameras</h2>
            <p>Finding the cameras you can watch.</p>
          </div>
        ) : !cameras.length ? (
          <div class="catalog-message" role="status">
            <CameraIcon />
            <h2>No cameras yet</h2>
            <p>
              No cameras are set up for you. Ask the person who runs camstream
              to add cameras or give you access.
            </p>
          </div>
        ) : (
          <div class="workspace">
            <CameraSelector
              cameras={cameras}
              selected={selected}
              onSelect={select}
            />
            <div class="view-panel">
              {camera && stream && selected ? (
                <>
                  <div class="view-heading">
                    <div>
                      <p class="eyebrow">SELECTED CAMERA</p>
                      <h2>{camera.name}</h2>
                    </div>
                    <div class="stream-select">
                      <label for="stream-select">Stream</label>
                      <select
                        id="stream-select"
                        value={stream.id}
                        onChange={(event) =>
                          select({
                            camera_id: camera.id,
                            stream_id: event.currentTarget.value,
                          })
                        }
                      >
                        {camera.streams.map((item) => (
                          <option key={item.id} value={item.id}>
                            {item.name}
                          </option>
                        ))}
                      </select>
                    </div>
                  </div>
                  <Player
                    key={JSON.stringify([camera.id, stream.id])}
                    stream={stream}
                    presentation={presentation}
                    reloadHref={linkFor(camera, stream.id)}
                  />
                  <div class="stream-details" aria-label="Stream details">
                    <div>
                      <span class="detail-label">MEDIA</span>
                      <strong>
                        {stream.capabilities.video
                          ? stream.capabilities.audio
                            ? "Video + audio"
                            : "Video only"
                          : stream.capabilities.audio
                            ? "Audio only"
                            : "No media"}
                      </strong>
                    </div>
                    <div>
                      <span class="detail-label">VIEWERS</span>
                      <strong>
                        {status?.viewers == null ? "Unknown" : status.viewers}
                      </strong>
                    </div>
                    <div>
                      <span class="detail-label">LAST UPDATE</span>
                      <strong>{formatObserved(status?.observed_at)}</strong>
                    </div>
                  </div>
                </>
              ) : (
                <div class="catalog-message" role="status">
                  <h2>No supported streams</h2>
                  <p>These cameras have no streams this viewer can play.</p>
                </div>
              )}
            </div>
          </div>
        )}
        <footer class="page-footer">
          <span>camstream</span>
          <p>Switching views affects only this page.</p>
        </footer>
      </main>
    </div>
  );
}

function formatObserved(value: string | null | undefined) {
  if (!value) return "No update yet";
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "No update yet"
    : date.toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      });
}

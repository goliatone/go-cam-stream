import type { CameraSummary, StreamRef } from "../services/contracts";
import { stateLabels } from "../services/copy";
import { linkFor } from "../services/links";
import { CameraIcon } from "./icon";
import { describeView, StatusBadge, type ViewState } from "./status";

// Rows are real links to each camera's view. A plain click switches in place;
// modified and middle clicks keep the browser's behavior and open a new tab,
// which starts on that camera without first connecting to another.
const opensElsewhere = (event: MouseEvent) =>
  event.button !== 0 ||
  event.metaKey ||
  event.ctrlKey ||
  event.shiftKey ||
  event.altKey;

export function CameraLinks({
  cameras,
  selected,
  view,
  onSelect,
  titles = false,
}: {
  cameras: readonly CameraSummary[];
  selected: StreamRef | undefined;
  /** This page's player state, shown on the selected row. */
  view?: ViewState;
  onSelect: (selection: StreamRef) => void;
  /** Name each row and its status in a tooltip, for the collapsed rail that hides both. */
  titles?: boolean;
}) {
  return (
    <ul class="camera-list">
      {cameras.map((camera) => {
        const first = camera.streams[0];
        const current = selected?.camera_id === camera.id;
        const own = current && view;
        // The selected row shows this page's player state, never "Status
        // unknown". Other rows show the server's availability only when it says
        // something: live, starting or failed. Idle is not shown, because it
        // does not establish that the camera can play. A failed camera is dimmed.
        const badge = own || describeView({ state: camera.availability });
        const shown = own
          ? badge.label !== stateLabels.unknown
          : badge.tone !== "idle";
        const content = (
          <>
            <span class="camera-icon">
              <CameraIcon />
            </span>
            <span class="camera-label">
              <strong>{camera.name}</strong>
              {!first ? (
                <span class="small">No supported streams</span>
              ) : (
                shown && <StatusBadge view={badge} />
              )}
            </span>
          </>
        );
        return (
          <li key={camera.id}>
            {first ? (
              <a
                class={
                  !own && badge.tone === "danger"
                    ? "camera-choice dim"
                    : "camera-choice"
                }
                href={linkFor(camera)}
                {...(current ? { "aria-current": "page" as const } : {})}
                {...(titles
                  ? {
                      title: shown
                        ? `${camera.name} · ${badge.label}`
                        : camera.name,
                    }
                  : {})}
                onClick={(event) => {
                  if (opensElsewhere(event)) return;
                  event.preventDefault();
                  onSelect({ camera_id: camera.id, stream_id: first.id });
                }}
              >
                {content}
              </a>
            ) : (
              <div class="camera-choice" aria-disabled="true">
                {content}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

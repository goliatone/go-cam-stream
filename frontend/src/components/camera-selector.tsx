import type { CameraSummary, StreamRef } from "../services/contracts";
import { linkFor } from "../services/links";
import { CameraIcon } from "./icon";
import {
  describeView,
  StatusBadge,
  stateLabels,
  type ViewState,
} from "./status";

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
  /** Name each row in a tooltip, for the collapsed rail that hides names. */
  titles?: boolean;
}) {
  return (
    <ul class="camera-list">
      {cameras.map((camera) => {
        const first = camera.streams[0];
        const current = selected?.camera_id === camera.id;
        // Real status or none: the catalog reports availability only as a
        // snapshot, often unknown, and no row should say "Status unknown".
        const badge =
          current && view ? view : describeView({ state: camera.availability });
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
                badge.label !== stateLabels.unknown && (
                  <StatusBadge view={badge} />
                )
              )}
            </span>
          </>
        );
        return (
          <li key={camera.id}>
            {first ? (
              <a
                class="camera-choice"
                href={linkFor(camera)}
                {...(current ? { "aria-current": "page" as const } : {})}
                {...(titles ? { title: camera.name } : {})}
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

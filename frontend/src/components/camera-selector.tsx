import type { CameraSummary, StreamRef } from "../services/contracts";
import { linkFor } from "../services/links";
import { CameraIcon } from "./icon";
import { describeView, StatusBadge } from "./status";

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
  onSelect,
  titles = false,
}: {
  cameras: readonly CameraSummary[];
  selected: StreamRef | undefined;
  onSelect: (selection: StreamRef) => void;
  /** Name each row in a tooltip, for the collapsed rail that hides names. */
  titles?: boolean;
}) {
  return (
    <ul class="camera-list">
      {cameras.map((camera) => {
        const first = camera.streams[0];
        const current = selected?.camera_id === camera.id;
        const content = (
          <>
            <span class="camera-icon">
              <CameraIcon />
            </span>
            <span class="camera-label">
              <strong>{camera.name}</strong>
              {first ? (
                <StatusBadge
                  view={describeView({ state: camera.availability })}
                />
              ) : (
                <span class="small">No supported streams</span>
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

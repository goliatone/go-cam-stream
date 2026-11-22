import type { CameraSummary, StreamRef } from "../services/contracts";
import { CameraChoice, cameraStatus } from "./camera-selector";
import { StatusBadge } from "./status";
import { TilePreview, type PreviewSource } from "./tile-preview";

/**
 * Every camera as a tile: its name and status, as the rail shows them, over its
 * picture. Each tile links to the camera's view, like its row in the rail. The
 * page plays nothing here, so every tile shows the server's availability.
 */
export function CameraGrid({
  cameras,
  selected,
  previewFor,
  onSelect,
}: {
  cameras: readonly CameraSummary[];
  selected: StreamRef | undefined;
  previewFor: (camera: string) => PreviewSource;
  onSelect: (selection: StreamRef) => void;
}) {
  return (
    <ul class="camera-grid" aria-label="All cameras">
      {cameras.map((camera, i) => {
        const { badge, shown, dim } = cameraStatus(camera);
        return (
          <li key={camera.id}>
            {/* The name comes first, so it starts the link's name; the
                picture is drawn above it. */}
            <CameraChoice
              camera={camera}
              index={i}
              current={selected?.camera_id === camera.id}
              dim={dim}
              class="grid-tile"
              onSelect={onSelect}
            >
              <span class="camera-label">
                <strong>{camera.name}</strong>
                {!camera.streams.length ? (
                  <span class="small">No supported streams</span>
                ) : (
                  shown && <StatusBadge view={badge} />
                )}
              </span>
              <TilePreview source={previewFor(camera.id)} tile={i % 8} />
            </CameraChoice>
          </li>
        );
      })}
    </ul>
  );
}

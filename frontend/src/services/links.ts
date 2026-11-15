import type { CameraSummary, StreamRef } from "./contracts";

// A view's address is `?camera=<id>&stream=<id>`. IDs are only requests: the
// page resolves them against the filtered catalog, so unknown and revoked IDs
// fall back alike and a link reveals nothing about cameras outside it.
export function parseLink(search: string): Partial<StreamRef> {
  const query = new URLSearchParams(search);
  const camera = query.get("camera");
  const stream = query.get("stream");
  return {
    ...(camera ? { camera_id: camera } : {}),
    ...(stream ? { stream_id: stream } : {}),
  };
}

/** Relative link to a view. The stream is omitted when it is the camera's first. */
export function linkFor(camera: CameraSummary, streamId?: string) {
  const query = new URLSearchParams({ camera: camera.id });
  if (streamId && camera.streams[0]?.id !== streamId)
    query.set("stream", streamId);
  return `?${query.toString()}`;
}

/** The requested camera when it has streams, else the first that does. */
export function resolveSelection(
  cameras: readonly CameraSummary[],
  requested: Partial<StreamRef> | undefined,
): StreamRef | undefined {
  const camera =
    cameras.find(
      (item) => item.id === requested?.camera_id && item.streams.length > 0,
    ) ?? cameras.find((item) => item.streams.length > 0);
  const stream =
    (camera?.id === requested?.camera_id
      ? camera?.streams.find((item) => item.id === requested?.stream_id)
      : undefined) ?? camera?.streams[0];
  return camera && stream
    ? { camera_id: camera.id, stream_id: stream.id }
    : undefined;
}

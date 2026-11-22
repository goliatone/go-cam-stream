import { CameraIcon } from "./icon";
import { duration, useTick } from "./status";

/**
 * What a camera's tile shows. Live video or a server snapshot would be new
 * kinds, drawn here, with no change to the tile or the grid.
 */
export type PreviewSource =
  | { kind: "none" }
  /** The last frame this page played of the camera, kept at `at` (performance.now()). */
  | { kind: "frame"; url: string; at: number };

/**
 * A tile's picture: the camera's last frame and how long ago it was seen, or
 * its colour tile. A kind this page does not know shows the colour tile.
 */
export function TilePreview({ source, tile }: { source: PreviewSource; tile: number }) {
  return (
    <span class={`tile-preview tile-${tile}`}>
      {source.kind === "frame" ? <Frame {...source} /> : <CameraIcon />}
    </span>
  );
}

function Frame({ url, at }: { url: string; at: number }) {
  useTick(true);
  const age = performance.now() - at;
  return (
    <>
      <img src={url} alt="" />
      <span class="tile-age">Seen {age < 3000 ? "just now" : `${duration(age)} ago`}</span>
    </>
  );
}

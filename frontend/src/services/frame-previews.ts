import type { PreviewSource } from "../components/tile-preview";

/** Frames are scaled down to at most this width before they are kept. */
export const FRAME_WIDTH = 480;

/**
 * The last frame this page played of each camera, as a scaled JPEG held in
 * memory. Frames are never stored or sent. Each is a `data:` URL, which the
 * viewer's content security policy allows for images, unlike `blob:`.
 */
export class FramePreviews {
  #frames = new Map<string, { url: string; at: number }>();
  #listeners = new Set<() => void>();

  previewFor(camera: string): PreviewSource {
    const frame = this.#frames.get(camera);
    return frame ? { kind: "frame", ...frame } : { kind: "none" };
  }

  /** Calls `listener` whenever a frame is kept or dropped; returns the unsubscribe. */
  subscribe(listener: () => void) {
    this.#listeners.add(listener);
    return () => void this.#listeners.delete(listener);
  }

  /**
   * Keeps the video's current picture as the camera's frame. A video without a
   * picture, or one the browser will not draw or encode, keeps the previous frame.
   */
  capture(camera: string, video: HTMLVideoElement) {
    const { videoWidth: width, videoHeight: height } = video;
    if (video.readyState < 2 || !width || !height) return;
    const scale = Math.min(1, FRAME_WIDTH / width);
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(width * scale);
    canvas.height = Math.round(height * scale);
    let url: string;
    try {
      canvas.getContext("2d")!.drawImage(video, 0, 0, canvas.width, canvas.height);
      url = canvas.toDataURL("image/jpeg", 0.7);
    } catch {
      // No picture to draw, or a canvas the browser considers tainted.
      return;
    }
    // A canvas that cannot encode an image gives "data:,".
    if (!url.startsWith("data:image/")) return;
    this.#frames.set(camera, { url, at: performance.now() });
    this.#changed();
  }

  /** Drops the frames of cameras missing from `cameras`, such as a removed one. */
  retain(cameras: readonly string[]) {
    const kept = new Set(cameras);
    let changed = false;
    for (const camera of this.#frames.keys())
      if (!kept.has(camera)) changed = this.#frames.delete(camera) || changed;
    if (changed) this.#changed();
  }

  /** Drops every frame, as the page unloads. */
  clear() {
    this.retain([]);
  }

  #changed() {
    for (const listener of this.#listeners) listener();
  }
}

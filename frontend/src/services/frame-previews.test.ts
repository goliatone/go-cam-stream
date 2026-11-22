import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FRAME_WIDTH, FramePreviews } from "./frame-previews";

// jsdom draws and encodes nothing.
type Encode = (canvas: HTMLCanvasElement, type?: string, quality?: unknown) => string;
let encode: Encode;
const drawImage = vi.fn();
let made = 0;
beforeEach(() => {
  made = 0;
  drawImage.mockReset();
  encode = () => `data:image/jpeg;base64,frame-${++made}`;
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
    drawImage,
  } as unknown as CanvasRenderingContext2D);
  vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockImplementation(function (
    this: HTMLCanvasElement,
    type,
    quality,
  ) {
    return encode(this, type, quality);
  });
});
afterEach(() => {
  vi.restoreAllMocks();
});

/** A video showing a picture of this size, or none before it has data. */
function video(width = 1280, height = 720, readyState = 4) {
  const node = document.createElement("video");
  Object.defineProperties(node, {
    videoWidth: { value: width },
    videoHeight: { value: height },
    readyState: { value: readyState },
  });
  return node;
}

describe("frame previews", () => {
  it("keeps a JPEG of the picture, scaled down to the frame width", () => {
    const frames = new FramePreviews();
    const changed = vi.fn();
    frames.subscribe(changed);
    let encoded: { width: number; height: number; type: string | undefined; quality: unknown } | undefined;
    const base = encode;
    encode = (canvas, type, quality) => {
      encoded = { width: canvas.width, height: canvas.height, type, quality };
      return base(canvas, type, quality);
    };
    const source = video(1920, 1080);
    const before = performance.now();
    frames.capture("room1", source);
    expect(drawImage).toHaveBeenCalledWith(source, 0, 0, FRAME_WIDTH, 270);
    expect(encoded).toEqual({ width: 480, height: 270, type: "image/jpeg", quality: 0.7 });
    const preview = frames.previewFor("room1");
    expect(preview).toEqual({
      kind: "frame",
      url: "data:image/jpeg;base64,frame-1",
      at: expect.any(Number),
    });
    expect(preview.kind === "frame" && preview.at).toBeGreaterThanOrEqual(before);
    expect(changed).toHaveBeenCalledOnce();
    // Other cameras have no frame, and a smaller picture keeps its size.
    expect(frames.previewFor("room2")).toEqual({ kind: "none" });
    frames.capture("room2", video(320, 240));
    expect(drawImage).toHaveBeenLastCalledWith(expect.anything(), 0, 0, 320, 240);
  });

  it("replaces a camera's frame with the newer one", () => {
    const frames = new FramePreviews();
    frames.capture("room1", video());
    frames.capture("room1", video());
    expect(frames.previewFor("room1")).toMatchObject({ url: "data:image/jpeg;base64,frame-2" });
  });

  it.each([
    ["a video without a picture yet", () => video(1280, 720, 1)],
    ["a video without a size", () => video(0, 0)],
    ["a draw that fails", () => {
      drawImage.mockImplementation(() => {
        throw new DOMException("no frame", "InvalidStateError");
      });
      return video();
    }],
    ["a canvas the browser will not encode", () => {
      encode = () => {
        throw new DOMException("tainted", "SecurityError");
      };
      return video();
    }],
    ["an encoding that produces no image", () => {
      encode = () => "data:,";
      return video();
    }],
  ])("keeps the previous frame after %s", (_, failing) => {
    const frames = new FramePreviews();
    frames.capture("room1", video());
    const changed = vi.fn();
    frames.subscribe(changed);
    frames.capture("room1", failing());
    expect(frames.previewFor("room1")).toMatchObject({
      kind: "frame",
      url: "data:image/jpeg;base64,frame-1",
    });
    expect(changed).not.toHaveBeenCalled();
  });

  it("drops the frames of cameras that leave the catalog, and every frame on clear", () => {
    const frames = new FramePreviews();
    frames.capture("room1", video());
    frames.capture("room2", video());
    const changed = vi.fn();
    const stop = frames.subscribe(changed);
    frames.retain(["room2", "room3"]);
    expect(frames.previewFor("room1")).toEqual({ kind: "none" });
    expect(frames.previewFor("room2")).toMatchObject({ url: "data:image/jpeg;base64,frame-2" });
    expect(changed).toHaveBeenCalledOnce();
    // Nothing to drop tells no one.
    frames.retain(["room2"]);
    expect(changed).toHaveBeenCalledOnce();
    frames.clear();
    expect(frames.previewFor("room2")).toEqual({ kind: "none" });
    expect(changed).toHaveBeenCalledTimes(2);
    // An unsubscribed listener hears nothing more.
    stop();
    frames.capture("room1", video());
    expect(changed).toHaveBeenCalledTimes(2);
  });
});

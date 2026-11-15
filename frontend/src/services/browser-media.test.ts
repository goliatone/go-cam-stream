import { afterEach, describe, expect, it, vi } from "vitest";
import {
  detachVideo,
  fullscreen,
  mediaControls,
  pictureInPicture,
} from "./browser-media";

// jsdom omits browser media extensions. Own-property descriptors are restored
// after each test, so these cases test API selection, not browser support.
const restore: (() => void)[] = [];
function define(target: object, values: Record<string, unknown>) {
  for (const [key, value] of Object.entries(values)) {
    const original = Object.getOwnPropertyDescriptor(target, key);
    Object.defineProperty(target, key, { configurable: true, value });
    restore.push(() =>
      original
        ? Object.defineProperty(target, key, original)
        : Reflect.deleteProperty(target, key),
    );
  }
}
afterEach(() => {
  restore.reverse().forEach((fn) => fn());
  restore.length = 0;
  vi.restoreAllMocks();
});

describe("browser media capabilities", () => {
  it("keeps unsupported controls explained and PiP unavailable before media", () => {
    const video = document.createElement("video");
    define(document, {
      fullscreenEnabled: false,
      pictureInPictureEnabled: false,
    });
    expect(mediaControls(video).fullscreen.enabled).toBe(false);
    expect(mediaControls(video).pip).toMatchObject({
      enabled: false,
      reason: expect.stringContaining("after video"),
    });
  });
  it("uses standard presentation APIs and propagates denied gestures", async () => {
    const video = document.createElement("video");
    const enter = vi.fn().mockRejectedValue(new TypeError("not granted"));
    const pip = vi.fn().mockResolvedValue({});
    define(document, {
      fullscreenEnabled: true,
      fullscreenElement: null,
      pictureInPictureEnabled: true,
      pictureInPictureElement: null,
    });
    define(video, {
      readyState: 2,
      requestFullscreen: enter,
      requestPictureInPicture: pip,
    });
    expect(mediaControls(video).pip.enabled).toBe(true);
    await expect(fullscreen(video)).rejects.toThrow("not granted");
    await pictureInPicture(video);
    expect(enter).toHaveBeenCalledOnce();
    expect(pip).toHaveBeenCalledOnce();
  });
  it("honors disabled standard PiP and checks Safari support per mode", async () => {
    const video = document.createElement("video");
    const standard = vi.fn();
    const safari = vi.fn();
    define(document, {
      pictureInPictureEnabled: true,
      pictureInPictureElement: null,
    });
    define(video, {
      readyState: 2,
      disablePictureInPicture: true,
      requestPictureInPicture: standard,
      webkitSupportsPresentationMode: (mode: string) =>
        mode === "picture-in-picture",
      webkitSetPresentationMode: safari,
      webkitPresentationMode: "inline",
    });
    expect(mediaControls(video).pip.enabled).toBe(true);
    await pictureInPicture(video);
    expect(standard).not.toHaveBeenCalled();
    expect(safari).toHaveBeenCalledWith("picture-in-picture");
  });
  it("uses Safari fullscreen when the standard API is unavailable", async () => {
    const video = document.createElement("video");
    const enter = vi.fn();
    const exit = vi.fn();
    define(document, { fullscreenEnabled: false, fullscreenElement: null });
    define(video, { webkitEnterFullscreen: enter, webkitExitFullscreen: exit });
    // iPhone Safari can only show the video itself, once metadata exists.
    expect(mediaControls(video).fullscreen).toMatchObject({
      enabled: false,
      reason: expect.stringContaining("after video"),
    });
    define(video, { readyState: 1 });
    expect(mediaControls(video).fullscreen.enabled).toBe(true);
    await fullscreen(video, document.createElement("section"));
    define(video, { webkitDisplayingFullscreen: true });
    await fullscreen(video);
    expect(enter).toHaveBeenCalledOnce();
    expect(exit).toHaveBeenCalledOnce();
  });
  it("shows the player container fullscreen, prefixed where needed", async () => {
    const video = document.createElement("video");
    const container = document.createElement("section");
    const standard = vi.fn().mockResolvedValue(undefined);
    const bare = vi.fn();
    define(document, { fullscreenEnabled: true, fullscreenElement: null });
    define(container, { requestFullscreen: standard });
    define(video, { requestFullscreen: bare });
    expect(mediaControls(video).fullscreen.enabled).toBe(true);
    await fullscreen(video, container);
    expect(standard).toHaveBeenCalledOnce();
    expect(bare).not.toHaveBeenCalled();

    const prefixed = vi.fn();
    const exit = vi.fn();
    define(document, { fullscreenEnabled: false, webkitFullscreenEnabled: true });
    define(container, { webkitRequestFullscreen: prefixed });
    expect(mediaControls(video).fullscreen.enabled).toBe(true);
    await fullscreen(video, container);
    expect(prefixed).toHaveBeenCalledOnce();
    define(document, {
      webkitFullscreenElement: container,
      webkitExitFullscreen: exit,
      exitFullscreen: undefined,
    });
    await fullscreen(video, container);
    expect(exit).toHaveBeenCalledOnce();
    expect(prefixed).toHaveBeenCalledOnce();
  });
  it("detaches immediately even when browser presentation exit rejects", async () => {
    const video = document.createElement("video");
    const pause = vi.fn();
    define(video, { pause });
    video.srcObject = {} as MediaStream;
    define(document, {
      pictureInPictureElement: video,
      exitPictureInPicture: vi.fn().mockRejectedValue(new Error("denied")),
    });
    const done = detachVideo(video);
    expect(pause).toHaveBeenCalledOnce();
    expect(video.srcObject).toBeNull();
    await done;
  });
  it("fails unsupported actions explicitly", async () => {
    const video = document.createElement("video");
    define(document, {
      fullscreenEnabled: false,
      fullscreenElement: null,
      pictureInPictureEnabled: false,
      pictureInPictureElement: null,
    });
    await expect(fullscreen(video)).rejects.toMatchObject({
      body: { code: "unsupported_capability", retryable: false },
    });
    await expect(pictureInPicture(video)).rejects.toMatchObject({
      body: { code: "unsupported_capability", retryable: false },
    });
  });
});

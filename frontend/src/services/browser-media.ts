import type { PlayerControls } from "../components/player";
import { PlaybackError } from "./api";

type SafariVideo = HTMLVideoElement & {
  webkitSupportsPresentationMode?: (mode: string) => boolean;
  webkitSetPresentationMode?: (mode: string) => void;
  webkitPresentationMode?: string;
  webkitEnterFullscreen?: () => void;
  webkitExitFullscreen?: () => void;
  webkitDisplayingFullscreen?: boolean;
};
type FullscreenDocument = Document & {
  webkitFullscreenEnabled?: boolean;
  webkitFullscreenElement?: Element | null;
  webkitExitFullscreen?: () => void;
};
type FullscreenTarget = HTMLElement & { webkitRequestFullscreen?: () => void };

/** The element shown through the (possibly prefixed) element fullscreen API. */
export function fullscreenElement(): Element | null {
  const doc = document as FullscreenDocument;
  return doc.fullscreenElement ?? doc.webkitFullscreenElement ?? null;
}
function elementFullscreen() {
  const doc = document as FullscreenDocument;
  return Boolean(doc.fullscreenEnabled || doc.webkitFullscreenEnabled);
}

export function mediaControls(video: HTMLVideoElement | null): PlayerControls {
  const safari = video as SafariVideo | null;
  const available = Boolean(video && video.readyState >= 2);
  // iPhone Safari can only show the video element itself, once metadata exists.
  const videoOnly = typeof safari?.webkitEnterFullscreen === "function";
  return {
    audio: { enabled: true },
    fullscreen:
      video && (elementFullscreen() || (videoOnly && video.readyState >= 1))
        ? { enabled: true }
        : {
            enabled: false,
            reason: videoOnly
              ? "Fullscreen is available after video arrives."
              : "Fullscreen is not supported by this browser.",
          },
    pip:
      available &&
      ((document.pictureInPictureEnabled &&
        typeof video?.requestPictureInPicture === "function" &&
        !video.disablePictureInPicture) ||
        (typeof safari?.webkitSetPresentationMode === "function" &&
          safari.webkitSupportsPresentationMode?.("picture-in-picture")))
        ? { enabled: true }
        : {
            enabled: false,
            reason: available
              ? "Picture in picture is not supported by this browser."
              : "Picture in picture is available after video arrives.",
          },
  };
}
// Fullscreen shows the player container, not the bare video, so status,
// messages and controls stay visible. iPhone Safari falls back to its native
// video fullscreen, which brings its own controls.
export async function fullscreen(
  video: HTMLVideoElement,
  container?: HTMLElement | null,
) {
  const doc = document as FullscreenDocument;
  const safari = video as SafariVideo;
  if (fullscreenElement()) {
    if (typeof doc.exitFullscreen === "function") return doc.exitFullscreen();
    if (doc.webkitExitFullscreen) return doc.webkitExitFullscreen();
  }
  const target: FullscreenTarget = container ?? video;
  if (doc.fullscreenEnabled && typeof target.requestFullscreen === "function")
    return target.requestFullscreen();
  if (doc.webkitFullscreenEnabled && target.webkitRequestFullscreen)
    return target.webkitRequestFullscreen();
  if (safari.webkitDisplayingFullscreen && safari.webkitExitFullscreen)
    return safari.webkitExitFullscreen();
  if (safari.webkitEnterFullscreen) return safari.webkitEnterFullscreen();
  throw new PlaybackError("unsupported_capability", false);
}
export async function pictureInPicture(video: HTMLVideoElement) {
  const safari = video as SafariVideo;
  if (document.pictureInPictureElement) return document.exitPictureInPicture();
  if (
    document.pictureInPictureEnabled &&
    video.requestPictureInPicture &&
    !video.disablePictureInPicture
  ) {
    await video.requestPictureInPicture();
    return;
  }
  if (
    safari.webkitSupportsPresentationMode?.("picture-in-picture") &&
    safari.webkitSetPresentationMode
  ) {
    safari.webkitSetPresentationMode(
      safari.webkitPresentationMode === "picture-in-picture"
        ? "inline"
        : "picture-in-picture",
    );
    return;
  }
  throw new PlaybackError("unsupported_capability", false);
}
export async function detachVideo(video: HTMLVideoElement) {
  // Best effort exit does not prevent immediate local media teardown.
  video.pause();
  video.srcObject = null;
  try {
    if (document.pictureInPictureElement === video)
      await document.exitPictureInPicture();
    const safari = video as SafariVideo;
    if (safari.webkitPresentationMode === "picture-in-picture")
      safari.webkitSetPresentationMode?.("inline");
    if (document.fullscreenElement === video) await document.exitFullscreen();
  } catch {
    /* Browser owns presentation exit during node removal. */
  }
}

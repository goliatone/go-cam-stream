import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { AppProps, ViewerInput } from "../app";
import type { CameraList, ErrorBody, StreamRef } from "../services/contracts";
import { APIClient, safeError, type ViewerAPI } from "../services/api";
import {
  PlaybackController,
  type PlaybackSnapshot,
} from "../services/playback";
import {
  detachVideo,
  fullscreen,
  mediaControls,
  pictureInPicture,
} from "../services/browser-media";
import { linkFor, parseLink, withView } from "../services/links";

// Replace the address without adding a history entry. Browsers rate-limit
// history updates and may throw; the view itself must not depend on it.
function showAddress(link: string) {
  const next = withView(location.href, link);
  if (next === location.href) return;
  try {
    history.replaceState(history.state, "", next);
  } catch {
    // The address catches up on the next selection or history traversal.
  }
}

export function useViewer(api: ViewerAPI): AppProps & { onReload: () => void } {
  const [catalog, setCatalog] = useState<CameraList>({ cameras: [] });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ErrorBody>();
  const [snapshot, setSnapshot] = useState<PlaybackSnapshot>();
  const [needsPlay, setNeedsPlay] = useState(false);
  const [paused, setPaused] = useState(false);
  const [controls, setControls] = useState(() => mediaControls(null));
  // The address only requests a view; App resolves it against the catalog.
  const [initialSelection] = useState(() => parseLink(location.search));
  // Address of the view on screen. Fragment links such as the skip link add
  // same-document history entries; traversing them must not leave the address
  // naming a camera this page no longer shows.
  const viewLink = useRef<string | undefined>(undefined);
  const catalogRef = useRef(catalog);
  catalogRef.current = catalog;
  const video = useRef<HTMLVideoElement | null>(null);
  const mediaRef = useRef<MediaStream | null>(null);
  const alive = useRef(true);
  const [controller] = useState(
    () =>
      new PlaybackController(api, (next) => {
        if (alive.current) setSnapshot(next);
      }),
  );
  const refreshAbort = useRef<AbortController | undefined>(undefined);
  const reload = useCallback(() => {
    refreshAbort.current?.abort();
    const abort = new AbortController();
    refreshAbort.current = abort;
    setLoading(true);
    setError(undefined);
    void api
      .list(abort.signal)
      .then((result) => {
        if (!abort.signal.aborted && alive.current) setCatalog(result);
      })
      .catch((reason) => {
        if (!abort.signal.aborted && alive.current) {
          setCatalog({ cameras: [] });
          setError(safeError(reason, "server_unavailable"));
        }
      })
      .finally(() => {
        if (!abort.signal.aborted && alive.current) setLoading(false);
      });
  }, [api]);
  const onSelect = useCallback(
    (ref: StreamRef | undefined) => {
      const camera =
        ref && catalogRef.current.cameras.find((c) => c.id === ref.camera_id);
      const stream =
        ref && camera?.streams.find((s) => s.id === ref.stream_id);
      setNeedsPlay(false);
      setPaused(false);
      void controller.select(ref, stream?.capabilities);
      // Keep the address on the effective view without adding history entries.
      // Loading, denied and empty states leave it alone, so a reload retries it.
      if (ref && camera) {
        viewLink.current = linkFor(camera, ref.stream_id);
        showAddress(viewLink.current);
      }
    },
    [controller],
  );

  // Stable ref preserves the video node and its browser-owned PiP presentation
  // across status updates. Selection changes unmount and detach the old node.
  const bindVideo = useCallback(
    (node: HTMLVideoElement | null) => {
      const old = video.current;
      if (old && old !== node) {
        old.onplaying =
          old.ontimeupdate =
          old.onwaiting =
          old.onloadedmetadata =
          old.onemptied =
          old.onpause =
          old.onplay =
          old.onerror =
            null;
        void detachVideo(old);
      }
      video.current = node;
      if (!node) return;
      node.muted = true;
      const playing = () => {
        if (!alive.current || video.current !== node) return;
        if (!node.paused && node.readyState >= 2) {
          controller.playing();
          setNeedsPlay(false);
        }
        setControls(mediaControls(node));
      };
      node.onplaying = node.ontimeupdate = playing;
      // A picture-in-picture pause button, a media key or the OS can pause
      // live video. Media keeps arriving, so show Paused and offer Play.
      node.onpause = () => {
        if (alive.current && video.current === node && node.srcObject)
          setPaused(true);
      };
      node.onplay = () => {
        if (alive.current && video.current === node) setPaused(false);
      };
      node.onwaiting = () => controller.waiting();
      node.onerror = () => controller.mediaError();
      // Controls depend on the element's media. Removing it empties the
      // element, so iPhone's video-only fullscreen must not stay enabled.
      node.onloadedmetadata = node.onemptied = () => {
        if (alive.current && video.current === node)
          setControls(mediaControls(node));
      };
      setControls(mediaControls(node));
    },
    [controller],
  );

  useEffect(() => {
    const node = video.current;
    const media = snapshot?.media ?? null;
    if (!node) return;
    if (node.srcObject !== media) {
      node.srcObject = media;
      mediaRef.current = media;
      node.muted = true;
      setNeedsPlay(false);
      setPaused(false);
      setControls(mediaControls(node));
      if (media)
        void node.play().catch(() => {
          if (
            alive.current &&
            video.current === node &&
            mediaRef.current === media
          )
            setNeedsPlay(true);
        });
    }
  }, [snapshot]);
  useEffect(() => {
    if (snapshot?.error?.code === "access_denied") reload();
  }, [snapshot?.error?.code]);
  useEffect(() => {
    reload();
    const visibility = () =>
      controller.observe(document.visibilityState !== "hidden");
    const pagehide = (event: PageTransitionEvent) => {
      if (event.persisted) {
        void controller.select(undefined);
        return;
      }
      void controller.dispose(true).catch(() => {});
    };
    const pageshow = (event: PageTransitionEvent) => {
      if (event.persisted) reload();
    };
    const popstate = () => {
      if (viewLink.current) showAddress(viewLink.current);
    };
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("pagehide", pagehide);
    window.addEventListener("pageshow", pageshow);
    window.addEventListener("popstate", popstate);
    return () => {
      alive.current = false;
      refreshAbort.current?.abort();
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("pagehide", pagehide);
      window.removeEventListener("pageshow", pageshow);
      window.removeEventListener("popstate", popstate);
      if (video.current) void detachVideo(video.current);
      void controller.dispose().catch(() => {});
    };
  }, [controller, reload]);
  let viewer: ViewerInput | undefined;
  if (snapshot)
    viewer = {
      selection: snapshot.selection,
      ...(snapshot.status ? { status: snapshot.status } : {}),
      presentation: {
        state: snapshot.state,
        ...(snapshot.error ? { error: snapshot.error } : {}),
        ...(snapshot.transport ? { transport: snapshot.transport } : {}),
        needsPlay,
        paused,
        reloadRequired: snapshot.reloadRequired ?? false,
        controls,
        videoRef: bindVideo,
        actions: {
          onRetry: () => controller.retry(),
          onPlay: async () => {
            const node = video.current;
            if (!node) return;
            const media = node.srcObject;
            await node.play();
            if (alive.current && video.current === node && node.srcObject === media)
              setNeedsPlay(false);
          },
          onMute: async (muted) => {
            const node = video.current;
            if (!node) return;
            node.muted = muted;
            try {
              await node.play();
            } catch {
              node.muted = true;
              throw new Error("playback_not_allowed");
            }
          },
          onFullscreen: (target) =>
            video.current
              ? fullscreen(video.current, target)
              : Promise.resolve(),
          onPictureInPicture: () =>
            video.current ? pictureInPicture(video.current) : Promise.resolve(),
        },
      },
    };
  return {
    catalog,
    loading,
    ...(error ? { error } : {}),
    ...(viewer ? { viewer } : {}),
    initialSelection,
    onSelect,
    onReload: reload,
  };
}
export const viewerAPI = new APIClient();

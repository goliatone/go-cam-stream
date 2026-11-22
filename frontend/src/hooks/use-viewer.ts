import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { AppProps, ViewerInput } from "../app";
import type { CameraList, CatalogRevision, ErrorBody, StreamRef } from "../services/contracts";
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
import { FramePreviews } from "../services/frame-previews";

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

/** Camera rows show the server's availability, refreshed this often while visible. */
export const AVAILABILITY_MS = 4000;
/** A playing camera's tile frame is kept this long after it starts, then this often. */
export const FIRST_FRAME_MS = 2000;
export const FRAME_EVERY_MS = 15000;

export function useViewer(api: ViewerAPI): AppProps & { onReload: () => void; refreshCatalog: (minimum?: CatalogRevision) => Promise<CatalogRevision | undefined>; onRecovered: () => Promise<void> } {
  const [catalog, setCatalog] = useState<CameraList>({ cameras: [] });
  const [catalogStale, setCatalogStale] = useState(false);
  const requestSequence = useRef(0);
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
  // The camera whose media the video shows, and the last frame of each camera.
  const mediaCamera = useRef<string | undefined>(undefined);
  const [frames] = useState(() => new FramePreviews());
  const [, setFramesSeen] = useState(0);
  // Nothing is kept of a camera that left the list, or once the page unloads.
  const keepFrame = (node: HTMLVideoElement | null) => {
    const camera = mediaCamera.current;
    if (
      node && camera && node.srcObject && node.srcObject === mediaRef.current &&
      catalogRef.current.cameras.some((item) => item.id === camera)
    )
      frames.capture(camera, node);
  };
  const alive = useRef(true);
  const [controller] = useState(
    () =>
      new PlaybackController(api, (next) => {
        if (alive.current) setSnapshot(next);
      }),
  );
  const catalogAbort = useRef<AbortController | undefined>(undefined);
  const refreshCatalog = useCallback(async (minimum?: CatalogRevision) => {
    const sequence = ++requestSequence.current;
    catalogAbort.current?.abort();
    const abort = new AbortController(); catalogAbort.current = abort;
    try {
      const result = await api.list(abort.signal);
      if (alive.current && sequence === requestSequence.current) {
        const old = catalogRef.current.revision, next = result.revision;
        if (next && old && next.runtime_epoch === old.runtime_epoch && next.generation < old.generation) throw Error();
        if (minimum && next && next.runtime_epoch === minimum.runtime_epoch && next.generation < minimum.generation) throw Error();
        catalogRef.current = result; setCatalog(result); setCatalogStale(false);
        frames.retain(result.cameras.map((camera) => camera.id));
      }
      const revision = catalogRef.current.revision;
      if (minimum && (!revision || (revision.runtime_epoch === minimum.runtime_epoch && revision.generation < minimum.generation))) throw Error();
      return revision;
    } catch (reason) {
      if (alive.current && sequence === requestSequence.current) {
        if (safeError(reason).code === "access_denied") { catalogRef.current={cameras:[]};setCatalog(catalogRef.current); setError(safeError(reason)); frames.clear(); }
        else { const stale={...catalogRef.current,cameras:catalogRef.current.cameras.map(camera=>({...camera,availability:"unknown" as const}))}; catalogRef.current=stale;setCatalog(stale);setCatalogStale(true); }
      }
      throw reason;
    }
  }, [api]);
  const reload = useCallback(() => {
    setLoading(true);
    setError(undefined);
    const sequence=requestSequence.current+1;
    void refreshCatalog().catch((reason) => {
      if (alive.current && sequence===requestSequence.current && !catalogAbort.current?.signal.aborted) {
        setCatalog({cameras:[]});
        setError(safeError(reason, "server_unavailable"));
      }
    }).finally(() => { if (alive.current) setLoading(false); });
  }, [refreshCatalog]);
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
        // Leaving a camera, or opening the grid, keeps its last frame.
        keepFrame(old);
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
      // A stop or a new connection replaces the media; keep what it showed.
      keepFrame(node);
      node.srcObject = media;
      mediaRef.current = media;
      mediaCamera.current = media ? snapshot?.selection.camera_id : undefined;
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
  // A playing camera's frame is kept soon after it starts and then
  // periodically, so its tile stays close to what this page last saw.
  const live = snapshot?.state === "playing" ? snapshot.selection.camera_id : undefined;
  useEffect(() => {
    if (!live) return;
    let timer = setTimeout(function keep() {
      keepFrame(video.current);
      timer = setTimeout(keep, FRAME_EVERY_MS);
    }, FIRST_FRAME_MS);
    return () => clearTimeout(timer);
  }, [live]);
  useEffect(
    () => frames.subscribe(() => {
      if (alive.current) setFramesSeen((seen) => seen + 1);
    }),
    [frames],
  );
  useEffect(() => {
    reload();
    const visibility = () =>
      controller.observe(document.visibilityState !== "hidden");
    // A page opened in a background tab starts hidden.
    visibility();
    const pagehide = (event: PageTransitionEvent) => {
      // Frames live only as long as the page, and none is kept as it goes.
      mediaCamera.current = undefined;
      frames.clear();
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
      catalogAbort.current?.abort();
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("pagehide", pagehide);
      window.removeEventListener("pageshow", pageshow);
      window.removeEventListener("popstate", popstate);
      if (video.current) void detachVideo(video.current);
      frames.clear();
      void controller.dispose().catch(() => {});
    };
  }, [controller, reload]);
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined, stopped = false;
    const refresh = () => {
      clearTimeout(timer);
      if (document.visibilityState === "hidden" || stopped) { catalogAbort.current?.abort(); ++requestSequence.current; return; }
      void refreshCatalog().catch(() => {}).finally(() => { if (!stopped) timer = setTimeout(refresh, AVAILABILITY_MS); });
    };
    timer = setTimeout(refresh, AVAILABILITY_MS);
    document.addEventListener("visibilitychange", refresh);
    return () => { stopped = true; clearTimeout(timer); document.removeEventListener("visibilitychange", refresh); };
  }, [refreshCatalog]);
  const viewer: ViewerInput | undefined = snapshot && {
    selection: snapshot.selection,
    status: snapshot.status,
    statusAt: snapshot.statusAt,
    observingSince: snapshot.observingSince,
    presentation: {
      state: snapshot.state,
      recoverBy: snapshot.recoverBy,
      retryAt: snapshot.retryAt,
      error: snapshot.error,
      transport: snapshot.transport,
      needsPlay,
      paused,
      reloadRequired: snapshot.reloadRequired ?? false,
      controls,
      videoRef: bindVideo,
      actions: {
        // The controller reports progress through snapshots and publishes
        // connecting at once, so the player's pending action ends here
        // rather than holding every control until media arrives.
        onRetry: () => {
          void controller.retry();
        },
        // Ends this page's session; the stopped view offers Play (retry).
        onStop: () => controller.stop(),
        // Media keeps arriving while paused; the pause event shows Play.
        onPause: () => video.current?.pause(),
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
          video.current ? fullscreen(video.current, target) : Promise.resolve(),
        onPictureInPicture: () =>
          video.current ? pictureInPicture(video.current) : Promise.resolve(),
      },
    },
  };
  return {
    catalog,
    loading,
    error,
    viewer,
    initialSelection,
    onSelect,
    previewFor: (camera) => frames.previewFor(camera),
    onReload: reload,
    catalogStale,
    refreshCatalog,
    onRecovered: async () => { await controller.stop(); await controller.retry(); },
  };
}
export const viewerAPI = new APIClient();

import { useEffect, useRef, useState } from "preact/hooks";
import { useTick } from "../components/status";
import { detachVideo, fullscreen } from "../services/browser-media";
import { isEnded, type GuestPhase, type GuestSession, type GuestView } from "./guest-session";

interface GuestCopy {
  title: string;
  detail?: string;
  action?: "Play" | "Retry";
}

/** "3:15 PM", or with the date when the share ends on another day. */
export function endTime(at: number, now = Date.now(), locale?: string) {
  const sameDay = new Date(at).toDateString() === new Date(now).toDateString();
  return new Intl.DateTimeFormat(locale, {
    ...(sameDay ? {} : { month: "short", day: "numeric" }),
    hour: "numeric",
    minute: "2-digit",
  }).format(at);
}

/** Plain-language state for the guest. Server text never appears here. */
export function guestCopy(view: GuestView, seconds = 0): GuestCopy {
  // The end time shows below the video while the link is valid.
  const copy: Record<GuestPhase, GuestCopy> = {
    checking: { title: "Opening the shared camera…" },
    ready: { title: "Ready to watch", detail: "Video only, without sound.", action: "Play" },
    connecting: { title: "Connecting…" },
    live: { title: "Live" },
    reconnecting: {
      title: "Reconnecting…",
      detail: `Your place is kept for ${seconds} more ${seconds === 1 ? "second" : "seconds"}.`,
    },
    stalled: { title: "Video paused", detail: "Your connection may be weak.", action: "Retry" },
    lost: { title: "Connection lost", detail: "Retry to watch again if a place is free.", action: "Retry" },
    full: {
      title: "This camera is already being viewed",
      detail: "Retry when the other viewer has finished.",
      action: "Retry",
    },
    unavailable: { title: "The camera isn't available right now", detail: "Try again in a moment.", action: "Retry" },
    offline: { title: "You're offline", detail: "Check your internet connection, then retry.", action: "Retry" },
    expired: { title: "This link has expired", detail: "Ask the person who shared it for a new link." },
    revoked: { title: "Sharing has stopped", detail: "The person who shared this camera ended the link." },
    denied: {
      title: "This link doesn't work",
      detail: "Check that you opened the complete link, or ask for a new one.",
    },
    unsupported: {
      title: "This browser can't play the video",
      detail: view.embedded
        ? "Open the link in Safari or Chrome. In this app, use its menu to open the page in your browser."
        : "Open the link in a current version of Safari, Chrome, Edge or Firefox.",
    },
    reopen: {
      title: "Open the link again",
      detail: "This page doesn't keep your link. Open the original link to watch.",
    },
  };
  return copy[view.phase];
}

function useGuestView(session: GuestSession) {
  const [view, setView] = useState(session.view);
  useEffect(() => {
    setView(session.view);
    return session.subscribe(setView);
  }, [session]);
  return view;
}

/** Starts playback; the result is false only when the browser wants a tap. */
async function start(video: HTMLVideoElement) {
  try {
    await video.play();
    return true;
  } catch (error) {
    return !(error instanceof DOMException && error.name === "NotAllowedError");
  }
}

const elementFullscreen = () => {
  const doc = document as Document & { webkitFullscreenEnabled?: boolean };
  return Boolean(doc.fullscreenEnabled || doc.webkitFullscreenEnabled);
};

/**
 * The invitation viewer: one video, Play/Retry and plain status. There are no
 * sound, microphone or management controls, and the page never asks for
 * camera or microphone permission.
 */
export function GuestViewer({
  session,
  monotonic = () => performance.now(),
}: {
  session: GuestSession;
  monotonic?: () => number;
}) {
  const view = useGuestView(session);
  const video = useRef<HTMLVideoElement>(null);
  const stage = useRef<HTMLDivElement>(null);
  const action = useRef<HTMLButtonElement>(null);
  // The browser refused to start playback without another tap.
  const [blocked, setBlocked] = useState(false);
  useTick(view.phase === "reconnecting");

  useEffect(() => {
    const element = video.current;
    if (!element) return;
    const media = view.media;
    if (!media) {
      setBlocked(false);
      if (element.srcObject) void detachVideo(element);
      return;
    }
    if (element.srcObject === media) return;
    element.defaultMuted = true;
    element.muted = true;
    element.srcObject = media;
    let reported = false;
    const presented = () => {
      if (reported) return;
      reported = true;
      session.framesStarted(media);
    };
    // Live only once a frame is presented, not when negotiation completes.
    if (typeof element.requestVideoFrameCallback === "function")
      element.requestVideoFrameCallback(presented);
    const playing = () => {
      if (element.videoWidth > 0) presented();
    };
    element.addEventListener("playing", playing);
    void start(element).then((started) => setBlocked(!started));
    return () => element.removeEventListener("playing", playing);
  }, [view.media, session]);

  // A phone may pause the video in the background; resume it on return.
  useEffect(() => {
    const resume = () => {
      const element = video.current;
      if (document.visibilityState === "visible" && element?.srcObject && element.paused)
        void start(element).then((started) => setBlocked(!started));
    };
    document.addEventListener("visibilitychange", resume);
    return () => document.removeEventListener("visibilitychange", resume);
  }, []);

  useEffect(() => {
    document.title = view.phase === "live" ? "Live · Shared camera" : "Shared camera";
  }, [view.phase]);

  const ended = isEnded(view.phase);
  const seconds =
    view.reconnectUntil === null ? 0 : Math.max(0, Math.ceil((view.reconnectUntil - monotonic()) / 1000));
  const copy: GuestCopy = blocked && view.media
    ? { title: "Tap Play to start the video", action: "Play" }
    : guestCopy(view, seconds);
  // Move focus to a newly offered action when nothing else holds it.
  useEffect(() => {
    const focused = document.activeElement;
    if (action.current && (!focused || focused === document.body)) action.current.focus();
  }, [copy.action, view.phase]);

  const live = view.phase === "live" && !blocked;
  const badge = view.phase === "reconnecting" && view.media !== null;
  const act = () => {
    if (blocked && video.current) {
      void start(video.current).then((started) => setBlocked(!started));
      return;
    }
    session.play();
  };
  return (
    <div class="guest">
      <header class="guest-bar">
        <span class="guest-brand">
          <svg class="guest-icon" viewBox="0 0 32 32" aria-hidden="true">
            <rect x="1" y="8" width="21" height="17" rx="5" fill="currentColor" />
            <path d="M22 14.5 30 10v13l-8-4.5z" fill="currentColor" />
          </svg>
          Shared camera
        </span>
        <span class="guest-video-only" role="note" aria-label="Video only. This shared camera has no sound.">
          <svg class="guest-icon" viewBox="0 0 24 24" aria-hidden="true">
            <path
              d="M3 7.5A2.5 2.5 0 0 1 5.5 5h8A2.5 2.5 0 0 1 16 7.5v1.3l4.2-2.4A.5.5 0 0 1 21 6.8v10.4a.5.5 0 0 1-.8.4L16 15.2v1.3a2.5 2.5 0 0 1-2.5 2.5h-8A2.5 2.5 0 0 1 3 16.5z"
              fill="currentColor"
            />
          </svg>
          Video only
        </span>
      </header>
      <main class="guest-main">
        <div class={`guest-stage guest-${view.phase}`} ref={stage}>
          <video
            ref={video}
            class="guest-video"
            muted
            autoplay
            playsInline
            disablePictureInPicture
            disableRemotePlayback
            hidden={!view.media}
            aria-label="Shared camera video"
          />
          {live && (
            <span class="guest-live">
              <span class="guest-dot" aria-hidden="true" />
              Live
            </span>
          )}
          {badge && (
            <span class="guest-badge">
              <span class="guest-dot guest-dot-busy" aria-hidden="true" />
              Reconnecting · {seconds} s
            </span>
          )}
          {/* Phones in landscape hide the bar; its note stays readable there. */}
          <span class="guest-stage-note" aria-hidden="true">
            Video only
          </span>
          {!live && !badge && (
            <div class="guest-panel">
              <p class="guest-title">{copy.title}</p>
              {copy.detail && <p class="guest-detail">{copy.detail}</p>}
              {copy.action && (
                <button ref={action} type="button" class="guest-action" onClick={act}>
                  {copy.action === "Play" && (
                    <svg class="guest-icon" viewBox="0 0 24 24" aria-hidden="true">
                      <path d="M8 5.5v13a.5.5 0 0 0 .8.4l10-6.5a.5.5 0 0 0 0-.8l-10-6.5a.5.5 0 0 0-.8.4z" fill="currentColor" />
                    </svg>
                  )}
                  {copy.action}
                </button>
              )}
            </div>
          )}
          {live && elementFullscreen() && (
            <button
              type="button"
              class="guest-fullscreen"
              aria-label="Full screen"
              onClick={() => {
                if (video.current) void fullscreen(video.current, stage.current).catch(() => {});
              }}
            >
              <svg class="guest-icon" viewBox="0 0 24 24" aria-hidden="true">
                <path
                  d="M4 9V5a1 1 0 0 1 1-1h4v2H6v3zm11-5h4a1 1 0 0 1 1 1v4h-2V6h-3zM6 15v3h3v2H5a1 1 0 0 1-1-1v-4zm12 0h2v4a1 1 0 0 1-1 1h-4v-2h3z"
                  fill="currentColor"
                />
              </svg>
            </button>
          )}
        </div>
        {/* State changes only: the countdown stays visual, not announced each second. */}
        <p class="guest-sr" role="status" aria-live="polite">
          {live ? "Live" : copy.title}
        </p>
        {view.expiresAt !== null && !ended && (
          <p class="guest-until">Shared until {endTime(view.expiresAt)}</p>
        )}
        {view.embedded && !ended && (
          <p class="guest-hint">If the video doesn't start, open the original link in Safari or Chrome.</p>
        )}
      </main>
    </div>
  );
}

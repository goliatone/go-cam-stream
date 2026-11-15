import type { Ref } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import type { ErrorBody, State, StreamSummary } from "../services/contracts";
import { fullscreenElement } from "../services/browser-media";
import {
  CameraIcon,
  ExitFullscreenIcon,
  FullscreenIcon,
  MutedIcon,
  PipIcon,
  PlayIcon,
  VolumeIcon,
} from "./icon";
import { describeView, errorCopy, StatusBadge } from "./status";

export type Control = { enabled: true } | { enabled: false; reason: string };
export interface PlayerControls {
  audio: Control;
  fullscreen: Control;
  pip: Control;
}
export interface PlayerActions {
  onPlay?: () => void | Promise<void>;
  onRetry?: () => void | Promise<void>;
  onMute?: (muted: boolean) => void | Promise<void>;
  /** Receives the player container, which is what goes fullscreen. */
  onFullscreen?: (target: HTMLElement) => void | Promise<void>;
  onPictureInPicture?: () => void | Promise<void>;
}
export interface PlayerPresentation {
  transport?: "tcp" | "udp";
  needsPlay?: boolean;
  /** The video element is paused while media keeps arriving. */
  paused?: boolean;
  reloadRequired?: boolean;
  state: State;
  error?: ErrorBody;
  controls?: PlayerControls;
  actions?: PlayerActions;
  videoRef?: Ref<HTMLVideoElement>;
}

const unavailable = {
  enabled: false,
  reason: "Available when video is connected.",
} as const;
const defaultControls: PlayerControls = {
  audio: unavailable,
  fullscreen: unavailable,
  pip: unavailable,
};
const stateCopy: Record<
  Exclude<State, "playing">,
  { title: string; detail: string }
> = {
  unknown: {
    title: "Getting ready",
    detail: "Waiting for this stream's status.",
  },
  idle: {
    title: "Ready to connect",
    detail: "This stream is ready to watch.",
  },
  connecting: {
    title: "Connecting…",
    detail: "Video will appear in a moment.",
  },
  reconnecting: {
    title: "Reconnecting…",
    detail: "The connection dropped. Trying to restore it.",
  },
  closing: {
    title: "Closing the previous view",
    detail: "Waiting for it to finish.",
  },
  closed: {
    title: "Video stopped",
    detail: "Try again to watch this stream.",
  },
  offline: {
    title: "Camera offline",
    detail: "This camera isn't available. Other cameras may still work.",
  },
  failed: {
    title: "Video unavailable",
    detail: "This view couldn't connect. Other cameras may still work.",
  },
};

export function Player({
  stream,
  presentation,
  reloadHref,
}: {
  stream: StreamSummary;
  presentation: PlayerPresentation;
  /** Address of this view, so a reload reopens the same camera and stream. */
  reloadHref: string;
}) {
  const {
    state,
    error,
    actions = {},
    controls = defaultControls,
    videoRef,
    needsPlay = false,
    paused = false,
    reloadRequired = false,
    transport,
  } = presentation;
  const [muted, setMuted] = useState(true);
  const [pending, setPending] = useState<string | null>(null);
  const [feedback, setFeedback] = useState("");
  const [isFullscreen, setFullscreen] = useState(false);
  const section = useRef<HTMLElement>(null);
  const mounted = useRef(true);
  useEffect(() => {
    if (state === "connecting") {
      setMuted(true);
      setFeedback("");
    }
  }, [state]);
  useEffect(() => {
    const changed = () =>
      setFullscreen(
        section.current !== null && fullscreenElement() === section.current,
      );
    document.addEventListener("fullscreenchange", changed);
    document.addEventListener("webkitfullscreenchange", changed);
    return () => {
      mounted.current = false;
      document.removeEventListener("fullscreenchange", changed);
      document.removeEventListener("webkitfullscreenchange", changed);
    };
  }, []);

  async function run(label: string, action: () => void | Promise<void>) {
    if (pending) return;
    setPending(label);
    setFeedback("");
    try {
      await action();
    } catch {
      if (mounted.current)
        setFeedback(`${label} could not be completed. Please try again.`);
    } finally {
      if (mounted.current) setPending(null);
    }
  }

  const live = state === "playing" && !error;
  const view = describeView({
    state,
    error,
    paused: live && paused,
    needsPlay,
    reloadRequired,
  });
  const copy = reloadRequired
    ? {
        title: "This view needs a reload",
        detail:
          "camstream no longer recognizes this page's video session, for example after a restart. Reload to reconnect.",
      }
    : error
      ? errorCopy[error.code]
      : state === "playing"
        ? undefined
        : stateCopy[state];
  // A paused or blocked video still has live media; Play resumes it.
  const prompt = needsPlay
    ? {
        title: "Ready to play",
        detail: "Your browser needs a tap to start video.",
      }
    : live && paused
      ? { title: "Paused", detail: "Play to return to live video." }
      : undefined;
  const message = prompt ?? copy;
  const mayRetry =
    !reloadRequired &&
    (error
      ? error.retryable
      : ["idle", "closed", "offline", "failed"].includes(state));
  const retryEnabled = mayRetry && Boolean(actions.onRetry) && !pending;
  const audioReason = !stream.capabilities.audio
    ? "This stream has no audio."
    : !live
      ? "Audio is available while video plays."
      : !controls.audio.enabled
        ? controls.audio.reason
        : !actions.onMute
          ? "Audio control is not connected."
          : undefined;
  const fullscreenReason = !stream.capabilities.video
    ? "This stream has no video."
    : !controls.fullscreen.enabled
      ? controls.fullscreen.reason
      : !actions.onFullscreen
        ? "Fullscreen control is not connected."
        : undefined;
  const pipReason = !stream.capabilities.video
    ? "This stream has no video."
    : !live
      ? "Picture in picture is available while video plays."
      : !controls.pip.enabled
        ? controls.pip.reason
        : !actions.onPictureInPicture
          ? "Picture in picture control is not connected."
          : undefined;

  return (
    <section
      ref={section}
      class="player"
      aria-label="Selected camera player"
      {...(isFullscreen ? { "data-fullscreen": "" } : {})}
    >
      <div
        class="video-stage"
        aria-busy={state === "connecting" || state === "reconnecting"}
      >
        <video
          key={stream.id}
          {...(videoRef ? { ref: videoRef } : {})}
          muted={muted}
          playsInline
          aria-label={`${stream.name} video`}
        />
        <div class="stage-badge">
          <StatusBadge view={view} />
          {transport && (
            <span class="media-transport">
              {" "}
              · WebRTC {transport.toUpperCase()}
            </span>
          )}
        </div>
        {message && (
          <div
            class={`player-message${prompt ? " is-prompt" : ""}`}
            role="status"
            aria-live="polite"
          >
            <span
              class={`message-icon ${state === "connecting" || state === "reconnecting" ? "is-connecting" : ""}`}
            >
              <CameraIcon />
            </span>
            <h3>{message.title}</h3>
            <p>{message.detail}</p>
            {prompt && (
              <button
                type="button"
                class="primary-button"
                disabled={Boolean(pending)}
                onClick={() =>
                  actions.onPlay && void run("Play", actions.onPlay)
                }
              >
                <PlayIcon />
                Play video
              </button>
            )}
            {reloadRequired && (
              <a class="primary-button" href={reloadHref}>
                Reload page
              </a>
            )}
            {mayRetry && (
              <button
                type="button"
                class="primary-button"
                disabled={!retryEnabled}
                {...(!actions.onRetry
                  ? { "aria-describedby": "retry-help" }
                  : {})}
                onClick={() =>
                  actions.onRetry && void run("Retry", actions.onRetry)
                }
              >
                {pending === "Retry" ? "Trying again…" : "Try again"}
              </button>
            )}
            {mayRetry && !actions.onRetry && (
              <p class="retry-help" id="retry-help">
                Trying again is available when the viewer is connected.
              </p>
            )}
            {error && !error.retryable && !reloadRequired && (
              <p class="retry-help">Trying again from this page won't fix this.</p>
            )}
          </div>
        )}
        {!stream.capabilities.video && live && !message && (
          <div class="player-message">
            <span class="message-icon">
              <CameraIcon />
            </span>
            <h3>Audio only</h3>
            <p>This stream has no video.</p>
          </div>
        )}
      </div>
      <div class="player-toolbar" aria-label="Player controls">
        <div class="control-item">
          <button
            type="button"
            class="control-button"
            disabled={Boolean(audioReason) || Boolean(pending)}
            aria-pressed={!muted}
            aria-describedby="audio-help"
            onClick={() => {
              const next = !muted;
              void run("Audio change", async () => {
                await actions.onMute?.(next);
                if (mounted.current) setMuted(next);
              });
            }}
          >
            {muted ? <MutedIcon /> : <VolumeIcon />}
            {muted ? "Unmute" : "Mute"}
          </button>
          <span id="audio-help" class="control-help">
            {audioReason ?? (muted ? "Audio is muted." : "Audio is on.")}
          </span>
        </div>
        <div class="control-item">
          <button
            type="button"
            class="control-button"
            disabled={Boolean(fullscreenReason) || Boolean(pending)}
            aria-describedby="fullscreen-help"
            onClick={() => {
              const enter = actions.onFullscreen;
              const target = section.current;
              if (enter && target) void run("Fullscreen", () => enter(target));
            }}
          >
            {isFullscreen ? <ExitFullscreenIcon /> : <FullscreenIcon />}
            {isFullscreen ? "Exit fullscreen" : "Fullscreen"}
          </button>
          <span id="fullscreen-help" class="control-help">
            {fullscreenReason ??
              (isFullscreen
                ? "Return to the page."
                : "Fill the screen with the player.")}
          </span>
        </div>
        <div class="control-item">
          <button
            type="button"
            class="control-button"
            disabled={Boolean(pipReason) || Boolean(pending)}
            aria-describedby="pip-help"
            onClick={() =>
              actions.onPictureInPicture &&
              void run("Picture in picture", actions.onPictureInPicture)
            }
          >
            <PipIcon />
            Picture in picture
          </button>
          <span id="pip-help" class="control-help">
            {pipReason ?? "Keep video in a floating window."}
          </span>
        </div>
      </div>
      <p class="control-feedback" role="status" aria-live="polite">
        {feedback || (pending ? `${pending} in progress…` : "")}
      </p>
    </section>
  );
}

import type { ComponentChildren, Ref } from "preact";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "preact/hooks";
import type {
  ErrorBody,
  ErrorCode,
  State,
  StreamSummary,
} from "../services/contracts";
import { fullscreenElement } from "../services/browser-media";
import { useShortcuts } from "../services/shortcuts";
import {
  CameraIcon,
  ExitFullscreenIcon,
  FullscreenIcon,
  MutedIcon,
  PauseIcon,
  PipIcon,
  PlayIcon,
  StopIcon,
  VolumeIcon,
} from "./icon";
import {
  describeView,
  ErrorDetails,
  errorCopy,
  resumable,
  StatusBadge,
  useTick,
} from "./status";

export type Control = { enabled: true } | { enabled: false; reason: string };
export interface PlayerControls {
  audio: Control;
  fullscreen: Control;
  pip: Control;
}
export interface PlayerActions {
  onPlay?: () => void | Promise<void>;
  onPause?: () => void | Promise<void>;
  /** Releases this page's session; the view then offers Play. */
  onStop?: () => void | Promise<void>;
  onRetry?: () => void | Promise<void>;
  onMute?: (muted: boolean) => void | Promise<void>;
  /** Receives the element to show fullscreen: the player root, or the player. */
  onFullscreen?: (target: HTMLElement) => void | Promise<void>;
  onPictureInPicture?: () => void | Promise<void>;
}
export interface PlayerPresentation {
  transport?: "tcp" | "udp";
  needsPlay?: boolean;
  /** The video element is paused while media keeps arriving. */
  paused?: boolean;
  reloadRequired?: boolean;
  /** While a dropped connection may recover: when it fails (performance.now()). */
  recoverBy?: number;
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
// Reconnecting shows a compact badge instead, over the last frame.
const stateCopy: Record<
  Exclude<State, "playing" | "reconnecting">,
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
  closing: {
    title: "Stopping…",
    detail: "Freeing this page's viewer slot.",
  },
  closed: {
    title: "Video stopped",
    detail: "Play to watch this stream again.",
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
// Failures the server reports for this camera or stream, where another camera
// may still play. Lost connections and timeouts can come from this device's
// network as well, so they offer Try again only.
const cameraFaults = new Set<ErrorCode>([
  "not_found",
  "authentication_failed",
  "camera_unreachable",
  "stream_unavailable",
  "source_unavailable",
  "unsupported_capability",
  "camera_busy",
  "conflict",
]);
/** Controls fade after this long without pointer, touch or keyboard activity. */
const IDLE_MS = 3000;
const HINT_MS = 4000;
type Timer = ReturnType<typeof setTimeout>;

const isControl = (target: EventTarget | null) =>
  target instanceof Element && Boolean(target.closest("button, a, select"));
/** Keyboard focus, not a mouse click, rests on a control inside `node`. */
function keyboardFocusIn(node: HTMLElement | null) {
  const active = document.activeElement;
  if (!node || !active || active === node || !node.contains(active))
    return false;
  try {
    return active.matches(":focus-visible");
  } catch {
    return true;
  }
}

export function Player({
  cameraName,
  stream,
  presentation,
  reloadHref,
  onCameras,
  onChooseCamera,
}: {
  cameraName: string;
  stream: StreamSummary;
  presentation: PlayerPresentation;
  /** Address of this view, so a reload reopens the same camera and stream. */
  reloadHref: string;
  /** Opens the camera list where the page shows only the player. */
  onCameras?: () => void;
  /** Offered where this camera fails; set only when another camera can play. */
  onChooseCamera?: () => void;
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
    recoverBy,
    transport,
  } = presentation;
  const [muted, setMuted] = useState(true);
  const [pending, setPending] = useState<string | null>(null);
  const [hint, setHint] = useState("");
  const [isFullscreen, setFullscreen] = useState(false);
  const [idle, setIdle] = useState(false);
  const section = useRef<HTMLElement>(null);
  const mounted = useRef(true);
  const idleTimer = useRef<Timer | undefined>(undefined);
  const hintTimer = useRef<Timer | undefined>(undefined);
  const running = useRef<string | null>(null);
  const runs = useRef(0);
  // The control that last held focus in the player, and whether a mouse or
  // pen rests on the toolbar. Touch leaves :hover behind, so it does not count.
  const lastFocus = useRef<Element | null>(null);
  const overToolbar = useRef(false);
  useEffect(() => {
    if (state === "connecting") {
      setMuted(true);
      say("");
    }
  }, [state]);
  useEffect(() => {
    // Fullscreen may show the player root, which outlives a camera switch.
    const changed = () => {
      const node = section.current;
      const shown = fullscreenElement();
      setFullscreen(Boolean(node && shown?.contains(node)));
    };
    changed();
    document.addEventListener("fullscreenchange", changed);
    document.addEventListener("webkitfullscreenchange", changed);
    return () => {
      mounted.current = false;
      clearTimeout(idleTimer.current);
      clearTimeout(hintTimer.current);
      document.removeEventListener("fullscreenchange", changed);
      document.removeEventListener("webkitfullscreenchange", changed);
    };
  }, []);

  /** Shows a short status line over the video; an empty text clears it. */
  function say(text: string) {
    clearTimeout(hintTimer.current);
    setHint(text);
    if (text)
      hintTimer.current = setTimeout(() => {
        if (mounted.current) setHint("");
      }, HINT_MS);
  }
  // One action at a time. A ref, not render state, so a key pressed before
  // the next render still sees the action in flight. Stop may preempt, so it
  // can end a Play or Try again that is still connecting; an action that was
  // preempted neither clears the newer one nor reports its own outcome.
  async function run(
    label: string,
    action: () => void | Promise<void>,
    preempt = false,
  ) {
    if (running.current && !preempt) return;
    const turn = ++runs.current;
    running.current = label;
    setPending(label);
    say("");
    try {
      await action();
    } catch {
      if (mounted.current && turn === runs.current)
        say(`${label} could not be completed. Please try again.`);
    } finally {
      if (turn === runs.current) {
        running.current = null;
        if (mounted.current) setPending(null);
      }
    }
  }
  // A control that goes away once its action works, such as Play video or Try
  // again, would drop focus to the page: keep it in the player. Focus that
  // moved elsewhere in the meantime stays where it is.
  useLayoutEffect(() => {
    const last = lastFocus.current;
    const active = document.activeElement;
    if (last && !last.isConnected && (!active || active === document.body)) {
      lastFocus.current = null;
      section.current?.focus({ preventScroll: true });
    }
  });

  const live = state === "playing" && !error;
  const canResume = resumable({ state, error, paused, reloadRequired });
  const stopped = state === "closed" && !error && !reloadRequired;
  const playing = live && !paused && !needsPlay;
  const view = describeView({
    state,
    error,
    paused,
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
      : state === "playing" || state === "reconnecting"
        ? undefined
        : stateCopy[state];
  // These wait on the viewer, not the connection; Play answers each of them.
  const prompt = needsPlay
    ? {
        title: "Ready to play",
        detail: "Your browser needs a tap to start video.",
      }
    : canResume
      ? { title: "Paused", detail: "Play to return to live video." }
      : stopped
        ? {
            title: "Video stopped",
            detail: "Your viewer slot is free. Play to watch again.",
          }
        : undefined;
  const message = prompt ?? copy;
  // The connection dropped; the last frame stays visible while it recovers.
  const recovering = !message && state === "reconnecting";
  const busy =
    !prompt &&
    (state === "connecting" || state === "reconnecting" || state === "closing");
  const mayRetry =
    !prompt &&
    !reloadRequired &&
    (error
      ? error.retryable
      : ["idle", "closed", "offline", "failed"].includes(state));
  const retryEnabled = mayRetry && Boolean(actions.onRetry) && !pending;
  // A dead end of this camera offers the others.
  const switchHelps =
    !prompt &&
    !reloadRequired &&
    (error
      ? cameraFaults.has(error.code)
      : state === "offline" || state === "failed");
  const retryReason = !mayRetry
    ? "Nothing to try again."
    : !actions.onRetry
      ? "Trying again is available when the viewer is connected."
      : undefined;
  // A stopped view starts again; a waiting or paused video resumes.
  const toggle = stopped
    ? actions.onRetry
    : needsPlay || canResume
      ? actions.onPlay
      : playing
        ? actions.onPause
        : undefined;
  const playReason = toggle
    ? undefined
    : "Available while video is connected.";
  const stopReason = !actions.onStop
    ? "Stop control is not connected."
    : !error &&
        !reloadRequired &&
        ["connecting", "playing", "reconnecting"].includes(state)
      ? undefined
      : "Nothing is playing.";
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

  // Controls hide while video plays; pointer movement, a tap or focus brings
  // them back. They stay while anything else needs the viewer's attention.
  const watching =
    playing && stream.capabilities.video && !hint && !pending && !message;
  const wake = useCallback(() => {
    setIdle(false);
    clearTimeout(idleTimer.current);
    if (!watching) return;
    const arm = () => {
      idleTimer.current = setTimeout(() => {
        const node = section.current;
        if (overToolbar.current || keyboardFocusIn(node)) arm();
        else if (mounted.current) setIdle(true);
      }, IDLE_MS);
    };
    arm();
  }, [watching]);
  useEffect(wake, [wake]);
  const hide = () => {
    clearTimeout(idleTimer.current);
    setIdle(true);
  };

  const commands = {
    play() {
      if (toggle) void run(playing ? "Pause" : "Play", toggle);
      else say(playReason ?? "");
    },
    stop() {
      if (stopReason || !actions.onStop) say(stopReason ?? "");
      else if (running.current !== "Stop") void run("Stop", actions.onStop, true);
    },
    mute() {
      if (audioReason) return say(audioReason);
      const next = !muted;
      void run("Audio change", async () => {
        await actions.onMute?.(next);
        if (mounted.current) setMuted(next);
      });
    },
    fullscreen() {
      if (fullscreenReason) return say(fullscreenReason);
      const enter = actions.onFullscreen;
      const node = section.current;
      // The player root outlives camera switches, so fullscreen does too.
      const target =
        node?.closest<HTMLElement>("[data-player-root]") ?? node;
      if (enter && target) void run("Fullscreen", () => enter(target));
    },
    pip() {
      if (pipReason || !actions.onPictureInPicture) say(pipReason ?? "");
      else void run("Picture in picture", actions.onPictureInPicture);
    },
    retry() {
      // A stopped view starts again, as its Play does.
      if (stopped) commands.play();
      else if (retryEnabled && actions.onRetry) void run("Retry", actions.onRetry);
      else if (retryReason) say(retryReason);
    },
  };
  const keys = new Map<string, () => void>([
    [" ", commands.play],
    ["k", commands.play],
    ["s", commands.stop],
    ["m", commands.mute],
    ["f", commands.fullscreen],
    ["p", commands.pip],
    ["r", commands.retry],
  ]);
  useShortcuts((key) => {
    const command = keys.get(key);
    if (!command) return false;
    wake();
    command();
    return true;
  });

  const status = hint || (pending ? `${pending} in progress…` : "");
  return (
    <section
      ref={section}
      class="player"
      tabIndex={-1}
      aria-label="Selected camera player"
      {...(isFullscreen ? { "data-fullscreen": "" } : {})}
      {...(idle ? { "data-idle": "" } : {})}
      onPointerMove={(event) => {
        if (event.pointerType === "mouse") wake();
      }}
      onPointerLeave={(event) => {
        if (event.pointerType === "mouse" && watching) hide();
      }}
      onPointerUp={(event) => {
        if (event.pointerType === "mouse" || isControl(event.target)) return;
        // A tap shows hidden controls, or hides visible ones during playback.
        if (idle || !watching) wake();
        else hide();
      }}
      onDblClick={(event) => {
        if (!isControl(event.target)) commands.fullscreen();
      }}
      onFocusCapture={(event) => {
        if (event.target instanceof Element) lastFocus.current = event.target;
        wake();
      }}
    >
      <div
        class={`video-stage${message ? " has-message" : ""}${prompt ? " has-prompt" : ""}`}
      >
        {/* Busy marks only the video, so status messages and hints inside
            the stage are still announced while it connects. */}
        <video
          key={stream.id}
          aria-busy={busy}
          {...(videoRef ? { ref: videoRef } : {})}
          muted={muted}
          playsInline
          aria-label={`${stream.name} video`}
        />
        <div class="stage-top">
          <div class="stage-badge">
            <StatusBadge view={view} />
            {transport && (
              <span class="media-transport">
                {" "}
                · WebRTC {transport.toUpperCase()}
              </span>
            )}
          </div>
          {onCameras && (
            <button type="button" class="overlay-cameras" onClick={onCameras}>
              <CameraIcon />
              Cameras
            </button>
          )}
        </div>
        <div class="stage-center">
          {recovering && (
            <p class="player-notice">
              <span class="status-dot is-connecting" />
              Reconnecting…
              {recoverBy !== undefined && <Countdown until={recoverBy} />}
            </p>
          )}
          {message && (
            <div class="player-message" role="status" aria-live="polite">
              <span class={`message-icon ${busy ? "is-connecting" : ""}`}>
                <CameraIcon />
              </span>
              <h2>{message.title}</h2>
              <p>{message.detail}</p>
              <div class="message-actions">
                {prompt && (
                  <button
                    type="button"
                    class="primary-button"
                    disabled={Boolean(pending)}
                    onClick={() => {
                      if (toggle) void run("Play", toggle);
                    }}
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
                    onClick={commands.retry}
                  >
                    {pending === "Retry" ? "Trying again…" : "Try again"}
                  </button>
                )}
                {switchHelps && onChooseCamera && (
                  <button
                    type="button"
                    class={mayRetry ? "secondary-button" : "primary-button"}
                    aria-haspopup="dialog"
                    onClick={onChooseCamera}
                  >
                    Choose another camera
                  </button>
                )}
              </div>
              {mayRetry && !actions.onRetry && (
                <p class="retry-help" id="retry-help">
                  Trying again is available when the viewer is connected.
                </p>
              )}
              {error && !error.retryable && !reloadRequired && (
                <p class="retry-help">
                  Trying again from this page won't fix this.
                </p>
              )}
              {error && (
                <ErrorDetails
                  code={error.code}
                  where={[cameraName, stream.name]}
                />
              )}
            </div>
          )}
          {!stream.capabilities.video && live && !message && (
            <div class="player-message">
              <span class="message-icon">
                <CameraIcon />
              </span>
              <h2>Audio only</h2>
              <p>This stream has no video.</p>
            </div>
          )}
        </div>
        <p class="player-hint" role="status" aria-live="polite">
          {status}
        </p>
        {/* Present from the start, so the change is announced; a region
            inserted with its text may go unannounced. */}
        <p class="sr-only" role="status">
          {recovering ? "Reconnecting. Waiting for video to resume." : ""}
        </p>
        <div
          class="player-toolbar"
          role="group"
          aria-label="Player controls"
          onPointerEnter={(event) => {
            overToolbar.current = event.pointerType !== "touch";
          }}
          onPointerLeave={() => {
            overToolbar.current = false;
          }}
        >
          <Control
            id="play"
            label={playing ? "Pause" : "Play"}
            keys="Space"
            reason={playReason}
            onPress={commands.play}
          >
            {playing ? <PauseIcon /> : <PlayIcon />}
          </Control>
          <Control
            id="stop"
            label="Stop"
            keys="S"
            reason={stopReason}
            onPress={commands.stop}
          >
            <StopIcon />
          </Control>
          <Control
            id="audio"
            label={muted ? "Unmute" : "Mute"}
            keys="M"
            reason={audioReason}
            onPress={commands.mute}
          >
            {muted ? <MutedIcon /> : <VolumeIcon />}
          </Control>
          <span class="toolbar-gap" />
          <Control
            id="pip"
            label="Picture in picture"
            keys="P"
            reason={pipReason}
            onPress={commands.pip}
          >
            <PipIcon />
          </Control>
          <Control
            id="fullscreen"
            label={isFullscreen ? "Exit fullscreen" : "Fullscreen"}
            keys="F"
            reason={fullscreenReason}
            onPress={commands.fullscreen}
          >
            {isFullscreen ? <ExitFullscreenIcon /> : <FullscreenIcon />}
          </Control>
        </div>
      </div>
    </section>
  );
}

// Seconds left in the recovery window. Visual only: a live region that
// changed every second would interrupt a screen reader.
function Countdown({ until }: { until: number }) {
  useTick(true);
  const left = Math.ceil((until - performance.now()) / 1000);
  return left > 0 ? (
    <span class="countdown" aria-hidden="true">
      {left} s
    </span>
  ) : null;
}

// Unavailable controls stay focusable and explain why when pressed, by
// pointer, touch or keyboard, instead of silently doing nothing.
function Control({
  id,
  label,
  keys,
  reason,
  onPress,
  children,
}: {
  id: string;
  label: string;
  keys: string;
  reason: string | undefined;
  onPress: () => void;
  children: ComponentChildren;
}) {
  const help = `${id}-help`;
  return (
    <>
      <button
        type="button"
        class="control-button"
        aria-label={label}
        aria-keyshortcuts={keys}
        {...(reason
          ? { "aria-disabled": "true" as const, "aria-describedby": help }
          : {})}
        data-tip={reason ?? `${label} (${keys})`}
        onClick={onPress}
      >
        {children}
      </button>
      {reason && (
        <span id={help} class="sr-only">
          {reason}
        </span>
      )}
    </>
  );
}

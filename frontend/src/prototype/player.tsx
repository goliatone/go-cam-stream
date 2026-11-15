import type { ComponentChildren, RefObject } from "preact";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type {
  CameraSummary,
  ErrorBody,
  StreamRef,
  StreamSummary,
} from "../services/contracts";
import { lookOf, sizeOf } from "./catalog";
import { errorMessage, retryNote, secs, type Describe } from "./copy";
import {
  AlertIcon,
  CheckIcon,
  CopyIcon,
  ExitFullscreenIcon,
  FullscreenIcon,
  GridIcon,
  MinusIcon,
  MutedIcon,
  OfflineIcon,
  PauseIcon,
  PipIcon,
  PlayIcon,
  RefreshIcon,
  StopIcon,
  VolumeIcon,
} from "./icons";
import {
  MAX_RETRIES,
  retryMode,
  type Operator,
  type Phase,
  type Sim,
} from "./sim";
import { startFeed, type Feed } from "./synthetic-video";

export interface MediaReport {
  paused: boolean;
  pip: boolean;
  fullscreen: boolean;
}
export interface PlayerCommands {
  playPause(): void;
  stop(): void;
  mute(): void;
  fullscreen(): void;
  pip(): void;
}
export interface Alternative {
  camera: CameraSummary;
  status: Describe;
}
interface PlayerProps {
  sim: Sim;
  camera: CameraSummary;
  stream: StreamSummary;
  phase: Phase;
  state: Describe;
  operator: Operator;
  operatorMode: boolean;
  alternatives: readonly Alternative[];
  immersive: boolean;
  now: number;
  commands: RefObject<PlayerCommands | null>;
  onMedia: (report: MediaReport) => void;
  onSelect: (ref: StreamRef) => void;
  onChooseCamera: () => void;
  onOpenCameras: () => void;
}

type Timer = ReturnType<typeof setTimeout>;
type FsDocument = Document & {
  webkitFullscreenElement?: Element | null;
  webkitFullscreenEnabled?: boolean;
  webkitExitFullscreen?: () => void;
};
type FsElement = HTMLElement & { webkitRequestFullscreen?: () => void };
type WebkitVideo = HTMLVideoElement & {
  webkitEnterFullscreen?: () => void;
  webkitSupportsPresentationMode?: (mode: string) => boolean;
  webkitSetPresentationMode?: (mode: string) => void;
  webkitPresentationMode?: string;
};

function fullscreenElement() {
  const doc = document as FsDocument;
  return doc.fullscreenElement ?? doc.webkitFullscreenElement ?? null;
}
function canFullscreen(stage: HTMLElement, video: HTMLVideoElement) {
  const doc = document as FsDocument;
  return (
    (doc.fullscreenEnabled && typeof stage.requestFullscreen === "function") ||
    (Boolean(doc.webkitFullscreenEnabled) &&
      typeof (stage as FsElement).webkitRequestFullscreen === "function") ||
    typeof (video as WebkitVideo).webkitEnterFullscreen === "function"
  );
}
// The stage goes fullscreen, not the bare video, so status, messages and
// controls stay visible. iPhone only allows the video element itself.
async function toggleFullscreen(stage: HTMLElement, video: HTMLVideoElement) {
  const doc = document as FsDocument;
  if (fullscreenElement()) {
    if (typeof doc.exitFullscreen === "function") await doc.exitFullscreen();
    else doc.webkitExitFullscreen?.();
    return;
  }
  if (doc.fullscreenEnabled && typeof stage.requestFullscreen === "function")
    return stage.requestFullscreen();
  const element = stage as FsElement;
  if (typeof element.webkitRequestFullscreen === "function")
    return element.webkitRequestFullscreen();
  (video as WebkitVideo).webkitEnterFullscreen?.();
}
function canPip(video: HTMLVideoElement) {
  return (
    (document.pictureInPictureEnabled &&
      typeof video.requestPictureInPicture === "function" &&
      !video.disablePictureInPicture) ||
    Boolean(
      (video as WebkitVideo).webkitSupportsPresentationMode?.(
        "picture-in-picture",
      ),
    )
  );
}
async function togglePip(video: HTMLVideoElement) {
  if (document.pictureInPictureElement)
    return document.exitPictureInPicture();
  if (
    document.pictureInPictureEnabled &&
    typeof video.requestPictureInPicture === "function"
  ) {
    await video.requestPictureInPicture();
    return;
  }
  const safari = video as WebkitVideo;
  safari.webkitSetPresentationMode?.(
    safari.webkitPresentationMode === "picture-in-picture"
      ? "inline"
      : "picture-in-picture",
  );
}
function detach(video: HTMLVideoElement) {
  video.pause();
  video.srcObject = null;
  if (document.pictureInPictureElement === video)
    document.exitPictureInPicture().catch(() => undefined);
}
const isControl = (target: EventTarget | null) =>
  target instanceof Element &&
  Boolean(target.closest("button, a, input, select, .card"));

export function Player(props: PlayerProps) {
  const { sim, camera, stream, phase, state, now, onMedia } = props;
  const stageRef = useRef<HTMLElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const feedRef = useRef<Feed | null>(null);
  const idleTimer = useRef<Timer | undefined>(undefined);
  const hintTimer = useRef<Timer | undefined>(undefined);
  const userPaused = useRef(false);
  const mediaOn =
    phase.kind === "live" ||
    phase.kind === "blocked" ||
    phase.kind === "reconnecting";
  const mediaOnRef = useRef(mediaOn);
  mediaOnRef.current = mediaOn;
  const [paused, setPaused] = useState(false);
  const [muted, setMuted] = useState(true);
  const [pip, setPip] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [fallback, setFallback] = useState(false);
  const [support, setSupport] = useState({ fullscreen: true, pip: true });
  const [idle, setIdle] = useState(false);
  const [hint, setHint] = useState("");
  const feedKey = `${camera.id}/${stream.id}`;
  const playing = phase.kind === "live" && !paused && !pip;

  useEffect(() => {
    const stage = stageRef.current;
    const video = videoRef.current;
    if (stage && video)
      setSupport({
        fullscreen: canFullscreen(stage, video),
        pip: canPip(video),
      });
  }, []);

  // Real media events drive the UI: a pause from the PiP window, media keys
  // or the OS shows Paused, not Playing.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const onPause = () => {
      if (mediaOnRef.current) setPaused(true);
    };
    const onPlay = () => setPaused(false);
    const onEnter = () => setPip(true);
    const onLeave = () => setPip(false);
    const onMode = () =>
      setPip(
        (video as WebkitVideo).webkitPresentationMode === "picture-in-picture",
      );
    const onFullscreen = () =>
      setFullscreen(fullscreenElement() === stageRef.current);
    const media: [string, () => void][] = [
      ["pause", onPause],
      ["play", onPlay],
      ["playing", onPlay],
      ["enterpictureinpicture", onEnter],
      ["leavepictureinpicture", onLeave],
      ["webkitpresentationmodechanged", onMode],
    ];
    for (const [name, fn] of media) video.addEventListener(name, fn);
    document.addEventListener("fullscreenchange", onFullscreen);
    document.addEventListener("webkitfullscreenchange", onFullscreen);
    return () => {
      for (const [name, fn] of media) video.removeEventListener(name, fn);
      document.removeEventListener("fullscreenchange", onFullscreen);
      document.removeEventListener("webkitfullscreenchange", onFullscreen);
    };
  }, []);

  // Synthetic feed: attached while there is a session, detached otherwise.
  useEffect(() => {
    const canvas = canvasRef.current;
    const video = videoRef.current;
    if (!mediaOn || !canvas || !video) return;
    const feed = startFeed(canvas, {
      name: camera.name,
      ...lookOf(camera.id),
      ...sizeOf(stream.id),
    });
    feedRef.current = feed;
    setFallback(!feed.stream);
    video.srcObject = feed.stream;
    return () => {
      feedRef.current = null;
      feed.dispose();
      detach(video);
    };
  }, [mediaOn, feedKey]);

  useEffect(() => {
    const video = videoRef.current;
    if (phase.kind !== "live" || !video?.srcObject || userPaused.current)
      return;
    const source = video.srcObject;
    let current = true;
    video.play().catch(() => {
      if (current && videoRef.current === video && video.srcObject === source)
        sim.blocked();
    });
    return () => {
      current = false;
    };
  }, [phase.kind, feedKey, sim]);

  useEffect(() => {
    feedRef.current?.setFrozen(phase.kind === "reconnecting");
  }, [phase.kind, feedKey]);

  useEffect(() => {
    if (phase.kind === "connecting") {
      setMuted(true);
      userPaused.current = false;
    }
    if (!mediaOn) setPaused(false);
  }, [phase.kind]);

  useEffect(() => {
    onMedia({ paused: phase.kind === "live" && paused, pip, fullscreen });
  }, [onMedia, paused, pip, fullscreen, phase.kind]);

  // Controls hide after 3 s of playback; pointer, tap or focus brings them back.
  const wake = useCallback(() => {
    setIdle(false);
    clearTimeout(idleTimer.current);
    if (!playing) return;
    idleTimer.current = setTimeout(() => {
      const stage = stageRef.current;
      const active = document.activeElement;
      // Stay visible while the pointer rests on the controls or a control
      // has keyboard focus.
      if (
        stage?.querySelector(".ov-bottom:hover, .ov-top:hover") ||
        (active instanceof HTMLElement &&
          stage?.contains(active) &&
          active.matches(":focus-visible"))
      )
        return;
      setIdle(true);
    }, 3000);
  }, [playing]);
  useEffect(() => {
    wake();
    return () => clearTimeout(idleTimer.current);
  }, [wake]);
  useEffect(() => () => clearTimeout(hintTimer.current), []);

  function say(text: string) {
    setHint(text);
    clearTimeout(hintTimer.current);
    hintTimer.current = setTimeout(() => setHint(""), 3500);
    wake();
  }

  const playReason =
    phase.kind === "live" || phase.kind === "stopped" || phase.kind === "blocked"
      ? null
      : "Available once video is connected.";
  const stopReason =
    mediaOn || phase.kind === "connecting" || phase.kind === "retrying"
      ? null
      : "Nothing is playing.";
  const muteReason = !stream.capabilities.audio
    ? "This stream has no audio."
    : phase.kind !== "live"
      ? "Available once video is playing."
      : null;
  const pipReason = fallback
    ? "Picture in picture isn't available in this browser."
    : !support.pip
      ? "This browser doesn't support picture in picture."
      : phase.kind !== "live"
        ? "Available once video is playing."
        : null;
  const fullscreenReason = support.fullscreen
    ? null
    : "This browser doesn't support fullscreen.";

  function playBlocked() {
    void videoRef.current?.play().catch(() => undefined);
    sim.play();
  }
  function playPause() {
    if (playReason) return say(playReason);
    if (phase.kind === "stopped") return sim.play();
    if (phase.kind === "blocked") return playBlocked();
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) {
      userPaused.current = false;
      video
        .play()
        .catch(() => say("The browser blocked playback. Press Play again."));
    } else {
      userPaused.current = true;
      video.pause();
    }
    wake();
  }
  function stop() {
    if (stopReason) return say(stopReason);
    sim.stop();
  }
  function mute() {
    if (muteReason) return say(muteReason);
    const video = videoRef.current;
    if (!video) return;
    const next = !muted;
    video.muted = next;
    setMuted(next);
    wake();
  }
  function toggleFullscreenCommand() {
    if (fullscreenReason) return say(fullscreenReason);
    const stage = stageRef.current;
    const video = videoRef.current;
    if (!stage || !video) return;
    toggleFullscreen(stage, video).catch(() =>
      say("The browser blocked fullscreen."),
    );
    wake();
  }
  function togglePipCommand() {
    if (pipReason && !pip) return say(pipReason);
    const video = videoRef.current;
    if (!video) return;
    togglePip(video).catch(() =>
      say("The browser blocked picture in picture."),
    );
  }

  useEffect(() => {
    props.commands.current = {
      playPause,
      stop,
      mute,
      fullscreen: toggleFullscreenCommand,
      pip: togglePipCommand,
    };
  });
  useEffect(
    () => () => {
      props.commands.current = null;
    },
    [],
  );

  const message = renderMessage();
  const announce =
    phase.kind === "live"
      ? pip
        ? "Playing in picture in picture"
        : paused
          ? `${camera.name} paused`
          : `${camera.name} is live`
      : `${camera.name}: ${state.word}`;

  function renderMessage(): {
    node: ComponentChildren;
    scrim: boolean;
    card: boolean;
  } | null {
    const other = camera.streams.find((s) => s.id !== stream.id) ?? null;
    switch (phase.kind) {
      case "none":
        return null;
      case "connecting": {
        const elapsed = now - phase.since;
        return {
          scrim: false,
          card: false,
          node: (
            <div class="connecting">
              <span class="spinner" aria-hidden="true" />
              <p class="connecting-title">
                {phase.attempt
                  ? `Retrying · attempt ${phase.attempt} of ${MAX_RETRIES}`
                  : `Connecting to ${camera.name}…`}
              </p>
              {elapsed >= 5000 && (
                <p class="connecting-sub">Still connecting · {secs(elapsed)} s</p>
              )}
            </div>
          ),
        };
      }
      case "reconnecting":
        return {
          scrim: false,
          card: false,
          node: (
            <div class="pill-msg">
              <span class="spinner spinner-sm" aria-hidden="true" />
              Restoring the connection…
            </div>
          ),
        };
      case "live":
        if (pip)
          return {
            scrim: true,
            card: true,
            node: (
              <Card
                tone="info"
                icon={<PipIcon />}
                title="Playing in picture in picture"
                actions={
                  <button type="button" class="btn" onClick={togglePipCommand}>
                    Bring video back
                  </button>
                }
              >
                <p>Video continues in a floating window.</p>
              </Card>
            ),
          };
        if (paused)
          return {
            scrim: true,
            card: false,
            node: (
              <button
                type="button"
                class="big-play"
                aria-label="Play"
                onClick={playPause}
              >
                <PlayIcon size={34} />
              </button>
            ),
          };
        return null;
      case "blocked":
        return {
          scrim: true,
          card: true,
          node: (
            <Card
              tone="info"
              icon={<PlayIcon />}
              title="Tap to start video"
              actions={
                <button
                  type="button"
                  class="btn btn-primary"
                  onClick={playBlocked}
                >
                  <PlayIcon size={18} />
                  Play
                </button>
              }
            >
              <p>Your browser needs a tap before video can play.</p>
            </Card>
          ),
        };
      case "stopped":
        return {
          scrim: true,
          card: true,
          node: (
            <Card
              tone="info"
              icon={<StopIcon />}
              title="Video stopped"
              actions={
                <button
                  type="button"
                  class="btn btn-primary"
                  onClick={() => sim.play()}
                >
                  <PlayIcon size={18} />
                  Play
                </button>
              }
            >
              <p>Your viewer slot is free for someone else.</p>
            </Card>
          ),
        };
      case "reload":
        return {
          scrim: true,
          card: true,
          node: (
            <Card
              tone="danger"
              icon={<RefreshIcon />}
              title="This view needs a reload"
              actions={
                <button
                  type="button"
                  class="btn btn-primary"
                  onClick={() => location.reload()}
                >
                  Reload page
                </button>
              }
            >
              <p>
                camstream restarted or your access changed. Reloading brings
                you back to {camera.name}.
              </p>
            </Card>
          ),
        };
      case "retrying": {
        const copy = errorMessage(phase.error, camera.name, null);
        const held = phase.held !== null;
        const remaining = phase.held ?? Math.max(0, phase.at - now);
        return {
          scrim: true,
          card: true,
          node: (
            <Card
              tone="warn"
              icon={<RefreshIcon />}
              title={copy.title}
              actions={
                <>
                  <button
                    type="button"
                    class="btn btn-primary"
                    onClick={() => sim.retry()}
                  >
                    Try now
                  </button>
                  <button
                    type="button"
                    class="btn"
                    onClick={() => sim.cancelRetry()}
                  >
                    Stop retrying
                  </button>
                </>
              }
              footer={
                <ErrorDetails
                  error={phase.error}
                  camera={camera}
                  stream={stream}
                  at={phase.at - phase.delay}
                />
              }
            >
              <p>
                {copy.detail}{" "}
                <span aria-hidden="true">
                  {held
                    ? "Retrying when you return to this tab."
                    : `Retrying in ${secs(remaining)} s.`}
                </span>
                <span class="sr-only">Retrying automatically.</span>
              </p>
              <div class="progress" aria-hidden="true">
                <span
                  key={phase.attempt}
                  style={`animation-duration:${phase.delay}ms;animation-play-state:${held ? "paused" : "running"}`}
                />
              </div>
              <p class="card-meta">
                Attempt {phase.attempt} of {MAX_RETRIES}
              </p>
            </Card>
          ),
        };
      }
      case "failed": {
        const error = phase.error;
        const mode = retryMode(error);
        const busyOther = error.code === "camera_busy" ? other : null;
        const copy = phase.exhausted
          ? {
              title: "Still can't connect",
              detail: `camstream tried ${MAX_RETRIES} times. Check your connection, then try again.`,
            }
          : errorMessage(error, camera.name, busyOther?.name ?? null);
        const actions = (
          <>
            {busyOther && (
              <button
                type="button"
                class="btn btn-primary"
                onClick={() =>
                  props.onSelect({
                    camera_id: camera.id,
                    stream_id: busyOther.id,
                  })
                }
              >
                Watch {busyOther.name}
              </button>
            )}
            <button
              type="button"
              class={busyOther ? "btn" : "btn btn-primary"}
              onClick={() => sim.retry()}
            >
              <RefreshIcon size={18} />
              Try again
            </button>
            <button
              type="button"
              class="btn btn-optional"
              onClick={props.onChooseCamera}
            >
              Choose another camera
            </button>
          </>
        );
        return {
          scrim: true,
          card: true,
          node: (
            <Card
              tone={mode === "none" ? "danger" : "warn"}
              icon={mode === "none" ? <OfflineIcon /> : <AlertIcon />}
              title={copy.title}
              {...(mode === "none" ? {} : { actions })}
              footer={
                <>
                  {mode === "none" && props.operatorMode && (
                    <OperatorPanel
                      sim={sim}
                      operator={props.operator}
                      camera={camera}
                    />
                  )}
                  <ErrorDetails
                    error={error}
                    camera={camera}
                    stream={stream}
                    at={phase.at}
                  />
                </>
              }
            >
              <p>
                {copy.detail}
                {!phase.exhausted && mode === "auto"
                  ? " Automatic retries stopped."
                  : ""}
              </p>
              {mode === "none" && (
                <Alternatives
                  items={props.alternatives}
                  onSelect={props.onSelect}
                  onChoose={props.onChooseCamera}
                />
              )}
            </Card>
          ),
        };
      }
    }
  }

  return (
    <section
      ref={stageRef}
      class="stage"
      aria-label={`${camera.name} player`}
      aria-busy={phase.kind === "connecting" || phase.kind === "reconnecting"}
      data-phase={phase.kind}
      data-idle={idle ? "" : undefined}
      data-fs={fullscreen ? "" : undefined}
      data-immersive={props.immersive || fullscreen ? "" : undefined}
      data-scrim={message?.scrim ? "" : undefined}
      data-card={message?.card ? "" : undefined}
      data-fallback={fallback ? "" : undefined}
      onPointerMove={(e) => {
        if (e.pointerType === "mouse") wake();
      }}
      onPointerLeave={(e) => {
        if (e.pointerType !== "mouse" || !playing) return;
        clearTimeout(idleTimer.current);
        setIdle(true);
      }}
      onPointerUp={(e) => {
        if (e.pointerType === "mouse" || isControl(e.target)) return;
        if (idle || !playing) wake();
        else {
          clearTimeout(idleTimer.current);
          setIdle(true);
        }
      }}
      onDblClick={(e) => {
        if (!isControl(e.target)) toggleFullscreenCommand();
      }}
      onFocusIn={wake}
    >
      <canvas ref={canvasRef} class="feed-canvas" aria-hidden="true" />
      <video
        ref={videoRef}
        class="feed-video"
        muted={muted}
        playsInline
        aria-label={`${camera.name} video`}
      />
      <div class="ov">
        <div class="ov-top">
          <span class="badge" data-tone={state.tone}>
            <span class="dot" aria-hidden="true" />
            {state.label}
          </span>
          <span class="ov-name ov-auto">
            {camera.name}
            {camera.streams.length > 1 ? ` · ${stream.name}` : ""}
          </span>
          <button
            type="button"
            class="glass-btn ov-cams ov-auto"
            onClick={props.onOpenCameras}
          >
            <GridIcon size={18} />
            Cameras
          </button>
        </div>
        <div class="ov-center">{message?.node}</div>
        <p class="ov-hint" role="status">
          {hint}
        </p>
        <div class="ov-bottom ov-auto">
          <Control
            id="play"
            label={playing ? "Pause" : "Play"}
            keys="Space"
            reason={playReason}
            onPress={playPause}
          >
            {playing ? <PauseIcon /> : <PlayIcon />}
          </Control>
          <Control id="stop" label="Stop" keys="S" reason={stopReason} onPress={stop}>
            <StopIcon />
          </Control>
          <Control
            id="mute"
            label={muted ? "Unmute" : "Mute"}
            keys="M"
            reason={muteReason}
            onPress={mute}
          >
            {muted ? <MutedIcon /> : <VolumeIcon />}
          </Control>
          <span class="ctl-gap" />
          <Control
            id="pip"
            label={pip ? "Exit picture in picture" : "Picture in picture"}
            keys="P"
            reason={pip ? null : pipReason}
            onPress={togglePipCommand}
          >
            <PipIcon />
          </Control>
          <Control
            id="fullscreen"
            label={fullscreen ? "Exit fullscreen" : "Fullscreen"}
            keys="F"
            reason={fullscreenReason}
            onPress={toggleFullscreenCommand}
          >
            {fullscreen ? <ExitFullscreenIcon /> : <FullscreenIcon />}
          </Control>
        </div>
      </div>
      <p class="sr-only" role="status">
        {announce}
      </p>
    </section>
  );
}

// Unavailable controls stay focusable (aria-disabled) and explain why on
// hover, focus or tap instead of silently doing nothing.
function Control(props: {
  id: string;
  label: string;
  keys: string;
  reason: string | null;
  onPress: () => void;
  children: ComponentChildren;
}) {
  const reasonId = `pt-reason-${props.id}`;
  return (
    <>
      <button
        type="button"
        class="ctl"
        aria-label={props.label}
        aria-keyshortcuts={props.keys}
        aria-disabled={props.reason ? "true" : undefined}
        aria-describedby={props.reason ? reasonId : undefined}
        data-tip={props.reason ?? `${props.label} (${props.keys})`}
        onClick={props.onPress}
      >
        {props.children}
      </button>
      {props.reason && (
        <span id={reasonId} class="sr-only">
          {props.reason}
        </span>
      )}
    </>
  );
}

function Card(props: {
  tone: "info" | "warn" | "danger";
  icon: ComponentChildren;
  title: string;
  children?: ComponentChildren;
  actions?: ComponentChildren;
  footer?: ComponentChildren;
}) {
  return (
    <div class="card" data-tone={props.tone}>
      <div class="card-head">
        <span class="card-icon" aria-hidden="true">
          {props.icon}
        </span>
        <div class="card-body">
          <h2 class="card-title">{props.title}</h2>
          {props.children}
        </div>
      </div>
      {props.actions ? <div class="card-actions">{props.actions}</div> : null}
      {props.footer}
    </div>
  );
}

function ErrorDetails(props: {
  error: ErrorBody;
  camera: CameraSummary;
  stream: StreamSummary;
  at: number;
}) {
  const [open, setOpen] = useState(false);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const time = new Date(props.at);
  const summary = `camstream · ${props.error.code} · ${props.camera.id}/${props.stream.id} · ${time.toISOString()}`;
  useEffect(() => setCopyState("idle"), [summary]);
  async function copyDetails() {
    try {
      if (!navigator.clipboard) throw new Error("Clipboard unavailable");
      await navigator.clipboard.writeText(summary);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    }
  }
  return (
    <div class="card-details">
      <button
        type="button"
        class="link-btn"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {open ? "Hide details" : "Details"}
      </button>
      {open && (
        <div class="details">
          <dl>
            <dt>Code</dt>
            <dd>
              <code>{props.error.code}</code>
            </dd>
            <dt>Camera</dt>
            <dd>
              <code>{props.camera.id}</code> · <code>{props.stream.id}</code>
            </dd>
            <dt>Time</dt>
            <dd>{time.toLocaleTimeString()}</dd>
            <dt>Retry</dt>
            <dd>{retryNote(props.error)}</dd>
          </dl>
          <button
            type="button"
            class="link-btn"
            onClick={() => { void copyDetails(); }}
          >
            <CopyIcon size={14} />
            {copyState === "copied" ? "Copied" : "Copy details"}
          </button>
          {copyState === "failed" && (
            <p role="status">Could not copy. Select the details above to copy them manually.</p>
          )}
        </div>
      )}
    </div>
  );
}

function Alternatives(props: {
  items: readonly Alternative[];
  onSelect: (ref: StreamRef) => void;
  onChoose: () => void;
}) {
  return (
    <div class="alts">
      {props.items.length > 0 && (
        <>
          <p class="alts-label">Watch another camera</p>
          <div class="alts-list">
            {props.items.map(({ camera, status }) => {
              const first = camera.streams[0];
              if (!first) return null;
              return (
                <button
                  key={camera.id}
                  type="button"
                  class="btn alt-btn"
                  onClick={() =>
                    props.onSelect({ camera_id: camera.id, stream_id: first.id })
                  }
                >
                  <span class="dot" data-tone={status.tone} aria-hidden="true" />
                  <span class="alt-name">{camera.name}</span>
                  <span class="alt-status">{status.label}</span>
                </button>
              );
            })}
          </div>
        </>
      )}
      <button type="button" class="btn alts-more" onClick={props.onChoose}>
        Choose another camera
      </button>
    </div>
  );
}

const checkLabels = { source: "Source", backend: "Video service", media: "Media" };
const checkStates = { passed: "Passed", failed: "Failed", unknown: "Not checked" };

function OperatorPanel(props: {
  sim: Sim;
  operator: Operator;
  camera: CameraSummary;
}) {
  const { sim, operator, camera } = props;
  const [confirming, setConfirming] = useState(false);
  if (operator.kind === "busy")
    return (
      <div class="op">
        <p class="op-status">
          <span class="spinner spinner-sm" aria-hidden="true" />
          {operator.action === "diagnose"
            ? "Running diagnostics…"
            : `Reconnecting ${camera.name}'s source…`}
        </p>
      </div>
    );
  return (
    <div class="op">
      <p class="op-label">Operator</p>
      {operator.kind === "diagnosed" && (
        <ul class="checks">
          {operator.checks.map((check) => (
            <li key={check.name} data-state={check.state}>
              {check.state === "passed" ? (
                <CheckIcon size={16} />
              ) : check.state === "failed" ? (
                <AlertIcon size={16} />
              ) : (
                <MinusIcon size={16} />
              )}
              <span>{checkLabels[check.name]}</span>
              <span class="check-state">{checkStates[check.state]}</span>
            </li>
          ))}
        </ul>
      )}
      {confirming ? (
        <div class="op-confirm" role="group" aria-label="Confirm source reconnect">
          <p>
            Reconnect {camera.name}'s source? Everyone watching {camera.name} is
            disconnected for a moment.
          </p>
          <div class="card-actions">
            <button
              type="button"
              class="btn btn-primary"
              onClick={() => {
                setConfirming(false);
                sim.reconnectSource();
              }}
            >
              Reconnect source
            </button>
            <button type="button" class="btn" onClick={() => setConfirming(false)}>
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <div class="card-actions">
          <button type="button" class="btn" onClick={() => sim.diagnose()}>
            Run diagnostics
          </button>
          <button type="button" class="btn" onClick={() => setConfirming(true)}>
            Reconnect source…
          </button>
        </div>
      )}
    </div>
  );
}

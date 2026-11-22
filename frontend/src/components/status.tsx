import { useEffect, useRef, useState } from "preact/hooks";
import type { ErrorBody, ErrorCode, State } from "../services/contracts";
import { stateLabels } from "../services/copy";

export type Tone = "live" | "busy" | "danger" | "idle";
export interface ViewState {
  label: string;
  tone: Tone;
}

const stateTones: Record<State, Tone> = {
  unknown: "idle",
  idle: "idle",
  connecting: "busy",
  playing: "live",
  reconnecting: "busy",
  closing: "idle",
  closed: "idle",
  offline: "danger",
  failed: "danger",
};

// One description of the player's state, shared by the badge, the document
// title and the favicon so they never disagree.
/**
 * A paused video that Play can resume. A paused element fires no media events,
 * so a reconnect that recovers while paused leaves the state at "reconnecting";
 * Play then restores "playing" through the element's playing event.
 */
export function resumable(view: {
  state: State;
  error?: ErrorBody | undefined;
  paused?: boolean | undefined;
  reloadRequired?: boolean | undefined;
}) {
  return Boolean(
    view.paused &&
      !view.error &&
      !view.reloadRequired &&
      (view.state === "playing" || view.state === "reconnecting"),
  );
}

export function describeView(view: {
  state: State;
  error?: ErrorBody | undefined;
  paused?: boolean | undefined;
  needsPlay?: boolean | undefined;
  reloadRequired?: boolean | undefined;
  /** A lost connection that is tried again on its own is still recovering. */
  retryAt?: number | undefined;
}): ViewState {
  if (view.reloadRequired) return { label: "Reload needed", tone: "danger" };
  if (view.error)
    return view.retryAt === undefined
      ? { label: stateLabels.failed, tone: "danger" }
      : { label: stateLabels.reconnecting, tone: "busy" };
  if (view.needsPlay) return { label: "Ready to play", tone: "idle" };
  if (resumable(view)) return { label: "Paused", tone: "idle" };
  return { label: stateLabels[view.state], tone: stateTones[view.state] };
}

/** What a stream carries: "Video + audio", "Video only", "Audio only" or "No media". */
export const mediaLabel = ({ video, audio }: { video: boolean; audio: boolean }) =>
  video ? (audio ? "Video + audio" : "Video only") : audio ? "Audio only" : "No media";

export function StatusBadge({ view }: { view: ViewState }) {
  return (
    <span class={`status-badge tone-${view.tone}`}>
      <span class="status-dot" />
      {view.label}
    </span>
  );
}

/** Renders again every second while `active`, for ages and countdowns. */
export function useTick(active: boolean) {
  const [, setTick] = useState(0);
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(timer);
  }, [active]);
}

/** "4 s", "2 min" or "3 h". */
export function duration(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000));
  return s < 60
    ? `${s} s`
    : s < 3600
      ? `${Math.floor(s / 60)} min`
      : `${Math.floor(s / 3600)} h`;
}

/**
 * The safe code, where it happened and when it was first shown, so a report
 * names what an operator needs. Never the server's message.
 */
export function ErrorDetails({
  code,
  where,
}: {
  code: ErrorCode;
  where: readonly string[];
}) {
  const seen = useRef<{ code: ErrorCode; at: Date } | undefined>(undefined);
  if (seen.current?.code !== code) seen.current = { code, at: new Date() };
  const { at } = seen.current;
  return (
    <p class="error-details">
      <code>{code}</code>
      {where.map((part) => ` · ${part}`).join("")} ·{" "}
      <time dateTime={at.toISOString()}>{at.toLocaleTimeString()}</time>
    </p>
  );
}

// Proposed plain-language copy. Fixed and allowlisted: server messages, SDP
// and URLs are never shown. Error codes appear only under Details.
import type { ErrorBody } from "../services/contracts";
import { MAX_RETRIES, retryMode, type CameraLive, type Phase } from "./sim";

export type Tone = "live" | "warn" | "danger" | "idle" | "unknown";
export interface Describe {
  /** Shown in the badge and camera list; may carry a countdown. */
  label: string;
  /** Stable word for the document title and announcements. */
  word: string;
  tone: Tone;
}

export const secs = (ms: number) => Math.max(0, Math.ceil(ms / 1000));

export function describePhase(
  phase: Phase,
  paused: boolean,
  now: number,
): Describe {
  switch (phase.kind) {
    case "none":
      return { label: "Starting", word: "Starting", tone: "idle" };
    case "connecting":
      return phase.attempt
        ? {
            label: `Retrying · ${phase.attempt} of ${MAX_RETRIES}`,
            word: "Retrying",
            tone: "warn",
          }
        : { label: "Connecting…", word: "Connecting", tone: "warn" };
    case "live":
      return paused
        ? { label: "Paused", word: "Paused", tone: "idle" }
        : { label: "Live", word: "Live", tone: "live" };
    case "blocked":
      return { label: "Tap to play", word: "Ready", tone: "idle" };
    case "reconnecting":
      return {
        label: `Reconnecting · ${secs(phase.until - now)} s`,
        word: "Reconnecting",
        tone: "warn",
      };
    case "retrying":
      return {
        label:
          phase.held === null
            ? `Retrying in ${secs(phase.at - now)} s`
            : "Retry on hold",
        word: "Retrying",
        tone: "warn",
      };
    case "failed": {
      const word = failedWord(phase.error, phase.exhausted);
      return { label: word, word, tone: "danger" };
    }
    case "stopped":
      return { label: "Stopped", word: "Stopped", tone: "idle" };
    case "reload":
      return { label: "Reload needed", word: "Reload needed", tone: "danger" };
  }
}

function failedWord(error: ErrorBody, exhausted: boolean) {
  if (exhausted) return "Can't connect";
  switch (error.code) {
    case "source_unavailable":
    case "camera_unreachable":
      return "Offline";
    case "deadline_exceeded":
      return "Timed out";
    case "camera_busy":
      return "Busy";
    case "capacity_exceeded":
      return "Viewer limit";
    case "backend_unavailable":
    case "server_unavailable":
      return "Service down";
    case "media_connection_failed":
    case "signaling_failed":
      return "Can't connect";
    default:
      return "Unavailable";
  }
}

/** Other cameras in the list. `today` mimics the server's current "unknown". */
export function describeCamera(
  live: CameraLive | undefined,
  today: boolean,
): Describe {
  if (today || !live || live.observedAt === null)
    return { label: "Status unknown", word: "Unknown", tone: "unknown" };
  if (live.fault)
    return { label: "Not sending video", word: "Offline", tone: "danger" };
  if (live.viewers > 0)
    return {
      label: `Live · ${live.viewers} watching`,
      word: "Live",
      tone: "live",
    };
  return { label: "Idle", word: "Idle", tone: "idle" };
}

export function errorMessage(
  error: ErrorBody,
  camera: string,
  otherStream: string | null,
): { title: string; detail: string } {
  switch (error.code) {
    case "source_unavailable":
      return {
        title: `${camera} isn't sending video`,
        detail:
          "The camera source stopped and needs an operator to reconnect it. Trying again from here won't fix it.",
      };
    case "camera_unreachable":
      return {
        title: `Can't reach ${camera}`,
        detail:
          "The camera isn't responding on the network. Other cameras may still work.",
      };
    case "deadline_exceeded":
      return {
        title: `${camera} didn't respond in time`,
        detail:
          "The connection timed out. The camera may still be starting up, so try again in a moment.",
      };
    case "camera_busy":
      return otherStream
        ? {
            title: `${camera} is busy`,
            detail: `This camera streams one view at a time and ${otherStream} is in use. Watch ${otherStream} instead, or try again later.`,
          }
        : {
            title: `${camera} is busy`,
            detail: "The camera is in use right now. Try again in a minute.",
          };
    case "capacity_exceeded":
      return {
        title: "Too many viewers right now",
        detail:
          "camstream has reached its viewer limit. Try again in a minute, or close a viewer you don't need.",
      };
    case "media_connection_failed":
      return { title: "Connection lost", detail: "Video stopped arriving." };
    case "signaling_failed":
      return {
        title: "Couldn't start the connection",
        detail: "This browser and camstream couldn't agree on how to connect.",
      };
    case "backend_unavailable":
      return {
        title: "Video service is down",
        detail: "camstream's video service isn't ready.",
      };
    case "server_unavailable":
      return {
        title: "Can't reach camstream",
        detail: "Check that this device is on your private network.",
      };
    case "unsupported_browser":
      return {
        title: "This browser can't play the stream",
        detail: "Try a current version of Safari or Chrome.",
      };
    default:
      return {
        title: "Video unavailable",
        detail:
          "Something went wrong in camstream. If it keeps happening, tell the person who runs it.",
      };
  }
}

export function retryNote(error: ErrorBody) {
  switch (retryMode(error)) {
    case "auto":
      return "Automatic, with backoff";
    case "manual":
      return error.code === "deadline_exceeded"
        ? "Manual only: the request may still be in progress"
        : "Manual";
    case "none":
      return error.code === "source_unavailable"
        ? "Not from here: an operator must reconnect the source"
        : "Not available";
  }
}

export function freshness(observedAt: number | null, now: number) {
  if (observedAt === null) return { text: "No status yet", stale: false };
  const age = Math.max(0, now - observedAt);
  if (age > 10_000)
    return {
      text: `No status update for ${Math.round(age / 1000)} s`,
      stale: true,
    };
  return {
    text:
      age < 3000 ? "Updated just now" : `Updated ${Math.round(age / 1000)} s ago`,
    stale: false,
  };
}

const TONE_COLORS: Record<Tone, string> = {
  live: "#1e9e5a",
  warn: "#d4900f",
  danger: "#d23f3f",
  idle: "#8b969e",
  unknown: "#8b969e",
};
/** Favicon with a state dot. data: URIs are allowed by the production CSP. */
export function faviconHref(tone: Tone | null) {
  const dot = tone
    ? `<circle cx='25' cy='8' r='6' fill='${TONE_COLORS[tone]}' stroke='white' stroke-width='2'/>`
    : "";
  const svg = `<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'><rect x='1' y='8' width='21' height='17' rx='5' fill='#176b51'/><path d='M22 14.5 30 10v13l-8-4.5z' fill='#176b51'/><circle cx='11.5' cy='16.5' r='4' fill='white'/>${dot}</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

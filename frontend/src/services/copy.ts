import type { ErrorCode, State } from "./contracts";

// Reviewed copy for each state and error code. Their keys are also the states
// and codes that the API client accepts from the server, so keep each table
// complete: a state or code without an entry is rejected.
export const stateLabels: Record<State, string> = {
  unknown: "Status unknown",
  idle: "Ready",
  connecting: "Connecting",
  playing: "Live",
  reconnecting: "Reconnecting",
  closing: "Stopping",
  closed: "Stopped",
  offline: "Offline",
  failed: "Unavailable",
};

// Deliberately ignore raw error messages. Only reviewed, operator-safe copy reaches the page.
export const errorCopy: Record<ErrorCode, { title: string; detail: string }> = {
  invalid_argument: {
    title: "Couldn't open this stream",
    detail: "Choose the stream again.",
  },
  not_found: {
    title: "This stream isn't available",
    detail:
      "It may have been removed, or you may no longer have access. Choose another camera or stream.",
  },
  conflict: {
    title: "This stream can't start right now",
    detail:
      "It conflicts with how this camera is set up or already in use. Choose another stream or camera.",
  },
  authentication_failed: {
    title: "The camera rejected its sign-in",
    detail:
      "The person who runs camstream needs to check this camera's credentials.",
  },
  camera_unreachable: {
    title: "Can't reach this camera",
    detail: "The camera isn't responding. Other cameras may still work.",
  },
  stream_unavailable: {
    title: "This stream isn't available",
    detail: "Choose another stream or camera.",
  },
  backend_unavailable: {
    title: "Video service isn't ready",
    detail: "camstream's video service isn't running yet. Try again in a moment.",
  },
  source_unavailable: {
    title: "This camera isn't sending video",
    detail:
      "The person who runs camstream needs to reconnect it. Other cameras may still work.",
  },
  signaling_failed: {
    title: "Couldn't start the connection",
    detail: "This browser and camstream couldn't set up video. Try again.",
  },
  media_connection_failed: {
    title: "Connection lost",
    detail: "Video stopped arriving. Check your connection, then try again.",
  },
  unsupported_capability: {
    title: "This stream can't play that way",
    detail: "The stream doesn't support the requested playback option.",
  },
  unsupported_browser: {
    title: "This browser can't play the stream",
    detail: "Try a current version of Safari or Chrome.",
  },
  capacity_exceeded: {
    title: "Too many viewers right now",
    detail: "Every viewer slot is in use. Try again when someone stops watching.",
  },
  camera_busy: {
    title: "This camera is busy",
    detail:
      "It’s being changed, or a different stream of it is playing. Try again in a moment, or switch to that stream.",
  },
  cleanup_incomplete: {
    title: "The previous connection is still closing",
    detail:
      "Wait a moment, then try again. If it keeps happening, tell the person who runs camstream.",
  },
  controller_conflict: {
    title: "Video service isn't available",
    detail: "The person who runs camstream needs to check the video service.",
  },
  server_unavailable: {
    title: "Can't reach camstream",
    detail: "Check that this device is on your private network, then try again.",
  },
  access_denied: {
    title: "You don't have access",
    detail: "Ask the person who runs camstream to give you access.",
  },
  csrf_invalid: {
    title: "Your session needs refreshing",
    detail: "Try again. If it keeps happening, reload the page.",
  },
  canceled: {
    title: "Connection canceled",
    detail: "Video didn't start. Try again.",
  },
  deadline_exceeded: {
    title: "The camera didn't respond in time",
    detail:
      "The connection timed out. The camera may still be starting, so try again in a moment.",
  },
  internal: {
    title: "Video unavailable",
    detail:
      "Something went wrong in camstream. If it keeps happening, tell the person who runs it.",
  },
};

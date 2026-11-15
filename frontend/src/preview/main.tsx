import { render } from "preact";
import { useState } from "preact/hooks";
import { App, type ViewerInput } from "../app";
import type {
  CameraList,
  ErrorBody,
  State,
  StreamRef,
} from "../services/contracts";
import type { PlayerControls } from "../components/player";
import samples from "../../../data/api/examples.json";
import "../styles.css";
import "./preview.css";

// Contract fixtures, extended only with synthetic presentation examples. No media or API I/O.
export const catalog: CameraList = {
  cameras: [
    ...(samples.catalog.cameras as CameraList["cameras"]),
    {
      id: "livingroom",
      name: "Living room",
      availability: "idle",
      streams: [
        {
          id: "main",
          name: "Main",
          capabilities: { video: true, audio: true },
        },
        {
          id: "compact",
          name: "Compact",
          capabilities: { video: true, audio: false },
        },
      ],
    },
    {
      id: "hall",
      name: "Hall",
      availability: "offline",
      streams: [
        {
          id: "default",
          name: "Default",
          capabilities: { video: true, audio: false },
        },
      ],
    },
    {
      id: "front-porch",
      name: "Front porch",
      availability: "unknown",
      streams: [
        {
          id: "default",
          name: "Default",
          capabilities: { video: true, audio: true },
        },
      ],
    },
  ],
};
const scenarios = [
  "connecting",
  "playing",
  "paused",
  "reconnecting",
  "idle",
  "unknown",
  "offline",
  "denied",
  "empty",
  "loading",
  "backend",
  "signaling",
  "media",
  "busy",
  "capacity",
  "unsupported",
  "no-streams",
] as const;
type Scenario = (typeof scenarios)[number];
const failures: Partial<Record<Scenario, ErrorBody>> = {
  offline: samples.offline_status.streams[0]!.error as ErrorBody,
  backend: { code: "backend_unavailable", message: "", retryable: true },
  signaling: { code: "signaling_failed", message: "", retryable: true },
  media: { code: "media_connection_failed", message: "", retryable: true },
  busy: { code: "camera_busy", message: "", retryable: true },
  capacity: { code: "capacity_exceeded", message: "", retryable: true },
};

function Preview() {
  const [scenario, setScenario] = useState<Scenario>("connecting");
  const [selection, setSelection] = useState<StreamRef>({
    camera_id: "room1",
    stream_id: "default",
  });
  const [event, setEvent] = useState("No action yet");
  const [attempt, setAttempt] = useState(0);
  const error =
    failures[scenario] ??
    (selection.camera_id === "hall" ? failures.offline : undefined);
  const state: State = error
    ? "failed"
    : ["playing", "paused", "unsupported"].includes(scenario)
      ? "playing"
      : ["idle", "unknown", "reconnecting"].includes(scenario)
        ? (scenario as State)
        : "connecting";
  const controls: PlayerControls = {
    audio: { enabled: true },
    fullscreen: { enabled: true },
    pip:
      scenario === "unsupported"
        ? {
            enabled: false,
            reason: "Picture in picture is not supported by this browser.",
          }
        : { enabled: true },
  };
  const viewer: ViewerInput = {
    selection,
    status: {
      stream_id: selection.stream_id,
      state,
      viewers: state === "playing" ? 2 : null,
      observed_at: state === "playing" ? "2026-11-14T14:30:00Z" : null,
    },
    presentation: {
      state,
      paused: scenario === "paused",
      ...(error ? { error } : {}),
      controls,
      actions: {
        onRetry: () => {
          setAttempt(attempt + 1);
          setEvent(`Viewer retry callback ${attempt + 1}; no network request`);
        },
        onMute: (muted) =>
          setEvent(muted ? "Mute callback" : "Unmute callback"),
        onFullscreen: () => setEvent("Fullscreen callback; preview only"),
        onPictureInPicture: () =>
          setEvent("Picture in picture callback; preview only"),
      },
    },
  };
  return (
    <>
      <div class="preview-bar">
        <strong>Presentation preview</strong>
        <label for="scenario">State</label>
        <select
          id="scenario"
          value={scenario}
          onChange={(e) => {
            setScenario(e.currentTarget.value as Scenario);
            setEvent("No action yet");
          }}
        >
          {scenarios.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
        <p>
          Synthetic inputs. No camera connection, media, or browser capability
          validation.
        </p>
        <span class="preview-event" role="status">
          {event}
        </span>
      </div>
      <App
        catalog={
          scenario === "empty"
            ? { cameras: [] }
            : scenario === "no-streams"
              ? {
                  cameras: [
                    {
                      id: "unavailable",
                      name: "Unconfigured camera",
                      availability: "unknown",
                      streams: [],
                    },
                  ],
                }
              : catalog
        }
        loading={scenario === "loading"}
        {...(scenario === "denied"
          ? { error: samples.denied.error as ErrorBody }
          : {})}
        viewer={viewer}
        onSelect={(next) => {
          if (next) setSelection(next);
          setEvent("Selection changed; presentation reset");
        }}
      />
    </>
  );
}
render(<Preview />, document.getElementById("app")!);

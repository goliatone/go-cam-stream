import { render } from "preact";
import { useEffect, useState } from "preact/hooks";
import { App, type ViewerInput } from "../app";
import type {
  CameraList,
  ErrorBody,
  State,
  StreamRef,
} from "../services/contracts";
import type { PlayerControls } from "../components/player";
import type { ManagementActions, ManagementView } from "../components/camera-management";
import { CameraRecovery } from "../components/camera-recovery";
import type { DraftReview, TestResult } from "../services/contracts";
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
      // A latched fault, which the server lists as failed.
      availability: "failed",
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
  "retrying",
  "stale",
  "stopping",
  "stopped",
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
  retrying: { code: "media_connection_failed", message: "", retryable: true },
  busy: { code: "camera_busy", message: "", retryable: true },
  capacity: { code: "capacity_exceeded", message: "", retryable: true },
};

// A manager's view: the entry points, each management dialog state and the
// notices. No draft, test, save or refresh reaches a server.
const draft: DraftReview = {
  id: "draft",
  candidate_id: "office",
  revision: 1,
  runtime_epoch: "preview",
  expires_at: "2026-11-14T15:00:00Z",
  name: "Office",
  endpoint: "rtsp://localhost:18556/macbook",
  authentication: "credentials",
  credentials_present: true,
  warnings: ["server_relative_localhost", "creator_only_access"],
  state: "ready",
};
const running: TestResult = {
  id: "test",
  attempt_key: "attempt",
  draft_revision: 1,
  stage: "source_media",
  expires_at: draft.expires_at,
  source_media_observed: true,
  browser_frames_reported: false,
  cleanup: "pending",
};
const passed: TestResult = {
  ...running,
  stage: "passed",
  browser_frames_reported: true,
  cleanup: "confirmed",
};
const failed: TestResult = {
  ...running,
  stage: "failed",
  source_media_observed: false,
  cleanup: "confirmed",
  error: { code: "source_unavailable", message: "", retryable: true },
};
const managing = {
  viewer: undefined,
  manager: undefined,
  "add-camera": { phase: "editing" },
  "import-camera": { phase: "editing" },
  review: { phase: "review", draft },
  testing: {
    phase: "testing",
    draft: { ...draft, state: "testing" },
    test: running,
  },
  "check-failed": { phase: "review", draft, test: failed },
  tested: {
    phase: "review",
    draft: { ...draft, state: "tested", test: passed, dial_address: "127.0.0.1:18556" },
    test: passed,
  },
  saved: {
    phase: "saved",
    saved: {
      camera_id: "office",
      catalog_revision: { runtime_epoch: "preview", generation: 2 },
    },
  },
  "camera-settings": {
    phase: "editing",
    credentialTarget: { id: "room1", name: "Room 1" },
  },
  "checking-save": {
    phase: "recovering",
    busy: true,
    unresolved: true,
    checkingSave: true,
  },
  unresolved: {
    phase: "recovering",
    draft,
    unresolved: true,
    error: "save_outcome_unknown",
    canRetrySave: true,
    canAbandon: true,
  },
  "stale-list": undefined,
  reconnect: undefined,
} satisfies Record<string, Omit<ManagementView, "storageReady"> | undefined>;
type Managing = keyof typeof managing;
/** A manager may change each listed camera's credentials. */
const canManage = (list: CameraList, manager: boolean): CameraList =>
  manager
    ? {
        cameras: list.cameras.map((camera) => ({
          ...camera,
          can_update_credentials: true,
        })),
      }
    : list;

/** Scenarios whose synthetic status keeps arriving every 2 s. */
const observed: readonly Scenario[] = [
  "playing",
  "paused",
  "unsupported",
  "reconnecting",
];

function Preview() {
  const [scenario, setScenario] = useState<Scenario>("connecting");
  // When the scenario or view began and when its synthetic status last
  // arrived, in page-local performance.now() time as the controller uses.
  const [since, setSince] = useState(() => performance.now());
  const [polled, setPolled] = useState(() => performance.now());
  const [selection, setSelection] = useState<StreamRef>({
    camera_id: "room1",
    stream_id: "default",
  });
  const [event, setEvent] = useState("No action yet");
  const [attempt, setAttempt] = useState(0);
  const [manage, setManage] = useState<Managing>("viewer");
  const dialog = managing[manage];
  const back = (what: string) => () => {
    setManage("manager");
    setEvent(`${what} callback; preview only`);
  };
  const managementActions: ManagementActions = {
    submit: () => setEvent("Review callback; no draft is created"),
    importFiles: () => setEvent("Import callback; no file is read"),
    test: () => setEvent("Test callback; no media"),
    cancelTest: () => setEvent("Cancel test callback; preview only"),
    save: () => setEvent("Save callback; nothing is saved"),
    recover: () => setEvent("Check again callback; no request"),
    abandon: back("Abandon"),
    retrySave: () => setEvent("Retry callback; nothing is saved"),
    cancel: back("Cancel"),
    close: back("Close"),
    watch: back("Watch"),
    submitCredentials: () => setEvent("Credentials callback; nothing is sent"),
    openSettings: () => setManage("camera-settings"),
    reset: () => setManage("add-camera"),
  };
  function show(next: Scenario) {
    setScenario(next);
    setSince(performance.now());
    setPolled(performance.now());
  }
  const live = observed.includes(scenario);
  useEffect(() => {
    if (!live) return;
    const timer = setInterval(() => setPolled(performance.now()), 2000);
    return () => clearInterval(timer);
  }, [live]);
  // As a real connection that stays down: the recovery window ends in a lost
  // connection, which is tried again on its own 3 s later.
  useEffect(() => {
    const next =
      scenario === "reconnecting"
        ? (["retrying", 15000, "Recovery window ended"] as const)
        : scenario === "retrying"
          ? (["connecting", 3000, "Automatic retry"] as const)
          : undefined;
    if (!next) return;
    const timer = setTimeout(() => {
      show(next[0]);
      setEvent(`${next[2]}; preview only`);
    }, next[1]);
    return () => clearTimeout(timer);
  }, [scenario, since]);
  const error =
    failures[scenario] ??
    (selection.camera_id === "hall" ? failures.offline : undefined);
  const state: State = error
    ? "failed"
    : ["playing", "paused", "unsupported", "stale"].includes(scenario)
      ? "playing"
      : scenario === "stopping"
        ? "closing"
        : scenario === "stopped"
          ? "closed"
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
      observed_at: live || state === "playing" ? "2026-11-14T14:30:00Z" : null,
    },
    // Synthetic times: live scenarios answer every 2 s; "stale" stopped 25 s
    // before it was chosen.
    ...(live
      ? { statusAt: polled, observingSince: since }
      : scenario === "stale"
        ? { statusAt: since - 25000, observingSince: since - 40000 }
        : {}),
    presentation: {
      state,
      paused: scenario === "paused",
      ...(scenario === "reconnecting" ? { recoverBy: since + 15000 } : {}),
      ...(scenario === "retrying" ? { retryAt: since + 3000 } : {}),
      ...(error ? { error } : {}),
      controls,
      actions: {
        onRetry: () => {
          setAttempt(attempt + 1);
          setEvent(`Viewer retry callback ${attempt + 1}; no network request`);
          if (scenario === "stopped" || scenario === "retrying")
            show("connecting");
        },
        onPlay: () => {
          show("playing");
          setEvent("Play callback; preview only");
        },
        onPause: () => {
          show("paused");
          setEvent("Pause callback; preview only");
        },
        onStop: () => {
          show("stopped");
          setEvent("Stop callback; no network request");
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
            show(e.currentTarget.value as Scenario);
            setEvent("No action yet");
          }}
        >
          {scenarios.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
        <label for="managing">Managing</label>
        <select
          id="managing"
          value={manage}
          onChange={(e) => {
            setManage(e.currentTarget.value as Managing);
            setEvent("No action yet");
          }}
        >
          {Object.keys(managing).map((value) => (
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
        catalog={canManage(
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
              : catalog,
          manage !== "viewer",
        )}
        loading={scenario === "loading"}
        {...(scenario === "denied"
          ? { error: samples.denied.error as ErrorBody }
          : {})}
        {...(manage === "viewer"
          ? {}
          : {
              onAddCamera: (mode) =>
                setManage(mode === "import" ? "import-camera" : "add-camera"),
              onCameraSettings: () => setManage("camera-settings"),
            })}
        {...(dialog
          ? {
              management: {
                view: { storageReady: true, ...dialog },
                actions: managementActions,
                mode: manage === "import-camera" ? "import" : "manual",
              },
            }
          : {})}
        {...(manage === "stale-list"
          ? {
              catalogStale: true,
              refreshCatalog: async () =>
                setEvent("Refresh callback; no request"),
            }
          : {})}
        {...(manage === "reconnect"
          ? {
              recoveryControl: (
                <CameraRecovery
                  busy={false}
                  unresolved={false}
                  disabled={false}
                  onConfirm={() => setEvent("Reconnect callback; preview only")}
                  onCheck={() => setEvent("Check reconnect callback")}
                />
              ),
            }
          : {})}
        viewer={viewer}
        onSelect={(next) => {
          if (next) setSelection(next);
          // A new view starts its own status and recovery times.
          show(scenario);
          setEvent("Selection changed; presentation reset");
        }}
      />
    </>
  );
}
render(<Preview />, document.getElementById("app")!);

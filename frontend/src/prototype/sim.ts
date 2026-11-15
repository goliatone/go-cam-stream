// Dev-only simulated viewer for the UI prototype. No network requests, no
// browser storage. It mirrors the real controller's rules where the proposal
// depends on them: one active player per page, a single owner of retries,
// latched source faults, and no automatic retry after an uncertain create.
import type {
  CameraID,
  CameraList,
  CameraSummary,
  DiagnosticCheck,
  ErrorBody,
  ErrorCode,
  StreamRef,
} from "../services/contracts";

export type Transport = "udp" | "tcp";
export type CatalogMode = "normal" | "loading" | "empty" | "denied" | "down";
/** Outcome of the next simulated connection attempt. */
export type Outcome = "ok" | "timeout" | ErrorCode;

export type CatalogState =
  | { kind: "loading" }
  | { kind: "ready"; cameras: readonly CameraSummary[] }
  | { kind: "empty" }
  | { kind: "denied" }
  | { kind: "down"; attempt: number; at: number };

export type Phase =
  | { kind: "none" }
  | { kind: "connecting"; since: number; attempt: number }
  | { kind: "live"; since: number; transport: Transport }
  | { kind: "blocked"; transport: Transport }
  | { kind: "reconnecting"; until: number; transport: Transport }
  | {
      kind: "retrying";
      error: ErrorBody;
      attempt: number;
      delay: number;
      at: number;
      /** Remaining delay while the countdown is held (hidden tab). */
      held: number | null;
    }
  | { kind: "failed"; error: ErrorBody; exhausted: boolean; at: number }
  | { kind: "stopped" }
  | { kind: "reload" };

export type Operator =
  | { kind: "idle" }
  | { kind: "busy"; action: "diagnose" | "reconnect" }
  | { kind: "diagnosed"; checks: readonly DiagnosticCheck[] };

export interface CameraLive {
  viewers: number;
  /** null: never observed (no viewer has demanded it yet). */
  observedAt: number | null;
  fault: ErrorBody | null;
}

export interface SimOptions {
  catalog: CatalogMode;
  fast: boolean;
  keepFailing: boolean;
  statusFrozen: boolean;
  autoplayBlocked: boolean;
}

export interface Snapshot {
  catalog: CatalogState;
  selection: StreamRef | null;
  phase: Phase;
  cameras: Readonly<Record<CameraID, CameraLive>>;
  operator: Operator;
  options: SimOptions;
}

export interface SimConfig {
  list: CameraList;
  viewers?: Readonly<Record<CameraID, number>>;
  faults?: Readonly<Record<CameraID, ErrorBody>>;
  unobserved?: readonly CameraID[];
}

export interface SimDeps {
  now?: () => number;
  random?: () => number;
}

export const MAX_RETRIES = 6;
const BACKOFF_SECONDS = [2, 4, 8, 16, 30, 60] as const;
/** Real controller values where they exist (20 s deadline, 15 s window). */
export const TIMING = {
  catalog: 700,
  connect: 1200,
  fault: 700,
  deadline: 20_000,
  window: 15_000,
  recover: 4_000,
  poll: 2_000,
  diagnose: 900,
  reconnectSource: 1_600,
} as const;

// Proposal, pending the retry-policy decision: retry automatically only after
// a known failure. A timed-out create has an unknown outcome and is never
// retried automatically.
const AUTO_RETRY: ReadonlySet<ErrorCode> = new Set<ErrorCode>([
  "media_connection_failed",
  "signaling_failed",
  "backend_unavailable",
  "server_unavailable",
]);
const NOT_RETRYABLE: ReadonlySet<ErrorCode> = new Set<ErrorCode>([
  "source_unavailable",
  "access_denied",
  "authentication_failed",
  "not_found",
  "unsupported_browser",
  "unsupported_capability",
  "controller_conflict",
]);

export type RetryMode = "auto" | "manual" | "none";
export function retryMode(error: ErrorBody): RetryMode {
  if (!error.retryable) return "none";
  return AUTO_RETRY.has(error.code) ? "auto" : "manual";
}

/** Exponential backoff with ±20% jitter, capped at 60 s. */
export function backoff(attempt: number, random: () => number = Math.random) {
  const step = Math.min(Math.max(attempt, 1), BACKOFF_SECONDS.length) - 1;
  const base = (BACKOFF_SECONDS[step] ?? 60) * 1000;
  return Math.min(60_000, Math.round(base * (0.8 + 0.4 * random())));
}

/** Synthetic error body. The UI never renders `message`. */
export function failure(code: ErrorCode): ErrorBody {
  return { code, message: "", retryable: !NOT_RETRYABLE.has(code) };
}

export function parseLink(search: string): Partial<StreamRef> {
  const query = new URLSearchParams(search);
  const camera = query.get("camera");
  const stream = query.get("stream");
  return {
    ...(camera ? { camera_id: camera } : {}),
    ...(stream ? { stream_id: stream } : {}),
  };
}

/** Relative deep link. The stream is omitted when it is the camera's first. */
export function linkFor(camera: CameraSummary, streamId?: string) {
  const query = new URLSearchParams({ camera: camera.id });
  if (streamId && camera.streams[0]?.id !== streamId)
    query.set("stream", streamId);
  return `?${query.toString()}`;
}

type Timer = ReturnType<typeof setTimeout>;

export class Sim {
  private state: Snapshot;
  private readonly listeners = new Set<() => void>();
  private readonly base: Record<CameraID, number>;
  private readonly now: () => number;
  private readonly random: () => number;
  private wanted: Partial<StreamRef>;
  private forced: Outcome | null = null;
  private hidden = false;
  private pip = false;
  private phaseTimer: Timer | undefined;
  private catalogTimer: Timer | undefined;
  private operatorTimer: Timer | undefined;
  private poller: ReturnType<typeof setInterval> | undefined;

  constructor(
    private readonly config: SimConfig,
    wanted: Partial<StreamRef> = {},
    deps: SimDeps = {},
  ) {
    this.now = deps.now ?? (() => Date.now());
    this.random = deps.random ?? Math.random;
    this.wanted = wanted;
    const start = this.now();
    this.base = Object.fromEntries(
      config.list.cameras.map((c) => [c.id, config.viewers?.[c.id] ?? 0]),
    );
    this.state = {
      catalog: { kind: "loading" },
      selection: null,
      phase: { kind: "none" },
      cameras: Object.fromEntries(
        config.list.cameras.map((c): [CameraID, CameraLive] => [
          c.id,
          {
            viewers: this.base[c.id] ?? 0,
            observedAt: config.unobserved?.includes(c.id) ? null : start,
            fault: config.faults?.[c.id] ?? null,
          },
        ]),
      ),
      operator: { kind: "idle" },
      options: {
        catalog: "normal",
        fast: false,
        keepFailing: false,
        statusFrozen: false,
        autoplayBlocked: false,
      },
    };
  }

  get(): Snapshot {
    return this.state;
  }

  subscribe(listener: () => void) {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  start() {
    this.load();
    clearInterval(this.poller);
    this.poller = setInterval(() => this.poll(true), TIMING.poll);
  }

  dispose() {
    this.clearPhase();
    clearTimeout(this.catalogTimer);
    clearTimeout(this.operatorTimer);
    clearInterval(this.poller);
    this.listeners.clear();
  }

  /** Catalog request. Safe to repeat: it is a GET. */
  load() {
    this.clearPhase();
    clearTimeout(this.catalogTimer);
    clearTimeout(this.operatorTimer);
    this.set({
      catalog: { kind: "loading" },
      selection: null,
      phase: { kind: "none" },
      operator: { kind: "idle" },
    });
    this.catalogTimer = setTimeout(
      () => this.resolveCatalog(0),
      this.ms(TIMING.catalog),
    );
  }

  retryCatalog() {
    const catalog = this.state.catalog;
    if (catalog.kind !== "down") return;
    clearTimeout(this.catalogTimer);
    this.resolveCatalog(catalog.attempt);
  }

  select(ref: StreamRef) {
    const current = this.state.selection;
    const kind = this.state.phase.kind;
    if (
      current?.camera_id === ref.camera_id &&
      current.stream_id === ref.stream_id &&
      kind !== "failed" &&
      kind !== "stopped" &&
      kind !== "none"
    )
      return;
    this.wanted = ref;
    clearTimeout(this.operatorTimer);
    this.set({ selection: ref, operator: { kind: "idle" } });
    this.connect(0);
  }

  /** "Try now" keeps the attempt count; "Try again" starts over. */
  retry() {
    const phase = this.state.phase;
    if (phase.kind === "retrying") this.connect(phase.attempt);
    else if (
      phase.kind === "failed" ||
      phase.kind === "stopped" ||
      phase.kind === "none"
    )
      this.connect(0);
  }

  cancelRetry() {
    const phase = this.state.phase;
    if (phase.kind !== "retrying") return;
    this.clearPhase();
    this.set({
      phase: {
        kind: "failed",
        error: phase.error,
        exhausted: false,
        at: this.now(),
      },
    });
  }

  /** Releases the viewer slot. */
  stop() {
    const kind = this.state.phase.kind;
    if (
      kind === "none" ||
      kind === "stopped" ||
      kind === "failed" ||
      kind === "reload"
    )
      return;
    this.clearPhase();
    this.set({ phase: { kind: "stopped" } });
  }

  play() {
    const phase = this.state.phase;
    if (phase.kind === "blocked")
      this.set({
        phase: { kind: "live", since: this.now(), transport: phase.transport },
      });
    else if (phase.kind === "stopped") this.connect(0);
  }

  /** The media element rejected play(). */
  blocked() {
    const phase = this.state.phase;
    if (phase.kind === "live")
      this.set({ phase: { kind: "blocked", transport: phase.transport } });
  }

  dropConnection(recovers: boolean) {
    const phase = this.state.phase;
    if (phase.kind !== "live" && phase.kind !== "blocked") return;
    this.clearPhase();
    const transport = phase.transport;
    this.set({
      phase: {
        kind: "reconnecting",
        until: this.now() + this.ms(TIMING.window),
        transport,
      },
    });
    if (recovers)
      this.after(TIMING.recover, () =>
        this.set({ phase: { kind: "live", since: this.now(), transport } }),
      );
    else
      this.after(TIMING.window, () =>
        this.fail(failure("media_connection_failed"), 0),
      );
  }

  /** Sustained media failure: the viewer is released (a known failure). */
  loseMedia() {
    const kind = this.state.phase.kind;
    if (kind === "live" || kind === "blocked" || kind === "reconnecting")
      this.fail(failure("media_connection_failed"), 0);
  }

  /** Restart the current selection with a forced outcome. */
  force(outcome: Outcome) {
    if (!this.state.selection) return;
    this.forced = outcome;
    this.connect(0);
  }

  /** Session ownership lost (controller restart or access change). */
  expire() {
    if (!this.state.selection) return;
    this.clearPhase();
    clearTimeout(this.operatorTimer);
    this.set({ phase: { kind: "reload" }, operator: { kind: "idle" } });
  }

  setOption<K extends keyof SimOptions>(key: K, value: SimOptions[K]) {
    this.set({ options: { ...this.state.options, [key]: value } });
    if (key === "catalog") this.load();
    if (key === "statusFrozen" && !value) this.poll(false);
  }

  /** Retry countdowns hold while the page is hidden, unless video is in PiP. */
  setPresence(hidden: boolean, pip: boolean) {
    this.hidden = hidden;
    this.pip = pip;
    const phase = this.state.phase;
    if (phase.kind !== "retrying") return;
    if (this.holding() && phase.held === null) {
      this.clearPhase();
      this.set({
        phase: { ...phase, held: Math.max(0, phase.at - this.now()) },
      });
    } else if (!this.holding() && phase.held !== null) {
      const remaining = phase.held;
      this.set({
        phase: { ...phase, at: this.now() + remaining, held: null },
      });
      this.phaseTimer = setTimeout(
        () => this.connect(phase.attempt),
        remaining,
      );
    }
  }

  diagnose() {
    const selection = this.state.selection;
    if (!selection) return;
    clearTimeout(this.operatorTimer);
    this.set({ operator: { kind: "busy", action: "diagnose" } });
    this.operatorTimer = setTimeout(() => {
      const fault = this.state.cameras[selection.camera_id]?.fault ?? null;
      const checks: DiagnosticCheck[] = fault
        ? [
            { name: "source", state: "failed", error: fault },
            { name: "backend", state: "passed" },
            { name: "media", state: "unknown" },
          ]
        : [
            { name: "source", state: "passed" },
            { name: "backend", state: "passed" },
            { name: "media", state: "passed" },
          ];
      this.set({ operator: { kind: "diagnosed", checks } });
    }, this.ms(TIMING.diagnose));
  }

  /** Operator reconnect clears the latch; viewer retry never does. */
  reconnectSource() {
    const selection = this.state.selection;
    if (!selection) return;
    clearTimeout(this.operatorTimer);
    this.set({ operator: { kind: "busy", action: "reconnect" } });
    this.operatorTimer = setTimeout(() => {
      const cameras = { ...this.state.cameras };
      const camera = cameras[selection.camera_id];
      if (camera)
        cameras[selection.camera_id] = {
          ...camera,
          fault: null,
          observedAt: this.now(),
        };
      this.set({ cameras, operator: { kind: "idle" } });
      this.connect(0);
    }, this.ms(TIMING.reconnectSource));
  }

  private resolveCatalog(attempt: number) {
    switch (this.state.options.catalog) {
      case "loading":
        return;
      case "empty":
        return this.set({ catalog: { kind: "empty" } });
      case "denied":
        return this.set({ catalog: { kind: "denied" } });
      case "down": {
        const next = attempt + 1;
        const delay = this.ms(backoff(next, this.random));
        this.set({
          catalog: { kind: "down", attempt: next, at: this.now() + delay },
        });
        this.catalogTimer = setTimeout(() => this.resolveCatalog(next), delay);
        return;
      }
      case "normal": {
        const cameras = this.config.list.cameras.filter(
          (c) => c.streams.length > 0,
        );
        if (!cameras.length) return this.set({ catalog: { kind: "empty" } });
        this.set({ catalog: { kind: "ready", cameras } });
        const ref = resolveRef(cameras, this.wanted);
        if (ref) this.select(ref);
      }
    }
  }

  private connect(attempt: number) {
    const selection = this.state.selection;
    if (!selection) return;
    this.clearPhase();
    const fault = this.state.cameras[selection.camera_id]?.fault ?? null;
    const outcome: Outcome = fault
      ? fault.code
      : (this.forced ??
        (this.state.options.keepFailing ? "media_connection_failed" : "ok"));
    this.forced = null;
    this.set({ phase: { kind: "connecting", since: this.now(), attempt } });
    if (outcome === "timeout")
      this.after(TIMING.deadline, () =>
        this.fail(failure("deadline_exceeded"), attempt),
      );
    else if (outcome === "ok")
      this.after(TIMING.connect + 400 * this.random(), () =>
        this.established(),
      );
    else
      this.after(fault ? TIMING.fault : TIMING.connect, () =>
        this.fail(fault ?? failure(outcome), attempt),
      );
  }

  private established() {
    const transport: Transport = this.random() < 0.8 ? "udp" : "tcp";
    this.set({
      phase: this.state.options.autoplayBlocked
        ? { kind: "blocked", transport }
        : { kind: "live", since: this.now(), transport },
    });
    this.poll(false);
  }

  private fail(error: ErrorBody, attempt: number) {
    this.clearPhase();
    const at = this.now();
    if (retryMode(error) !== "auto")
      return this.set({
        phase: { kind: "failed", error, exhausted: false, at },
      });
    const next = attempt + 1;
    if (next > MAX_RETRIES)
      return this.set({ phase: { kind: "failed", error, exhausted: true, at } });
    const delay = this.ms(backoff(next, this.random));
    const hold = this.holding();
    this.set({
      phase: {
        kind: "retrying",
        error,
        attempt: next,
        delay,
        at: at + delay,
        held: hold ? delay : null,
      },
    });
    if (!hold) this.phaseTimer = setTimeout(() => this.connect(next), delay);
  }

  private poll(walk: boolean) {
    if (this.state.options.statusFrozen) return;
    const now = this.now();
    const cameras: Record<CameraID, CameraLive> = {};
    for (const [id, camera] of Object.entries(this.state.cameras)) {
      const watching = this.watching(id);
      if (camera.observedAt === null && !watching) {
        cameras[id] = camera;
        continue;
      }
      let base = this.base[id] ?? 0;
      if (walk && !camera.fault && this.random() < 0.12)
        base = Math.min(3, Math.max(0, base + (this.random() < 0.5 ? -1 : 1)));
      this.base[id] = base;
      cameras[id] = {
        ...camera,
        viewers: base + (watching ? 1 : 0),
        observedAt: now,
      };
    }
    this.set({ cameras });
  }

  private watching(id: CameraID) {
    const kind = this.state.phase.kind;
    return (
      this.state.selection?.camera_id === id &&
      (kind === "live" || kind === "blocked" || kind === "reconnecting")
    );
  }

  private holding() {
    return this.hidden && !this.pip;
  }

  private ms(value: number) {
    return Math.round(this.state.options.fast ? value / 4 : value);
  }

  private after(value: number, run: () => void) {
    this.phaseTimer = setTimeout(run, this.ms(value));
  }

  private clearPhase() {
    clearTimeout(this.phaseTimer);
    this.phaseTimer = undefined;
  }

  private set(patch: Partial<Snapshot>) {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener();
  }
}

/** Unknown camera or stream IDs fall back silently to the first available. */
export function resolveRef(
  cameras: readonly CameraSummary[],
  wanted: Partial<StreamRef>,
): StreamRef | null {
  const camera =
    cameras.find((c) => c.id === wanted.camera_id) ?? cameras[0] ?? null;
  if (!camera) return null;
  const stream =
    camera.streams.find((s) => s.id === wanted.stream_id) ?? camera.streams[0];
  return stream ? { camera_id: camera.id, stream_id: stream.id } : null;
}

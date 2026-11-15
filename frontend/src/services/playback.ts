import type {
  ErrorBody,
  MediaCapabilities,
  State,
  StreamRef,
  StreamStatus,
} from "./contracts";
import { PlaybackError, safeError, type ViewerAPI } from "./api";

export interface PlaybackSnapshot {
  selection: StreamRef;
  state: State;
  media: MediaStream | null;
  error?: ErrorBody;
  status?: StreamStatus;
  transport?: "tcp" | "udp";
  reloadRequired?: boolean;
  // Page-local times from performance.now(), which a clock change cannot move.
  /** While a dropped connection may recover: when it fails. */
  recoverBy?: number | undefined;
  /** After a lost connection: when it is tried again on its own. */
  retryAt?: number;
  /** When this page last received a newer observation of a known state. */
  statusAt?: number;
  /** Since when this page has asked for status; unset while it does not. */
  observingSince?: number | undefined;
}
/** A dropped connection fails unless it recovers within this window. */
export const RECOVERY_MS = 15000;
/** Delays before each automatic retry after a lost connection; then Try again. */
export const RETRY_MS = [3000, 10000];
/** Video that played this long before its connection was lost earns new retries. */
export const STABLE_MS = 60000;
interface Attempt {
  generation: number;
  selection: StreamRef;
  abort: AbortController;
  peer: RTCPeerConnection;
  media: MediaStream;
  timers: Set<ReturnType<typeof setTimeout>>;
  disconnectTimer?: ReturnType<typeof setTimeout>;
  connectTimer?: ReturnType<typeof setTimeout>;
  /** The status loop has started. */
  observed?: boolean;
  /** The observation time of the last answer counted as an update. */
  counted?: string | null;
  /** Started by an automatic retry, not by the viewer. */
  auto: boolean;
  /** When the current stretch of video began; unset while disconnected. */
  since?: number;
  /** Set once a stretch of video ends: the longest without a disconnection. */
  steady?: number;
}
export function gather(
  peer: RTCPeerConnection,
  signal: AbortSignal,
  timeoutMS = 10000,
): Promise<void> {
  if (signal.aborted) return Promise.reject(new PlaybackError("canceled"));
  if (peer.iceGatheringState === "complete") return Promise.resolve();
  return new Promise((resolve, reject) => {
    const finish = (error?: PlaybackError) => {
      clearTimeout(timer);
      peer.removeEventListener("icegatheringstatechange", changed);
      signal.removeEventListener("abort", aborted);
      error ? reject(error) : resolve();
    };
    const changed = () => {
      if (peer.iceGatheringState === "complete") finish();
    };
    const aborted = () => finish(new PlaybackError("canceled"));
    const timer = setTimeout(
      () => finish(new PlaybackError("deadline_exceeded")),
      timeoutMS,
    );
    peer.addEventListener("icegatheringstatechange", changed);
    signal.addEventListener("abort", aborted, { once: true });
    changed();
  });
}

// A page owns one peer and a serialized create/cleanup chain. No source reset
// or lease renewal exists here. Browser ICE owns recovery of a dropped
// connection; only a lost connection of video that played is tried again on
// its own, a bounded number of times, never an uncertain create.
export class PlaybackController {
  private generation = 0;
  private active: Attempt | undefined;
  private snapshot: PlaybackSnapshot | undefined;
  private tail: Promise<void> = Promise.resolve();
  private sessions = new Set<string>();
  private selected:
    | { ref: StreamRef; capabilities: MediaCapabilities }
    | undefined;
  private observing = true;
  private disposed = false;
  private keepalive = false;
  private reloadRequired = false;
  /** Automatic retries used since the viewer last acted or video played steadily. */
  private retries = 0;
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  /** A retry came due; it starts once the page is visible. */
  private due = false;
  constructor(
    private readonly api: ViewerAPI,
    private readonly changed: (snapshot: PlaybackSnapshot | undefined) => void,
    private readonly createPeer = () =>
      new RTCPeerConnection({ iceServers: [], bundlePolicy: "max-bundle" }),
  ) {}

  private current(attempt: Attempt) {
    return (
      !this.disposed &&
      this.active === attempt &&
      attempt.generation === this.generation
    );
  }
  private publish(attempt: Attempt, update: Partial<PlaybackSnapshot>) {
    if (!this.current(attempt)) return;
    this.snapshot = {
      selection: attempt.selection,
      state: "connecting",
      media: attempt.media,
      ...this.snapshot,
      ...update,
    };
    this.changed(this.snapshot);
  }
  private timer(attempt: Attempt, fn: () => void, ms: number) {
    const timer = setTimeout(() => {
      attempt.timers.delete(timer);
      if (this.current(attempt)) fn();
    }, ms);
    attempt.timers.add(timer);
    return timer;
  }
  private cancel() {
    clearTimeout(this.retryTimer);
    this.due = false;
    const attempt = this.active;
    this.active = undefined;
    if (!attempt) return;
    attempt.abort.abort();
    attempt.timers.forEach(clearTimeout);
    attempt.peer.ontrack = null;
    attempt.peer.onconnectionstatechange = null;
    attempt.peer.close();
    attempt.media.getTracks().forEach((track) => track.stop());
  }
  private async release(keepalive = this.keepalive) {
    for (const sessionID of this.sessions) {
      try {
        await this.api.close(sessionID, keepalive);
        this.sessions.delete(sessionID);
      } catch (error) {
        // A restarted controller intentionally denies unknown session IDs, just
        // as it denies revoked ownership. Neither response proves cleanup. Keep
        // the ID and require a fresh page to recheck catalog access/readiness.
        if (safeError(error).code === "access_denied") this.reloadRequired = true;
        throw new PlaybackError("cleanup_incomplete", !this.reloadRequired);
      }
    }
    this.reloadRequired = false;
  }
  private enqueue(work: () => Promise<void>) {
    this.tail = this.tail.then(work, work);
    return this.tail;
  }
  /** `auto` marks an automatic retry, which keeps the count of retries used. */
  select(
    ref: StreamRef | undefined,
    capabilities?: MediaCapabilities,
    auto = false,
  ): Promise<void> {
    const generation = ++this.generation;
    this.cancel();
    if (!auto) this.retries = 0;
    this.selected = ref && capabilities ? { ref, capabilities } : undefined;
    this.snapshot = ref
      ? { selection: ref, state: "connecting", media: null }
      : undefined;
    this.changed(this.snapshot);
    return this.enqueue(async () => {
      try {
        await this.release();
        if (
          !ref ||
          !capabilities ||
          generation !== this.generation ||
          this.disposed
        )
          return;
        await this.start(generation, ref, capabilities, auto);
      } catch (error) {
        if (generation !== this.generation || this.disposed || !ref) return;
        this.cancel();
        this.snapshot = {
          selection: ref,
          state: "failed",
          media: null,
          error: safeError(error, "signaling_failed"),
          reloadRequired: this.reloadRequired,
        };
        this.changed(this.snapshot);
      }
    });
  }
  retry() {
    if (this.selected)
      return this.select(this.selected.ref, this.selected.capabilities);
    return Promise.resolve();
  }
  /**
   * Ends this page's view and releases its session, which frees the viewer slot
   * without closing the page. The selection is kept, so retry() starts it again.
   */
  stop(): Promise<void> {
    const kept = this.selected;
    if (!kept || this.disposed) return Promise.resolve();
    const generation = ++this.generation;
    this.cancel();
    this.snapshot = { selection: kept.ref, state: "closing", media: null };
    this.changed(this.snapshot);
    return this.enqueue(async () => {
      let error: ErrorBody | undefined;
      try {
        await this.release();
      } catch (reason) {
        error = safeError(reason, "cleanup_incomplete");
      }
      if (generation !== this.generation || this.disposed) return;
      this.snapshot = error
        ? {
            selection: kept.ref,
            state: "failed",
            media: null,
            error,
            reloadRequired: this.reloadRequired,
          }
        : { selection: kept.ref, state: "closed", media: null };
      this.changed(this.snapshot);
    });
  }
  async dispose(keepalive = false) {
    this.disposed = true;
    this.keepalive = keepalive;
    ++this.generation;
    this.cancel();
    this.selected = undefined;
    await this.enqueue(() => this.release(keepalive));
  }
  observe(enabled: boolean) {
    if (enabled === this.observing) return;
    this.observing = enabled;
    this.resume();
    // Freshness counts only while the page asks for status.
    const attempt = this.active;
    if (attempt?.observed)
      this.publish(attempt, {
        observingSince: enabled ? performance.now() : undefined,
      });
  }
  playing() {
    const attempt = this.active;
    if (attempt && attempt.peer.connectionState === "connected") {
      attempt.since ??= performance.now();
      this.publish(attempt, { state: "playing" });
    }
  }
  // Ends the current stretch of video and keeps the longest. An outage is not
  // video, so a link that keeps dropping never earns new retries.
  private still(attempt: Attempt) {
    if (attempt.since !== undefined)
      attempt.steady = Math.max(
        attempt.steady ?? 0,
        performance.now() - attempt.since,
      );
    delete attempt.since;
  }
  // A due retry waits while the page is hidden, so a page nobody sees does not
  // take a viewer slot again.
  private resume() {
    if (this.due && this.observing && this.selected)
      void this.select(this.selected.ref, this.selected.capabilities, true);
  }
  mediaError() {
    if (this.active)
      void this.failure(
        this.active,
        safeError(new PlaybackError("media_connection_failed")),
      );
  }
  waiting() {
    const attempt = this.active;
    if (attempt && this.snapshot?.state === "playing")
      this.publish(attempt, { state: "reconnecting" });
  }
  /** `lost`: the peer failed, or stayed disconnected past its window. */
  private async failure(attempt: Attempt, error: ErrorBody, lost = false) {
    if (!this.current(attempt)) return;
    this.cancel();
    this.still(attempt);
    if ((attempt.steady ?? 0) >= STABLE_MS) this.retries = 0;
    // Only video that played (a stretch ended above), or an automatic retry
    // of it, is tried again.
    const delay =
      lost && (attempt.steady !== undefined || attempt.auto)
        ? RETRY_MS[this.retries]
        : undefined;
    this.snapshot = {
      selection: attempt.selection,
      state: "failed",
      media: null,
      error,
    };
    if (delay !== undefined) {
      this.retries++;
      this.snapshot.retryAt = performance.now() + delay;
    }
    const { retryAt } = this.snapshot;
    this.changed(this.snapshot);
    await this.enqueue(async () => {
      try {
        await this.release();
      } catch {
        // Cleanup that failed is not repeated on its own, and nor is the view.
        if (attempt.generation === this.generation && !this.disposed) {
          this.snapshot = {
            selection: attempt.selection,
            state: "failed",
            media: null,
            error: safeError(
              new PlaybackError("cleanup_incomplete", !this.reloadRequired),
            ),
            reloadRequired: this.reloadRequired,
          };
          this.changed(this.snapshot);
        }
        return;
      }
      // The retry waits for the server to confirm the old session's release,
      // however long that takes; a viewer action meanwhile supersedes it.
      if (
        retryAt !== undefined &&
        attempt.generation === this.generation &&
        !this.disposed
      )
        this.retryTimer = setTimeout(() => {
          this.due = true;
          this.resume();
        }, retryAt - performance.now());
    });
  }
  private async start(
    generation: number,
    ref: StreamRef,
    capabilities: MediaCapabilities,
    auto: boolean,
  ) {
    if (!capabilities.video && !capabilities.audio)
      throw new PlaybackError("unsupported_capability", false);
    let peer: RTCPeerConnection;
    try {
      peer = this.createPeer();
    } catch {
      throw new PlaybackError("unsupported_browser", false);
    }
    const attempt: Attempt = {
      generation,
      selection: ref,
      abort: new AbortController(),
      peer,
      media: new MediaStream(),
      timers: new Set(),
      auto,
    };
    this.active = attempt;
    const lost = (really = true) =>
      void this.failure(
        attempt,
        safeError(new PlaybackError("media_connection_failed")),
        really,
      );
    peer.ontrack = (event) => {
      if (!this.current(attempt)) {
        event.track.stop();
        return;
      }
      if (!attempt.media.getTracks().includes(event.track))
        attempt.media.addTrack(event.track);
      this.publish(attempt, { media: attempt.media });
    };
    peer.onconnectionstatechange = () => {
      if (!this.current(attempt)) return;
      if (
        peer.connectionState === "connected" &&
        attempt.connectTimer !== undefined
      ) {
        clearTimeout(attempt.connectTimer);
        attempt.timers.delete(attempt.connectTimer);
        delete attempt.connectTimer;
      }
      if (
        peer.connectionState !== "disconnected" &&
        attempt.disconnectTimer !== undefined
      ) {
        clearTimeout(attempt.disconnectTimer);
        attempt.timers.delete(attempt.disconnectTimer);
        delete attempt.disconnectTimer;
        this.publish(attempt, { recoverBy: undefined });
      }
      if (
        peer.connectionState === "failed" ||
        peer.connectionState === "closed"
      ) {
        // Only this page closes its peers, after detaching this handler.
        lost(peer.connectionState === "failed");
      } else if (peer.connectionState === "disconnected") {
        if (attempt.disconnectTimer !== undefined) {
          this.publish(attempt, { state: "reconnecting" });
          return;
        }
        this.still(attempt);
        attempt.disconnectTimer = this.timer(
          attempt,
          () => {
            if (peer.connectionState === "disconnected") lost();
          },
          RECOVERY_MS,
        );
        this.publish(attempt, {
          state: "reconnecting",
          recoverBy: performance.now() + RECOVERY_MS,
        });
      }
    };
    try {
      if (capabilities.video)
        peer.addTransceiver("video", { direction: "recvonly" });
      if (capabilities.audio)
        peer.addTransceiver("audio", { direction: "recvonly" });
      await peer.setLocalDescription(await peer.createOffer());
      await gather(peer, attempt.abort.signal);
      if (!this.current(attempt)) return;
      const sdp = peer.localDescription?.sdp;
      if (!sdp) throw new PlaybackError("signaling_failed");
      // Once sent, let the bounded create return its ID even if the selection
      // changes. A late success is released before another create is admitted.
      const session = await this.api.create(ref, sdp);
      this.sessions.add(session.id);
      if (!this.current(attempt)) {
        await this.release();
        return;
      }
      await peer.setRemoteDescription(session.playback.answer);
      if (!this.current(attempt)) return;
      attempt.observed = true;
      this.publish(attempt, {
        media: attempt.media,
        observingSince: this.observing ? performance.now() : undefined,
      });
      if (peer.connectionState !== "connected") {
        attempt.connectTimer = this.timer(
          attempt,
          () => {
            if (peer.connectionState !== "connected") lost();
          },
          20000,
        );
      }
      void this.observation(attempt);
    } catch (error) {
      const current = this.current(attempt);
      if (current) this.cancel();
      await this.release();
      if (current) throw error;
    }
  }
  private async observation(attempt: Attempt) {
    if (!this.current(attempt)) return;
    try {
      if (this.observing) {
        const status = await this.api.status(
          attempt.selection,
          attempt.abort.signal,
        );
        if (!this.current(attempt)) return;
        // Only a newer observation of a known state is an update. A server
        // whose observation went stale answers unknown with its old time.
        const update =
          status.observed_at !== null &&
          status.observed_at !== attempt.counted &&
          status.state !== "unknown";
        if (update) attempt.counted = status.observed_at;
        this.publish(
          attempt,
          update ? { status, statusAt: performance.now() } : { status },
        );
        if (status.error) {
          void this.failure(attempt, status.error);
          return;
        }
        // Only expose the selected transport, never candidate addresses or SDP.
        const stats = await attempt.peer.getStats();
        if (!this.current(attempt)) return;
        stats.forEach((report) => {
          if (report.type !== "transport" || !report.selectedCandidatePairId)
            return;
          const pair = stats.get(report.selectedCandidatePairId);
          const candidate = pair && stats.get(pair.remoteCandidateId);
          if (candidate?.protocol === "tcp" || candidate?.protocol === "udp")
            this.publish(attempt, { transport: candidate.protocol });
        });
      }
    } catch (error) {
      if (!this.current(attempt)) return;
      const safe = safeError(error, "server_unavailable");
      if (safe.code === "access_denied" || safe.code === "not_found") {
        void this.failure(attempt, safe);
        return;
      }
      // A status request does not keep media alive. Preserve a working peer
      // during a control-plane outage; unknown observations remain unknown,
      // and the last statusAt keeps counting how long updates have stopped.
      this.publish(attempt, {
        status: {
          stream_id: attempt.selection.stream_id,
          state: "unknown",
          viewers: null,
          observed_at: null,
        },
      });
    } finally {
      this.timer(
        attempt,
        () => {
          void this.observation(attempt);
        },
        2000,
      );
    }
  }
}

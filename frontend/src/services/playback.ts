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
}
interface Attempt {
  generation: number;
  selection: StreamRef;
  abort: AbortController;
  peer: RTCPeerConnection;
  media: MediaStream;
  timers: Set<ReturnType<typeof setTimeout>>;
  disconnectTimer?: ReturnType<typeof setTimeout>;
  connectTimer?: ReturnType<typeof setTimeout>;
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

// A page owns one peer and a serialized create/cleanup chain. No source reset,
// lease renewal or automatic POST retry exists here. Browser ICE owns recovery.
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
  select(
    ref: StreamRef | undefined,
    capabilities?: MediaCapabilities,
  ): Promise<void> {
    const generation = ++this.generation;
    this.cancel();
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
        await this.start(generation, ref, capabilities);
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
  async dispose(keepalive = false) {
    this.disposed = true;
    this.keepalive = keepalive;
    ++this.generation;
    this.cancel();
    this.selected = undefined;
    await this.enqueue(() => this.release(keepalive));
  }
  observe(enabled: boolean) {
    this.observing = enabled;
  }
  playing() {
    const attempt = this.active;
    if (attempt && attempt.peer.connectionState === "connected")
      this.publish(attempt, { state: "playing" });
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
  private async failure(attempt: Attempt, error: ErrorBody) {
    if (!this.current(attempt)) return;
    this.cancel();
    this.snapshot = {
      selection: attempt.selection,
      state: "failed",
      media: null,
      error,
    };
    this.changed(this.snapshot);
    await this.enqueue(async () => {
      try {
        await this.release();
      } catch {
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
      }
    });
  }
  private async start(
    generation: number,
    ref: StreamRef,
    capabilities: MediaCapabilities,
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
    };
    this.active = attempt;
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
      }
      if (
        peer.connectionState === "failed" ||
        peer.connectionState === "closed"
      ) {
        void this.failure(
          attempt,
          safeError(new PlaybackError("media_connection_failed")),
        );
      } else if (peer.connectionState === "disconnected") {
        this.publish(attempt, { state: "reconnecting" });
        if (attempt.disconnectTimer !== undefined) return;
        attempt.disconnectTimer = this.timer(
          attempt,
          () => {
            if (peer.connectionState === "disconnected")
              void this.failure(
                attempt,
                safeError(new PlaybackError("media_connection_failed")),
              );
          },
          15000,
        );
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
      this.publish(attempt, { media: attempt.media });
      if (peer.connectionState !== "connected") {
        attempt.connectTimer = this.timer(
          attempt,
          () => {
            if (attempt.peer.connectionState !== "connected")
              void this.failure(
                attempt,
                safeError(new PlaybackError("media_connection_failed")),
              );
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
        this.publish(attempt, { status });
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
      // during a control-plane outage; unknown observations remain unknown.
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

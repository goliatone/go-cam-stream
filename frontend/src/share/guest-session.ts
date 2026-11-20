import { gather } from "../services/playback";
import {
  GuestError,
  validHandle,
  validInvitation,
  type GuestAPI,
  type GuestFailure,
} from "./guest-api";

export type GuestPhase =
  /** Exchanging the link's secret for this tab's guest session. */
  | "checking"
  /** Exchanged; Play admits a viewer. No viewer slot is held yet. */
  | "ready"
  | "connecting"
  /** Video frames are arriving. */
  | "live"
  /** The connection dropped; this tab's place is kept while it reconnects. */
  | "reconnecting"
  /** Connected, but frames stopped or never arrived. */
  | "stalled"
  /** The reconnect window passed; Retry admits again if a place is free. */
  | "lost"
  | "full"
  | "unavailable"
  | "offline"
  | "expired"
  | "revoked"
  | "denied"
  | "unsupported"
  /** Opened without the link's secret, for example after a reload. */
  | "reopen";

export interface GuestView {
  phase: GuestPhase;
  /** Video only; an audio track is never attached. */
  media: MediaStream | null;
  /** Absolute end of the share, in milliseconds since the epoch. */
  expiresAt: number | null;
  /** Page-local time (monotonic clock) when the kept place lapses. */
  reconnectUntil: number | null;
  /** An in-app browser, where playback is best effort. */
  embedded: boolean;
}

/** D04: a dropped viewer keeps its place this long while it reconnects. */
export const RECONNECT_MS = 30000;
/** Browser ICE may recover a brief drop on its own first. */
export const RENEGOTIATE_MS = 2000;
export const RECONNECT_RETRY_MS = 3000;
export const GATHER_MS = 8000;
export const FIRST_FRAME_MS = 20000;
export const STALL_MS = 4000;
const RATE_RETRIES = 2;
const RETRYABLE: readonly GuestPhase[] = [
  "ready",
  "stalled",
  "lost",
  "full",
  "unavailable",
  "offline",
];
const PLAYING: readonly GuestPhase[] = ["connecting", "live", "stalled"];
const ENDED: readonly GuestPhase[] = [
  "expired",
  "revoked",
  "denied",
  "unsupported",
  "reopen",
];
export const isEnded = (phase: GuestPhase) => ENDED.includes(phase);

export interface GuestEnvironment {
  pathname: string;
  hash: string;
  /** Replaces the current history entry's URL, removing the fragment. */
  replaceURL(url: string): void;
  /** Absent when the browser cannot receive WebRTC video. */
  createPeer: ((config: RTCConfiguration) => RTCPeerConnection) | null;
  createStream(tracks: MediaStreamTrack[]): MediaStream;
  /** Wall clock, for the share's absolute deadline. */
  now(): number;
  /** Monotonic page clock, for windows a clock change must not move. */
  monotonic(): number;
  setTimer(run: () => void, ms: number): unknown;
  clearTimer(timer: unknown): void;
  online(): boolean;
  userAgent: string;
}

export function browserEnvironment(): GuestEnvironment {
  const supported =
    typeof RTCPeerConnection === "function" &&
    typeof RTCPeerConnection.prototype.addTransceiver === "function" &&
    typeof MediaStream === "function";
  return {
    pathname: location.pathname,
    hash: location.hash,
    replaceURL: (url) => history.replaceState(history.state, "", url),
    createPeer: supported ? (config) => new RTCPeerConnection(config) : null,
    createStream: (tracks) => new MediaStream(tracks),
    now: () => Date.now(),
    monotonic: () => performance.now(),
    setTimer: (run, ms) => setTimeout(run, ms),
    clearTimer: (timer) => clearTimeout(timer as ReturnType<typeof setTimeout>),
    online: () => navigator.onLine !== false,
    userAgent: navigator.userAgent,
  };
}

/** Common in-app browsers, where WebRTC playback is best effort (D06). */
export const embeddedBrowser = (userAgent: string) =>
  /\b(?:FBAN|FBAV|FB_IAB|Instagram|Line\/|MicroMessenger|BytedanceWebview|musical_ly|Snapchat|LinkedInApp|GSA\/)|; wv\)/i.test(
    userAgent,
  );

type Timer = "expiry" | "window" | "renegotiate" | "frame" | "stall" | "rate";

/**
 * One tab's guest viewer. The link's secret is read once, removed from the
 * address bar and exchanged for an HttpOnly guest session; its CSRF token and
 * viewer handles live only in this object. Nothing is stored in the browser.
 * Opening the page holds no viewer slot: only Play admits a viewer.
 */
export class GuestSession {
  private state: GuestView;
  private readonly listeners = new Set<(view: GuestView) => void>();
  private api: GuestAPI | null = null;
  private invitation: string | undefined;
  private csrf: string | undefined;
  /** The server's clock minus this device's, when they disagree noticeably. */
  private skew = 0;
  private viewerID: string | undefined;
  private attemptID: string | undefined;
  private peer: RTCPeerConnection | null = null;
  /** The current connection's video track, for the stall watchdog. */
  private video: MediaStreamTrack | null = null;
  /** Releases still in flight; a new admission waits for them. */
  private releasing: Promise<void> = Promise.resolve();
  private abort: AbortController | null = null;
  /** Invalidates the callbacks and replies of superseded attempts. */
  private generation = 0;
  /** The current peer has delivered video frames. */
  private framed = false;
  /** Monotonic time the current loss began; null while connected. */
  private lossAt: number | null = null;
  private rateRetries = 0;
  private readonly timers = new Map<Timer, unknown>();

  constructor(
    private readonly env: GuestEnvironment,
    private readonly connect: (shareID: string) => GuestAPI,
  ) {
    this.state = {
      phase: "checking",
      media: null,
      expiresAt: null,
      reconnectUntil: null,
      embedded: embeddedBrowser(env.userAgent),
    };
  }

  get view(): GuestView {
    return this.state;
  }
  subscribe(listener: (view: GuestView) => void) {
    this.listeners.add(listener);
    return () => void this.listeners.delete(listener);
  }

  start() {
    const shareID = /^\/s\/([^/]+)$/.exec(this.env.pathname)?.[1];
    if (!validHandle(shareID)) return this.update({ phase: "denied" });
    // Keep the fragment where WebRTC is missing, so an in-app browser's
    // "Open in browser" carries the complete link.
    if (!this.env.createPeer) return this.update({ phase: "unsupported" });
    const invitation = this.env.hash.replace(/^#/, "");
    if (!invitation) return this.update({ phase: "reopen" });
    // The secret leaves the address bar and this history entry before any
    // request; the exchange and its retries use the in-memory copy.
    this.env.replaceURL(this.env.pathname);
    if (!validInvitation(invitation)) return this.update({ phase: "denied" });
    this.exchange(shareID, invitation);
  }

  /**
   * The link was opened again in this tab. Only the fragment changes, so the
   * page does not reload: the secret leaves the address bar at once, and a tab
   * that is not watching starts over with it.
   */
  relinked(hash: string) {
    const invitation = hash.replace(/^#/, "");
    if (!invitation || !this.env.createPeer) return;
    this.env.replaceURL(this.env.pathname);
    const shareID = /^\/s\/([^/]+)$/.exec(this.env.pathname)?.[1];
    if (!validHandle(shareID) || !validInvitation(invitation)) return;
    if (PLAYING.includes(this.state.phase) || this.state.phase === "reconnecting") return;
    this.stop();
    this.release();
    this.exchange(shareID, invitation);
  }

  /** Play and Retry: admit this tab's viewer, or repeat a failed exchange. */
  play() {
    if (!RETRYABLE.includes(this.state.phase)) return;
    if (!this.csrf) {
      if (this.invitation) void this.redeem();
      return;
    }
    this.rateRetries = 0;
    // Stalled video reconnects within this tab's kept place. Anything else
    // admits anew, subject to the share's current validity and capacity.
    if (this.state.phase === "stalled") return this.dropped(true);
    this.release();
    void this.admit(undefined, false);
  }

  /** The viewer reports that frames of `media` are being presented. */
  framesStarted(media: MediaStream) {
    if (media !== this.state.media) return;
    this.framed = true;
    this.clear("frame");
    this.clear("stall");
    if (this.lossAt === null && PLAYING.includes(this.state.phase))
      this.update({ phase: "live" });
  }

  /** The page became visible again, for example after a phone was unlocked. */
  visible() {
    if (isEnded(this.state.phase)) return;
    if (this.expiredNow()) return this.end("expired");
    const state = this.peer?.connectionState;
    if (
      PLAYING.includes(this.state.phase) &&
      (state === "disconnected" || state === "failed" || state === "closed")
    )
      this.dropped(state !== "disconnected");
  }
  online() {
    if (this.state.phase === "offline") return this.play();
    // Waiting to reconnect: try now. An attempt still negotiating continues,
    // since replacing it would strand the place it reserved.
    if (this.state.phase === "reconnecting" && this.timers.has("renegotiate")) this.reconnect();
  }
  /** Leaving the page frees this tab's place at once. */
  pagehide() {
    if (!PLAYING.includes(this.state.phase) && this.state.phase !== "reconnecting")
      return;
    this.stop();
    this.release(true);
    this.update({ phase: "ready", media: null, reconnectUntil: null });
  }
  /** A page restored from the back/forward cache rechecks its deadline. */
  pageshow() {
    if (!isEnded(this.state.phase) && this.expiredNow()) this.end("expired");
  }
  dispose() {
    this.stop();
    this.clear("expiry");
    this.listeners.clear();
  }

  private exchange(shareID: string, invitation: string) {
    this.api = this.connect(shareID);
    this.invitation = invitation;
    void this.redeem();
  }

  private async redeem() {
    const api = this.api;
    const invitation = this.invitation;
    if (!api || !invitation) return;
    const generation = this.restart();
    this.update({ phase: "checking" });
    try {
      const result = await api.redeem(invitation, this.abort!.signal);
      if (generation !== this.generation) return;
      this.invitation = undefined;
      this.csrf = result.csrf;
      this.rateRetries = 0;
      this.deadline(result.expiresAt, result.serverTime);
      this.update({ phase: "ready" });
    } catch (error) {
      if (generation === this.generation) this.failed(error, () => void this.redeem());
    }
  }

  private async admit(viewerID: string | undefined, reconnecting: boolean) {
    const api = this.api;
    const csrf = this.csrf;
    const create = this.env.createPeer;
    if (!api || !csrf || !create) return;
    const generation = this.restart();
    const signal = this.abort!.signal;
    this.framed = false;
    if (reconnecting) this.update({ phase: "reconnecting" });
    else this.update({ phase: "connecting", media: null, reconnectUntil: null });
    // A place this tab just gave back must be free before it asks again.
    await this.releasing;
    if (generation !== this.generation) return;
    let admission;
    try {
      // Never aborted: an admission that arrives after the page moved on is
      // closed below rather than left reserved on the server.
      admission = await api.admit(csrf, viewerID);
    } catch (error) {
      if (generation === this.generation)
        this.failed(error, () => void this.admit(viewerID, reconnecting), viewerID);
      return;
    }
    if (generation !== this.generation) {
      this.closeAttempt(api, csrf, admission.viewerID, admission.attemptID);
      return;
    }
    this.rateRetries = 0;
    this.viewerID = admission.viewerID;
    this.attemptID = admission.attemptID;
    this.deadline(admission.expiresAt, admission.serverTime);
    try {
      const peer = create({ iceServers: admission.iceServers, bundlePolicy: "max-bundle" });
      this.peer = peer;
      peer.addTransceiver("video", { direction: "recvonly" });
      peer.addEventListener("track", (event) => this.track(generation, event));
      peer.addEventListener("connectionstatechange", () => this.changed(generation));
      await peer.setLocalDescription(await peer.createOffer());
      try {
        await gather(peer, signal, GATHER_MS);
      } catch {
        if (signal.aborted) return;
        // Send the candidates gathered in time; a missing relay candidate
        // shows up as a failed connection, not as a stuck page.
      }
      const sdp = peer.localDescription?.sdp;
      if (!sdp) throw new GuestError("unsupported_offer");
      const answer = await api.open(
        csrf,
        admission.viewerID,
        admission.attemptID,
        { type: "offer", sdp },
        signal,
      );
      if (generation !== this.generation) return;
      await peer.setRemoteDescription(answer);
    } catch (error) {
      if (generation !== this.generation) return;
      // An attempt that never opened holds no resumable place: free it now.
      this.closePeer();
      this.release();
      this.failed(error, () => void this.admit(undefined, reconnecting));
      return;
    }
    if (generation !== this.generation) return;
    this.watch(generation);
  }

  /** Arms the first-frame or stall watchdog for the current connection. */
  private watch(generation: number) {
    if (!this.framed)
      this.timer("frame", FIRST_FRAME_MS, () => {
        if (generation === this.generation && !this.framed) this.stalled();
      });
    else if (this.video?.muted) this.timer("stall", STALL_MS, () => this.stalled());
  }

  private track(generation: number, event: RTCTrackEvent) {
    const track = event.track;
    if (generation !== this.generation || track.kind !== "video") return;
    track.addEventListener("mute", () => {
      if (generation === this.generation && this.state.phase === "live")
        this.timer("stall", STALL_MS, () => this.stalled());
    });
    track.addEventListener("unmute", () => {
      if (generation !== this.generation) return;
      this.clear("stall");
      if (this.state.phase === "stalled" && this.framed) this.update({ phase: "live" });
    });
    track.addEventListener("ended", () => {
      if (generation === this.generation) this.dropped(true);
    });
    this.video = track;
    this.update({ media: this.env.createStream([track]) });
  }

  private changed(generation: number) {
    const state = this.peer?.connectionState;
    if (generation !== this.generation || !state) return;
    if (state === "connected") {
      if (this.lossAt === null) return;
      this.lossAt = null;
      this.clear("window");
      this.clear("renegotiate");
      this.update({ phase: this.framed ? "live" : "connecting", reconnectUntil: null });
      // Dropping cleared the watchdogs; a recovered connection needs them again.
      this.watch(generation);
    } else if (state === "disconnected") this.dropped(false);
    else if (state === "failed" || state === "closed") this.dropped(true);
  }

  /** The current connection dropped: keep the place for up to 30 seconds. */
  private dropped(now: boolean) {
    if (this.lossAt === null) {
      if (!PLAYING.includes(this.state.phase)) return;
      this.lossAt = this.env.monotonic();
      this.clear("frame");
      this.clear("stall");
      this.timer("window", RECONNECT_MS, () => this.lapsed());
      this.update({ phase: "reconnecting", reconnectUntil: this.lossAt + RECONNECT_MS });
    }
    this.timer("renegotiate", now ? 0 : RENEGOTIATE_MS, () => this.reconnect());
  }
  private reconnect() {
    if (this.lossAt === null) return;
    if (this.expiredNow()) return this.end("expired");
    this.clear("renegotiate");
    void this.admit(this.viewerID, true);
  }
  /** The kept place lapsed without video: free it and offer Retry. */
  private lapsed() {
    this.stop();
    this.release();
    this.update({ phase: this.env.online() ? "lost" : "offline", media: null, reconnectUntil: null });
  }
  private stalled() {
    if (PLAYING.includes(this.state.phase)) this.update({ phase: "stalled" });
  }

  private failed(error: unknown, again: () => void, viewerID?: string) {
    const code: GuestFailure = error instanceof GuestError ? error.code : "unavailable";
    if (code === "canceled") return;
    switch (code) {
      case "expired":
      case "revoked":
        return this.end(code);
      case "invalid_invitation":
      case "permission_denied":
        return this.end("denied");
      case "invalid_request":
        if (this.csrf === undefined) return this.end("denied");
        break;
      case "unsupported_offer":
        return this.end("unsupported");
      case "capacity_reached":
        return this.rest("full");
      case "stale_viewer_attempt":
        // The kept place is gone. While reconnecting, give back any attempt
        // this tab still holds, then ask for a new place.
        if (viewerID !== undefined && this.lossAt !== null) {
          this.release();
          return void this.admit(undefined, true);
        }
        break;
      case "rate_limited":
        if (this.rateRetries < RATE_RETRIES) {
          this.rateRetries += 1;
          this.timer("rate", Math.max(1000, error instanceof GuestError ? error.retryAfterMS : 0), again);
          return;
        }
        break;
    }
    // While reconnecting, keep trying on a short interval; the window's own
    // timer ends the attempt with Connection lost.
    if (this.lossAt !== null) {
      if (this.env.monotonic() + RECONNECT_RETRY_MS < this.lossAt + RECONNECT_MS)
        this.timer("renegotiate", RECONNECT_RETRY_MS, () => this.reconnect());
      return;
    }
    this.rest(code === "network" && !this.env.online() ? "offline" : "unavailable");
  }

  /** A state that waits for Retry: no peer, no media, no held place. */
  private rest(phase: GuestPhase) {
    this.stop();
    this.release();
    this.update({ phase, media: null, reconnectUntil: null });
  }
  /** Expiry, revocation and denial override any recovery and clear media. */
  private end(phase: GuestPhase) {
    this.stop();
    this.clear("expiry");
    this.invitation = undefined;
    this.csrf = undefined;
    this.viewerID = undefined;
    this.attemptID = undefined;
    this.update({ phase, media: null, reconnectUntil: null });
  }

  private deadline(expiresAt: number, serverTime?: number) {
    if (serverTime !== undefined) {
      const skew = serverTime - this.env.now();
      // The Date header has one-second precision; ignore smaller differences.
      this.skew = Math.abs(skew) > 2000 ? skew : 0;
    }
    this.update({ expiresAt });
    this.timer("expiry", Math.max(0, expiresAt - this.serverNow()), () => this.end("expired"));
  }
  private serverNow() {
    return this.env.now() + this.skew;
  }
  private expiredNow() {
    return this.state.expiresAt !== null && this.serverNow() >= this.state.expiresAt;
  }

  /** Starts a new attempt; a reconnect window in progress continues. */
  private restart() {
    this.generation += 1;
    this.abort?.abort();
    this.abort = new AbortController();
    this.closePeer();
    for (const name of ["renegotiate", "frame", "stall", "rate"] as const)
      this.clear(name);
    return this.generation;
  }
  /** Cancels in-flight work and any reconnect window. */
  private stop() {
    this.generation += 1;
    this.abort?.abort();
    this.abort = null;
    this.closePeer();
    this.lossAt = null;
    for (const name of ["window", "renegotiate", "frame", "stall", "rate"] as const)
      this.clear(name);
  }
  private closePeer() {
    const peer = this.peer;
    this.peer = null;
    this.video = null;
    peer?.close();
  }
  /** Frees this tab's place for the current attempt, best effort. */
  private release(keepalive = false) {
    const { api, csrf, viewerID, attemptID } = this;
    this.viewerID = undefined;
    this.attemptID = undefined;
    if (api && csrf && viewerID && attemptID) this.closeAttempt(api, csrf, viewerID, attemptID, keepalive);
  }
  private closeAttempt(api: GuestAPI, csrf: string, viewerID: string, attemptID: string, keepalive = false) {
    const closed = api.close(csrf, viewerID, attemptID, keepalive).catch(() => {});
    this.releasing = Promise.all([this.releasing, closed]).then(() => {});
  }
  private timer(name: Timer, ms: number, run: () => void) {
    this.clear(name);
    this.timers.set(
      name,
      this.env.setTimer(() => {
        this.timers.delete(name);
        run();
      }, ms),
    );
  }
  private clear(name: Timer) {
    if (!this.timers.has(name)) return;
    this.env.clearTimer(this.timers.get(name));
    this.timers.delete(name);
  }
  private update(patch: Partial<GuestView>) {
    this.state = { ...this.state, ...patch };
    for (const listener of this.listeners) listener(this.state);
  }
}

/** Connects page lifecycle events to the session. */
export function bindPageEvents(session: GuestSession, page: Window = window) {
  const doc = page.document;
  const visibility = () => {
    if (doc.visibilityState === "visible") session.visible();
  };
  const online = () => session.online();
  const hide = () => session.pagehide();
  const show = (event: PageTransitionEvent) => {
    if (event.persisted) session.pageshow();
  };
  const relinked = () => session.relinked(page.location.hash);
  doc.addEventListener("visibilitychange", visibility);
  page.addEventListener("online", online);
  page.addEventListener("pagehide", hide);
  page.addEventListener("pageshow", show);
  page.addEventListener("hashchange", relinked);
  return () => {
    doc.removeEventListener("visibilitychange", visibility);
    page.removeEventListener("online", online);
    page.removeEventListener("pagehide", hide);
    page.removeEventListener("pageshow", show);
    page.removeEventListener("hashchange", relinked);
  };
}

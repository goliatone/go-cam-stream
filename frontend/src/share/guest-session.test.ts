import { afterEach, describe, expect, it, vi } from "vitest";
import examples from "../../../data/api/sharing_examples.json";
import { GuestError, type Admission, type GuestAPI } from "./guest-api";
import {
  embeddedBrowser,
  FIRST_FRAME_MS,
  GuestSession,
  RECONNECT_MS,
  RENEGOTIATE_MS,
  STALL_MS,
  type GuestEnvironment,
} from "./guest-session";

const SHARE = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
const VIEWER = examples.admitted.viewer_id;
const INVITATION = examples.redeem.invitation;
const T0 = Date.parse("2026-11-17T22:30:00Z");
const END = Date.parse(examples.redeemed.expires_at);
const attempt = (n: number) => `${String.fromCharCode(65 + n).repeat(42)}A`;

class FakeTrack extends EventTarget {
  muted = false;
  constructor(readonly kind: string) {
    super();
  }
}
// Receive-only negotiation double. Gathering is already complete.
class FakePeer extends EventTarget {
  connectionState: RTCPeerConnectionState = "new";
  iceGatheringState: RTCIceGatheringState = "complete";
  localDescription: RTCSessionDescriptionInit | null = null;
  remote: RTCSessionDescriptionInit | null = null;
  transceivers: { kind: string; init: RTCRtpTransceiverInit | undefined }[] = [];
  closed = false;
  constructor(readonly config: RTCConfiguration) {
    super();
  }
  addTransceiver(kind: string, init?: RTCRtpTransceiverInit) {
    this.transceivers.push({ kind, init });
  }
  async createOffer() {
    return { type: "offer" as const, sdp: "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=recvonly\r\n" };
  }
  async setLocalDescription(value: RTCSessionDescriptionInit) {
    this.localDescription = value;
  }
  async setRemoteDescription(value: RTCSessionDescriptionInit) {
    this.remote = value;
  }
  close() {
    this.closed = true;
    this.connectionState = "closed";
  }
  state(next: RTCPeerConnectionState) {
    this.connectionState = next;
    this.dispatchEvent(new Event("connectionstatechange"));
  }
  track(kind = "video") {
    const track = new FakeTrack(kind);
    this.dispatchEvent(Object.assign(new Event("track"), { track }));
    return track;
  }
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

function setup(options: Partial<GuestEnvironment> & { api?: Partial<Pick<GuestAPI, "redeem" | "admit">> } = {}) {
  let mono = 1000;
  const timers = new Map<number, { at: number; run: () => void }>();
  let next = 0;
  let attempts = 0;
  const peers: FakePeer[] = [];
  const api = {
    redeem: vi.fn<GuestAPI["redeem"]>(async () => ({ expiresAt: END, csrf: "fixture-csrf" })),
    admit: vi.fn<GuestAPI["admit"]>(
      async (_csrf, viewerID): Promise<Admission> => ({
        viewerID: viewerID ?? VIEWER,
        attemptID: attempt(++attempts),
        expiresAt: END,
        iceServers: [{ urls: ["turns:relay.example.test:443?transport=tcp"], username: "u", credential: "c" }],
      }),
    ),
    open: vi.fn<GuestAPI["open"]>(async () => ({ type: "answer", sdp: "v=0\r\nanswer" })),
    close: vi.fn<GuestAPI["close"]>(async () => {}),
  };
  const { api: replaced, ...overrides } = options;
  if (replaced?.redeem) api.redeem.mockImplementation(replaced.redeem);
  if (replaced?.admit) api.admit.mockImplementation(replaced.admit);
  const env: GuestEnvironment = {
    pathname: `/s/${SHARE}`,
    hash: `#${INVITATION}`,
    replaceURL: vi.fn(),
    createPeer: (config) => {
      const peer = new FakePeer(config);
      peers.push(peer);
      return peer as unknown as RTCPeerConnection;
    },
    createStream: (tracks) => ({ tracks }) as unknown as MediaStream,
    now: () => T0 + mono - 1000,
    monotonic: () => mono,
    setTimer: (run, ms) => {
      timers.set(++next, { at: mono + ms, run });
      return next;
    },
    clearTimer: (timer) => void timers.delete(timer as number),
    online: () => true,
    userAgent: "Mozilla/5.0 (Macintosh) Safari/605.1.15",
    ...overrides,
  };
  const connect = vi.fn(() => api as GuestAPI);
  const session = new GuestSession(env, connect);
  const phases: string[] = [];
  session.subscribe((view) => {
    if (phases.at(-1) !== view.phase) phases.push(view.phase);
  });
  async function advance(ms: number) {
    const end = mono + ms;
    for (;;) {
      const due = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
      if (!due) break;
      timers.delete(due[0]);
      mono = due[1].at;
      due[1].run();
      await flush();
    }
    mono = end;
    await flush();
  }
  const peer = () => peers.at(-1)!;
  async function live() {
    session.start();
    await flush();
    session.play();
    await flush();
    peer().state("connected");
    const track = peer().track("video");
    session.framesStarted(session.view.media!);
    expect(session.view.phase).toBe("live");
    return track;
  }
  return { session, env, api, connect, peers, peer, phases, advance, live, timers };
}

afterEach(() => vi.restoreAllMocks());

describe("guest session", () => {
  it("removes the fragment before exchanging it and holds no slot until Play", async () => {
    const { session, env, api } = setup();
    session.start();
    expect(env.replaceURL).toHaveBeenCalledWith(`/s/${SHARE}`);
    expect(vi.mocked(env.replaceURL).mock.invocationCallOrder[0]!).toBeLessThan(
      api.redeem.mock.invocationCallOrder[0]!,
    );
    expect(api.redeem).toHaveBeenCalledWith(INVITATION, expect.any(AbortSignal));
    await flush();
    expect(session.view).toMatchObject({ phase: "ready", expiresAt: END, media: null });
    expect(api.admit).not.toHaveBeenCalled();
  });

  it("admits on Play, offers one receive-only video section and is Live only on frames", async () => {
    const { session, api, peer, phases } = setup();
    session.start();
    await flush();
    session.play();
    await flush();
    expect(api.admit).toHaveBeenCalledWith("fixture-csrf", undefined);
    expect(peer().config.iceServers).toEqual([
      { urls: ["turns:relay.example.test:443?transport=tcp"], username: "u", credential: "c" },
    ]);
    expect(peer().transceivers).toEqual([{ kind: "video", init: { direction: "recvonly" } }]);
    expect(api.open).toHaveBeenCalledWith(
      "fixture-csrf",
      VIEWER,
      attempt(1),
      { type: "offer", sdp: expect.stringMatching(/^v=0\r\n/) },
      expect.any(AbortSignal),
    );
    expect(peer().remote).toEqual({ type: "answer", sdp: "v=0\r\nanswer" });
    peer().state("connected");
    peer().track("audio");
    expect(session.view.media).toBeNull();
    peer().track("video");
    expect(session.view).toMatchObject({ phase: "connecting", media: { tracks: [expect.objectContaining({ kind: "video" })] } });
    session.framesStarted({} as MediaStream);
    expect(session.view.phase).toBe("connecting");
    session.framesStarted(session.view.media!);
    expect(phases).toEqual(["checking", "ready", "connecting", "live"]);
  });

  it("reconnects the same viewer within 30 seconds and returns to Live", async () => {
    const { session, api, peers, peer, live, advance } = setup();
    await live();
    peer().state("disconnected");
    expect(session.view.phase).toBe("reconnecting");
    expect(session.view.reconnectUntil).toBe(1000 + RECONNECT_MS);
    expect(session.view.media).not.toBeNull();
    await advance(RENEGOTIATE_MS);
    expect(api.admit).toHaveBeenLastCalledWith("fixture-csrf", VIEWER);
    expect(peers).toHaveLength(2);
    expect(peers[0]!.closed).toBe(true);
    peer().state("connected");
    peer().track("video");
    expect(session.view.phase).toBe("connecting");
    session.framesStarted(session.view.media!);
    expect(session.view).toMatchObject({ phase: "live", reconnectUntil: null });
    await advance(RECONNECT_MS);
    expect(session.view.phase).toBe("live");
  });

  it("releases the place after 30 seconds without video; Retry admits anew", async () => {
    const { session, api, peer, live, advance } = setup();
    await live();
    api.admit.mockRejectedValue(new GuestError("network"));
    peer().state("failed");
    await advance(0);
    expect(api.admit).toHaveBeenLastCalledWith("fixture-csrf", VIEWER);
    await advance(RECONNECT_MS);
    expect(session.view).toMatchObject({ phase: "lost", media: null, reconnectUntil: null });
    expect(api.close).toHaveBeenCalledWith("fixture-csrf", VIEWER, attempt(1), false);
    const tries = api.admit.mock.calls.length;
    expect(tries).toBeGreaterThan(5);
    await advance(RECONNECT_MS);
    expect(api.admit).toHaveBeenCalledTimes(tries);
    api.admit.mockReset();
    session.play();
    await flush();
    expect(api.admit).toHaveBeenCalledWith("fixture-csrf", undefined);
  });

  it("asks again only once the place it gave back is free", async () => {
    const { session, api, peer, live, advance } = setup();
    await live();
    let closed: () => void = () => {};
    api.close.mockImplementationOnce(() => new Promise<void>((resolve) => (closed = resolve)));
    api.admit.mockRejectedValue(new GuestError("network"));
    peer().state("failed");
    await advance(RECONNECT_MS);
    expect(session.view.phase).toBe("lost");
    api.admit.mockReset();
    session.play();
    await flush();
    expect(api.admit).not.toHaveBeenCalled();
    closed();
    await flush();
    expect(api.admit).toHaveBeenCalledWith("fixture-csrf", undefined);
  });

  it("waits out the window while offline, then watches again once back online", async () => {
    let online = true;
    const { session, api, peer, live, advance } = setup({ online: () => online });
    await live();
    online = false;
    api.admit.mockRejectedValue(new GuestError("network"));
    peer().state("disconnected");
    await advance(RECONNECT_MS - 1);
    expect(session.view.phase).toBe("reconnecting");
    await advance(1);
    expect(session.view).toMatchObject({ phase: "offline", media: null });
    api.admit.mockReset();
    online = true;
    session.online();
    await flush();
    expect(api.admit).toHaveBeenCalledWith("fixture-csrf", undefined);
  });

  it("retries at once when the device comes back online, without replacing an attempt in progress", async () => {
    const { session, api, peer, live, advance } = setup();
    await live();
    let answer: (value: { type: "answer"; sdp: string }) => void = () => {};
    api.open.mockImplementationOnce(() => new Promise((resolve) => (answer = resolve)));
    peer().state("disconnected");
    await advance(RENEGOTIATE_MS);
    expect(api.admit).toHaveBeenCalledTimes(2);
    session.online();
    await advance(0);
    expect(api.admit).toHaveBeenCalledTimes(2);
    answer({ type: "answer", sdp: "v=0\r\nanswer" });
    await flush();
    expect(peer().remote).toEqual({ type: "answer", sdp: "v=0\r\nanswer" });
    expect(api.close).not.toHaveBeenCalled();

    api.admit.mockRejectedValueOnce(new GuestError("network"));
    peer().state("failed");
    await advance(0);
    expect(api.admit).toHaveBeenCalledTimes(3);
    session.online();
    await flush();
    expect(api.admit).toHaveBeenCalledTimes(4);
    expect(session.view.phase).toBe("reconnecting");
  });

  it("asks for a new place when the kept one is gone", async () => {
    const { session, api, peer, live, advance } = setup();
    await live();
    api.admit.mockRejectedValueOnce(new GuestError("stale_viewer_attempt"));
    peer().state("failed");
    await advance(0);
    expect(api.admit.mock.calls.slice(-2).map((call) => call[1])).toEqual([VIEWER, undefined]);
    expect(api.close).toHaveBeenCalledWith("fixture-csrf", VIEWER, attempt(1), false);
    expect(api.close.mock.invocationCallOrder[0]!).toBeLessThan(api.admit.mock.invocationCallOrder.at(-1)!);
    expect(session.view.phase).toBe("reconnecting");
  });

  it("shows a full camera with Retry and admits again on request", async () => {
    const { session, api } = setup();
    session.start();
    await flush();
    api.admit.mockRejectedValueOnce(new GuestError("capacity_reached"));
    session.play();
    await flush();
    expect(session.view.phase).toBe("full");
    session.play();
    await flush();
    expect(api.admit).toHaveBeenCalledTimes(2);
    expect(session.view.phase).toBe("connecting");
  });

  it("lets expiry and revocation override recovery and clear media", async () => {
    const revoked = setup();
    await revoked.live();
    revoked.api.admit.mockRejectedValue(new GuestError("revoked"));
    revoked.peer().state("failed");
    await revoked.advance(0);
    expect(revoked.session.view).toMatchObject({ phase: "revoked", media: null });
    expect(revoked.peer().closed).toBe(true);
    const calls = revoked.api.admit.mock.calls.length;
    await revoked.advance(RECONNECT_MS * 2);
    expect(revoked.api.admit).toHaveBeenCalledTimes(calls);
    revoked.session.play();
    expect(revoked.api.admit).toHaveBeenCalledTimes(calls);

    const expiring = setup({
      api: { redeem: vi.fn(async () => ({ expiresAt: T0 + 60000, csrf: "fixture-csrf" })) },
    });
    expiring.api.admit.mockImplementation(async () => ({
      viewerID: VIEWER,
      attemptID: attempt(1),
      expiresAt: T0 + 60000,
      iceServers: [],
    }));
    await expiring.live();
    expiring.peer().state("disconnected");
    await expiring.advance(60000);
    expect(expiring.session.view).toMatchObject({ phase: "expired", media: null, reconnectUntil: null });
    expect(expiring.peer().closed).toBe(true);
  });

  it("uses the server's clock for the deadline when this device's clock is off", async () => {
    const { session, advance } = setup({
      api: {
        redeem: vi.fn(async () => ({ expiresAt: T0 + 600000, csrf: "c", serverTime: T0 + 300000 })),
      },
    });
    session.start();
    await flush();
    await advance(299000);
    expect(session.view.phase).toBe("ready");
    await advance(1000);
    expect(session.view.phase).toBe("expired");
  });

  it("maps exchange failures to final or retryable states", async () => {
    const outcomes: [GuestError, string][] = [
      [new GuestError("expired"), "expired"],
      [new GuestError("revoked"), "revoked"],
      [new GuestError("invalid_invitation"), "denied"],
      [new GuestError("invalid_request"), "denied"],
      [new GuestError("camera_unavailable"), "unavailable"],
      [new GuestError("malformed"), "unavailable"],
    ];
    for (const [error, phase] of outcomes) {
      const { session } = setup({ api: { redeem: vi.fn(async () => Promise.reject(error)) } });
      session.start();
      await flush();
      expect(session.view.phase).toBe(phase);
    }
    let online = false;
    const offline = setup({ online: () => online });
    offline.api.redeem.mockRejectedValueOnce(new GuestError("network"));
    offline.session.start();
    await flush();
    expect(offline.session.view.phase).toBe("offline");
    online = true;
    offline.session.online();
    await flush();
    expect(offline.api.redeem).toHaveBeenCalledTimes(2);
    expect(offline.session.view.phase).toBe("ready");

    const limited = setup();
    limited.api.redeem.mockRejectedValueOnce(new GuestError("rate_limited", 1000));
    limited.session.start();
    await flush();
    expect(limited.session.view.phase).toBe("checking");
    await limited.advance(1000);
    expect(limited.session.view.phase).toBe("ready");
  });

  it("keeps the link and sends nothing when the browser cannot play WebRTC", async () => {
    const { session, env, connect } = setup({
      createPeer: null,
      userAgent: "Mozilla/5.0 (iPhone) Mobile/15E148 Instagram 300.0",
    });
    session.start();
    expect(session.view).toMatchObject({ phase: "unsupported", embedded: true });
    expect(env.replaceURL).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
  });

  it("asks for the original link when the secret is missing or malformed", () => {
    const missing = setup({ hash: "" });
    missing.session.start();
    expect(missing.session.view.phase).toBe("reopen");
    expect(missing.connect).not.toHaveBeenCalled();
    const malformed = setup({ hash: "#not-a-token" });
    malformed.session.start();
    expect(malformed.env.replaceURL).toHaveBeenCalledWith(`/s/${SHARE}`);
    expect(malformed.session.view.phase).toBe("denied");
    expect(malformed.connect).not.toHaveBeenCalled();
  });

  it("removes a re-opened link's secret and starts over only when not watching", async () => {
    const { session, env, api } = setup();
    session.start();
    await flush();
    session.relinked(`#${INVITATION}`);
    expect(env.replaceURL).toHaveBeenCalledTimes(2);
    expect(api.redeem).toHaveBeenCalledTimes(2);
    await flush();
    expect(session.view.phase).toBe("ready");
    session.relinked("#not-a-token");
    expect(env.replaceURL).toHaveBeenCalledTimes(3);
    expect(api.redeem).toHaveBeenCalledTimes(2);

    const watching = setup();
    await watching.live();
    watching.session.relinked(`#${INVITATION}`);
    expect(watching.env.replaceURL).toHaveBeenCalledTimes(2);
    expect(watching.api.redeem).toHaveBeenCalledTimes(1);
    expect(watching.session.view.phase).toBe("live");

    const embedded = setup({ createPeer: null });
    embedded.session.start();
    embedded.session.relinked(`#${INVITATION}`);
    expect(embedded.env.replaceURL).not.toHaveBeenCalled();
  });

  it("frees the place at once when the page is left", async () => {
    const { session, api, peer, live } = setup();
    await live();
    session.pagehide();
    expect(api.close).toHaveBeenCalledWith("fixture-csrf", VIEWER, attempt(1), true);
    expect(peer().closed).toBe(true);
    expect(session.view).toMatchObject({ phase: "ready", media: null });
  });

  it("releases an admission that arrives after the page moved on", async () => {
    let admit: (value: Admission) => void = () => {};
    const { session, api, peers } = setup({
      api: { admit: vi.fn(() => new Promise<Admission>((resolve) => (admit = resolve))) },
    });
    session.start();
    await flush();
    session.play();
    await flush();
    session.pagehide();
    expect(session.view.phase).toBe("ready");
    admit({ viewerID: VIEWER, attemptID: attempt(7), expiresAt: END, iceServers: [] });
    await flush();
    expect(peers).toHaveLength(0);
    expect(api.close).toHaveBeenCalledWith("fixture-csrf", VIEWER, attempt(7), false);
  });

  it("keeps watching for missing or frozen video after a short connection drop", async () => {
    const first = setup();
    first.session.start();
    await flush();
    first.session.play();
    await flush();
    first.peer().state("connected");
    first.peer().track("video");
    first.peer().state("disconnected");
    await first.advance(1000);
    first.peer().state("connected");
    expect(first.session.view.phase).toBe("connecting");
    await first.advance(FIRST_FRAME_MS);
    expect(first.session.view.phase).toBe("stalled");
    first.peer().state("disconnected");
    await first.advance(1000);
    first.peer().state("connected");
    await first.advance(FIRST_FRAME_MS);
    expect(first.session.view.phase).toBe("stalled");

    const frozen = setup();
    const track = await frozen.live();
    track.muted = true;
    track.dispatchEvent(new Event("mute"));
    frozen.peer().state("disconnected");
    await frozen.advance(1000);
    frozen.peer().state("connected");
    expect(frozen.session.view.phase).toBe("live");
    await frozen.advance(STALL_MS);
    expect(frozen.session.view.phase).toBe("stalled");
  });

  it("keeps a stopped link's state when the page returns after its end time", async () => {
    const { session, api, advance } = setup({
      api: { redeem: vi.fn(async () => ({ expiresAt: T0 + 60_000, csrf: "fixture-csrf" })) },
    });
    session.start();
    await flush();
    api.admit.mockRejectedValueOnce(new GuestError("revoked"));
    session.play();
    await flush();
    expect(session.view.phase).toBe("revoked");
    await advance(120_000);
    session.visible();
    session.pageshow();
    expect(session.view.phase).toBe("revoked");
  });

  it("offers Retry for stalled video and reconnects within the kept place", async () => {
    const { session, api, peer, advance, live } = setup();
    await live();
    const track = peer().track("video");
    session.framesStarted(session.view.media!);
    track.dispatchEvent(new Event("mute"));
    await advance(STALL_MS);
    expect(session.view.phase).toBe("stalled");
    track.dispatchEvent(new Event("unmute"));
    expect(session.view.phase).toBe("live");
    track.dispatchEvent(new Event("mute"));
    await advance(STALL_MS);
    session.play();
    await advance(0);
    expect(session.view.phase).toBe("reconnecting");
    expect(api.admit).toHaveBeenLastCalledWith("fixture-csrf", VIEWER);
  });

  it("reports video that never arrives", async () => {
    const { session, advance } = setup();
    session.start();
    await flush();
    session.play();
    await flush();
    await advance(FIRST_FRAME_MS);
    expect(session.view.phase).toBe("stalled");
  });

  it("treats a refused offer as an unsupported browser and frees the attempt", async () => {
    const { session, api } = setup();
    api.open.mockRejectedValueOnce(new GuestError("unsupported_offer"));
    session.start();
    await flush();
    session.play();
    await flush();
    expect(session.view.phase).toBe("unsupported");
    expect(api.close).toHaveBeenCalledWith("fixture-csrf", VIEWER, attempt(1), false);
  });

  it("never stores data in the browser or asks for capture", async () => {
    const getUserMedia = vi.fn();
    Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: { getUserMedia } });
    const local = vi.spyOn(Storage.prototype, "setItem");
    const cookie = vi.spyOn(document, "cookie", "set");
    const { session, peer, advance, live } = setup();
    await live();
    peer().state("failed");
    await advance(RECONNECT_MS);
    session.pagehide();
    expect(getUserMedia).not.toHaveBeenCalled();
    expect(local).not.toHaveBeenCalled();
    expect(cookie).not.toHaveBeenCalled();
    Reflect.deleteProperty(navigator, "mediaDevices");
  });

  it("recognises common in-app browsers", () => {
    expect(embeddedBrowser("Mozilla/5.0 (iPhone) [FBAN/FBIOS;FBAV/450.0]")).toBe(true);
    expect(embeddedBrowser("Mozilla/5.0 (Linux; Android 14; wv) Chrome/131.0")).toBe(true);
    expect(embeddedBrowser("Mozilla/5.0 (Linux; Android 14) Chrome/131.0 Mobile Safari/537.36")).toBe(false);
    expect(embeddedBrowser("Mozilla/5.0 (iPhone) Version/18.0 Mobile/15E148 Safari/604.1")).toBe(false);
  });
});

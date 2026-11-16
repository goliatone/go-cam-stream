import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PlaybackController,
  RECOVERY_MS,
  RETRY_MS,
  STABLE_MS,
  gather,
  type PlaybackSnapshot,
} from "./playback";
import { PlaybackError, type ViewerAPI } from "./api";
import type { Session, StreamRef, StreamStatus } from "./contracts";
class Stream {
  tracks: MediaStreamTrack[] = [];
  getTracks() {
    return this.tracks;
  }
  addTrack(track: MediaStreamTrack) {
    this.tracks.push(track);
  }
}
class Peer extends EventTarget {
  iceGatheringState = "complete";
  connectionState = "new";
  localDescription = { type: "offer", sdp: "v=0\r\ncomplete" };
  ontrack: ((event: RTCTrackEvent) => void) | null = null;
  onconnectionstatechange: (() => void) | null = null;
  addTransceiver = vi.fn();
  createOffer = vi.fn(async () => this.localDescription);
  setLocalDescription = vi.fn(async () => {});
  setRemoteDescription = vi.fn(async () => {});
  getStats = vi.fn(
    async () =>
      new Map([
        ["transport", { type: "transport", selectedCandidatePairId: "pair" }],
        ["pair", { remoteCandidateId: "remote" }],
        [
          "remote",
          { protocol: "tcp", address: "private-should-not-be-exposed" },
        ],
      ]),
  );
  close = vi.fn(() => {
    this.connectionState = "closed";
  });
  connect(state: string) {
    this.connectionState = state;
    this.onconnectionstatechange?.();
  }
}
const one = { camera_id: "one", stream_id: "default" };
const two = { camera_id: "two", stream_id: "default" };
const capabilities = { video: true, audio: true };
const answer = (ref: StreamRef, id = ref.camera_id): Session => ({
  ...ref,
  id,
  state: "connecting",
  playback: {
    protocol: "webrtc",
    answer: { type: "answer", sdp: "v=0\r\nanswer" },
  },
});
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
async function flush() {
  for (let i = 0; i < 20; i++) await Promise.resolve();
}
function setup() {
  const peers: Peer[] = [];
  const changes: (PlaybackSnapshot | undefined)[] = [];
  const api = {
    list: vi.fn(async () => ({ cameras: [] })),
    create: vi.fn(async (ref: StreamRef) => answer(ref)),
    close: vi.fn(async () => {}),
    status: vi.fn(
      async (ref: StreamRef): Promise<StreamStatus> => ({
        stream_id: ref.stream_id,
        state: "playing",
        viewers: 1,
        observed_at: "2026-11-14T12:00:00Z",
      }),
    ),
  } satisfies ViewerAPI;
  const controller = new PlaybackController(
    api,
    (v) => changes.push(v),
    () => {
      const peer = new Peer();
      peers.push(peer);
      return peer as unknown as RTCPeerConnection;
    },
  );
  return { api, controller, peers, changes, last: () => changes.at(-1) };
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("MediaStream", Stream);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("owned viewer lifecycle", () => {
  it("waits for complete ICE, uses receive-only media and never treats an answer as playing", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    await flush();
    expect(peers[0]!.addTransceiver.mock.calls).toEqual([
      ["video", { direction: "recvonly" }],
      ["audio", { direction: "recvonly" }],
    ]);
    expect(api.create).toHaveBeenCalledWith(one, "v=0\r\ncomplete");
    expect(last()!.state).toBe("connecting");
    peers[0]!.connect("connected");
    controller.playing();
    expect(last()!.state).toBe("playing");
    expect(last()!.transport).toBe("tcp");
    expect(JSON.stringify(last())).not.toContain(
      "private-should-not-be-exposed",
    );
    await controller.dispose();
  });
  it("releases a late create before starting the newest requested selection", async () => {
    const { controller, api, peers, last } = setup();
    const late = deferred<Session>();
    api.create.mockImplementationOnce(() => late.promise);
    const first = controller.select(one, capabilities);
    await flush();
    const middle = controller.select(two, capabilities);
    const newest = controller.select(
      { ...two, stream_id: "alternate" },
      capabilities,
    );
    expect(peers[0]!.close).toHaveBeenCalledOnce();
    expect(api.create).toHaveBeenCalledTimes(1);
    late.resolve(answer(one));
    await Promise.all([first, middle, newest]);
    expect(api.close).toHaveBeenCalledWith("one", false);
    expect(api.create).toHaveBeenCalledTimes(2);
    expect(api.close.mock.invocationCallOrder[0]).toBeLessThan(
      api.create.mock.invocationCallOrder[1]!,
    );
    expect(last()!.selection).toEqual({ ...two, stream_id: "alternate" });
    expect(peers[0]!.setRemoteDescription).not.toHaveBeenCalled();
    await controller.dispose();
  });
  it("withholds new allocation after failed cleanup and retries cleanup explicitly", async () => {
    const { controller, api, last } = setup();
    await controller.select(one, capabilities);
    api.close.mockRejectedValueOnce(new PlaybackError("cleanup_incomplete"));
    await controller.select(two, capabilities);
    expect(api.create).toHaveBeenCalledTimes(1);
    expect(last()!.error!.code).toBe("cleanup_incomplete");
    await controller.retry();
    expect(api.create).toHaveBeenCalledTimes(2);
    expect(api.close).toHaveBeenCalledTimes(2);
    await controller.dispose();
  });
  it("requires a page reload when the server no longer recognizes ownership", async () => {
    const { controller, api, last } = setup();
    await controller.select(one, capabilities);
    api.close.mockRejectedValue(new PlaybackError("access_denied", false));
    await controller.select(two, capabilities);
    expect(last()!.reloadRequired).toBe(true);
    expect(last()!.error!.retryable).toBe(false);
    await controller.retry();
    expect(api.create).toHaveBeenCalledOnce();
    await expect(controller.dispose()).rejects.toMatchObject({
      body: { code: "cleanup_incomplete" },
    });
  });
  it("requires a page reload when ownership is denied after a lost connection", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    api.close.mockRejectedValue(new PlaybackError("access_denied", false));
    peers[0]!.connect("failed");
    await flush();
    expect(last()).toMatchObject({
      state: "failed",
      reloadRequired: true,
      error: { code: "cleanup_incomplete", retryable: false },
    });
    await expect(controller.dispose()).rejects.toMatchObject({
      body: { code: "cleanup_incomplete" },
    });
  });
  it("requires a page reload when ownership is denied during a stop", async () => {
    const { controller, api, last } = setup();
    await controller.select(one, capabilities);
    api.close.mockRejectedValue(new PlaybackError("access_denied", false));
    await controller.stop();
    expect(last()).toMatchObject({
      state: "failed",
      reloadRequired: true,
      error: { code: "cleanup_incomplete", retryable: false },
    });
    await expect(controller.dispose()).rejects.toMatchObject({
      body: { code: "cleanup_incomplete" },
    });
  });
  it("stops tracks and closes only this page's session on removal", async () => {
    const { controller, api, peers } = setup();
    await controller.select(one, capabilities);
    const track = { stop: vi.fn() } as unknown as MediaStreamTrack;
    peers[0]!.ontrack!({ track } as RTCTrackEvent);
    await controller.select(undefined);
    expect(track.stop).toHaveBeenCalledOnce();
    expect(api.close).toHaveBeenCalledWith("one", false);
    expect(api.create).toHaveBeenCalledOnce();
    await controller.dispose();
  });
  it("cleans a late session after disposal, without publishing stale state", async () => {
    const { controller, api, changes } = setup();
    const late = deferred<Session>();
    api.create.mockImplementationOnce(() => late.promise);
    const start = controller.select(one, capabilities);
    await flush();
    const stop = controller.dispose();
    const count = changes.length;
    late.resolve(answer(one));
    await Promise.all([start, stop]);
    expect(api.close).toHaveBeenCalledOnce();
    expect(changes).toHaveLength(count);
  });
  it("does not replay uncertain session creation", async () => {
    const { controller, api, last } = setup();
    api.create.mockRejectedValueOnce(new PlaybackError("server_unavailable"));
    await controller.select(one, capabilities);
    await vi.advanceTimersByTimeAsync(120000);
    expect(api.create).toHaveBeenCalledOnce();
    expect(last()!.error!.code).toBe("server_unavailable");
    await controller.retry();
    expect(api.create).toHaveBeenCalledTimes(2);
    await controller.dispose();
  });
  it("keeps media alive beyond CSRF expiry with observation paused and no heartbeat", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    await flush();
    peers[0]!.connect("connected");
    controller.playing();
    controller.observe(false);
    const reads = api.status.mock.calls.length;
    await vi.advanceTimersByTimeAsync(35 * 60 * 1000);
    expect(last()!.state).toBe("playing");
    expect(api.status).toHaveBeenCalledTimes(reads);
    expect(api.create).toHaveBeenCalledOnce();
    expect(api.close).not.toHaveBeenCalled();
    await controller.dispose();
  });
  it("permits a connected peer to await an autoplay gesture without a viewing TTL", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    peers[0]!.connect("connected");
    controller.observe(false);
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.close).not.toHaveBeenCalled();
    expect(last()!.state).toBe("connecting");
    controller.playing();
    expect(last()!.state).toBe("playing");
    await controller.dispose();
  });
  it("allows native ICE recovery, then releases a connection that failed", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    peers[0]!.connect("connected");
    controller.playing();
    peers[0]!.connect("disconnected");
    expect(last()!.state).toBe("reconnecting");
    peers[0]!.connect("connected");
    controller.playing();
    await vi.advanceTimersByTimeAsync(16000);
    expect(api.create).toHaveBeenCalledOnce();
    peers[0]!.connect("failed");
    await flush();
    // A lost connection can be tried again.
    expect(last()!.error).toEqual({
      code: "media_connection_failed",
      message: "",
      retryable: true,
    });
    expect(api.close).toHaveBeenCalledOnce();
    expect(api.create).toHaveBeenCalledOnce();
    await controller.dispose();
  });
  it("gives each disconnection its own recovery window", async () => {
    const { controller, peers, last } = setup();
    await controller.select(one, capabilities);
    peers[0]!.connect("connected");
    controller.playing();
    peers[0]!.connect("disconnected");
    await vi.advanceTimersByTimeAsync(10000);
    peers[0]!.connect("connected");
    controller.playing();
    peers[0]!.connect("disconnected");
    await vi.advanceTimersByTimeAsync(5000);
    expect(last()!.state).toBe("reconnecting");
    await vi.advanceTimersByTimeAsync(9000);
    expect(last()!.state).toBe("reconnecting");
    await vi.advanceTimersByTimeAsync(1000);
    expect(last()!.error!.code).toBe("media_connection_failed");
    await controller.dispose();
  });
  it("publishes when each disconnection fails unless it recovers", async () => {
    const { controller, peers, last } = setup();
    await controller.select(one, capabilities);
    peers[0]!.connect("connected");
    controller.playing();
    expect(last()!.recoverBy).toBeUndefined();
    peers[0]!.connect("disconnected");
    expect(last()!.state).toBe("reconnecting");
    expect(last()!.recoverBy).toBe(performance.now() + RECOVERY_MS);
    await vi.advanceTimersByTimeAsync(5000);
    // Recovered: no window is open, so no deadline remains to show.
    peers[0]!.connect("connected");
    expect(last()!.recoverBy).toBeUndefined();
    controller.playing();
    expect(last()!.state).toBe("playing");
    // A stalled element has no deadline; only a dropped peer does.
    controller.waiting();
    expect(last()!.state).toBe("reconnecting");
    expect(last()!.recoverBy).toBeUndefined();
    controller.playing();
    peers[0]!.connect("disconnected");
    const deadline = performance.now() + RECOVERY_MS;
    expect(last()!.recoverBy).toBe(deadline);
    // A repeated disconnected event keeps the same window.
    await vi.advanceTimersByTimeAsync(3000);
    peers[0]!.connect("disconnected");
    expect(last()!.recoverBy).toBe(deadline);
    await vi.advanceTimersByTimeAsync(RECOVERY_MS - 3000);
    expect(last()!.error!.code).toBe("media_connection_failed");
    expect(last()!.recoverBy).toBeUndefined();
    await controller.dispose();
  });
  it("counts only newer observations of a known state as updates", async () => {
    const { controller, api, peers, last } = setup();
    // A healthy server observes continuously, so each answer is newer.
    const healthy = async (ref: StreamRef): Promise<StreamStatus> => ({
      stream_id: ref.stream_id,
      state: "playing",
      viewers: 1,
      observed_at: new Date().toISOString(),
    });
    api.status.mockImplementation(healthy);
    const start = performance.now();
    await controller.select(one, capabilities);
    await flush();
    peers[0]!.connect("connected");
    expect(last()!.observingSince).toBe(start);
    expect(last()!.statusAt).toBe(start);
    await vi.advanceTimersByTimeAsync(2000);
    await flush();
    expect(last()!.statusAt).toBe(start + 2000);
    const seen = last()!.status!.observed_at;
    // A failed request keeps the time of the last update, so the page can
    // tell how long updates have stopped.
    api.status.mockRejectedValue(new PlaybackError("server_unavailable"));
    await vi.advanceTimersByTimeAsync(2000);
    await flush();
    expect(last()!.status!.state).toBe("unknown");
    expect(last()!.statusAt).toBe(start + 2000);
    // A server whose observation went stale answers unknown with its old
    // time. The same observation again, an unknown state or no observation
    // is no update either.
    for (const answer of [
      { state: "unknown", viewers: null, observed_at: seen },
      { state: "playing", viewers: 1, observed_at: seen },
      { state: "unknown", viewers: null, observed_at: "2026-11-15T09:00:00Z" },
      { state: "unknown", viewers: null, observed_at: null },
    ] as const) {
      api.status.mockResolvedValue({ stream_id: "default", ...answer });
      await vi.advanceTimersByTimeAsync(2000);
      await flush();
      expect(last()!.status!.state).toBe(answer.state);
      expect(last()!.statusAt).toBe(start + 2000);
    }
    api.status.mockImplementation(healthy);
    await vi.advanceTimersByTimeAsync(2000);
    await flush();
    // Two answers, the failure and four non-updates later: 14 s in.
    expect(last()!.statusAt).toBe(start + 14000);
    // A hidden page does not ask; shown again, its window starts afresh.
    controller.observe(false);
    expect(last()!.observingSince).toBeUndefined();
    await vi.advanceTimersByTimeAsync(30000);
    controller.observe(true);
    expect(last()!.observingSince).toBe(performance.now());
    // Stopping ends observation and its times.
    await controller.stop();
    expect(last()!.state).toBe("closed");
    expect(last()!.observingSince).toBeUndefined();
    expect(last()!.statusAt).toBeUndefined();
    await controller.dispose();
  });
  it("starts the window only once the page asks for status", async () => {
    const { controller, api, last } = setup();
    controller.observe(false);
    await controller.select(one, capabilities);
    await flush();
    expect(api.status).not.toHaveBeenCalled();
    expect(last()!.observingSince).toBeUndefined();
    controller.observe(true);
    expect(last()!.observingSince).toBe(performance.now());
    await controller.dispose();
  });
  it("retains healthy playback during a read-only control-plane outage", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    peers[0]!.connect("connected");
    controller.playing();
    api.status.mockRejectedValue(new PlaybackError("server_unavailable"));
    await vi.advanceTimersByTimeAsync(2500);
    expect(last()!.state).toBe("playing");
    expect(last()!.status!.viewers).toBeNull();
    expect(api.close).not.toHaveBeenCalled();
    await controller.dispose();
  });
  it("stops a view, frees its session and starts it again only on request", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    peers[0]!.connect("connected");
    controller.playing();
    const track = { stop: vi.fn() } as unknown as MediaStreamTrack;
    peers[0]!.ontrack!({ track } as RTCTrackEvent);
    const stopping = controller.stop();
    expect(last()).toMatchObject({ selection: one, state: "closing", media: null });
    expect(peers[0]!.close).toHaveBeenCalledOnce();
    expect(track.stop).toHaveBeenCalledOnce();
    await stopping;
    expect(api.close).toHaveBeenCalledWith("one", false);
    expect(last()).toMatchObject({ selection: one, state: "closed", media: null });
    // Stopped means stopped: no observation, no media events, no new session.
    const reads = api.status.mock.calls.length;
    controller.playing();
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.status).toHaveBeenCalledTimes(reads);
    expect(api.create).toHaveBeenCalledOnce();
    expect(last()!.state).toBe("closed");
    await controller.retry();
    expect(api.create).toHaveBeenCalledTimes(2);
    expect(last()).toMatchObject({ selection: one, state: "connecting" });
    await controller.dispose();
  });
  it("stops a view that is still connecting once its late create is released", async () => {
    const { controller, api, peers, last } = setup();
    const late = deferred<Session>();
    api.create.mockImplementationOnce(() => late.promise);
    const start = controller.select(one, capabilities);
    await flush();
    const stopping = controller.stop();
    expect(last()!.state).toBe("closing");
    late.resolve(answer(one));
    await Promise.all([start, stopping]);
    expect(api.close).toHaveBeenCalledOnce();
    expect(peers[0]!.setRemoteDescription).not.toHaveBeenCalled();
    expect(last()!.state).toBe("closed");
    await controller.dispose();
  });
  it("reports a stop whose cleanup failed instead of claiming the slot is free", async () => {
    const { controller, api, last } = setup();
    await controller.select(one, capabilities);
    api.close.mockRejectedValueOnce(new PlaybackError("cleanup_incomplete"));
    await controller.stop();
    expect(last()).toMatchObject({
      state: "failed",
      error: { code: "cleanup_incomplete", retryable: true },
    });
    await controller.retry();
    expect(api.close).toHaveBeenCalledTimes(2);
    expect(api.create).toHaveBeenCalledTimes(2);
    await controller.dispose();
  });
  it("stops the viewer after permission revocation without reconnecting upstream", async () => {
    const { controller, api, last } = setup();
    await controller.select(one, capabilities);
    api.status.mockRejectedValue(new PlaybackError("access_denied", false));
    await vi.advanceTimersByTimeAsync(2500);
    await flush();
    expect(last()!.error!.code).toBe("access_denied");
    expect(api.close).toHaveBeenCalledOnce();
    expect(api.create).toHaveBeenCalledOnce();
    await controller.dispose();
  });
});
describe("automatic retry of lost connections", () => {
  // Plays the newest peer's video, then loses its connection.
  async function lose(peers: Peer[], controller: PlaybackController) {
    const peer = peers.at(-1)!;
    peer.connect("connected");
    controller.playing();
    peer.connect("failed");
    await flush();
  }
  it("tries a lost connection again on its own, twice, then leaves it to Try again", async () => {
    // The policy: 3 s, then 10 s; a minute of steady video earns a new pair.
    expect(RETRY_MS).toEqual([3000, 10000]);
    expect(STABLE_MS).toBe(60000);
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    await lose(peers, controller);
    // The lost session is released at once; the retry waits its delay.
    expect(api.close).toHaveBeenCalledWith("one", false);
    expect(last()).toMatchObject({
      state: "failed",
      error: { code: "media_connection_failed" },
      retryAt: performance.now() + RETRY_MS[0]!,
    });
    await vi.advanceTimersByTimeAsync(RETRY_MS[0]! - 1);
    expect(api.create).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(1);
    expect(api.create).toHaveBeenCalledTimes(2);
    expect(last()!.state).toBe("connecting");
    expect(last()!.retryAt).toBeUndefined();
    // The retry plays, then stays disconnected past its recovery window.
    peers[1]!.connect("connected");
    controller.playing();
    peers[1]!.connect("disconnected");
    await vi.advanceTimersByTimeAsync(RECOVERY_MS);
    expect(last()!.retryAt).toBe(performance.now() + RETRY_MS[1]!);
    await vi.advanceTimersByTimeAsync(RETRY_MS[1]!);
    expect(api.create).toHaveBeenCalledTimes(3);
    // A retry that never connects is lost too, and no retries remain.
    await vi.advanceTimersByTimeAsync(20000);
    expect(last()!.error!.code).toBe("media_connection_failed");
    expect(last()!.retryAt).toBeUndefined();
    await vi.advanceTimersByTimeAsync(120000);
    expect(api.create).toHaveBeenCalledTimes(3);
    expect(api.close).toHaveBeenCalledTimes(3);
    // Try again is the viewer's own and earns new retries.
    await controller.retry();
    await lose(peers, controller);
    expect(last()!.retryAt).toBe(performance.now() + RETRY_MS[0]!);
    // Try again during the countdown starts at once, and only once.
    await controller.retry();
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.create).toHaveBeenCalledTimes(5);
    await controller.dispose();
  });
  it("counts an automatic retry that cannot connect as lost", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    await lose(peers, controller);
    await vi.advanceTimersByTimeAsync(RETRY_MS[0]!);
    expect(api.create).toHaveBeenCalledTimes(2);
    // The retry never connects; its 20 s deadline is a lost connection.
    await vi.advanceTimersByTimeAsync(20000);
    expect(last()).toMatchObject({
      error: { code: "media_connection_failed" },
      retryAt: performance.now() + RETRY_MS[1]!,
    });
    await vi.advanceTimersByTimeAsync(RETRY_MS[1]!);
    expect(api.create).toHaveBeenCalledTimes(3);
    // The second retry's peer fails before it plays: lost, but none remain.
    peers[2]!.connect("failed");
    await flush();
    expect(last()!.retryAt).toBeUndefined();
    await vi.advanceTimersByTimeAsync(120000);
    expect(api.create).toHaveBeenCalledTimes(3);
    await controller.dispose();
  });
  it("starts a retry only once the old session's release is confirmed", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    // A release slower than the delay, which then fails.
    const slow = deferred<void>();
    api.close.mockImplementationOnce(() => slow.promise);
    await lose(peers, controller);
    expect(last()!.retryAt).toBe(performance.now() + RETRY_MS[0]!);
    await vi.advanceTimersByTimeAsync(RETRY_MS[0]! + 2000);
    expect(api.create).toHaveBeenCalledOnce();
    slow.reject(new PlaybackError("cleanup_incomplete"));
    await flush();
    expect(last()!.error!.code).toBe("cleanup_incomplete");
    expect(last()!.retryAt).toBeUndefined();
    // Neither the cleanup nor the view is repeated without the viewer.
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.close).toHaveBeenCalledOnce();
    expect(api.create).toHaveBeenCalledOnce();
    await controller.retry();
    expect(api.close).toHaveBeenCalledTimes(2);
    expect(api.create).toHaveBeenCalledTimes(2);
    // A slow release that succeeds starts the overdue retry at once, unless
    // the page was hidden meanwhile; then it waits to be shown.
    const late = deferred<void>();
    api.close.mockImplementationOnce(() => late.promise);
    await lose(peers, controller);
    await vi.advanceTimersByTimeAsync(RETRY_MS[0]! + 2000);
    controller.observe(false);
    late.resolve();
    await flush();
    await vi.advanceTimersByTimeAsync(0);
    expect(api.create).toHaveBeenCalledTimes(2);
    controller.observe(true);
    await flush();
    expect(api.create).toHaveBeenCalledTimes(3);
    const slower = deferred<void>();
    api.close.mockImplementationOnce(() => slower.promise);
    await lose(peers, controller);
    await vi.advanceTimersByTimeAsync(RETRY_MS[1]! + 2000);
    slower.resolve();
    await flush();
    await vi.advanceTimersByTimeAsync(0);
    expect(api.create).toHaveBeenCalledTimes(4);
    // Try again during a slow release supersedes a pending retry: it runs
    // once the release is confirmed, and no automatic one follows.
    await controller.retry();
    const slowest = deferred<void>();
    api.close.mockImplementationOnce(() => slowest.promise);
    await lose(peers, controller);
    expect(last()!.retryAt).toBe(performance.now() + RETRY_MS[0]!);
    const again = controller.retry();
    await vi.advanceTimersByTimeAsync(RETRY_MS[0]! + 2000);
    expect(api.create).toHaveBeenCalledTimes(5);
    slowest.resolve();
    await again;
    expect(api.create).toHaveBeenCalledTimes(6);
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.create).toHaveBeenCalledTimes(6);
    await controller.dispose();
  });
  it("leaves video that never played, and other failures, to Try again", async () => {
    const { controller, api, peers, last } = setup();
    // Connected but never played, as when a page waits for an autoplay tap.
    await controller.select(one, capabilities);
    peers[0]!.connect("connected");
    peers[0]!.connect("failed");
    await flush();
    expect(last()!.error!.code).toBe("media_connection_failed");
    expect(last()!.retryAt).toBeUndefined();
    // A media element error is not a lost connection.
    await controller.retry();
    peers[1]!.connect("connected");
    controller.playing();
    controller.mediaError();
    await flush();
    expect(last()!.error).toEqual({
      code: "media_connection_failed",
      message: "",
      retryable: true,
    });
    expect(last()!.retryAt).toBeUndefined();
    // Nor is a peer that reports closed: only this page closes its peers.
    await controller.retry();
    peers.at(-1)!.connect("connected");
    controller.playing();
    peers.at(-1)!.connect("closed");
    await flush();
    expect(last()!.error!.code).toBe("media_connection_failed");
    expect(last()!.retryAt).toBeUndefined();
    // Nor is a fault the server reports.
    await controller.retry();
    peers.at(-1)!.connect("connected");
    controller.playing();
    api.status.mockResolvedValueOnce({
      stream_id: "default",
      state: "failed",
      viewers: null,
      observed_at: null,
      error: { code: "source_unavailable", message: "", retryable: false },
    });
    await vi.advanceTimersByTimeAsync(2000);
    await flush();
    expect(last()!.error!.code).toBe("source_unavailable");
    expect(last()!.retryAt).toBeUndefined();
    // An automatic retry whose create fails, even one that may have reached
    // the server, ends the retries.
    await controller.retry();
    await lose(peers, controller);
    api.create.mockRejectedValueOnce(new PlaybackError("deadline_exceeded"));
    await vi.advanceTimersByTimeAsync(RETRY_MS[0]!);
    expect(last()!.error!.code).toBe("deadline_exceeded");
    expect(last()!.retryAt).toBeUndefined();
    await vi.advanceTimersByTimeAsync(120000);
    expect(api.create).toHaveBeenCalledTimes(6);
    await controller.dispose();
  });
  it("waits for a visible page and yields to Stop, a new selection and failed cleanup", async () => {
    const { controller, api, peers, last } = setup();
    await controller.select(one, capabilities);
    await lose(peers, controller);
    // A hidden page does not take a viewer slot again; shown, it tries at once.
    controller.observe(false);
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.create).toHaveBeenCalledOnce();
    expect(last()!.state).toBe("failed");
    controller.observe(true);
    await flush();
    expect(api.create).toHaveBeenCalledTimes(2);
    // Once it has started, showing the page again starts nothing more.
    peers[1]!.connect("connected");
    controller.playing();
    controller.observe(false);
    controller.observe(true);
    await flush();
    expect(api.create).toHaveBeenCalledTimes(2);
    // Stop ends a pending retry.
    await lose(peers, controller);
    await controller.stop();
    await vi.advanceTimersByTimeAsync(60000);
    expect(last()!.state).toBe("closed");
    expect(api.create).toHaveBeenCalledTimes(2);
    // So does choosing another camera.
    await controller.retry();
    await lose(peers, controller);
    await controller.select(two, capabilities);
    const peer = peers.at(-1)!;
    peer.connect("connected");
    controller.playing();
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.create).toHaveBeenCalledTimes(4);
    expect(last()).toMatchObject({ selection: two, state: "playing" });
    // Cleanup that failed is reported, and nothing is tried on its own.
    api.close.mockRejectedValueOnce(new PlaybackError("cleanup_incomplete"));
    peer.connect("failed");
    await flush();
    expect(last()!.error!.code).toBe("cleanup_incomplete");
    expect(last()!.retryAt).toBeUndefined();
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.create).toHaveBeenCalledTimes(4);
    // Closing the page leaves nothing to start.
    await controller.retry();
    await lose(peers, controller);
    await controller.dispose();
    controller.observe(false);
    controller.observe(true);
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.create).toHaveBeenCalledTimes(5);
  });
  it("earns new retries after a minute of steady video", async () => {
    const { controller, peers, last } = setup();
    // Plays the newest peer's video for `ms`, calling playing() again at the
    // end as the element's timeupdate events do.
    const play = async (ms: number) => {
      peers.at(-1)!.connect("connected");
      controller.playing();
      await vi.advanceTimersByTimeAsync(ms);
      controller.playing();
    };
    // A drop that recovers, and one that does not.
    const blip = async () => {
      peers.at(-1)!.connect("disconnected");
      await vi.advanceTimersByTimeAsync(5000);
    };
    const fail = async () => {
      peers.at(-1)!.connect("failed");
      await flush();
    };
    await controller.select(one, capabilities);
    // Two quick losses use both retries.
    for (const delay of RETRY_MS) {
      await play(0);
      await fail();
      expect(last()!.retryAt).toBe(performance.now() + delay);
      await vi.advanceTimersByTimeAsync(delay);
    }
    // The longest stretch counts: a minute, a recovered drop, then 10 s.
    await play(STABLE_MS);
    await blip();
    await play(10000);
    await fail();
    expect(last()!.retryAt).toBe(performance.now() + RETRY_MS[0]!);
    await vi.advanceTimersByTimeAsync(RETRY_MS[0]!);
    // Stretches split by a drop do not add up, and the outage before a loss
    // is not video.
    await play(40000);
    await blip();
    await play(40000);
    peers.at(-1)!.connect("disconnected");
    await vi.advanceTimersByTimeAsync(RECOVERY_MS);
    expect(last()!.retryAt).toBe(performance.now() + RETRY_MS[1]!);
    await vi.advanceTimersByTimeAsync(RETRY_MS[1]!);
    // Just under a minute is not enough, and no retries remain.
    await play(STABLE_MS - 1);
    await fail();
    expect(last()!.retryAt).toBeUndefined();
    await controller.dispose();
  });
});
it("cancels ICE gathering without allocating a server session", async () => {
  const peer = new Peer();
  peer.iceGatheringState = "gathering";
  const abort = new AbortController();
  const promise = gather(peer as unknown as RTCPeerConnection, abort.signal);
  abort.abort();
  await expect(promise).rejects.toMatchObject({ body: { code: "canceled" } });
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PlaybackController,
  RECOVERY_MS,
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
  it("allows native ICE recovery, but requires explicit retry after failure", async () => {
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
    expect(last()!.error!.code).toBe("media_connection_failed");
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
    expect(last()!.recoverBy).toBe(Date.now() + RECOVERY_MS);
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
    const deadline = Date.now() + RECOVERY_MS;
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
  it("tells when status last arrived and since when the page asks", async () => {
    const { controller, api, peers, last } = setup();
    const start = Date.now();
    await controller.select(one, capabilities);
    await flush();
    peers[0]!.connect("connected");
    expect(last()!.observingSince).toBe(start);
    expect(last()!.statusAt).toBe(start);
    await vi.advanceTimersByTimeAsync(2000);
    await flush();
    expect(last()!.statusAt).toBe(start + 2000);
    // A failed request keeps the time of the last answer, so the page can
    // tell how long updates have stopped.
    api.status.mockRejectedValue(new PlaybackError("server_unavailable"));
    await vi.advanceTimersByTimeAsync(2000);
    await flush();
    expect(last()!.status!.state).toBe("unknown");
    expect(last()!.statusAt).toBe(start + 2000);
    // An answer without an observation is not an update.
    api.status.mockResolvedValue({
      stream_id: "default",
      state: "unknown",
      viewers: null,
      observed_at: null,
    });
    await vi.advanceTimersByTimeAsync(2000);
    await flush();
    expect(last()!.status!.observed_at).toBeNull();
    expect(last()!.statusAt).toBe(start + 2000);
    // A hidden page does not ask; shown again, its window starts afresh.
    controller.observe(false);
    expect(last()!.observingSince).toBeUndefined();
    await vi.advanceTimersByTimeAsync(30000);
    controller.observe(true);
    expect(last()!.observingSince).toBe(Date.now());
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
    expect(last()!.observingSince).toBe(Date.now());
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
it("cancels ICE gathering without allocating a server session", async () => {
  const peer = new Peer();
  peer.iceGatheringState = "gathering";
  const abort = new AbortController();
  const promise = gather(peer as unknown as RTCPeerConnection, abort.signal);
  abort.abort();
  await expect(promise).rejects.toMatchObject({ body: { code: "canceled" } });
});

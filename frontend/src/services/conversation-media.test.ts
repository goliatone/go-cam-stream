import { afterEach, expect, test, vi } from "vitest";
import { captureConversation, ConversationMedia } from "./conversation-media";
import type { RoomAdmission, RoomView } from "./contracts";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

test("capture asks for exact selected devices and call audio processing", async () => {
  const getUserMedia = vi.fn().mockResolvedValue({});
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
  await captureConversation({ camera: "chosen-camera", microphone: "chosen-mic" });
  expect(getUserMedia).toHaveBeenCalledWith({
    video: { deviceId: { exact: "chosen-camera" } },
    audio: {
      echoCancellation: true,
      autoGainControl: true,
      noiseSuppression: true,
      deviceId: { exact: "chosen-mic" },
    },
  });
});

test("prejoin has no peer and owner loss stops negotiated capture", async () => {
  vi.useFakeTimers();
  const room: RoomView = {
    room_id: "A".repeat(43), version: 1, generation: 1, state: "waiting",
    expires_at: "2026-11-19T16:00:00Z", participants: [],
  };
  const admission: RoomAdmission = { room, role: "host", token: "fixture-token" };
  let ownerAlive = true;
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    if (!ownerAlive) throw new TypeError("owner went offline");
    if (path.endsWith("/join")) return new Response(JSON.stringify({ room, relay_only: false }));
    return new Response(JSON.stringify({ room }));
  });
  const senders = [{ replaceTrack: vi.fn(async () => {}) }, { replaceTrack: vi.fn(async () => {}) }];
  const close = vi.fn();
  const pc = {
    close, connectionState: "new",
    addTransceiver: vi.fn().mockReturnValueOnce({ sender: senders[0] }).mockReturnValueOnce({ sender: senders[1] }),
  };
  const makePeer = vi.fn(() => pc as unknown as RTCPeerConnection);
  const audio = { kind: "audio", enabled: true, readyState: "live", stop: vi.fn(), addEventListener: vi.fn() };
  const tracks = [audio];
  const local = {
    getTracks: () => tracks.slice(),
    addTrack: (track: typeof audio) => tracks.push(track),
    removeTrack: (track: typeof audio) => tracks.splice(tracks.indexOf(track), 1),
  };
  const failure = vi.fn();
  const session = new ConversationMedia(admission, vi.fn(), failure, {
    now: Date.now, fetch: fetcher as typeof fetch, peer: makePeer,
    stream: () => ({ addTrack: vi.fn() }) as unknown as MediaStream,
  });
  session.start();
  await vi.advanceTimersByTimeAsync(500);
  expect(makePeer).not.toHaveBeenCalled();
  session.join(local as unknown as MediaStream);
  await vi.advanceTimersByTimeAsync(500);
  expect(makePeer).toHaveBeenCalledOnce();
  const incompatibleAudio = { ...audio, stop: vi.fn() };
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: async () => ({ getTracks: () => [incompatibleAudio] }) } });
  senders[0]?.replaceTrack.mockRejectedValueOnce(new DOMException("incompatible", "InvalidModificationError"));
  await expect(session.selectInput("audio", "incompatible-mic")).rejects.toThrow("incompatible");
  expect(tracks).toEqual([audio]);
  expect(audio.stop).not.toHaveBeenCalled();
  expect(incompatibleAudio.stop).toHaveBeenCalledOnce();
  session.removeInput("audio");
  expect(audio.stop).toHaveBeenCalledOnce();
  expect(tracks).toEqual([]);
  await vi.advanceTimersByTimeAsync(500);
  expect(senders[0]?.replaceTrack).toHaveBeenCalledWith(null);
  expect(JSON.parse(String(fetcher.mock.calls.find(([path]) => path.endsWith("/publication"))?.[1]?.body)).publication)
    .toEqual({ camera: false, microphone: false });
  const nextAudio = { ...audio, enabled: true, stop: vi.fn() };
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: async () => ({ getTracks: () => [nextAudio] }) } });
  expect(await session.selectInput("audio", "replacement-mic")).toBe(true);
  expect(nextAudio.enabled).toBe(true);
  expect(audio.stop).toHaveBeenCalledOnce();
  await vi.advanceTimersByTimeAsync(500);
  expect(JSON.parse(String(fetcher.mock.calls.filter(([path]) => path.endsWith("/publication")).at(-1)?.[1]?.body)).publication)
    .toEqual({ camera: false, microphone: true });
  ownerAlive = false;
  await vi.advanceTimersByTimeAsync(13_000);
  expect(close).toHaveBeenCalledOnce();
  expect(nextAudio.stop).toHaveBeenCalledOnce();
  expect(failure).toHaveBeenCalledWith("media_unreachable");
});

test("successful polling does not mask a persistent media setup failure", async () => {
  vi.useFakeTimers();
  const room: RoomView = {
    room_id: "A".repeat(43), version: 1, generation: 1, state: "waiting",
    expires_at: "2026-11-19T16:00:00Z", participants: [],
  };
  const fetcher = vi.fn(async (path: string) => path.endsWith("/join")
    ? new Response(JSON.stringify({ error: { code: "media_unreachable" } }), { status: 503 })
    : new Response(JSON.stringify({ room })));
  const failure = vi.fn();
  const audio = { kind: "audio", enabled: true, readyState: "live", stop: vi.fn(), addEventListener: vi.fn() };
  const session = new ConversationMedia({ room, role: "host", token: "fixture-token" }, vi.fn(), failure, {
    now: Date.now, fetch: fetcher as typeof fetch, peer: vi.fn(), stream: vi.fn(),
  });
  session.join({ getTracks: () => [audio] } as unknown as MediaStream);
  await vi.advanceTimersByTimeAsync(13_000);
  expect(audio.stop).toHaveBeenCalledOnce();
  expect(failure).toHaveBeenCalledExactlyOnceWith("media_unreachable");
  expect(fetcher.mock.calls.some(([path]) => path.endsWith("/leave"))).toBe(true);
});

test("a lost signal response replays the same SDP after late ICE candidates", async () => {
  vi.useFakeTimers();
  const room: RoomView = {
    room_id: "A".repeat(43), version: 1, generation: 1, state: "joining",
    expires_at: "2026-11-23T16:00:00Z",
    participants: [
      { role: "host", joined: true, publication: { camera: false, microphone: false } },
      { role: "guest", joined: true, publication: { camera: false, microphone: false } },
    ],
  };
  const peer = {
    iceGatheringState: "gathering", connectionState: "new",
    localDescription: null as RTCSessionDescriptionInit | null,
    createOffer: async (): Promise<RTCSessionDescriptionInit> => ({type: "offer", sdp: "v=0\r\n"}),
    setLocalDescription: async (description: RTCSessionDescriptionInit) => { peer.localDescription = description; },
    addEventListener: vi.fn(), removeEventListener: vi.fn(), close: vi.fn(),
    addTransceiver: () => ({sender: {}}),
  };
  const signals: string[] = [];
  const fetcher = vi.fn(async (path: string, request?: RequestInit) => {
    if (path.endsWith("/join")) return new Response(JSON.stringify({ room, relay_only: false }));
    if (path.endsWith("/signal")) {
      signals.push(JSON.parse(String(request?.body)).sdp);
      if (signals.length === 1) {
        peer.localDescription = {type: "offer", sdp: "v=0\r\na=end-of-candidates\r\n"};
        peer.iceGatheringState = "complete";
        throw new TypeError("response lost after the server stored the offer");
      }
      if (signals.at(-1) !== signals[0]) {
        return new Response(JSON.stringify({error: {code: "permission_denied"}}), {status: 403});
      }
    }
    return new Response(JSON.stringify({room}));
  });
  const failure = vi.fn();
  const session = new ConversationMedia({room, role: "host", token: "fixture-token"}, vi.fn(), failure, {
    now: Date.now, fetch: fetcher as typeof fetch,
    peer: () => peer as unknown as RTCPeerConnection,
    stream: () => ({} as MediaStream),
  });
  session.join({getTracks: () => []} as unknown as MediaStream);
  await vi.advanceTimersByTimeAsync(5000);
  expect(signals).toHaveLength(2);
  expect(signals[1]).toEqual(signals[0]);
  expect(failure).not.toHaveBeenCalled();
});

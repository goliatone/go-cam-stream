import {
  CONVERSATION_API_PREFIX,
  type JoinedRoom,
  type RoomAdmission,
  type RoomPoll,
  type RoomPublication,
  type RoomView,
} from "./contracts";
import { gather } from "./playback";

export type ConversationInput = { camera: boolean | string; microphone: boolean | string };
export type InputKind = "audio" | "video";

export async function captureConversation(input: ConversationInput): Promise<MediaStream> {
  const audio = input.microphone ? {
    echoCancellation: true,
    noiseSuppression: true,
    autoGainControl: true,
    ...(typeof input.microphone === "string" ? { deviceId: { exact: input.microphone } } : {}),
  } : false;
  const video = typeof input.camera === "string" ? { deviceId: { exact: input.camera } } : input.camera;
  return audio || video ? navigator.mediaDevices.getUserMedia({ audio, video }) : new MediaStream();
}

export type ConversationMediaUpdate = {
  room: RoomView;
  incoming: MediaStream | null;
  incomingAudio: boolean | null;
};

type AudioSample = { id: string; packets: number; energy: number; duration: number };

type MediaEnvironment = {
  fetch: typeof fetch;
  now(): number;
  peer(config: RTCConfiguration): RTCPeerConnection;
  stream(): MediaStream;
};

const browser: MediaEnvironment = {
  fetch: (input, init) => fetch(input, init),
  now: () => performance.now(),
  peer: (config) => new RTCPeerConnection(config),
  stream: () => new MediaStream(),
};

class RoomRequestError extends Error {
  constructor(readonly code: string, readonly status: number) {
    super(code);
  }
}

/** Owns one admitted endpoint. Start polling in prejoin. Only Join publishes. */
export class ConversationMedia {
  private readonly abort = new AbortController();
  private readonly base: string;
  private room: RoomView;
  private local: MediaStream | null = null;
  private incoming: MediaStream | null = null;
  private peer: RTCPeerConnection | null = null;
  private senders: RTCRtpSender[] = [];
  private timer: ReturnType<typeof setTimeout> | undefined;
  private lastContact: number;
  private running = false;
  private offered = false;
  private answered = false;
  private outboundSignal: RTCSessionDescriptionInit | undefined;
  private reportedConnected = false;
  private disconnectedSince: number | undefined;
  private pairingSince: number | undefined;
  private pendingPublication = false;
  private inputRevision = { audio: 0, video: 0 };
  private inputUpdate: Promise<unknown> = Promise.resolve();
  private audioSample: AudioSample | undefined;
  private incomingAudio: boolean | null = null;
  private samplingPeer: RTCPeerConnection | null = null;

  constructor(
    private readonly admission: RoomAdmission,
    private readonly update: (value: ConversationMediaUpdate) => void,
    private readonly failure: (code: string) => void,
    private readonly env: MediaEnvironment = browser,
  ) {
    this.room = admission.room;
    this.base = `${CONVERSATION_API_PREFIX}/${encodeURIComponent(admission.room.room_id)}`;
    this.lastContact = env.now();
  }

  start() {
    if (this.running || this.abort.signal.aborted) return;
    this.running = true;
    void this.tick();
  }

  join(stream: MediaStream) {
    if (this.local === stream) return;
    if (this.abort.signal.aborted || this.local) {
      stream.getTracks().forEach((track) => track.stop());
      return;
    }
    this.local = stream;
    for (const track of stream.getTracks()) {
      track.addEventListener("ended", () => { this.pendingPublication = true; });
    }
    this.start();
  }

  removeInput(kind: InputKind) {
    this.inputRevision[kind]++;
    const stream = this.local;
    if (!stream) return;
    for (const track of stream.getTracks().filter((item) => item.kind === kind)) {
      track.stop();
      stream.removeTrack(track);
    }
    this.pendingPublication = true;
  }

  async selectInput(kind: InputKind, deviceID: string | true) {
    const stream = this.local;
    if (!stream || !deviceID || this.abort.signal.aborted) return false;
    const revision = ++this.inputRevision[kind];
    const choice = kind === "audio" ? { microphone: deviceID, camera: false } : { camera: deviceID, microphone: false };
    const replacement = await captureConversation(choice);
    const track = replacement.getTracks().find((item) => item.kind === kind);
    let adopted = false;
    try {
      return await this.updateInputs(async () => {
        const current = () => !this.abort.signal.aborted && revision === this.inputRevision[kind];
        if (!track || !current()) return false;
        const peer = this.peer;
        const sender = this.senders[kind === "audio" ? 0 : 1];
        if (sender) {
          try { await sender.replaceTrack(track); }
          catch (error) { if (peer === this.peer) throw error; }
        }
        this.pendingPublication = true;
        if (!current()) return false;
        for (const previous of stream.getTracks()) {
          if (previous.kind === kind) {
            stream.removeTrack(previous);
            previous.stop();
          }
        }
        track.addEventListener("ended", () => { this.pendingPublication = true; });
        stream.addTrack(track);
        adopted = true;
        return true;
      });
    } finally {
      replacement.getTracks().forEach((item) => { if (!adopted || item !== track) item.stop(); });
    }
  }

  private updateInputs<T>(update: () => Promise<T>): Promise<T> {
    const operation = this.inputUpdate.then(update);
    this.inputUpdate = operation.catch(() => {});
    return operation;
  }

  private syncSenders() {
    return this.updateInputs(() => Promise.all(this.senders.map((sender, index) =>
      sender.replaceTrack(this.track(index === 0 ? "audio" : "video")))));
  }

  async leave(end = false) {
    if (this.abort.signal.aborted) return;
    this.stop();
    try {
      await this.request(end ? "/end" : "/leave", {}, true);
    } catch {
      // The room heartbeat releases the slot if the network is already down.
    }
  }

  stop() {
    this.abort.abort();
    clearTimeout(this.timer);
    this.closePeer();
    this.local?.getTracks().forEach((track) => track.stop());
    this.local = null;
    this.publishUpdate();
  }

  private async request<R>(path: string, body?: object, leaving = false): Promise<R> {
    const timeout = AbortSignal.timeout(path === "/join" ? 8000 : 5000);
    const signal = leaving ? timeout : AbortSignal.any([this.abort.signal, timeout]);
    const response = await this.env.fetch(this.base + path, {
      method: body ? "POST" : "GET",
      headers: { Authorization: `Bearer ${this.admission.token}`, "Content-Type": "application/json" },
      ...(body ? { body: JSON.stringify(body) } : {}),
      cache: "no-store", mode: "same-origin", credentials: "omit", redirect: "error",
      referrerPolicy: body ? "origin" : "no-referrer", signal, keepalive: leaving,
    });
    if (!response.ok) {
      const error = await response.json().catch(() => null);
      throw new RoomRequestError(error?.error?.code ?? "media_unreachable", response.status);
    }
    return await response.json() as R;
  }

  private async tick() {
    try {
      const poll = await this.request<RoomPoll>("");
      this.abort.signal.throwIfAborted();
      const generationChanged = poll.room.generation !== this.room.generation;
      this.room = poll.room;
      if (this.room.state === "ended" || this.room.state === "expired") {
        this.stop();
        return;
      }
      if (generationChanged) this.closePeer();
      if (this.room.state === "connected" || this.room.participants.length < 2) {
        this.pairingSince = undefined;
      } else if (this.local && this.room.participants.length === 2 &&
                 this.room.participants.some((participant) => participant.joined && participant.role !== this.admission.role)) {
        this.pairingSince ??= this.env.now();
      }
      if (this.pairingSince !== undefined && this.env.now() - this.pairingSince >= 30_000) {
        await this.leave();
        this.failure("media_unreachable");
        return;
      }
      if (this.local) await this.negotiate(poll);
      this.abort.signal.throwIfAborted();
      this.lastContact = this.env.now();
      this.refreshIncomingAudio();
      this.publishUpdate();
    } catch (error) {
      if (this.abort.signal.aborted) return;
      if ((error instanceof RoomRequestError && (error.status === 403 || error.status === 410)) ||
          this.env.now() - this.lastContact >= 12_000) {
        void this.leave();
        this.failure(error instanceof RoomRequestError ? error.code : "media_unreachable");
        return;
      }
    }
    if (!this.abort.signal.aborted) this.timer = setTimeout(() => void this.tick(), 500);
  }

  private publishUpdate() {
    this.update({ room: this.room, incoming: this.incoming, incomingAudio: this.incomingAudio });
  }

  private refreshIncomingAudio() {
    const peer = this.peer;
    const other = this.room.participants.find(({ role }) => role !== this.admission.role);
    if (peer?.connectionState !== "connected" || this.room.state !== "connected" || !other?.publication.microphone) {
      this.audioSample = undefined;
      this.incomingAudio = null;
      return;
    }
    if (this.samplingPeer === peer) return;
    this.samplingPeer = peer;
    void peer.getStats().then((report) => {
      if (this.peer !== peer || this.abort.signal.aborted || this.room.state !== "connected" ||
          !this.room.participants.some(({ role, publication }) => role !== this.admission.role && publication.microphone)) return;
      const stats = [...report.values()].find((value) => value.type === "inbound-rtp" && value.kind === "audio");
      const previous = this.audioSample;
      this.audioSample = stats && {
        id: stats.id, packets: stats.packetsReceived,
        energy: stats.totalAudioEnergy, duration: stats.totalSamplesDuration,
      };
      let active: boolean | null = null;
      if (this.audioSample && this.audioSample.packets > 0) {
        const { id, packets, energy, duration } = this.audioSample;
        if (previous?.id === id && packets === previous.packets) active = false;
        else if (previous?.id === id && duration > previous.duration && energy >= previous.energy) {
          active = (energy - previous.energy) / (duration - previous.duration) >= 0.000001;
        } else if (typeof stats.audioLevel === "number") active = stats.audioLevel >= 0.001;
      }
      this.incomingAudio = active;
      this.publishUpdate();
    }).catch(() => {}).finally(() => {
      if (this.samplingPeer === peer) this.samplingPeer = null;
    });
  }

  private async negotiate(poll: RoomPoll) {
    const generation = this.room.generation;
    const peer = this.peer ?? await this.openPeer(generation);
    if (this.pendingPublication) {
      this.pendingPublication = false;
      try {
        await this.syncSenders();
        await this.request("/publication", { generation, publication: this.publication() });
      } catch (error) {
        this.pendingPublication = true;
        throw error;
      }
    }
    if (peer.connectionState === "disconnected") this.disconnectedSince ??= this.env.now();
    else this.disconnectedSince = undefined;
    const stalled = this.disconnectedSince !== undefined && this.env.now() - this.disconnectedSince >= 3000;
    if (peer.connectionState === "failed" || stalled) {
      await this.request("/connection", { generation, connected: false });
      return;
    }
    if (!this.offered && this.admission.role === "host" && poll.room.participants.length === 2 &&
        poll.room.participants.some((participant) => participant.role === "guest" && participant.joined)) {
      if (!peer.localDescription) await peer.setLocalDescription(await peer.createOffer());
      await this.publishDescription(peer, generation);
      this.offered = true;
    }
    if (!this.answered && poll.signal?.generation === generation) {
      if (!peer.remoteDescription) await peer.setRemoteDescription(poll.signal);
      if (this.admission.role === "guest") {
        if (!peer.localDescription) {
          await this.prepareAnswer(peer);
          await peer.setLocalDescription(await peer.createAnswer());
        }
        await this.publishDescription(peer, generation);
      }
      this.answered = true;
    }
    if (!this.reportedConnected && this.answered && peer.connectionState === "connected") {
      await this.request("/connection", { generation, connected: true });
      this.reportedConnected = true;
    }
  }

  private async publishDescription(peer: RTCPeerConnection, generation: number) {
    if (!this.outboundSignal) {
      try {
        await gather(peer, this.abort.signal, 4000);
      } catch {
        this.abort.signal.throwIfAborted();
      }
      const description = peer.localDescription;
      if (!description) throw new Error("missing local description");
      // A lost response can follow a committed signal. Late ICE candidates must
      // not change the payload on the retry for this peer generation.
      this.outboundSignal = { type: description.type, sdp: description.sdp };
    }
    await this.request("/signal", { generation, ...this.outboundSignal });
  }

  private async prepareAnswer(peer: RTCPeerConnection) {
    // The offer already created the answerer's media sections. Bind to those
    // senders so the answer can publish into the negotiated sections.
    const transceivers = (["audio", "video"] as const).map((kind) => {
      const transceiver = peer.getTransceivers().find(({ receiver }) => receiver.track.kind === kind);
      if (!transceiver) throw new Error("missing offered media section");
      transceiver.direction = "sendrecv";
      return transceiver;
    });
    this.senders = transceivers.map(({ sender }) => sender);
    await this.syncSenders();
  }

  private async openPeer(generation: number) {
    const joined = await this.request<JoinedRoom>("/join", { generation, publication: this.publication() });
    this.abort.signal.throwIfAborted();
    this.room = joined.room;
    const peer = this.env.peer({
      iceServers: (joined.ice_servers ?? []).map(({ urls, username, credential }) => ({ urls: [...urls], username, credential })),
      iceTransportPolicy: joined.relay_only ? "relay" : "all",
    });
    this.peer = peer;
    this.incoming = this.env.stream();
    peer.ontrack = ({ track }) => {
      if (this.peer === peer) this.incoming?.addTrack(track);
    };
    if (this.admission.role === "host") {
      this.senders = (["audio", "video"] as const).map((kind) => {
        const track = this.track(kind);
        return peer.addTransceiver(track ?? kind, {
          direction: "sendrecv",
          streams: track && this.local ? [this.local] : [],
        }).sender;
      });
    }
    return peer;
  }

  private track(kind: "audio" | "video") {
    return this.local?.getTracks().find((track) => track.kind === kind && track.enabled && track.readyState === "live") ?? null;
  }

  private publication(): RoomPublication {
    return { microphone: !!this.track("audio"), camera: !!this.track("video") };
  }

  private closePeer() {
    this.peer?.close();
    this.peer = null;
    this.incoming = null;
    this.audioSample = undefined;
    this.incomingAudio = null;
    this.senders = [];
    this.disconnectedSince = undefined;
    this.outboundSignal = undefined;
    this.offered = this.answered = this.reportedConnected = false;
  }
}

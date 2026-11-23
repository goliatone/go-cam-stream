import type { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { captureConversation, ConversationMedia, type InputKind } from "../services/conversation-media";
import type { RoomAdmission, RoomDownload, RoomView } from "../services/contracts";
import { deviceLabel, type CallEnd, type CallPhase, type CallState, type CaptureRefusal, type NativeCall } from "./bridge";
import { CallView, type CallActions, type CallModel, type CallPresence } from "./call-view";
import { failureOf, inputKinds, type Choices, type InputErrors, type InputFailure, type Pending } from "./devices";
import { Icon, type IconName } from "./icons";
import { acknowledgeBrowserStop, BrowserCaptureStop, download, redeem, redeemHost, type HostHandoff, type Invitation } from "./invitation";
import { duration } from "./layout";
import { Notices, type Toast } from "./overlays";
import { Prejoin, type PrejoinActions, type PrejoinModel } from "./prejoin";

const roomStatus: Record<RoomView["state"], string> = {
  waiting: "Waiting for the other participant",
  joining: "Connecting",
  connected: "Connected",
  reconnecting: "Reconnecting",
  ended: "Conversation ended",
  expired: "Invitation expired",
  failed: "Connection failed",
};

function failureCode(error: unknown) {
  const detail = error as { name?: string; message?: string } | null | undefined;
  return detail?.name === "Error" ? detail.message : detail?.name;
}

/** Redemption failures that change what the invitation can still do. */
type Blocked = "full" | "ended" | "expired" | "invalid";
const blockedBy: Partial<Record<string, Blocked>> = {
  room_full: "full", ended: "ended", expired: "expired", invitation_invalid: "invalid", permission_denied: "invalid",
};
const newInvitation = "Ask the host for a new invitation to talk again.";
const blockedCopy: Record<Blocked, { heading: string; detail: string }> = {
  full: { heading: "Someone else is already in this conversation", detail: "Only one guest can join at a time. Try again after they leave." },
  ended: { heading: "This conversation has ended", detail: newInvitation },
  expired: { heading: "This invitation has expired", detail: newInvitation },
  invalid: { heading: "This invitation no longer works", detail: newInvitation },
};
const blockedIcons: Record<Blocked, IconName> = { full: "user", ended: "end", expired: "clock", invalid: "link" };

function describe(error: unknown): string {
  switch (failureCode(error)) {
    case "NotAllowedError": return "Camera or microphone access was denied. Check permissions or continue with those inputs off.";
    case "NotFoundError":
    case "OverconstrainedError": return "The selected input is unavailable. Choose another device or turn that input off.";
    case "NotReadableError": return "A device is busy. Close the app using it and try again.";
    case "room_full": return "Someone else is already in this conversation. Try again after they leave.";
    case "expired": return "This invitation has expired. Ask the host for a new one.";
    case "ended": return "The host ended this conversation.";
    case "invitation_invalid":
    case "permission_denied": return "This invitation is invalid or no longer grants access.";
    case "incompatible_client": return "Update Camstream or ask the host for a new invitation.";
    case "capture_busy": return "Stop camera streaming in Camstream before enabling call devices.";
    case "capture_unavailable": return "Camstream couldn't hand over the camera and mic. Check the Camera tab, then try again.";
    default: return "The conversation could not connect. Check your connection and try again.";
  }
}

function stop(stream: MediaStream | null) {
  stream?.getTracks().forEach((track) => track.stop());
}

function discardAdmission(admission: RoomAdmission) {
  void new ConversationMedia(admission, () => {}, () => {}).leave(admission.role === "host");
}

const refused = (refusal: CaptureRefusal): InputFailure => refusal === "busy" ? "capture_busy" : "capture_unavailable";

type InputRequest = { kind: InputKind; device: string };

/** The page between calls: opening, ended, left and blocked invitations. */
function StateCard({ icon, title, native, meta, detail, alert, children }: {
  icon: IconName | "spinner"; title: string; native: boolean; meta?: string | undefined; detail?: string | undefined;
  alert?: string | undefined; children?: ComponentChildren;
}) {
  return <div class="state-view">
    {!native && <span class="wordmark">Camstream</span>}
    <div class="card">
      {icon === "spinner" ? <span class="spinner large" aria-hidden="true" /> :
        <span class={`badge-icon${icon === "alert" ? " warn" : " muted"}`} aria-hidden="true"><Icon name={icon} /></span>}
      <p class="card-title" aria-hidden="true">{title}</p>
      {meta && <p class="card-meta">{meta}</p>}
      {detail && <p>{detail}</p>}
      {alert && <p role="alert">{alert}</p>}
      {children}
    </div>
  </div>;
}

export function Conversation({ invitation, native, host = null, initialBlocked = null }: {
  invitation: Invitation | null; native: NativeCall | undefined; host?: HostHandoff | null;
  initialBlocked?: "ended" | "expired" | null;
}) {
  const [admission, setAdmission] = useState<RoomAdmission | null>(null);
  const [room, setRoom] = useState<RoomView | null>(null);
  const [error, setError] = useState("");
  const [blocked, setBlocked] = useState<Blocked | null>(initialBlocked);
  const [invitationRouteReady, setInvitationRouteReady] = useState(true);
  useEffect(() => {
    if (!native) return;
    const update = (event: Event) => {
      const reachable = (event as CustomEvent<unknown>).detail;
      if (typeof reachable === "boolean") setInvitationRouteReady(reachable);
    };
    window.addEventListener("camstream:invitation-route", update);
    return () => window.removeEventListener("camstream:invitation-route", update);
  }, [native]);
  const [audioNotice, setAudioNotice] = useState("");
  // Mac and host pages open their admission at once, so the first frame already says so.
  const opening = native !== undefined || host !== null;
  const [busy, setBusy] = useState(opening);
  const [joining, setJoining] = useState(false);
  const [joined, setJoined] = useState(false);
  const [finished, setFinished] = useState(false);
  const [left, setLeft] = useState(false);
  const browserInvitation = !native && !host && invitation !== null;
  const initialChoice = { device: "default", selected: browserInvitation };
  const [choices, setChoices] = useState<Choices>({ video: initialChoice, audio: initialChoice });
  const [inputErrors, setInputErrors] = useState<InputErrors>({});
  const [pending, setPending] = useState<Pending>({});
  const [playbackMuted, setPlaybackMuted] = useState(false);
  const [speaker, setSpeaker] = useState("default");
  const [local, setLocal] = useState<MediaStream | null>(null);
  const [incoming, setIncoming] = useState<MediaStream | null>(null);
  const [incomingAudio, setIncomingAudio] = useState<boolean | null>(null);
  const [devices, setDevices] = useState<MediaDeviceInfo[]>([]);
  const [artifact, setArtifact] = useState<RoomDownload | null>(null);
  const [connectedSince, setConnectedSince] = useState<number | null>(null);
  const [callSeconds, setCallSeconds] = useState(0);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [announcement, setAnnouncement] = useState("");
  const [page, setPage] = useState<HTMLDivElement | null>(null);
  const preview = useRef<MediaStream | null>(null);
  const browserCapture = useRef<BrowserCaptureStop | null>(null);
  const session = useRef<ConversationMedia | null>(null);
  const lifetime = useRef(new AbortController());
  const operation = useRef(0);
  const busyRef = useRef(opening);
  const leaving = useRef(false);
  const heard = useRef(false);
  const connectedAt = useRef<number | null>(null);
  const timers = useRef(new Set<ReturnType<typeof setTimeout>>());
  const toastID = useRef(0);

  useEffect(() => () => { for (const timer of timers.current) clearTimeout(timer); }, []);

  function later(delay: number, run: () => void) {
    const timer = setTimeout(() => { timers.current.delete(timer); run(); }, delay);
    timers.current.add(timer);
  }

  function toast(text: string, icon: IconName = "check") {
    const id = ++toastID.current;
    setToasts((list) => [...list.slice(-2), { id, text, icon }]);
    later(4000, () => setToasts((list) => list.filter((item) => item.id !== id)));
  }

  /** Reads `text` out once; repeating the same text still announces it. */
  function announce(text: string) {
    setAnnouncement("");
    later(30, () => setAnnouncement(text));
  }

  function setWorking(value: boolean) { busyRef.current = value; setBusy(value); }

  function releasePreview() {
    stop(preview.current);
    preview.current = null;
    setLocal(null);
  }

  function updateLocalPreview() {
    const tracks = preview.current?.getTracks().filter((track) => track.readyState === "live") ?? [];
    setLocal(tracks.length ? new MediaStream(tracks) : null);
  }

  /** Stops and drops one input from the preview or call stream. */
  function releaseInput(kind: InputKind) {
    const stream = preview.current;
    if (!stream) return;
    for (const track of stream.getTracks()) {
      if (track.kind === kind) { track.stop(); stream.removeTrack(track); }
    }
    updateLocalPreview();
  }

  useEffect(() => {
    const tracks = local?.getTracks() ?? [];
    // Stopping a track never fires "ended": this is a device that
    // disconnected or was taken, so its control explains why it is off.
    const ended = (event: Event) => {
      updateLocalPreview();
      inputProblem((event.target as MediaStreamTrack).kind as InputKind, "lost");
    };
    for (const track of tracks) track.addEventListener("ended", ended);
    return () => { for (const track of tracks) track.removeEventListener("ended", ended); };
  }, [local]);

  function finish(reason: CallEnd = "closed") {
    operation.current++;
    releasePreview();
    browserCapture.current?.stopped();
    session.current = null;
    setCallSeconds(connectedAt.current === null ? 0 : Math.max(0, Math.round((Date.now() - connectedAt.current) / 1000)));
    connectedAt.current = null;
    setConnectedSince(null);
    setAdmission(null);
    setIncoming(null);
    setIncomingAudio(null);
    setInputErrors({});
    setPending({});
    setJoined(false);
    setJoining(false);
    setFinished(true);
    setWorking(false);
    native?.closed(reason);
  }

  function adopt(value: RoomAdmission) {
    const next = new ConversationMedia(value, ({ room, incoming: stream, incomingAudio: activity }) => {
      setRoom(room);
      setIncoming(stream);
      setIncomingAudio(activity);
      if ((room.state === "ended" || room.state === "expired") && !leaving.current) finish(room.state);
    }, (code) => {
      setError(describe(new Error(code)));
      if (!leaving.current) finish("failed");
    });
    session.current = next;
    setRoom(value.room);
    setAdmission(value);
    next.start();
    return next;
  }

  function startMedia(endpoint: ConversationMedia, stream: MediaStream) {
    preview.current = stream;
    updateLocalPreview();
    endpoint.join(stream);
    setJoined(true);
  }

  useEffect(() => {
    const abort = lifetime.current;
    const revision = operation.current;
    if (native || host) {
      setWorking(true);
      const incoming = native ? native.admission() : redeemHost(host!, abort.signal);
      void incoming.then((value) => {
        if (host) browserCapture.current = new BrowserCaptureStop(() => acknowledgeBrowserStop(host));
        if (abort.signal.aborted || operation.current !== revision) {
          discardAdmission(value);
          browserCapture.current?.stopped();
          return;
        }
        const endpoint = adopt(value);
        if (native && value.role === "host" && "start_muted" in value && value.start_muted === true) {
          startMedia(endpoint, new MediaStream());
        }
      }).catch((failure) => {
        if (!abort.signal.aborted) setError(describe(failure));
      }).finally(() => { if (!abort.signal.aborted) setWorking(false); });
    } else if (invitation?.appURL) {
      void download(abort.signal).then((value) => { if (!abort.signal.aborted) setArtifact(value); });
    }
    const onHide = () => {
      operation.current++;
      stop(preview.current);
      void session.current?.leave();
      browserCapture.current?.stopped();
    };
    const onPageHide = (event: PageTransitionEvent) => {
      onHide();
      if (event.persisted) finish();
    };
    window.addEventListener("pagehide", onPageHide);
    const mediaDevices = navigator.mediaDevices;
    const onDeviceChange = () => { void refreshDevices(); };
    mediaDevices?.addEventListener?.("devicechange", onDeviceChange);
    return () => {
      abort.abort();
      onHide();
      window.removeEventListener("pagehide", onPageHide);
      mediaDevices?.removeEventListener?.("devicechange", onDeviceChange);
    };
  }, []);

  async function refreshDevices() {
    try { setDevices(await navigator.mediaDevices.enumerateDevices()); }
    catch { setDevices([]); }
  }

  function setDevice(kind: InputKind, device: string) {
    setChoices((previous) => ({ ...previous, [kind]: { ...previous[kind], device } }));
  }

  function select(kind: InputKind, selected: boolean) {
    setChoices((previous) => ({ ...previous, [kind]: { ...previous[kind], selected } }));
  }

  function inputProblem(kind: InputKind, problem?: InputFailure) {
    setInputErrors((previous) => ({ ...previous, [kind]: problem }));
  }

  function setInputPending(kinds: InputKind[], value: boolean) {
    setPending((previous) => {
      const next = { ...previous };
      for (const kind of kinds) next[kind] = value;
      return next;
    });
  }

  function hasInput(kind: InputKind) {
    return liveInput(kind) !== undefined;
  }

  /** Asks the Mac app for the device lease after an explicit device action. */
  async function nativeRefusal(): Promise<CaptureRefusal | null> {
    if (host && !browserCapture.current) return "unavailable";
    if (!native) return null;
    const granted = await native.capture().catch((): CaptureRefusal => "unavailable");
    return granted === true ? null : granted;
  }

  function liveInput(kind: InputKind) {
    return preview.current?.getTracks().find((track) => track.kind === kind && track.readyState === "live" && track.enabled);
  }

  const selectedInputs = () => inputKinds.filter((kind) => choices[kind].selected)
    .map((kind): InputRequest => ({ kind, device: choices[kind].device }));

  /** Opens each requested input that is not live yet, one at a time. */
  async function previewInputs(revision: number, requests: InputRequest[]) {
    const stream = preview.current ?? new MediaStream();
    preview.current = stream;
    const missing = requests.filter(({ kind }) => !hasInput(kind));
    if (!missing.length) return stream;
    const refusal = await nativeRefusal();
    if (refusal) {
      if (operation.current !== revision) return null;
      for (const { kind } of missing) inputProblem(kind, refused(refusal));
      return stream;
    }
    for (const { kind, device: chosen } of missing) {
      if (operation.current !== revision) return null;
      inputProblem(kind);
      setInputPending([kind], true);
      const device = chosen === "default" ? true : chosen;
      try {
        const captured = await captureConversation({ camera: kind === "video" && device, microphone: kind === "audio" && device });
        if (operation.current !== revision) { stop(captured); return null; }
        const track = captured.getTracks().find((item) => item.kind === kind && item.readyState === "live");
        for (const item of captured.getTracks()) if (item !== track) item.stop();
        if (!track) throw new DOMException("No selected track", "NotFoundError");
        for (const previous of stream.getTracks()) {
          if (previous.kind === kind) { previous.stop(); stream.removeTrack(previous); }
        }
        stream.addTrack(track);
        updateLocalPreview();
      } catch (failure) {
        if (operation.current !== revision) return null;
        inputProblem(kind, failureOf(failure));
      } finally { setInputPending([kind], false); }
    }
    void refreshDevices();
    return stream;
  }

  async function showPreview(requests = selectedInputs()) {
    if (busyRef.current) return;
    setWorking(true);
    setError("");
    const revision = ++operation.current;
    try {
      const opening = previewInputs(revision, requests);
      await (browserCapture.current?.track(opening) ?? opening);
    } catch (failure) {
      if (operation.current === revision) setError(describe(failure));
    } finally { if (operation.current === revision) setWorking(false); }
  }

  async function joinCall() {
    if (busyRef.current || joined) return;
    const requests = selectedInputs();
    leaving.current = false;
    setWorking(true);
    setJoining(true);
    setError("");
    const revision = ++operation.current;
    try {
      let endpoint = session.current;
      if (!endpoint && browserInvitation) {
        const value = await redeem(invitation, lifetime.current.signal);
        if (operation.current !== revision) { discardAdmission(value); return; }
        setBlocked(null);
        setFinished(false);
        setPlaybackMuted(false);
        setCallSeconds(0);
        endpoint = adopt(value);
      }
      if (!endpoint) return;

      if (requests.length) {
        const opening = previewInputs(revision, requests);
        const stream = await (browserCapture.current?.track(opening) ?? opening);
        if (stream && operation.current === revision) startMedia(endpoint, stream);
        return;
      }
      releasePreview();
      setInputErrors({});
      startMedia(endpoint, new MediaStream());
    } catch (failure) {
      if (operation.current !== revision) return;
      // A full room stays retryable. Ended, expired and invalid links stop
      // offering Join, and any device preview is released.
      const reason = session.current ? undefined : blockedBy[failureCode(failure) ?? ""];
      if (!reason) { setError(describe(failure)); return; }
      if (reason !== "full") releasePreview();
      setBlocked(reason);
    } finally {
      if (operation.current === revision) { setWorking(false); setJoining(false); }
    }
  }

  async function copyInvitation() {
    if (!native?.copyInvitation) return;
    setError("");
    if (await native.copyInvitation()) toast("Invitation copied");
    else setError("The invitation couldn't be copied. Use Copy Invitation in the Camstream Call menu.");
  }

  async function copyLink() {
    if (!invitation) return;
    try {
      const link = new URL("/r/" + invitation.room + "#" + invitation.token, window.location.origin);
      await navigator.clipboard.writeText(link.href);
      toast("Invitation link copied");
    } catch { setError("Copy the original invitation from the message where you received it."); }
  }

  async function enableInputs(inputs: InputRequest[]) {
    const endpoint = session.current;
    if (!endpoint || busyRef.current) return;
    const revision = ++operation.current;
    const kinds = inputs.map(({ kind }) => kind);
    for (const kind of kinds) inputProblem(kind);
    setWorking(true);
    setInputPending(kinds, true);
    try {
      const refusal = await nativeRefusal();
      if (refusal) {
        if (operation.current !== revision) return;
        for (const kind of kinds) inputProblem(kind, refused(refusal));
        return;
      }
      for (const { kind, device } of inputs) {
        if (operation.current !== revision) return;
        try {
          const opening = endpoint.selectInput(kind, device === "default" ? true : device);
          const selected = await (browserCapture.current?.track(opening) ?? opening);
          if (operation.current !== revision) return;
          if (!selected) throw new DOMException("No selected track", "NotFoundError");
          setDevice(kind, device);
          updateLocalPreview();
        } catch (failure) {
          if (operation.current !== revision) return;
          inputProblem(kind, failureOf(failure));
        } finally { setInputPending([kind], false); }
      }
      void refreshDevices();
    } finally {
      setInputPending(kinds, false);
      if (operation.current === revision) setWorking(false);
    }
  }

  /** Device menus are unavailable while an input opens, since that request already names its device. */
  function changeDevice(kind: InputKind, value: string) {
    if (busyRef.current) return;
    inputProblem(kind);
    if (joined && hasInput(kind)) { void enableInputs([{ kind, device: value }]); return; }
    setDevice(kind, value);
    // A running preview moves to the chosen device; otherwise the choice waits for Preview or Join.
    if (!joined && hasInput(kind)) {
      releaseInput(kind);
      void showPreview([{ kind, device: value }]);
    }
  }

  function toggle(kind: InputKind) {
    if (busyRef.current) return;
    if (!hasInput(kind)) { void enableInputs([{ kind, device: choices[kind].device }]); return; }
    session.current?.removeInput(kind);
    inputProblem(kind);
    updateLocalPreview();
  }

  /** Prejoin toggles record what Join uses. Turning one off releases its device. */
  function toggleChoice(kind: InputKind) {
    if (busyRef.current) return;
    const on = !choices[kind].selected;
    select(kind, on);
    inputProblem(kind);
    if (!on) { releaseInput(kind); return; }
    if (preview.current?.getTracks().some((track) => track.readyState === "live")) {
      void showPreview([{ kind, device: choices[kind].device }]);
    }
  }

  async function leave() {
    if (leaving.current) return;
    leaving.current = true;
    // A browser guest can rejoin with the inputs that were on, or asked for, when they left.
    const rejoinable = browserInvitation && joined;
    if (rejoinable) for (const kind of inputKinds) select(kind, hasInput(kind) || !!inputErrors[kind]);
    setWorking(true);
    operation.current++;
    releasePreview();
    const endpoint = session.current;
    await endpoint?.leave(admission?.role === "host");
    session.current = null;
    setRoom(null);
    setLeft(rejoinable);
    finish();
  }

  function rejoin() {
    setLeft(false);
    setError("");
    later(0, () => document.getElementById("join-call")?.focus());
  }

  const role = admission?.role ?? (host ? "host" : "guest");
  const peerName = role === "host" ? "Guest" : "Host";
  const peer = room?.participants.find(({ role: other }) => other !== admission?.role);
  const micOn = hasInput("audio");
  const cameraOn = hasInput("video");
  const remoteCamera = !!peer?.publication.camera;
  const remoteMic = !!peer?.publication.microphone;
  const terminal = room?.state === "expired" || room?.state === "ended";
  const stopped = !admission && blocked !== null && blocked !== "full";
  const visibleCall = (admission !== null && !finished) || (browserInvitation && !terminal && !stopped);
  const loading = busy && !admission && !finished && opening;
  const waiting = role === "host" ? "Waiting for your guest" : "Waiting for the host";
  const headline = loading ? "Opening the conversation…" :
    !admission && blocked ? blockedCopy[blocked].heading : terminal ? roomStatus[room.state] :
    left ? "You left the conversation" :
    joined && room ? (room.state === "waiting" || room.state === "joining" && !peer?.joined ? waiting : roomStatus[room.state]) :
    visibleCall ? "Ready to join" : finished ? "Conversation closed" :
    error ? "Couldn't open the conversation" : "Conversation invitation";
  // What to do once this page can no longer join or host.
  const nextStep = stopped ? "" : finished && host ? "To talk again, start a new conversation in Camstream on your Mac." :
    terminal && browserInvitation ? "You can close this page. " + newInvitation : "";
  const callPhase: CallPhase | null = !admission || finished ? null : !joined ? "prejoin" :
    room?.state === "waiting" || room?.state === "joining" && !peer?.joined ? "waiting" :
    room?.state === "joining" ? "connecting" : room?.state === "connected" || room?.state === "reconnecting" ? room.state : null;
  const present = callPhase === "connected" || callPhase === "reconnecting";
  useEffect(() => {
    if (!remoteMic) { heard.current = false; setAudioNotice(""); return; }
    if (incomingAudio === true && !heard.current) {
      heard.current = true;
      setAudioNotice(`Incoming audio from the ${peerName.toLowerCase()}`);
    }
  }, [remoteMic, incomingAudio, peerName]);
  // The call length counts from when the other person is first present and
  // restarts if they leave and come back.
  useEffect(() => {
    if (present && connectedAt.current === null) {
      connectedAt.current = Date.now();
      setConnectedSince(connectedAt.current);
    } else if (!present && joined && connectedAt.current !== null) {
      connectedAt.current = null;
      setConnectedSince(null);
    }
  }, [present, joined]);
  const reported = useRef("");
  // The Mac app shows this activity in its window and menu bar. Track state
  // lives in a ref, so compare each render's report with the last one sent.
  useEffect(() => {
    if (!native || !admission || !callPhase) return;
    const camera = liveInput("video");
    const microphone = liveInput("audio");
    const cameraName = deviceLabel(camera);
    const microphoneName = deviceLabel(microphone);
    const state: CallState = {
      version: 1, room: admission.room.room_id, role: admission.role, phase: callPhase,
      camera: camera !== undefined, microphone: microphone !== undefined,
      ...(cameraName ? { camera_label: cameraName } : {}), ...(microphoneName ? { microphone_label: microphoneName } : {}),
    };
    const key = JSON.stringify(state);
    if (key === reported.current) return;
    reported.current = key;
    native.state(state);
  });

  const view = joined && admission ? "call" : visibleCall && !left ? "prejoin" : "card";
  const phase: CallPresence = callPhase === "connecting" || present ? callPhase : "waiting";
  const meta = callSeconds > 0 ? `${duration(callSeconds)} with the ${peerName.toLowerCase()}` : undefined;

  const prejoinModel: PrejoinModel = {
    role, native: native !== undefined, headline,
    notice: !admission && blocked === "full" ? blockedCopy.full.detail : "",
    origin: browserInvitation ? window.location.host : "", peerPresent: !!peer?.joined,
    choices, devices, speaker, local, cameraLive: cameraOn, micLive: micOn,
    pending, inputErrors, busy, joining, error, exit: admission !== null, leaving: leaving.current,
    mac: browserInvitation && !admission && !busy && invitation.appURL ? { appURL: invitation.appURL, download: artifact } : null,
  };
  const prejoinActions: PrejoinActions = {
    toggle: toggleChoice, preview: () => void showPreview(),
    retry: (kind) => void showPreview([{ kind, device: choices[kind].device }]), join: () => void joinCall(),
    changeDevice, chooseSpeaker: setSpeaker, refreshDevices: () => void refreshDevices(),
    leave: () => void leave(), openApp: releasePreview, copyLink: () => void copyLink(),
  };
  const callModel: CallModel = {
    role, native: native !== undefined, canCopyInvitation: !!native?.copyInvitation && invitationRouteReady, phase,
    remoteCamera, remoteMic, incoming, incomingAudio, playbackMuted, local, micOn, cameraOn,
    pending, inputErrors, busy, leaving: leaving.current, devices, choices, speaker,
    expiresAt: room?.expires_at ?? "", connectedSince,
  };
  const callActions: CallActions = {
    toggle, enableBoth: () => void enableInputs(inputKinds.map((kind) => ({ kind, device: choices[kind].device }))),
    retry: (kind) => void enableInputs([{ kind, device: choices[kind].device }]),
    changeDevice, chooseSpeaker: setSpeaker, refreshDevices: () => void refreshDevices(),
    setPlaybackMuted, copyInvitation: () => void copyInvitation(), leave: () => void leave(), toast, announce,
  };

  function card() {
    const common = { title: headline, native: native !== undefined };
    if (loading) return <StateCard icon="spinner" {...common} />;
    if (!admission && blocked) return <StateCard icon={blockedIcons[blocked]} {...common} alert={blockedCopy[blocked].detail} />;
    if (left) return <StateCard icon="leave" {...common} meta={meta} detail="You can rejoin while the host keeps the conversation open.">
      <button id="rejoin" class="btn primary" type="button" onClick={rejoin}>Rejoin</button>
    </StateCard>;
    if (terminal || finished) {
      return <StateCard icon={room?.state === "expired" ? "clock" : "end"} {...common} meta={meta} detail={nextStep} alert={error} />;
    }
    if (error) return <StateCard icon="alert" {...common} alert={error} />;
    return <StateCard icon="link" {...common} detail="Open the invitation link from the host to join." />;
  }

  return <div ref={setPage} class="call-root" data-view={view}>
    <h1 class="visually-hidden" aria-live="polite">{headline}</h1>
    <p class="visually-hidden" role="status">{busy ? "Working…" : ""}</p>
    <p class="visually-hidden" role="status">{audioNotice}</p>
    <p class="visually-hidden" aria-live="polite">{announcement}</p>
    {view === "prejoin" && <Prejoin model={prejoinModel} actions={prejoinActions} page={page} />}
    {view === "call" && <CallView model={callModel} actions={callActions} page={page} />}
    {view === "card" && card()}
    <Notices toasts={toasts}>
      {view === "call" && phase === "reconnecting" && <div class="banner" role="status">
        <span class="spinner" aria-hidden="true" />
        <span><strong>Reconnecting…</strong> The call continues when the connection returns.</span>
      </div>}
      {view === "call" && error && <div class="banner error" role="alert">
        <Icon name="alert" /><span>{error}</span>
        <button class="banner-close" type="button" aria-label="Dismiss" onClick={() => setError("")}><Icon name="close" /></button>
      </div>}
    </Notices>
  </div>;
}

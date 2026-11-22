import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/preact";
import { afterEach, expect, test, vi } from "vitest";
import type { RoomAdmission } from "../services/contracts";
import { Conversation } from "./conversation";
import { readInvitation } from "./invitation";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const roomID = "A".repeat(43);
const invitation = readInvitation(new URL(`https://call.example.test/r/${roomID}#header.payload.signature`));
const guest: RoomAdmission = {
  room: { room_id: roomID, state: "waiting", version: 1, generation: 1, expires_at: "",
    participants: [{ role: "guest", joined: false, publication: { camera: false, microphone: false } }] },
  role: "guest", token: "B".repeat(43),
};

class TestTrack extends EventTarget {
  readyState: MediaStreamTrackState = "live";
  enabled = true;
  constructor(readonly kind: "video" | "audio", readonly label = "") { super(); }
  stop = vi.fn(() => { this.readyState = "ended"; });
}

class TestStream extends EventTarget {
  constructor(private tracks: TestTrack[] = []) { super(); this.tracks = [...tracks]; }
  getTracks() { return this.tracks.slice(); }
  addTrack(track: TestTrack) { this.tracks.push(track); this.dispatchEvent(new Event("addtrack")); }
  removeTrack(track: TestTrack) { this.tracks = this.tracks.filter((item) => item !== track); }
}

/** Waits for a call control's on/off state. */
async function pressed(controls: HTMLElement, name: string, on: boolean) {
  const button = within(controls).getByRole("button", { name });
  await waitFor(() => expect(button.getAttribute("aria-pressed")).toBe(String(on)));
  return button;
}

/** The sentence under Join that says what Join will do. */
function joinSummary() {
  const join = screen.getByRole("button", { name: /^(Join|Try again)$/ });
  return document.getElementById(join.getAttribute("aria-describedby")!)!.textContent;
}

function stubPeer(incoming?: TestTrack) {
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
  vi.stubGlobal("MediaStream", TestStream);
  vi.stubGlobal("RTCPeerConnection", class {
    connectionState = "new";
    addTransceiver() { return { sender: { replaceTrack: async () => {} } }; }
    close() {}
    set ontrack(receive: (event: { track: TestTrack }) => void) {
      if (incoming) receive({ track: incoming });
    }
  });
}

function macHost(capture: () => Promise<true | "busy" | "unavailable">, state = vi.fn()) {
  return { admission: async () => ({ ...guest, role: "host" as const, start_muted: true }), capture, closed: vi.fn(), state };
}

test("one browser Join keeps the camera when microphone permission fails and can retry it in the call", async () => {
  stubPeer();
  const video = new TestTrack("video");
  const micro = new TestTrack("audio");
  let micAllowed = false;
  const getUserMedia = vi.fn(async (input: MediaStreamConstraints) => {
    if (input.video) return new TestStream([video]);
    if (micAllowed) return new TestStream([micro]);
    throw new DOMException("denied", "NotAllowedError");
  });
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  const fetcher = vi.fn(async (path: string, _init?: RequestInit) => {
    const body = path.endsWith("/redeem") ? guest : { room: guest.room };
    return new Response(JSON.stringify(body));
  });
  vi.stubGlobal("fetch", fetcher);
  const link = invitation?.appURL;
  expect(link).toContain("camstream://conversation/join?");
  render(<Conversation invitation={invitation} native={undefined} />);
  expect(screen.getByRole("link", { name: "Open in Camstream" }).getAttribute("href")).toBe(link);
  expect(getUserMedia).not.toHaveBeenCalled();
  expect(fetcher.mock.calls.some(([path]) => /\/(redeem|join)$/.test(path))).toBe(false);
  expect(screen.queryByLabelText("Host's video")).toBeNull();
  // A browser invitation starts with both inputs selected, and Join says so.
  expect(screen.getByRole("button", { name: "Camera" }).getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByRole("button", { name: "Microphone" }).getAttribute("aria-pressed")).toBe("true");
  expect(joinSummary()).toBe("You’ll join with your camera and mic on.");
  fireEvent.click(screen.getByRole("button", { name: "Join" }));
  await screen.findByRole("heading", { name: "Waiting for the host" });
  expect(getUserMedia).toHaveBeenCalledTimes(2);
  // The failure is explained at the microphone control, which takes focus.
  const problem = await screen.findByRole("dialog", { name: "Microphone blocked" });
  expect(problem.textContent).toContain("This browser blocked the microphone. Allow it in the site settings, then try again.");
  const retry = within(problem).getByRole("button", { name: "Try again" });
  expect(document.activeElement).toBe(retry);
  expect(within(screen.getByRole("region", { name: "You" })).getByText("Camera on · Mic off")).toBeTruthy();
  await waitFor(() => expect(fetcher.mock.calls.find(([path]) => path.endsWith("/join"))?.[1]?.body)
    .toBe(JSON.stringify({ generation: 1, publication: { microphone: false, camera: true } })));
  expect(screen.getByLabelText("Host's video")).toBeTruthy();
  const controls = screen.getByRole("group", { name: "Call controls" });
  micAllowed = true;
  fireEvent.click(retry);
  await pressed(controls, "Microphone", true);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(video.stop).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Leave" }));
  await waitFor(() => expect(fetcher.mock.calls.some(([path]) => path.endsWith("/leave"))).toBe(true));
  await screen.findByRole("heading", { name: "You left the call" });
  // Rejoin returns to prejoin with the inputs that were on and captures nothing until Join.
  fireEvent.click(screen.getByRole("button", { name: "Rejoin" }));
  await screen.findByRole("heading", { name: "Ready to join" });
  expect(screen.getByRole("button", { name: "Camera" }).getAttribute("aria-pressed")).toBe("true");
  expect(screen.getByRole("button", { name: "Microphone" }).getAttribute("aria-pressed")).toBe("true");
  expect(getUserMedia).toHaveBeenCalledTimes(3);
});

test("prejoin captures only on request, and turning an input off releases its device", async () => {
  stubPeer();
  const camera = new TestTrack("video");
  const micro = new TestTrack("audio");
  const getUserMedia = vi.fn(async (input: MediaStreamConstraints) => new TestStream([input.video ? camera : micro]));
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  const fetcher = vi.fn(async (_path: string) => new Response("{}", { status: 404 }));
  vi.stubGlobal("fetch", fetcher);
  render(<Conversation invitation={invitation} native={undefined} />);
  expect(screen.getByText("Nothing is captured until you preview or join.")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Camera: System default" })).toBeTruthy();
  const preview = screen.getByRole("button", { name: "Preview camera and mic" });
  preview.focus();
  fireEvent.click(preview);
  // Preview gives way to progress, so focus moves to the camera it starts.
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Camera" }));
  // The toggles wait while devices open: they keep focus and ignore presses.
  const microphone = screen.getByRole("button", { name: "Microphone" });
  expect(microphone.getAttribute("aria-disabled")).toBe("true");
  fireEvent.click(microphone);
  expect(microphone.getAttribute("aria-pressed")).toBe("true");
  await waitFor(() => expect(microphone.getAttribute("aria-disabled")).toBe("false"));
  expect(screen.queryByText("Camera preview is off")).toBeNull();
  expect(getUserMedia).toHaveBeenCalledTimes(2);
  fireEvent.click(microphone);
  expect(micro.stop).toHaveBeenCalledOnce();
  expect(camera.stop).not.toHaveBeenCalled();
  expect(joinSummary()).toBe("You’ll join with your camera on and mic off.");
  fireEvent.click(screen.getByRole("button", { name: "Camera" }));
  expect(camera.stop).toHaveBeenCalledOnce();
  expect(screen.getByText("Your camera is off")).toBeTruthy();
  expect(joinSummary()).toBe("You’ll join with your camera and mic off. You can turn them on in the call.");
  expect(getUserMedia).toHaveBeenCalledTimes(2);
  expect(fetcher.mock.calls.some(([path]) => /\/(redeem|join)$/.test(path))).toBe(false);
});

test("prejoin retries hand focus to the input they retry", async () => {
  stubPeer();
  const getUserMedia = vi.fn(async () => { throw new DOMException("denied", "NotAllowedError"); });
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 404 })));
  render(<Conversation invitation={invitation} native={undefined} />);
  fireEvent.click(screen.getByRole("button", { name: "Preview camera and mic" }));
  const preview = screen.getByRole("region", { name: "You" });
  const cameraRetry = await within(preview).findByRole("button", { name: "Try again" });
  await waitFor(() => expect(cameraRetry.getAttribute("aria-disabled")).toBe("false"));
  cameraRetry.focus();
  fireEvent.click(cameraRetry);
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Camera" }));
  await waitFor(() => expect(getUserMedia).toHaveBeenCalledTimes(3));
  const micProblem = screen.getAllByRole("alert").find((alert) => alert.textContent?.includes("Microphone blocked"))!;
  const micRetry = within(micProblem).getByRole("button", { name: "Try again" });
  await waitFor(() => expect(micRetry.getAttribute("aria-disabled")).toBe("false"));
  micRetry.focus();
  fireEvent.click(micRetry);
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Microphone" }));
  await waitFor(() => expect(getUserMedia).toHaveBeenCalledTimes(4));
});

test("busy call controls keep focus, and an input's explanation takes it", async () => {
  stubPeer();
  let openMic!: (stream: TestStream) => void;
  const audio = new TestTrack("audio");
  const getUserMedia = vi.fn((input: MediaStreamConstraints) => input.video
    ? Promise.reject(new DOMException("denied", "NotAllowedError"))
    : new Promise<TestStream>((resolve) => { openMic = resolve; }));
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ room: guest.room }))));
  render(<Conversation invitation={null} native={macHost(vi.fn(async () => true as const))} />);
  const controls = await screen.findByRole("group", { name: "Call controls" });
  const self = screen.getByRole("region", { name: "You" });
  const turnOn = within(self).getByRole("button", { name: "Turn on camera and mic" });
  turnOn.focus();
  fireEvent.click(turnOn);
  // Turn on gives way to progress, so focus stays on your video. The camera's
  // explanation then takes it while the microphone is still opening.
  expect(document.activeElement).toBe(self);
  const problem = await screen.findByRole("dialog", { name: "Camera blocked" });
  const retry = within(problem).getByRole("button", { name: "Try again" });
  expect(document.activeElement).toBe(retry);
  expect(retry.getAttribute("aria-disabled")).toBe("true");
  fireEvent.click(retry);
  expect(getUserMedia).toHaveBeenCalledTimes(2);
  // Device menus wait too, since the open request already names its device.
  const options = within(controls).getByRole("button", { name: "Camera options" });
  expect(options.getAttribute("aria-disabled")).toBe("true");
  fireEvent.click(options);
  expect(screen.queryByRole("menu")).toBeNull();
  // Escape from elsewhere closes the explanation and returns to its control.
  const camera = within(controls).getByRole("button", { name: "Camera" });
  const microphone = within(controls).getByRole("button", { name: "Microphone" });
  microphone.focus();
  fireEvent.keyDown(microphone, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(camera);
  openMic(new TestStream([audio]));
  await pressed(controls, "Microphone", true);
  // Once nothing is opening, Try again returns focus to the camera control and retries.
  fireEvent.click(camera);
  const again = within(screen.getByRole("dialog", { name: "Camera blocked" })).getByRole("button", { name: "Try again" });
  expect(again.getAttribute("aria-disabled")).toBe("false");
  fireEvent.click(again);
  expect(document.activeElement).toBe(camera);
  await waitFor(() => expect(getUserMedia).toHaveBeenCalledTimes(3));
});

test("an explicit Mac host start joins with both inputs off and requests no capture", async () => {
  stubPeer();
  const capture = vi.fn();
  const getUserMedia = vi.fn();
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  const fetcher = vi.fn(async (_path: string, _init?: RequestInit) => new Response(JSON.stringify({ room: guest.room })));
  vi.stubGlobal("fetch", fetcher);
  const native = { admission: async () => ({ ...guest, role: "host" as const, start_muted: true }), capture, closed: vi.fn(), state: vi.fn() };
  render(<Conversation invitation={null} native={native} />);
  await screen.findByRole("heading", { name: "Waiting for your guest" });
  await waitFor(() => expect(fetcher.mock.calls.some(([path]) => path.endsWith("/join"))).toBe(true));
  expect(capture).not.toHaveBeenCalled();
  expect(getUserMedia).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Join" })).toBeNull();
});

test("one Mac call action requests both inputs even if camera permission fails", async () => {
  stubPeer();
  const capture = vi.fn(async () => true as const);
  const audio = new TestTrack("audio");
  const getUserMedia = vi.fn(async (input: MediaStreamConstraints) => {
    if (input.video) throw new DOMException("denied", "NotAllowedError");
    return new TestStream([audio]);
  });
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ room: guest.room }))));
  render(<Conversation invitation={null} native={macHost(capture)} />);
  const controls = await screen.findByRole("group", { name: "Call controls" });
  const self = screen.getByRole("region", { name: "You" });
  fireEvent.click(within(self).getByRole("button", { name: "Turn on camera and mic" }));
  await pressed(controls, "Microphone", true);
  expect(capture).toHaveBeenCalledOnce();
  expect(getUserMedia).toHaveBeenCalledTimes(2);
  const problem = await screen.findByRole("dialog", { name: "Camera blocked" });
  expect(problem.textContent).toContain("Allow Camstream to use the camera in System Settings › Privacy & Security, then try again.");
  expect(screen.queryByRole("button", { name: "Turn on camera and mic" })).toBeNull();
  const camera = await pressed(controls, "Camera", false);
  expect(screen.getByText("Camera off · Mic on")).toBeTruthy();
  // Escape closes the explanation and returns focus to the camera control.
  fireEvent.keyDown(problem, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(camera);
  // Pressing the failed control explains it again before anything is retried.
  fireEvent.click(camera);
  expect(screen.getByRole("dialog", { name: "Camera blocked" })).toBeTruthy();
  expect(getUserMedia).toHaveBeenCalledTimes(2);
});

test("the Mac camera toggle releases its track and remembers the chosen device", async () => {
  stubPeer();
  const first = new TestTrack("video");
  const second = new TestTrack("video");
  const capture = vi.fn(async () => true as const);
  const getUserMedia = vi.fn().mockResolvedValueOnce(new TestStream([first])).mockResolvedValueOnce(new TestStream([second]));
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [{ kind: "videoinput", deviceId: "usb-camera", label: "USB Camera" }] } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ room: guest.room }))));
  render(<Conversation invitation={null} native={macHost(capture)} />);
  const controls = await screen.findByRole("group", { name: "Call controls" });
  const tile = screen.getByRole("region", { name: "You" });
  fireEvent.click(await pressed(controls, "Camera", false));
  fireEvent.click(await pressed(controls, "Camera", true));
  expect(first.stop).toHaveBeenCalledOnce();
  // With the camera off, choosing a device only records it.
  fireEvent.click(within(controls).getByRole("button", { name: "Camera options" }));
  fireEvent.click(await screen.findByRole("menuitemradio", { name: "USB Camera" }));
  expect(screen.queryByRole("menu")).toBeNull();
  expect(getUserMedia).toHaveBeenCalledOnce();
  fireEvent.click(await pressed(controls, "Camera", false));
  const off = await pressed(controls, "Camera", true);
  expect(getUserMedia).toHaveBeenLastCalledWith({ audio: false, video: { deviceId: { exact: "usb-camera" } } });
  fireEvent.click(off);
  expect(second.stop).toHaveBeenCalledOnce();
  expect(within(tile).getByText("Camera off · Mic off")).toBeTruthy();
});

test("a camera that disconnects mid-call turns off and explains why", async () => {
  stubPeer();
  const camera = new TestTrack("video");
  const getUserMedia = vi.fn(async () => new TestStream([camera]));
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ room: guest.room }))));
  render(<Conversation invitation={null} native={macHost(vi.fn(async () => true as const))} />);
  const controls = await screen.findByRole("group", { name: "Call controls" });
  fireEvent.click(await pressed(controls, "Camera", false));
  await pressed(controls, "Camera", true);
  camera.readyState = "ended";
  camera.dispatchEvent(new Event("ended"));
  const problem = await screen.findByRole("dialog", { name: "Camera stopped" });
  expect(problem.textContent).toContain("The camera disconnected or another app took it.");
  await pressed(controls, "Camera", false);
  // Nothing is substituted: the camera stays off until it is turned on again.
  expect(getUserMedia).toHaveBeenCalledOnce();
});

test("the incoming playback prompt clears when media starts playing", async () => {
  stubPeer(new TestTrack("video"));
  vi.mocked(HTMLMediaElement.prototype.play).mockRejectedValue(new DOMException("blocked", "NotAllowedError"));
  const remote = { role: "guest" as const, joined: false, publication: { camera: true, microphone: false } };
  const room = { ...guest.room, participants: [remote] };
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ room }))));
  const { container } = render(<Conversation invitation={null} native={{
    admission: async () => ({ ...guest, room, role: "host", start_muted: true }), capture: vi.fn(), closed: vi.fn(), state: vi.fn(),
  }} />);
  await screen.findByRole("button", { name: "Play incoming audio and video" });
  fireEvent.playing(container.querySelector("#remote-video")!);
  expect(screen.queryByRole("button", { name: "Play incoming audio and video" })).toBeNull();
});

test("leaving while the native capture lease is pending does not open devices", async () => {
  stubPeer();
  let authorize!: (allowed: true) => void;
  const capture = vi.fn(() => new Promise<true>((resolve) => { authorize = resolve; }));
  const getUserMedia = vi.fn();
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ room: guest.room }))));
  const closed = vi.fn();
  render(<Conversation invitation={null} native={{ admission: async () => guest, capture, closed, state: vi.fn() }} />);
  await screen.findByRole("button", { name: "Join" });
  // A Mac guest starts with both inputs off.
  const camera = screen.getByRole("button", { name: "Camera" });
  expect(camera.getAttribute("aria-pressed")).toBe("false");
  fireEvent.click(camera);
  fireEvent.click(screen.getByRole("button", { name: "Join" }));
  expect(capture).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole("button", { name: "Leave" }));
  await waitFor(() => expect(closed).toHaveBeenCalledOnce());
  authorize(true);
  await Promise.resolve();
  expect(getUserMedia).not.toHaveBeenCalled();
});

test("a redemption that returns after pagehide releases its guest seat", async () => {
  let finishRedemption!: (response: Response) => void;
  const fetcher = vi.fn(async (path: string) => path.endsWith("/redeem")
    ? new Promise<Response>((resolve) => { finishRedemption = resolve; })
    : new Response(JSON.stringify({ room: guest.room })));
  vi.stubGlobal("fetch", fetcher);
  render(<Conversation invitation={invitation} native={undefined} />);
  fireEvent.click(screen.getByRole("button", { name: "Camera" }));
  fireEvent.click(screen.getByRole("button", { name: "Microphone" }));
  fireEvent.click(screen.getByRole("button", { name: "Join" }));
  expect(screen.getByRole("button", { name: "Joining…" })).toBeTruthy();
  const event = new Event("pagehide");
  Object.defineProperty(event, "persisted", { value: true });
  fireEvent(window, event);
  finishRedemption(new Response(JSON.stringify(guest)));
  await waitFor(() => expect(fetcher.mock.calls.some(([path]) => path.endsWith("/leave"))).toBe(true));
  expect(screen.queryByRole("button", { name: "Leave" })).toBeNull();
});

test("the Mac page reports local call activity without admission details", async () => {
  stubPeer();
  const camera = new TestTrack("video", "FaceTime HD Camera");
  const getUserMedia = vi.fn(async () => new TestStream([camera]));
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ room: guest.room }))));
  const state = vi.fn();
  render(<Conversation invitation={null} native={macHost(vi.fn(async () => true as const), state)} />);
  const controls = await screen.findByRole("group", { name: "Call controls" });
  await waitFor(() => expect(state).toHaveBeenLastCalledWith({
    version: 1, room: roomID, role: "host", phase: "waiting", camera: false, microphone: false,
  }));
  fireEvent.click(await pressed(controls, "Camera", false));
  await pressed(controls, "Camera", true);
  await waitFor(() => expect(state).toHaveBeenLastCalledWith({
    version: 1, room: roomID, role: "host", phase: "waiting", camera: true, microphone: false, camera_label: "FaceTime HD Camera",
  }));
  const reports = JSON.stringify(state.mock.calls);
  expect(reports).not.toContain(guest.token);
  expect(reports).not.toContain("header.payload.signature");
});

test("a refused Mac device request explains whether a stream or unconfirmed cleanup blocks it", async () => {
  stubPeer();
  const getUserMedia = vi.fn();
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ room: guest.room }))));
  const capture = vi.fn<() => Promise<true | "busy" | "unavailable">>()
    .mockResolvedValueOnce("unavailable").mockResolvedValueOnce("busy");
  render(<Conversation invitation={null} native={macHost(capture)} />);
  const controls = await screen.findByRole("group", { name: "Call controls" });
  fireEvent.click(await pressed(controls, "Camera", false));
  expect((await screen.findByRole("dialog", { name: "Camera unavailable" })).textContent).toContain("Check the camera in the Camstream window");
  // With its explanation open, pressing the control again retries.
  fireEvent.click(await pressed(controls, "Camera", false));
  await waitFor(() => expect(screen.getByRole("dialog").textContent).toContain("Stop the Camstream camera stream"));
  expect(getUserMedia).not.toHaveBeenCalled();
});

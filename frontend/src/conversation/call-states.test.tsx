import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/preact";
import { render as renderNow } from "preact";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { captureConversation, type ConversationMediaUpdate } from "../services/conversation-media";
import type { RoomAdmission, RoomView } from "../services/contracts";
import { Conversation } from "./conversation";
import { readInvitation } from "./invitation";

// Room and redemption states with the media session replaced, so each test
// can drive what the server reports without WebRTC.
const sessions = vi.hoisted(() => [] as Array<{
  update(value: ConversationMediaUpdate): void; leave: (end?: boolean) => Promise<void>;
}>);
vi.mock("../services/conversation-media", () => ({
  captureConversation: vi.fn(async () => { throw new DOMException("unused", "NotAllowedError"); }),
  ConversationMedia: class {
    leave = vi.fn(async () => {});
    constructor(_admission: RoomAdmission, readonly update: (value: ConversationMediaUpdate) => void) { sessions.push(this); }
    start() {}
    join() {}
    removeInput() {}
    async selectInput() { return false; }
  },
}));

class TestStream extends EventTarget {
  private tracks: unknown[] = [];
  getTracks() { return this.tracks.slice(); }
  addTrack(track: unknown) { this.tracks.push(track); }
  removeTrack(track: unknown) { this.tracks = this.tracks.filter((item) => item !== track); }
}

beforeEach(() => { vi.stubGlobal("MediaStream", TestStream); });
afterEach(() => { cleanup(); sessions.length = 0; vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const roomID = "A".repeat(43);
const invitation = readInvitation(new URL(`https://call.example.test/r/${roomID}#header.payload.signature`));
const waiting: RoomView = { room_id: roomID, state: "waiting", version: 1, generation: 1, expires_at: "",
  participants: [{ role: "host", joined: true, publication: { camera: false, microphone: false } }] };
const guest: RoomAdmission = { room: waiting, role: "guest", token: "B".repeat(43) };

function nativeHost(copyInvitation?: () => Promise<boolean>, room = waiting) {
  return {
    admission: async () => ({ ...guest, room, role: "host" as const, start_muted: true }),
    capture: vi.fn(), closed: vi.fn(), state: vi.fn(), ...(copyInvitation ? { copyInvitation } : {}),
  };
}

function report(room: RoomView, incomingAudio: boolean | null = null) {
  act(() => sessions.at(-1)!.update({ room, incoming: null, incomingAudio }));
}

function withGuest(microphone: boolean): RoomView {
  return { ...waiting, state: "connected",
    participants: [waiting.participants[0]!, { role: "guest", joined: true, publication: { camera: false, microphone } }] };
}

/** A Mac host whose guest has arrived. */
async function connectedHost(native = nativeHost(async () => true)) {
  render(<Conversation invitation={null} native={native} />);
  await screen.findByRole("heading", { name: "Waiting for your guest" });
  report(withGuest(true));
  await screen.findByRole("heading", { name: "Connected" });
  return native;
}

/** Redemption replies in order; later calls repeat the last one. */
function redemptions(...replies: Array<{ status: number; body: unknown }>) {
  const fetcher = vi.fn(async (path: string) => {
    if (!path.endsWith("/redeem")) return new Response(JSON.stringify({ room: waiting }));
    const reply = replies.length > 1 ? replies.shift()! : replies[0]!;
    return new Response(JSON.stringify(reply.body), { status: reply.status });
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

const failure = (code: string, status = 409) => ({ status, body: { error: { code, retryable: code === "room_full" } } });

test("an ended link shows its terminal state immediately without offering Join", () => {
  const fetcher = redemptions(failure("ended", 410));
  render(<Conversation invitation={null} native={undefined} initialBlocked="ended" />);
  expect(screen.getByRole("heading", { name: "This call has ended" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Join" })).toBeNull();
  expect(fetcher).not.toHaveBeenCalled();
});

test("a waiting host can share again after the app verifies a recovered route", async () => {
  render(<Conversation invitation={null} native={nativeHost(async () => true)} />);
  const name = { name: "Copy invitation" };
  await screen.findByRole("button", name);
  act(() => { window.dispatchEvent(new CustomEvent("camstream:invitation-route", { detail: false })); });
  await waitFor(() => expect(screen.queryByRole("button", name)).toBeNull());
  act(() => { window.dispatchEvent(new CustomEvent("camstream:invitation-route", { detail: true })); });
  await screen.findByRole("button", name);
});

/** Turns both prejoin toggles off, so Join enters with no devices. */
function joinWithoutDevices() {
  fireEvent.click(screen.getByRole("button", { name: "Camera" }));
  fireEvent.click(screen.getByRole("button", { name: "Microphone" }));
  fireEvent.click(screen.getByRole("button", { name: "Join" }));
}

test("a waiting Mac host copies the invitation from the waiting card without seeing it", async () => {
  const copy = vi.fn<() => Promise<boolean>>().mockResolvedValueOnce(true).mockResolvedValueOnce(false);
  const expires = new Date(Date.now() + 3600_000).toISOString();
  render(<Conversation invitation={null} native={nativeHost(copy, { ...waiting, expires_at: expires })} />);
  await screen.findByRole("heading", { name: "Waiting for your guest" });
  const stage = screen.getByRole("region", { name: "Guest" });
  expect(within(stage).getByText(/^Send them the invitation link\. It works until .+\.$/)).toBeTruthy();
  fireEvent.click(within(stage).getByRole("button", { name: "Copy invitation" }));
  await screen.findByText("Invitation copied");
  expect(copy).toHaveBeenCalledOnce();
  // The paired device action stays in the self view.
  expect(within(screen.getByRole("region", { name: "You" })).getByRole("button", { name: "Turn on camera and mic" })).toBeTruthy();
  fireEvent.click(within(stage).getByRole("button", { name: "Copy invitation" }));
  expect((await screen.findByRole("alert")).textContent).toContain("Copy Invitation in the Camstream Call menu");
  // With nobody to hear yet, Sound waits for the guest.
  expect(screen.queryByRole("button", { name: "Sound" })).toBeNull();
  const controls = screen.getByRole("group", { name: "Call controls" });
  expect(within(controls).getAllByRole("button").map((button) => button.getAttribute("aria-label") ?? button.textContent))
    .toEqual(["Microphone", "Microphone options", "Camera", "Camera options", "More options", "End call"]);
});

test("a connected host sees the guest first, with incoming audio announced once", async () => {
  const native = await connectedHost();
  const remote = screen.getByRole("region", { name: "Guest" });
  const self = screen.getByRole("region", { name: "You" });
  expect(within(remote).getByText("Camera off · Mic on")).toBeTruthy();
  expect(screen.getByLabelText("Guest's video")).toBeTruthy();
  // The guest fills the stage and comes first; you float in a corner.
  expect(remote.compareDocumentPosition(self) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(screen.getByText(/^\d\d:\d\d$/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Copy invitation" })).toBeNull();
  expect(within(self).getByRole("button", { name: "Turn on camera and mic" })).toBeTruthy();
  const announced = () => screen.getAllByRole("status").map((node) => node.textContent).filter(Boolean);
  report(withGuest(true), true);
  expect(announced()).toEqual(["Incoming audio from the guest"]);
  report(withGuest(true), false);
  report(withGuest(true), true);
  expect(announced()).toEqual(["Incoming audio from the guest"]);
  report(withGuest(false));
  expect(announced()).toEqual([]);
  // Sound mutes playback on this device only, and the Mac app is not told.
  const reports = native.state.mock.calls.length;
  const sound = screen.getByRole("button", { name: "Sound" });
  expect(sound.getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(sound);
  expect(sound.getAttribute("aria-pressed")).toBe("false");
  expect(within(remote).getByText("Camera off · Mic off · Sound off")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Sound is off on this device. Turn it on." }));
  expect(sound.getAttribute("aria-pressed")).toBe("true");
  expect(native.state).toHaveBeenCalledTimes(reports);
});

test("the host confirms End, and Keep call returns to the call", async () => {
  await connectedHost();
  const end = screen.getByRole("button", { name: "End call" });
  fireEvent.click(end);
  const confirm = screen.getByRole("dialog", { name: "End this call for both of you?" });
  expect(confirm.textContent).toContain("Your guest leaves too, and the invitation stops working.");
  const keep = within(confirm).getByRole("button", { name: "Keep call" });
  expect(document.activeElement).toBe(keep);
  fireEvent.click(keep);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(end);
  expect(sessions.at(-1)!.leave).not.toHaveBeenCalled();
  fireEvent.click(end);
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "End call" }));
  await screen.findByRole("heading", { name: "Call closed" });
  expect(sessions.at(-1)!.leave).toHaveBeenCalledWith(true);
});

test("the self view moves, swaps, minimizes and hides without touching devices", async () => {
  const getUserMedia = vi.fn();
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  const native = await connectedHost();
  const self = screen.getByRole("region", { name: "You" });
  // Arrow keys move the focused self view between corners, and each move is announced.
  self.focus();
  fireEvent.keyDown(self, { key: "ArrowUp" });
  await screen.findByText("Self view moved to the top right");
  fireEvent.keyDown(self, { key: "ArrowLeft" });
  await screen.findByText("Self view moved to the top left");
  // Swap puts you on the stage and the guest in the corner. Focus follows
  // Swap into the guest's corner.
  const swap = within(self).getByRole("button", { name: "Swap with the other video" });
  swap.focus();
  fireEvent.click(swap);
  const remote = screen.getByRole("region", { name: "Guest" });
  expect(within(self).queryByRole("button", { name: "Swap with the other video" })).toBeNull();
  expect(document.activeElement).toBe(within(remote).getByRole("button", { name: "Swap with the other video" }));
  // Minimizing leaves a pill that brings the self view back, and focus moves with it.
  fireEvent.click(within(remote).getByRole("button", { name: "Minimize self view" }));
  expect(screen.queryByRole("region", { name: "You" })).toBeNull();
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Show self view" }));
  fireEvent.click(screen.getByRole("button", { name: "Show self view" }));
  expect(document.activeElement).toBe(screen.getByRole("region", { name: "You" }));
  // Hiding it is undone from More.
  const controls = screen.getByRole("group", { name: "Call controls" });
  fireEvent.click(within(controls).getByRole("button", { name: "More options" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Hide self view" }));
  expect(screen.queryByRole("region", { name: "You" })).toBeNull();
  await screen.findByText("Self view hidden. Show it again from More.");
  fireEvent.click(within(controls).getByRole("button", { name: "More options" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Show self view" }));
  expect(screen.getByRole("region", { name: "You" })).toBeTruthy();
  expect(getUserMedia).not.toHaveBeenCalled();
  expect(native.capture).not.toHaveBeenCalled();
});

test("a Mac page says it is opening from its first frame", () => {
  // Rendered outside act, so effects have not run: this is the first paint.
  const frame = document.body.appendChild(document.createElement("div"));
  const native = { ...nativeHost(), admission: () => new Promise<never>(() => {}) };
  renderNow(<Conversation invitation={null} native={native} />, frame);
  expect(frame.querySelector("h1")?.textContent).toBe("Opening the call…");
  expect(frame.textContent).not.toContain("Open the invitation link");
  renderNow(null, frame);
  frame.remove();
});

test("prejoin menus and the host's End confirmation open outside the scrolling page", async () => {
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn(), enumerateDevices: async () => [] } });
  const native = { ...nativeHost(), admission: async () => ({ ...guest, role: "host" as const, start_muted: false }) };
  const { container } = render(<Conversation invitation={null} native={native} />);
  const chip = await screen.findByRole("button", { name: "Camera: System default" });
  const page = container.querySelector(".prejoin")!;
  // Focus lands in the menu without scrolling the page toward it.
  fireEvent.click(chip);
  const menu = screen.getByRole("menu", { name: "Camera" });
  expect(page.contains(menu)).toBe(false);
  expect(document.activeElement).toBe(within(menu).getByRole("menuitemradio", { name: "System default" }));
  // Scrolling the page leaves the menu behind its button, so it closes.
  fireEvent.scroll(page);
  expect(screen.queryByRole("menu")).toBeNull();
  const end = screen.getByRole("button", { name: "End call" });
  fireEvent.click(end);
  const confirm = screen.getByRole("dialog", { name: "End this call?" });
  expect(page.contains(confirm)).toBe(false);
  expect(document.activeElement).toBe(within(confirm).getByRole("button", { name: "Keep call" }));
  // Escape closes it even with focus elsewhere, and focus returns to End.
  chip.focus();
  fireEvent.keyDown(chip, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(end);
  fireEvent.click(end);
  fireEvent.scroll(page);
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("controls step aside only while connected, and stay while a menu is open", async () => {
  const { container } = render(<Conversation invitation={null} native={nativeHost(async () => true)} />);
  await screen.findByRole("heading", { name: "Waiting for your guest" });
  const call = container.querySelector(".call")!;
  vi.useFakeTimers();
  try {
    act(() => { vi.advanceTimersByTime(5000); });
    expect(call.hasAttribute("data-idle")).toBe(false);
    report(withGuest(true));
    act(() => { vi.advanceTimersByTime(3100); });
    expect(call.hasAttribute("data-idle")).toBe(true);
    // Any input brings them back.
    fireEvent.pointerMove(document);
    expect(call.hasAttribute("data-idle")).toBe(false);
    fireEvent.click(within(screen.getByRole("group", { name: "Call controls" })).getByRole("button", { name: "More options" }));
    expect(screen.getByRole("menu", { name: "More options" })).toBeTruthy();
    act(() => { vi.advanceTimersByTime(10_000); });
    expect(call.hasAttribute("data-idle")).toBe(false);
  } finally { vi.useRealTimers(); }
});

test("a press on the self view holds it in place while the controls return", async () => {
  const { container } = render(<Conversation invitation={null} native={nativeHost(async () => true)} />);
  await screen.findByRole("heading", { name: "Waiting for your guest" });
  const call = container.querySelector(".call")!;
  vi.useFakeTimers();
  try {
    report(withGuest(true));
    // In a top corner, the self view's row depends on whether the top bar shows.
    const self = screen.getByRole("region", { name: "You" });
    fireEvent.keyDown(self, { key: "ArrowUp" });
    act(() => { vi.advanceTimersByTime(3100); });
    expect(call.hasAttribute("data-idle")).toBe(true);
    const resting = self.style.top;
    fireEvent.pointerDown(self, { pointerId: 1, button: 0, pointerType: "touch" });
    // The controls return at once; the self view waits for the press to end.
    expect(call.hasAttribute("data-idle")).toBe(false);
    expect(self.style.top).toBe(resting);
    fireEvent.pointerUp(self, { pointerId: 1, button: 0, pointerType: "touch" });
    expect(self.style.top).toBe(resting);
    act(() => { vi.advanceTimersByTime(200); });
    expect(self.style.top).not.toBe(resting);
  } finally { vi.useRealTimers(); }
});

test("reconnecting shows on the stage, and a departure returns it to waiting", async () => {
  await connectedHost();
  report({ ...withGuest(true), state: "reconnecting" });
  await screen.findByRole("heading", { name: "Reconnecting" });
  expect(screen.getByText("The call continues when the connection returns.")).toBeTruthy();
  report(waiting);
  await screen.findByRole("heading", { name: "Waiting for your guest" });
  await screen.findByText("The guest left");
  expect(screen.queryByText("The call continues when the connection returns.")).toBeNull();
});

test("a full room keeps an explicit retry that joins once the seat frees", async () => {
  const fetcher = redemptions(failure("room_full"), { status: 200, body: guest });
  const getUserMedia = vi.fn();
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia, enumerateDevices: async () => [] } });
  render(<Conversation invitation={invitation} native={undefined} />);
  // Opening the link keeps admission and capture idle.
  expect(fetcher.mock.calls.some(([path]) => /\/(redeem|join)$/.test(path))).toBe(false);
  joinWithoutDevices();
  await screen.findByRole("heading", { name: "Someone else is already in this call" });
  expect(screen.getByRole("alert").textContent).toBe("Only one guest can join at a time. Try again after they leave.");
  expect(screen.getByRole("link", { name: "Open in Camstream" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByRole("heading", { name: "Waiting for the host" });
  expect(screen.queryByRole("alert")).toBeNull();
  expect(fetcher.mock.calls.filter(([path]) => path.endsWith("/redeem"))).toHaveLength(2);
  expect(getUserMedia).not.toHaveBeenCalled();
});

test("ended, expired and invalid invitations stop offering Join and release a preview", async () => {
  for (const [code, heading] of [["ended", "This call has ended"], ["expired", "This invitation has expired"],
    ["invitation_invalid", "This invitation no longer works"]] as const) {
    redemptions(failure(code, 410));
    const track = { kind: "video", readyState: "live", enabled: true, label: "", stop: vi.fn(),
      addEventListener() {}, removeEventListener() {} };
    const { captureConversation } = await import("../services/conversation-media");
    vi.mocked(captureConversation).mockResolvedValue({ getTracks: () => [track] } as unknown as MediaStream);
    vi.stubGlobal("navigator", { mediaDevices: { enumerateDevices: async () => [] } });
    render(<Conversation invitation={invitation} native={undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "Microphone" }));
    fireEvent.click(screen.getByRole("button", { name: "Preview camera" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Join" }).getAttribute("aria-disabled")).toBe("false"));
    fireEvent.click(screen.getByRole("button", { name: "Join" }));
    await screen.findByRole("heading", { name: heading });
    expect(screen.getByRole("alert").textContent).toBe("Ask the host for a new invitation to talk again.");
    expect(screen.queryByRole("button", { name: /Join|Try again/ })).toBeNull();
    expect(screen.queryByRole("link", { name: "Open in Camstream" })).toBeNull();
    expect(track.stop).toHaveBeenCalled();
    cleanup();
  }
});

test("a guest whose room ends is told to ask for a new invitation", async () => {
  redemptions({ status: 200, body: guest });
  render(<Conversation invitation={invitation} native={undefined} />);
  joinWithoutDevices();
  await screen.findByRole("heading", { name: "Waiting for the host" });
  report({ ...waiting, state: "ended" });
  await screen.findByRole("heading", { name: "Call ended" });
  expect(screen.getByText("You can close this page. Ask the host for a new invitation to talk again.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Join/ })).toBeNull();
});

test("a browser host is told to share and restart from the Mac app", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(waiting))));
  render(<Conversation invitation={null} native={undefined} host={{ room: roomID, token: "C".repeat(43) }} />);
  fireEvent.click(await screen.findByRole("button", { name: "Join" }));
  await screen.findByRole("heading", { name: "Waiting for your guest" });
  const stage = screen.getByRole("region", { name: "Guest" });
  expect(within(stage).getByText("Copy the invitation in Camstream on your Mac and send it to your guest.")).toBeTruthy();
  expect(within(stage).queryByRole("button")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "End call" }));
  const confirm = screen.getByRole("dialog", { name: "End this call?" });
  fireEvent.click(within(confirm).getByRole("button", { name: "End call" }));
  await screen.findByText("To talk again, start a new call in Camstream on your Mac.");
  expect(sessions.at(-1)!.leave).toHaveBeenCalledWith(true);
});

test("browser stop acknowledgement waits for a pending input to dispose of its late track", async () => {
  const fetcher = vi.fn(async (_path: string) => new Response(JSON.stringify(waiting)));
  vi.stubGlobal("fetch", fetcher);
  const track = { kind: "video", readyState: "live", stop: vi.fn() };
  let finishCapture!: (stream: MediaStream) => void;
  vi.mocked(captureConversation).mockImplementationOnce(() => new Promise((resolve) => { finishCapture = resolve; }));
  render(<Conversation invitation={null} native={undefined} host={{ room: roomID, token: "C".repeat(43) }} />);
  fireEvent.click(await screen.findByRole("button", { name: "Camera" }));
  fireEvent.click(await screen.findByRole("button", { name: "Join" }));
  await waitFor(() => expect(finishCapture).toBeTypeOf("function"));
  const acknowledgements = () => fetcher.mock.calls.filter(([path]) => path.endsWith("/browser-stopped")).length;
  const before = acknowledgements();
  report({ ...waiting, state: "ended" });
  expect(acknowledgements()).toBe(before);
  finishCapture({ getTracks: () => [track] } as unknown as MediaStream);
  await waitFor(() => expect(acknowledgements()).toBe(before + 1));
  expect(track.stop).toHaveBeenCalledOnce();
});

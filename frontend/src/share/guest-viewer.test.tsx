import { act, cleanup, fireEvent, render, screen } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GuestPhase, GuestSession, GuestView } from "./guest-session";
import { endTime, guestCopy, GuestViewer } from "./guest-viewer";

const END = Date.parse("2026-11-17T23:15:00Z");
const NOW = 50_000;
const stream = () => ({ id: "video" }) as unknown as MediaStream;

function fakeSession(initial: Partial<GuestView> = {}) {
  let view: GuestView = { phase: "ready", media: null, expiresAt: END, reconnectUntil: null, embedded: false, ...initial };
  const listeners = new Set<(view: GuestView) => void>();
  const play = vi.fn();
  const framesStarted = vi.fn();
  const session = {
    get view() {
      return view;
    },
    subscribe(listener: (view: GuestView) => void) {
      listeners.add(listener);
      return () => void listeners.delete(listener);
    },
    play,
    framesStarted,
  } as unknown as GuestSession;
  const set = (next: Partial<GuestView>) =>
    act(() => {
      view = { ...view, ...next };
      for (const listener of listeners) listener(view);
    });
  render(<GuestViewer session={session} monotonic={() => NOW} />);
  return { play, framesStarted, set };
}
const video = () => document.querySelector("video")!;
const buttons = () => screen.queryAllByRole("button").map((button) => button.textContent?.trim() || button.getAttribute("aria-label"));
const flush = () => act(() => new Promise<void>((resolve) => setTimeout(resolve, 0)));
const title = () => document.querySelector(".guest-title")?.textContent;

let mediaPlay: ReturnType<typeof vi.fn<() => Promise<void>>>;
beforeEach(() => {
  mediaPlay = vi.fn(async () => {});
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(mediaPlay);
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  Reflect.deleteProperty(document, "fullscreenEnabled");
});

describe("guest viewer", () => {
  it("says video only and offers no sound, capture or management controls", () => {
    fakeSession();
    expect(screen.getByRole("note", { name: "Video only. This shared camera has no sound." })).toBeTruthy();
    expect(document.querySelector(".guest-stage-note")?.getAttribute("aria-hidden")).toBe("true");
    expect(document.querySelector(".guest-detail")?.textContent).toBe("Video only, without sound.");
    expect(buttons()).toEqual(["Play"]);
    const element = video();
    expect(element.muted).toBe(true);
    expect(element.hasAttribute("controls")).toBe(false);
    expect(element.hasAttribute("playsinline") || element.playsInline).toBe(true);
    expect(element.hidden).toBe(true);
    expect(document.body.innerHTML).not.toMatch(/mute|volume|microphone|unmute|speaker/i);
    expect(document.querySelectorAll("[style]")).toHaveLength(0);
    expect(screen.getByText(`Shared until ${endTime(END)}`)).toBeTruthy();
  });

  it("asks the session for a place only when Play is pressed", () => {
    const { play } = fakeSession();
    expect(play).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    expect(play).toHaveBeenCalledOnce();
  });

  it("attaches muted video and reports Live only once frames are presented", async () => {
    const { set, framesStarted } = fakeSession();
    const media = stream();
    set({ phase: "connecting", media });
    await flush();
    const element = video();
    expect(element.srcObject).toBe(media);
    expect(element.muted && element.defaultMuted).toBe(true);
    expect(element.hidden).toBe(false);
    expect(mediaPlay).toHaveBeenCalledOnce();
    expect(title()).toBe("Connecting…");
    fireEvent(element, new Event("playing"));
    expect(framesStarted).not.toHaveBeenCalled();
    Object.defineProperty(element, "videoWidth", { configurable: true, value: 640 });
    fireEvent(element, new Event("playing"));
    expect(framesStarted).toHaveBeenCalledWith(media);
    set({ phase: "live" });
    expect(screen.getByText("Live", { selector: ".guest-live" })).toBeTruthy();
    expect(screen.getByRole("status").textContent).toBe("Live");
    expect(document.title).toBe("Live · Shared camera");
    expect(buttons()).toEqual([]);
    set({ phase: "expired", media: null });
    expect(element.srcObject).toBeNull();
    expect(element.hidden).toBe(true);
    expect(document.title).toBe("Shared camera");
  });

  it("offers full screen only where the browser allows it", () => {
    Object.defineProperty(document, "fullscreenEnabled", { configurable: true, value: true });
    const { set } = fakeSession({ phase: "live", media: stream() });
    expect(buttons()).toEqual(["Full screen"]);
    set({ phase: "stalled" });
    expect(buttons()).toEqual(["Retry"]);
  });

  it("keeps the picture while reconnecting and counts down the kept place", () => {
    const { set } = fakeSession({ phase: "live", media: stream() });
    set({ phase: "reconnecting", reconnectUntil: NOW + 12_000 });
    expect(screen.getByText("Reconnecting · 12 s")).toBeTruthy();
    expect(screen.getByRole("status").textContent).toBe("Reconnecting…");
    expect(video().hidden).toBe(false);
    expect(buttons()).toEqual([]);
    set({ media: null });
    expect(title()).toBe("Reconnecting…");
    expect(guestCopy({ phase: "reconnecting", media: null, expiresAt: null, reconnectUntil: 0, embedded: false }, 1).detail).toBe(
      "Your place is kept for 1 more second.",
    );
  });

  it("offers Retry where watching can resume and nothing once the link has ended", () => {
    const { set, play } = fakeSession();
    const retry: [GuestPhase, string][] = [
      ["stalled", "Video paused"],
      ["lost", "Connection lost"],
      ["full", "This camera is already being viewed"],
      ["unavailable", "The camera isn't available right now"],
      ["offline", "You're offline"],
    ];
    for (const [phase, heading] of retry) {
      set({ phase });
      expect(title()).toBe(heading);
      fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    }
    expect(play).toHaveBeenCalledTimes(retry.length);
    const ended: [GuestPhase, string][] = [
      ["expired", "This link has expired"],
      ["revoked", "Sharing has stopped"],
      ["denied", "This link doesn't work"],
      ["reopen", "Open the link again"],
      ["unsupported", "This browser can't play the video"],
    ];
    for (const [phase, heading] of ended) {
      set({ phase });
      expect(title()).toBe(heading);
      expect(buttons()).toEqual([]);
      expect(screen.queryByText(/^Shared until/)).toBeNull();
    }
  });

  it("guides in-app browsers to Safari or Chrome", () => {
    const { set } = fakeSession({ embedded: true });
    expect(screen.getByText("If the video doesn't start, open the original link in Safari or Chrome.")).toBeTruthy();
    set({ phase: "unsupported" });
    expect(screen.getByText(/Open the link in Safari or Chrome\. In this app/)).toBeTruthy();
    expect(screen.queryByText(/If the video doesn't start/)).toBeNull();
  });

  it("asks for a tap when the browser blocks playback, without a new admission", async () => {
    mediaPlay.mockRejectedValueOnce(new DOMException("blocked", "NotAllowedError"));
    const { set, play } = fakeSession();
    set({ phase: "live", media: stream() });
    await flush();
    expect(title()).toBe("Tap Play to start the video");
    expect(screen.queryByText("Live", { selector: ".guest-live" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    await flush();
    expect(play).not.toHaveBeenCalled();
    expect(mediaPlay).toHaveBeenCalledTimes(2);
    expect(screen.getByText("Live", { selector: ".guest-live" })).toBeTruthy();
  });

  it("adds the date when the share ends on another day", () => {
    const noon = new Date(2026, 10, 17, 12, 0).getTime();
    expect(endTime(noon + 3_600_000, noon, "en-GB")).toBe("13:00");
    expect(endTime(noon + 3 * 86_400_000, noon, "en-GB")).toBe("20 Nov, 12:00");
  });
});

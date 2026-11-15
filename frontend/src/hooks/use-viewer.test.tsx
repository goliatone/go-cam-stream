import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";
import { useViewer } from "./use-viewer";
import type { PlaybackSnapshot } from "../services/playback";
import type { CameraList } from "../services/contracts";
import samples from "../../../data/api/examples.json";

const control = vi.hoisted(() => ({
  changed: undefined as
    | ((value: PlaybackSnapshot | undefined) => void)
    | undefined,
  select: vi.fn().mockResolvedValue(undefined),
  retry: vi.fn().mockResolvedValue(undefined),
  dispose: vi.fn().mockResolvedValue(undefined),
  observe: vi.fn(),
  playing: vi.fn(),
  waiting: vi.fn(),
  mediaError: vi.fn(),
}));
vi.mock("../services/playback", () => ({
  PlaybackController: class {
    constructor(_api: unknown, changed: typeof control.changed) {
      control.changed = changed;
    }
    select = control.select;
    retry = control.retry;
    dispose = control.dispose;
    observe = control.observe;
    playing = control.playing;
    waiting = control.waiting;
    mediaError = control.mediaError;
  },
}));
const catalog = samples.catalog as CameraList;
const api = {
  list: vi.fn(async () => catalog),
  status: vi.fn(),
  create: vi.fn(),
  close: vi.fn(),
};
function Live() {
  return <App {...useViewer(api)} />;
}
const ref = { camera_id: "room1", stream_id: "default" };
function snapshot(
  state: PlaybackSnapshot["state"] = "connecting",
): PlaybackSnapshot {
  return { selection: ref, state, media: {} as MediaStream };
}
beforeEach(() => {
  vi.clearAllMocks();
  api.list.mockResolvedValue(catalog);
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(async () => {
  await act(() => cleanup());
  vi.restoreAllMocks();
  history.replaceState(null, "", "/");
});

describe("live media binding", () => {
  it("offers a user gesture after autoplay rejection and binds actual playing events", async () => {
    const play = vi
      .spyOn(HTMLMediaElement.prototype, "play")
      .mockRejectedValueOnce(new DOMException("blocked", "NotAllowedError"))
      .mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() =>
      expect(control.select).toHaveBeenCalledWith(ref, {
        audio: true,
        video: true,
      }),
    );
    const next = snapshot();
    act(() => control.changed!(next));
    await screen.findByRole("button", { name: "Play video" });
    expect(control.playing).not.toHaveBeenCalled();
    const video = document.querySelector("video")!;
    expect(video.srcObject).toBe(next.media);
    expect(video.muted).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Play video" }));
    await waitFor(() => expect(play).toHaveBeenCalledTimes(2));
    Object.defineProperties(video, {
      paused: { configurable: true, value: false },
      readyState: { configurable: true, value: 2 },
    });
    fireEvent(video, new Event("playing"));
    expect(control.playing).toHaveBeenCalledOnce();
    fireEvent(video, new Event("waiting"));
    expect(control.waiting).toHaveBeenCalledOnce();
    fireEvent(video, new Event("error"));
    expect(control.mediaError).toHaveBeenCalledOnce();
  });
  it("keeps the same video node through observations and detaches on selection removal", async () => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    const view = render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    const next = snapshot("playing");
    act(() => control.changed!(next));
    await waitFor(() =>
      expect(document.querySelector("video")!.srcObject).toBe(next.media),
    );
    const video = document.querySelector("video")!;
    act(() =>
      control.changed!({
        ...next,
        status: {
          stream_id: "default",
          state: "playing",
          viewers: 2,
          observed_at: null,
        },
      }),
    );
    expect(document.querySelector("video")).toBe(video);
    act(() => view.unmount());
    expect(video.srcObject).toBeNull();
    expect(control.dispose).toHaveBeenCalled();
  });
  it("pauses only observation when hidden and uses keepalive cleanup on exit", async () => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    fireEvent(document, new Event("visibilitychange"));
    expect(control.observe).toHaveBeenCalledWith(false);
    expect(control.dispose).not.toHaveBeenCalled();
    fireEvent(
      window,
      new PageTransitionEvent("pagehide", { persisted: false }),
    );
    expect(control.dispose).toHaveBeenCalledWith(true);
  });
  it("releases before back-forward caching and reloads authorized scope on restore", async () => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    fireEvent(window, new PageTransitionEvent("pagehide", { persisted: true }));
    expect(control.select).toHaveBeenLastCalledWith(undefined);
    expect(control.dispose).not.toHaveBeenCalled();
    fireEvent(window, new PageTransitionEvent("pageshow", { persisted: true }));
    await waitFor(() => expect(api.list).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(control.select).toHaveBeenLastCalledWith(ref, {
      audio: true,
      video: true,
    }));
  });
});

describe("addresses and paused video", () => {
  it("opens the linked camera without connecting to the first one", async () => {
    history.replaceState(null, "", "/?camera=room2");
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    render(<Live />);
    const room2 = { camera_id: "room2", stream_id: "default" };
    await waitFor(() =>
      expect(control.select).toHaveBeenLastCalledWith(room2, expect.anything()),
    );
    expect(control.select).not.toHaveBeenCalledWith(ref, expect.anything());
    expect(location.search).toBe("?camera=room2");
    fireEvent.click(screen.getByRole("link", { name: /Room 1/ }));
    await waitFor(() => expect(location.search).toBe("?camera=room1"));
    expect(history.length).toBe(1);
  });

  it("shows Paused and offers Play when live video pauses", async () => {
    const play = vi
      .spyOn(HTMLMediaElement.prototype, "play")
      .mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    act(() => control.changed!(snapshot("playing")));
    const video = document.querySelector("video")!;
    await waitFor(() => expect(video.srcObject).toBeTruthy());
    fireEvent(video, new Event("pause"));
    expect(await screen.findByRole("heading", { name: "Paused" })).toBeTruthy();
    expect(document.title).toBe("Room 1 · Paused · camstream");
    fireEvent.click(screen.getByRole("button", { name: "Play video" }));
    await waitFor(() => expect(play).toHaveBeenCalledTimes(2));
    fireEvent(video, new Event("play"));
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: "Paused" })).toBeNull(),
    );
    expect(document.title).toBe("Room 1 · Live · camstream");
  });

  it("keeps the new view's Play prompt when an old Play request finishes", async () => {
    let finishOld!: () => void;
    const pending = new Promise<void>((resolve) => {
      finishOld = resolve;
    });
    vi.spyOn(HTMLMediaElement.prototype, "play")
      .mockRejectedValueOnce(new DOMException("blocked", "NotAllowedError"))
      .mockImplementationOnce(() => pending)
      .mockRejectedValueOnce(new DOMException("blocked", "NotAllowedError"));
    render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    act(() => control.changed!(snapshot("playing")));
    fireEvent.click(await screen.findByRole("button", { name: "Play video" }));
    fireEvent.click(screen.getByRole("link", { name: /Room 2/ }));
    const room2 = { camera_id: "room2", stream_id: "default" };
    await waitFor(() =>
      expect(control.select).toHaveBeenLastCalledWith(room2, expect.anything()),
    );
    act(() => control.changed!({ ...snapshot("playing"), selection: room2 }));
    await screen.findByRole("button", { name: "Play video" });
    await act(async () => finishOld());
    expect(screen.getByRole("button", { name: "Play video" })).toBeTruthy();
    await waitFor(() =>
      expect(document.title).toBe("Room 2 · Ready to play · camstream"),
    );
  });
});

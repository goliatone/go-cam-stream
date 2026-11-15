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
  stop: vi.fn().mockResolvedValue(undefined),
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
    stop = control.stop;
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

  it("pauses live video from the toolbar", async () => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    act(() => control.changed!(snapshot("playing")));
    fireEvent.click(await screen.findByRole("button", { name: "Pause" }));
    expect(HTMLMediaElement.prototype.pause).toHaveBeenCalledOnce();
    fireEvent(document.querySelector("video")!, new Event("pause"));
    expect(await screen.findByRole("heading", { name: "Paused" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Play" })).toBeTruthy();
  });

  it("stops the view, detaches its media and starts again only on Play", async () => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    const live = snapshot("playing");
    act(() => control.changed!(live));
    const video = document.querySelector("video")!;
    await waitFor(() => expect(video.srcObject).toBe(live.media));
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(control.stop).toHaveBeenCalledOnce());
    act(() => control.changed!({ selection: ref, state: "closing", media: null }));
    expect(await screen.findByRole("heading", { name: "Stopping…" })).toBeTruthy();
    expect(video.srcObject).toBeNull();
    act(() => control.changed!({ selection: ref, state: "closed", media: null }));
    expect(
      await screen.findByRole("heading", { name: "Video stopped" }),
    ).toBeTruthy();
    expect(document.title).toBe("Room 1 · Stopped · camstream");
    expect(control.retry).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Play video" }));
    await waitFor(() => expect(control.retry).toHaveBeenCalledOnce());
  });

  it("lets Stop end a stopped view's Play while it connects", async () => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    act(() => control.changed!({ selection: ref, state: "closed", media: null }));
    // Negotiation can take seconds; the retry settles only when it ends.
    control.retry.mockReturnValueOnce(new Promise(() => {}));
    fireEvent.click(await screen.findByRole("button", { name: "Play video" }));
    expect(control.retry).toHaveBeenCalledOnce();
    act(() =>
      control.changed!({ selection: ref, state: "connecting", media: null }),
    );
    await waitFor(() =>
      expect(document.querySelector(".player-hint")!.textContent).toBe(""),
    );
    fireEvent.keyDown(document.body, { key: "s" });
    await waitFor(() => expect(control.stop).toHaveBeenCalledOnce());
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

  it("offers Play when the connection recovers while video is paused", async () => {
    const play = vi
      .spyOn(HTMLMediaElement.prototype, "play")
      .mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() => expect(control.select).toHaveBeenCalled());
    const live = snapshot("playing");
    act(() => control.changed!(live));
    const video = document.querySelector("video")!;
    await waitFor(() => expect(video.srcObject).toBe(live.media));
    fireEvent(video, new Event("pause"));
    // The peer drops and recovers; a paused element reports no playback, so
    // the controller stays at reconnecting until Play resumes the element.
    act(() => control.changed!({ ...live, state: "reconnecting" }));
    expect(await screen.findByRole("heading", { name: "Paused" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Play video" }));
    await waitFor(() => expect(play).toHaveBeenCalledTimes(2));
    Object.defineProperties(video, {
      paused: { configurable: true, value: false },
      readyState: { configurable: true, value: 2 },
    });
    fireEvent(video, new Event("play"));
    fireEvent(video, new Event("playing"));
    expect(control.playing).toHaveBeenCalledOnce();
  });

  it("keeps other address parts and restores the view's address on Back", async () => {
    history.replaceState(null, "", "/?camera=room1&lang=en#viewer");
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() =>
      expect(control.select).toHaveBeenCalledWith(ref, expect.anything()),
    );
    fireEvent.click(screen.getByRole("link", { name: /Room 2/ }));
    await waitFor(() => expect(location.search).toBe("?camera=room2&lang=en"));
    expect(location.hash).toBe("#viewer");
    // Back reaches an older same-document entry that still names Room 1, as
    // after following the skip link.
    history.replaceState(null, "", "/?camera=room1");
    fireEvent(window, new PopStateEvent("popstate"));
    expect(location.search).toBe("?camera=room2");
  });

  it("switches cameras even when the browser refuses an address update", async () => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    render(<Live />);
    await waitFor(() =>
      expect(control.select).toHaveBeenCalledWith(ref, expect.anything()),
    );
    vi.spyOn(history, "replaceState").mockImplementation(() => {
      throw new DOMException("Too many calls", "SecurityError");
    });
    fireEvent.click(screen.getByRole("link", { name: /Room 2/ }));
    await waitFor(() =>
      expect(control.select).toHaveBeenLastCalledWith(
        { camera_id: "room2", stream_id: "default" },
        expect.anything(),
      ),
    );
  });

  it("disables iPhone video fullscreen once the media is removed", async () => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    Object.defineProperty(HTMLVideoElement.prototype, "webkitEnterFullscreen", {
      configurable: true,
      value: vi.fn(),
    });
    try {
      render(<Live />);
      await waitFor(() => expect(control.select).toHaveBeenCalled());
      const live = snapshot("playing");
      act(() => control.changed!(live));
      const video = document.querySelector("video")!;
      Object.defineProperty(video, "readyState", {
        configurable: true,
        value: 1,
      });
      fireEvent(video, new Event("loadedmetadata"));
      const button = () => screen.getByRole("button", { name: "Fullscreen" });
      await waitFor(() =>
        expect(button().getAttribute("aria-disabled")).toBeNull(),
      );
      Object.defineProperty(video, "readyState", {
        configurable: true,
        value: 0,
      });
      act(() =>
        control.changed!({
          ...live,
          state: "failed",
          media: null,
          error: { code: "media_connection_failed", message: "", retryable: true },
        }),
      );
      await waitFor(() =>
        expect(button().getAttribute("aria-disabled")).toBe("true"),
      );
      expect(
        screen.getByText("Fullscreen is available after video arrives."),
      ).toBeTruthy();
    } finally {
      Reflect.deleteProperty(HTMLVideoElement.prototype, "webkitEnterFullscreen");
    }
  });
});

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App, type ViewerInput } from "./app";
import type {
  CameraList,
  ErrorBody,
  State,
  StreamRef,
} from "./services/contracts";
import { errorCopy } from "./components/status";
import samples from "../../data/api/examples.json";

afterEach(cleanup);
const catalog: CameraList = {
  cameras: [
    {
      ...samples.catalog.cameras[0]!,
      availability: "idle",
      streams: [
        ...samples.catalog.cameras[0]!.streams,
        {
          id: "compact",
          name: "Compact",
          capabilities: { video: true, audio: false },
        },
      ],
    },
    { ...samples.catalog.cameras[1]!, availability: "playing" },
  ],
};
function input(state: State = "playing"): ViewerInput {
  return {
    selection: { camera_id: "room1", stream_id: "default" },
    status: { stream_id: "default", state, viewers: null, observed_at: null },
    presentation: {
      state,
      controls: {
        audio: { enabled: true },
        fullscreen: { enabled: true },
        pip: { enabled: true },
      },
      actions: {
        onPlay: vi.fn(),
        onPause: vi.fn(),
        onStop: vi.fn(),
        onMute: vi.fn(),
        onRetry: vi.fn(),
        onFullscreen: vi.fn(),
        onPictureInPicture: vi.fn(),
      },
    },
  };
}

describe("camera selection and scope", () => {
  it("shows the published catalog, supported streams and exactly one player", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<App catalog={catalog} onSelect={onSelect} />);
    expect(document.querySelectorAll("video")).toHaveLength(1);
    expect(
      screen
        .getByRole("combobox", { name: "Stream" })
        .querySelectorAll("option"),
    ).toHaveLength(2);
    await user.selectOptions(screen.getByLabelText("Stream"), "compact");
    await waitFor(() =>
      expect(onSelect).toHaveBeenLastCalledWith({
        camera_id: "room1",
        stream_id: "compact",
      }),
    );
    await user.click(screen.getByRole("link", { name: /Room 2/ }));
    await waitFor(() =>
      expect(onSelect).toHaveBeenLastCalledWith({
        camera_id: "room2",
        stream_id: "default",
      }),
    );
    expect(document.querySelectorAll("video")).toHaveLength(1);
    expect(screen.getByText("This stream has no audio.")).toBeTruthy();
    expect(
      screen.getByLabelText("Stream").querySelectorAll("option"),
    ).toHaveLength(1);
  });

  it("resets video, mute and action feedback when switching streams or cameras", async () => {
    const user = userEvent.setup();
    const viewer = input();
    viewer.presentation.actions!.onFullscreen = () =>
      Promise.reject(new Error("private detail"));
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    const firstVideo = document.querySelector("video");
    await user.click(screen.getByRole("button", { name: "Unmute" }));
    expect(viewer.presentation.actions!.onMute).toHaveBeenCalledWith(false);
    // The label names the next action; there is no pressed state to misread.
    expect(
      screen.getByRole("button", { name: "Mute" }).hasAttribute("aria-pressed"),
    ).toBe(false);
    await user.click(screen.getByRole("button", { name: "Fullscreen" }));
    expect(
      await screen.findByText(
        "Fullscreen could not be completed. Please try again.",
      ),
    ).toBeTruthy();
    expect(screen.queryByText(/private detail/)).toBeNull();
    await user.selectOptions(screen.getByLabelText("Stream"), "compact");
    expect(document.querySelector("video")).not.toBe(firstVideo);
    expect(document.querySelector("video")!.muted).toBe(true);
    expect(screen.queryByText(/Fullscreen could not/)).toBeNull();
    expect(screen.getByText("Getting ready")).toBeTruthy();
    const compact = document.querySelector("video");
    await user.click(screen.getByRole("link", { name: /Room 2/ }));
    expect(document.querySelector("video")).not.toBe(compact);
    rerender(<App catalog={{ cameras: [] }} viewer={viewer} />);
    expect(document.querySelector("video")).toBeNull();
  });

  it("removes camera details and player when permission is denied", () => {
    const { rerender } = render(<App catalog={catalog} viewer={input()} />);
    rerender(
      <App catalog={catalog} error={samples.denied.error as ErrorBody} />,
    );
    expect(screen.getByRole("alert").textContent).toContain("You don't have access");
    expect(screen.queryByText("Room 1")).toBeNull();
    expect(document.querySelector("video")).toBeNull();
  });

  it("falls back safely when the selected camera disappears and ignores mismatched observations", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<App catalog={catalog} viewer={input()} />);
    await user.click(screen.getByRole("link", { name: /Room 2/ }));
    expect(screen.getByText("Getting ready")).toBeTruthy();
    rerender(
      <App catalog={{ cameras: [catalog.cameras[0]!] }} viewer={input()} />,
    );
    expect(screen.getByRole("heading", { name: "Room 1" })).toBeTruthy();
    const viewer = input();
    viewer.status = {
      stream_id: "wrong-stream",
      state: "playing",
      viewers: 99,
      observed_at: null,
    };
    rerender(<App catalog={catalog} viewer={viewer} />);
    expect(screen.queryByText("99")).toBeNull();
  });
});

it("reports initial selection, catalog removal and denied scope to the integration owner", async () => {
  const onSelect = vi.fn();
  const { rerender } = render(<App catalog={catalog} onSelect={onSelect} />);
  await waitFor(() =>
    expect(onSelect).toHaveBeenLastCalledWith({
      camera_id: "room1",
      stream_id: "default",
    }),
  );
  rerender(
    <App catalog={{ cameras: [catalog.cameras[1]!] }} onSelect={onSelect} />,
  );
  await waitFor(() =>
    expect(onSelect).toHaveBeenLastCalledWith({
      camera_id: "room2",
      stream_id: "default",
    }),
  );
  rerender(
    <App
      catalog={catalog}
      error={samples.denied.error as ErrorBody}
      onSelect={onSelect}
    />,
  );
  await waitFor(() => expect(onSelect).toHaveBeenLastCalledWith(undefined));
});

describe("states and controls", () => {
  it.each([
    "unknown",
    "idle",
    "connecting",
    "playing",
    "reconnecting",
    "closing",
    "closed",
    "offline",
    "failed",
  ] as const)("presents %s without adding players", (state) => {
    render(<App catalog={catalog} viewer={input(state)} />);
    expect(document.querySelectorAll("video")).toHaveLength(1);
    expect(document.querySelector(".stage-badge")!.textContent).toBeTruthy();
  });

  it("handles empty, loading and cameras without streams", () => {
    const { rerender } = render(<App catalog={samples.empty_catalog} />);
    expect(
      screen.getByRole("heading", { name: "No cameras yet" }),
    ).toBeTruthy();
    rerender(<App catalog={catalog} loading />);
    expect(
      screen.getByRole("heading", { name: "Loading cameras" }),
    ).toBeTruthy();
    expect(screen.queryByText("Room 1")).toBeNull();
    rerender(
      <App
        catalog={{
          cameras: [
            {
              id: "none",
              name: "No profiles",
              availability: "unknown",
              streams: [],
            },
          ],
        }}
      />,
    );
    expect(screen.queryByRole("link", { name: /No profiles/ })).toBeNull();
    expect(
      screen.getByText("No profiles").closest("[aria-disabled='true']"),
    ).toBeTruthy();
    expect(
      screen.getByRole("heading", { name: "No supported streams" }),
    ).toBeTruthy();
    expect(document.querySelector("video")).toBeNull();
  });

  it("distinguishes unknown observations from zero", () => {
    const viewer = input();
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    expect(screen.getByText("Unknown")).toBeTruthy();
    expect(screen.getByText("No update yet")).toBeTruthy();
    viewer.status = {
      stream_id: "default",
      state: "idle",
      viewers: 0,
      observed_at: "2026-11-14T14:30:00Z",
    };
    rerender(<App catalog={catalog} viewer={{ ...viewer }} />);
    expect(screen.getByText("0")).toBeTruthy();
    expect(screen.queryByText("No update yet")).toBeNull();
  });

  it("explains unsupported controls and invokes only supported callbacks", async () => {
    const user = userEvent.setup();
    const viewer = input();
    viewer.presentation.controls!.pip = {
      enabled: false,
      reason: "Picture in picture is not supported by this browser.",
    };
    render(<App catalog={catalog} viewer={viewer} />);
    // Unavailable controls stay focusable, so the reason is reachable by
    // keyboard and touch, not hover alone.
    const pip = screen.getByRole("button", { name: "Picture in picture" });
    expect(pip.getAttribute("aria-disabled")).toBe("true");
    expect(
      document.getElementById(pip.getAttribute("aria-describedby")!)!
        .textContent,
    ).toBe("Picture in picture is not supported by this browser.");
    await user.click(pip);
    expect(
      viewer.presentation.actions!.onPictureInPicture,
    ).not.toHaveBeenCalled();
    expect(document.querySelector(".player-hint")!.textContent).toBe(
      "Picture in picture is not supported by this browser.",
    );
    await user.click(screen.getByRole("button", { name: "Fullscreen" }));
    expect(viewer.presentation.actions!.onFullscreen).toHaveBeenCalledOnce();
  });

  it("gates retry by the published error and prevents concurrent clicks", async () => {
    let finish!: () => void;
    const viewer = input("failed");
    const retry = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    viewer.presentation.actions!.onRetry = retry;
    viewer.presentation.error = {
      code: "backend_unavailable",
      message: "never render this",
      retryable: true,
    };
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    expect(screen.getByText(errorCopy.backend_unavailable.title)).toBeTruthy();
    const button = screen.getByRole<HTMLButtonElement>("button", {
      name: "Try again",
    });
    button.focus();
    await userEvent.setup().keyboard("{Enter}");
    expect(retry).toHaveBeenCalledOnce();
    expect(button.disabled).toBe(true);
    fireEvent.click(button);
    expect(retry).toHaveBeenCalledOnce();
    finish();
    await waitFor(() => expect(button.disabled).toBe(false));
    viewer.presentation.error = samples.offline_status.streams[0]!
      .error as ErrorBody;
    rerender(<App catalog={catalog} viewer={{ ...viewer }} />);
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    expect(
      screen.getByText("Trying again from this page won't fix this."),
    ).toBeTruthy();
    expect(screen.queryByText("never render this")).toBeNull();
  });

  it("discards late action failures after selection changes", async () => {
    let reject!: (error: Error) => void;
    const viewer = input();
    viewer.presentation.actions!.onFullscreen = () =>
      new Promise<void>((_, fail) => {
        reject = fail;
      });
    render(<App catalog={catalog} viewer={viewer} />);
    fireEvent.click(screen.getByRole("button", { name: "Fullscreen" }));
    fireEvent.click(screen.getByRole("link", { name: /Room 2/ }));
    reject(new Error("old view"));
    await waitFor(() =>
      expect(screen.queryByText(/Fullscreen could not/)).toBeNull(),
    );
    expect(screen.getByText("Getting ready")).toBeTruthy();
  });

  it.each(Object.keys(errorCopy) as (keyof typeof errorCopy)[])(
    "projects %s without raw details",
    (code) => {
      const viewer = input("failed");
      viewer.presentation.error = {
        code,
        message: "rtsp://secret:password@camera-private",
        retryable: false,
      };
      render(<App catalog={catalog} viewer={viewer} />);
      expect(screen.getByText(errorCopy[code].title)).toBeTruthy();
      expect(document.body.textContent).not.toContain("secret:password");
    },
  );
});

it("offers a fresh page after lost session ownership, without retrying cleanup forever", () => {
  const viewer = input("failed");
  viewer.presentation.error = { code: "cleanup_incomplete", message: "", retryable: false };
  viewer.presentation.reloadRequired = true;
  render(<App catalog={catalog} viewer={viewer} />);
  // Reload keeps the selected view instead of returning to the first camera.
  expect(screen.getByRole("link", { name: "Reload page" }).getAttribute("href")).toBe("?camera=room1");
  expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  expect(screen.getByText("This view needs a reload")).toBeTruthy();
});

describe("addressable views and tab state", () => {
  it("opens the linked camera and stream, and falls back silently", async () => {
    const cases: [Partial<StreamRef>, StreamRef][] = [
      [
        { camera_id: "room1", stream_id: "compact" },
        { camera_id: "room1", stream_id: "compact" },
      ],
      [{ camera_id: "room2" }, { camera_id: "room2", stream_id: "default" }],
      [
        { camera_id: "room2", stream_id: "missing" },
        { camera_id: "room2", stream_id: "default" },
      ],
      [
        { camera_id: "missing", stream_id: "compact" },
        { camera_id: "room1", stream_id: "default" },
      ],
    ];
    for (const [link, expected] of cases) {
      const onSelect = vi.fn();
      const view = render(
        <App catalog={catalog} initialSelection={link} onSelect={onSelect} />,
      );
      await waitFor(() => expect(onSelect).toHaveBeenLastCalledWith(expected));
      expect(onSelect).toHaveBeenCalledOnce();
      view.unmount();
    }
  });

  it("renders camera rows as links that switch in place on a plain click", async () => {
    const onSelect = vi.fn();
    render(<App catalog={catalog} onSelect={onSelect} />);
    const room2 = screen.getByRole("link", { name: /Room 2/ });
    expect(
      screen.getByRole("link", { name: /Room 1/ }).getAttribute("aria-current"),
    ).toBe("page");
    expect(room2.getAttribute("href")).toBe("?camera=room2");
    // A modified click keeps the browser's new-tab behavior.
    let followed = false;
    const observe = (event: Event) => {
      followed = !event.defaultPrevented;
      event.preventDefault();
    };
    document.addEventListener("click", observe);
    fireEvent.click(room2, { metaKey: true });
    document.removeEventListener("click", observe);
    expect(followed).toBe(true);
    expect(onSelect).not.toHaveBeenCalledWith({
      camera_id: "room2",
      stream_id: "default",
    });
    expect(fireEvent.click(room2)).toBe(false);
    await waitFor(() =>
      expect(onSelect).toHaveBeenLastCalledWith({
        camera_id: "room2",
        stream_id: "default",
      }),
    );
    expect(room2.getAttribute("aria-current")).toBe("page");
  });

  it("shows the view in the tab title and favicon", () => {
    const icon = () =>
      decodeURIComponent(
        document
          .querySelector<HTMLLinkElement>('link[rel~="icon"]')!
          .getAttribute("href")!,
      );
    const viewer = input();
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    expect(document.title).toBe("Room 1 · Live · camstream");
    expect(icon()).toContain("#1e9e5a");
    rerender(
      <App
        catalog={catalog}
        viewer={{
          ...viewer,
          presentation: { ...viewer.presentation, paused: true },
        }}
      />,
    );
    expect(document.title).toBe("Room 1 · Paused · camstream");
    expect(icon()).not.toContain("cx='25'");
    rerender(<App catalog={catalog} />);
    expect(document.title).toBe("Room 1 · camstream");
    rerender(
      <App catalog={catalog} error={samples.denied.error as ErrorBody} />,
    );
    expect(document.title).toBe("camstream");
    expect(icon()).toContain("#d23f3f");
  });

  it("offers Play when live video is paused", async () => {
    const user = userEvent.setup();
    const viewer = input();
    const onPlay = vi.fn();
    viewer.presentation.paused = true;
    viewer.presentation.actions!.onPlay = onPlay;
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    expect(document.querySelector(".stage-badge")!.textContent).toBe("Paused");
    expect(screen.getByRole("heading", { name: "Paused" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Play video" }));
    expect(onPlay).toHaveBeenCalledOnce();
    // Video resumes and the prompt goes away; focus stays in the player.
    rerender(
      <App
        catalog={catalog}
        viewer={{
          ...viewer,
          presentation: { ...viewer.presentation, paused: false },
        }}
      />,
    );
    expect(document.activeElement).toBe(
      document.querySelector("section.player"),
    );
  });

  it("offers Play when video paused across a brief reconnect", () => {
    const viewer = input("reconnecting");
    viewer.presentation.paused = true;
    viewer.presentation.actions!.onPlay = vi.fn();
    render(<App catalog={catalog} viewer={viewer} />);
    expect(document.querySelector(".stage-badge")!.textContent).toBe("Paused");
    expect(screen.getByRole("heading", { name: "Paused" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Reconnecting…" })).toBeNull();
    expect(screen.getByRole("button", { name: "Play video" })).toBeTruthy();
    expect(document.querySelector("video")!.getAttribute("aria-busy")).toBe(
      "false",
    );
    expect(document.title).toBe("Room 1 · Paused · camstream");
  });

  it("fullscreens the player root and labels the way back", async () => {
    const user = userEvent.setup();
    const viewer = input();
    render(<App catalog={catalog} viewer={viewer} />);
    const root = document.querySelector<HTMLElement>("main")!;
    const player = document.querySelector<HTMLElement>("section.player")!;
    expect(root.contains(player)).toBe(true);
    await user.click(screen.getByRole("button", { name: "Fullscreen" }));
    // The root outlives camera switches, so fullscreen survives them.
    expect(viewer.presentation.actions!.onFullscreen).toHaveBeenCalledWith(
      root,
    );
    Object.defineProperty(document, "fullscreenElement", {
      configurable: true,
      value: root,
    });
    try {
      fireEvent(document, new Event("fullscreenchange"));
      expect(
        screen.getByRole("button", { name: "Exit fullscreen" }),
      ).toBeTruthy();
      expect(player.hasAttribute("data-fullscreen")).toBe(true);
      fireEvent.keyDown(document.body, { key: "]" });
      await waitFor(() =>
        expect(document.querySelector("section.player")).not.toBe(player),
      );
      expect(
        document
          .querySelector("section.player")!
          .hasAttribute("data-fullscreen"),
      ).toBe(true);
    } finally {
      Reflect.deleteProperty(document, "fullscreenElement");
    }
  });

  it("keeps the selected stream in the reload address", () => {
    const viewer = input("failed");
    viewer.selection = { camera_id: "room1", stream_id: "compact" };
    viewer.presentation.error = {
      code: "cleanup_incomplete",
      message: "",
      retryable: false,
    };
    viewer.presentation.reloadRequired = true;
    render(
      <App
        catalog={catalog}
        viewer={viewer}
        initialSelection={{ camera_id: "room1", stream_id: "compact" }}
      />,
    );
    expect(
      screen.getByRole("link", { name: "Reload page" }).getAttribute("href"),
    ).toBe("?camera=room1&stream=compact");
  });
});

describe("video-first shell", () => {
  const paused = (viewer: ViewerInput): ViewerInput => ({
    ...viewer,
    presentation: { ...viewer.presentation, paused: true },
  });

  it("hides the controls during playback until the pointer, a tap or focus returns", () => {
    vi.useFakeTimers();
    try {
      const viewer = input();
      const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
      const player = document.querySelector<HTMLElement>("section.player")!;
      const video = player.querySelector("video")!;
      const idle = () => player.hasAttribute("data-idle");
      act(() => {
        vi.advanceTimersByTime(2900);
      });
      expect(idle()).toBe(false);
      act(() => {
        vi.advanceTimersByTime(200);
      });
      expect(idle()).toBe(true);
      fireEvent.pointerMove(player, { pointerType: "mouse" });
      expect(idle()).toBe(false);
      act(() => {
        vi.advanceTimersByTime(3000);
      });
      expect(idle()).toBe(true);
      // A tap shows them; a tap on the video while they show hides them.
      fireEvent.pointerUp(video, { pointerType: "touch" });
      expect(idle()).toBe(false);
      fireEvent.pointerUp(video, { pointerType: "touch" });
      expect(idle()).toBe(true);
      fireEvent.focus(screen.getByRole("button", { name: "Stop" }));
      expect(idle()).toBe(false);
      // Anything that waits on the viewer keeps them in view.
      rerender(<App catalog={catalog} viewer={paused(viewer)} />);
      act(() => {
        vi.advanceTimersByTime(10000);
      });
      expect(idle()).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("stops the view, frees the slot and offers Play", async () => {
    const user = userEvent.setup();
    const viewer = input();
    const actions = viewer.presentation.actions!;
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    await user.click(screen.getByRole("button", { name: "Stop" }));
    expect(actions.onStop).toHaveBeenCalledOnce();
    rerender(
      <App
        catalog={catalog}
        viewer={{
          ...viewer,
          presentation: { ...viewer.presentation, state: "closed" },
        }}
      />,
    );
    expect(screen.getByRole("heading", { name: "Video stopped" })).toBeTruthy();
    expect(
      screen.getByText("Your viewer slot is free. Play to watch again."),
    ).toBeTruthy();
    expect(document.querySelector(".stage-badge")!.textContent).toBe("Stopped");
    expect(document.title).toBe("Room 1 · Stopped · camstream");
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    const stop = screen.getByRole("button", { name: "Stop" });
    expect(stop.getAttribute("aria-disabled")).toBe("true");
    await user.click(stop);
    expect(actions.onStop).toHaveBeenCalledOnce();
    expect(document.querySelector(".player-hint")!.textContent).toBe(
      "Nothing is playing.",
    );
    // Play starts a new session; nothing restarts on its own.
    await user.click(screen.getByRole("button", { name: "Play video" }));
    expect(actions.onRetry).toHaveBeenCalledOnce();
    await user.click(screen.getByRole("button", { name: "Play" }));
    expect(actions.onRetry).toHaveBeenCalledTimes(2);
    expect(actions.onPlay).not.toHaveBeenCalled();
  });

  it("plays and pauses from the toolbar and explains when it cannot", async () => {
    const user = userEvent.setup();
    const viewer = input();
    const actions = viewer.presentation.actions!;
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(actions.onPause).toHaveBeenCalledOnce();
    rerender(<App catalog={catalog} viewer={paused(viewer)} />);
    await user.click(screen.getByRole("button", { name: "Play" }));
    expect(actions.onPlay).toHaveBeenCalledOnce();
    rerender(<App catalog={catalog} viewer={input("connecting")} />);
    const play = screen.getByRole("button", { name: "Play" });
    expect(play.getAttribute("aria-disabled")).toBe("true");
    await user.click(play);
    expect(document.querySelector(".player-hint")!.textContent).toBe(
      "Available while video is connected.",
    );
  });

  it("drives the player and the camera list from the keyboard", async () => {
    const viewer = input();
    const actions = viewer.presentation.actions!;
    const onSelect = vi.fn();
    render(<App catalog={catalog} viewer={viewer} onSelect={onSelect} />);
    // The player runs one action at a time; wait for each to finish.
    const press = async (
      key: string,
      init: KeyboardEventInit = {},
      target: Element = document.body,
    ) => {
      fireEvent.keyDown(target, { key, ...init });
      await waitFor(() =>
        expect(document.querySelector(".player-hint")!.textContent).toBe(""),
      );
    };
    await press("m");
    expect(actions.onMute).toHaveBeenCalledWith(false);
    await press("f");
    expect(actions.onFullscreen).toHaveBeenCalledWith(
      document.querySelector("main"),
    );
    await press("p");
    expect(actions.onPictureInPicture).toHaveBeenCalledOnce();
    await press(" ");
    expect(actions.onPause).toHaveBeenCalledOnce();
    await press("s");
    expect(actions.onStop).toHaveBeenCalledOnce();
    // Browser shortcuts and focused fields keep their keys.
    await press("f", { metaKey: true });
    await press("m", {}, screen.getByLabelText("Stream"));
    expect(actions.onFullscreen).toHaveBeenCalledOnce();
    expect(actions.onMute).toHaveBeenCalledOnce();
    const room = (camera_id: string) => ({ camera_id, stream_id: "default" });
    await press("]");
    await waitFor(() => expect(onSelect).toHaveBeenLastCalledWith(room("room2")));
    await press("1");
    await waitFor(() => expect(onSelect).toHaveBeenLastCalledWith(room("room1")));
    await press("[");
    await waitFor(() => expect(onSelect).toHaveBeenLastCalledWith(room("room2")));
    await press("?");
    const help = document.querySelector("dialog[aria-labelledby='shortcuts-title']")!;
    expect(help.hasAttribute("open")).toBe(true);
    // An open dialog keeps every key.
    await press("1");
    expect(onSelect).toHaveBeenLastCalledWith(room("room2"));
  });

  it("retries from the keyboard only when the view offers it", async () => {
    const viewer = input("failed");
    viewer.presentation.error = {
      code: "media_connection_failed",
      message: "",
      retryable: true,
    };
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    fireEvent.keyDown(document.body, { key: "r" });
    await act(async () => {});
    expect(viewer.presentation.actions!.onRetry).toHaveBeenCalledOnce();
    const playing = input();
    rerender(<App catalog={catalog} viewer={playing} />);
    fireEvent.keyDown(document.body, { key: "r" });
    expect(playing.presentation.actions!.onRetry).not.toHaveBeenCalled();
  });

  it("toggles fullscreen when the video is double-clicked", () => {
    const viewer = input();
    render(<App catalog={catalog} viewer={viewer} />);
    fireEvent.dblClick(screen.getByRole("button", { name: "Stop" }));
    expect(viewer.presentation.actions!.onFullscreen).not.toHaveBeenCalled();
    fireEvent.dblClick(document.querySelector("video")!);
    expect(viewer.presentation.actions!.onFullscreen).toHaveBeenCalledWith(
      document.querySelector("main"),
    );
  });

  it("collapses the rail to icons and keeps every name available", async () => {
    const user = userEvent.setup();
    render(<App catalog={catalog} />);
    const toggle = screen.getByRole("button", { name: "Camera list" });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    await user.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(document.querySelector(".app")!.getAttribute("data-rail")).toBe(
      "collapsed",
    );
    expect(
      screen.getByRole("link", { name: /Room 2/ }).getAttribute("title"),
    ).toBe("Room 2");
  });

  it("opens the camera sheet from the player and closes it on a choice", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<App catalog={catalog} viewer={input()} onSelect={onSelect} />);
    const sheet = document.querySelector<HTMLElement>(".camera-sheet")!;
    expect(sheet.querySelectorAll("a")).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Cameras" }));
    expect(sheet.hasAttribute("open")).toBe(true);
    await user.click(within(sheet).getByRole("link", { name: /Room 2/ }));
    await waitFor(() =>
      expect(onSelect).toHaveBeenLastCalledWith({
        camera_id: "room2",
        stream_id: "default",
      }),
    );
    expect(sheet.hasAttribute("open")).toBe(false);
    expect(sheet.querySelectorAll("a")).toHaveLength(0);
    // The button that opened the sheet left with the old player.
    expect(document.activeElement).toBe(document.querySelector("main"));
  });

  it("keeps focus in the viewer when a key switches cameras from a control", async () => {
    const onSelect = vi.fn();
    render(<App catalog={catalog} viewer={input()} onSelect={onSelect} />);
    screen.getByRole("button", { name: "Stop" }).focus();
    fireEvent.keyDown(document.activeElement!, { key: "]" });
    await waitFor(() =>
      expect(onSelect).toHaveBeenLastCalledWith({
        camera_id: "room2",
        stream_id: "default",
      }),
    );
    expect(document.activeElement).toBe(document.querySelector("main"));
  });

  it("shows a skeleton of the same layout while cameras load", () => {
    render(<App catalog={catalog} loading />);
    const stage = document.querySelector<HTMLElement>(".stage-skeleton")!;
    expect(stage.getAttribute("aria-busy")).toBe("true");
    expect(
      within(stage).getByRole("heading", { name: "Loading cameras" }),
    ).toBeTruthy();
    expect(document.querySelectorAll(".row-skeleton").length).toBeGreaterThan(0);
    expect(screen.queryByRole("link", { name: /Room/ })).toBeNull();
    expect(document.querySelector("video")).toBeNull();
  });
});

describe("controls while a view connects", () => {
  const connecting = (viewer: ViewerInput): ViewerInput => ({
    ...viewer,
    presentation: { ...viewer.presentation, state: "connecting" },
  });

  it("lets Stop end a Play that is still connecting", async () => {
    const user = userEvent.setup();
    const viewer = input("closed");
    const actions = viewer.presentation.actions!;
    let fail!: (reason: unknown) => void;
    actions.onRetry = vi.fn(
      () =>
        new Promise<void>((_, reject) => {
          fail = reject;
        }),
    );
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    await user.click(screen.getByRole("button", { name: "Play video" }));
    expect(actions.onRetry).toHaveBeenCalledOnce();
    // The controller publishes connecting while the retry is still pending.
    rerender(<App catalog={catalog} viewer={connecting(viewer)} />);
    const stop = screen.getByRole("button", { name: "Stop" });
    expect(stop.getAttribute("aria-disabled")).toBeNull();
    await user.click(stop);
    expect(actions.onStop).toHaveBeenCalledOnce();
    // The superseded Play reports nothing and leaves nothing pending.
    await act(async () => fail(new Error("superseded")));
    expect(document.querySelector(".player-hint")!.textContent).toBe("");
  });

  it("moves focus to the player when its control goes away, and not later", async () => {
    const user = userEvent.setup();
    const viewer = input("closed");
    let finish!: () => void;
    viewer.presentation.actions!.onRetry = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    await user.click(screen.getByRole("button", { name: "Play video" }));
    rerender(<App catalog={catalog} viewer={connecting(viewer)} />);
    expect(document.activeElement).toBe(
      document.querySelector("section.player"),
    );
    // Focus moved elsewhere stays there when the slow Play completes.
    const streamSelect = screen.getByLabelText("Stream");
    streamSelect.focus();
    await act(async () => finish());
    expect(document.activeElement).toBe(streamSelect);
  });

  it("keeps focus in the player when Try again starts connecting", async () => {
    const user = userEvent.setup();
    const viewer = input("failed");
    viewer.presentation.error = {
      code: "media_connection_failed",
      message: "",
      retryable: true,
    };
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    rerender(<App catalog={catalog} viewer={input("connecting")} />);
    expect(document.activeElement).toBe(
      document.querySelector("section.player"),
    );
  });

  it("marks only the video busy, so messages and hints are announced", () => {
    render(<App catalog={catalog} viewer={input("connecting")} />);
    expect(document.querySelector("video")!.getAttribute("aria-busy")).toBe(
      "true",
    );
    expect(document.querySelectorAll("[role='status']").length).toBeGreaterThan(
      1,
    );
    expect(
      document.querySelector("[aria-busy='true'] [role='status']"),
    ).toBeNull();
  });

  it("ignores held keys", async () => {
    const viewer = input();
    const onSelect = vi.fn();
    render(<App catalog={catalog} viewer={viewer} onSelect={onSelect} />);
    await waitFor(() => expect(onSelect).toHaveBeenCalled());
    onSelect.mockClear();
    fireEvent.keyDown(document.body, { key: " ", repeat: true });
    fireEvent.keyDown(document.body, { key: "]", repeat: true });
    await act(async () => {});
    expect(viewer.presentation.actions!.onPause).not.toHaveBeenCalled();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("starts a stopped view with R and says when there is nothing to retry", async () => {
    const viewer = input();
    const { rerender } = render(<App catalog={catalog} viewer={viewer} />);
    fireEvent.keyDown(document.body, { key: "r" });
    await act(async () => {});
    expect(document.querySelector(".player-hint")!.textContent).toBe(
      "Nothing to try again.",
    );
    expect(viewer.presentation.actions!.onRetry).not.toHaveBeenCalled();
    const stopped = input("closed");
    rerender(<App catalog={catalog} viewer={stopped} />);
    fireEvent.keyDown(document.body, { key: "r" });
    await act(async () => {});
    expect(stopped.presentation.actions!.onRetry).toHaveBeenCalledOnce();
  });

  it("keeps the controls under a resting mouse, but not after a tap", () => {
    vi.useFakeTimers();
    try {
      render(<App catalog={catalog} viewer={input()} />);
      const player = document.querySelector<HTMLElement>("section.player")!;
      const toolbar = player.querySelector(".player-toolbar")!;
      const idle = () => player.hasAttribute("data-idle");
      fireEvent.pointerEnter(toolbar, { pointerType: "mouse" });
      act(() => {
        vi.advanceTimersByTime(6000);
      });
      expect(idle()).toBe(false);
      fireEvent.pointerLeave(toolbar, { pointerType: "mouse" });
      act(() => {
        vi.advanceTimersByTime(3000);
      });
      expect(idle()).toBe(true);
      // Touch leaves :hover on the tapped control; it must not pin them.
      fireEvent.pointerUp(player.querySelector("video")!, {
        pointerType: "touch",
      });
      expect(idle()).toBe(false);
      fireEvent.pointerEnter(toolbar, { pointerType: "touch" });
      act(() => {
        vi.advanceTimersByTime(3000);
      });
      expect(idle()).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it("offers the stream choice in the camera sheet", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<App catalog={catalog} viewer={input()} onSelect={onSelect} />);
    await user.click(screen.getByRole("button", { name: "Cameras" }));
    const sheet = document.querySelector<HTMLElement>(".camera-sheet")!;
    await user.selectOptions(within(sheet).getByLabelText("Stream"), "compact");
    await waitFor(() =>
      expect(onSelect).toHaveBeenLastCalledWith({
        camera_id: "room1",
        stream_id: "compact",
      }),
    );
    expect(sheet.hasAttribute("open")).toBe(true);
    expect(document.querySelectorAll("#stream-select")).toHaveLength(1);
  });

  it("returns focus to the viewer when a dialog's opener is gone", async () => {
    const user = userEvent.setup();
    render(<App catalog={catalog} viewer={input()} />);
    await user.click(screen.getByRole("button", { name: "Cameras" }));
    const sheet = document.querySelector<HTMLElement>(".camera-sheet")!;
    // A switch replaced the player and its Cameras button, so closing the
    // dialog leaves focus on the page.
    (document.activeElement as HTMLElement).blur();
    fireEvent(sheet, new Event("close"));
    expect(document.activeElement).toBe(document.querySelector("main"));
  });
});

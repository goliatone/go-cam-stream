import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
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
    expect(
      screen.getByRole("button", { name: "Mute" }).getAttribute("aria-pressed"),
    ).toBe("true");
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
    expect(
      screen.getByRole<HTMLButtonElement>("button", {
        name: "Picture in picture",
      }).disabled,
    ).toBe(true);
    expect(
      screen.getByText("Picture in picture is not supported by this browser."),
    ).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Fullscreen" }));
    expect(viewer.presentation.actions!.onFullscreen).toHaveBeenCalledOnce();
    expect(
      viewer.presentation.actions!.onPictureInPicture,
    ).not.toHaveBeenCalled();
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
    render(<App catalog={catalog} viewer={viewer} />);
    expect(document.querySelector(".stage-badge")!.textContent).toBe("Paused");
    expect(screen.getByRole("heading", { name: "Paused" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Play video" }));
    expect(onPlay).toHaveBeenCalledOnce();
  });

  it("fullscreens the whole player and labels the way back", async () => {
    const user = userEvent.setup();
    const viewer = input();
    render(<App catalog={catalog} viewer={viewer} />);
    const player = document.querySelector<HTMLElement>("section.player")!;
    await user.click(screen.getByRole("button", { name: "Fullscreen" }));
    expect(viewer.presentation.actions!.onFullscreen).toHaveBeenCalledWith(
      player,
    );
    Object.defineProperty(document, "fullscreenElement", {
      configurable: true,
      value: player,
    });
    try {
      fireEvent(document, new Event("fullscreenchange"));
      expect(
        screen.getByRole("button", { name: "Exit fullscreen" }),
      ).toBeTruthy();
      expect(player.hasAttribute("data-fullscreen")).toBe(true);
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

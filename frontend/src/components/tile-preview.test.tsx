import { act, cleanup, render } from "@testing-library/preact";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TilePreview, type PreviewSource } from "./tile-preview";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("tile preview", () => {
  it("shows the colour tile for a camera without a frame", () => {
    const { container } = render(<TilePreview source={{ kind: "none" }} tile={3} />);
    const tile = container.querySelector(".tile-preview")!;
    expect(tile.classList.contains("tile-3")).toBe(true);
    expect(tile.querySelector("svg")).toBeTruthy();
    expect(tile.querySelector("img")).toBeNull();
    expect(tile.textContent).toBe("");
  });

  it("shows the last frame and how long ago it was seen, as time passes", () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const now = vi.spyOn(performance, "now").mockReturnValue(10_000);
    const { container } = render(
      <TilePreview source={{ kind: "frame", url: "data:image/jpeg;base64,frame", at: 10_000 }} tile={0} />,
    );
    const image = container.querySelector("img")!;
    expect(image.getAttribute("src")).toBe("data:image/jpeg;base64,frame");
    // The frame says nothing the tile's name and status do not.
    expect(image.getAttribute("alt")).toBe("");
    expect(container.querySelector("svg")).toBeNull();
    expect(container.textContent).toBe("Seen just now");
    now.mockReturnValue(15_000);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(container.textContent).toBe("Seen 5 s ago");
    now.mockReturnValue(10_000 + 125_000);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(container.textContent).toBe("Seen 2 min ago");
  });

  it("shows the colour tile for a kind it does not know", () => {
    // A newer producer may describe live video or a server snapshot.
    const live = { kind: "live", url: "https://camera.invalid/live" } as unknown as PreviewSource;
    const { container } = render(<TilePreview source={live} tile={5} />);
    const tile = container.querySelector(".tile-preview")!;
    expect(tile.classList.contains("tile-5")).toBe(true);
    expect(tile.querySelector("svg")).toBeTruthy();
    expect(tile.querySelector("img")).toBeNull();
  });
});

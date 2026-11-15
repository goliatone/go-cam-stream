import { createRef } from "preact";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { catalog, simConfig } from "./catalog";
import { describePhase } from "./copy";
import { Player, type PlayerCommands } from "./player";
import { failure, Sim, type Phase } from "./sim";

vi.mock("./synthetic-video", () => ({
  startFeed: vi.fn(() => ({ stream: {}, dispose: vi.fn(), setFrozen: vi.fn() })),
}));
const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, "clipboard");
beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => undefined);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  if (clipboardDescriptor) Object.defineProperty(navigator, "clipboard", clipboardDescriptor);
  else Reflect.deleteProperty(navigator, "clipboard");
});

function player(sim: Sim, index = 0, phase: Phase = { kind: "live", since: 0, transport: "tcp" }) {
  const camera = catalog.cameras[index]!;
  return <Player sim={sim} camera={camera} stream={camera.streams[0]!}
    phase={phase} state={describePhase(phase, false, 0)} now={0}
    operator={{ kind: "idle" }} operatorMode={false} alternatives={[]}
    immersive={false} commands={createRef<PlayerCommands>()} onMedia={() => undefined}
    onSelect={() => undefined} onChooseCamera={() => undefined} onOpenCameras={() => undefined} />;
}

function pendingPlay() {
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((_resolve, fail) => { reject = fail; });
  const play = vi.spyOn(HTMLMediaElement.prototype, "play")
    .mockImplementationOnce(() => promise).mockResolvedValue(undefined);
  return { play, reject };
}

describe("prototype player lifecycle", () => {
  it("ignores an old play rejection after switching cameras", async () => {
    const sim = new Sim(simConfig);
    const blocked = vi.spyOn(sim, "blocked");
    const { play, reject } = pendingPlay();
    const view = render(player(sim));
    await waitFor(() => expect(play).toHaveBeenCalledTimes(1));
    view.rerender(player(sim, 1));
    await waitFor(() => expect(play).toHaveBeenCalledTimes(2));
    await act(async () => reject(new Error("old play aborted")));
    expect(blocked).not.toHaveBeenCalled();
  });

  it("ignores a play rejection after unmount", async () => {
    const sim = new Sim(simConfig);
    const blocked = vi.spyOn(sim, "blocked");
    const { play, reject } = pendingPlay();
    const view = render(player(sim));
    await waitFor(() => expect(play).toHaveBeenCalledOnce());
    view.unmount();
    await act(async () => reject(new Error("detached")));
    expect(blocked).not.toHaveBeenCalled();
  });

  it("reports a rejection for the current player", async () => {
    const sim = new Sim(simConfig);
    const blocked = vi.spyOn(sim, "blocked");
    vi.spyOn(HTMLMediaElement.prototype, "play").mockRejectedValue(new Error("not allowed"));
    render(player(sim));
    await waitFor(() => expect(blocked).toHaveBeenCalledOnce());
  });
});

describe("prototype error details", () => {
  it.each(["denied", "unavailable"])("explains %s clipboard access without an unhandled rejection", async (mode) => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: mode === "denied" ? { writeText: vi.fn().mockRejectedValue(new Error("denied")) } : undefined,
    });
    render(player(new Sim(simConfig), 0, {
      kind: "failed", error: failure("source_unavailable"), exhausted: false, at: 0,
    }));
    fireEvent.click(screen.getByRole("button", { name: "Details" }));
    fireEvent.click(screen.getByRole("button", { name: "Copy details" }));
    expect(await screen.findByText("Could not copy. Select the details above to copy them manually.")).toBeTruthy();
  });
});

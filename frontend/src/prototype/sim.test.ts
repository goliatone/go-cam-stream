import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { catalog, simConfig } from "./catalog";
import {
  backoff,
  failure,
  linkFor,
  MAX_RETRIES,
  parseLink,
  resolveRef,
  retryMode,
  Sim,
  TIMING,
} from "./sim";
import type { StreamRef } from "../services/contracts";

const half = () => 0.5;
const connectOk = TIMING.connect + 400 * 0.5;
let sim: Sim | undefined;

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  sim?.dispose();
  sim = undefined;
  vi.useRealTimers();
});

function start(link: Partial<StreamRef> = {}) {
  sim?.dispose();
  const created = new Sim(simConfig, link, { random: half });
  sim = created;
  created.start();
  vi.advanceTimersByTime(TIMING.catalog);
  return created;
}
function live(link: Partial<StreamRef> = {}) {
  const created = start(link);
  vi.advanceTimersByTime(connectOk);
  expect(created.get().phase.kind).toBe("live");
  return created;
}

describe("retry policy", () => {
  it("backs off exponentially with ±20% jitter, capped at 60 s", () => {
    expect(backoff(1, () => 0)).toBe(1600);
    expect(backoff(1, () => 1)).toBe(2400);
    expect([1, 2, 3, 4, 5, 6].map((n) => backoff(n, half))).toEqual([
      2000, 4000, 8000, 16000, 30000, 60000,
    ]);
    expect(backoff(12, half)).toBe(60000);
    expect(backoff(6, () => 1)).toBe(60000);
    expect(backoff(12, () => 1)).toBe(60000);
  });

  it("retries automatically only after known failures", () => {
    expect(retryMode(failure("media_connection_failed"))).toBe("auto");
    expect(retryMode(failure("deadline_exceeded"))).toBe("manual");
    expect(retryMode(failure("source_unavailable"))).toBe("none");
  });
});

describe("simulated viewer", () => {
  it("opens the deep-linked stream and falls back silently", () => {
    expect(
      start({ camera_id: "livingroom", stream_id: "compact" }).get().selection,
    ).toEqual({ camera_id: "livingroom", stream_id: "compact" });
    expect(start({ camera_id: "missing", stream_id: "x" }).get().selection).toEqual(
      { camera_id: "room1", stream_id: "default" },
    );
  });

  it("never retries a timed-out create automatically", () => {
    const viewer = live();
    viewer.force("timeout");
    vi.advanceTimersByTime(TIMING.deadline);
    expect(viewer.get().phase).toMatchObject({
      kind: "failed",
      exhausted: false,
      error: { code: "deadline_exceeded" },
    });
    vi.advanceTimersByTime(120_000);
    expect(viewer.get().phase.kind).toBe("failed");
  });

  it("retries known failures with backoff and stops at the limit", () => {
    const viewer = live();
    viewer.setOption("keepFailing", true);
    viewer.loseMedia();
    for (let attempt = 1; attempt <= MAX_RETRIES; attempt++) {
      expect(viewer.get().phase).toMatchObject({ kind: "retrying", attempt });
      vi.advanceTimersByTime(backoff(attempt, half));
      expect(viewer.get().phase).toMatchObject({ kind: "connecting", attempt });
      vi.advanceTimersByTime(TIMING.connect);
    }
    expect(viewer.get().phase).toMatchObject({ kind: "failed", exhausted: true });
  });

  it("recovers on the next attempt once the failure clears", () => {
    const viewer = live();
    viewer.loseMedia();
    vi.advanceTimersByTime(backoff(1, half) + connectOk);
    expect(viewer.get().phase.kind).toBe("live");
  });

  it("keeps a latched source fault until an operator reconnects it", () => {
    const viewer = start({ camera_id: "hall" });
    vi.advanceTimersByTime(TIMING.fault);
    expect(viewer.get().phase).toMatchObject({
      kind: "failed",
      error: { code: "source_unavailable", retryable: false },
    });
    viewer.retry();
    vi.advanceTimersByTime(TIMING.fault);
    expect(viewer.get().phase).toMatchObject({
      kind: "failed",
      error: { code: "source_unavailable" },
    });
    viewer.reconnectSource();
    vi.advanceTimersByTime(TIMING.reconnectSource + connectOk);
    expect(viewer.get().phase.kind).toBe("live");
  });

  it("cancels old diagnostics when the catalog is reloaded", () => {
    const viewer = live();
    viewer.diagnose();
    viewer.setOption("catalog", "denied");
    vi.advanceTimersByTime(TIMING.diagnose + TIMING.catalog);
    expect(viewer.get().catalog.kind).toBe("denied");
    expect(viewer.get().operator.kind).toBe("idle");
  });

  it("does not clear a source fault after its catalog is reset", () => {
    const viewer = start({ camera_id: "hall" });
    vi.advanceTimersByTime(TIMING.fault);
    viewer.reconnectSource();
    viewer.setOption("catalog", "empty");
    vi.advanceTimersByTime(TIMING.reconnectSource + connectOk);
    expect(viewer.get().cameras.hall?.fault?.code).toBe("source_unavailable");
    expect(viewer.get().phase.kind).toBe("none");
  });

  it("preserves reload-needed state when ownership is lost during reconnect", () => {
    const viewer = start({ camera_id: "hall" });
    vi.advanceTimersByTime(TIMING.fault);
    viewer.reconnectSource();
    viewer.expire();
    vi.advanceTimersByTime(TIMING.reconnectSource + connectOk);
    expect(viewer.get().phase.kind).toBe("reload");
    expect(viewer.get().operator.kind).toBe("idle");
    expect(viewer.get().cameras.hall?.fault?.code).toBe("source_unavailable");
  });

  it("holds the retry countdown while hidden, but not in picture in picture", () => {
    const viewer = live();
    viewer.loseMedia();
    viewer.setPresence(true, false);
    vi.advanceTimersByTime(60_000);
    expect(viewer.get().phase).toMatchObject({ kind: "retrying", held: 2000 });
    viewer.setPresence(false, false);
    vi.advanceTimersByTime(2000);
    expect(viewer.get().phase).toMatchObject({ kind: "connecting", attempt: 1 });

    const pip = live();
    pip.loseMedia();
    pip.setPresence(true, true);
    vi.advanceTimersByTime(2000);
    expect(pip.get().phase.kind).toBe("connecting");
  });

  it("restores brief drops and releases sustained ones", () => {
    const viewer = live();
    viewer.dropConnection(true);
    expect(viewer.get().phase.kind).toBe("reconnecting");
    vi.advanceTimersByTime(TIMING.recover);
    expect(viewer.get().phase.kind).toBe("live");
    viewer.dropConnection(false);
    vi.advanceTimersByTime(TIMING.window);
    expect(viewer.get().phase).toMatchObject({
      kind: "retrying",
      error: { code: "media_connection_failed" },
    });
  });

  it("stop frees the slot and play reconnects", () => {
    const viewer = live();
    expect(viewer.get().cameras.room1?.viewers).toBe(1);
    viewer.stop();
    expect(viewer.get().phase.kind).toBe("stopped");
    viewer.play();
    vi.advanceTimersByTime(connectOk);
    expect(viewer.get().phase.kind).toBe("live");
  });

  it("keeps retrying the catalog while the service is down", () => {
    const viewer = start();
    viewer.setOption("catalog", "down");
    vi.advanceTimersByTime(TIMING.catalog);
    expect(viewer.get().catalog).toMatchObject({ kind: "down", attempt: 1 });
    vi.advanceTimersByTime(backoff(1, half));
    expect(viewer.get().catalog).toMatchObject({ kind: "down", attempt: 2 });
    viewer.setOption("catalog", "normal");
    vi.advanceTimersByTime(TIMING.catalog);
    expect(viewer.get().catalog.kind).toBe("ready");
  });
});

describe("deep links", () => {
  it("omits the first stream and round-trips", () => {
    const living = catalog.cameras.find((c) => c.id === "livingroom")!;
    expect(linkFor(living)).toBe("?camera=livingroom");
    expect(linkFor(living, "main")).toBe("?camera=livingroom");
    expect(linkFor(living, "compact")).toBe(
      "?camera=livingroom&stream=compact",
    );
    expect(parseLink("?camera=hall&stream=default")).toEqual({
      camera_id: "hall",
      stream_id: "default",
    });
    expect(parseLink("")).toEqual({});
    expect(
      resolveRef(catalog.cameras, { camera_id: "hall", stream_id: "zzz" }),
    ).toEqual({ camera_id: "hall", stream_id: "default" });
  });
});

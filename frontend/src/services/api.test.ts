import { afterEach, describe, expect, it, vi } from "vitest";
import { APIClient } from "./api";
import samples from "../../../data/api/examples.json";
const ref = { camera_id: "room1", stream_id: "default" };
const csrf = {
  token: "package-token",
  field_name: "_token",
  header_name: "X-CSRF-Token",
};
const response = (body: unknown, status = 200) =>
  new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
afterEach(() => vi.useRealTimers());
describe("same-origin API and package CSRF", () => {
  it("uses one keepalive DELETE with the in-memory token during page exit", async () => {
    const request = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(csrf))
      .mockResolvedValueOnce(response(samples.session, 201))
      .mockResolvedValueOnce(response(null, 204));
    const api = new APIClient(request);
    await api.create(ref, "v=0\r\n");
    await api.close("session-example", true);
    expect(request).toHaveBeenCalledTimes(3);
    expect(request.mock.calls[2]).toMatchObject([
      "/api/playback/sessions/session-example",
      {
        method: "DELETE",
        keepalive: true,
        headers: { "X-CSRF-Token": "package-token" },
      },
    ]);
  });
  it("bootstraps a fresh token for each mutation and sends no identity", async () => {
    const request = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(csrf))
      .mockResolvedValueOnce(response(samples.session, 201))
      .mockResolvedValueOnce(response({ ...csrf, token: "fresh-token" }))
      .mockResolvedValueOnce(response(null, 204));
    const api = new APIClient(request);
    const session = await api.create(ref, "v=0\r\n");
    await api.close(session.id);
    expect(request.mock.calls.map((c) => c[0])).toEqual([
      "/api/csrf",
      "/api/cameras/room1/streams/default/sessions",
      "/api/csrf",
      "/api/playback/sessions/session-example",
    ]);
    expect(request.mock.calls[1]![1]).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
      headers: {
        "X-CSRF-Token": "package-token",
        "Content-Type": "application/json",
      },
    });
    expect(request.mock.calls[3]![1]).toMatchObject({
      method: "DELETE",
      headers: { "X-CSRF-Token": "fresh-token" },
    });
    expect(JSON.stringify(request.mock.calls)).not.toContain("Tailscale-User");
  });
  it("does not replay a write after CSRF expiry; a manual retry bootstraps anew", async () => {
    const request = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(csrf))
      .mockResolvedValueOnce(
        response(
          {
            error: {
              code: "csrf_invalid",
              message: "private",
              retryable: true,
            },
          },
          403,
        ),
      );
    const api = new APIClient(request);
    await expect(api.create(ref, "v=0\r\n")).rejects.toMatchObject({
      body: { code: "csrf_invalid", message: "" },
    });
    expect(request).toHaveBeenCalledTimes(2);
    request
      .mockResolvedValueOnce(response(csrf))
      .mockResolvedValueOnce(response(samples.session, 201));
    await api.create(ref, "v=0\r\n");
    expect(request).toHaveBeenCalledTimes(4);
  });
  it("rejects a forged token header and never sends the mutation", async () => {
    const request = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        response({ ...csrf, header_name: "Tailscale-User-Login" }),
      );
    await expect(
      new APIClient(request).create(ref, "v=0\r\n"),
    ).rejects.toMatchObject({ body: { code: "csrf_invalid" } });
    expect(request).toHaveBeenCalledOnce();
  });
  it("requires HTTP 204 for confirmed cleanup", async () => {
    const request = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(csrf))
      .mockResolvedValueOnce(response(null));
    await expect(
      new APIClient(request).close("session-example"),
    ).rejects.toMatchObject({ body: { code: "cleanup_incomplete" } });
  });
  it("rejects mismatched session bindings and untrusted shapes", async () => {
    const request = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(csrf))
      .mockResolvedValueOnce(
        response({ ...samples.session, camera_id: "other" }, 201),
      );
    await expect(
      new APIClient(request).create(ref, "v=0\r\n"),
    ).rejects.toMatchObject({ body: { code: "server_unavailable" } });
    request.mockResolvedValueOnce(
      response({ cameras: [{ ...samples.catalog.cameras[0], streams: null }] }),
    );
    await expect(new APIClient(request).list()).rejects.toThrow();
  });
  it("projects the catalog and status without unexpected private fields", async () => {
    const request = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(samples.catalog))
      .mockResolvedValueOnce(
        response({ ...samples.unknown_status, source_url: "secret" }),
      );
    const api = new APIClient(request);
    expect(await api.list()).toEqual(samples.catalog);
    expect(await api.status(ref)).toEqual(samples.unknown_status);
  });
  it("sanitizes network errors and bounds reads without repeating them", async () => {
    vi.useFakeTimers();
    const request = vi.fn<typeof fetch>(
      (_, init) =>
        new Promise((_, reject) =>
          init!.signal!.addEventListener("abort", () =>
            reject(new Error("private host")),
          ),
        ),
    );
    const promise = new APIClient(request, 50).list();
    const assertion = expect(promise).rejects.toMatchObject({
      body: { code: "deadline_exceeded", message: "" },
    });
    await vi.advanceTimersByTimeAsync(60);
    await assertion;
    expect(request).toHaveBeenCalledOnce();
  });
  it("does not retry an uncertain POST", async () => {
    const request = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response(csrf))
      .mockRejectedValueOnce(new Error("private upstream"));
    await expect(
      new APIClient(request).create(ref, "v=0\r\n"),
    ).rejects.toMatchObject({
      body: { code: "server_unavailable", message: "" },
    });
    expect(request).toHaveBeenCalledTimes(2);
  });
});

import { describe, expect, it, vi } from "vitest";
import examples from "../../../data/api/sharing_examples.json";
import { createGuestAPI, GuestError, validHandle, validInvitation } from "./guest-api";

const SHARE = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
const VIEWER = examples.admitted.viewer_id;
const ATTEMPT = examples.admitted.attempt_id;
const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
function client(...responses: Response[]) {
  const fetcher = vi.fn<typeof fetch>(async () => {
    const next = responses.shift();
    if (!next) throw new Error("unexpected request");
    return next;
  });
  return { api: createGuestAPI(SHARE, fetcher), fetcher };
}
const request = (fetcher: ReturnType<typeof client>["fetcher"], index = 0) => {
  const [url, init] = fetcher.mock.calls[index]!;
  return { url, init: init!, body: JSON.parse(String(init!.body)), headers: init!.headers as Record<string, string> };
};
const signal = () => new AbortController().signal;

describe("guest API client", () => {
  it("exchanges the invitation with a bounded same-origin request and no CSRF header", async () => {
    const { api, fetcher } = client(
      json(200, examples.redeemed, { Date: "Tue, 17 Nov 2026 22:30:00 GMT" }),
    );
    const result = await api.redeem(examples.redeem.invitation, signal());
    const sent = request(fetcher);
    expect(sent.url).toBe(`/share-api/v1/shares/${SHARE}/redeem`);
    expect(sent.body).toEqual(examples.redeem);
    expect(sent.headers).toEqual({ "Content-Type": "application/json" });
    expect(sent.init).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      mode: "same-origin",
      cache: "no-store",
      redirect: "error",
      referrerPolicy: "no-referrer",
      keepalive: false,
    });
    expect(result).toEqual({
      expiresAt: Date.parse(examples.redeemed.expires_at),
      csrf: examples.redeemed.csrf_token,
      serverTime: Date.parse("2026-11-17T22:30:00Z"),
    });
  });

  it("admits with the CSRF token, reconnects with the viewer and maps ICE servers", async () => {
    const { api, fetcher } = client(json(201, examples.admitted), json(201, examples.readmitted));
    const first = await api.admit("csrf", undefined);
    const again = await api.admit("csrf", VIEWER);
    expect(request(fetcher, 0)).toMatchObject({
      url: `/share-api/v1/shares/${SHARE}/viewers`,
      body: {},
      headers: { "Content-Type": "application/json", "X-CSRF-Token": "csrf" },
    });
    expect(request(fetcher, 1).body).toEqual({ viewer_id: VIEWER });
    expect(first).toMatchObject({ viewerID: VIEWER, attemptID: ATTEMPT });
    expect(again.attemptID).toBe(examples.readmitted.attempt_id);
    expect(first.iceServers).toEqual([]);
    expect(again.iceServers).toEqual(
      examples.readmitted.ice_servers.map(({ urls, username, credential }) => ({ urls, username, credential })),
    );
  });

  it("sends the offer for the current attempt and accepts only an SDP answer", async () => {
    const { api, fetcher } = client(json(200, { answer: { type: "answer", sdp: "v=0\r\nanswer" } }));
    const offer = { type: "offer" as const, sdp: "v=0\r\noffer" };
    await expect(api.open("csrf", VIEWER, ATTEMPT, offer, signal())).resolves.toEqual({
      type: "answer",
      sdp: "v=0\r\nanswer",
    });
    expect(request(fetcher)).toMatchObject({
      url: `/share-api/v1/shares/${SHARE}/viewers/${VIEWER}/offer`,
      body: { attempt_id: ATTEMPT, offer },
      headers: { "X-CSRF-Token": "csrf" },
    });
    const bad = client(json(200, { answer: { type: "offer", sdp: "v=0\r\n" } }));
    await expect(bad.api.open("csrf", VIEWER, ATTEMPT, offer, signal())).rejects.toMatchObject({ code: "malformed" });
  });

  it("closes one attempt, optionally outliving the page", async () => {
    const { api, fetcher } = client(new Response(null, { status: 204 }));
    await api.close("csrf", VIEWER, ATTEMPT, true);
    expect(request(fetcher)).toMatchObject({
      url: `/share-api/v1/shares/${SHARE}/viewers/${VIEWER}`,
      body: { attempt_id: ATTEMPT },
      init: { method: "DELETE", keepalive: true },
    });
  });

  it("keeps only allowlisted error codes, never server text", async () => {
    const cases: [Response, string, number?][] = [
      [json(409, examples.at_capacity), "capacity_reached"],
      [json(410, examples.expired), "expired"],
      [json(410, examples.revoked), "revoked"],
      [json(409, examples.stale_attempt), "stale_viewer_attempt"],
      [json(429, { error: { code: "rate_limited", message: "x", retryable: true } }, { "Retry-After": "1" }), "rate_limited", 1000],
      [json(403, { error: { code: "made_up", message: "SECRET DETAIL", retryable: false } }), "permission_denied"],
      [new Response("Bad gateway", { status: 502, headers: { "Content-Type": "text/plain" } }), "unavailable"],
      [json(503, { error: { code: "camera_unavailable", message: "SECRET DETAIL", retryable: true } }), "camera_unavailable"],
    ];
    for (const [response, code, wait = 0] of cases) {
      const { api } = client(response);
      const error = await api.admit("csrf", undefined).catch((e: unknown) => e);
      expect(error).toBeInstanceOf(GuestError);
      expect(error).toMatchObject({ code, retryAfterMS: wait, message: code });
    }
  });

  it("rejects malformed success bodies and reports network loss and cancellation", async () => {
    const malformed = client(json(201, { ...examples.admitted, viewer_id: "short" }));
    await expect(malformed.api.admit("csrf", undefined)).rejects.toMatchObject({ code: "malformed" });
    const insecure = client(
      json(201, { ...examples.readmitted, ice_servers: [{ ...examples.readmitted.ice_servers[0], urls: ["http://relay.example"] }] }),
    );
    await expect(insecure.api.admit("csrf", undefined)).rejects.toMatchObject({ code: "malformed" });
    const offline = createGuestAPI(SHARE, async () => {
      throw new TypeError("Failed to fetch");
    });
    await expect(offline.redeem("a.b.c", signal())).rejects.toMatchObject({ code: "network" });
    const abort = new AbortController();
    abort.abort();
    await expect(offline.redeem("a.b.c", abort.signal)).rejects.toMatchObject({ code: "canceled" });
  });

  it("gives up on a request that never answers, without canceling the caller", async () => {
    vi.useFakeTimers();
    try {
      const hung = vi.fn<typeof fetch>(
        (_input, init) =>
          new Promise((_resolve, reject) =>
            init!.signal!.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))),
          ),
      );
      const api = createGuestAPI(SHARE, hung, 25_000);
      const admitted = api.admit("csrf", undefined).catch((error: unknown) => error);
      const redeemed = api.redeem("a.b.c", signal()).catch((error: unknown) => error);
      await vi.advanceTimersByTimeAsync(24_999);
      expect(hung).toHaveBeenCalledTimes(2);
      await vi.advanceTimersByTimeAsync(1);
      await expect(admitted).resolves.toMatchObject({ code: "network" });
      await expect(redeemed).resolves.toMatchObject({ code: "network" });
    } finally {
      vi.useRealTimers();
    }
  });

  it("validates handles and invitations like the server", () => {
    expect(validHandle(SHARE)).toBe(true);
    expect(validHandle(`${SHARE.slice(0, 42)}B`)).toBe(false);
    expect(validHandle("short")).toBe(false);
    expect(validInvitation(examples.redeem.invitation)).toBe(true);
    expect(validInvitation("a.b")).toBe(false);
    expect(validInvitation(`a.b.${"c".repeat(4096)}`)).toBe(false);
  });
});

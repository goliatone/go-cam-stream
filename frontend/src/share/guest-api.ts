import {
  SHARE_API_PREFIX,
  SHARE_CSRF_HEADER,
  type SDPAnswer,
  type SDPOffer,
  type ShareErrorCode,
} from "../services/contracts";

/**
 * What the guest page tells apart. Server messages are never kept: a failure
 * is only its allowlisted code, so no server text reaches the page.
 */
export type GuestFailure =
  | ShareErrorCode
  | "network"
  | "unavailable"
  | "malformed"
  | "canceled";
export class GuestError extends Error {
  constructor(
    readonly code: GuestFailure,
    /** Server-requested wait before the same request, in milliseconds. */
    readonly retryAfterMS = 0,
  ) {
    super(code);
  }
}

export interface Redeemed {
  /** Absolute end of the share, in milliseconds since the epoch. */
  expiresAt: number;
  csrf: string;
  /** The server's clock when it answered, from the Date header. */
  serverTime?: number;
}
export interface Admission {
  viewerID: string;
  attemptID: string;
  expiresAt: number;
  iceServers: RTCIceServer[];
  serverTime?: number;
}
/** The four guest routes of one share. Handles stay in tab memory only. */
export interface GuestAPI {
  redeem(invitation: string, signal: AbortSignal): Promise<Redeemed>;
  /**
   * Not cancelable: a place the server reserved must reach the page, which
   * closes an admission it no longer wants instead of leaving it held.
   */
  admit(csrf: string, viewerID: string | undefined): Promise<Admission>;
  open(
    csrf: string,
    viewerID: string,
    attemptID: string,
    offer: SDPOffer,
    signal: AbortSignal,
  ): Promise<SDPAnswer>;
  /** Ends one attempt. `keepalive` lets the request outlive a closing page. */
  close(
    csrf: string,
    viewerID: string,
    attemptID: string,
    keepalive?: boolean,
  ): Promise<void>;
}

const shareErrorCodes: ReadonlySet<string> = new Set<ShareErrorCode>([
  "invalid_request",
  "invalid_invitation",
  "expired",
  "revoked",
  "capacity_reached",
  "share_already_active",
  "stale_viewer_attempt",
  "camera_unavailable",
  "ingress_unavailable",
  "relay_unavailable",
  "media_unreachable",
  "unsupported_offer",
  "permission_denied",
  "rate_limited",
]);
/** Longer than the guest listener's 24-second write timeout. */
export const REQUEST_TIMEOUT_MS = 25_000;
// Status codes without an allowlisted body, as from a proxy in front of the page.
const statusCodes: Record<number, ShareErrorCode> = {
  400: "invalid_request",
  403: "permission_denied",
  410: "expired",
  422: "unsupported_offer",
  429: "rate_limited",
};

/** A canonical 32-byte base64url handle: share, viewer and attempt IDs. */
export const validHandle = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(value);
/** The compact signed invitation carried in the link's fragment. */
export const validInvitation = (value: string) =>
  value.length <= 4096 &&
  /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(value);

const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object" && !Array.isArray(value);
const text = (value: unknown, max: number): value is string =>
  typeof value === "string" && value.length > 0 && value.length <= max;
function instant(value: unknown): number {
  const time = typeof value === "string" ? Date.parse(value) : NaN;
  if (!Number.isFinite(time)) throw new GuestError("malformed");
  return time;
}
function iceServers(value: unknown): RTCIceServer[] {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > 8)
    throw new GuestError("malformed");
  return value.map((server: unknown) => {
    if (
      !object(server) ||
      !Array.isArray(server.urls) ||
      server.urls.length === 0 ||
      server.urls.length > 8 ||
      !server.urls.every(
        (url) => text(url, 512) && /^(?:stun|turns?):[^\s]+$/.test(url),
      ) ||
      !text(server.username, 512) ||
      !text(server.credential, 512)
    )
      throw new GuestError("malformed");
    instant(server.expires_at);
    return {
      urls: [...(server.urls as string[])],
      username: server.username,
      credential: server.credential,
    };
  });
}

export function createGuestAPI(
  shareID: string,
  fetcher: typeof fetch = (input, init) => fetch(input, init),
  timeoutMS = REQUEST_TIMEOUT_MS,
): GuestAPI {
  const base = `${SHARE_API_PREFIX}/${shareID}`;
  // A request that never answers fails as a network error, so the page
  // offers Retry instead of waiting forever.
  async function call(
    path: string,
    method: "POST" | "DELETE",
    body: object,
    csrf: string | undefined,
    signal: AbortSignal | undefined,
    keepalive = false,
  ): Promise<{ value: unknown; serverTime?: number }> {
    const limit = new AbortController();
    const timer = setTimeout(() => limit.abort(), timeoutMS);
    const cancel = () => limit.abort();
    if (signal?.aborted) cancel();
    signal?.addEventListener("abort", cancel, { once: true });
    try {
      return await send(path, method, body, csrf, signal, keepalive, limit.signal);
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener("abort", cancel);
    }
  }
  async function send(
    path: string,
    method: "POST" | "DELETE",
    body: object,
    csrf: string | undefined,
    signal: AbortSignal | undefined,
    keepalive: boolean,
    limit: AbortSignal,
  ): Promise<{ value: unknown; serverTime?: number }> {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (csrf) headers[SHARE_CSRF_HEADER] = csrf;
    let response: Response;
    try {
      response = await fetcher(base + path, {
        method,
        headers,
        body: JSON.stringify(body),
        credentials: "same-origin",
        mode: "same-origin",
        cache: "no-store",
        redirect: "error",
        referrerPolicy: "no-referrer",
        keepalive,
        signal: limit,
      });
    } catch {
      throw new GuestError(signal?.aborted ? "canceled" : "network");
    }
    const date = Date.parse(response.headers.get("Date") ?? "");
    const serverTime = Number.isFinite(date) ? { serverTime: date } : {};
    const json = (response.headers.get("Content-Type") ?? "").startsWith(
      "application/json",
    );
    let value: unknown;
    try {
      value = response.status === 204 || !json ? undefined : await response.json();
    } catch {
      if (signal?.aborted) throw new GuestError("canceled");
      value = undefined;
    }
    if (response.ok) return { value, ...serverTime };
    const code =
      object(value) &&
      object(value.error) &&
      typeof value.error.code === "string" &&
      shareErrorCodes.has(value.error.code)
        ? (value.error.code as ShareErrorCode)
        : (statusCodes[response.status] ?? "unavailable");
    const wait = Number(response.headers.get("Retry-After"));
    throw new GuestError(
      code,
      Number.isFinite(wait) && wait > 0 ? Math.min(wait, 30) * 1000 : 0,
    );
  }
  return {
    async redeem(invitation, signal) {
      const { value, ...time } = await call(
        "/redeem",
        "POST",
        { invitation },
        undefined,
        signal,
      );
      if (!object(value) || !text(value.csrf_token, 512))
        throw new GuestError("malformed");
      return { expiresAt: instant(value.expires_at), csrf: value.csrf_token, ...time };
    },
    async admit(csrf, viewerID) {
      const { value, ...time } = await call(
        "/viewers",
        "POST",
        viewerID ? { viewer_id: viewerID } : {},
        csrf,
        undefined,
      );
      if (
        !object(value) ||
        !validHandle(value.viewer_id) ||
        !validHandle(value.attempt_id)
      )
        throw new GuestError("malformed");
      return {
        viewerID: value.viewer_id,
        attemptID: value.attempt_id,
        expiresAt: instant(value.expires_at),
        iceServers: iceServers(value.ice_servers),
        ...time,
      };
    },
    async open(csrf, viewerID, attemptID, offer, signal) {
      const { value } = await call(
        `/viewers/${viewerID}/offer`,
        "POST",
        { attempt_id: attemptID, offer },
        csrf,
        signal,
      );
      const answer = object(value) ? value.answer : undefined;
      if (
        !object(answer) ||
        answer.type !== "answer" ||
        !text(answer.sdp, 65536) ||
        !answer.sdp.startsWith("v=0\r\n")
      )
        throw new GuestError("malformed");
      return { type: "answer", sdp: answer.sdp };
    },
    async close(csrf, viewerID, attemptID, keepalive = false) {
      await call(
        `/viewers/${viewerID}`,
        "DELETE",
        { attempt_id: attemptID },
        csrf,
        undefined,
        keepalive,
      );
    },
  };
}

import { CONVERSATION_API_PREFIX, CONVERSATION_VERSION, type RoomAdmission, type RoomDownload } from "../services/contracts";

const roomID = /^[A-Za-z0-9_-]{43}$/;
const compactToken = /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/;

export interface Invitation { room: string; token: string; appURL: string | null }
export type HostHandoff = { room: string; token: string };

export function readHostHandoff(location: URL): HostHandoff | null {
  const room = location.pathname.slice(3);
  const token = location.hash.slice(6);
  return location.pathname.startsWith("/r/") && location.hash.startsWith("#host=") && roomID.test(room) && roomID.test(token)
    ? { room, token } : null;
}

export async function redeemHost({ room, token }: HostHandoff, signal: AbortSignal): Promise<RoomAdmission> {
  const response = await fetch(CONVERSATION_API_PREFIX + "/" + room + "/host", {
    headers: { Authorization: "Bearer " + token },
    credentials: "omit", mode: "same-origin", cache: "no-store", redirect: "error",
    referrerPolicy: "no-referrer", signal: AbortSignal.any([signal, AbortSignal.timeout(6000)]),
  });
  const body = await response.json();
  if (!response.ok) throw new Error(body?.error?.code ?? "media_unreachable");
  if (body?.room_id !== room || body.version !== CONVERSATION_VERSION) throw new Error("incompatible_client");
  return { role: "host", token, room: body };
}

// A permission prompt can finish after the call has closed. Wait for every
// input operation to dispose of any late tracks before acknowledging stop.
export class BrowserCaptureStop {
  private readonly pending = new Set<Promise<unknown>>();
  private stopping = false;

  constructor(private readonly acknowledge: () => Promise<unknown>) {}

  track<T>(operation: Promise<T>): Promise<T> {
    this.pending.add(operation);
    const remove = () => { this.pending.delete(operation); };
    void operation.then(remove, remove);
    return operation;
  }

  stopped() {
    if (this.stopping) return;
    this.stopping = true;
    void Promise.allSettled(this.pending).then(this.acknowledge).catch(() => {});
  }
}

export function acknowledgeBrowserStop({ room, token }: HostHandoff): Promise<Response> {
  return fetch(`${CONVERSATION_API_PREFIX}/${room}/browser-stopped`, {
    method: "POST", headers: { Authorization: "Bearer " + token }, keepalive: true,
    credentials: "omit", mode: "same-origin", cache: "no-store", redirect: "error",
    referrerPolicy: "origin", signal: AbortSignal.timeout(5000),
  });
}

export function readInvitation(location: URL): Invitation | null {
  const room = location.pathname.slice(3);
  const token = location.hash.slice(1);
  if (!location.pathname.startsWith("/r/") || !roomID.test(room) || token.length > 4096 || !compactToken.test(token)) return null;
  const query = new URLSearchParams({ origin: location.origin, room, v: String(CONVERSATION_VERSION) });
  return { room, token, appURL: location.protocol === "https:" ? `camstream://conversation/join?${query}#${token}` : null };
}

export async function redeem({ room, token }: Invitation, signal: AbortSignal): Promise<RoomAdmission> {
  const response = await fetch(`${CONVERSATION_API_PREFIX}/${room}/redeem`, {
    method: "POST", body: JSON.stringify({ version: CONVERSATION_VERSION, invitation: token }),
    headers: { "Content-Type": "application/json" },
    credentials: "omit", mode: "same-origin", cache: "no-store", redirect: "error",
    referrerPolicy: "origin", signal: AbortSignal.any([signal, AbortSignal.timeout(6000)]),
  });
  const body = await response.json();
  if (!response.ok) throw new Error(body?.error?.code ?? "media_unreachable");
  if (body?.room?.room_id !== room || body.room.version !== CONVERSATION_VERSION ||
      body.role !== "guest" || typeof body.token !== "string" || !roomID.test(body.token)) throw new Error("incompatible_client");
  return body;
}

export async function download(signal: AbortSignal): Promise<RoomDownload | null> {
  try {
    const response = await fetch("/conversation-api/v1/download", {
      mode: "same-origin", credentials: "omit", cache: "no-store", redirect: "error",
      referrerPolicy: "no-referrer", signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]),
    });
    if (!response.ok) return null;
    const value = await response.json();
    const url = new URL(value.url);
    if (url.protocol !== "https:" || url.username || url.password ||
        !/^\d+\.\d+\.\d+$/.test(value.version) || !/^[a-f0-9]{64}$/.test(value.sha256)) return null;
    return { url: url.href, version: value.version, sha256: value.sha256 };
  } catch { return null; }
}

import type {
  CameraList,
  ErrorBody,
  ErrorCode,
  Session,
  State,
  StreamRef,
  StreamStatus,
} from "./contracts";
import { CSRF_HEADER, CSRF_PATH } from "./contracts";
import { errorCopy, stateLabels } from "./copy";

/** The page's own description of an error; it never carries a server message. */
export const errorBody = (code: ErrorCode, retryable = true): ErrorBody => ({
  code,
  message: "",
  retryable,
});
export class PlaybackError extends Error {
  readonly body: ErrorBody;
  constructor(code: ErrorCode, retryable = true) {
    super(code);
    this.body = errorBody(code, retryable);
  }
}
export function safeError(
  error: unknown,
  fallback: ErrorCode = "internal",
): ErrorBody {
  return error instanceof PlaybackError ? error.body : errorBody(fallback);
}
const object = (v: unknown): v is Record<string, unknown> =>
  v !== null && typeof v === "object" && !Array.isArray(v);
const id = (v: unknown): v is string =>
  typeof v === "string" && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(v);
/** `v` names one of the table's own entries, such as a state with copy. */
const listed = <K extends string>(
  table: Record<K, unknown>,
  v: unknown,
): v is K =>
  typeof v === "string" &&
  // Not Object.hasOwn, which Safari 15.0–15.3 lack.
  Object.prototype.hasOwnProperty.call(table, v);
const state = (v: unknown): v is State => listed(stateLabels, v);
function fail(): never {
  throw new PlaybackError("server_unavailable");
}
function publicError(value: unknown): ErrorBody {
  if (
    !object(value) ||
    !listed(errorCopy, value.code) ||
    typeof value.retryable !== "boolean"
  )
    return fail();
  return errorBody(value.code, value.retryable);
}
function catalog(value: unknown): CameraList {
  if (!object(value) || !Array.isArray(value.cameras)) return fail();
  const cameraIDs = new Set<string>();
  return {
    cameras: value.cameras.map((camera: unknown) => {
      if (
        !object(camera) ||
        !id(camera.id) ||
        cameraIDs.has(camera.id) ||
        typeof camera.name !== "string" ||
        !state(camera.availability) ||
        !Array.isArray(camera.streams)
      )
        return fail();
      cameraIDs.add(camera.id);
      const streamIDs = new Set<string>();
      return {
        id: camera.id,
        name: camera.name,
        availability: camera.availability,
        streams: camera.streams.map((stream: unknown) => {
          if (
            !object(stream) ||
            !id(stream.id) ||
            streamIDs.has(stream.id) ||
            typeof stream.name !== "string" ||
            !object(stream.capabilities) ||
            typeof stream.capabilities.video !== "boolean" ||
            typeof stream.capabilities.audio !== "boolean"
          )
            return fail();
          streamIDs.add(stream.id);
          return {
            id: stream.id,
            name: stream.name,
            capabilities: {
              video: stream.capabilities.video,
              audio: stream.capabilities.audio,
            },
          };
        }),
      };
    }),
  };
}
function status(value: unknown, ref: StreamRef): StreamStatus {
  if (
    !object(value) ||
    value.stream_id !== ref.stream_id ||
    !state(value.state) ||
    !(
      value.viewers === null ||
      (Number.isSafeInteger(value.viewers) && (value.viewers as number) >= 0)
    ) ||
    !(
      value.observed_at === null ||
      (typeof value.observed_at === "string" &&
        Number.isFinite(Date.parse(value.observed_at)))
    )
  )
    return fail();
  return {
    stream_id: ref.stream_id,
    state: value.state,
    viewers: value.viewers as number | null,
    observed_at: value.observed_at as string | null,
    ...(value.error === undefined ? {} : { error: publicError(value.error) }),
  };
}
function session(value: unknown, ref: StreamRef): Session {
  if (
    !object(value) ||
    !id(value.id) ||
    value.camera_id !== ref.camera_id ||
    value.stream_id !== ref.stream_id ||
    !state(value.state) ||
    !object(value.playback) ||
    value.playback.protocol !== "webrtc" ||
    !object(value.playback.answer) ||
    value.playback.answer.type !== "answer" ||
    typeof value.playback.answer.sdp !== "string" ||
    !value.playback.answer.sdp.startsWith("v=0\r\n") ||
    value.playback.answer.sdp.length > 65536
  )
    return fail();
  return {
    id: value.id,
    ...ref,
    state: value.state,
    playback: {
      protocol: "webrtc",
      answer: { type: "answer", sdp: value.playback.answer.sdp },
    },
  };
}
export interface ViewerAPI {
  list(signal?: AbortSignal): Promise<CameraList>;
  status(ref: StreamRef, signal?: AbortSignal): Promise<StreamStatus>;
  create(ref: StreamRef, sdp: string): Promise<Session>;
  close(sessionID: string, keepalive?: boolean): Promise<void>;
}
export class APIClient implements ViewerAPI {
  #lastToken: string | undefined;
  readonly #request: typeof fetch;
  readonly #timeoutMS: number;
  constructor(
    request: typeof fetch = (...args) => fetch(...args),
    timeoutMS = 20000,
  ) {
    this.#request = request;
    this.#timeoutMS = timeoutMS;
  }
  async #json(
    path: string,
    init: RequestInit = {},
    expected = 200,
  ): Promise<unknown> {
    const abort = new AbortController();
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      abort.abort();
    }, this.#timeoutMS);
    const canceled = () => abort.abort();
    init.signal?.addEventListener("abort", canceled, { once: true });
    if (init.signal?.aborted) abort.abort();
    try {
      const response = await this.#request(path, {
        ...init,
        signal: abort.signal,
        credentials: "same-origin",
        redirect: "error",
        cache: "no-store",
      });
      if (response.status === 204 && expected === 204) return null;
      const value: unknown = await response.json();
      if (!response.ok) {
        const error = publicError(object(value) ? value.error : undefined);
        throw new PlaybackError(error.code, error.retryable);
      }
      if (response.status !== expected)
        throw new PlaybackError(
          expected === 204 ? "cleanup_incomplete" : "server_unavailable",
        );
      return value;
    } catch (error) {
      if (timedOut) throw new PlaybackError("deadline_exceeded");
      if (init.signal?.aborted) throw new PlaybackError("canceled");
      if (error instanceof PlaybackError) throw error;
      throw new PlaybackError("server_unavailable");
    } finally {
      clearTimeout(timer);
      init.signal?.removeEventListener("abort", canceled);
    }
  }
  #path(ref: StreamRef) {
    if (!id(ref.camera_id) || !id(ref.stream_id))
      throw new PlaybackError("invalid_argument", false);
    return `/api/cameras/${encodeURIComponent(ref.camera_id)}/streams/${encodeURIComponent(ref.stream_id)}`;
  }
  async #write(
    path: string,
    method: "POST" | "DELETE",
    body?: unknown,
    keepalive = false,
  ) {
    // Fresh token for ordinary writes. Page exit may only have time for one
    // keepalive request, so reuse the latest in-memory package token there.
    let token = keepalive ? this.#lastToken : undefined;
    if (!token) {
      const csrf = await this.#json(CSRF_PATH, { keepalive });
      if (
        !object(csrf) ||
        csrf.header_name !== CSRF_HEADER ||
        typeof csrf.token !== "string" ||
        !csrf.token ||
        csrf.token.length > 8192
      )
        throw new PlaybackError("csrf_invalid");
      token = csrf.token;
      this.#lastToken = token;
    }
    return this.#json(
      path,
      {
        method,
        keepalive,
        headers: {
          [CSRF_HEADER]: token,
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      },
      method === "POST" ? 201 : 204,
    );
  }
  async list(signal?: AbortSignal) {
    return catalog(await this.#json("/api/cameras", signal ? { signal } : {}));
  }
  async status(ref: StreamRef, signal?: AbortSignal) {
    return status(
      await this.#json(`${this.#path(ref)}/status`, signal ? { signal } : {}),
      ref,
    );
  }
  async create(ref: StreamRef, sdp: string) {
    if (!sdp.startsWith("v=0\r\n") || sdp.length > 65536)
      throw new PlaybackError("signaling_failed");
    return session(
      await this.#write(`${this.#path(ref)}/sessions`, "POST", {
        offer: { type: "offer", sdp },
      }),
      ref,
    );
  }
  async close(sessionID: string, keepalive = false) {
    if (!id(sessionID)) throw new PlaybackError("invalid_argument", false);
    const result = await this.#write(
      `/api/playback/sessions/${encodeURIComponent(sessionID)}`,
      "DELETE",
      undefined,
      keepalive,
    );
    if (result !== null) throw new PlaybackError("cleanup_incomplete");
  }
}

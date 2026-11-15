// Reference application wire contracts (data/api). No backend process/source fields.
export type CameraID = string;
export type StreamID = string;
export type SessionID = string;
export type State = "unknown" | "idle" | "connecting" | "playing" | "reconnecting" | "closing" | "closed" | "offline" | "failed";
export type ErrorCode =
  | "invalid_argument" | "not_found" | "conflict" | "authentication_failed"
  | "camera_unreachable" | "stream_unavailable" | "backend_unavailable" | "source_unavailable"
  | "signaling_failed" | "media_connection_failed" | "unsupported_capability" | "unsupported_browser"
  | "capacity_exceeded" | "camera_busy" | "cleanup_incomplete" | "controller_conflict"
  | "server_unavailable" | "access_denied" | "csrf_invalid" | "canceled" | "deadline_exceeded" | "internal";
export interface MediaCapabilities { video: boolean; audio: boolean }
export interface StreamSummary { id: StreamID; name: string; capabilities: MediaCapabilities }
export interface CameraSummary { id: CameraID; name: string; availability: State; streams: readonly StreamSummary[] }
export interface CameraList { cameras: readonly CameraSummary[] }
export type CameraDetail = CameraSummary;
export interface StreamList { streams: readonly StreamSummary[] }
export interface ErrorBody { code: ErrorCode; message: string; retryable: boolean }
export interface ErrorResponse { error: ErrorBody }
export interface StreamStatus {
  stream_id: StreamID;
  state: State;
  viewers: number | null;
  observed_at: string | null; // RFC3339 timestamp; null means unobserved
  error?: ErrorBody;
}
export interface CameraStatus { camera_id: CameraID; streams: readonly StreamStatus[] }
export interface SDPOffer { type: "offer"; sdp: string }
export interface SDPAnswer { type: "answer"; sdp: string }
export interface CreateSessionRequest { offer: SDPOffer }
export interface Playback { protocol: "webrtc"; answer: SDPAnswer }
export interface Session { id: SessionID; camera_id: CameraID; stream_id: StreamID; state: State; playback: Playback }
export interface DiagnoseRequest { stream_id?: StreamID }
export type CheckState = "unknown" | "passed" | "failed";
export interface DiagnosticCheck { name: "source" | "backend" | "media"; state: CheckState; error?: ErrorBody }
export interface StreamDiagnosis extends StreamStatus { capabilities: MediaCapabilities; checks: readonly DiagnosticCheck[] }
export interface DiagnoseResponse { camera_id: CameraID; streams: readonly StreamDiagnosis[] }
export interface StreamRef { camera_id: CameraID; stream_id: StreamID }
export interface ReconnectResponse { affected: readonly StreamRef[]; closed_sessions: readonly SessionID[] }
export interface CSRFResponse { token: string; field_name: string; header_name: string }

export const CSRF_PATH = "/api/csrf";
export const CSRF_HEADER = "X-CSRF-Token";
export const DIAGNOSE_COMMAND = "camera.diagnose.v1";
export const RECONNECT_COMMAND = "camera.reconnect.v1";

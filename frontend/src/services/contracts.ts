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
export interface StreamSummary { can_reconnect?: boolean | undefined; recovery_required?: boolean | undefined; id: StreamID; name: string; capabilities: MediaCapabilities }
export interface CameraSummary { can_update_credentials?: boolean | undefined; id: CameraID; name: string; availability: State; streams: readonly StreamSummary[] }
export interface CameraList { revision?: CatalogRevision | undefined; cameras: readonly CameraSummary[] }
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

// Camera Sharing guest wire contracts. Operator status and link copy are native-only.
export const SHARE_API_PREFIX = "/share-api/v1/shares";
export const SHARE_ASSET_PREFIX = "/share-assets/";
export const SHARE_CSRF_HEADER = "X-CSRF-Token";
export interface RedeemShareRequest { invitation: string }
export interface RedeemShareResult { expires_at: string; csrf_token: string }
export interface AdmitShareViewerRequest { viewer_id?: string }
export interface ShareICECredentials {
  urls: readonly string[];
  username: string;
  credential: string;
  expires_at: string;
}
export interface AdmitShareViewerResult {
  viewer_id: string;
  attempt_id: string;
  expires_at: string;
  ice_servers?: readonly ShareICECredentials[];
}
export interface ShareViewerAttempt { attempt_id: string }
export interface OpenShareViewerRequest extends ShareViewerAttempt { offer: SDPOffer }
export interface OpenShareViewerResult { answer: SDPAnswer }

export const CONVERSATION_API_PREFIX = "/conversation-api/v1/rooms";
export const CONVERSATION_VERSION = 1;

export type RoomState =
  | "waiting" | "joining" | "connected" | "reconnecting" | "ended" | "expired" | "failed";
export type RoomRole = "host" | "guest";
export interface RoomPublication { microphone: boolean; camera: boolean }
export interface RoomParticipant {
  role: RoomRole;
  joined: boolean;
  publication: RoomPublication;
}
export interface RoomView {
  room_id: string;
  version: number;
  state: RoomState;
  generation: number;
  expires_at: string;
  participants: RoomParticipant[];
}
export interface RoomAdmission { room: RoomView; role: RoomRole; token: string }
export interface RoomJoinRequest { generation: number; publication: RoomPublication }
export interface JoinedRoom { room: RoomView; ice_servers?: ShareICECredentials[]; relay_only: boolean }
export interface RoomSignal { generation: number; type: "offer" | "answer"; sdp: string }
export interface RoomPoll { room: RoomView; signal?: RoomSignal }
export interface RoomConnectionRequest { generation: number; connected: boolean }
export interface RoomFault { code: string; retryable: boolean }
export interface RoomDownload { url: string; version: string; sha256: string }
export type ShareErrorCode =
  | "invalid_request" | "invalid_invitation" | "expired" | "revoked" | "capacity_reached"
  | "share_already_active" | "stale_viewer_attempt" | "camera_unavailable" | "ingress_unavailable"
  | "relay_unavailable" | "media_unreachable" | "unsupported_offer" | "permission_denied" | "rate_limited";
export interface ShareErrorResponse {
  error: { code: ShareErrorCode; message: string; retryable: boolean };
}

// Camera management wire contracts; see data/api/management.go.
export interface CatalogRevision { runtime_epoch: string; generation: number }
export interface Capabilities { can_manage_cameras: boolean; recovery_scope: string; runtime_epoch: string }
export interface Mutation { operation_key: string; runtime_epoch: string }
export type AuthenticationMode = "anonymous" | "credentials";
export interface Credentials { username: string; password: string }
export interface DraftInput { name: string; endpoint: string; authentication: AuthenticationMode; credentials?: Credentials }
export interface CreateDraftRequest extends Mutation, DraftInput {  }
export interface ImportDraftRequest extends Mutation { yaml: string; env?: string }
export interface CredentialDraftRequest extends Mutation { camera_id: string; credentials: Credentials }
export type PatchDraftRequest = Mutation & { expected_revision: number } & (DraftInput | { credentials: Credentials });
export type DraftState = "incomplete" | "ready" | "testing" | "cleaning" | "tested" | "saving" | "saved" | "canceled" | "expired" | "cleanup_failed";
export type DraftWarning = "server_relative_localhost" | "imported_grants_ignored" | "initial_address_only" | "creator_only_access" | "fixed_address";
export interface DraftReview { credential_update?: boolean; id: string; candidate_id: string; revision: number; runtime_epoch: string; expires_at: string; name: string; endpoint: string; authentication: AuthenticationMode; credentials_present: boolean; dial_address?: string; warnings: readonly DraftWarning[]; state: DraftState; test?: TestResult }
export type TestStage = "negotiating" | "source_media" | "browser_playback" | "cleaning" | "passed" | "failed" | "canceled" | "cleanup_failed";
export type CleanupState = "pending" | "confirmed" | "unknown";
export interface CreateTestRequest extends Mutation { expected_revision: number; offer: SDPOffer }
export interface FrameEvidence { method: string; frames: number; width: number; height: number; paused: boolean }
export interface CompleteTestRequest extends Mutation { expected_revision: number; evidence: FrameEvidence }
export interface TestResult { id: string; attempt_key: string; draft_revision: number; stage: TestStage; expires_at: string; answer?: SDPAnswer; source_media_observed: boolean; browser_frames_reported: boolean; cleanup: CleanupState; passed_at?: string; error?: ManagementErrorBody }
export interface SaveCameraRequest extends Mutation { draft_id: string; expected_revision: number; catalog_revision: CatalogRevision }
export interface SaveCameraResponse { credentials_updated?: boolean; camera_id: string; catalog_revision: CatalogRevision }
export type OperationKind = "create" | "import" | "patch" | "cancel" | "test" | "complete" | "cancel_test" | "save" | "recovery";
export type OperationState = "processing" | "outcome_unknown" | "succeeded" | "not_committed" | "expired" | "not_found";
export interface OperationResult { operation_key: string; kind: OperationKind; state: OperationState; runtime_epoch: string; revision?: number; draft_id?: string; candidate_id?: string; test_id?: string; saved?: SaveCameraResponse; recovery?: ReconnectResponse; error?: ManagementErrorBody }
export type ManagementErrorCode = ErrorCode | "draft_lost" | "draft_expired" | "test_expired" | "draft_revision_conflict" | "catalog_revision_conflict" | "operation_conflict" | "operation_in_progress" | "save_outcome_unknown" | "storage_unavailable" | "recovery_storage_unavailable" | "receipt_expired" | "recovery_lost" | "destination_denied" | "address_unavailable" | "identity_unverified" | "duplicate_camera" | "credentials_required" | "credentials_changed" | "import_invalid" | "playback_evidence_unavailable" | "playback_evidence_insufficient";
export interface ManagementErrorBody { camera_id?: string; code: ManagementErrorCode; message: string; retryable: boolean }
export interface ManagementErrorResponse { error: ManagementErrorBody }

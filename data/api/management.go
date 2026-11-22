package api

import "time"

// Management contracts are host-owned. Defining a contract does not install a
// route. Every mutation is authenticated, operation-keyed and CSRF protected.
const (
	CapabilitiesPath         = "/api/capabilities"
	DraftsPath               = "/api/camera-drafts"
	OperationsPath           = "/api/camera-operations"
	CreateDraftCommand       = "camera.draft.create.v1"
	CredentialDraftCommand   = "camera.credentials.draft.v1"
	ImportDraftCommand       = "camera.draft.import.v1"
	PatchDraftCommand        = "camera.draft.patch.v1"
	CancelDraftCommand       = "camera.draft.cancel.v1"
	TestDraftCommand         = "camera.draft.test.v1"
	CompleteTestCommand      = "camera.draft.complete.v1"
	CancelTestCommand        = "camera.draft.test.cancel.v1"
	SaveCameraCommand        = "camera.save.v1"
	RecoverCameraCommand     = "camera.recovery.v1"
	MaxImportYAMLBytes       = 32 * 1024
	MaxImportEnvBytes        = 8 * 1024
	DraftLifetime            = 15 * time.Minute
	TerminalRecoveryLifetime = 5 * time.Minute
	TestDeadline             = 20 * time.Second
	TestPassLifetime         = 5 * time.Minute
	SaveReceiptLifetime      = 24 * time.Hour
	RecoveryLifetime         = 10 * time.Minute
	MaxOwnerDrafts           = 4
	MaxDrafts                = 32
	MaxOwnerTests            = 1
	MaxTests                 = 4
	MaxDraftOperations       = 64
	MaxOperations            = 2048
	MaxSaveReceipts          = 1024
	MaxRecoveryOperations    = 128
)

type CatalogRevision struct {
	RuntimeEpoch string `json:"runtime_epoch"`
	Generation   uint64 `json:"generation"`
}
type Capabilities struct {
	CanManageCameras bool   `json:"can_manage_cameras"`
	RecoveryScope    string `json:"recovery_scope"`
	RuntimeEpoch     string `json:"runtime_epoch"`
}
type Mutation struct {
	OperationKey string `json:"operation_key"` // 32 lowercase hexadecimal characters (128 random bits)
	RuntimeEpoch string `json:"runtime_epoch"`
}
type AuthenticationMode string

const (
	AuthenticationAnonymous   AuthenticationMode = "anonymous"
	AuthenticationCredentials AuthenticationMode = "credentials"
)

// Credentials is write-only. Never serialize requests as telemetry or events.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (Credentials) String() string   { return "[credentials redacted]" }
func (Credentials) GoString() string { return "[credentials redacted]" }

type DraftInput struct {
	Name           string             `json:"name"`
	Endpoint       string             `json:"endpoint"`
	Authentication AuthenticationMode `json:"authentication"`
	Credentials    *Credentials       `json:"credentials,omitempty"`
}

func (DraftInput) String() string   { return "[draft input redacted]" }
func (DraftInput) GoString() string { return "[draft input redacted]" }

type CreateDraftRequest struct {
	Mutation
	DraftInput
}

type CredentialDraftRequest struct {
	Mutation
	CameraID    string      `json:"camera_id"`
	Credentials Credentials `json:"credentials"` // empty username retains the saved username
}

func (CredentialDraftRequest) String() string   { return "[credential draft redacted]" }
func (CredentialDraftRequest) GoString() string { return "[credential draft redacted]" }

type ImportDraftRequest struct {
	Mutation
	YAML string `json:"yaml"`          // multipart field, not a server path
	Env  string `json:"env,omitempty"` // multipart field; optional
}

func (ImportDraftRequest) String() string   { return "[import redacted]" }
func (ImportDraftRequest) GoString() string { return "[import redacted]" }

// PATCH submits complete review fields. Omitted credentials retain the existing
// secret; explicit anonymous clears it. Null is rejected by the HTTP decoder.
type PatchDraftRequest struct {
	Mutation
	ExpectedRevision uint64 `json:"expected_revision"`
	DraftInput
}
type DraftState string

const (
	DraftIncomplete    DraftState = "incomplete"
	DraftReady         DraftState = "ready"
	DraftTesting       DraftState = "testing"
	DraftCleaning      DraftState = "cleaning"
	DraftTested        DraftState = "tested"
	DraftSaving        DraftState = "saving"
	DraftSaved         DraftState = "saved"
	DraftCanceled      DraftState = "canceled"
	DraftExpired       DraftState = "expired"
	DraftCleanupFailed DraftState = "cleanup_failed"
)

type DraftWarning string

const (
	WarningServerLocalhost    DraftWarning = "server_relative_localhost"
	WarningIgnoredGrants      DraftWarning = "imported_grants_ignored"
	WarningInitialAddressOnly DraftWarning = "initial_address_only"
	WarningPrivateAccess      DraftWarning = "creator_only_access"
	WarningFixedAddress       DraftWarning = "fixed_address"
)

type DraftReview struct {
	ID                 string             `json:"id"`
	CandidateID        string             `json:"candidate_id"`
	Revision           uint64             `json:"revision"`
	RuntimeEpoch       string             `json:"runtime_epoch"`
	ExpiresAt          time.Time          `json:"expires_at"`
	Name               string             `json:"name"`
	Endpoint           string             `json:"endpoint"`
	Authentication     AuthenticationMode `json:"authentication"`
	CredentialsPresent bool               `json:"credentials_present"`
	DialAddress        string             `json:"dial_address,omitempty"`
	Warnings           []DraftWarning     `json:"warnings"`
	State              DraftState         `json:"state"`
	Test               *TestResult        `json:"test,omitempty"`
	CredentialUpdate   bool               `json:"credential_update,omitempty"`
}
type TestStage string

const (
	TestNegotiating     TestStage = "negotiating"
	TestSourceMedia     TestStage = "source_media"
	TestBrowserPlayback TestStage = "browser_playback"
	TestCleaning        TestStage = "cleaning"
	TestPassed          TestStage = "passed"
	TestFailed          TestStage = "failed"
	TestCanceled        TestStage = "canceled"
	TestCleanupFailed   TestStage = "cleanup_failed"
)

type CleanupState string

const (
	CleanupPending   CleanupState = "pending"
	CleanupConfirmed CleanupState = "confirmed"
	CleanupUnknown   CleanupState = "unknown"
)

type CreateTestRequest struct {
	Mutation                  // operation_key is also the attempt_key; never allocate two keys
	ExpectedRevision uint64   `json:"expected_revision"`
	Offer            SDPOffer `json:"offer"`
}
type FrameEvidence struct {
	Method string `json:"method"` // video_frame_callback, frames_decoded or playback_quality
	Frames uint64 `json:"frames"` // advancing frames in this peer, not an absolute counter
	Width  uint32 `json:"width"`
	Height uint32 `json:"height"`
	Paused bool   `json:"paused"`
}
type CompleteTestRequest struct {
	Mutation
	ExpectedRevision uint64        `json:"expected_revision"`
	Evidence         FrameEvidence `json:"evidence"`
}
type TestResult struct {
	ID                    string               `json:"id"`
	AttemptKey            string               `json:"attempt_key"`
	DraftRevision         uint64               `json:"draft_revision"`
	Stage                 TestStage            `json:"stage"`
	ExpiresAt             time.Time            `json:"expires_at"`
	Answer                *SDPAnswer           `json:"answer,omitempty"`
	SourceMediaObserved   bool                 `json:"source_media_observed"`   // server observation
	BrowserFramesReported bool                 `json:"browser_frames_reported"` // client evidence, not server verification
	Cleanup               CleanupState         `json:"cleanup"`
	PassedAt              *time.Time           `json:"passed_at,omitempty"`
	Error                 *ManagementErrorBody `json:"error,omitempty"`
}
type SaveCameraRequest struct {
	Mutation
	DraftID          string          `json:"draft_id"`
	ExpectedRevision uint64          `json:"expected_revision"`
	CatalogRevision  CatalogRevision `json:"catalog_revision"`
}
type SaveCameraResponse struct {
	CameraID           string          `json:"camera_id"`
	CatalogRevision    CatalogRevision `json:"catalog_revision"` // current revision, including receipt replay
	CredentialsUpdated bool            `json:"credentials_updated,omitempty"`
}
type OperationKind string

const (
	OperationCreate     OperationKind = "create"
	OperationImport     OperationKind = "import"
	OperationPatch      OperationKind = "patch"
	OperationCancel     OperationKind = "cancel"
	OperationTest       OperationKind = "test"
	OperationComplete   OperationKind = "complete"
	OperationCancelTest OperationKind = "cancel_test"
	OperationSave       OperationKind = "save"
	OperationRecovery   OperationKind = "recovery"
)

type OperationState string

// OperationLookup describes an optional recovery intent when a key has no
// retained record. DraftID is used only for an exact save retry.
type OperationLookup struct {
	Kind    OperationKind
	DraftID string
}

const (
	OperationProcessing   OperationState = "processing"
	OperationUnknown      OperationState = "outcome_unknown"
	OperationSucceeded    OperationState = "succeeded"
	OperationNotCommitted OperationState = "not_committed"
	OperationExpired      OperationState = "expired"
	OperationNotFound     OperationState = "not_found"
)

// OperationResult contains IDs only, never imported fields, endpoint or secrets.
// GET with candidate_id resolves a permanent save anchor after receipt expiry.
type OperationResult struct {
	OperationKey string               `json:"operation_key"`
	Kind         OperationKind        `json:"kind"`
	State        OperationState       `json:"state"`
	RuntimeEpoch string               `json:"runtime_epoch"`
	Revision     uint64               `json:"revision,omitempty"`
	DraftID      string               `json:"draft_id,omitempty"`
	CandidateID  string               `json:"candidate_id,omitempty"`
	TestID       string               `json:"test_id,omitempty"`
	Saved        *SaveCameraResponse  `json:"saved,omitempty"`
	Recovery     *ReconnectResponse   `json:"recovery,omitempty"`
	Error        *ManagementErrorBody `json:"error,omitempty"`
}

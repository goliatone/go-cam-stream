package viewer

import (
	"context"
	"github.com/goliatone/go-cam-stream/data/api"
)

// CameraManagement is the service boundary consumed by the registered commands
// and read adapters. Authority always comes from WithPrincipal, never a payload.
// Implementations serialize transitions, own secrets and verify cleanup before
// reporting a pass. A canceled context does not prove resource absence.
type CameraManagement interface {
	Capabilities(context.Context) (api.Capabilities, error)
	CreateDraft(context.Context, api.CreateDraftRequest) (api.DraftReview, error)
	ImportDraft(context.Context, api.ImportDraftRequest) (api.DraftReview, error)
	Draft(context.Context, string) (api.DraftReview, error)
	PatchDraft(context.Context, string, api.PatchDraftRequest) (api.DraftReview, error)
	CancelDraft(context.Context, string, api.Mutation) (api.DraftReview, error)
	CreateTest(context.Context, string, api.CreateTestRequest) (api.TestResult, error)
	Test(context.Context, string, string) (api.TestResult, error)
	TestByAttempt(context.Context, string, string) (api.TestResult, error)
	CompleteTest(context.Context, string, string, api.CompleteTestRequest) (api.TestResult, error)
	CancelTest(context.Context, string, string, api.Mutation) (api.TestResult, error)
	SaveCamera(context.Context, api.SaveCameraRequest) (api.SaveCameraResponse, error)
	Operation(context.Context, string, string, string) (api.OperationResult, error) // key, candidate ID, original epoch
}

// CameraRecovery remains usable independently of management enablement. Its
// implementation delegates reset to the existing registered Reconnect command.
type CameraRecovery interface {
	RecoverCamera(context.Context, api.StreamRef, api.Mutation) (api.ReconnectResponse, error)
	RecoveryOperation(context.Context, string, string) (api.OperationResult, error)
}

// ManagedStateRepository owns bytes and durability, not authorization/catalog
// policy. Payload is a versioned private envelope, bounded and validated by the
// codec before commit. Implementations never expose it through API responses.
// A false durable outcome must not be published or returned as a successful save.
type ManagedStateRepository interface {
	Load(context.Context) (ManagedState, error)
	Commit(context.Context, uint64, ManagedState) (CommitOutcome, error)
	Recover(context.Context) (ManagedState, CommitOutcome, error)
	Close() error
}
type ManagedState struct {
	Revision uint64
	CommitID string
	Payload  []byte
}

func (ManagedState) String() string   { return "[managed state redacted]" }
func (ManagedState) GoString() string { return "[managed state redacted]" }

type CommitOutcome string

const (
	CommitPrepared     CommitOutcome = "prepared"
	CommitUnknown      CommitOutcome = "outcome_unknown"
	CommitDurable      CommitOutcome = "durable"
	CommitNotCommitted CommitOutcome = "not_committed"
)

package viewer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-command"
)

type managementMessage interface {
	Type() string
	Validate() error
}
type managementHandler[M managementMessage, R any] struct {
	call func(context.Context, M) (R, error)
}

func (h managementHandler[M, R]) Execute(ctx context.Context, msg M) error {
	var value R
	err := msg.Validate()
	if err == nil {
		value, err = h.call(ctx, msg)
	}
	err = api.ManagementCommandFailure(err)
	if result := command.ResultFromContext[R](ctx); result != nil {
		if err != nil {
			result.StoreError(err)
		} else {
			result.Store(value)
		}
	}
	return err
}
func registerManagementCommand[M managementMessage, R any](set *admin.CommandRegistrationSet, call func(context.Context, M) (R, error)) error {
	var msg M
	if err := admin.RegisterSetCommand(set, managementHandler[M, R]{call}); err != nil {
		return err
	}
	return admin.RegisterSetContextMessageResultFactory[M, R](set, msg.Type(), func(_ context.Context, p map[string]any, ids []string) (M, error) {
		var out M
		if len(ids) != 0 {
			return out, invalid()
		}
		if typed, ok := p["_message"].(M); ok && len(p) == 1 {
			return typed, typed.Validate()
		}
		body, err := json.Marshal(p)
		if err != nil {
			return out, invalid()
		}
		defer clear(body)
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&out) != nil {
			return out, invalid()
		}
		return out, out.Validate()
	})
}

type CreateDraft struct {
	Request api.CreateDraftRequest `json:"request"`
}

func (CreateDraft) Type() string       { return api.CreateDraftCommand }
func (CreateDraft) String() string     { return "[management command redacted]" }
func (m CreateDraft) GoString() string { return m.String() }
func (m CreateDraft) Validate() error {
	return m.Request.Mutation.Validate()
}

type ImportDraft struct {
	Request api.ImportDraftRequest `json:"request"`
}

type CredentialDraft struct {
	Request api.CredentialDraftRequest `json:"request"`
}

func (CredentialDraft) Type() string       { return api.CredentialDraftCommand }
func (CredentialDraft) String() string     { return "[management command redacted]" }
func (m CredentialDraft) GoString() string { return m.String() }
func (m CredentialDraft) Validate() error {
	if !config.ValidID(m.Request.CameraID) {
		return invalid()
	}
	return m.Request.Mutation.Validate()
}

func (ImportDraft) Type() string       { return api.ImportDraftCommand }
func (ImportDraft) String() string     { return "[management command redacted]" }
func (m ImportDraft) GoString() string { return m.String() }
func (m ImportDraft) Validate() error {
	return m.Request.Mutation.Validate()
}

type PatchDraft struct {
	Request api.PatchDraftRequest `json:"request"`
	DraftID string                `json:"draft_id"`
}

func (PatchDraft) Type() string       { return api.PatchDraftCommand }
func (PatchDraft) String() string     { return "[management command redacted]" }
func (m PatchDraft) GoString() string { return m.String() }
func (m PatchDraft) Validate() error {
	if !config.ValidID(m.DraftID) {
		return invalid()
	}
	return m.Request.Mutation.Validate()
}

type CancelDraft struct {
	Request api.Mutation `json:"request"`
	DraftID string       `json:"draft_id"`
}

func (CancelDraft) Type() string       { return api.CancelDraftCommand }
func (CancelDraft) String() string     { return "[management command redacted]" }
func (m CancelDraft) GoString() string { return m.String() }
func (m CancelDraft) Validate() error {
	if !config.ValidID(m.DraftID) {
		return invalid()
	}
	return m.Request.Validate()
}

type TestDraft struct {
	Request api.CreateTestRequest `json:"request"`
	DraftID string                `json:"draft_id"`
}

func (TestDraft) Type() string       { return api.TestDraftCommand }
func (TestDraft) String() string     { return "[management command redacted]" }
func (m TestDraft) GoString() string { return m.String() }
func (m TestDraft) Validate() error {
	if !config.ValidID(m.DraftID) {
		return invalid()
	}
	return m.Request.Validate()
}

type CompleteTest struct {
	Request api.CompleteTestRequest `json:"request"`
	DraftID string                  `json:"draft_id"`
	TestID  string                  `json:"test_id"`
}

func (CompleteTest) Type() string       { return api.CompleteTestCommand }
func (CompleteTest) String() string     { return "[management command redacted]" }
func (m CompleteTest) GoString() string { return m.String() }
func (m CompleteTest) Validate() error {
	if !config.ValidID(m.DraftID) {
		return invalid()
	}
	if !config.ValidID(m.TestID) {
		return invalid()
	}
	return m.Request.Mutation.Validate()
}

type CancelTest struct {
	Request api.Mutation `json:"request"`
	DraftID string       `json:"draft_id"`
	TestID  string       `json:"test_id"`
}

func (CancelTest) Type() string       { return api.CancelTestCommand }
func (CancelTest) String() string     { return "[management command redacted]" }
func (m CancelTest) GoString() string { return m.String() }
func (m CancelTest) Validate() error {
	if !config.ValidID(m.DraftID) {
		return invalid()
	}
	if !config.ValidID(m.TestID) {
		return invalid()
	}
	return m.Request.Validate()
}

type SaveCamera struct {
	Request api.SaveCameraRequest `json:"request"`
}

func (SaveCamera) Type() string       { return api.SaveCameraCommand }
func (SaveCamera) String() string     { return "[management command redacted]" }
func (m SaveCamera) GoString() string { return m.String() }
func (m SaveCamera) Validate() error {
	return m.Request.Mutation.Validate()
}

type RenameCamera struct {
	Request  api.RenameCameraRequest `json:"request"`
	CameraID string                  `json:"camera_id"`
}

func (RenameCamera) Type() string       { return api.RenameCameraCommand }
func (RenameCamera) String() string     { return "[management command redacted]" }
func (m RenameCamera) GoString() string { return m.String() }
func (m RenameCamera) Validate() error {
	if !config.ValidID(m.CameraID) {
		return invalid()
	}
	return m.Request.Validate()
}

type RemoveCamera struct {
	Request  api.RemoveCameraRequest `json:"request"`
	CameraID string                  `json:"camera_id"`
}

func (RemoveCamera) Type() string       { return api.RemoveCameraCommand }
func (RemoveCamera) String() string     { return "[management command redacted]" }
func (m RemoveCamera) GoString() string { return m.String() }
func (m RemoveCamera) Validate() error {
	if !config.ValidID(m.CameraID) {
		return invalid()
	}
	return m.Request.Validate()
}

type StopViewers struct {
	Request api.StopViewersRequest `json:"request"`
	DraftID string                 `json:"draft_id"`
}

func (StopViewers) Type() string       { return api.StopViewersCommand }
func (StopViewers) String() string     { return "[management command redacted]" }
func (m StopViewers) GoString() string { return m.String() }
func (m StopViewers) Validate() error {
	if !config.ValidID(m.DraftID) {
		return invalid()
	}
	return m.Request.Validate()
}

type RecoverCamera struct {
	Request api.Mutation  `json:"request"`
	Ref     api.StreamRef `json:"ref"`
}

func (RecoverCamera) Type() string       { return api.RecoverCameraCommand }
func (RecoverCamera) String() string     { return "[management command redacted]" }
func (m RecoverCamera) GoString() string { return m.String() }
func (m RecoverCamera) Validate() error {
	if !config.ValidID(string(m.Ref.CameraID)) || !config.ValidID(string(m.Ref.StreamID)) {
		return invalid()
	}
	return m.Request.Validate()
}

// RegisterManagement owns inline-only management commands on the host bus.
func RegisterManagement(bus *admin.CommandBus, service CameraManagement, recovery CameraRecovery) (admin.CommandRegistrationHandle, error) {
	if bus == nil || service == nil || recovery == nil {
		return nil, invalid()
	}
	set, err := bus.NewRegistrationSet("camstream.management")
	if err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m CreateDraft) (api.DraftReview, error) {
		return service.CreateDraft(ctx, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m ImportDraft) (api.DraftReview, error) {
		return service.ImportDraft(ctx, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m CredentialDraft) (api.DraftReview, error) {
		return service.CredentialDraft(ctx, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m PatchDraft) (api.DraftReview, error) {
		return service.PatchDraft(ctx, m.DraftID, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m CancelDraft) (api.DraftReview, error) {
		return service.CancelDraft(ctx, m.DraftID, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m TestDraft) (api.TestResult, error) {
		return service.CreateTest(ctx, m.DraftID, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m CompleteTest) (api.TestResult, error) {
		return service.CompleteTest(ctx, m.DraftID, m.TestID, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m CancelTest) (api.TestResult, error) {
		return service.CancelTest(ctx, m.DraftID, m.TestID, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m SaveCamera) (api.SaveCameraResponse, error) {
		return service.SaveCamera(ctx, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m RenameCamera) (api.CameraChange, error) {
		return service.RenameCamera(ctx, m.CameraID, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m RemoveCamera) (api.CameraChange, error) {
		return service.RemoveCamera(ctx, m.CameraID, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m StopViewers) (api.DraftReview, error) {
		return service.StopViewers(ctx, m.DraftID, m.Request)
	}); err != nil {
		return nil, err
	}
	if err = registerManagementCommand(set, func(ctx context.Context, m RecoverCamera) (api.ReconnectResponse, error) {
		return recovery.RecoverCamera(ctx, m.Ref, m.Request)
	}); err != nil {
		return nil, err
	}
	return set.Commit()
}

var _ fmt.Stringer = CreateDraft{}

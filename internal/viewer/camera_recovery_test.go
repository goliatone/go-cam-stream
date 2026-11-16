package viewer_test

import (
	"context"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
)

func startManagementPreview(t *testing.T, s *viewer.CameraManagementService, ctx context.Context) (api.DraftReview, api.TestResult) {
	t.Helper()
	d, err := s.CreateDraft(ctx, draftRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.CreateTest(ctx, d.ID, api.CreateTestRequest{Mutation: mutation(2), ExpectedRevision: d.Revision, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
	if err != nil {
		t.Fatal(err)
	}
	return d, preview
}

func TestManagementExpiredPatchNeverRecoversAsSuccess(t *testing.T) {
	s, ctx, backend := managementFixture(t)
	d, _ := startManagementPreview(t, s, ctx)
	backend.mu.Lock()
	backend.resetFailure = true
	backend.mu.Unlock()
	viewer.ExpireManagementDraftForTest(s, d.ID)
	patch := api.PatchDraftRequest{Mutation: mutation(3), ExpectedRevision: d.Revision, Name: "Edited", Endpoint: d.Endpoint, Authentication: api.AuthenticationAnonymous}
	if _, err := s.PatchDraft(ctx, d.ID, patch); err == nil {
		t.Fatal("expected expiry cleanup failure")
	}
	for _, recoverCleanup := range []bool{false, true} {
		if recoverCleanup {
			backend.mu.Lock()
			backend.resetFailure = false
			backend.mu.Unlock()
			if _, err := s.Draft(ctx, d.ID); err == nil {
				t.Fatal("expired draft readable")
			}
		}
		op, err := s.Operation(ctx, patch.OperationKey, "", "epoch")
		if err != nil || op.State != api.OperationNotCommitted || op.Error == nil {
			t.Fatal("unapplied PATCH outcome", op, err)
		}
		if _, err := s.PatchDraft(ctx, d.ID, patch); err == nil {
			t.Fatal("unapplied PATCH replay succeeded")
		}
	}
}

func TestManagementCancelIntentSurvivesCleanupFailure(t *testing.T) {
	s, ctx, backend := managementFixture(t)
	d, preview := startManagementPreview(t, s, ctx)
	complete := api.CompleteTestRequest{Mutation: mutation(3), ExpectedRevision: d.Revision, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}}
	backend.mu.Lock()
	backend.resetFailure = true
	backend.mu.Unlock()
	if _, err := s.CompleteTest(ctx, d.ID, preview.ID, complete); err == nil {
		t.Fatal("expected completion cleanup failure")
	}
	cancel := mutation(4)
	if _, err := s.CancelTest(ctx, d.ID, preview.ID, cancel); err == nil {
		t.Fatal("expected cancellation cleanup failure")
	}
	late := complete
	late.Mutation = mutation(5)
	if _, err := s.CompleteTest(ctx, d.ID, preview.ID, late); err == nil {
		t.Fatal("late completion revived cancellation")
	}
	backend.mu.Lock()
	backend.resetFailure = false
	backend.mu.Unlock()
	viewer.ExpireManagementTestForTest(s, d.ID, true)
	review, err := s.Draft(ctx, d.ID)
	if err != nil || review.State != api.DraftReady || review.Test.Stage != api.TestCanceled || review.Test.PassedAt != nil || review.Test.Cleanup != api.CleanupConfirmed {
		t.Fatal("canceled outcome lost", review, err)
	}
	for _, tc := range []struct {
		mutation api.Mutation
		state    api.OperationState
	}{{complete.Mutation, api.OperationNotCommitted}, {cancel, api.OperationSucceeded}} {
		op, err := s.Operation(ctx, tc.mutation.OperationKey, "", "epoch")
		if err != nil || op.State != tc.state || op.DraftID != d.ID || op.TestID != preview.ID || op.Revision != d.Revision {
			t.Fatal("recovered result identity/outcome", op, err)
		}
	}
	if _, err := s.CompleteTest(ctx, d.ID, preview.ID, complete); err == nil {
		t.Fatal("original completion replay revived pass")
	}
	canceled, err := s.CancelTest(ctx, d.ID, preview.ID, cancel)
	if err != nil || canceled.Stage != api.TestCanceled {
		t.Fatal("cancel replay", err)
	}
	// A new explicit attempt can acquire the retired source; old outcomes stay terminal.
	next, err := s.CreateTest(ctx, d.ID, api.CreateTestRequest{Mutation: mutation(6), ExpectedRevision: d.Revision, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
	if err != nil || next.ID == preview.ID {
		t.Fatal("fresh attempt unavailable", err)
	}
	if old, err := s.CancelTest(ctx, d.ID, preview.ID, cancel); err != nil || old.ID != preview.ID || old.Stage != api.TestCanceled {
		t.Fatal("old cancel touched new attempt", err)
	}
	late.Mutation = mutation(7)
	if passed, err := s.CompleteTest(ctx, d.ID, next.ID, late); err != nil || passed.Stage != api.TestPassed {
		t.Fatal("new attempt inherited cancellation", err)
	}
}

func TestManagementFailureReasonSurvivesCleanupRetry(t *testing.T) {
	for _, path := range []string{"poll-deadline", "sweep-deadline", "source-fault"} {
		t.Run(path, func(t *testing.T) {
			s, ctx, backend := managementFixture(t)
			d, preview := startManagementPreview(t, s, ctx)
			backend.mu.Lock()
			backend.resetFailure = true
			backend.statusFailure = path == "source-fault"
			backend.mu.Unlock()
			if path != "source-fault" {
				viewer.ExpireManagementTestForTest(s, d.ID, path == "sweep-deadline")
			}
			if path != "sweep-deadline" {
				if _, err := s.Test(ctx, d.ID, preview.ID); err == nil {
					t.Fatal("expected cleanup failure")
				}
			}
			review, err := s.Draft(ctx, d.ID)
			if err != nil || review.State != api.DraftCleanupFailed || review.Test.Error == nil {
				t.Fatal("failure cause lost during cleanup", err)
			}
			wantCode := review.Test.Error.Code
			if path != "source-fault" && wantCode != string(api.CodeTestExpired) {
				t.Fatal("deadline lacks safe reason", wantCode)
			}
			backend.mu.Lock()
			backend.resetFailure = false
			backend.statusFailure = false
			backend.mu.Unlock()
			viewer.ExpireManagementTestForTest(s, d.ID, true)
			result, err := s.Test(ctx, d.ID, preview.ID)
			if err != nil || result.Stage != api.TestFailed || result.Error == nil || result.Error.Code != wantCode || result.Cleanup != api.CleanupConfirmed || result.PassedAt != nil {
				t.Fatal("recovered failure changed", result, err)
			}
		})
	}
}

func TestManagementFailedNegotiationRecovery(t *testing.T) {
	s, ctx, backend := managementFixture(t)
	d, err := s.CreateDraft(ctx, draftRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.resetFailure = true
	backend.blockCreate = true
	backend.mu.Unlock()
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	request := api.CreateTestRequest{Mutation: mutation(2), ExpectedRevision: d.Revision, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}}
	if _, err := s.CreateTest(short, d.ID, request); err == nil {
		t.Fatal("expected failed negotiation cleanup")
	}
	op, err := s.Operation(ctx, request.OperationKey, "", "epoch")
	if err != nil || op.State != api.OperationUnknown {
		t.Fatal("accepted test did not retain cleanup recovery", op, err)
	}
	backend.mu.Lock()
	backend.resetFailure = false
	backend.blockCreate = false
	backend.mu.Unlock()
	viewer.ExpireManagementTestForTest(s, d.ID, true)
	result, err := s.CreateTest(ctx, d.ID, request)
	if err != nil || result.Stage != api.TestFailed || result.Error == nil || result.Cleanup != api.CleanupConfirmed {
		t.Fatal("failed test replay", result, err)
	}
	op, err = s.Operation(ctx, request.OperationKey, "", "epoch")
	if err != nil || op.State != api.OperationSucceeded || op.DraftID != d.ID || op.TestID != result.ID || op.Revision != d.Revision {
		t.Fatal("test recovery identity", op, err)
	}
}

func TestManagementCompletionRecoverySweepReplay(t *testing.T) {
	s, ctx, backend := managementFixture(t)
	d, preview := startManagementPreview(t, s, ctx)
	complete := api.CompleteTestRequest{Mutation: mutation(3), ExpectedRevision: d.Revision, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}}
	backend.mu.Lock()
	backend.resetFailure = true
	backend.mu.Unlock()
	if _, err := s.CompleteTest(ctx, d.ID, preview.ID, complete); err == nil {
		t.Fatal("expected uncertain cleanup")
	}
	backend.mu.Lock()
	backend.resetFailure = false
	backend.mu.Unlock()
	viewer.ExpireManagementTestForTest(s, d.ID, true)
	op, err := s.Operation(ctx, complete.OperationKey, "", "epoch")
	if err != nil || op.State != api.OperationSucceeded || op.Error != nil || op.DraftID != d.ID || op.TestID != preview.ID || op.Revision != d.Revision {
		t.Fatal("automatic recovery identity", op, err)
	}
	passed, err := s.CompleteTest(ctx, d.ID, preview.ID, complete)
	if err != nil || passed.ID != preview.ID || passed.Stage != api.TestPassed || passed.PassedAt == nil || passed.Cleanup != api.CleanupConfirmed {
		t.Fatal("automatic completion replay", passed, err)
	}
}

func TestManagementCancelDraftRecoveryReplay(t *testing.T) {
	s, ctx, backend := managementFixture(t)
	d, _ := startManagementPreview(t, s, ctx)
	backend.mu.Lock()
	backend.resetFailure = true
	backend.mu.Unlock()
	cancel := mutation(3)
	if _, err := s.CancelDraft(ctx, d.ID, cancel); err == nil {
		t.Fatal("expected uncertain draft cleanup")
	}
	backend.mu.Lock()
	backend.resetFailure = false
	backend.mu.Unlock()
	viewer.ExpireManagementTestForTest(s, d.ID, true)
	op, err := s.Operation(ctx, cancel.OperationKey, "", "epoch")
	if err != nil || op.State != api.OperationSucceeded || op.DraftID != d.ID || op.CandidateID != d.CandidateID || op.Revision != d.Revision {
		t.Fatal("cancel draft recovery identity", op, err)
	}
	result, err := s.CancelDraft(ctx, d.ID, cancel)
	if err != nil || result.ID != d.ID || result.State != api.DraftCanceled || result.Test.Stage != api.TestCanceled {
		t.Fatal("cancel draft replay", result, err)
	}
}

func TestSavedRecoveryReplaysWithoutAnotherReset(t *testing.T) {
	service, _, backend, _ := fixture(t)
	create(t, service, "owner", "room1")
	recovery, err := viewer.NewCameraRecoveryService(service, "epoch", func(ctx context.Context, ref api.StreamRef) (api.ReconnectResponse, error) {
		return (viewer.ReconnectHandler{Service: service}).Run(ctx, viewer.Reconnect{CameraID: ref.CameraID, StreamID: ref.StreamID})
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := api.StreamRef{CameraID: "room1", StreamID: "default"}
	for range 2 {
		if _, err = recovery.RecoverCamera(actor("owner"), ref, mutation(1)); err != nil {
			t.Fatal(err)
		}
	}
	backend.mu.Lock()
	resets := backend.resets
	backend.mu.Unlock()
	if resets != 1 {
		t.Fatal("reset replay repeated side effects", resets)
	}
	op, err := recovery.RecoveryOperation(actor("owner"), mutation(1).OperationKey, "epoch")
	if err != nil || op.State != api.OperationSucceeded || op.Recovery == nil {
		t.Fatal("recovery operation", err)
	}
	if _, err = recovery.RecoverCamera(actor("alice"), ref, mutation(2)); err == nil {
		t.Fatal("viewer gained reconnect permission")
	}
	if _, err = recovery.RecoverCamera(actor("owner"), api.StreamRef{CameraID: "room2", StreamID: "default"}, mutation(1)); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationConflict) {
		t.Fatal("changed reconnect intent", err)
	}
	if _, err = recovery.RecoveryOperation(actor("owner"), mutation(1).OperationKey, "old-epoch"); api.ManagementPublicError(err).Error.Code != string(api.CodeRecoveryLost) {
		t.Fatal("stale reset epoch", err)
	}
}

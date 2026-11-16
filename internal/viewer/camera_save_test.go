package viewer_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
)

func saveFixture(t *testing.T, configure ...func(*viewer.ManagementOptions)) (*viewer.CameraManagementService, context.Context, *viewer.PrivateStateStore) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := viewer.OpenPrivateState(context.Background(), filepath.Join(root, "managed"), "installation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s, ctx, _ := managementFixture(t, func(o *viewer.ManagementOptions) {
		o.Repository = store
		for _, fn := range configure {
			fn(o)
		}
	})
	if err = s.InitializeManagedState(ctx, viewer.ManagedState{}); err != nil {
		t.Fatal(err)
	}
	return s, ctx, store
}
func testedDraft(t *testing.T, s *viewer.CameraManagementService, ctx context.Context) (api.DraftReview, api.SaveCameraRequest) {
	t.Helper()
	d, p := startManagementPreview(t, s, ctx)
	_, err := s.CompleteTest(ctx, d.ID, p.ID, api.CompleteTestRequest{Mutation: mutation(3), ExpectedRevision: 1, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}})
	if err != nil {
		t.Fatal(err)
	}
	return d, api.SaveCameraRequest{Mutation: mutation(4), DraftID: d.ID, ExpectedRevision: 1, CatalogRevision: api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 1}}
}
func TestManagementSaveConcurrentReplayRestartAndDisabledAccess(t *testing.T) {
	s, ctx, store := saveFixture(t)
	d, r := testedDraft(t, s, ctx)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			saved, err := s.SaveCamera(ctx, r)
			if err != nil {
				if api.ManagementPublicError(err).Error.Code == string(api.CodeOperationInProgress) {
					return
				}
				t.Error(err)
				return
			}
			if saved.CameraID != d.CandidateID {
				t.Error("duplicate save identity")
			}
		})
	}
	wg.Wait()
	saved, err := s.SaveCamera(ctx, r)
	if err != nil || saved.CatalogRevision.Generation != 2 {
		t.Fatal("save replay", saved, err)
	}
	state, err := store.Load(ctx)
	if err != nil || state.Revision != 1 {
		t.Fatal("multiple durable writes", err)
	}
	fresh, actor, _ := managementFixture(t, func(o *viewer.ManagementOptions) {
		o.Epoch = "restarted"
		o.Repository = store
		o.Policy, _ = viewer.NewManagementPolicy(config.Management{}, "installation")
	})
	if err = fresh.InitializeManagedState(actor, state); err != nil {
		t.Fatal(err)
	}
	result, err := fresh.SaveCamera(actor, r)
	if err != nil || result.CameraID != saved.CameraID || result.CatalogRevision.RuntimeEpoch != "restarted" || result.CatalogRevision.Generation != 1 {
		t.Fatal("restart replay", result, err)
	}
	op, err := fresh.Operation(actor, r.OperationKey, d.CandidateID, "epoch")
	if err != nil || op.State != api.OperationSucceeded || op.Saved.CameraID != d.CandidateID {
		t.Fatal("disabled save lookup", op, err)
	}
	if _, err = fresh.Operation(viewer.WithPrincipal(actor, "other"), r.OperationKey, d.CandidateID, "epoch"); err == nil {
		t.Fatal("cross-owner durable lookup")
	}
	altered := r
	altered.ExpectedRevision++
	if _, err = fresh.SaveCamera(actor, altered); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationConflict) {
		t.Fatal("changed receipt intent", err)
	}
	viewer.ExpireManagedReceiptsForTest(fresh)
	if _, err = fresh.SaveCamera(actor, r); api.ManagementPublicError(err).Error.Code != string(api.CodeReceiptExpired) {
		t.Fatal("expired receipt replay", err)
	}
	if op, err = fresh.Operation(actor, r.OperationKey, d.CandidateID, "epoch"); err != nil || op.State != api.OperationSucceeded {
		t.Fatal("permanent anchor lost", err)
	}
}
func TestManagementSaveUnknownBarrierAndCancellationFence(t *testing.T) {
	s, ctx, store := saveFixture(t)
	d, r := testedDraft(t, s, ctx)
	restore := viewer.FailManagedSyncForTest(store)
	defer restore()
	if _, err := s.SaveCamera(ctx, r); api.ManagementPublicError(err).Error.Code != string(api.CodeSaveUnknown) {
		t.Fatal("failed barrier acknowledged", err)
	}
	if op, err := s.Operation(ctx, r.OperationKey, d.CandidateID, "epoch"); err != nil || op.State != api.OperationUnknown {
		t.Fatal("readback promoted failed barrier", err)
	}
	if _, err := s.CancelDraft(ctx, d.ID, mutation(5)); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationInProgress) {
		t.Fatal("cancellation entered save", err)
	}
	viewer.ExpireManagementDraftForTest(s, d.ID)
	restore()
	op, err := s.Operation(ctx, r.OperationKey, d.CandidateID, "epoch")
	if err != nil || op.State != api.OperationSucceeded || op.Saved.CatalogRevision.Generation != 2 {
		t.Fatal("durable recovery", op, err)
	}
	if _, err = s.SaveCamera(ctx, r); err != nil {
		t.Fatal("lost reply after draft expiry", err)
	}
	state, err := store.Load(ctx)
	if err != nil || state.Revision != 1 {
		t.Fatal("recovery duplicated save", err)
	}
}
func TestManagementSaveRejectsStaleAndUntested(t *testing.T) {
	s, ctx, _ := saveFixture(t)
	d, err := s.CreateDraft(ctx, draftRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	r := api.SaveCameraRequest{Mutation: mutation(2), DraftID: d.ID, ExpectedRevision: 1, CatalogRevision: api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 1}}
	if _, err = s.SaveCamera(ctx, r); api.ManagementPublicError(err).Error.Code != string(api.CodeTestExpired) {
		t.Fatal("untested save", err)
	}
	r.Mutation = mutation(3)
	r.CatalogRevision.Generation = 2
	if _, err = s.SaveCamera(ctx, r); api.ManagementPublicError(err).Error.Code != string(api.CodeCatalogConflict) {
		t.Fatal("stale catalog", err)
	}
}

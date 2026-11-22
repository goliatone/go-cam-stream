package viewer_test

import (
	"context"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
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
	return testedSave(t, s, ctx, draftRequest(1), 2, 1)
}

func testedSave(t *testing.T, s *viewer.CameraManagementService, ctx context.Context, create api.CreateDraftRequest, testOperation int, generation uint64) (api.DraftReview, api.SaveCameraRequest) {
	t.Helper()
	d, err := s.CreateDraft(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateTest(ctx, d.ID, api.CreateTestRequest{
		Mutation: mutation(testOperation), ExpectedRevision: d.Revision, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CompleteTest(ctx, d.ID, p.ID, api.CompleteTestRequest{
		Mutation: mutation(testOperation + 1), ExpectedRevision: d.Revision, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360},
	})
	if err != nil {
		t.Fatal(err)
	}
	return d, api.SaveCameraRequest{
		Mutation: mutation(testOperation + 2), DraftID: d.ID, ExpectedRevision: d.Revision,
		CatalogRevision: api.CatalogRevision{RuntimeEpoch: "epoch", Generation: generation},
	}
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

func TestUncertainSaveLeavesOtherOperationsRecoverable(t *testing.T) {
	s, actor, store := saveFixture(t)
	original, durable := testedDraft(t, s, actor)
	if _, err := s.SaveCamera(actor, durable); err != nil {
		t.Fatal(err)
	}
	other := viewer.WithPrincipal(actor, "other")
	request := draftRequest(11)
	request.Endpoint = "rtsp://localhost:18557/other"
	pendingDraft, pending := testedSave(t, s, other, request, 12, 2)
	actorCreate := draftRequest(21)
	actorCreate.Endpoint = "rtsp://localhost:18558/next"
	openDraft, err := s.CreateDraft(actor, actorCreate)
	if err != nil {
		t.Fatal(err)
	}

	restore := viewer.FailManagedSyncForTest(store)
	defer restore()
	if _, err = s.SaveCamera(other, pending); api.ManagementPublicError(err).Error.Code != string(api.CodeSaveUnknown) {
		t.Fatal("expected uncertain save", err)
	}
	op, err := s.Operation(actor, durable.OperationKey, original.CandidateID, durable.RuntimeEpoch, api.OperationLookup{Kind: api.OperationSave})
	if err != nil || op.State != api.OperationSucceeded || op.Saved == nil || op.Saved.CameraID != original.CandidateID {
		t.Fatal("durable confirmation", op, err)
	}
	if replay, err := s.SaveCamera(actor, durable); err != nil || replay.CameraID != original.CandidateID {
		t.Fatal("durable replay", replay, err)
	}
	op, err = s.Operation(actor, actorCreate.OperationKey, openDraft.CandidateID, actorCreate.RuntimeEpoch, api.OperationLookup{Kind: api.OperationCreate})
	if err != nil || op.State != api.OperationSucceeded || op.DraftID != openDraft.ID {
		t.Fatal("draft confirmation", op, err)
	}
	op, err = s.Operation(actor, pending.OperationKey, pendingDraft.CandidateID, pending.RuntimeEpoch, api.OperationLookup{Kind: api.OperationSave})
	if err != nil || op.State != api.OperationNotFound || op.Saved != nil {
		t.Fatal("unrelated actor received the pending result", op, err)
	}
	op, err = s.Operation(other, pending.OperationKey, pendingDraft.CandidateID, pending.RuntimeEpoch)
	if err != nil || op.State != api.OperationUnknown {
		t.Fatal("lost pending owner's confirmation", op, err)
	}
	altered := pending
	altered.ExpectedRevision++
	if _, err = s.SaveCamera(other, altered); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationConflict) {
		t.Fatal("accepted a different request for the pending key", err)
	}
	restore()
	if replay, err := s.SaveCamera(other, pending); err != nil || replay.CameraID != pendingDraft.CandidateID {
		t.Fatal("pending replay did not recover", replay, err)
	}
}

type pauseSaveResolution struct{}

func TestConcurrentSaveBlockedBeforeWriteHasDefiniteOutcome(t *testing.T) {
	resolving, resume := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(resume) })
	defer release()
	s, actor, store := saveFixture(t, func(o *viewer.ManagementOptions) {
		o.Base = config.Catalog{Cameras: []config.CatalogCamera{{
			Camera: cs.Camera{ID: "base", PhysicalID: "base-device", Name: "Base", Config: rtsp.Config{
				Host: "fixed.example.test", Port: 8554, Profiles: []rtsp.Profile{{ID: "default", Path: "/live"}},
			}},
			Streams: []cs.Stream{{ID: "default", Name: "Default"}},
			Sources: map[cs.StreamID]cs.SourceKey{"default": "base-source"},
		}}}
		o.Lookup = func(ctx context.Context, _ string) ([]netip.Addr, error) {
			if ctx.Value(pauseSaveResolution{}) == true {
				close(resolving)
				select {
				case <-resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
	})
	firstDraft, first := testedDraft(t, s, actor)
	other := viewer.WithPrincipal(actor, "other")
	create := draftRequest(11)
	create.Endpoint = "rtsp://localhost:18557/other"
	secondDraft, second := testedSave(t, s, other, create, 12, 1)
	secondResult := make(chan error, 1)
	go func() {
		_, err := s.SaveCamera(context.WithValue(other, pauseSaveResolution{}, true), second)
		secondResult <- err
	}()
	select {
	case <-resolving:
	case <-time.After(2 * time.Second):
		t.Fatal("second save did not reach address resolution")
	}
	restore := viewer.FailManagedSyncForTest(store)
	defer restore()
	if _, err := s.SaveCamera(actor, first); api.ManagementPublicError(err).Error.Code != string(api.CodeSaveUnknown) {
		t.Fatal("expected uncertain first save", err)
	}
	release()
	if err := <-secondResult; api.ManagementPublicError(err).Error.Code != string(api.CodeOperationInProgress) {
		t.Fatal("second save should be blocked before commit", err)
	}
	checkSecond := func() {
		t.Helper()
		op, err := s.Operation(other, second.OperationKey, secondDraft.CandidateID, second.RuntimeEpoch, api.OperationLookup{Kind: api.OperationSave})
		if err != nil || op.State != api.OperationNotCommitted || op.Error == nil || op.Error.Code != string(api.CodeOperationInProgress) {
			t.Fatal("second save has no definite outcome", op, err)
		}
	}
	checkSecond()
	restore()
	op, err := s.Operation(actor, first.OperationKey, firstDraft.CandidateID, first.RuntimeEpoch)
	if err != nil || op.State != api.OperationSucceeded || op.Saved == nil {
		t.Fatal("first save did not recover", op, err)
	}
	checkSecond()
	state, err := store.Load(other)
	if err != nil || state.Revision != 1 {
		t.Fatal("second save modified the store", state.Revision, err)
	}
	second.Mutation = mutation(15)
	second.CatalogRevision = op.Saved.CatalogRevision
	if saved, err := s.SaveCamera(other, second); err != nil || saved.CameraID != secondDraft.CandidateID {
		t.Fatal("fresh save could not use the tested draft", saved, err)
	}
}

func TestContendedSavesLeaveRoomToFinishDraft(t *testing.T) {
	for _, tc := range []struct {
		action   string
		attempts int
	}{
		{"save", api.MaxOperations},
		{"cancel", api.MaxDraftOperations},
	} {
		t.Run(tc.action, func(t *testing.T) {
			s, owner, store := saveFixture(t)
			firstDraft, first := testedDraft(t, s, owner)
			other := viewer.WithPrincipal(owner, "other")
			create := draftRequest(11)
			create.Endpoint = "rtsp://localhost:18557/other"
			draft, blocked := testedSave(t, s, other, create, 12, 1)
			restore := viewer.FailManagedSyncForTest(store)
			defer restore()
			if _, err := s.SaveCamera(owner, first); api.ManagementPublicError(err).Error.Code != string(api.CodeSaveUnknown) {
				t.Fatal("expected uncertain first save", err)
			}
			for i := range tc.attempts {
				blocked.Mutation = mutation(100 + i)
				if _, err := s.SaveCamera(other, blocked); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationInProgress) {
					t.Fatalf("blocked save %d: %v", i, err)
				}
			}
			restore()
			saved, err := s.Operation(owner, first.OperationKey, firstDraft.CandidateID, first.RuntimeEpoch)
			if err != nil || saved.State != api.OperationSucceeded || saved.Saved == nil {
				t.Fatal("first operation did not recover", saved, err)
			}
			last, err := s.Operation(other, blocked.OperationKey, draft.CandidateID, blocked.RuntimeEpoch)
			if err != nil || last.State != api.OperationNotCommitted || last.Error == nil || last.Error.Code != string(api.CodeOperationInProgress) {
				t.Fatal("missing definite outcome for latest blocked save", last, err)
			}
			next := mutation(100 + tc.attempts)
			if tc.action == "cancel" {
				canceled, err := s.CancelDraft(other, draft.ID, next)
				if err != nil || canceled.State != api.DraftCanceled {
					t.Fatal("blocked operations exhausted cancellation capacity", canceled.State, err)
				}
				return
			}
			blocked.Mutation = next
			blocked.CatalogRevision = saved.Saved.CatalogRevision
			result, err := s.SaveCamera(other, blocked)
			if err != nil || result.CameraID != draft.CandidateID {
				t.Fatal("blocked operations exhausted save capacity", result, err)
			}
		})
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

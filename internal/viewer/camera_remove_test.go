package viewer_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

func secondCamera(n int) api.CreateDraftRequest {
	r := draftRequest(n)
	r.Name = "Second"
	r.Endpoint = "rtsp://localhost:18557/second"
	return r
}

func listed(t *testing.T, cameras *viewer.CameraService, ctx context.Context, id string) bool {
	t.Helper()
	list, err := cameras.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list.Cameras {
		if string(c.ID) == id {
			return true
		}
	}
	return false
}

func TestRemoveCameraStopsViewersAndFreesItsAddress(t *testing.T) {
	s, ctx, store, cameras, _ := editFixture(t)
	create := draftRequest(1)
	create.Authentication = api.AuthenticationCredentials
	create.Credentials = &api.Credentials{Username: "user", Password: "REMOVED-PASSWORD"}
	_, firstSave := testedSave(t, s, ctx, create, 2, 1)
	saved, err := s.SaveCamera(ctx, firstSave)
	if err != nil {
		t.Fatal(err)
	}
	kept := savedCamera(t, s, ctx, secondCamera(10), 11, 2)
	offer := api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}
	id := cs.CameraID(saved.CameraID)
	watching, err := cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	other, err := cameras.CreateSession(ctx, cs.CameraID(kept.CameraID), "default", api.CreateSessionRequest{Offer: offer})
	if err != nil {
		t.Fatal(err)
	}

	waiting := api.RemoveCameraRequest{Mutation: mutation(20), CatalogRevision: cameras.CatalogRevision()}
	if _, err = s.RemoveCamera(ctx, saved.CameraID, waiting); cs.DescribeError(err).Code != cs.CodeCameraBusy {
		t.Fatal("a watched camera was removed without stopping its viewers", err)
	}
	if !listed(t, cameras, ctx, saved.CameraID) {
		t.Fatal("a refused removal changed the catalog")
	}
	if _, err = cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer}); err != nil {
		t.Fatal("a refused removal kept the fence", err)
	}
	for name, actor := range map[string]context.Context{"another manager": viewer.WithPrincipal(ctx, "other"), "not a manager": viewer.WithPrincipal(ctx, "viewer")} {
		denied := api.RemoveCameraRequest{Mutation: mutation(21), CatalogRevision: cameras.CatalogRevision(), StopViewers: true}
		if _, err = s.RemoveCamera(actor, saved.CameraID, denied); cs.DescribeError(err).Code != cs.CodeAccessDenied {
			t.Fatal(name, "removed the camera", err)
		}
	}
	stale := api.RemoveCameraRequest{Mutation: mutation(22), CatalogRevision: api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 1}, StopViewers: true}
	if _, err = s.RemoveCamera(ctx, saved.CameraID, stale); api.ManagementPublicError(err).Error.Code != string(api.CodeCatalogConflict) {
		t.Fatal("stale removal", err)
	}

	remove := api.RemoveCameraRequest{Mutation: mutation(23), CatalogRevision: cameras.CatalogRevision(), StopViewers: true}
	var removals atomic.Int32
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			change, err := s.RemoveCamera(ctx, saved.CameraID, remove)
			if api.ManagementPublicError(err).Error.Code == string(api.CodeOperationInProgress) {
				return
			}
			if err != nil || !change.Removed || change.CameraID != saved.CameraID || change.CatalogRevision.Generation != 4 {
				t.Error("removal", change, err)
			}
			if change.StoppedSessions == 2 {
				removals.Add(1)
			}
		})
	}
	wg.Wait()
	if removals.Load() != 1 {
		t.Fatal("the stopped-session count was not reported once", removals.Load())
	}
	if listed(t, cameras, ctx, saved.CameraID) || !listed(t, cameras, ctx, kept.CameraID) {
		t.Fatal("catalog after removal")
	}
	if _, err = cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer}); cs.DescribeError(err).Code != cs.CodeAccessDenied {
		t.Fatal("a removed camera admitted playback", err)
	}
	if err = cameras.CloseSession(ctx, other.ID); err != nil {
		t.Fatal("removal interrupted another camera", err)
	}
	// Its sessions closed with it, and each owner may still close its own, as a
	// page does on leaving the camera; nobody else may.
	if err = cameras.CloseSession(viewer.WithPrincipal(ctx, "other"), watching.ID); cs.DescribeError(err).Code != cs.CodeAccessDenied {
		t.Fatal("another principal closed a removed camera's session", err)
	}
	if err = cameras.CloseSession(ctx, watching.ID); err != nil {
		t.Fatal("the owner could not close a removed camera's session", err)
	}
	state, err := store.Load(ctx)
	if err != nil || state.Revision != 3 {
		t.Fatal("removal should commit exactly once", err)
	}
	defer clear(state.Payload)
	if strings.Contains(string(state.Payload), saved.CameraID+`","physical_id"`) || strings.Contains(string(state.Payload), "REMOVED-PASSWORD") || !strings.Contains(string(state.Payload), `"removed":[`) {
		t.Fatal("the removed record, its sign-in or its tombstone")
	}

	again, err := s.RemoveCamera(ctx, saved.CameraID, remove)
	if err != nil || !again.Removed || again.StoppedSessions != 0 {
		t.Fatal("removal replay", again, err)
	}
	changed := remove
	changed.StopViewers = false
	if _, err = s.RemoveCamera(ctx, saved.CameraID, changed); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationConflict) {
		t.Fatal("changed removal intent", err)
	}
	op, err := s.Operation(ctx, remove.OperationKey, saved.CameraID, "epoch", api.OperationLookup{Kind: api.OperationRemove})
	if err != nil || op.State != api.OperationSucceeded || op.Change == nil || !op.Change.Removed {
		t.Fatal("removal lookup", op, err)
	}
	if _, err = s.SaveCamera(ctx, firstSave); api.ManagementPublicError(err).Error.Code != string(api.CodeReceiptExpired) {
		t.Fatal("a removed camera's creation replayed", err)
	}
	if op, err = s.Operation(ctx, firstSave.OperationKey, saved.CameraID, "epoch"); err != nil || op.State != api.OperationExpired || op.CandidateID != saved.CameraID {
		t.Fatal("removed creation lookup", op, err)
	}
	if _, err = s.CameraSettings(ctx, saved.CameraID); cs.DescribeError(err).Code != cs.CodeAccessDenied {
		t.Fatal("settings for a removed camera", err)
	}

	// The removed camera's address is free for a new camera at once.
	reuse := draftRequest(30)
	reuse.Name = "Replacement"
	replacement := savedCamera(t, s, ctx, reuse, 31, 4)
	if replacement.CameraID == saved.CameraID || !listed(t, cameras, ctx, replacement.CameraID) {
		t.Fatal("replacement camera", replacement)
	}
}

func TestRemoveIdleCameraWithoutStopping(t *testing.T) {
	s, ctx, _, cameras, _ := editFixture(t)
	saved := savedCamera(t, s, ctx, draftRequest(1), 2, 1)
	change, err := s.RemoveCamera(ctx, saved.CameraID, api.RemoveCameraRequest{Mutation: mutation(10), CatalogRevision: cameras.CatalogRevision()})
	if err != nil || !change.Removed || change.StoppedSessions != 0 || listed(t, cameras, ctx, saved.CameraID) {
		t.Fatal("idle removal", change, err)
	}
	list, err := cameras.List(ctx)
	if err != nil || len(list.Cameras) != 0 {
		t.Fatal("the manager should keep catalog access with no cameras left", err)
	}
}

// refusingRepository reports a commit that did not happen.
type refusingRepository struct {
	viewer.ManagedStateRepository
	refuse atomic.Bool
}

func (r *refusingRepository) Commit(ctx context.Context, expected uint64, state viewer.ManagedState) (viewer.CommitOutcome, error) {
	if r.refuse.Load() {
		return viewer.CommitNotCommitted, api.ManagementFailure(api.CodeStorageUnavailable)
	}
	return r.ManagedStateRepository.Commit(ctx, expected, state)
}

func TestRemovalCommitOutcomesHoldOrReleaseTheFence(t *testing.T) {
	s, ctx, store, cameras, _ := editFixture(t)
	saved := savedCamera(t, s, ctx, draftRequest(1), 2, 1)
	id := cs.CameraID(saved.CameraID)
	offer := api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}
	remove := api.RemoveCameraRequest{Mutation: mutation(10), CatalogRevision: cameras.CatalogRevision()}
	restore := viewer.FailManagedSyncForTest(store)
	if _, err := s.RemoveCamera(ctx, saved.CameraID, remove); api.ManagementPublicError(err).Error.Code != string(api.CodeSaveUnknown) {
		t.Fatal("removal acknowledged before durability", err)
	}
	if _, err := cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer}); cs.DescribeError(err).Code != cs.CodeCameraBusy {
		t.Fatal("an uncertain removal admitted playback", err)
	}
	if !listed(t, cameras, ctx, saved.CameraID) {
		t.Fatal("an uncertain removal was published")
	}
	lookup := api.OperationLookup{Kind: api.OperationRemove}
	if op, err := s.Operation(ctx, remove.OperationKey, saved.CameraID, "epoch", lookup); err != nil || op.State != api.OperationUnknown || op.Kind != api.OperationRemove {
		t.Fatal("uncertain removal lookup", op, err)
	}
	restore()
	op, err := s.Operation(ctx, remove.OperationKey, saved.CameraID, "epoch", lookup)
	if err != nil || op.State != api.OperationSucceeded || op.Change == nil || !op.Change.Removed {
		t.Fatal("recovered removal", op, err)
	}
	if listed(t, cameras, ctx, saved.CameraID) {
		t.Fatal("recovered removal not published")
	}

	// A commit that did not happen releases the fence and keeps the camera.
	refusing := &refusingRepository{ManagedStateRepository: store}
	s2, ctx2, _ := managementFixture(t, func(o *viewer.ManagementOptions) { o.Repository, o.Epoch, cameras = refusing, "second", o.Viewer })
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s2.InitializeManagedState(ctx2, state); err != nil {
		t.Fatal(err)
	}
	create := draftRequest(20)
	create.RuntimeEpoch = "second"
	d, err := s2.CreateDraft(ctx2, create)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := s2.CreateTest(ctx2, d.ID, api.CreateTestRequest{Mutation: api.Mutation{OperationKey: mutation(21).OperationKey, RuntimeEpoch: "second"}, ExpectedRevision: d.Revision, Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s2.CompleteTest(ctx2, d.ID, probe.ID, api.CompleteTestRequest{Mutation: api.Mutation{OperationKey: mutation(22).OperationKey, RuntimeEpoch: "second"}, ExpectedRevision: d.Revision, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}}); err != nil {
		t.Fatal(err)
	}
	created, err := s2.SaveCamera(ctx2, api.SaveCameraRequest{Mutation: api.Mutation{OperationKey: mutation(23).OperationKey, RuntimeEpoch: "second"}, DraftID: d.ID, ExpectedRevision: d.Revision, CatalogRevision: cameras.CatalogRevision()})
	if err != nil {
		t.Fatal(err)
	}
	refusing.refuse.Store(true)
	refused := api.RemoveCameraRequest{Mutation: api.Mutation{OperationKey: mutation(24).OperationKey, RuntimeEpoch: "second"}, CatalogRevision: cameras.CatalogRevision()}
	if _, err = s2.RemoveCamera(ctx2, created.CameraID, refused); api.ManagementPublicError(err).Error.Code != string(api.CodeStorageUnavailable) {
		t.Fatal("a refused commit reported a removal", err)
	}
	if !listed(t, cameras, ctx2, created.CameraID) {
		t.Fatal("a refused commit removed the camera")
	}
	session, err := cameras.CreateSession(ctx2, cs.CameraID(created.CameraID), "default", api.CreateSessionRequest{Offer: offer})
	if err != nil {
		t.Fatal("a refused commit kept the fence", err)
	}
	if err = cameras.CloseSession(ctx2, session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestTombstonesValidatePruneAndRollBack(t *testing.T) {
	s, ctx, store, cameras, _ := editFixture(t)
	removed := savedCamera(t, s, ctx, draftRequest(1), 2, 1)
	kept := savedCamera(t, s, ctx, secondCamera(10), 11, 2)
	if _, err := s.RemoveCamera(ctx, removed.CameraID, api.RemoveCameraRequest{Mutation: mutation(20), CatalogRevision: cameras.CatalogRevision()}); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(state.Payload)
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(state.Payload, &raw); err != nil {
		t.Fatal(err)
	}
	var tombstones []map[string]any
	if err = json.Unmarshal(raw["removed"], &tombstones); err != nil || len(tombstones) != 1 || tombstones[0]["id"] != removed.CameraID {
		t.Fatal("tombstone", string(raw["removed"]), err)
	}
	for name, edit := range map[string]func(string) string{
		"tombstone for a live camera": func(p string) string {
			return strings.Replace(p, `"removed":[{"id":"`+removed.CameraID, `"removed":[{"id":"`+kept.CameraID, 1)
		},
		"removal without a tombstone": func(p string) string { return strings.Replace(p, `,"removed":[`+string(raw["removed"])[1:], "", 1) },
		"removal by another owner": func(p string) string {
			return strings.Replace(p, `"owner":"manager","candidate_id":"`+removed.CameraID, `"owner":"other","candidate_id":"`+removed.CameraID, 1)
		},
	} {
		changed := edit(string(state.Payload))
		if changed == string(state.Payload) {
			t.Fatal(name, "edit did not apply")
		}
		if _, err := viewer.DecodeManagedCatalog(viewer.ManagedState{Revision: state.Revision, CommitID: state.CommitID, Payload: []byte(changed)}); err == nil {
			t.Fatal(name, "accepted")
		}
	}

	// Rollback drops tombstones and editing receipts; an older host reads it.
	if dropped, err := viewer.PrepareManagedRollback(ctx, store); err != nil || dropped != 1 {
		t.Fatal("rollback", dropped, err)
	}
	pruned, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(pruned.Payload)
	if strings.Contains(string(pruned.Payload), `"removed"`) || strings.Contains(string(pruned.Payload), `"kind"`) {
		t.Fatal("rollback kept editing state")
	}
	var cameras2 *viewer.CameraService
	fresh, actor, _ := managementFixture(t, func(o *viewer.ManagementOptions) { o.Epoch, o.Repository, cameras2 = "rolled-back", store, o.Viewer })
	if err = fresh.InitializeManagedState(actor, pruned); err != nil {
		t.Fatal(err)
	}
	if listed(t, cameras2, actor, removed.CameraID) || !listed(t, cameras2, actor, kept.CameraID) {
		t.Fatal("rollback restored a removed camera or lost a kept one")
	}

	// Tombstones leave with their last receipt at the next commit.
	s3, ctx3, store3, cameras3, _ := editFixture(t)
	gone := savedCamera(t, s3, ctx3, draftRequest(1), 2, 1)
	stay := savedCamera(t, s3, ctx3, secondCamera(10), 11, 2)
	if _, err = s3.RemoveCamera(ctx3, gone.CameraID, api.RemoveCameraRequest{Mutation: mutation(20), CatalogRevision: cameras3.CatalogRevision()}); err != nil {
		t.Fatal(err)
	}
	viewer.ExpireManagedReceiptsForTest(s3)
	if _, err = s3.RenameCamera(ctx3, stay.CameraID, api.RenameCameraRequest{Mutation: mutation(21), Name: "Renamed", CatalogRevision: cameras3.CatalogRevision()}); err != nil {
		t.Fatal(err)
	}
	after, err := store3.Load(ctx3)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(after.Payload)
	if strings.Contains(string(after.Payload), `"removed"`) || strings.Contains(string(after.Payload), gone.CameraID) {
		t.Fatal("expired tombstone was kept")
	}
}

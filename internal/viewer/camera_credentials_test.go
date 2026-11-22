package viewer_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

func TestManagementCredentialUpdatePreservesCameraAndRecoversSave(t *testing.T) {
	var cameras *viewer.CameraService
	service, ctx, store := saveFixture(t, func(o *viewer.ManagementOptions) { cameras = o.Viewer })
	offer := api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}
	evidence := api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}
	pass := func(d api.DraftReview, sequence int) {
		t.Helper()
		test, err := service.CreateTest(ctx, d.ID, api.CreateTestRequest{Mutation: mutation(sequence), ExpectedRevision: d.Revision, Offer: offer})
		if err != nil {
			t.Fatal(err)
		}
		test, err = service.CompleteTest(ctx, d.ID, test.ID, api.CompleteTestRequest{Mutation: mutation(sequence + 1), ExpectedRevision: d.Revision, Evidence: evidence})
		if err != nil || test.Stage != api.TestPassed || test.Cleanup != api.CleanupConfirmed {
			t.Fatal("test did not pass and clean up", err)
		}
	}
	input := draftRequest(1)
	input.Authentication = api.AuthenticationCredentials
	input.Credentials = &api.Credentials{Username: "original-user", Password: "original-password"}
	draft, err := service.CreateDraft(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	pass(draft, 2)
	created, err := service.SaveCamera(ctx, api.SaveCameraRequest{Mutation: mutation(4), DraftID: draft.ID,
		ExpectedRevision: draft.Revision, CatalogRevision: cameras.CatalogRevision()})
	if err != nil {
		t.Fatal(err)
	}
	id := cs.CameraID(created.CameraID)
	other := viewer.WithPrincipal(ctx, "other")
	update := api.CredentialDraftRequest{Mutation: mutation(5), CameraID: created.CameraID, Credentials: api.Credentials{Password: "rotated-password"}}
	if _, err = service.CredentialDraft(other, update); cs.DescribeError(err).Code != cs.CodeAccessDenied {
		t.Fatal("non-owner credential update", err)
	}
	dup, err := service.CreateDraft(other, draftRequest(6))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateTest(other, dup.ID, api.CreateTestRequest{Mutation: mutation(7), ExpectedRevision: dup.Revision, Offer: offer})
	if body := api.ManagementPublicError(err).Error; body.Code != string(api.CodeDuplicateCamera) || body.CameraID != "" {
		t.Fatal("duplicate exposes another manager's camera")
	}
	updateDraft, err := service.CredentialDraft(ctx, update)
	if err != nil || !updateDraft.CredentialUpdate || updateDraft.CandidateID != created.CameraID {
		t.Fatal("credential update draft", err)
	}
	update.Mutation = mutation(8)
	staleDraft, err := service.CredentialDraft(ctx, update)
	if err != nil {
		t.Fatal(err)
	}
	session, err := cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateTest(ctx, updateDraft.ID, api.CreateTestRequest{Mutation: mutation(9), ExpectedRevision: updateDraft.Revision, Offer: offer})
	if cs.DescribeError(err).Code != cs.CodeCameraBusy {
		t.Fatal("credential test should wait for existing playback", err)
	}
	if err = cameras.CloseSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	pass(updateDraft, 10)
	save := api.SaveCameraRequest{Mutation: mutation(12), DraftID: updateDraft.ID, ExpectedRevision: updateDraft.Revision, CatalogRevision: cameras.CatalogRevision()}
	lookup := api.OperationLookup{Kind: api.OperationSave, DraftID: updateDraft.ID}
	missing, err := service.Operation(ctx, save.OperationKey, created.CameraID, "epoch", lookup)
	if err != nil || missing.State != api.OperationNotFound {
		t.Fatal("the original save should be retryable against the tested draft", err)
	}
	restore := viewer.FailManagedSyncForTest(store)
	defer restore()
	if _, err = service.SaveCamera(ctx, save); api.ManagementPublicError(err).Error.Code != string(api.CodeSaveUnknown) {
		t.Fatal("save acknowledged before durability", err)
	}
	if _, err = cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer}); cs.DescribeError(err).Code != cs.CodeCameraBusy {
		t.Fatal("uncertain commit admitted the old source", err)
	}
	if err = service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	restore()
	// No operation lookup or subsequent save is needed to reopen playback.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		session, err = cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer})
		if err == nil {
			if err = cameras.CloseSession(ctx, session.ID); err != nil {
				t.Fatal(err)
			}
			break
		}
		if cs.DescribeError(err).Code != cs.CodeCameraBusy || time.Now().After(deadline) {
			t.Fatal("background recovery did not reopen playback", err)
		}
	}
	op, err := service.Operation(ctx, save.OperationKey, created.CameraID, "epoch")
	if err != nil || op.Saved == nil || !op.Saved.CredentialsUpdated || op.Saved.CameraID != created.CameraID {
		t.Fatal("recovering rotation", err)
	}
	_, err = service.CreateTest(ctx, staleDraft.ID, api.CreateTestRequest{Mutation: mutation(13), ExpectedRevision: staleDraft.Revision, Offer: offer})
	if api.ManagementPublicError(err).Error.Code != string(api.CodeCredentialsChanged) {
		t.Fatal("stale credential update admitted", err)
	}
	state, err := store.Load(ctx)
	if err != nil || state.Revision != 2 {
		t.Fatal("durable rotation", err)
	}
	defer clear(state.Payload)
	catalog, err := viewer.DecodeManagedCatalog(state)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Destroy()
	if len(catalog.Cameras) != 1 || catalog.Cameras[0].ID != id || len(catalog.Credentials) != 1 ||
		string(catalog.Credentials[0].Username) != "original-user" || string(catalog.Credentials[0].Password) != "rotated-password" {
		t.Fatal("rotation did not replace only the saved password")
	}
	fresh, actor, _ := managementFixture(t, func(o *viewer.ManagementOptions) {
		o.Epoch, o.Repository, cameras = "restarted", store, o.Viewer
	})
	if err = fresh.InitializeManagedState(actor, state); err != nil {
		t.Fatal(err)
	}
	result, err := fresh.SaveCamera(actor, save)
	if err != nil || !result.CredentialsUpdated || result.CameraID != created.CameraID {
		t.Fatal("durable update receipt replay", err)
	}
	opened, err := cameras.CreateSession(actor, id, "default", api.CreateSessionRequest{Offer: offer})
	if err != nil {
		t.Fatal("same camera lost playback or access", err)
	}
	if err = cameras.CloseSession(context.Background(), opened.ID); cs.DescribeError(err).Code != cs.CodeAccessDenied {
		t.Fatal("session close permitted an unknown principal", err)
	}
	if err = cameras.CloseSession(actor, opened.ID); err != nil {
		t.Fatal(err)
	}
	if removed, err := viewer.PrepareManagedRollback(ctx, store); err != nil || removed != 1 {
		t.Fatal("prune the rotation receipt", removed, err)
	}
	pruned, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(pruned.Payload)
	afterPrune, actor, _ := managementFixture(t, func(o *viewer.ManagementOptions) {
		o.Epoch, o.Repository = "after-prune", store
	})
	if err = afterPrune.InitializeManagedState(actor, pruned); err != nil {
		t.Fatal(err)
	}
	for _, epoch := range []string{"after-prune", "epoch"} {
		expired, err := afterPrune.Operation(actor, save.OperationKey, created.CameraID, epoch, lookup)
		if err != nil || expired.State != api.OperationExpired || expired.Saved != nil {
			t.Fatal("missing rotation receipt was not classified as expired", epoch, err)
		}
	}
	_, err = afterPrune.Operation(actor, mutation(99).OperationKey, created.CameraID, "epoch", api.OperationLookup{Kind: api.OperationPatch})
	if api.ManagementPublicError(err).Error.Code != string(api.CodeDraftLost) {
		t.Fatal("a missing edit operation was confused with an expired save", err)
	}
}

func TestCanceledCredentialProbePreservesSavedSourceFault(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := viewer.OpenPrivateState(context.Background(), filepath.Join(root, "managed"), "installation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var cameras *viewer.CameraService
	service, ctx, backend := managementFixture(t, func(o *viewer.ManagementOptions) { o.Repository, cameras = store, o.Viewer })
	if err = service.InitializeManagedState(ctx, viewer.ManagedState{}); err != nil {
		t.Fatal(err)
	}
	original, save := testedDraft(t, service, ctx)
	if _, err = service.SaveCamera(ctx, save); err != nil {
		t.Fatal(err)
	}
	id := cs.CameraID(original.CandidateID)
	offer := api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}
	v, err := cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.faults[backend.sessions[v.ID].SourceKey] = cs.CodeSourceUnavailable
	backend.mu.Unlock()
	if err = cameras.CloseSession(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	requireFault := func() {
		t.Helper()
		detail, err := cameras.Camera(ctx, id)
		if err != nil || !detail.Streams[0].RecoveryRequired {
			t.Fatal("saved camera lost its recovery action", err)
		}
	}
	requireFault()
	draft, err := service.CredentialDraft(ctx, api.CredentialDraftRequest{
		Mutation: mutation(5), CameraID: string(id), Credentials: api.Credentials{Username: "camera", Password: "replacement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := service.CreateTest(ctx, draft.ID, api.CreateTestRequest{Mutation: mutation(6), ExpectedRevision: draft.Revision, Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	requireFault()
	if _, err = service.CancelTest(ctx, draft.ID, probe.ID, mutation(7)); err != nil {
		t.Fatal(err)
	}
	requireFault()
	if _, err = cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: offer}); cs.DescribeError(err).Code != cs.CodeSourceUnavailable {
		t.Fatal("viewer demand bypassed the saved fault latch", err)
	}
}

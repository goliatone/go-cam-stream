package viewer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

type probeBackend struct{ *testBackend }

func (b probeBackend) RetireTransientSessions(_ context.Context, key cs.SourceKey, ids []cs.SessionID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range ids {
		v := b.sessions[id]
		if v.SourceKey != key || v.Resources != cs.PresenceAbsent {
			return cs.NewError(cs.CodeCleanupIncomplete, nil)
		}
	}
	for _, id := range ids {
		delete(b.sessions, id)
	}
	return nil
}
func managementFixture(t *testing.T, configure ...func(*viewer.ManagementOptions)) (*viewer.CameraManagementService, context.Context, *testBackend) {
	t.Helper()
	backend := newBackend()
	core, err := cs.NewService(probeBackend{backend}, cs.ServiceOptions{}, rtsp.Strategy{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = core.Start(ctx); err != nil {
		t.Fatal(err)
	}
	catalog, err := viewer.NewCameraService(core, config.Catalog{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := viewer.NewManagementPolicy(config.Management{Enabled: true, Principals: []string{"manager", "other"}}, "installation")
	if err != nil {
		t.Fatal(err)
	}
	secret := viewer.NewManagedSecrets(nil)
	options := viewer.ManagementOptions{Core: core, Viewer: catalog, Policy: policy, Secrets: secret, Epoch: "epoch", CleanupTimeout: time.Second, MaxSources: 4}
	for _, change := range configure {
		change(&options)
	}
	service, err := viewer.NewCameraManagementService(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(ctx); _ = core.Close(ctx); secret.Close() })
	return service, viewer.WithPrincipal(ctx, "manager"), backend
}
func mutation(n int) api.Mutation {
	return api.Mutation{OperationKey: fmt.Sprintf("%032x", n), RuntimeEpoch: "epoch"}
}
func draftRequest(n int) api.CreateDraftRequest {
	return api.CreateDraftRequest{Mutation: mutation(n), DraftInput: api.DraftInput{Name: "Camera", Endpoint: "rtsp://localhost:18556/macbook", Authentication: api.AuthenticationAnonymous}}
}
func TestManagementDraftIdempotencyAndOwnership(t *testing.T) {
	service, ctx, _ := managementFixture(t)
	request := draftRequest(1)
	first, err := service.CreateDraft(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for j := 0; j < 12; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.CreateDraft(ctx, request)
			if err != nil || result.ID != first.ID {
				t.Error("duplicate operation allocated draft", err)
			}
		}()
	}
	wg.Wait()
	request.Name = "Changed"
	if _, err = service.CreateDraft(ctx, request); err == nil {
		t.Fatal("changed intent accepted")
	}
	if _, err = service.Draft(viewer.WithPrincipal(ctx, "other"), first.ID); err == nil {
		t.Fatal("cross-owner read")
	}
	if _, err = service.CreateDraft(viewer.WithPrincipal(ctx, "viewer"), draftRequest(2)); err == nil {
		t.Fatal("viewer can manage")
	}
	patch := api.PatchDraftRequest{Mutation: mutation(2), ExpectedRevision: first.Revision, DraftInput: api.DraftInput{Name: "Edited", Endpoint: first.Endpoint, Authentication: api.AuthenticationAnonymous}}
	edited, err := service.PatchDraft(ctx, first.ID, patch)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.PatchDraft(ctx, first.ID, patch)
	if err != nil || replay.Revision != edited.Revision {
		t.Fatal("lost PATCH not recoverable", err)
	}
	operation, err := service.Operation(ctx, mutation(2).OperationKey, "", "epoch")
	if err != nil || operation.DraftID != first.ID || operation.Revision != edited.Revision {
		t.Fatal("operation lookup", err)
	}
	request = draftRequest(3)
	request.RuntimeEpoch = "old"
	if _, err = service.CreateDraft(ctx, request); err == nil {
		t.Fatal("old epoch admitted")
	}
	if _, err = service.CancelDraft(ctx, first.ID, mutation(4)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CancelDraft(ctx, first.ID, mutation(4)); err != nil {
		t.Fatal("cancel replay", err)
	}
}
func TestManagementPreviewEvidenceAndAliasReservation(t *testing.T) {
	service, ctx, backend := managementFixture(t)
	draft, err := service.CreateDraft(ctx, draftRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	offer := api.CreateTestRequest{Mutation: mutation(2), ExpectedRevision: draft.Revision, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}}
	preview, err := service.CreateTest(ctx, draft.ID, offer)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Draft(ctx, draft.ID)
	if err != nil || review.DialAddress != "127.0.0.1" {
		t.Fatal("localhost pin", err)
	}
	secondRequest := draftRequest(3)
	secondRequest.Endpoint = "rtsp://127.0.0.1:18556/other"
	second, err := service.CreateDraft(viewer.WithPrincipal(ctx, "other"), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	offer.Mutation = mutation(4)
	if _, err = service.CreateTest(viewer.WithPrincipal(ctx, "other"), second.ID, offer); err == nil {
		t.Fatal("alias tested twice")
	}
	complete := api.CompleteTestRequest{Mutation: mutation(5), ExpectedRevision: draft.Revision, Evidence: api.FrameEvidence{Method: "playing", Frames: 5, Width: 640, Height: 360}}
	if _, err = service.CompleteTest(ctx, draft.ID, preview.ID, complete); err == nil {
		t.Fatal("playing event treated as frames")
	}
	complete.Mutation = mutation(6)
	complete.Evidence.Method = "frames_decoded"
	passed, err := service.CompleteTest(ctx, draft.ID, preview.ID, complete)
	if err != nil || passed.Stage != api.TestPassed || passed.Cleanup != api.CleanupConfirmed {
		t.Fatal("test not cleaned/passed", err)
	}
	replay, err := service.CompleteTest(ctx, draft.ID, preview.ID, complete)
	if err != nil || replay.ID != passed.ID {
		t.Fatal("completion replay", err)
	}
	backend.mu.Lock()
	remaining := len(backend.sessions)
	backend.mu.Unlock()
	if remaining != 0 {
		t.Fatal("probe metadata retained")
	}
	patch := api.PatchDraftRequest{Mutation: mutation(7), ExpectedRevision: draft.Revision, DraftInput: api.DraftInput{Name: "Edited", Endpoint: draft.Endpoint, Authentication: api.AuthenticationAnonymous}}
	changed, err := service.PatchDraft(ctx, draft.ID, patch)
	if err != nil || changed.Test != nil || changed.State != api.DraftReady {
		t.Fatal("edit retained pass", err)
	}
}
func TestManagementSecretReviewAndQuota(t *testing.T) {
	service, ctx, _ := managementFixture(t)
	request := draftRequest(1)
	request.Authentication = api.AuthenticationCredentials
	request.Credentials = &api.Credentials{Username: "camera", Password: "private-secret"}
	review, err := service.CreateDraft(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(review)
	if strings.Contains(string(body), "private-secret") || strings.Contains(string(body), "managed://") || !review.CredentialsPresent {
		t.Fatal("secret review boundary")
	}
	for j := 2; j <= 4; j++ {
		if _, err = service.CreateDraft(ctx, draftRequest(j)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.CreateDraft(ctx, draftRequest(5)); err == nil {
		t.Fatal("owner quota not enforced")
	}
}

var _ viewer.AddressLookup = func(context.Context, string) ([]netip.Addr, error) { return nil, nil }

func TestManagementFailedCleanupRecovery(t *testing.T) {
	service, ctx, backend := managementFixture(t)
	draft, err := service.CreateDraft(ctx, draftRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.CreateTest(ctx, draft.ID, api.CreateTestRequest{Mutation: mutation(2), ExpectedRevision: draft.Revision, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.resetFailure = true
	backend.mu.Unlock()
	completion := api.CompleteTestRequest{Mutation: mutation(3), ExpectedRevision: draft.Revision, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}}
	if _, err = service.CompleteTest(ctx, draft.ID, preview.ID, completion); err == nil {
		t.Fatal("failed cleanup passed")
	}
	status, err := service.Operation(ctx, mutation(3).OperationKey, "", "epoch")
	if err != nil || status.State != api.OperationUnknown {
		t.Fatal("cleanup uncertainty lost", err)
	}
	backend.mu.Lock()
	backend.resetFailure = false
	backend.mu.Unlock()
	canceled, err := service.CancelDraft(ctx, draft.ID, mutation(4))
	if err != nil || canceled.State != api.DraftCanceled {
		t.Fatal("cleanup recovery failed", err)
	}
	status, err = service.Operation(ctx, mutation(3).OperationKey, "", "epoch")
	if err != nil || status.State != api.OperationNotCommitted {
		t.Fatal("canceled completion revived", err)
	}
}

func TestManagementShutdownIsBoundedAndStopsAdmission(t *testing.T) {
	service, ctx, _ := managementFixture(t)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateDraft(ctx, draftRequest(1)); err != nil {
		t.Fatal(err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateDraft(ctx, draftRequest(2)); err == nil {
		t.Fatal("shutdown admitted mutation")
	}
}

// A previously uncertain completion must recover the original result IDs after cleanup.
func TestManagementRecoveredCompletionIncludesResultIdentity(t *testing.T) {
	service, ctx, backend := managementFixture(t)
	draft, err := service.CreateDraft(ctx, draftRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.CreateTest(ctx, draft.ID, api.CreateTestRequest{Mutation: mutation(2), ExpectedRevision: draft.Revision, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
	if err != nil {
		t.Fatal(err)
	}
	complete := api.CompleteTestRequest{Mutation: mutation(3), ExpectedRevision: draft.Revision, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}}
	backend.mu.Lock()
	backend.resetFailure = true
	backend.mu.Unlock()
	if _, err = service.CompleteTest(ctx, draft.ID, preview.ID, complete); err == nil {
		t.Fatal("expected uncertain cleanup")
	}
	backend.mu.Lock()
	backend.resetFailure = false
	backend.mu.Unlock()
	retry := complete
	retry.Mutation = mutation(4)
	if _, err = service.CompleteTest(ctx, draft.ID, preview.ID, retry); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.Operation(ctx, complete.OperationKey, "", "epoch")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != api.OperationSucceeded || recovered.DraftID != draft.ID || recovered.TestID != preview.ID || recovered.Revision != draft.Revision {
		t.Fatalf("recovered completion lost result identity: state=%s draft_id_present=%t test_id_present=%t revision=%d", recovered.State, recovered.DraftID != "", recovered.TestID != "", recovered.Revision)
	}
}

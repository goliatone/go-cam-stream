package viewer_test

import (
	"context"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

// fencedDraft saves a signed-in camera, opens two sessions on it, starts a
// sign-in draft and stops everyone's playback for it.
func fencedDraft(t *testing.T, s *viewer.CameraManagementService, ctx context.Context, cameras *viewer.CameraService, backend *testBackend) (api.SaveCameraResponse, api.DraftReview) {
	t.Helper()
	create := draftRequest(1)
	create.Authentication = api.AuthenticationCredentials
	create.Credentials = &api.Credentials{Username: "user", Password: "first-password"}
	saved := savedCamera(t, s, ctx, create, 2, 1)
	id := cs.CameraID(saved.CameraID)
	var sessions []cs.SessionID
	for range 2 {
		session, err := cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: probeOffer})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session.ID)
	}
	d, err := s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(10), CameraID: saved.CameraID, Credentials: api.Credentials{Password: "second-password"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTest(ctx, d.ID, api.CreateTestRequest{Mutation: mutation(11), ExpectedRevision: d.Revision, Offer: probeOffer}); cs.DescribeError(err).Code != cs.CodeCameraBusy {
		t.Fatal("a watched camera's test should wait", err)
	}
	if _, err = s.StopViewers(viewer.WithPrincipal(ctx, "other"), d.ID, api.StopViewersRequest{Mutation: mutation(12), ExpectedRevision: d.Revision}); cs.DescribeError(err).Code != cs.CodeAccessDenied {
		t.Fatal("another manager stopped the viewers", err)
	}
	if _, err = s.StopViewers(ctx, d.ID, api.StopViewersRequest{Mutation: mutation(13), ExpectedRevision: d.Revision + 1}); api.ManagementPublicError(err).Error.Code != string(api.CodeRevisionConflict) {
		t.Fatal("a stale stop", err)
	}
	fenced, err := s.StopViewers(ctx, d.ID, api.StopViewersRequest{Mutation: mutation(14), ExpectedRevision: d.Revision})
	if err != nil || !fenced.Fenced || fenced.Revision != d.Revision {
		t.Fatal("stop viewers", fenced, err)
	}
	backend.mu.Lock()
	for _, id := range sessions {
		if backend.sessions[id].State != cs.StateClosed {
			t.Error("a session kept playing", id)
		}
	}
	backend.mu.Unlock()
	if _, err = cameras.CreateSession(ctx, id, "default", api.CreateSessionRequest{Offer: probeOffer}); cs.DescribeError(err).Code != cs.CodeCameraBusy {
		t.Fatal("a fenced camera admitted playback", err)
	}
	return saved, fenced
}

func playable(t *testing.T, cameras *viewer.CameraService, ctx context.Context, id string) bool {
	t.Helper()
	session, err := cameras.CreateSession(ctx, cs.CameraID(id), "default", api.CreateSessionRequest{Offer: probeOffer})
	if cs.DescribeError(err).Code == cs.CodeCameraBusy {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = cameras.CloseSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	return true
}

func TestStopViewersHoldsTheFenceThroughTestAndSave(t *testing.T) {
	s, ctx, _, cameras, _, backend := editHost(t)
	saved, d := fencedDraft(t, s, ctx, cameras, backend)
	again, err := s.StopViewers(ctx, d.ID, api.StopViewersRequest{Mutation: mutation(15), ExpectedRevision: d.Revision})
	if err != nil || !again.Fenced {
		t.Fatal("a repeated stop should keep the fence", err)
	}
	passProbe(t, s, ctx, d, 16)
	if playable(t, cameras, ctx, saved.CameraID) {
		t.Fatal("the fence ended with the test")
	}
	if current, err := s.Draft(ctx, d.ID); err != nil || !current.Fenced || current.State != api.DraftTested {
		t.Fatal("tested fenced draft", current, err)
	}
	if _, err = s.SaveCamera(ctx, api.SaveCameraRequest{Mutation: mutation(18), DraftID: d.ID, ExpectedRevision: d.Revision, CatalogRevision: cameras.CatalogRevision()}); err != nil {
		t.Fatal("save under the held fence", err)
	}
	if !playable(t, cameras, ctx, saved.CameraID) {
		t.Fatal("the save did not release the fence")
	}
	if current, err := s.Draft(ctx, d.ID); err != nil || current.Fenced || current.State != api.DraftSaved {
		t.Fatal("saved draft still fenced", current, err)
	}
}

func TestStopViewersFenceReleasePaths(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		s, ctx, _, cameras, _, backend := editHost(t)
		saved, d := fencedDraft(t, s, ctx, cameras, backend)
		if _, err := s.CancelDraft(ctx, d.ID, mutation(20)); err != nil {
			t.Fatal(err)
		}
		if !playable(t, cameras, ctx, saved.CameraID) {
			t.Fatal("cancellation kept the fence")
		}
	})
	t.Run("expiry", func(t *testing.T) {
		s, ctx, _, cameras, _, backend := editHost(t)
		saved, d := fencedDraft(t, s, ctx, cameras, backend)
		viewer.ExpireManagementDraftForTest(s, d.ID)
		if _, err := s.Draft(ctx, d.ID); api.ManagementPublicError(err).Error.Code != string(api.CodeDraftExpired) {
			t.Fatal(err)
		}
		if !playable(t, cameras, ctx, saved.CameraID) {
			t.Fatal("expiry kept the fence")
		}
	})
	t.Run("shutdown", func(t *testing.T) {
		s, ctx, _, cameras, _, backend := editHost(t)
		saved, _ := fencedDraft(t, s, ctx, cameras, backend)
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if !playable(t, cameras, ctx, saved.CameraID) {
			t.Fatal("shutdown kept the fence")
		}
	})
	t.Run("abandoned save", func(t *testing.T) {
		s, ctx, store, cameras, _, backend := editHost(t)
		refusing := &refusingRepository{ManagedStateRepository: store}
		saved, d := fencedDraft(t, s, ctx, cameras, backend)
		passProbe(t, s, ctx, d, 16)
		viewer.SetManagedRepositoryForTest(s, refusing)
		refusing.refuse.Store(true)
		if _, err := s.SaveCamera(ctx, api.SaveCameraRequest{Mutation: mutation(18), DraftID: d.ID, ExpectedRevision: d.Revision, CatalogRevision: cameras.CatalogRevision()}); api.ManagementPublicError(err).Error.Code != string(api.CodeStorageUnavailable) {
			t.Fatal("refused save", err)
		}
		if !playable(t, cameras, ctx, saved.CameraID) {
			t.Fatal("a save that did not commit kept the fence")
		}
	})
	t.Run("new camera draft", func(t *testing.T) {
		s, ctx, _, _, _, _ := editHost(t)
		d, err := s.CreateDraft(ctx, draftRequest(1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StopViewers(ctx, d.ID, api.StopViewersRequest{Mutation: mutation(2), ExpectedRevision: d.Revision}); cs.DescribeError(err).Code != cs.CodeInvalidArgument {
			t.Fatal("a new camera has no viewers to stop", err)
		}
	})
}

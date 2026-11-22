package viewer_test

import (
	"context"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

var (
	probeOffer    = api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}
	probeEvidence = api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}
)

// passProbe runs a draft's test to a confirmed pass with operation keys n, n+1.
func passProbe(t *testing.T, s *viewer.CameraManagementService, ctx context.Context, d api.DraftReview, n int) api.TestResult {
	t.Helper()
	probe, err := s.CreateTest(ctx, d.ID, api.CreateTestRequest{Mutation: mutation(n), ExpectedRevision: d.Revision, Offer: probeOffer})
	if err != nil {
		t.Fatal(err)
	}
	probe, err = s.CompleteTest(ctx, d.ID, probe.ID, api.CompleteTestRequest{Mutation: mutation(n + 1), ExpectedRevision: d.Revision, Evidence: probeEvidence})
	if err != nil || probe.Stage != api.TestPassed || probe.Cleanup != api.CleanupConfirmed {
		t.Fatal("test did not pass and clean up", probe.Stage, err)
	}
	return probe
}

func savedCredential(t *testing.T, store *viewer.PrivateStateStore) (viewer.ManagedCatalog, func()) {
	t.Helper()
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := viewer.DecodeManagedCatalog(state)
	clear(state.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, catalog.Destroy
}

func TestAddressChangeKeepsTheSignInAndFreesTheOldAddress(t *testing.T) {
	s, ctx, store, cameras, path := editFixture(t)
	create := draftRequest(1)
	create.Name = "Porch"
	create.Authentication = api.AuthenticationCredentials
	create.Credentials = &api.Credentials{Username: "porch-user", Password: "porch-password"}
	saved := savedCamera(t, s, ctx, create, 2, 1)
	before, destroy := savedCredential(t, store)
	oldCredential := before.Credentials[0].ID
	destroy()

	moved := "rtsp://localhost:18558/moved?channel=2"
	d, err := s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(10), CameraID: saved.CameraID, Endpoint: moved})
	if err != nil || !d.CredentialUpdate || d.Endpoint != moved || d.Authentication != api.AuthenticationCredentials || !d.CredentialsPresent || d.State != api.DraftReady {
		t.Fatal("address draft", d, err)
	}
	if d.DialAddress != "" {
		t.Fatal("an address draft pinned before its test")
	}
	passProbe(t, s, ctx, d, 11)
	if d, err = s.Draft(ctx, d.ID); err != nil || d.DialAddress != "127.0.0.1" {
		t.Fatal("tested address", d.DialAddress, err)
	}
	result, err := s.SaveCamera(ctx, api.SaveCameraRequest{Mutation: mutation(13), DraftID: d.ID, ExpectedRevision: d.Revision, CatalogRevision: cameras.CatalogRevision()})
	if err != nil || result.CameraID != saved.CameraID || !result.CredentialsUpdated {
		t.Fatal("address save", result, err)
	}
	settings, err := s.CameraSettings(ctx, saved.CameraID)
	if err != nil || settings.Endpoint != moved || settings.Authentication != api.AuthenticationCredentials || settings.Name != "Porch" {
		t.Fatal("settings after the address change", settings, err)
	}
	after, destroy := savedCredential(t, store)
	if len(after.Credentials) != 1 || after.Credentials[0].ID == oldCredential || string(after.Credentials[0].Username) != "porch-user" || string(after.Credentials[0].Password) != "porch-password" {
		t.Fatal("an address-only change should copy the saved sign-in")
	}
	destroy()
	session, err := cameras.CreateSession(ctx, cs.CameraID(saved.CameraID), "default", api.CreateSessionRequest{Offer: probeOffer})
	if err != nil {
		t.Fatal("the moved camera does not play", err)
	}
	if err = cameras.CloseSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}

	// The new address belongs to the camera, and its old one is free.
	again := draftRequest(20)
	again.Endpoint = moved
	taken, err := s.CreateDraft(ctx, again)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTest(ctx, taken.ID, api.CreateTestRequest{Mutation: mutation(21), ExpectedRevision: taken.Revision, Offer: probeOffer})
	if body := api.ManagementPublicError(err).Error; body.Code != string(api.CodeDuplicateCamera) || body.CameraID != saved.CameraID {
		t.Fatal("the moved address was not reserved for the camera", err)
	}
	freed := savedCamera(t, s, ctx, draftRequest(30), 31, 3)
	if freed.CameraID == saved.CameraID {
		t.Fatal("the old address did not admit a new camera")
	}

	// The change survives a restart and rollback preparation.
	_ = store.Close()
	reopened, err := viewer.OpenPrivateState(context.Background(), path, "installation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err = viewer.PrepareManagedRollback(ctx, reopened); err != nil {
		t.Fatal(err)
	}
	state, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(state.Payload)
	if strings.Contains(string(state.Payload), `"kind"`) {
		t.Fatal("rollback left an editing receipt")
	}
	fresh, actor, _ := managementFixture(t, func(o *viewer.ManagementOptions) { o.Epoch, o.Repository = "restarted", reopened })
	if err = fresh.InitializeManagedState(actor, state); err != nil {
		t.Fatal(err)
	}
	if settings, err = fresh.CameraSettings(actor, saved.CameraID); err != nil || settings.Endpoint != moved || settings.DialAddress != "127.0.0.1" {
		t.Fatal("address lost across restart", settings, err)
	}
}

func TestAddressAndSignInChangeTogether(t *testing.T) {
	s, ctx, store, cameras, _ := editFixture(t)
	create := draftRequest(1)
	create.Authentication = api.AuthenticationCredentials
	create.Credentials = &api.Credentials{Username: "kept-user", Password: "old-password"}
	saved := savedCamera(t, s, ctx, create, 2, 1)
	d, err := s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(10), CameraID: saved.CameraID, Endpoint: "rtsp://localhost:18558/both", Credentials: api.Credentials{Password: "new-password"}})
	if err != nil {
		t.Fatal(err)
	}
	passProbe(t, s, ctx, d, 11)
	if _, err = s.SaveCamera(ctx, api.SaveCameraRequest{Mutation: mutation(13), DraftID: d.ID, ExpectedRevision: d.Revision, CatalogRevision: cameras.CatalogRevision()}); err != nil {
		t.Fatal(err)
	}
	after, destroy := savedCredential(t, store)
	defer destroy()
	if len(after.Credentials) != 1 || string(after.Credentials[0].Username) != "kept-user" || string(after.Credentials[0].Password) != "new-password" || after.Cameras[0].Binding.Port != 18558 || after.Cameras[0].Path != "/both" {
		t.Fatal("address and sign-in change")
	}
	same, err := s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(20), CameraID: saved.CameraID, Endpoint: "rtsp://localhost:18558/both"})
	if api.ManagementPublicError(err).Error.Code != string(api.CodeCredentialsRequired) {
		t.Fatal("an unchanged address with no password should need a sign-in", same, err)
	}
}

func TestAnonymousCameraAddressChangeStaysAnonymous(t *testing.T) {
	s, ctx, store, cameras, _ := editFixture(t)
	saved := savedCamera(t, s, ctx, draftRequest(1), 2, 1)
	d, err := s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(10), CameraID: saved.CameraID, Endpoint: "rtsp://localhost:18558/open"})
	if err != nil || d.Authentication != api.AuthenticationAnonymous || d.CredentialsPresent {
		t.Fatal("anonymous address draft", d, err)
	}
	passProbe(t, s, ctx, d, 11)
	if _, err = s.SaveCamera(ctx, api.SaveCameraRequest{Mutation: mutation(13), DraftID: d.ID, ExpectedRevision: d.Revision, CatalogRevision: cameras.CatalogRevision()}); err != nil {
		t.Fatal(err)
	}
	settings, err := s.CameraSettings(ctx, saved.CameraID)
	if err != nil || settings.Authentication != api.AuthenticationAnonymous || settings.Endpoint != "rtsp://localhost:18558/open" {
		t.Fatal("anonymous settings", settings, err)
	}
	after, destroy := savedCredential(t, store)
	defer destroy()
	if len(after.Credentials) != 0 {
		t.Fatal("an anonymous camera gained a sign-in")
	}
	if _, err = s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(20), CameraID: saved.CameraID}); api.ManagementPublicError(err).Error.Code != string(api.CodeCredentialsRequired) {
		t.Fatal("an empty settings draft", err)
	}
	if _, err = s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(21), CameraID: saved.CameraID, Endpoint: "rtsp://user@localhost:18558/open"}); cs.DescribeError(err).Code != cs.CodeInvalidArgument {
		t.Fatal("an endpoint with credentials", err)
	}
}

func TestAddressConflictAndFailedTestLeaveTheCameraUntouched(t *testing.T) {
	s, ctx, _, cameras, _, backend := editHost(t)
	first := savedCamera(t, s, ctx, draftRequest(1), 2, 1)
	second := savedCamera(t, s, ctx, secondCamera(10), 11, 2)
	original, err := s.CameraSettings(ctx, second.CameraID)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(20), CameraID: second.CameraID, Endpoint: "rtsp://localhost:18556/elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTest(ctx, conflict.ID, api.CreateTestRequest{Mutation: mutation(21), ExpectedRevision: conflict.Revision, Offer: probeOffer})
	if body := api.ManagementPublicError(err).Error; body.Code != string(api.CodeDuplicateCamera) || body.CameraID != first.CameraID {
		t.Fatal("an address used by another camera", err)
	}

	watched, err := cameras.CreateSession(ctx, cs.CameraID(second.CameraID), "default", api.CreateSessionRequest{Offer: probeOffer})
	if err != nil {
		t.Fatal(err)
	}
	failing, err := s.CredentialDraft(ctx, api.CredentialDraftRequest{Mutation: mutation(30), CameraID: second.CameraID, Endpoint: "rtsp://localhost:18559/broken"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTest(ctx, failing.ID, api.CreateTestRequest{Mutation: mutation(31), ExpectedRevision: failing.Revision, Offer: probeOffer}); cs.DescribeError(err).Code != cs.CodeCameraBusy {
		t.Fatal("an address test should wait for existing playback", err)
	}
	if err = cameras.CloseSession(ctx, watched.ID); err != nil {
		t.Fatal(err)
	}
	probe, err := s.CreateTest(ctx, failing.ID, api.CreateTestRequest{Mutation: mutation(32), ExpectedRevision: failing.Revision, Offer: probeOffer})
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	savedSource := backend.sessions[watched.ID].SourceKey
	for _, session := range backend.sessions {
		if session.SourceKey != savedSource {
			backend.faults[session.SourceKey] = cs.CodeSourceUnavailable
		}
	}
	backend.mu.Unlock()
	if probe, err = s.Test(ctx, failing.ID, probe.ID); err != nil || probe.Stage != api.TestFailed {
		t.Fatal("the faulted address test did not fail", probe.Stage, err)
	}
	if got, err := s.CameraSettings(ctx, second.CameraID); err != nil || got != original {
		t.Fatal("a failed test changed the camera", got, err)
	}
	session, err := cameras.CreateSession(ctx, cs.CameraID(second.CameraID), "default", api.CreateSessionRequest{Offer: probeOffer})
	if err != nil {
		t.Fatal("the camera lost its saved address", err)
	}
	if err = cameras.CloseSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
}

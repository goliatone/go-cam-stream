package viewer_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

// savedCamera adds a tested camera through the ordinary draft, test and save path.
func savedCamera(t *testing.T, s *viewer.CameraManagementService, ctx context.Context, create api.CreateDraftRequest, operation int, generation uint64) api.SaveCameraResponse {
	t.Helper()
	_, save := testedSave(t, s, ctx, create, operation, generation)
	saved, err := s.SaveCamera(ctx, save)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestCameraSettingsReadOnlyForTheCreatingManager(t *testing.T) {
	s, ctx, _ := saveFixture(t)
	create := draftRequest(1)
	create.Name = "Porch"
	create.Endpoint = "rtsp://localhost:18556/macbook?channel=1"
	create.Authentication = api.AuthenticationCredentials
	create.Credentials = &api.Credentials{Username: "SECRET-USER", Password: "SECRET-PASSWORD"}
	signed := savedCamera(t, s, ctx, create, 2, 1)
	anonymous := draftRequest(10)
	anonymous.Endpoint = "rtsp://localhost:18557/second"
	open := savedCamera(t, s, ctx, anonymous, 11, 2)

	got, err := s.CameraSettings(ctx, signed.CameraID)
	if err != nil {
		t.Fatal(err)
	}
	want := api.CameraSettings{CameraID: signed.CameraID, Name: "Porch", Endpoint: "rtsp://localhost:18556/macbook?channel=1",
		DialAddress: "127.0.0.1", Authentication: api.AuthenticationCredentials, CatalogRevision: api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 3}}
	if got != want {
		t.Fatalf("settings %+v", got)
	}
	if text := fmt.Sprintf("%v", got); strings.Contains(text, "SECRET") {
		t.Fatal("settings carried credentials")
	}
	if got, err = s.CameraSettings(ctx, open.CameraID); err != nil || got.Authentication != api.AuthenticationAnonymous || got.Endpoint != "rtsp://localhost:18557/second" {
		t.Fatal("anonymous settings", got, err)
	}
	for name, tc := range map[string]struct {
		ctx context.Context
		id  string
	}{
		"another manager": {viewer.WithPrincipal(ctx, "other"), signed.CameraID},
		"not a manager":   {viewer.WithPrincipal(ctx, "viewer"), signed.CameraID},
		"unknown camera":  {ctx, "missing"},
	} {
		if _, err := s.CameraSettings(tc.ctx, tc.id); cs.DescribeError(err).Code != cs.CodeAccessDenied {
			t.Fatal(name, "read settings", err)
		}
	}
}

// editFixture is saveFixture with the viewer and the state directory, so a
// test can restart the host from disk.
func editFixture(t *testing.T) (*viewer.CameraManagementService, context.Context, *viewer.PrivateStateStore, *viewer.CameraService, string) {
	t.Helper()
	s, ctx, store, cameras, path, _ := editHost(t)
	return s, ctx, store, cameras, path
}

func editHost(t *testing.T) (*viewer.CameraManagementService, context.Context, *viewer.PrivateStateStore, *viewer.CameraService, string, *testBackend) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "managed")
	store, err := viewer.OpenPrivateState(context.Background(), path, "installation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var cameras *viewer.CameraService
	s, ctx, backend := managementFixture(t, func(o *viewer.ManagementOptions) { o.Repository, cameras = store, o.Viewer })
	if err = s.InitializeManagedState(ctx, viewer.ManagedState{}); err != nil {
		t.Fatal(err)
	}
	return s, ctx, store, cameras, path, backend
}

func cameraName(t *testing.T, cameras *viewer.CameraService, ctx context.Context, id string) string {
	t.Helper()
	list, err := cameras.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list.Cameras {
		if string(c.ID) == id {
			return c.Name
		}
	}
	return ""
}

func TestRenameCameraKeepsPlaybackAndReplays(t *testing.T) {
	s, ctx, store, cameras, _ := editFixture(t)
	saved := savedCamera(t, s, ctx, draftRequest(1), 2, 1)
	offer := api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}
	session, err := cameras.CreateSession(ctx, cs.CameraID(saved.CameraID), "default", api.CreateSessionRequest{Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	rename := api.RenameCameraRequest{Mutation: mutation(10), Name: "Front door", CatalogRevision: cameras.CatalogRevision()}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			got, err := s.RenameCamera(ctx, saved.CameraID, rename)
			if api.ManagementPublicError(err).Error.Code == string(api.CodeOperationInProgress) {
				return
			}
			if err != nil || got.CameraID != saved.CameraID || got.CatalogRevision.Generation != 3 || got.Removed {
				t.Error("rename", got, err)
			}
		})
	}
	wg.Wait()
	if name := cameraName(t, cameras, ctx, saved.CameraID); name != "Front door" {
		t.Fatal("catalog name", name)
	}
	if state, err := store.Load(ctx); err != nil || state.Revision != 2 {
		t.Fatal("rename should commit exactly once", err)
	}
	if err = cameras.CloseSession(ctx, session.ID); err != nil {
		t.Fatal("rename interrupted playback", err)
	}
	if got, err := s.RenameCamera(ctx, saved.CameraID, rename); err != nil || got.CatalogRevision.Generation != 3 {
		t.Fatal("replay", got, err)
	}
	op, err := s.Operation(ctx, rename.OperationKey, saved.CameraID, "epoch", api.OperationLookup{Kind: api.OperationRename})
	if err != nil || op.State != api.OperationSucceeded || op.Kind != api.OperationRename || op.Change == nil || op.Change.CameraID != saved.CameraID || op.Saved != nil {
		t.Fatal("rename lookup", op, err)
	}
	if op, err = s.Operation(viewer.WithPrincipal(ctx, "other"), rename.OperationKey, saved.CameraID, "epoch"); err != nil || op.State != api.OperationNotFound || op.Change != nil {
		t.Fatal("cross-owner rename lookup", op, err)
	}
	conflicting := rename
	conflicting.Name = "Back door"
	if _, err = s.RenameCamera(ctx, saved.CameraID, conflicting); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationConflict) {
		t.Fatal("changed rename intent", err)
	}
	save := api.SaveCameraRequest{Mutation: rename.Mutation, DraftID: "draft", ExpectedRevision: 1, CatalogRevision: cameras.CatalogRevision()}
	if _, err = s.SaveCamera(ctx, save); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationConflict) {
		t.Fatal("save reused a rename key", err)
	}
	creation := api.RenameCameraRequest{Mutation: mutation(4), Name: "Reused", CatalogRevision: cameras.CatalogRevision()}
	if _, err = s.RenameCamera(ctx, saved.CameraID, creation); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationConflict) {
		t.Fatal("rename reused the creation key", err)
	}

	stale := api.RenameCameraRequest{Mutation: mutation(20), Name: "Stale", CatalogRevision: api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 2}}
	if _, err = s.RenameCamera(ctx, saved.CameraID, stale); api.ManagementPublicError(err).Error.Code != string(api.CodeCatalogConflict) {
		t.Fatal("stale revision", err)
	}
	for i, name := range []string{"bad\x00name", strings.Repeat("n", rtsp.MaxLabelBytes+1), "\xff"} {
		bad := api.RenameCameraRequest{Mutation: mutation(30 + i), Name: name, CatalogRevision: cameras.CatalogRevision()}
		if _, err = s.RenameCamera(ctx, saved.CameraID, bad); cs.DescribeError(err).Code != cs.CodeInvalidArgument {
			t.Fatal("invalid name accepted", i, err)
		}
	}
	for name, actor := range map[string]context.Context{"another manager": viewer.WithPrincipal(ctx, "other"), "not a manager": viewer.WithPrincipal(ctx, "viewer")} {
		denied := api.RenameCameraRequest{Mutation: mutation(40), Name: "Taken", CatalogRevision: cameras.CatalogRevision()}
		if _, err = s.RenameCamera(actor, saved.CameraID, denied); cs.DescribeError(err).Code != cs.CodeAccessDenied {
			t.Fatal(name, "rename", err)
		}
	}
	same := api.RenameCameraRequest{Mutation: mutation(41), Name: "Front door", CatalogRevision: cameras.CatalogRevision()}
	if got, err := s.RenameCamera(ctx, saved.CameraID, same); err != nil || got.CatalogRevision.Generation != 3 {
		t.Fatal("unchanged name should not commit", got, err)
	}
}

func TestUncertainRenameRecoversOnLookupAndRestart(t *testing.T) {
	s, ctx, store, cameras, path := editFixture(t)
	saved := savedCamera(t, s, ctx, draftRequest(1), 2, 1)
	other := savedCamera(t, s, ctx, func() api.CreateDraftRequest {
		r := draftRequest(10)
		r.Endpoint = "rtsp://localhost:18557/second"
		return r
	}(), 11, 2)
	rename := api.RenameCameraRequest{Mutation: mutation(20), Name: "Porch", CatalogRevision: cameras.CatalogRevision()}
	restore := viewer.FailManagedSyncForTest(store)
	if _, err := s.RenameCamera(ctx, saved.CameraID, rename); api.ManagementPublicError(err).Error.Code != string(api.CodeSaveUnknown) {
		t.Fatal("rename acknowledged before durability", err)
	}
	lookup := api.OperationLookup{Kind: api.OperationRename}
	if op, err := s.Operation(ctx, rename.OperationKey, saved.CameraID, "epoch", lookup); err != nil || op.State != api.OperationUnknown || op.Kind != api.OperationRename {
		t.Fatal("uncertain rename lookup", op, err)
	}
	blocked := api.RenameCameraRequest{Mutation: mutation(21), Name: "Garage", CatalogRevision: cameras.CatalogRevision()}
	if _, err := s.RenameCamera(ctx, other.CameraID, blocked); api.ManagementPublicError(err).Error.Code != string(api.CodeOperationInProgress) {
		t.Fatal("a second change committed beside an uncertain one", err)
	}
	if name := cameraName(t, cameras, ctx, saved.CameraID); name != "Camera" {
		t.Fatal("uncertain rename was published", name)
	}
	restore()
	op, err := s.Operation(ctx, rename.OperationKey, saved.CameraID, "epoch", lookup)
	if err != nil || op.State != api.OperationSucceeded || op.Change == nil || op.Change.CatalogRevision.Generation != 4 {
		t.Fatal("rename recovery", op, err)
	}
	if name := cameraName(t, cameras, ctx, saved.CameraID); name != "Porch" {
		t.Fatal("recovered rename not published", name)
	}
	if got, err := s.RenameCamera(ctx, saved.CameraID, rename); err != nil || got.CatalogRevision.Generation != 4 {
		t.Fatal("replay after recovery", got, err)
	}

	// A rename whose barrier fails before shutdown is selected from disk at startup.
	late := api.RenameCameraRequest{Mutation: mutation(30), Name: "Shed", CatalogRevision: cameras.CatalogRevision()}
	restore = viewer.FailManagedSyncForTest(store)
	if _, err = s.RenameCamera(ctx, other.CameraID, late); api.ManagementPublicError(err).Error.Code != string(api.CodeSaveUnknown) {
		t.Fatal("second uncertain rename", err)
	}
	restore()
	_ = store.Close()
	reopened, err := viewer.OpenPrivateState(context.Background(), path, "installation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	state, err := reopened.Load(ctx)
	if err != nil || state.Revision != 4 {
		t.Fatal("startup did not select the renamed state", state.Revision, err)
	}
	var restarted *viewer.CameraService
	fresh, actor, _ := managementFixture(t, func(o *viewer.ManagementOptions) { o.Epoch, o.Repository, restarted = "restarted", reopened, o.Viewer })
	if err = fresh.InitializeManagedState(actor, state); err != nil {
		t.Fatal(err)
	}
	if cameraName(t, restarted, actor, saved.CameraID) != "Porch" || cameraName(t, restarted, actor, other.CameraID) != "Shed" {
		t.Fatal("renames lost across restart")
	}
	for _, r := range []api.RenameCameraRequest{rename, late} {
		camera := saved.CameraID
		if r.OperationKey == late.OperationKey {
			camera = other.CameraID
		}
		op, err := fresh.Operation(actor, r.OperationKey, camera, "epoch", lookup)
		if err != nil || op.State != api.OperationSucceeded || op.Change == nil || op.Change.CatalogRevision.RuntimeEpoch != "restarted" {
			t.Fatal("restart lookup", op, err)
		}
		if got, err := fresh.RenameCamera(actor, camera, r); err != nil || got.CameraID != camera {
			t.Fatal("restart replay", got, err)
		}
	}

	if removed, err := viewer.PrepareManagedRollback(ctx, reopened); err != nil || removed != 2 {
		t.Fatal("rollback should drop both rename receipts", removed, err)
	}
	pruned, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(pruned.Payload)
	if strings.Contains(string(pruned.Payload), `"kind"`) {
		t.Fatal("rollback left an editing receipt")
	}
	afterRollback, actor, _ := managementFixture(t, func(o *viewer.ManagementOptions) {
		o.Epoch, o.Repository, restarted = "rolled-back", reopened, o.Viewer
	})
	if err = afterRollback.InitializeManagedState(actor, pruned); err != nil {
		t.Fatal(err)
	}
	if cameraName(t, restarted, actor, saved.CameraID) != "Porch" {
		t.Fatal("rollback lost the saved name")
	}
	if op, err := afterRollback.Operation(actor, rename.OperationKey, saved.CameraID, "epoch", lookup); err != nil || op.State != api.OperationExpired {
		t.Fatal("dropped rename receipt", op, err)
	}
}

func TestManagedCatalogRejectsMalformedChangeReceipts(t *testing.T) {
	s, ctx, store, cameras, _ := editFixture(t)
	saved := savedCamera(t, s, ctx, draftRequest(1), 2, 1)
	if _, err := s.RenameCamera(ctx, saved.CameraID, api.RenameCameraRequest{Mutation: mutation(10), Name: "Porch", CatalogRevision: cameras.CatalogRevision()}); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(state.Payload)
	for name, edit := range map[string]func(string) string{
		"unknown kind": func(p string) string { return strings.Replace(p, `"kind":"rename"`, `"kind":"wipe"`, 1) },
		"creation key": func(p string) string {
			return strings.Replace(p, fmt.Sprintf(`"operation_key":"%032x"`, 10), fmt.Sprintf(`"operation_key":"%032x"`, 4), 1)
		},
		"missing camera": func(p string) string {
			return strings.Replace(p, `"candidate_id":"`+saved.CameraID+`","kind"`, `"candidate_id":"missing","kind"`, 1)
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
}

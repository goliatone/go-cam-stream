package viewer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	httptransport "github.com/goliatone/go-cam-stream/internal/transport/http"
	"github.com/goliatone/go-cam-stream/internal/viewer"
)

func TestManagementRegisteredHTTPWorkflow(t *testing.T) {
	var cameras *viewer.CameraService
	s, _, _ := saveFixture(t, func(o *viewer.ManagementOptions) { cameras = o.Viewer })
	recovery, err := viewer.NewCameraRecoveryService(cameras, "epoch", func(ctx context.Context, ref api.StreamRef) (api.ReconnectResponse, error) {
		return (viewer.ReconnectHandler{Service: cameras}).Run(ctx, viewer.Reconnect{CameraID: ref.CameraID, StreamID: ref.StreamID})
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := admin.NewCommandBus(true)
	defer bus.Close()
	registration, err := viewer.RegisterManagement(bus, s, recovery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registration.Close() }()
	cfg, err := config.Load(context.Background(), "../../config/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.InstallationID = "installation"
	cfg.Origin = "https://camera.test"
	handler, err := httptransport.New(context.Background(), cfg, cameras, bus, func() bool { return true }, nil, httptransport.ManagementRoutes{Service: s, Recovery: recovery})
	if err != nil {
		t.Fatal(err)
	}
	token := ""
	login := "manager"
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), method, "https://camera.test"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set(api.ServeLoginHeader, login)
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set(api.CSRFHeader, token)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, r)
		return out
	}
	csrf := request("GET", api.CSRFPath, "")
	var bootstrap api.CSRFResponse
	if csrf.Code != 200 || json.Unmarshal(csrf.Body.Bytes(), &bootstrap) != nil {
		t.Fatal("csrf", csrf.Code)
	}
	input := draftRequest(1)
	input.Authentication = api.AuthenticationCredentials
	input.Credentials = &api.Credentials{Username: "camera", Password: "PRIVATE-COMMAND-PASSWORD"}
	body, _ := json.Marshal(input)
	if out := request("POST", api.DraftsPath, string(body)); out.Code != 403 {
		t.Fatal("missing csrf accepted", out.Code)
	}
	token = bootstrap.Token
	out := request("POST", api.DraftsPath, string(body))
	var d api.DraftReview
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &d) != nil {
		t.Fatal("create dispatch", out.Code, out.Body.String())
	}
	if strings.Contains(out.Body.String(), input.Credentials.Password) || strings.Contains(fmt.Sprintf("%#v", viewer.CreateDraft{Request: input}), input.Credentials.Password) {
		t.Fatal("credentials leaked")
	}
	create := api.CreateTestRequest{Mutation: mutation(2), ExpectedRevision: 1, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}}
	body, _ = json.Marshal(create)
	out = request("POST", api.DraftsPath+"/"+d.ID+"/tests", string(body))
	var preview api.TestResult
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &preview) != nil {
		t.Fatal("test dispatch", out.Code, out.Body.String())
	}
	complete := api.CompleteTestRequest{Mutation: mutation(3), ExpectedRevision: 1, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}}
	body, _ = json.Marshal(complete)
	out = request("POST", api.DraftsPath+"/"+d.ID+"/tests/"+preview.ID+"/complete", string(body))
	if out.Code != 200 {
		t.Fatal("complete dispatch", out.Code, out.Body.String())
	}
	save := api.SaveCameraRequest{Mutation: mutation(4), DraftID: d.ID, ExpectedRevision: 1, CatalogRevision: api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 1}}
	body, _ = json.Marshal(save)
	out = request("POST", "/api/cameras", string(body))
	var saved api.SaveCameraResponse
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &saved) != nil || saved.CameraID != d.CandidateID {
		t.Fatal("save dispatch", out.Code, out.Body.String())
	}
	out = request("GET", api.OperationsPath+"/"+save.OperationKey+"?runtime_epoch=epoch", "")
	if out.Code != 200 || !strings.Contains(out.Body.String(), saved.CameraID) {
		t.Fatal("save recovery", out.Code)
	}
	body, _ = json.Marshal(mutation(5))
	out = request("POST", "/api/cameras/"+saved.CameraID+"/streams/default/recovery", string(body))
	if out.Code != 200 {
		t.Fatal("reconnect dispatch", out.Code, out.Body.String())
	}
	out = request("GET", api.OperationsPath+"/"+mutation(5).OperationKey+"?runtime_epoch=epoch&kind=recovery", "")
	if out.Code != 200 || !strings.Contains(out.Body.String(), "succeeded") {
		t.Fatal("reconnect lookup", out.Code, out.Body.String())
	}
	// A missing or unauthorized target stays a safe denial through command wrappers.
	out = request("POST", "/api/cameras/missing/streams/default/recovery", string(body))
	if out.Code != 403 || !strings.Contains(out.Body.String(), "access_denied") {
		t.Fatal("recovery denial projection", out.Code, out.Body.String())
	}
	// Safe classified command errors cross the registered runtime unchanged.
	save.Mutation = mutation(6)
	save.CatalogRevision.Generation = 99
	body, _ = json.Marshal(save)
	out = request("POST", "/api/cameras", string(body))
	if out.Code != 409 || !strings.Contains(out.Body.String(), string(api.CodeCatalogConflict)) {
		t.Fatal("management error projection", out.Code, out.Body.String())
	}
	if out = request("PATCH", api.DraftsPath+"/"+d.ID, `{"credentials":null}`); out.Code != 400 {
		t.Fatal("null credential retention accepted", out.Code)
	}
	duplicate := draftRequest(7)
	body, _ = json.Marshal(duplicate)
	out = request("POST", api.DraftsPath, string(body))
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &d) != nil {
		t.Fatal("duplicate review", out.Code)
	}
	create.Mutation = mutation(8)
	body, _ = json.Marshal(create)
	out = request("POST", api.DraftsPath+"/"+d.ID+"/tests", string(body))
	var conflict api.ManagementErrorResponse
	if out.Code != 409 || json.Unmarshal(out.Body.Bytes(), &conflict) != nil ||
		conflict.Error.Code != string(api.CodeDuplicateCamera) || conflict.Error.CameraID != saved.CameraID {
		t.Fatal("authorized duplicate target lost through command dispatch", out.Code, out.Body.String())
	}

	replace := api.CredentialDraftRequest{Mutation: mutation(9), CameraID: saved.CameraID, Credentials: api.Credentials{Password: "REPLACEMENT-PRIVATE-PASSWORD"}}
	body, _ = json.Marshal(replace)
	token = ""
	if out = request("POST", api.DraftsPath+"/credentials", string(body)); out.Code != 403 {
		t.Fatal("missing CSRF accepted for credential update", out.Code)
	}
	token = bootstrap.Token
	out = request("POST", api.DraftsPath+"/credentials", string(body))
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &d) != nil || !d.CredentialUpdate || d.CandidateID != saved.CameraID ||
		strings.Contains(out.Body.String(), replace.Credentials.Password) || strings.Contains(out.Body.String(), duplicate.Endpoint) {
		t.Fatal("credential draft did not return a safe review", out.Code)
	}
	stop, _ := json.Marshal(api.StopViewersRequest{Mutation: mutation(20), ExpectedRevision: d.Revision})
	token = ""
	if out = request("POST", api.DraftsPath+"/"+d.ID+"/stop-viewers", string(stop)); out.Code != 403 {
		t.Fatal("missing CSRF accepted for stop-viewers", out.Code)
	}
	token = bootstrap.Token
	var fenced api.DraftReview
	if out = request("POST", api.DraftsPath+"/"+d.ID+"/stop-viewers", string(stop)); out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &fenced) != nil || !fenced.Fenced {
		t.Fatal("stop-viewers", out.Code, out.Body.String())
	}
	cancel, _ := json.Marshal(mutation(21))
	if out = request("DELETE", api.DraftsPath+"/"+d.ID, string(cancel)); out.Code != 200 || strings.Contains(out.Body.String(), `"fenced"`) {
		t.Fatal("canceling the fenced draft", out.Code, out.Body.String())
	}
	out = request("GET", "/api/cameras/"+saved.CameraID+"/settings", "")
	var settings api.CameraSettings
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &settings) != nil || settings.CameraID != saved.CameraID ||
		settings.Endpoint != "rtsp://localhost:18556/macbook" || settings.Authentication != api.AuthenticationCredentials ||
		strings.Contains(out.Body.String(), "PRIVATE") || strings.Contains(out.Body.String(), "username") {
		t.Fatal("settings read", out.Code, out.Body.String())
	}
	rename := api.RenameCameraRequest{Mutation: mutation(10), Name: "Porch", CatalogRevision: settings.CatalogRevision}
	body, _ = json.Marshal(rename)
	token = ""
	if out = request("PATCH", "/api/cameras/"+saved.CameraID, string(body)); out.Code != 403 {
		t.Fatal("missing CSRF accepted for rename", out.Code)
	}
	token = bootstrap.Token
	if out = request("PATCH", "/api/cameras/"+saved.CameraID, `{"name":"Porch","unknown":true}`); out.Code != 400 {
		t.Fatal("rename accepted an unknown field", out.Code)
	}
	out = request("PATCH", "/api/cameras/"+saved.CameraID, string(body))
	var change api.CameraChange
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &change) != nil || change.CameraID != saved.CameraID || change.CatalogRevision.Generation != settings.CatalogRevision.Generation+1 {
		t.Fatal("rename", out.Code, out.Body.String())
	}
	out = request("GET", api.OperationsPath+"/"+rename.OperationKey+"?runtime_epoch=epoch&kind=rename&candidate_id="+saved.CameraID, "")
	var op api.OperationResult
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &op) != nil || op.State != api.OperationSucceeded || op.Change == nil || op.Kind != api.OperationRename {
		t.Fatal("rename lookup", out.Code, out.Body.String())
	}
	stale, _ := json.Marshal(api.RenameCameraRequest{Mutation: mutation(11), Name: "Stale", CatalogRevision: settings.CatalogRevision})
	if out = request("PATCH", "/api/cameras/"+saved.CameraID, string(stale)); out.Code != 409 || !strings.Contains(out.Body.String(), string(api.CodeCatalogConflict)) {
		t.Fatal("stale rename", out.Code, out.Body.String())
	}
	login = "other"
	if out = request("GET", "/api/cameras/"+saved.CameraID+"/settings", ""); out.Code != 403 || strings.Contains(out.Body.String(), "18556") {
		t.Fatal("another manager read the settings", out.Code, out.Body.String())
	}
	other, _ := json.Marshal(api.RenameCameraRequest{Mutation: mutation(12), Name: "Taken", CatalogRevision: change.CatalogRevision})
	if out = request("PATCH", "/api/cameras/"+saved.CameraID, string(other)); out.Code != 403 {
		t.Fatal("another manager renamed the camera", out.Code, out.Body.String())
	}
	remove, _ := json.Marshal(api.RemoveCameraRequest{Mutation: mutation(13), CatalogRevision: change.CatalogRevision, StopViewers: true})
	if out = request("DELETE", "/api/cameras/"+saved.CameraID, string(remove)); out.Code != 403 {
		t.Fatal("another manager removed the camera", out.Code, out.Body.String())
	}
	login = "manager"
	token = ""
	if out = request("DELETE", "/api/cameras/"+saved.CameraID, string(remove)); out.Code != 403 {
		t.Fatal("missing CSRF accepted for removal", out.Code)
	}
	token = bootstrap.Token
	out = request("DELETE", "/api/cameras/"+saved.CameraID, string(remove))
	var removed api.CameraChange
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &removed) != nil || !removed.Removed || removed.CameraID != saved.CameraID {
		t.Fatal("removal", out.Code, out.Body.String())
	}
	if out = request("GET", "/api/cameras/"+saved.CameraID+"/settings", ""); out.Code != 403 {
		t.Fatal("settings for a removed camera", out.Code, out.Body.String())
	}
}

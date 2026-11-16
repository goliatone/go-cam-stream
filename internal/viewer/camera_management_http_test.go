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
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), method, "https://camera.test"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set(api.ServeLoginHeader, "manager")
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
}

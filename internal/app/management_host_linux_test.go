//go:build linux

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	pion "github.com/pion/webrtc/v4"
)

// The built Linux host uses its production encoder, storage, commands and ingress.
// RTP is transport evidence; decoded browser evidence is verified separately.
func TestManagementHost(t *testing.T) {
	if os.Getenv("CAMSTREAM_RTSP_T05") != "1" {
		t.Skip("isolated controlled-media fixture")
	}
	h := startRTSPHost(t, false, func(cfg *config.Config) {
		cfg.Management = config.Management{Enabled: true, Principals: []string{"owner"}}
		// Leave the authenticated source unconfigured for onboarding. The other
		// anonymous source remains active throughout Save.
		cfg.Cameras = cfg.Cameras[1:2]
		cfg.Access = []config.Access{
			{Login: "owner", Grants: []config.Grant{{CameraID: "other", Streams: []cs.StreamID{"main"}, View: true}}},
			{Login: "viewer", Grants: []config.Grant{{CameraID: "other", Streams: []cs.StreamID{"main"}, View: true}}},
		}
	})
	first := h.peer(t, "other", "main", "owner", 201, "")
	second := h.peer(t, "other", "main", "viewer", 201, "")
	eventually(t, func() bool { return first.packets.Load() > 10 && second.packets.Load() > 10 })
	firstBefore, secondBefore, pid := first.packets.Load(), second.packets.Load(), os.Getpid()
	call := func(method, path string, input, output any) int {
		body := ""
		if input != nil {
			data, e := json.Marshal(input)
			if e != nil {
				t.Fatal(e)
			}
			body = string(data)
		}
		status, data := h.request(t, "owner", method, path, body)
		if output != nil && status == 200 {
			if e := json.Unmarshal(data, output); e != nil {
				t.Fatal(e)
			}
		}
		return status
	}
	var caps api.Capabilities
	if call("GET", api.CapabilitiesPath, nil, &caps) != 200 || !caps.CanManageCameras {
		t.Fatal("manager capabilities")
	}
	seq := 0
	mutation := func() api.Mutation {
		seq++
		return api.Mutation{OperationKey: fmt.Sprintf("%032x", seq), RuntimeEpoch: caps.RuntimeEpoch}
	}
	var draft api.DraftReview
	input := api.CreateDraftRequest{Mutation: mutation(), DraftInput: api.DraftInput{Name: "Managed authenticated camera", Endpoint: "rtsp://localhost:34001/t05-main-private", Authentication: api.AuthenticationCredentials, Credentials: &api.Credentials{Username: "t05-user", Password: "t05-fixture-password"}}}
	if call("POST", api.DraftsPath, input, &draft) != 200 {
		t.Fatal("create")
	}
	peer, offer := managementPeer(t)
	var preview api.TestResult
	if call("POST", api.DraftsPath+"/"+draft.ID+"/tests", api.CreateTestRequest{Mutation: mutation(), ExpectedRevision: draft.Revision, Offer: offer}, &preview) != 200 || preview.Answer == nil {
		t.Fatal("test")
	}
	if e := peer.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: preview.Answer.SDP}); e != nil {
		t.Fatal(e)
	}
	eventually(t, func() bool { return peer.packets.Load() > 10 })
	eventually(t, func() bool {
		var status api.TestResult
		return call("GET", api.DraftsPath+"/"+draft.ID+"/tests/"+preview.ID, nil, &status) == 200 && status.SourceMediaObserved
	})
	var passed api.TestResult
	if call("POST", api.DraftsPath+"/"+draft.ID+"/tests/"+preview.ID+"/complete", api.CompleteTestRequest{Mutation: mutation(), ExpectedRevision: draft.Revision, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}}, &passed) != 200 || passed.Stage != api.TestPassed {
		t.Fatal("complete")
	}
	if e := peer.pc.Close(); e != nil {
		t.Fatal(e)
	}
	var catalog api.CameraList
	if call("GET", "/api/cameras", nil, &catalog) != 200 {
		t.Fatal("catalog")
	}
	request := api.SaveCameraRequest{Mutation: mutation(), DraftID: draft.ID, ExpectedRevision: draft.Revision, CatalogRevision: *catalog.Revision}
	var saved api.SaveCameraResponse
	if call("POST", "/api/cameras", request, &saved) != 200 {
		t.Fatal("save")
	}
	if call("POST", "/api/cameras", request, &saved) != 200 {
		t.Fatal("save replay")
	}
	if os.Getpid() != pid {
		t.Fatal("host replaced during save")
	}
	eventually(t, func() bool { return first.packets.Load() > firstBefore+10 && second.packets.Load() > secondBefore+10 })
	var op api.OperationResult
	if call("GET", api.OperationsPath+"/"+request.OperationKey+"?runtime_epoch="+caps.RuntimeEpoch, nil, &op) != 200 || op.State != api.OperationSucceeded {
		t.Fatal("save recovery")
	}
	_, privateList := h.request(t, "viewer", "GET", "/api/cameras", "")
	var filtered api.CameraList
	if json.Unmarshal(privateList, &filtered) != nil || len(filtered.Cameras) != 1 {
		t.Fatal("creator grant leaked")
	}
	watched := h.peer(t, saved.CameraID, "default", "owner", 201, "")
	eventually(t, func() bool { return watched.packets.Load() > 10 })
	cfg := h.c.cfg
	h.stop()
	for _, p := range []*mediaPeer{first, second, watched} {
		if e := p.pc.Close(); e != nil {
			t.Fatal(e)
		}
	}
	cfg.Management.Enabled = false
	fresh, err := NewDefault(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fresh.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	eventually(t, fresh.ready.Load)
	if call("GET", api.CapabilitiesPath, nil, &caps) != 200 || caps.CanManageCameras {
		t.Fatal("disabled manager")
	}
	if call("GET", api.OperationsPath+"/"+request.OperationKey+"?runtime_epoch="+request.RuntimeEpoch, nil, &op) != 200 || op.State != api.OperationSucceeded {
		t.Fatal("restart save anchor")
	}
	restored := h.peer(t, saved.CameraID, "default", "owner", 201, "")
	eventually(t, func() bool { return restored.packets.Load() > 10 })
	// Existing reconnect command registration is used, with persisted creator grant.
	var recovery api.ReconnectResponse
	if call("POST", "/api/cameras"+"/"+saved.CameraID+"/streams/default/recovery", mutation(), &recovery) != 200 {
		t.Fatal("disabled-management reconnect")
	}
	t.Log("Production Save preserved two RTP viewers and PID; duplicate receipt, creator isolation, persistent credentials and disabled-management playback/reconnect passed.")
}

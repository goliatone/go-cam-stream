//go:build linux

package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	encoder "github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/rtsp"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	pion "github.com/pion/webrtc/v4"
)

// TestManagementProbes uses the maintained isolated Linux origins. RTP packets
// establish real upstream/WebRTC media. Completion evidence is an API harness
// report; decoded browser frames remain the separate T09 integration gate.
func TestManagementProbes(t *testing.T) {
	if os.Getenv("CAMSTREAM_RTSP_T05") != "1" {
		t.Skip("isolated controlled-media fixture")
	}
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.Backend.RuntimeDir = filepath.Join(t.TempDir(), "workers")
	cfg.Backend.MediaAddress = "127.0.0.1"
	cfg.Backend.CandidateAddress = "127.0.0.1"
	secrets := viewer.NewManagedSecrets(nil)
	defer secrets.Close()
	backend, err := go2rtc.New(cfg.BackendConfig(go2rtc.ContainerBoundary), encoder.NewEncoder(secrets))
	if err != nil {
		t.Fatal(err)
	}
	if err = backend.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := backend.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	core, err := cs.NewService(backend, cfg.ServiceOptions(), rtsp.Strategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := core.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	catalog, err := viewer.NewCameraService(core, config.Catalog{}, cfg.OperationTimeout)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := viewer.NewManagementPolicy(config.Management{Enabled: true, Principals: []string{"manager"}}, "management-fixture")
	if err != nil {
		t.Fatal(err)
	}
	service, err := viewer.NewCameraManagementService(viewer.ManagementOptions{Core: core, Viewer: catalog, Policy: policy, Secrets: secrets, Epoch: "fixture", CleanupTimeout: cfg.Limits.CleanupTimeout, MaxSources: cfg.Limits.MaxSources})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := service.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	actor := viewer.WithPrincipal(ctx, "manager")
	sequence := 0
	mutation := func() api.Mutation {
		sequence++
		return api.Mutation{OperationKey: fmt.Sprintf("%032x", sequence), RuntimeEpoch: "fixture"}
	}
	for _, authenticated := range []bool{false, true} {
		input := api.DraftInput{Name: "Controlled source", Endpoint: "rtsp://localhost:34003/t05-other-private", Authentication: api.AuthenticationAnonymous}
		if authenticated {
			input.Endpoint = "rtsp://localhost:34001/t05-main-private"
			input.Authentication = api.AuthenticationCredentials
			input.Credentials = &api.Credentials{Username: "t05-user", Password: "t05-fixture-password"}
		}
		draft, err := service.CreateDraft(actor, api.CreateDraftRequest{Mutation: mutation(), DraftInput: input})
		if err != nil {
			t.Fatal(err)
		}
		peer, offer := managementPeer(t)
		preview, err := service.CreateTest(actor, draft.ID, api.CreateTestRequest{Mutation: mutation(), ExpectedRevision: draft.Revision, Offer: offer})
		if err != nil || preview.Answer == nil {
			t.Fatal("preview negotiation failed", err, preview.Stage)
		}
		if err = peer.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: preview.Answer.SDP}); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return peer.packets.Load() > 10 })
		eventually(t, func() bool {
			status, e := service.Test(actor, draft.ID, preview.ID)
			return e == nil && status.SourceMediaObserved
		})
		completion := api.CompleteTestRequest{Mutation: mutation(), ExpectedRevision: draft.Revision, Evidence: api.FrameEvidence{Method: "frames_decoded", Frames: 3, Width: 640, Height: 360}}
		passed, err := service.CompleteTest(actor, draft.ID, preview.ID, completion)
		if err != nil || passed.Stage != api.TestPassed || passed.Cleanup != api.CleanupConfirmed {
			t.Fatal("preview completion", err)
		}
		replay, err := service.CompleteTest(actor, draft.ID, preview.ID, completion)
		if err != nil || replay.ID != passed.ID {
			t.Fatal("completion replay", err)
		}
		if _, err = service.CancelDraft(actor, draft.ID, mutation()); err != nil {
			t.Fatal(err)
		}
		if err = peer.pc.Close(); err != nil {
			t.Fatal(err)
		}
	}

	for _, input := range []api.DraftInput{
		{Name: "Wrong password", Endpoint: "rtsp://localhost:34001/t05-main-private", Authentication: api.AuthenticationCredentials, Credentials: &api.Credentials{Username: "t05-user", Password: "deliberately-wrong"}},
		{Name: "Unsupported MPEG4 video", Endpoint: "rtsp://localhost:34003/t05-unsupported-private", Authentication: api.AuthenticationAnonymous},
		{Name: "Unavailable path", Endpoint: "rtsp://localhost:34003/does-not-exist", Authentication: api.AuthenticationAnonymous},
	} {
		draft, createErr := service.CreateDraft(actor, api.CreateDraftRequest{Mutation: mutation(), DraftInput: input})
		if createErr != nil {
			t.Fatal(createErr)
		}
		peer, offer := managementPeer(t)
		preview, testErr := service.CreateTest(actor, draft.ID, api.CreateTestRequest{Mutation: mutation(), ExpectedRevision: draft.Revision, Offer: offer})
		if testErr != nil {
			t.Fatal("failure left uncertain cleanup", testErr)
		}
		if preview.Answer != nil {
			if answerErr := peer.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: preview.Answer.SDP}); answerErr != nil {
				t.Fatal(answerErr)
			}
		}
		deadline := time.Now().Add(22 * time.Second)
		for preview.Cleanup != api.CleanupConfirmed && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			preview, testErr = service.Test(actor, draft.ID, preview.ID)
			if testErr != nil {
				t.Fatal("failure cleanup", testErr)
			}
		}
		if preview.Stage != api.TestFailed || preview.Error == nil || preview.Cleanup != api.CleanupConfirmed {
			t.Fatal("unavailable source did not fail safely", preview.Stage)
		}
		if _, cancelErr := service.CancelDraft(actor, draft.ID, mutation()); cancelErr != nil {
			t.Fatal(cancelErr)
		}
		if closeErr := peer.pc.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	// Explicit cancellation has a distinct terminal outcome on the same backend.
	cancelDraft, cancelCreateErr := service.CreateDraft(actor, api.CreateDraftRequest{Mutation: mutation(), DraftInput: api.DraftInput{Name: "Canceled source", Endpoint: "rtsp://localhost:34003/t05-other-private", Authentication: api.AuthenticationAnonymous}})
	if cancelCreateErr != nil {
		t.Fatal(cancelCreateErr)
	}
	cancelPeer, cancelOffer := managementPeer(t)
	cancelPreview, cancelTestErr := service.CreateTest(actor, cancelDraft.ID, api.CreateTestRequest{Mutation: mutation(), ExpectedRevision: cancelDraft.Revision, Offer: cancelOffer})
	if cancelTestErr != nil {
		t.Fatal(cancelTestErr)
	}
	cancelMutation := mutation()
	for range 2 {
		canceled, cancelErr := service.CancelTest(actor, cancelDraft.ID, cancelPreview.ID, cancelMutation)
		if cancelErr != nil || canceled.Stage != api.TestCanceled || canceled.Error != nil || canceled.PassedAt != nil || canceled.Cleanup != api.CleanupConfirmed {
			t.Fatal("explicit cancel/replay outcome", canceled.Stage, cancelErr)
		}
	}
	cancelStatus, cancelStatusErr := service.Operation(actor, cancelMutation.OperationKey, "", "fixture")
	if cancelStatusErr != nil || cancelStatus.State != api.OperationSucceeded || cancelStatus.DraftID != cancelDraft.ID || cancelStatus.TestID != cancelPreview.ID || cancelStatus.Revision != cancelDraft.Revision {
		t.Fatal("cancel operation identity", cancelStatusErr)
	}
	if _, cancelErr := service.CancelDraft(actor, cancelDraft.ID, mutation()); cancelErr != nil {
		t.Fatal(cancelErr)
	}
	if closeErr := cancelPeer.pc.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	t.Log("Anonymous/authenticated numeric-pinned production RTSP previews received H264 RTP; completion replay, failed-source deadlines, explicit cancellation/replay and owned cleanup passed. No browser decode or redirect-containment claim.")
}
func managementPeer(t *testing.T) (*mediaPeer, api.SDPOffer) {
	t.Helper()
	engine := pion.SettingEngine{}
	engine.SetIncludeLoopbackCandidate(true)
	engine.SetNetworkTypes([]pion.NetworkType{pion.NetworkTypeTCP4})
	engine.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	pc, err := pion.NewAPI(pion.WithSettingEngine(engine)).NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	peer := &mediaPeer{pc: pc}
	t.Cleanup(func() {
		if closeErr := pc.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	pc.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
		if track.Codec().MimeType != pion.MimeTypeH264 {
			t.Error("unexpected codec")
			return
		}
		for {
			packet, _, readErr := track.ReadRTP()
			if readErr != nil {
				return
			}
			if len(packet.Payload) > 0 {
				peer.packets.Add(1)
			}
		}
	})
	if _, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := pion.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
		t.Fatal("ICE gathering deadline")
	}
	return peer, api.SDPOffer{Type: "offer", SDP: pc.LocalDescription().SDP}
}

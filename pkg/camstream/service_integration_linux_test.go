//go:build linux

package camstream_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	pion "github.com/pion/webrtc/v4"
)

type fixtureEncoder struct{}

func (fixtureEncoder) Kind() cs.SourceKind { return (ownedSource("")).Kind() }
func (fixtureEncoder) Encode(ctx context.Context, source cs.Source) (go2rtc.Input, error) {
	if err := ctx.Err(); err != nil {
		return go2rtc.Input{}, err
	}
	v, ok := source.(ownedSource)
	if !ok {
		return go2rtc.Input{}, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return go2rtc.Input{URL: string(v)}, nil
}

type servicePeer struct {
	pc      *pion.PeerConnection
	packets atomic.Uint64
}

func newServicePeer(t *testing.T) (*servicePeer, cs.Offer) {
	t.Helper()
	engine := pion.SettingEngine{}
	engine.SetIncludeLoopbackCandidate(true)
	engine.SetNetworkTypes([]pion.NetworkType{pion.NetworkTypeUDP4})
	engine.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	pc, err := pion.NewAPI(pion.WithSettingEngine(engine)).NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	p := &servicePeer{pc: pc}
	t.Cleanup(func() {
		if closeErr := pc.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	pc.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
		for {
			if _, _, readErr := track.ReadRTP(); readErr != nil {
				return
			}
			p.packets.Add(1)
		}
	})
	if _, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := pion.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("gather timeout")
	}
	return p, cs.Offer{SDP: pc.LocalDescription().SDP}
}
func (p *servicePeer) answer(t *testing.T, state cs.SessionState) {
	t.Helper()
	if state.Answer == nil {
		t.Fatal("missing answer")
	}
	if err := p.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: state.Answer.SDP}); err != nil {
		t.Fatal(err)
	}
}

func TestServicePinnedMedia(t *testing.T) {
	if os.Getenv("CAMSTREAM_SERVICE_INTEGRATION") != "1" {
		t.Skip("requires pinned Linux arm64 --init container and synthetic fixture")
	}
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	b, err := go2rtc.New(go2rtc.Config{Executable: "/usr/local/bin/go2rtc", RuntimeDir: runtimeDir,
		PrivatePorts: go2rtc.PortRange{First: 35000, Last: 35099}, MediaPorts: go2rtc.PortRange{First: 35100, Last: 35199},
		MediaAddress: "127.0.0.1", CandidateAddress: "127.0.0.1", VerifyBoundary: go2rtc.ContainerBoundary, AllowExec: true,
		StartupTimeout: 4 * time.Second, FailureTimeout: 2 * time.Second, CleanupTimeout: 2 * time.Second, TermGrace: 100 * time.Millisecond, PollInterval: 100 * time.Millisecond}, fixtureEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if closeErr := b.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	s, err := cs.NewService(b, cs.ServiceOptions{MaxViewers: 3, PerSourceViewers: 2, PollInterval: 50 * time.Millisecond, CleanupTimeout: 3 * time.Second}, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if closeErr := s.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	camera := testCamera("room1", "one")
	camera.Config = ownedConfig("exec:ffmpeg -hide_banner -loglevel error -re -stream_loop -1 -i /fixture/clip.mkv -c copy -rtsp_transport tcp -f rtsp {output}")
	peers := make([]*servicePeer, 3)
	states := make([]cs.SessionState, 3)
	for i := range peers {
		if i == 1 {
			camera.ID = "hall"
		}
		if i == 2 {
			camera.ID, camera.PhysicalID = "room2", "two"
		}
		peer, offer := newServicePeer(t)
		state, createErr := s.CreateSession(context.Background(), camera, "main", offer)
		if createErr != nil {
			t.Fatal(createErr)
		}
		peer.answer(t, state)
		peers[i], states[i] = peer, state
	}
	eventuallyService(t, func() bool {
		return peers[0].packets.Load() > 5 && peers[1].packets.Load() > 5 && peers[2].packets.Load() > 5
	})
	workers, err := filepath.Glob(filepath.Join(runtimeDir, "generation-*", "worker-*"))
	if err != nil || len(workers) != 5 {
		t.Fatalf("expected C=2,V=3 workers=5; got %d, %v", len(workers), err)
	}
	camera.ID, camera.PhysicalID = "room1", "one"
	_, err = s.CreateSession(context.Background(), camera, "sub", cs.Offer{SDP: "v=0\r\n"})
	code(t, err, cs.CodeCameraBusy)
	if err = s.CloseSession(context.Background(), states[0].ID); err != nil {
		t.Fatal(err)
	}
	a, before := peers[1].packets.Load(), peers[2].packets.Load()
	eventuallyService(t, func() bool { return peers[1].packets.Load() > a+5 && peers[2].packets.Load() > before+5 })
	// No DELETE: peer disappearance must be reclaimed through backend observation.
	if err = peers[1].pc.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, statusErr := s.StreamStatus(context.Background(), camera, "main")
		if statusErr == nil && state.Upstream == cs.PresenceAbsent && state.ActiveSessions != nil && *state.ActiveSessions == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	peer, offer := newServicePeer(t)
	state, err := s.CreateSession(context.Background(), camera, "sub", offer)
	if err != nil {
		t.Fatal("observed final loss did not free device", err)
	}
	peer.answer(t, state)
	eventuallyService(t, func() bool { return peer.packets.Load() > 5 })
	before = peers[2].packets.Load()
	ids, err := s.ResetSource(context.Background(), state.SourceKey)
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	eventuallyService(t, func() bool { return peers[2].packets.Load() > before+5 })
	// An invalid offer after EnsureStream must not strand an idle device slot.
	camera.ID, camera.PhysicalID = "room3", "three"
	_, err = s.CreateSession(context.Background(), camera, "main", cs.Offer{SDP: "invalid"})
	code(t, err, cs.CodeInvalidArgument)
	lastPeer, lastOffer := newServicePeer(t)
	last, err := s.CreateSession(context.Background(), camera, "sub", lastOffer)
	if err != nil {
		t.Fatal("unallocated source rollback retained its device", err)
	}
	lastPeer.answer(t, last)
	eventuallyService(t, func() bool { return lastPeer.packets.Load() > 5 })
	t.Log("C=2/V=3 uses 5 workers; alias sharing, profile rejection, isolated close, observed peer loss, profile reacquisition and operator reset pass")
}

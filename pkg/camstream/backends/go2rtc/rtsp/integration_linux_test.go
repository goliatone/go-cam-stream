//go:build linux

package rtsp_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	encoderpkg "github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/rtsp"
	strategy "github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	pion "github.com/pion/webrtc/v4"
)

func TestPinnedRTSPIngestion(t *testing.T) {
	if os.Getenv("CAMSTREAM_RTSP_INTEGRATION") != "1" {
		t.Skip("requires pinned Linux arm64 executable and clip in an --init container")
	}
	for _, tc := range []struct {
		mode  string
		audio bool
	}{{"anonymous", false}, {"basic", true}, {"digest", true}} {
		t.Run(tc.mode, func(t *testing.T) {
			startOrigin(t)
			proxy := startProxy(t, tc.mode)
			cfg := strategy.Config{Host: "127.0.0.1", Port: 34003, Profiles: []strategy.Profile{{ID: "main", Path: fixturePath, Query: fixtureQuery, Audio: tc.audio}}}
			if tc.mode != "anonymous" {
				cfg.UsernameRef = ref(t, "env://USER")
				cfg.PasswordRef = ref(t, "env://PASS")
			}
			s, err := (strategy.Strategy{}).Resolve(context.Background(), camstream.Camera{ID: "fixture", PhysicalID: "fixture-1", Config: cfg}, "main")
			if err != nil {
				t.Fatal(err)
			}
			b := liveBackend(t, encoderpkg.NewEncoder(values(fixtureUser, fixturePassword, nil)))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err = b.EnsureStream(ctx, s); err != nil {
				t.Fatal(err)
			}
			pc, offer, video, audio := receiver(t)
			session, err := b.CreateSession(ctx, camstream.SessionSpec{ID: "rtsp-fixture", SourceKey: s.Key, Offer: offer})
			if err != nil {
				t.Fatalf("session failed: %v (describes=%d setups=%d plays=%d bad=%d auth=%d)", err, proxy.describes.Load(), proxy.setups.Load(), proxy.plays.Load(), proxy.bad.Load(), proxy.authenticated.Load())
			}
			if session.Answer == nil {
				t.Fatal("missing answer")
			}
			if err = pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: session.Answer.SDP}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return video.Load() > 50 && (!tc.audio || audio.Load() > 10) })
			wantSetups := uint64(1)
			if tc.audio {
				wantSetups = 2
			}
			if proxy.bad.Load() != 0 || proxy.describes.Load() != 1 || proxy.setups.Load() != wantSetups || proxy.plays.Load() != 1 {
				t.Fatalf("unexpected fixture counters: describe=%d setup=%d play=%d bad=%d", proxy.describes.Load(), proxy.setups.Load(), proxy.plays.Load(), proxy.bad.Load())
			}
			if !tc.audio && audio.Load() != 0 {
				t.Fatal("video-only source delivered audio")
			}
			if tc.mode != "anonymous" && (proxy.challenges.Load() == 0 || proxy.authenticated.Load() == 0) {
				t.Fatal("authentication was not exercised")
			}
			if err = b.CloseSession(ctx, "rtsp-fixture"); err != nil {
				t.Fatal(err)
			}
			t.Logf("mode=%s H264_RTP=%d audio_RTP=%d TCP_setups=%d challenges=%d authenticated=%d bad=%d", tc.mode, video.Load(), audio.Load(), proxy.setups.Load(), proxy.challenges.Load(), proxy.authenticated.Load(), proxy.bad.Load())
		})
	}
}

func TestPinnedRTSPRejectsWrongPassword(t *testing.T) {
	if os.Getenv("CAMSTREAM_RTSP_INTEGRATION") != "1" {
		t.Skip("requires pinned Linux arm64 executable and clip in an --init container")
	}
	startOrigin(t)
	proxy := startProxy(t, "digest")
	b := liveBackend(t, encoderpkg.NewEncoder(values(fixtureUser, "wrong-password", nil)))
	s := spec(t, "127.0.0.1", 34003)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := b.EnsureStream(ctx, s); err != nil {
		t.Fatal(err)
	}
	_, offer, video, audio := receiver(t)
	_, err := b.CreateSession(ctx, camstream.SessionSpec{ID: "wrong-password", SourceKey: s.Key, Offer: offer})
	if err == nil || strings.Contains(fmt.Sprintf("%+v", err), "wrong-password") || strings.Contains(fmt.Sprintf("%+v", err), fixtureUser) {
		t.Fatal("bad credentials were accepted or leaked")
	}
	if proxy.bad.Load() == 0 || proxy.plays.Load() != 0 || video.Load() != 0 || audio.Load() != 0 {
		t.Fatal("invalid credentials reached media")
	}
	t.Logf("wrong password rejected: code=%s challenges=%d rejected=%d plays=%d", camstream.DescribeError(err).Code, proxy.challenges.Load(), proxy.bad.Load(), proxy.plays.Load())
}

func liveBackend(t *testing.T, encoder *encoderpkg.Encoder) *go2rtc.Backend {
	t.Helper()
	b, err := go2rtc.New(go2rtc.Config{Executable: "/usr/local/bin/go2rtc", RuntimeDir: filepath.Join(t.TempDir(), "runtime"),
		PrivatePorts: go2rtc.PortRange{First: 35000, Last: 35099}, MediaPorts: go2rtc.PortRange{First: 35100, Last: 35131},
		MediaAddress: "127.0.0.1", CandidateAddress: "127.0.0.1", Network: "tcp", VerifyBoundary: go2rtc.ContainerBoundary,
		StartupTimeout: 5 * time.Second, FailureTimeout: 3 * time.Second, CleanupTimeout: 3 * time.Second, TermGrace: 100 * time.Millisecond, PollInterval: 100 * time.Millisecond}, encoder)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := b.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return b
}

func receiver(t *testing.T) (*pion.PeerConnection, camstream.Offer, *atomic.Uint64, *atomic.Uint64) {
	t.Helper()
	engine := pion.SettingEngine{}
	engine.SetNetworkTypes([]pion.NetworkType{pion.NetworkTypeTCP4})
	engine.SetIncludeLoopbackCandidate(true)
	engine.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	pc, err := pion.NewAPI(pion.WithSettingEngine(engine)).NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	video, audio := new(atomic.Uint64), new(atomic.Uint64)
	pc.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
		counter := audio
		if track.Kind() == pion.RTPCodecTypeVideo {
			if track.Codec().MimeType != pion.MimeTypeH264 {
				t.Error("unexpected video codec")
				return
			}
			counter = video
		} else if track.Codec().MimeType != pion.MimeTypeOpus {
			t.Error("unexpected audio codec")
			return
		}
		for {
			packet, _, readErr := track.ReadRTP()
			if readErr != nil {
				return
			}
			if len(packet.Payload) > 0 {
				counter.Add(1)
			}
		}
	})
	t.Cleanup(func() {
		if closeErr := pc.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	for _, kind := range []pion.RTPCodecType{pion.RTPCodecTypeVideo, pion.RTPCodecTypeAudio} {
		if _, err = pc.AddTransceiverFromKind(kind, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
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
		t.Fatal("ICE gathering deadline")
	}
	return pc, camstream.Offer{SDP: pc.LocalDescription().SDP}, video, audio
}

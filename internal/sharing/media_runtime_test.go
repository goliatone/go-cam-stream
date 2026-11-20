package sharing

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/pion/turn/v5"
	"github.com/pion/webrtc/v4"
)

func fixtureMedia(t *testing.T, tools string) (*MediaRuntime, string) {
	t.Helper()
	m, err := NewMediaRuntime(t.Context(), MediaRuntimeOptions{
		MediaMTX: filepath.Join(tools, "mediamtx"), FFmpeg: filepath.Join(tools, "ffmpeg"), LocalOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	address, err := reserveTCP(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings := mediaDefaults()
	settings["rtsp"], settings["rtspAddress"], settings["rtspTransports"] = true, address, []string{"tcp"}
	settings["paths"] = map[string]any{"fixture": map[string]any{"source": "publisher"}}
	settings["authInternalUsers"] = []mediaAuth{mediaUser("any", "", "publish", "fixture"), mediaUser("any", "", "read", "fixture")}
	dir := t.TempDir()
	run, err := m.startServer(dir, settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.stop)
	for deadline := time.Now().Add(2 * time.Second); ; {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic fixture server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	source := "rtsp://" + address + "/fixture"
	producer, err := runMediaProcess(t.Context(), dir, m.opts.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi",
		"-i", "testsrc2=size=640x360:rate=30", "-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000",
		"-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline", "-preset", "ultrafast", "-tune", "zerolatency",
		"-g", "30", "-bf", "0", "-c:a", "libopus", "-f", "rtsp", "-rtsp_transport", "tcp", source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.stop)
	time.Sleep(450 * time.Millisecond)
	return m, source
}

func connectTestPeer(t *testing.T, m *MediaRuntime, source string, scope ViewerAttempt, host *HostRelay, ice []webrtc.ICEServer, policy webrtc.ICETransportPolicy) (*atomic.Uint64, string) {
	t.Helper()
	var engine webrtc.SettingEngine
	engine.SetIncludeLoopbackCandidate(true)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(engine)).NewPeerConnection(webrtc.Configuration{ICEServers: ice, ICETransportPolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	packets := new(atomic.Uint64)
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			t.Error("nonvideo track")
			return
		}
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
			packets.Add(1)
		}
	})
	answer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 18*time.Second)
	defer cancel()
	select {
	case <-gather:
	case <-ctx.Done():
		t.Fatal("guest ICE gather expired")
	}
	peer, err := m.Open(ctx, MediaOffer{SourceID: source, Attempt: scope, Offer: api.SDPOffer{Type: "offer", SDP: pc.LocalDescription().SDP}, Relay: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background(), peer.ID) })
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: peer.Answer.SDP}); err != nil {
		t.Fatal(err)
	}
	for packets.Load() < 5 && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	if packets.Load() < 5 {
		t.Fatalf("no media packets, peer state %s", pc.ConnectionState())
	}
	return packets, peer.ID
}

func TestSharingMediaRealRuntime(t *testing.T) {
	tools := os.Getenv("CAMSTREAM_TEST_SHARING_TOOLS")
	if tools == "" {
		t.Skip("set CAMSTREAM_TEST_SHARING_TOOLS to the lock-verified runtime tools")
	}
	m, source := fixtureMedia(t, tools)
	live := make(chan ViewerAttempt, 2)
	m.opts.OnLive = func(id ViewerAttempt) { live <- id }
	first := ViewerAttempt{ShareID: handle(), ViewerID: handle(), AttemptID: handle()}
	second := ViewerAttempt{ShareID: first.ShareID, ViewerID: handle(), AttemptID: handle()}
	if _, err := m.Open(t.Context(), MediaOffer{SourceID: source, Attempt: first, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=recvonly\r\n"}}); !errors.Is(err, fault(api.ShareUnsupportedOffer)) {
		t.Fatal("audio signaling was not rejected")
	}
	a, p1 := connectTestPeer(t, m, source, first, nil, nil, webrtc.ICETransportPolicyAll)
	b, _ := connectTestPeer(t, m, source, second, nil, nil, webrtc.ICETransportPolicyAll)
	m.mu.Lock()
	peer := m.peers[first.AttemptID]
	sameSource := peer.source == m.peers[second.AttemptID].source
	m.mu.Unlock()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+peer.address+"/video/whep", bytes.NewBufferString("v=0"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/sdp")
	response, err := m.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("private signaling returned %d for an unauthenticated request", response.StatusCode)
	}
	for range 2 {
		select {
		case <-live:
		case <-time.After(2 * time.Second):
			t.Fatal("no observed server RTP health transition")
		}
	}
	if !sameSource {
		t.Fatal("viewers did not reuse the same encoder")
	}
	if err := m.Close(t.Context(), p1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	closed, unaffected := a.Load(), b.Load()
	time.Sleep(550 * time.Millisecond)
	if a.Load() != closed || b.Load() <= unaffected {
		t.Fatal("scoped media teardown failed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	audio, err := exec.CommandContext(ctx, filepath.Join(tools, "ffprobe"), "-v", "error", "-rtsp_transport", "tcp",
		"-select_streams", "a", "-show_entries", "stream=codec_name", "-of", "csv=p=0", source).Output()
	if err != nil || !bytes.Contains(audio, []byte("opus")) {
		t.Fatal("independent audio source stopped")
	}

}

func TestSharingMediaControlledRelay(t *testing.T) {
	tools := os.Getenv("CAMSTREAM_TEST_SHARING_TOOLS")
	if tools == "" {
		t.Skip("set CAMSTREAM_TEST_SHARING_TOOLS to the lock-verified runtime tools")
	}
	m, source := fixtureMedia(t, tools)
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	const sharedSecret = "fixture-provider-secret"
	server, err := turn.NewServer(turn.ServerConfig{
		Realm:       "fixture",
		AuthHandler: turn.LongTermTURNRESTAuthHandler(sharedSecret, nil),
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn:            conn,
			PermissionHandler:     func(_ net.Addr, peer net.IP) bool { return peer.IsLoopback() },
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.IPv4(127, 0, 0, 1), Address: "127.0.0.1"},
		}},
	})
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	relay, err := NewCoturnRelay([]string{"turn:" + conn.LocalAddr().String()}, sharedSecret)
	if err != nil {
		t.Fatal(err)
	}
	grant := testGrant()
	credential, err := relay.Issue(t.Context(), grant)
	if err != nil {
		t.Fatal(err)
	}
	host, guest, err := credential.ForAttempt(grant, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ice := []webrtc.ICEServer{{URLs: guest[0].URLs, Username: guest[0].Username, Credential: guest[0].Credential}}
	_, id := connectTestPeer(t, m, source, grant.Attempt, &host, ice, webrtc.ICETransportPolicyRelay)
	if err := m.Close(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	pion "github.com/pion/webrtc/v4"
)

// Explicit integration probe from a separate user-owned Tailnet device. This
// client supplies no authentication header or TLS bypass: real Serve owns identity.
func TestT09Tailnet(t *testing.T) {
	origin := os.Getenv("CAMSTREAM_T09_ORIGIN")
	if origin == "" {
		t.Skip("requires enrolled Tailnet client and controlled VM source")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		t.Fatal("HTTPS origin required")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	request := func(method, path, token, body string, headers map[string]string) (int, []byte) {
		r, e := http.NewRequestWithContext(context.Background(), method, origin+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set(api.CSRFHeader, token)
		}
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if e = resp.Body.Close(); e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, data
	}
	for _, path := range []string{"/", "/api/cameras", "/api/csrf"} {
		status, body := request("GET", path, "", "", map[string]string{api.ServeLoginHeader: "forged@example.invalid"})
		if status != 200 {
			t.Fatal("Serve identity replacement", path, status, string(body))
		}
	}
	status, body := request("POST", "/api/cameras/room1/diagnose", "", "{}", nil)
	if status != 403 || !strings.Contains(string(body), "csrf_invalid") {
		t.Fatal("missing CSRF", status, string(body))
	}
	status, body = request("GET", api.CSRFPath, "", "", nil)
	var csrf api.CSRFResponse
	if status != 200 || json.Unmarshal(body, &csrf) != nil {
		t.Fatal("CSRF bootstrap", status)
	}
	status, body = request("POST", "/api/cameras/room1/diagnose", csrf.Token, "{}", map[string]string{"Origin": "https://invalid.example"})
	if status != 403 || !strings.Contains(string(body), "access_denied") {
		t.Fatal("foreign Origin", status, string(body))
	}
	status, body = request("POST", "/api/cameras/room1/diagnose", csrf.Token, "{}", map[string]string{"Origin": origin})
	if status != 200 {
		t.Fatal("authenticated command", status, string(body))
	}
	for _, path := range []string{"/api/unknown", "/assets/not-found.js"} {
		status, body = request("GET", path, "", "", nil)
		if status != 404 {
			t.Fatal("unknown route", path, status, string(body))
		}
	}
	t.Log("Serve identity replacement, real HTTPS, CSRF, Origin, command and unknown-route checks passed")
	if os.Getenv("CAMSTREAM_T09_MEDIA") != "1" {
		return
	}
	protocol := os.Getenv("CAMSTREAM_T09_PROTOCOL")
	network := pion.NetworkTypeUDP4
	if protocol == "tcp" {
		network = pion.NetworkTypeTCP4
	} else if protocol != "udp" {
		t.Fatal("select tcp or udp")
	}
	type remotePeer struct {
		pc      *pion.PeerConnection
		packets atomic.Uint64
		session api.Session
	}
	var peers []*remotePeer
	for i := range 5 {
		engine := pion.SettingEngine{}
		engine.SetNetworkTypes([]pion.NetworkType{network})
		if ip := net.ParseIP(os.Getenv("CAMSTREAM_T09_CLIENT_IP")); ip != nil {
			engine.SetIPFilter(func(candidate net.IP) bool { return candidate.Equal(ip) })
		}
		pc, e := pion.NewAPI(pion.WithSettingEngine(engine)).NewPeerConnection(pion.Configuration{})
		if e != nil {
			t.Fatal(e)
		}
		p := &remotePeer{pc: pc}
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
		if _, e = pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); e != nil {
			t.Fatal(e)
		}
		offer, e := pc.CreateOffer(nil)
		if e != nil {
			t.Fatal(e)
		}
		gathered := pion.GatheringCompletePromise(pc)
		if e = pc.SetLocalDescription(offer); e != nil {
			t.Fatal(e)
		}
		select {
		case <-gathered:
		case <-time.After(5 * time.Second):
			t.Fatal("ICE gathering timeout")
		}
		requestBody, e := json.Marshal(api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: pc.LocalDescription().SDP}})
		if e != nil {
			t.Fatal(e)
		}
		start := time.Now()
		status, body = request("POST", fmt.Sprintf("/api/cameras/room%d/streams/default/sessions", i%2+1), csrf.Token, string(requestBody), nil)
		if i == 4 {
			if status != 429 || !strings.Contains(string(body), "capacity_exceeded") {
				t.Fatal("fifth viewer capacity rejection", status, string(body))
			}
			t.Log("fifth viewer rejected at configured capacity")
			break
		}
		if status != 201 || json.Unmarshal(body, &p.session) != nil {
			t.Fatal("negotiate", status, string(body))
		}
		t.Cleanup(func() {
			code, data := request("DELETE", "/api/playback/sessions/"+string(p.session.ID), csrf.Token, "", nil)
			if code != 204 {
				t.Error("release", code, string(data))
			}
		})
		if e = pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: p.session.Playback.Answer.SDP}); e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(20 * time.Second)
		for p.packets.Load() < 10 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if p.packets.Load() < 10 {
			t.Fatal("no RTP", pc.ICEConnectionState())
		}
		report := pc.GetStats()
		selected := false
		for _, stat := range report {
			pair, ok := stat.(pion.ICECandidatePairStats)
			if !ok || !pair.Nominated || pair.State != pion.StatsICECandidatePairStateSucceeded {
				continue
			}
			remote, ok := report[pair.RemoteCandidateID].(pion.ICECandidateStats)
			if !ok || remote.Protocol != protocol || remote.IP != os.Getenv("CAMSTREAM_T09_SERVER_IP") {
				t.Fatal("unexpected selected candidate", remote)
			}
			t.Logf("viewer=%d selected=%s remote=%s:%d RTP=%d startup_ms=%d", i+1, remote.Protocol, remote.IP, remote.Port, p.packets.Load(), time.Since(start).Milliseconds())
			selected = true
		}
		if !selected {
			t.Fatal("missing selected candidate evidence")
		}
		peers = append(peers, p)
	}
	if os.Getenv("CAMSTREAM_T09_HOLD") == "1" {
		t.Log("TAILNET_PEERS_READY")
		time.Sleep(30 * time.Second)
	}
	survivor := peers[2]
	if fault := os.Getenv("CAMSTREAM_T09_FAULT"); fault != "" {
		if fault != "viewer" && fault != "source" {
			t.Fatal("unknown external fault expectation")
		}
		counts := make([]uint64, len(peers))
		for i, p := range peers {
			counts[i] = p.packets.Load()
		}
		time.Sleep(time.Second)
		for i, p := range peers {
			stopped := i == 0 || fault == "source" && i == 2
			if stopped != (p.packets.Load() == counts[i]) {
				t.Fatalf("%s fault: viewer %d stopped=%v", fault, i+1, stopped)
			}
		}
		if fault == "source" {
			status, body = request("GET", "/api/cameras/room1/streams/default/status", "", "", nil)
			if status != 200 || !strings.Contains(string(body), "source_unavailable") {
				t.Fatal("source fault latch", status, string(body))
			}
			status, body = request("DELETE", "/api/playback/sessions/"+string(peers[2].session.ID), csrf.Token, "", nil)
			if status != 503 || !strings.Contains(string(body), "cleanup_incomplete") {
				t.Fatal("unconfirmed source cleanup", status, string(body))
			}
			status, body = request("POST", "/api/cameras/room1/streams/default/reconnect", csrf.Token, "", nil)
			if status != 200 {
				t.Fatal("source fault operator reset", status, string(body))
			}
			status, body = request("GET", "/api/cameras/room1/streams/default/status", "", "", nil)
			var resetState api.StreamStatus
			if status != 200 || json.Unmarshal(body, &resetState) != nil || resetState.Error != nil || resetState.State != "idle" {
				t.Fatal("source status after reset", status, string(body))
			}
			survivor = peers[1]
		}
		t.Logf("%s fault isolated; unaffected viewers retain RTP", fault)
	}
	before := survivor.packets.Load()
	status, body = request("DELETE", "/api/playback/sessions/"+string(peers[0].session.ID), csrf.Token, "", nil)
	if status != 204 {
		t.Fatal("isolated release", status, string(body))
	}
	time.Sleep(time.Second)
	if survivor.packets.Load() <= before {
		t.Fatal("sibling stopped after release")
	}
	t.Log("isolated release preserves sibling media")
}

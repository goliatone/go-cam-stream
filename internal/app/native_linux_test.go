//go:build linux

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	pion "github.com/pion/webrtc/v4"
)

// Explicit test-binary entrypoints for a disposable native camstream.service.
// The fixture changes only the source provider; production boundary checks and
// the installed configuration, HTTP listener and service policy remain active.
func TestT09NativeService(t *testing.T) {
	if os.Getenv("CAMSTREAM_T09_NATIVE") != "1" {
		t.Skip("native VM fixture")
	}
	cfg, err := config.Load(context.Background(), "/etc/camstream/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i := range cfg.Cameras {
		cfg.Cameras[i].Strategy = "fixture"
	}
	provider := config.Provider{Strategy: fixtureStrategy{}, Decode: func(input map[string]any) (config.Decoded, error) {
		host, ok := input["host"].(string)
		if !ok {
			return config.Decoded{}, cs.NewError(cs.CodeInvalidArgument, nil)
		}
		return config.Decoded{Config: fixtureConfig(host), Endpoint: host}, nil
	}}
	bc := cfg.BackendConfig(verifyCleanDeployment)
	bc.RuntimeDir = filepath.Join(cfg.Backend.RuntimeDir, "workers")
	bc.AllowExec = true
	b, err := go2rtc.New(bc, fixtureEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg, Dependencies{Backend: b, Runtime: b, Providers: []config.Provider{provider}, VerifyClean: verifyCleanDeployment})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	if err = c.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

// Run outside the service cgroup. Loopback identity headers are test input,
// not evidence of actual Tailscale Serve identity enforcement.
func TestT09NativePeers(t *testing.T) {
	if os.Getenv("CAMSTREAM_T09_PEERS") != "1" {
		t.Skip("native VM peers")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	request := func(method, path, token, body string) (int, []byte) {
		r, err := http.NewRequestWithContext(context.Background(), method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Host = "camera-pi.example.ts.net"
		r.Header.Set(api.ServeLoginHeader, "operator@example.test")
		r.Header.Set(api.CSRFHeader, token)
		r.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err = resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, data
	}
	status, data := request("GET", api.CSRFPath, "", "")
	var csrf api.CSRFResponse
	if status != 200 || json.Unmarshal(data, &csrf) != nil {
		t.Fatal(status, string(data))
	}
	var peers []*mediaPeer
	started := time.Now()
	for i := 0; i < 5; i++ {
		engine := pion.SettingEngine{}
		engine.SetIncludeLoopbackCandidate(true)
		network := pion.NetworkTypeUDP4
		if os.Getenv("CAMSTREAM_T09_TCP") == "1" {
			network = pion.NetworkTypeTCP4
		}
		engine.SetNetworkTypes([]pion.NetworkType{network})
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
			for {
				if _, _, readErr := track.ReadRTP(); readErr != nil {
					return
				}
				peer.packets.Add(1)
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
			t.Fatal("ICE gather timeout")
		}
		body, err := json.Marshal(api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: pc.LocalDescription().SDP}})
		if err != nil {
			t.Fatal(err)
		}
		status, data = request("POST", fmt.Sprintf("/api/cameras/room%d/streams/default/sessions", i%2+1), csrf.Token, string(body))
		if i == 4 {
			if status != 429 {
				t.Fatal("capacity rejection", status, string(data))
			}
			t.Logf("capacity_rejected status=%d body=%s", status, data)
			break
		}
		if status != 201 || json.Unmarshal(data, &peer.session) != nil {
			t.Fatal(status, string(data))
		}
		if err = pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: peer.session.Playback.Answer.SDP}); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool { return peer.packets.Load() > 5 })
		peers = append(peers, peer)
		t.Logf("viewer=%d packets=%d elapsed_ms=%d", i+1, peer.packets.Load(), time.Since(started).Milliseconds())
	}
	t.Log("NATIVE_PEERS_READY")
	if os.Getenv("CAMSTREAM_T09_FAULTS") == "1" {
		// This opt-in probe runs as root outside the disposable service. Select
		// only a currently owned worker from its cgroup and private config.
		killWorker := func(match string) {
			pids, err := os.ReadFile("/sys/fs/cgroup/system.slice/camstream.service/cgroup.procs")
			if err != nil {
				t.Fatal(err)
			}
			for _, pid := range strings.Fields(string(pids)) {
				cmd, err := os.ReadFile("/proc/" + pid + "/cmdline")
				if err != nil {
					continue
				}
				args := strings.Split(string(cmd), "\x00")
				if len(args) < 3 || args[1] != "-config" {
					continue
				}
				body, err := os.ReadFile(args[2])
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(body), match) {
					continue
				}
				n, err := strconv.Atoi(pid)
				if err != nil {
					t.Fatal(err)
				}
				if err = syscall.Kill(n, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
				return
			}
			t.Fatal("owned fault target missing", match)
		}
		killWorker("127.0.0.1:35100")
		time.Sleep(2 * time.Second)
		stopped := peers[0].packets.Load()
		baseline := peers[2].packets.Load()
		time.Sleep(time.Second)
		if peers[0].packets.Load() != stopped || peers[2].packets.Load() <= baseline {
			t.Fatal("viewer fault isolation")
		}
		t.Log("viewer_fault_isolated")
		killWorker("comment=ec70-001")
		time.Sleep(12 * time.Second)
		stopped = peers[2].packets.Load()
		baseline = peers[1].packets.Load()
		time.Sleep(time.Second)
		if peers[2].packets.Load() != stopped || peers[1].packets.Load() <= baseline {
			t.Fatal("ingest fault isolation")
		}
		status, data = request("GET", "/api/cameras/room1/streams/default/status", "", "")
		if status != 200 || !strings.Contains(string(data), "source_unavailable") {
			t.Fatal("source fault not latched", status, string(data))
		}
		t.Logf("ingest_fault_isolated status=%s", data)
		// A stale source observation deliberately withholds a cleanup claim.
		// Only the authenticated operator reset clears the source fault latch.
		status, data = request("DELETE", "/api/playback/sessions/"+string(peers[2].session.ID), csrf.Token, "")
		if status != 503 || !strings.Contains(string(data), "cleanup_incomplete") {
			t.Fatal("uncertain cleanup not retained", status, string(data))
		}
		status, data = request("POST", "/api/cameras/room1/streams/default/reconnect", csrf.Token, "")
		if status != 200 {
			t.Fatal("operator reset", status, string(data))
		}
		t.Log("uncertain_cleanup_retained_operator_reset_passed")
	}
	if os.Getenv("CAMSTREAM_T09_HOLD") == "1" {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer cancel()
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Minute):
		}
		return
	}
	for _, p := range peers {
		status, data = request("DELETE", "/api/playback/sessions/"+string(p.session.ID), csrf.Token, "")
		if status != 204 {
			t.Fatal(status, string(data))
		}
	}
}

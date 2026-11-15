//go:build linux

package app

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	pion "github.com/pion/webrtc/v4"
)

type fixtureConfig string

func (fixtureConfig) StrategyID() cs.StrategyID { return "fixture" }

type fixtureSource string

func (fixtureSource) Kind() cs.SourceKind { return "test.camstream/fixture" }

type fixtureStrategy struct{}

func (fixtureStrategy) ID() cs.StrategyID { return "fixture" }
func (fixtureStrategy) Streams(context.Context, cs.Camera) ([]cs.Stream, error) {
	return []cs.Stream{{ID: "default", Name: "Default", Capabilities: cs.MediaCapabilities{Video: true}}}, nil
}
func (fixtureStrategy) Resolve(_ context.Context, c cs.Camera, _ cs.StreamID) (cs.StreamSpec, error) {
	return cs.StreamSpec{Key: cs.SourceKey(c.PhysicalID), PhysicalCameraID: c.PhysicalID, Source: fixtureSource("exec:ffmpeg -hide_banner -loglevel error -re -stream_loop -1 -i /fixture/clip.mkv -metadata comment=" + string(c.PhysicalID) + " -c copy -rtsp_transport tcp -f rtsp {output}"), Constraints: cs.DeviceConstraints{MaxActiveSources: 1}, Capabilities: cs.MediaCapabilities{Video: true}}, nil
}

type fixtureEncoder struct{}

func (fixtureEncoder) Kind() cs.SourceKind { return fixtureSource("").Kind() }
func (fixtureEncoder) Encode(_ context.Context, source cs.Source) (go2rtc.Input, error) {
	v, ok := source.(fixtureSource)
	if !ok {
		return go2rtc.Input{}, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return go2rtc.Input{URL: string(v)}, nil
}
func mediaController(t *testing.T, cfg config.Config) (*Controller, *httptest.Server, string) {
	t.Helper()
	target, err := url.Parse("http://" + cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(api.ServeLoginHeader)
		r.Header.Set(api.ServeLoginHeader, "owner")
		proxy.ServeHTTP(w, r)
	}))
	cfg.Origin = server.URL
	for i := range cfg.Cameras {
		cfg.Cameras[i].Strategy = "fixture"
	}
	if os.Getenv("CAMSTREAM_T07_CRASH") != "1" {
		cfg.Backend.MediaAddress, cfg.Backend.CandidateAddress = "127.0.0.1", "127.0.0.1"
	}
	cfg.Backend.StartupTimeout, cfg.Backend.FailureTimeout = 4*time.Second, 2*time.Second
	cfg.Backend.CleanupTimeout, cfg.Backend.TermGrace, cfg.Backend.PollInterval = 2*time.Second, 100*time.Millisecond, 100*time.Millisecond
	cfg.Limits.CleanupTimeout, cfg.Limits.PollInterval = 3*time.Second, 100*time.Millisecond
	backendConfig := cfg.BackendConfig(verifyCleanContainer)
	backendConfig.RuntimeDir = filepath.Join(cfg.Backend.RuntimeDir, "workers")
	backendConfig.AllowExec = true
	backend, err := go2rtc.New(backendConfig, fixtureEncoder{})
	if err != nil {
		t.Fatalf("backend constructor: %v", err)
	}
	provider := config.Provider{Strategy: fixtureStrategy{}, Decode: func(input map[string]any) (config.Decoded, error) {
		host, ok := input["host"].(string)
		if !ok {
			return config.Decoded{}, cs.NewError(cs.CodeInvalidArgument, nil)
		}
		return config.Decoded{Config: fixtureConfig(host), Endpoint: host}, nil
	}}
	if err = cfg.Validate(); err != nil {
		t.Fatalf("config validation: %v", err)
	}
	if _, err = config.Prepare(context.Background(), cfg, provider); err != nil {
		t.Fatalf("catalog validation: %v", err)
	}
	c, err := New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: []config.Provider{provider}, VerifyClean: verifyCleanContainer})
	if err != nil {
		server.Close()
		t.Fatalf("host constructor: %v", err)
	}
	cert := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return c, server, cert
}

type mediaPeer struct {
	pc      *pion.PeerConnection
	packets atomic.Uint64
	session api.Session
}

func newMediaPeer(t *testing.T, c *Controller, camera string) *mediaPeer {
	t.Helper()
	engine := pion.SettingEngine{}
	engine.SetIncludeLoopbackCandidate(true)
	engine.SetNetworkTypes([]pion.NetworkType{pion.NetworkTypeUDP4})
	engine.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	pc, err := pion.NewAPI(pion.WithSettingEngine(engine)).NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	peer := &mediaPeer{pc: pc}
	t.Cleanup(func() {
		if e := pc.Close(); e != nil {
			t.Error(e)
		}
	})
	pc.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
		for {
			if _, _, e := track.ReadRTP(); e != nil {
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
	case <-time.After(2 * time.Second):
		t.Fatal("ICE gather")
	}
	request, err := json.Marshal(api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: pc.LocalDescription().SDP}})
	if err != nil {
		t.Fatal(err)
	}
	response := rawRequest(c, "POST", "/api/cameras/"+camera+"/streams/default/sessions", "owner", tokenFor(t, c, "owner"), "", string(request))
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &peer.session) != nil {
		t.Fatal("HTTP negotiation", response.Code, response.Body.String())
	}
	if err = pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: peer.session.Playback.Answer.SDP}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return peer.packets.Load() > 5 })
	return peer
}
func cliProcess(t *testing.T, cert string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/test/camstream", args...)
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+cert)
	return cmd.CombinedOutput()
}
func TestT07PinnedMedia(t *testing.T) {
	if os.Getenv("CAMSTREAM_T07_MEDIA") != "1" {
		t.Skip("requires dedicated pinned Linux --init fixture")
	}
	cfg := testConfig(t)
	c, server, cert := mediaController(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		server.Close()
	})
	eventually(t, c.ready.Load)
	first := newMediaPeer(t, c, "room1")
	second := newMediaPeer(t, c, "hall")
	other := newMediaPeer(t, c, "room2")
	workers, err := filepath.Glob(filepath.Join(cfg.Backend.RuntimeDir, "workers/generation-*/worker-*"))
	if err != nil || len(workers) != 5 {
		t.Fatal("expected five workers", len(workers), err)
	}
	before := first.packets.Load()
	output, err := cliProcess(t, cert, "status", "--origin", server.URL, "--camera", "room1")
	if err != nil || !strings.Contains(string(output), `"viewers":2`) {
		t.Fatal("second process status", string(output), err)
	}
	eventually(t, func() bool { return first.packets.Load() > before+5 })
	output, err = cliProcess(t, cert, "diagnose", "--origin", server.URL, "--camera", "room1")
	if err != nil || !strings.Contains(string(output), `"state":"passed"`) {
		t.Fatal("second process diagnose", string(output), err)
	}
	source, playback := sourceWorkers(t, c, "ec70-001")
	killWorker(t, playback)
	eventually(t, func() bool {
		r := rawRequest(c, "GET", "/api/cameras/room1/streams/default/status", "owner", "", "", "")
		var state api.StreamStatus
		return json.Unmarshal(r.Body.Bytes(), &state) == nil && state.Viewers != nil && *state.Viewers == 1
	})
	otherBefore := other.packets.Load()
	eventually(t, func() bool { return other.packets.Load() > otherBefore+5 })
	killWorker(t, source)
	eventually(t, func() bool {
		r := rawRequest(c, "GET", "/api/cameras/room1/streams/default/status", "owner", "", "", "")
		var state api.StreamStatus
		return json.Unmarshal(r.Body.Bytes(), &state) == nil && state.Error != nil && state.Error.Code == cs.CodeSourceUnavailable
	})
	rejected := rawRequest(c, "POST", "/api/cameras/room1/streams/default/sessions", "owner", tokenFor(t, c, "owner"), "", `{"offer":{"type":"offer","sdp":"v=0\r\n"}}`)
	if rejected.Code != 503 {
		t.Fatal("viewer cleared source fault", rejected.Code)
	}
	output, err = cliProcess(t, cert, "reconnect", "--origin", server.URL, "--camera", "room1", "--stream", "default")
	if err != nil || !strings.Contains(string(output), `"hall"`) {
		t.Fatal("operator reset", string(output), err)
	}
	renewed := newMediaPeer(t, c, "room1")
	otherBefore = other.packets.Load()
	eventually(t, func() bool { return renewed.packets.Load() > 5 && other.packets.Load() > otherBefore+5 })
	duplicateConfig := filepath.Join(t.TempDir(), "duplicate.yaml")
	body := fmt.Sprintf("installation_id: %s\norigin: %s\nlisten: %s\nbackend:\n  runtime_dir: %s\n  media_address: 127.0.0.1\n  candidate_address: 127.0.0.1\ncameras: []\naccess: []\n", cfg.InstallationID, server.URL, cfg.Listen, cfg.Backend.RuntimeDir)
	if err = os.WriteFile(duplicateConfig, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = cliProcess(t, cert, "serve", "--config", duplicateConfig)
	if err == nil || !strings.Contains(string(output), "controller_conflict") {
		t.Fatal("duplicate controller admitted", string(output), err)
	}
	if first.session.ID == second.session.ID {
		t.Fatal("duplicate sessions")
	}
	server.Close()
	output, err = cliProcess(t, cert, "list", "--origin", server.URL)
	if err == nil || !strings.Contains(string(output), "server_unavailable") {
		t.Fatal("stopped server fallback", string(output), err)
	}
	t.Log("C=2/V=3/five workers; HTTPS CLI status/diagnose/reset, viewer/ingest crash isolation, source latch, duplicate controller and stopped-server behavior pass")
}
func sourceWorkers(t *testing.T, c *Controller, physical string) (string, string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(c.cfg.Backend.RuntimeDir, "workers/generation-*/worker-*/config.json"))
	if err != nil {
		t.Fatal(err)
	}
	source, relay := "", ""
	bodies := map[string]string{}
	for _, path := range files {
		body, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		bodies[path] = string(body)
		if strings.Contains(string(body), "comment="+physical) {
			var cfg struct {
				RTSP struct {
					Listen string `json:"listen"`
				} `json:"rtsp"`
			}
			if e = json.Unmarshal(body, &cfg); e != nil {
				t.Fatal(e)
			}
			source, relay = path, cfg.RTSP.Listen
		}
	}
	for path, body := range bodies {
		if path != source && relay != "" && strings.Contains(body, "rtsp://"+relay) {
			return source, path
		}
	}
	t.Fatal("source/playback worker mapping")
	return "", ""
}
func killWorker(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		pid, parseErr := strconv.Atoi(e.Name())
		if parseErr != nil {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if readErr == nil && strings.Contains(string(body), path) {
			if err = syscall.Kill(pid, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("owned worker PID missing")
}

// This test-only bridge adapts the independent T15 host peer's SDP transport to
// the real authenticated HTTPS API. Production exposes no anonymous SDP bridge.
func TestT07CrashController(t *testing.T) {
	if os.Getenv("CAMSTREAM_T07_CRASH") != "1" {
		t.Skip("standalone crash fixture")
	}
	cfg := testConfig(t)
	cfg.Backend.RuntimeDir = "/tmp/t07-installation"
	cfg.Backend.Network = "tcp"
	cfg.Backend.CandidateAddress = "127.0.0.1"
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range addresses {
		ip, _, e := net.ParseCIDR(addr.String())
		if e == nil && ip.To4() != nil && !ip.IsLoopback() {
			cfg.Backend.MediaAddress = ip.String()
			break
		}
	}
	old, err := filepath.Glob(filepath.Join(cfg.Backend.RuntimeDir, "workers/generation-*"))
	if err != nil {
		t.Fatal(err)
	}
	c, proxy, _ := mediaController(t, cfg)
	defer proxy.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	eventually(t, c.ready.Load)
	if len(old) > 0 {
		for _, path := range old {
			if _, e := os.Stat(path); !os.IsNotExist(e) {
				t.Fatal("stale generation preserved after verified empty boundary", e)
			}
		}
		t.Log("stale_generation_retired")
	}
	priorPath := "/tmp/t07-prior-session"
	if prior, e := os.ReadFile(priorPath); e == nil {
		response := rawRequest(c, "DELETE", "/api/playback/sessions/"+string(prior), "owner", tokenFor(t, c, "owner"), "", "")
		if response.Code != http.StatusForbidden {
			t.Fatal("old session adopted", response.Code)
		}
		t.Log("old_session_reset")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if _, e := fmt.Fprint(w, os.Getpid()); e != nil {
			return
		}
	})
	mux.HandleFunc("/api/webrtc", func(w http.ResponseWriter, r *http.Request) {
		offer, e := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if e != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		tokenRequest, e := http.NewRequestWithContext(r.Context(), http.MethodGet, proxy.URL+api.CSRFPath, nil)
		if e != nil {
			http.Error(w, "request", http.StatusInternalServerError)
			return
		}
		tokenResponse, e := proxy.Client().Do(tokenRequest)
		if e != nil {
			http.Error(w, "bootstrap", http.StatusInternalServerError)
			return
		}
		var token api.CSRFResponse
		e = json.NewDecoder(tokenResponse.Body).Decode(&token)
		closeErr := tokenResponse.Body.Close()
		if e != nil || closeErr != nil {
			http.Error(w, "token", http.StatusInternalServerError)
			return
		}
		body, e := json.Marshal(api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: string(offer)}})
		if e != nil {
			http.Error(w, "encode", http.StatusInternalServerError)
			return
		}
		request, e := http.NewRequestWithContext(r.Context(), http.MethodPost, proxy.URL+"/api/cameras/room1/streams/default/sessions", strings.NewReader(string(body)))
		if e != nil {
			http.Error(w, "request", http.StatusInternalServerError)
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(api.CSRFHeader, token.Token)
		response, e := proxy.Client().Do(request)
		if e != nil {
			http.Error(w, "negotiate", http.StatusInternalServerError)
			return
		}
		var session api.Session
		e = json.NewDecoder(response.Body).Decode(&session)
		closeErr = response.Body.Close()
		if e != nil || closeErr != nil || response.StatusCode != http.StatusCreated {
			http.Error(w, "response", http.StatusInternalServerError)
			return
		}
		if e = os.WriteFile(priorPath, []byte(session.ID), 0600); e != nil {
			http.Error(w, "fixture state", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		if _, e := io.WriteString(w, session.Playback.Answer.SDP); e != nil {
			return
		}
	})
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", "0.0.0.0:35200")
	if err != nil {
		t.Fatal(err)
	}
	bridge := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() {
		if e := bridge.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			t.Error(e)
		}
	}()
	t.Logf("controller_ready pid=%d", os.Getpid())
	if err = <-done; err != nil {
		t.Error(err)
	}
	if err = bridge.Close(); err != nil {
		t.Error(err)
	}
}

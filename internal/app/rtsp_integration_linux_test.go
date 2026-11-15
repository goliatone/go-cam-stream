//go:build linux

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
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
	pion "github.com/pion/webrtc/v4"
)

// External, separately supervised origins supply controlled RTSP media on
// 34001/34003. The application uses NewDefault with exec disabled and its real
// container verifier. Only the test HTTPS ingress replaces Serve identity.
type rtspHost struct {
	c            *Controller
	proxy        *httptest.Server
	kasaRequests atomic.Uint64
}

func startRTSPHost(t *testing.T, browser bool) *rtspHost {
	t.Helper()
	h := new(rtspHost)
	t.Setenv("T05_RTSP_USER", "t05-user")
	t.Setenv("T05_RTSP_PASSWORD", "t05-fixture-password")
	t.Setenv("T05_KASA_USER", "t05-kasa-user")
	t.Setenv("T05_KASA_PASSWORD", "t05-kasa-password")
	kasa := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if r.Method == http.MethodGet && r.URL.Path == "/https/stream/mixed" && ok && user == "t05-kasa-user" && password == base64.StdEncoding.EncodeToString([]byte("t05-kasa-password")) {
			h.kasaRequests.Add(1)
		}
		w.WriteHeader(http.StatusServiceUnavailable) // auth route proof; no Kasa media claim
	}))
	t.Cleanup(kasa.Close)
	kasaURL, err := url.Parse(kasa.URL)
	if err != nil {
		t.Fatal(err)
	}
	kasaPort, err := strconv.Atoi(kasaURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	cfg.Backend.MediaAddress, cfg.Backend.CandidateAddress = "127.0.0.1", "127.0.0.1"
	cfg.Backend.MediaPorts = config.Ports{First: 35110, Last: 35115}
	cfg.Backend.StartupTimeout, cfg.Backend.FailureTimeout = 5*time.Second, 2*time.Second
	cfg.Backend.TermGrace, cfg.Backend.PollInterval = 100*time.Millisecond, 100*time.Millisecond
	cfg.Limits.MaxSources, cfg.Limits.MaxViewers = 5, 6
	cfg.Cameras = []config.Camera{
		{ID: "primary", Name: "RTSP Primary", PhysicalID: "rtsp-primary", Strategy: "rtsp", Config: map[string]any{
			"host": "127.0.0.1", "port": 34001, "username_ref": "env://T05_RTSP_USER", "password_ref": "env://T05_RTSP_PASSWORD",
			"profiles": []any{map[string]any{"id": "main", "name": "Main", "path": "/t05-main-private"}, map[string]any{"id": "sub", "name": "Substream", "path": "/t05-sub-private"}, map[string]any{"id": "broken", "name": "Unavailable", "path": "/t05-missing-private"}}}},
		{ID: "other", Name: "RTSP Other", PhysicalID: "rtsp-other", Strategy: "rtsp", Config: map[string]any{
			"host": "127.0.0.1", "port": 34003, "profiles": []any{map[string]any{"id": "main", "name": "Main", "path": "/t05-other-private"}}}},
		{ID: "kasa", Name: "Synthetic Kasa (no media)", PhysicalID: "kasa-synthetic", Strategy: "ec70", Config: map[string]any{
			"host": "127.0.0.1", "port": kasaPort, "username_ref": "env://T05_KASA_USER", "password_ref": "env://T05_KASA_PASSWORD"}},
	}
	alias := cfg.Cameras[0]
	alias.ID, alias.Name, alias.Streams = "alias", "RTSP Primary alias", []cs.StreamID{"main"}
	cfg.Cameras = append(cfg.Cameras, alias)
	cfg.Access = []config.Access{
		{Login: "owner", Grants: []config.Grant{
			{CameraID: "primary", Streams: []cs.StreamID{"main", "sub", "broken"}, View: true, Diagnose: true, Reconnect: true},
			{CameraID: "alias", Streams: []cs.StreamID{"main"}, View: true, Diagnose: true, Reconnect: true},
			{CameraID: "other", Streams: []cs.StreamID{"main"}, View: true, Diagnose: true, Reconnect: true},
			{CameraID: "kasa", Streams: []cs.StreamID{"default"}, View: true, Diagnose: true, Reconnect: true}}},
		{Login: "viewer", Grants: []config.Grant{{CameraID: "primary", Streams: []cs.StreamID{"main"}, View: true}}},
	}
	target, err := url.Parse("http://" + cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	reverse := httputil.NewSingleHostReverseProxy(target)
	h.proxy = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-Test-Actor")
		if actor == "" {
			actor = "owner"
		}
		r.Header.Del(api.ServeLoginHeader)
		r.Header.Set(api.ServeLoginHeader, actor)
		recorder := &browserResponse{ResponseWriter: w, status: 200}
		reverse.ServeHTTP(recorder, r)
		if browser {
			fmt.Printf("T05_HTTP %s %s %d\n", r.Method, r.URL.Path, recorder.status)
		}
	}))
	if browser {
		addresses, e := net.InterfaceAddrs()
		if e != nil {
			t.Fatal(e)
		}
		for _, addr := range addresses {
			ip, _, e := net.ParseCIDR(addr.String())
			if e == nil && ip.To4() != nil && !ip.IsLoopback() {
				cfg.Backend.MediaAddress = ip.String()
				break
			}
		}
		if err = h.proxy.Listener.Close(); err != nil {
			t.Fatal(err)
		}
		h.proxy.Listener, err = new(net.ListenConfig).Listen(context.Background(), "tcp", ":35410")
		if err != nil {
			t.Fatal(err)
		}
	}
	h.proxy.StartTLS()
	t.Cleanup(h.proxy.Close)
	cfg.Origin = h.proxy.URL
	if browser {
		cfg.Origin = "https://127.0.0.1:35410"
		if err = os.WriteFile("/evidence/ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: h.proxy.Certificate().Raw}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	h.c, err = NewDefault(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	eventually(t, h.c.ready.Load)
	return h
}

func (h *rtspHost) request(t *testing.T, actor, method, path, body string) (int, []byte) {
	t.Helper()
	token := ""
	if method != "GET" {
		status, data := h.request(t, actor, "GET", api.CSRFPath, "")
		var csrf api.CSRFResponse
		if status != 200 || json.Unmarshal(data, &csrf) != nil {
			t.Fatal("CSRF bootstrap")
		}
		token = csrf.Token
	}
	r, err := http.NewRequestWithContext(context.Background(), method, h.proxy.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Test-Actor", actor)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(api.CSRFHeader, token)
	response, err := h.proxy.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"t05-main-private", "t05-sub-private", "t05-other-private", "t05-missing-private", "t05-user", "t05-fixture-password", "t05-kasa-user", "t05-kasa-password", "env://", "rtsp://", "kasa://"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("public response leaked %q", private)
		}
	}
	return response.StatusCode, data
}

func (h *rtspHost) peer(t *testing.T, camera, stream, actor string, expected int, code cs.ErrorCode) *mediaPeer {
	t.Helper()
	engine := pion.SettingEngine{}
	engine.SetIncludeLoopbackCandidate(true)
	engine.SetNetworkTypes([]pion.NetworkType{pion.NetworkTypeTCP4})
	engine.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	pc, err := pion.NewAPI(pion.WithSettingEngine(engine)).NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	p := &mediaPeer{pc: pc}
	t.Cleanup(func() {
		if e := pc.Close(); e != nil {
			t.Error(e)
		}
	})
	pc.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
		if track.Codec().MimeType != pion.MimeTypeH264 {
			t.Error("unexpected video codec")
			return
		}
		for {
			packet, _, e := track.ReadRTP()
			if e != nil {
				return
			}
			if len(packet.Payload) > 0 {
				p.packets.Add(1)
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
		t.Fatal("gather deadline")
	}
	body, err := json.Marshal(api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: pc.LocalDescription().SDP}})
	if err != nil {
		t.Fatal(err)
	}
	status, data := h.request(t, actor, "POST", "/api/cameras/"+camera+"/streams/"+stream+"/sessions", string(body))
	if status != expected {
		t.Fatalf("negotiate %s/%s: status=%d body=%s", camera, stream, status, data)
	}
	if expected != 201 {
		var failure api.ErrorResponse
		if json.Unmarshal(data, &failure) != nil || failure.Error.Code != code {
			t.Fatalf("wrong failure: %s", data)
		}
		return p
	}
	if json.Unmarshal(data, &p.session) != nil {
		t.Fatal("session JSON")
	}
	if err = pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: p.session.Playback.Answer.SDP}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return p.packets.Load() > 10 })
	t.Logf("%s/%s receives H264 over TCP", camera, stream)
	return p
}

func (h *rtspHost) release(t *testing.T, p *mediaPeer) {
	t.Helper()
	status, body := h.request(t, "owner", "DELETE", "/api/playback/sessions/"+string(p.session.ID), "")
	if status != 204 {
		t.Fatalf("release: %d %s", status, body)
	}
	if err := p.pc.Close(); err != nil {
		t.Fatal(err)
	}
}

func originConsumers(t *testing.T, port int, stream string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/streams?src=%s", port, stream), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Consumers []json.RawMessage `json:"consumers"`
	}
	err = json.NewDecoder(response.Body).Decode(&state)
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || response.StatusCode != http.StatusOK {
		t.Fatal("origin observation", err, closeErr, response.StatusCode)
	}
	return len(state.Consumers)
}

// Change only the separately supervised fixture origin, never a managed worker.
func patchRTSPOrigin(t *testing.T, source string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	query := url.Values{"name": {"t05-missing-private"}, "src": {source}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, "http://127.0.0.1:34000/api/streams?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	closeErr := response.Body.Close()
	if closeErr != nil || response.StatusCode != http.StatusOK {
		t.Fatal("origin repair", closeErr, response.StatusCode)
	}
}

func TestT05ProductionRTSP(t *testing.T) {
	if os.Getenv("CAMSTREAM_RTSP_T05") != "1" {
		t.Skip("isolated RTSP host fixture")
	}
	h := startRTSPHost(t, false)
	status, body := h.request(t, "viewer", "GET", "/api/cameras", "")
	var catalog api.CameraList
	if status != 200 || json.Unmarshal(body, &catalog) != nil || len(catalog.Cameras) != 1 || catalog.Cameras[0].ID != "primary" || len(catalog.Cameras[0].Streams) != 1 {
		t.Fatal("filtered catalog")
	}
	h.peer(t, "other", "main", "viewer", 403, cs.CodeAccessDenied)
	h.peer(t, "primary", "sub", "viewer", 403, cs.CodeAccessDenied)
	if status, _ = h.request(t, "viewer", "POST", "/api/cameras/primary/streams/main/reconnect", ""); status != 403 {
		t.Fatal("unauthorized reset")
	}
	first := h.peer(t, "primary", "main", "owner", 201, "")
	second := h.peer(t, "alias", "main", "owner", 201, "")
	other := h.peer(t, "other", "main", "owner", 201, "")
	eventually(t, func() bool {
		return originConsumers(t, 34000, "t05-main-private") == 1 && originConsumers(t, 34002, "t05-other-private") == 1
	})
	h.peer(t, "primary", "sub", "owner", 409, cs.CodeCameraBusy)
	before := second.packets.Load()
	h.release(t, first)
	eventually(t, func() bool { return second.packets.Load() > before+10 })
	h.release(t, second)
	eventually(t, func() bool { return originConsumers(t, 34000, "t05-main-private") == 0 })
	sub := h.peer(t, "primary", "sub", "owner", 201, "")
	h.release(t, sub)
	eventually(t, func() bool { return originConsumers(t, 34000, "t05-sub-private") == 0 })
	reopened := h.peer(t, "primary", "main", "owner", 201, "")
	h.release(t, reopened)
	t.Log("alias sharing, sibling survival, competing-profile denial, switch and final-close/reopen passed")
	before = other.packets.Load()
	start := time.Now()
	h.peer(t, "primary", "broken", "owner", 503, cs.CodeSourceUnavailable)
	if time.Since(start) > 12*time.Second {
		t.Fatal("unbounded RTSP failure")
	}
	// Repair the same origin route while the production source remains latched.
	// Restore its unavailable state afterward for the optional browser phase.
	t.Cleanup(func() { patchRTSPOrigin(t, "rtsp://127.0.0.1:34003/t05-missing-private") })
	patchRTSPOrigin(t, "rtsp://127.0.0.1:34003/t05-other-private")
	h.peer(t, "primary", "broken", "owner", 503, cs.CodeSourceUnavailable)
	if status, body = h.request(t, "owner", "POST", "/api/cameras/primary/streams/broken/reconnect", ""); status != 200 {
		t.Fatalf("reset: %d %s", status, body)
	}
	recovered := h.peer(t, "primary", "broken", "owner", 201, "")
	t.Log("repaired same profile stays latched until operator reset, then receives media")
	eventually(t, func() bool { return other.packets.Load() > before+10 })
	h.peer(t, "kasa", "default", "owner", 503, cs.CodeSourceUnavailable)
	if h.kasaRequests.Load() == 0 {
		t.Fatal("default Kasa encoder did not reach synthetic auth fixture")
	}
	time.Sleep(3 * time.Second)
	count := h.kasaRequests.Load()
	time.Sleep(3 * time.Second)
	if h.kasaRequests.Load() != count {
		t.Fatal("Kasa retries outlived source fault")
	}
	h.peer(t, "kasa", "default", "owner", 503, cs.CodeSourceUnavailable)
	if h.kasaRequests.Load() != count {
		t.Fatal("viewer retry reopened latched Kasa source")
	}
	for _, id := range []string{"primary", "other", "kasa"} {
		status, _ = h.request(t, "owner", "POST", "/api/cameras/"+id+"/diagnose", "{}")
		if status != 200 {
			t.Fatal("diagnostic transport", status)
		}
	}
	before = recovered.packets.Load()
	otherBefore := other.packets.Load()
	eventually(t, func() bool { return recovered.packets.Load() > before+10 && other.packets.Load() > otherBefore+10 })
	h.release(t, recovered)
	h.release(t, other)
	eventually(t, func() bool {
		return originConsumers(t, 34000, "t05-main-private") == 0 && originConsumers(t, 34002, "t05-other-private") == 0
	})
	// Explicit operator reset also confirms cleanup of the failed synthetic source.
	if status, _ = h.request(t, "owner", "POST", "/api/cameras/kasa/streams/default/reconnect", ""); status != 200 {
		t.Fatal("Kasa reset")
	}
	workers, err := filepath.Glob(filepath.Join(h.c.cfg.Backend.RuntimeDir, "workers/generation-*/worker-*"))
	if err != nil || len(workers) != 0 {
		t.Fatal("retained workers after final release", len(workers), err)
	}
	t.Logf("failure isolation, bounded latching, operator recovery, grants and safe diagnostics passed; Kasa auth requests=%d (no Kasa media)", count)
}

func TestT05RTSPBrowser(t *testing.T) {
	if os.Getenv("CAMSTREAM_RTSP_T05_BROWSER") != "1" {
		t.Skip("isolated browser fixture")
	}
	_ = startRTSPHost(t, true)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	fmt.Println("T05_BROWSER_READY")
	<-ctx.Done()
}

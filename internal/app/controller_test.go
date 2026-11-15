package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/transport/cli"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type testBackend struct {
	mu                                       sync.Mutex
	sources                                  map[cs.SourceKey]bool
	sessions                                 map[cs.SessionID]cs.SessionState
	faults                                   map[cs.SourceKey]cs.ErrorCode
	statusFailure, resetFailure, blockCreate bool
	creates, resets                          int
}

func newBackend() *testBackend {
	return &testBackend{sources: map[cs.SourceKey]bool{}, sessions: map[cs.SessionID]cs.SessionState{}, faults: map[cs.SourceKey]cs.ErrorCode{}}
}
func (b *testBackend) state(key cs.SourceKey) cs.StreamState {
	n := 0
	for _, v := range b.sessions {
		if v.SourceKey == key && v.Resources == cs.PresencePresent {
			n++
		}
	}
	out := cs.StreamState{Key: key, State: cs.StateIdle, Upstream: cs.PresenceAbsent, ActiveSessions: &n, ObservedAt: time.Now(), Fault: b.faults[key]}
	if n > 0 {
		out.State, out.Upstream = cs.StatePlaying, cs.PresencePresent
	}
	return out
}
func (b *testBackend) EnsureStream(ctx context.Context, spec cs.StreamSpec) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sources[spec.Key] = true
	return b.state(spec.Key), nil
}
func (b *testBackend) StreamStatus(ctx context.Context, key cs.SourceKey) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.statusFailure {
		return cs.StreamState{}, cs.NewError(cs.CodeBackendUnavailable, errors.New("kasa://PRIVATE_PASSWORD@192.0.2.11"))
	}
	if !b.sources[key] {
		return cs.StreamState{}, cs.NewError(cs.CodeNotFound, nil)
	}
	return b.state(key), nil
}
func (b *testBackend) RemoveStream(ctx context.Context, key cs.SourceKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.faults[key] != "" || b.state(key).Upstream != cs.PresenceAbsent {
		return cs.NewError(cs.CodeConflict, nil)
	}
	delete(b.sources, key)
	return nil
}
func (b *testBackend) CreateSession(ctx context.Context, spec cs.SessionSpec) (cs.SessionState, error) {
	b.mu.Lock()
	b.creates++
	out := cs.SessionState{ID: spec.ID, SourceKey: spec.SourceKey, State: cs.StateConnecting, Resources: cs.PresencePresent, ObservedAt: time.Now()}
	b.sessions[spec.ID] = out
	block := b.blockCreate
	b.mu.Unlock()
	if block {
		<-ctx.Done()
		return out, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Answer = &cs.Answer{SDP: "v=0\r\n"}
	return out, nil
}
func (b *testBackend) SessionStatus(ctx context.Context, id cs.SessionID) (cs.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return cs.SessionState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.sessions[id]
	if !ok {
		return v, cs.NewError(cs.CodeNotFound, nil)
	}
	v.ObservedAt = time.Now()
	return v, nil
}
func (b *testBackend) CloseSession(ctx context.Context, id cs.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if v, ok := b.sessions[id]; ok {
		v.Resources, v.State = cs.PresenceAbsent, cs.StateClosed
		b.sessions[id] = v
	}
	return nil
}
func (b *testBackend) ResetSource(ctx context.Context, key cs.SourceKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resets++
	if b.resetFailure {
		return cs.NewError(cs.CodeCleanupIncomplete, errors.New("PRIVATE_PASSWORD"))
	}
	for id, v := range b.sessions {
		if v.SourceKey == key {
			v.Resources, v.State = cs.PresenceAbsent, cs.StateClosed
			b.sessions[id] = v
		}
	}
	delete(b.faults, key)
	return nil
}
func (b *testBackend) Start(context.Context) error { return nil }
func (b *testBackend) Close(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.resetFailure {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	return nil
}
func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(context.Background(), "../../config/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for {
		listener, e := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		cfg.Listen = listener.Addr().String()
		addr, e := net.ResolveTCPAddr("tcp", cfg.Listen)
		if e != nil {
			t.Fatal(e)
		}
		if e = listener.Close(); e != nil {
			t.Fatal(e)
		}
		// Linux ephemeral ports can overlap the configured worker pools.
		if (addr.Port < cfg.Backend.PrivatePorts.First || addr.Port > cfg.Backend.PrivatePorts.Last) && (addr.Port < cfg.Backend.MediaPorts.First || addr.Port > cfg.Backend.MediaPorts.Last) {
			break
		}
	}
	cfg.Backend.RuntimeDir = filepath.Join(t.TempDir(), "installation")
	cfg.Cameras = cfg.Cameras[:2]
	alias := cfg.Cameras[0]
	alias.ID = "hall"
	cfg.Cameras = append(cfg.Cameras, alias)
	all := []config.Grant{}
	for _, c := range cfg.Cameras {
		all = append(all, config.Grant{CameraID: c.ID, Streams: []cs.StreamID{"default"}, View: true, Diagnose: true, Reconnect: true})
	}
	cfg.Access = []config.Access{{Login: "owner", Grants: all}, {Login: "alice", Grants: []config.Grant{{CameraID: "room1", Streams: []cs.StreamID{"default"}, View: true}}}, {Login: "bob", Grants: []config.Grant{{CameraID: "room1", Streams: []cs.StreamID{"default"}, View: true}}}}
	return cfg
}
func startFixture(t *testing.T) (*Controller, *testBackend, *httptest.Server) {
	t.Helper()
	cfg := testConfig(t)
	target, parseErr := url.Parse("http://" + cfg.Listen)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Synthetic Serve equivalent: owns the header, never forwards supplied identity.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-Test-Actor")
		if actor == "" {
			actor = "owner"
		}
		r.Header.Del(api.ServeLoginHeader)
		r.Header.Set(api.ServeLoginHeader, actor)
		proxy.ServeHTTP(w, r)
	}))
	cfg.Origin = server.URL
	backend := newBackend()
	controller, err := New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: []config.Provider{config.EC70Provider()}, VerifyClean: func(context.Context) error { return nil }})
	if err != nil {
		server.Close()
		t.Fatalf("host construction: %v; cause: %#v", err, errors.Unwrap(err))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	t.Cleanup(func() {
		backend.mu.Lock()
		backend.resetFailure, backend.statusFailure = false, false
		backend.mu.Unlock()
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(10 * time.Second):
			t.Error("controller shutdown timeout")
		}
		server.Close()
	})
	eventually(t, controller.ready.Load)
	return controller, backend, server
}
func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func rawRequest(c *Controller, method, path, actor, token, origin, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	u, parseErr := url.Parse(c.cfg.Origin)
	if parseErr != nil {
		panic(parseErr)
	}
	request.Host = u.Host
	request.RemoteAddr = "127.0.0.1:1234"
	if actor != "" {
		request.Header.Set(api.ServeLoginHeader, actor)
	}
	if token != "" {
		request.Header.Set(api.CSRFHeader, token)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	c.server.Handler.ServeHTTP(response, request)
	return response
}
func tokenFor(t *testing.T, c *Controller, actor string) string {
	t.Helper()
	r := rawRequest(c, "GET", api.CSRFPath, actor, "", "", "")
	var out api.CSRFResponse
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil {
		t.Fatal("CSRF bootstrap", r.Code, r.Body.String())
	}
	return out.Token
}
func TestHTTPIdentityCSRFAndOwnedCommands(t *testing.T) {
	c, b, server := startFixture(t)
	token := tokenFor(t, c, "owner")
	for _, actor := range []string{"", "unknown", "Owner", "=?utf-8?Q?broken"} {
		if r := rawRequest(c, "GET", "/api/cameras", actor, "", "", ""); r.Code != 403 {
			t.Fatal(actor, r.Code)
		}
	}
	if r := rawRequest(c, "GET", "/api/cameras", "=?utf-8?Q?owner?=", "", "", ""); r.Code != 200 {
		t.Fatal("encoded login", r.Code)
	}
	for _, bad := range []struct{ actor, token, origin string }{{"owner", "", ""}, {"owner", "invalid", ""}, {"alice", token, ""}, {"owner", token, "https://evil.example"}} {
		r := rawRequest(c, "POST", "/api/cameras/room1/diagnose", bad.actor, bad.token, bad.origin, "{}")
		if r.Code != 403 {
			t.Fatal("write protection", r.Code, r.Body.String())
		}
	}
	if r := rawRequest(c, "POST", "/api/cameras/room1/diagnose", "owner", token, c.cfg.Origin, `{"actor":"owner"}`); r.Code != 400 {
		t.Fatal("unknown field", r.Code)
	}
	client, err := cli.NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	list, err := client.List(context.Background())
	if err != nil || len(list.Cameras) != 3 {
		t.Fatal("HTTPS list", err, list)
	}
	diagnosis, err := client.Diagnose(context.Background(), "room1", "")
	if err != nil || len(diagnosis.Streams) != 1 {
		t.Fatal("HTTPS diagnose", err, diagnosis)
	}
	if denied := rawRequest(c, "POST", "/api/cameras/room1/diagnose", "alice", tokenFor(t, c, "alice"), "", "{}"); denied.Code != 403 {
		t.Fatal("command authorization status", denied.Code, denied.Body.String())
	}
	payload := `{"offer":{"type":"offer","sdp":"v=0\r\n"}}`
	r := rawRequest(c, "POST", "/api/cameras/room1/streams/default/sessions", "alice", tokenFor(t, c, "alice"), "", payload)
	var session api.Session
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &session) != nil {
		t.Fatal("create", r.Code, r.Body.String())
	}
	if r = rawRequest(c, "DELETE", "/api/playback/sessions/"+string(session.ID), "bob", tokenFor(t, c, "bob"), "", ""); r.Code != 403 {
		t.Fatal("cross principal close", r.Code)
	}
	b.mu.Lock()
	b.statusFailure = true
	b.mu.Unlock()
	diagnosis, err = client.Diagnose(context.Background(), "room1", "")
	if cs.DescribeError(err).Code != cs.CodeBackendUnavailable || len(diagnosis.Streams) == 0 {
		t.Fatal("diagnostic failure lost", err)
	}
	b.mu.Lock()
	b.statusFailure = false
	b.mu.Unlock()
	reset, err := client.Reconnect(context.Background(), "room1", "default")
	if err != nil || len(reset.Affected) != 2 || len(reset.ClosedSessions) != 1 {
		t.Fatal("shared owned reset", reset, err)
	}
	// The minimal admin module dispatches through the same bus.
	r = rawRequest(c, "POST", "/admin/api/camera/cameras/room1/diagnose", "owner", token, "", "{}")
	if r.Code != 200 {
		t.Fatal("admin module route", r.Code, r.Body.String())
	}
	for _, path := range []string{"/api/missing", "/assets/missing.js", "/admin/missing", "/missing"} {
		if r = rawRequest(c, "GET", path, "owner", "", "", ""); r.Code != 404 {
			t.Fatal("route ownership", path, r.Code)
		}
	}
}
func TestRawIngressAndHeaderAmbiguity(t *testing.T) {
	c, _, _ := startFixture(t)
	u, parseErr := url.Parse(c.cfg.Origin)
	if parseErr != nil {
		panic(parseErr)
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234"; r.Header.Set("X-Forwarded-For", "127.0.0.1") },
		func(r *http.Request) { r.Header.Add(api.ServeLoginHeader, "owner") },
		func(r *http.Request) { r.Host = "evil.example" },
		func(r *http.Request) { r.Header.Add("Origin", c.cfg.Origin); r.Header.Add("Origin", c.cfg.Origin) },
	} {
		request := httptest.NewRequestWithContext(context.Background(), "GET", "/api/cameras", nil)
		request.Host = u.Host
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set(api.ServeLoginHeader, "owner")
		mutate(request)
		rec := httptest.NewRecorder()
		c.server.Handler.ServeHTTP(rec, request)
		if rec.Code != 403 {
			t.Fatal("ambiguous/untrusted ingress", rec.Code)
		}
	}
}
func TestInstallationOwnershipAndUnknownFiles(t *testing.T) {
	cfg := testConfig(t)
	lock, err := acquireInstallation(cfg.Backend.RuntimeDir, cfg.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = acquireInstallation(cfg.Backend.RuntimeDir, cfg.InstallationID); cs.DescribeError(err).Code != cs.CodeControllerConflict {
		t.Fatal("duplicate controller", err)
	}
	workers := filepath.Join(cfg.Backend.RuntimeDir, "workers")
	if err = os.Mkdir(workers, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workers, "unknown")
	if err = os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = lock.retireWorkers(); cs.DescribeError(err).Code != cs.CodeCleanupIncomplete {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("unknown files removed")
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = acquireInstallation(cfg.Backend.RuntimeDir, "another-installation"); cs.DescribeError(err).Code != cs.CodeControllerConflict {
		t.Fatal("identity changed", err)
	}
}

func TestFailedStartupKeepsDiagnosticsAndPreservesFiles(t *testing.T) {
	cfg := testConfig(t)
	backend := newBackend()
	workers := filepath.Join(cfg.Backend.RuntimeDir, "workers")
	if err := os.MkdirAll(workers, 0700); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(workers, "unknown")
	if err := os.WriteFile(unknown, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: []config.Provider{config.EC70Provider()}, VerifyClean: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	eventually(t, func() bool {
		req, e := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+cfg.Listen+"/healthz", nil)
		if e != nil {
			return false
		}
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			return false
		}
		if e = r.Body.Close(); e != nil {
			return false
		}
		return r.StatusCode == http.StatusOK
	})
	if c.ready.Load() {
		t.Fatal("uncertain cleanup admitted playback")
	}
	r := rawRequest(c, "GET", "/api/cameras", "owner", "", "", "")
	if r.Code != 200 {
		t.Fatal("catalog unavailable", r.Code)
	}
	r = rawRequest(c, "POST", "/api/cameras/room1/streams/default/sessions", "owner", tokenFor(t, c, "owner"), "", `{"offer":{"type":"offer","sdp":"v=0\r\n"}}`)
	if r.Code != 503 {
		t.Fatal("admitted on failed startup", r.Code)
	}
	if _, err = os.Stat(unknown); err != nil {
		t.Fatal("unknown state removed")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestUnconfirmedShutdownRetainsInstallationLock(t *testing.T) {
	cfg := testConfig(t)
	backend := newBackend()
	c, err := New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: []config.Provider{config.EC70Provider()}, VerifyClean: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	eventually(t, c.ready.Load)
	backend.mu.Lock()
	backend.resetFailure = true
	backend.mu.Unlock()
	cancel()
	if err = <-done; cs.DescribeError(err).Code != cs.CodeCleanupIncomplete {
		t.Fatal("shutdown outcome", err)
	}
	if _, err = acquireInstallation(cfg.Backend.RuntimeDir, cfg.InstallationID); cs.DescribeError(err).Code != cs.CodeControllerConflict {
		t.Fatal("released uncertain lock", err)
	}
	backend.mu.Lock()
	backend.resetFailure = false
	backend.mu.Unlock()
	if err = c.Close(context.Background()); err != nil {
		t.Fatal("cleanup retry", err)
	}
	lock, err := acquireInstallation(cfg.Backend.RuntimeDir, cfg.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPRequestCancellationAndFailedReset(t *testing.T) {
	c, b, proxy := startFixture(t)
	token := tokenFor(t, c, "owner")
	payload := `{"offer":{"type":"offer","sdp":"v=0\r\n"}}`
	b.mu.Lock()
	b.blockCreate = true
	b.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, proxy.URL+"/api/cameras/room1/streams/default/sessions", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(api.CSRFHeader, token)
	done := make(chan error, 1)
	go func() {
		response, e := proxy.Client().Do(request)
		if response != nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				e = errors.Join(e, closeErr)
			}
		}
		done <- e
	}()
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.creates == 1 })
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("request cancellation", err)
	}
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, v := range b.sessions {
			if v.Resources != cs.PresenceAbsent {
				return false
			}
		}
		return true
	})
	b.mu.Lock()
	b.blockCreate = false
	b.resetFailure = true
	b.mu.Unlock()
	response := rawRequest(c, "POST", "/api/cameras/room1/streams/default/reconnect", "owner", token, "", "")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "cleanup_incomplete") || strings.Contains(response.Body.String(), "PRIVATE_PASSWORD") {
		t.Fatal("failed reset", response.Code, response.Body.String())
	}
	b.mu.Lock()
	b.resetFailure = false
	b.mu.Unlock()
	response = rawRequest(c, "POST", "/api/cameras/room1/streams/default/reconnect", "owner", token, "", "")
	if response.Code != http.StatusOK {
		t.Fatal("reset retry", response.Code, response.Body.String())
	}
	response = rawRequest(c, "POST", "/admin/api/camera/cameras/room1/diagnose", "owner", "", "", "{}")
	if response.Code != http.StatusForbidden {
		t.Fatal("admin CSRF", response.Code)
	}
}

func TestEmbeddedAssetsRetainIngressAuthorization(t *testing.T) {
	c, _, _ := startFixture(t)
	for _, actor := range []string{"", "unknown"} {
		if r := rawRequest(c, "GET", "/", actor, "", "", ""); r.Code != 403 {
			t.Fatal("asset authorization", r.Code)
		}
	}
	r := rawRequest(c, "GET", "/", "owner", "", "", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "/assets/") {
		t.Fatal("embedded entry", r.Code)
	}
	for _, path := range []string{"/api/missing", "/admin/missing", "/assets/missing.js", "/preview.html"} {
		if r := rawRequest(c, "GET", path, "owner", "", "", ""); r.Code != 404 {
			t.Fatal(path, r.Code)
		}
	}
}

// Unknown ownership after a restart remains denied, even with a fresh token.
// The browser must offer a fresh-page recovery path, not retry this ID forever.
func TestReplacementControllerDeniesPreviousSession(t *testing.T) {
	original, _, _ := startFixture(t)
	payload := `{"offer":{"type":"offer","sdp":"v=0\r\n"}}`
	created := rawRequest(original, "POST", "/api/cameras/room1/streams/default/sessions", "alice", tokenFor(t, original, "alice"), "", payload)
	var session api.Session
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &session) != nil {
		t.Fatal("create", created.Code)
	}
	backend := newBackend()
	replacement, err := New(original.cfg, Dependencies{Backend: backend, Runtime: backend, Providers: []config.Provider{config.EC70Provider()}, VerifyClean: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := replacement.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	token := tokenFor(t, replacement, "alice")
	denied := rawRequest(replacement, "DELETE", "/api/playback/sessions/"+string(session.ID), "alice", token, "", "")
	var failure api.ErrorResponse
	if denied.Code != http.StatusForbidden || json.Unmarshal(denied.Body.Bytes(), &failure) != nil || failure.Error.Code != cs.CodeAccessDenied {
		t.Fatal("old ownership must stay denied", denied.Code)
	}
	// A fresh page can read its authorized catalog, but cannot allocate before readiness.
	if r := rawRequest(replacement, "GET", "/api/cameras", "alice", "", "", ""); r.Code != http.StatusOK {
		t.Fatal("catalog", r.Code)
	}
	if r := rawRequest(replacement, "POST", "/api/cameras/room1/streams/default/sessions", "alice", token, "", payload); r.Code != http.StatusServiceUnavailable {
		t.Fatal("readiness", r.Code)
	}
}

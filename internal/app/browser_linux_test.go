//go:build linux

package app

import (
	"context"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
)

// Test-only controlled media ingress. Never linked into the shipped executable.
// Run directly below init in the isolated T12 container. Only localhost ports
// are published; the test ingress replaces identity just like the T07 fixture.
type browserStrategy struct{ fixtureStrategy }

func (browserStrategy) Streams(context.Context, cs.Camera) ([]cs.Stream, error) {
	return []cs.Stream{
		{ID: "default", Name: "Default", Capabilities: cs.MediaCapabilities{Video: true, Audio: true}},
		{ID: "alternate", Name: "Alternate alias", Capabilities: cs.MediaCapabilities{Video: true, Audio: true}},
	}, nil
}
func (browserStrategy) Resolve(ctx context.Context, c cs.Camera, id cs.StreamID) (cs.StreamSpec, error) {
	out, err := (fixtureStrategy{}).Resolve(ctx, c, id)
	out.Capabilities.Audio = true
	return out, err
}

type browserResponse struct {
	http.ResponseWriter
	status int
}

func (w *browserResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func TestT12Browser(t *testing.T) {
	if os.Getenv("CAMSTREAM_T12_BROWSER") != "1" {
		t.Skip("isolated browser fixture")
	}
	cfg := testConfig(t)
	cfg.Origin = "https://127.0.0.1:35400"
	cfg.Backend.RuntimeDir = "/tmp/t12-installation"
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
	cfg.Limits.MaxViewers = 6
	cfg.Limits.PerSourceViewers = 4
	cfg.Limits.MaxSources = 2
	cfg.Backend.MediaPorts = config.Ports{First: 35100, Last: 35105}
	// A second namespace can exercise UDP without interrupting the TCP-only
	// long-lived viewer. Both variants are loopback-published test fixtures.
	ingress := ":35400"
	if os.Getenv("CAMSTREAM_T12_UDP") == "1" {
		cfg.Origin = "https://127.0.0.1:35401"
		cfg.Backend.MediaPorts = config.Ports{First: 35200, Last: 35205}
		ingress = ":35401"
	}
	cfg.Backend.StartupTimeout = 8 * time.Second
	cfg.Backend.FailureTimeout = 8 * time.Second
	cfg.Backend.TermGrace = 100 * time.Millisecond
	for i := range cfg.Cameras {
		cfg.Cameras[i].Strategy = "fixture"
		cfg.Cameras[i].Streams = []cs.StreamID{"default", "alternate"}
	}
	for i := range cfg.Access[0].Grants {
		cfg.Access[0].Grants[i].Streams = []cs.StreamID{"default", "alternate"}
	}
	provider := config.Provider{Strategy: browserStrategy{}, Decode: func(input map[string]any) (config.Decoded, error) {
		host, ok := input["host"].(string)
		if !ok {
			return config.Decoded{}, cs.NewError(cs.CodeInvalidArgument, nil)
		}
		return config.Decoded{Config: fixtureConfig(host), Endpoint: host}, nil
	}}
	backendCfg := cfg.BackendConfig(verifyCleanContainer)
	backendCfg.RuntimeDir = filepath.Join(cfg.Backend.RuntimeDir, "workers")
	backendCfg.AllowExec = true
	backend, err := go2rtc.New(backendCfg, fixtureEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: []config.Provider{provider}, VerifyClean: verifyCleanContainer})
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse("http://" + cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(api.ServeLoginHeader)
		r.Header.Set(api.ServeLoginHeader, "owner")
		recorder := &browserResponse{ResponseWriter: w, status: 200}
		proxy.ServeHTTP(recorder, r)
		fmt.Printf("T12_HTTP %s %s %d\n", r.Method, r.URL.Path, recorder.status)
	}))
	if err = server.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	server.Listener, err = new(net.ListenConfig).Listen(context.Background(), "tcp", ingress)
	if err != nil {
		t.Fatal(err)
	}
	server.StartTLS()
	defer server.Close()
	if err = os.WriteFile("/evidence/ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	eventually(t, c.ready.Load)
	fmt.Println("T12_READY")
	<-ctx.Done()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

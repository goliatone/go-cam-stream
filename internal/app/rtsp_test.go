package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

func mixedConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(context.Background(), "../../config/testdata/mixed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Backend.RuntimeDir = filepath.Join(t.TempDir(), "not-created")
	return cfg
}

func TestDefaultMixedRegistrationAndPublicCatalog(t *testing.T) {
	for _, key := range []string{"T04_KASA_USERNAME", "T04_KASA_PASSWORD", "T04_RTSP_USERNAME", "T04_RTSP_PASSWORD"} {
		t.Setenv(key, "")
	}
	cfg := mixedConfig(t)
	c, err := NewDefault(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := c.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	catalog, err := config.Prepare(context.Background(), cfg, config.DefaultProviders()...)
	if err != nil {
		t.Fatal(err)
	}
	// Construction and listing succeed while credentials are unusable and the
	// configured binary does not exist. Source acquisition below is encoder-only.
	response := rawRequest(c, "GET", "/api/cameras", "owner", "", "", "")
	var listing api.CameraList
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &listing) != nil || len(listing.Cameras) != 3 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/cameras", "/api/cameras/pi/streams", "/api/cameras/pi/status"} {
		r := rawRequest(c, "GET", path, "owner", "", "", "")
		if r.Code != 200 {
			t.Fatal(path, r.Code, r.Body.String())
		}
		assertNoSourceDetails(t, r.Body.String())
	}
	r := rawRequest(c, "GET", "/api/cameras", "viewer", "", "", "")
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &listing) != nil || len(listing.Cameras) != 1 || listing.Cameras[0].ID != "pi" {
		t.Fatal("mixed catalog grants", r.Code, r.Body.String())
	}
	r = rawRequest(c, "GET", "/api/cameras/pi/streams", "viewer", "", "", "")
	var streams api.StreamList
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &streams) != nil || len(streams.Streams) != 1 || streams.Streams[0].ID != "main" {
		t.Fatal("profile grant", r.Code, r.Body.String())
	}
	for _, key := range []string{"T04_KASA_USERNAME", "T04_KASA_PASSWORD", "T04_RTSP_USERNAME", "T04_RTSP_PASSWORD"} {
		t.Setenv(key, "T04_RESOLVED_SECRET")
	}
	for i, providerID := range []cs.StrategyID{"ec70", "rtsp", "rtsp"} {
		item := catalog.Cameras[i]
		var strategy cs.Strategy
		for _, provider := range c.deps.Providers {
			if provider.Strategy.ID() == providerID {
				strategy = provider.Strategy
			}
		}
		if strategy == nil {
			t.Fatal("missing strategy", providerID)
		}
		spec, resolveErr := strategy.Resolve(context.Background(), item.Camera, item.Streams[0].ID)
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		state, ensureErr := c.deps.Backend.EnsureStream(context.Background(), spec)
		if ensureErr != nil || state.Upstream != cs.PresenceAbsent {
			t.Fatal("default encoder registration", providerID, ensureErr)
		}
		data, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		assertNoSourceDetails(t, string(data))
	}
	if _, err = os.Stat(cfg.Backend.RuntimeDir); !os.IsNotExist(err) {
		t.Fatal("offline composition created runtime state")
	}
	if c.ready.Load() || c.started.Load() {
		t.Fatal("construction started runtime")
	}
}

func assertNoSourceDetails(t *testing.T, body string) {
	t.Helper()
	for _, private := range []string{"192.0.2.", "pi.example.test", "private-camera", "private-ip-camera", "channel=", "subtype=", "env://", "T04_", "rtsp://", "kasa://"} {
		if strings.Contains(body, private) {
			t.Fatalf("public result contains source detail %q", private)
		}
	}
}

func TestMixedControllerPreservesExplicitProviders(t *testing.T) {
	cfg := mixedConfig(t)
	backend := newBackend()
	deps := Dependencies{Backend: backend, Runtime: backend, Providers: []config.Provider{config.EC70Provider()}, VerifyClean: func(context.Context) error { return nil }}
	if _, err := New(cfg, deps); cs.DescribeError(err).Code != cs.CodeInvalidArgument {
		t.Fatal("explicit provider list overridden", err)
	}
	deps.Providers = config.DefaultProviders()
	c, err := New(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	if c.deps.Backend != backend {
		t.Fatal("injected backend replaced")
	}
	t.Cleanup(func() {
		if closeErr := c.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	catalog, err := config.Prepare(context.Background(), cfg, deps.Providers...)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.core.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The injected backend returns synthetic SDP. This checks strategy routing
	// and device admission only, without claiming camera or browser playback.
	for _, item := range catalog.Cameras {
		if _, err = c.core.CreateSession(context.Background(), item.Camera, item.Streams[0].ID, cs.Offer{SDP: "v=0\r\n"}); err != nil {
			t.Fatal("mixed source admission", err)
		}
	}
	pi := catalog.Cameras[1].Camera
	if _, err = c.core.CreateSession(context.Background(), pi, "sub", cs.Offer{SDP: "v=0\r\n"}); cs.DescribeError(err).Code != cs.CodeCameraBusy {
		t.Fatal("competing RTSP profile did not share device limit", err)
	}
	if _, err = c.core.CreateSession(context.Background(), pi, "main", cs.Offer{SDP: "v=0\r\n"}); err != nil {
		t.Fatal(err)
	}
}

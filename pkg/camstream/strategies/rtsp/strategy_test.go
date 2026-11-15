package rtsp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	secrets "github.com/goliatone/go-secrets"
)

func config() rtsp.Config {
	return rtsp.Config{Host: "CAMERA.invalid.", Profiles: []rtsp.Profile{
		{ID: "main", Path: "/cam//../main", Query: "channel=1&subtype=0"},
		{ID: "sub", Name: "Substream", Path: "/cam/sub", Audio: true},
	}}
}

func camera(cfg rtsp.Config) camstream.Camera {
	return camstream.Camera{ID: "room", Name: "Room", PhysicalID: "device-1", Config: cfg}
}

func reference(t *testing.T, uri string) secrets.Reference {
	t.Helper()
	ref, err := secrets.ParseReference(uri)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func resolve(t *testing.T, c camstream.Camera, id camstream.StreamID) camstream.StreamSpec {
	t.Helper()
	spec, err := (rtsp.Strategy{}).Resolve(context.Background(), c, id)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func source(t *testing.T, spec camstream.StreamSpec) rtsp.Source {
	t.Helper()
	s, ok := spec.Source.(rtsp.Source)
	if !ok {
		t.Fatal("wrong concrete source type")
	}
	return s
}

func TestProfilesAndDefensiveCopies(t *testing.T) {
	cfg := config()
	normalized, err := cfg.Normalize()
	if err != nil || normalized.Host != "camera.invalid" || normalized.Port != 554 || normalized.Profiles[0].Name != "main" {
		t.Fatal("normalization failed", err)
	}
	if cfg.Profiles[0].Name != "" || cfg.Host != "CAMERA.invalid." || cfg.Port != 0 {
		t.Fatal("normalization mutated input")
	}
	normalized.Profiles[0].Path = "/different"
	if cfg.Profiles[0].Path != "/cam//../main" {
		t.Fatal("normalized config shares input slice")
	}
	c := camera(cfg)
	strategy := rtsp.Strategy{}
	streams, err := strategy.Streams(context.Background(), c)
	if err != nil || strategy.ID() != rtsp.ID || cfg.StrategyID() != rtsp.ID || len(streams) != 2 {
		t.Fatal("streams failed", err)
	}
	if streams[0].ID != "main" || streams[0].Name != "main" || streams[0].Capabilities.Audio ||
		streams[1].ID != "sub" || streams[1].Name != "Substream" || !streams[1].Capabilities.Audio || !streams[0].Capabilities.Video {
		t.Fatal("unexpected profile order/defaults/capabilities")
	}
	streams[0].Name = "changed"
	spec := resolve(t, c, "main")
	s := source(t, spec)
	if s.Kind() != rtsp.SourceKind || spec.PhysicalCameraID != c.PhysicalID || spec.Constraints.MaxActiveSources != 1 || spec.Capabilities != (camstream.MediaCapabilities{Video: true}) {
		t.Fatal("source contract mismatch")
	}
	cfg.Profiles[0].Path = "/mutated-input"
	copyConfig := s.Config()
	if len(copyConfig.Profiles) != 1 || copyConfig.Profiles[0].Path != "/cam//../main" || copyConfig.Profiles[0].Query != "channel=1&subtype=0" {
		t.Fatal("source retained mutable input or lost raw path/query")
	}
	copyConfig.Profiles[0].Path = "/mutated-output"
	copyConfig.Profiles[0].Audio = true
	copyConfig.Profiles = append(copyConfig.Profiles, rtsp.Profile{ID: "extra"})
	if got := s.Config(); len(got.Profiles) != 1 || got.Profiles[0].Path != "/cam//../main" || got.Profiles[0].Audio {
		t.Fatal("source accessor shares state")
	}
	again, err := strategy.Streams(context.Background(), camera(config()))
	if err != nil || again[0].Name != "main" {
		t.Fatal("strategy retained mutable stream state", err)
	}
}

func TestEquivalentAliasesShareIdentity(t *testing.T) {
	cfg := config()
	first := resolve(t, camera(cfg), "main")
	const v1Key = "goliatone.com/camstream/rtsp:39428d7d12a30f62864fbd06238806433b6157aa5a109fcf9cc7db773c35e191"
	if string(first.Key) != v1Key {
		t.Fatal("versioned source identity changed")
	}
	cfg.Host, cfg.Port = "camera.invalid", 554
	cfg.Profiles[0].ID, cfg.Profiles[0].Name = "renamed", "New label"
	cfg.Profiles[1].Path = "/unselected-profile-change"
	c := camera(cfg)
	c.ID, c.Name = "other-alias", "Other display name"
	if first.Key != resolve(t, c, "renamed").Key {
		t.Fatal("labels or unrelated profile affected source key")
	}
	alias := cfg.Profiles[0]
	alias.ID = "duplicate-source"
	cfg.Profiles = append(cfg.Profiles, alias)
	if resolve(t, camera(cfg), "renamed").Key != resolve(t, camera(cfg), "duplicate-source").Key {
		t.Fatal("equivalent profiles do not share a source")
	}
}

func TestDistinctSelectionsAndBindings(t *testing.T) {
	base := config()
	base.UsernameRef = reference(t, "env://RTSP_MISSING_USERNAME")
	base.PasswordRef = reference(t, "env://RTSP_MISSING_PASSWORD")
	first := resolve(t, camera(base), "main")
	changes := map[string]func(*rtsp.Config){
		"host":        func(c *rtsp.Config) { c.Host = "other.invalid" },
		"port":        func(c *rtsp.Config) { c.Port = 8554 },
		"path":        func(c *rtsp.Config) { c.Profiles[0].Path = "/main" },
		"query":       func(c *rtsp.Config) { c.Profiles[0].Query = "channel=1&subtype=1" },
		"query-order": func(c *rtsp.Config) { c.Profiles[0].Query = "subtype=0&channel=1" },
		"audio":       func(c *rtsp.Config) { c.Profiles[0].Audio = true },
		"username":    func(c *rtsp.Config) { c.UsernameRef = reference(t, "env://OTHER_USER") },
		"password":    func(c *rtsp.Config) { c.PasswordRef = reference(t, "env://OTHER_PASS") },
		"anonymous":   func(c *rtsp.Config) { c.UsernameRef, c.PasswordRef = secrets.Reference{}, secrets.Reference{} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			cfg, err := base.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			change(&cfg)
			if resolve(t, camera(cfg), "main").Key == first.Key {
				t.Fatal("distinct source reused key")
			}
		})
	}
	c := camera(base)
	c.PhysicalID = "other-device"
	if resolve(t, c, "main").Key == first.Key || resolve(t, camera(base), "sub").Key == first.Key {
		t.Fatal("physical device or profile difference lost")
	}
	// The strategy cannot resolve credentials: even unavailable custom providers
	// are valid here. Environment changes cannot affect source identity.
	t.Setenv("RTSP_MISSING_PASSWORD", "first-value")
	if resolve(t, camera(base), "main").Key != first.Key {
		t.Fatal("environment affected source identity")
	}
	t.Setenv("RTSP_MISSING_PASSWORD", "rotated-value")
	if resolve(t, camera(base), "main").Key != first.Key {
		t.Fatal("rotation affected source identity")
	}
	base.UsernameRef = reference(t, "unavailable://camera/user")
	base.PasswordRef = reference(t, "unavailable://camera/password")
	resolve(t, camera(base), "main")
}

func TestEndpointNormalization(t *testing.T) {
	for _, tc := range []struct{ host, normalized, endpoint string }{
		{"CAMERA.invalid.", "camera.invalid", "camera.invalid:554"},
		{"2001:0db8::1", "2001:db8::1", "[2001:db8::1]:554"},
		{"::ffff:192.0.2.1", "192.0.2.1", "192.0.2.1:554"},
	} {
		cfg := config()
		cfg.Host = tc.host
		endpoint, err := cfg.Endpoint()
		if err != nil || endpoint != tc.endpoint {
			t.Fatal("endpoint normalization", err)
		}
		first := resolve(t, camera(cfg), "main")
		cfg.Host, cfg.Port = tc.normalized, 554
		if first.Key != resolve(t, camera(cfg), "main").Key {
			t.Fatal("equivalent endpoints have distinct keys")
		}
		cfg.Profiles[0].Path = "/another"
		cfg.UsernameRef = reference(t, "env://U")
		cfg.PasswordRef = reference(t, "env://P")
		if got, err := cfg.Endpoint(); err != nil || got != endpoint {
			t.Fatal("profile/auth affected endpoint", err)
		}
	}
}

type foreignConfig struct{}

func (foreignConfig) StrategyID() camstream.StrategyID { return rtsp.ID }

func TestInvalidCameraAndCancellation(t *testing.T) {
	strategy := rtsp.Strategy{}
	for _, cfg := range []camstream.CameraConfig{nil, foreignConfig{}, (*rtsp.Config)(nil), &rtsp.Config{}, rtsp.Config{}} {
		c := camera(config())
		c.Config = cfg
		_, err := strategy.Streams(context.Background(), c)
		assertCode(t, err, camstream.CodeInvalidArgument)
		_, err = strategy.Resolve(context.Background(), c, "main")
		assertCode(t, err, camstream.CodeInvalidArgument)
	}
	for _, id := range []string{"", " padded", "newline\n", strings.Repeat("a", 257), "bad\xff"} {
		c := camera(config())
		c.ID = camstream.CameraID(id)
		_, err := strategy.Streams(context.Background(), c)
		assertCode(t, err, camstream.CodeInvalidArgument)
		c = camera(config())
		c.PhysicalID = camstream.PhysicalCameraID(id)
		_, err = strategy.Resolve(context.Background(), c, "main")
		assertCode(t, err, camstream.CodeInvalidArgument)
	}
	for _, id := range []camstream.StreamID{"", "unknown", "MAIN"} {
		_, err := strategy.Resolve(context.Background(), camera(config()), id)
		assertCode(t, err, camstream.CodeUnsupported)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, ctx := range []context.Context{canceled, expired} {
		_, err := strategy.Streams(ctx, camera(config()))
		if !errors.Is(err, ctx.Err()) {
			t.Fatal("lost context error", err)
		}
		_, err = strategy.Resolve(ctx, camera(config()), "main")
		if !errors.Is(err, ctx.Err()) {
			t.Fatal("lost context error", err)
		}
	}
	if (rtsp.Source{}).Config().Validate() == nil {
		t.Fatal("zero source accepted")
	}
}

func assertCode(t *testing.T, err error, code camstream.ErrorCode) {
	t.Helper()
	if camstream.DescribeError(err).Code != code {
		t.Fatalf("expected %s; got %v", code, err)
	}
}

func TestPrivateValuesStayOutOfFormatting(t *testing.T) {
	cfg := config()
	cfg.UsernameRef = reference(t, "env://PRIVATE_USER")
	cfg.PasswordRef = reference(t, "env://PRIVATE_PASSWORD")
	spec := resolve(t, camera(cfg), "main")
	for _, value := range []any{cfg, cfg.Profiles[0], spec.Source, spec, camera(cfg)} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		out := fmt.Sprintf("%v %+v %#v %s", value, value, value, encoded)
		for _, secret := range []string{cfg.Host, "camera.invalid", cfg.Profiles[0].Path, cfg.Profiles[0].Query, "PRIVATE_USER", "PRIVATE_PASSWORD"} {
			if strings.Contains(out, secret) {
				t.Fatal("private source material escaped")
			}
		}
	}
	cfg.Profiles[0].Query = "private=\nsecret"
	err := cfg.Validate()
	assertCode(t, err, camstream.CodeInvalidArgument)
	if strings.Contains(fmt.Sprintf("%+v", err), "secret") {
		t.Fatal("validation exposed rejected input")
	}
}

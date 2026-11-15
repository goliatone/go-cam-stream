package config_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/config"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

func mixed(t *testing.T) config.Config {
	t.Helper()
	raw, err := config.Load(context.Background(), "testdata/mixed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDefaultProvidersOfflineMixedSnapshot(t *testing.T) {
	// Empty values would fail either encoder; preparation must not resolve them.
	for _, key := range []string{"T04_KASA_USERNAME", "T04_KASA_PASSWORD", "T04_RTSP_USERNAME", "T04_RTSP_PASSWORD"} {
		t.Setenv(key, "")
	}
	raw := mixed(t)
	raw.Backend.RuntimeDir = filepath.Join(t.TempDir(), "not-created")
	raw.Limits.MaxSources = 4
	alias := raw.Cameras[1]
	alias.ID = "pi-alias"
	alias.Name = "Alias"
	alias.Config = map[string]any{"host": "pi.example.test", "port": 554, "profiles": []map[string]any{{"id": "different-label", "path": "/private-camera", "query": "channel=1&subtype=0"}}}
	raw.Cameras = append(raw.Cameras, alias)
	catalog, err := config.Prepare(context.Background(), raw, config.DefaultProviders()...)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Cameras) != 4 || catalog.Cameras[1].Sources["main"] != catalog.Cameras[3].Sources["different-label"] {
		t.Fatal("mixed catalog alias identity")
	}
	typed, ok := catalog.Cameras[1].Camera.Config.(rtsp.Config)
	if !ok || typed.Host != "pi.example.test" || typed.Port != 554 || typed.Profiles[0].Name != "main" || typed.Profiles[0].Audio || !typed.Profiles[1].Audio {
		t.Fatal("RTSP defaults/normalization")
	}
	auth, ok := catalog.Cameras[2].Camera.Config.(rtsp.Config)
	if !ok || auth.Port != 8554 || auth.PasswordRef.URI() != "env://T04_RTSP_PASSWORD" {
		t.Fatal("unresolved RTSP binding")
	}
	raw.Cameras[1].Config["host"] = "changed.example"
	profiles, ok := raw.Cameras[1].Config["profiles"].([]any)
	if !ok || len(profiles) == 0 {
		t.Fatal("missing raw profiles")
	}
	profile, ok := profiles[0].(map[string]any)
	if !ok {
		t.Fatal("missing raw profile fields")
	}
	profile["path"] = "/changed"
	raw.Access[0].Grants[1].Streams[0] = "changed"
	if typed.Host != "pi.example.test" || typed.Profiles[0].Path != "/private-camera" || catalog.Access[0].Grants[1].Streams[0] != "main" {
		t.Fatal("snapshot aliases mutable input")
	}
	if _, err = os.Stat(raw.Backend.RuntimeDir); !os.IsNotExist(err) {
		t.Fatal("offline preparation created runtime state")
	}
	providers := config.DefaultProviders()
	providers[0] = config.RTSPProvider()
	if _, err = config.Prepare(context.Background(), example(t), config.DefaultProviders()...); err != nil {
		t.Fatal("EC70 compatibility or shared mutable provider list", err)
	}
}

func TestRTSPStrictConfiguration(t *testing.T) {
	cases := map[string]func(map[string]any){
		"unknown field": func(c map[string]any) { c["url"] = "rtsp://DO_NOT_EXPOSE" },
		"unknown profile field": func(c map[string]any) {
			c["profiles"] = []map[string]any{{"id": "main", "path": "/camera", "url": "DO_NOT_EXPOSE"}}
		},
		"zero port":        func(c map[string]any) { c["port"] = 0 },
		"null port":        func(c map[string]any) { c["port"] = nil },
		"negative port":    func(c map[string]any) { c["port"] = -1 },
		"overflow port":    func(c map[string]any) { c["port"] = 65536 },
		"string port":      func(c map[string]any) { c["port"] = "554" },
		"fractional port":  func(c map[string]any) { c["port"] = 554.5 },
		"inline auth":      func(c map[string]any) { c["password"] = "DO_NOT_EXPOSE" },
		"partial auth":     func(c map[string]any) { c["username_ref"] = "env://T04_USERNAME" },
		"null auth":        func(c map[string]any) { c["username_ref"], c["password_ref"] = nil, nil },
		"empty auth":       func(c map[string]any) { c["username_ref"], c["password_ref"] = "", "" },
		"unsupported auth": func(c map[string]any) { c["username_ref"], c["password_ref"] = "vault://USER", "env://PASS" },
		"malformed auth":   func(c map[string]any) { c["username_ref"], c["password_ref"] = "env://USER", "DO_NOT_EXPOSE" },
		"missing profiles": func(c map[string]any) { delete(c, "profiles") },
		"empty profiles":   func(c map[string]any) { c["profiles"] = []any{} },
		"duplicate profiles": func(c map[string]any) {
			c["profiles"] = []map[string]any{{"id": "main", "path": "/one"}, {"id": "main", "path": "/two"}}
		},
		"invalid profile ID": func(c map[string]any) { c["profiles"] = []map[string]any{{"id": "main/path", "path": "/one"}} },
		"profile capacity": func(c map[string]any) {
			profiles := []map[string]any{}
			for i := 0; i <= rtsp.MaxProfiles; i++ {
				profiles = append(profiles, map[string]any{"id": fmt.Sprintf("p%d", i), "path": "/one"})
			}
			c["profiles"] = profiles
		},
		"source expression": func(c map[string]any) { c["host"] = "exec:DO_NOT_EXPOSE" },
		"path fragment":     func(c map[string]any) { c["profiles"] = []map[string]any{{"id": "main", "path": "/one#DO_NOT_EXPOSE"}} },
		"wrong audio type": func(c map[string]any) {
			c["profiles"] = []map[string]any{{"id": "main", "path": "/one", "audio": "true"}}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			raw := mixed(t)
			change(raw.Cameras[1].Config)
			_, err := config.Prepare(context.Background(), raw, config.DefaultProviders()...)
			if cs.DescribeError(err).Code != cs.CodeInvalidArgument || strings.Contains(fmt.Sprintf("%+v", err), "DO_NOT_EXPOSE") {
				t.Fatal("unsafe or missing rejection", err)
			}
		})
	}
}

func TestMixedSelectionGrantsIdentityAndCapacity(t *testing.T) {
	cases := map[string]func(*config.Config){
		"unknown strategy":             func(c *config.Config) { c.Cameras[1].Strategy = "unknown" },
		"unknown selection":            func(c *config.Config) { c.Cameras[1].Streams = []cs.StreamID{"missing"} },
		"duplicate selection":          func(c *config.Config) { c.Cameras[1].Streams = []cs.StreamID{"main", "main"} },
		"grant unselected stream":      func(c *config.Config) { c.Cameras[1].Streams = []cs.StreamID{"main"} },
		"grant wrong provider profile": func(c *config.Config) { c.Access[0].Grants[1].Streams = []cs.StreamID{"default"} },
		"grant missing camera":         func(c *config.Config) { c.Access[0].Grants[1].CameraID = "missing" },
		"source capacity":              func(c *config.Config) { c.Limits.MaxSources = 3 },
		"endpoint claims two devices": func(c *config.Config) {
			c.Cameras[2].Config["host"] = "pi.example.test"
			delete(c.Cameras[2].Config, "port")
		},
		"device claims two endpoints": func(c *config.Config) { c.Cameras[2].PhysicalID = c.Cameras[1].PhysicalID },
		"cross strategy device":       func(c *config.Config) { c.Cameras[1].PhysicalID = c.Cameras[0].PhysicalID },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			raw := mixed(t)
			change(&raw)
			if _, err := config.Prepare(context.Background(), raw, config.DefaultProviders()...); cs.DescribeError(err).Code != cs.CodeInvalidArgument {
				t.Fatal(err)
			}
		})
	}
	raw := mixed(t)
	raw.Cameras[1].Streams = []cs.StreamID{"main"}
	raw.Access[0].Grants[1].Streams = []cs.StreamID{"main"}
	raw.Limits.MaxSources = 3
	catalog, err := config.Prepare(context.Background(), raw, config.DefaultProviders()...)
	if err != nil || len(catalog.Cameras[1].Streams) != 1 {
		t.Fatal("valid selection", err)
	}
	if _, err = config.Prepare(context.Background(), raw, config.EC70Provider()); err == nil {
		t.Fatal("explicit providers silently gained defaults")
	}
}

// Both shipped examples must remain offline-valid as documentation evolves.
func TestShippedMixedExamples(t *testing.T) {
	for _, path := range []string{"config.example.yaml", "../deploy/config.example.yaml"} {
		raw, err := config.Load(context.Background(), path)
		if err != nil {
			t.Fatal(path, err)
		}
		catalog, err := config.Prepare(context.Background(), raw, config.DefaultProviders()...)
		if err != nil {
			t.Fatal(path, err)
		}
		kinds := map[cs.StrategyID]bool{}
		for _, camera := range catalog.Cameras {
			kinds[camera.Camera.Config.StrategyID()] = true
		}
		if !kinds["ec70"] || !kinds["rtsp"] {
			t.Fatal(path, "lost mixed example")
		}
	}
}

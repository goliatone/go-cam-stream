package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
)

func example(t *testing.T) config.Config {
	t.Helper()
	raw, err := config.Load(context.Background(), "testdata/ec70.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestExampleOfflinePreparationAndNormalizedAliases(t *testing.T) {
	raw := example(t)
	raw.Backend.RuntimeDir = filepath.Join(t.TempDir(), "not-created")
	alias := raw.Cameras[0]
	alias.ID = "room1-alias"
	alias.Config = map[string]any{"host": "CAMERA.example.", "username_ref": "env://KASA_USERNAME", "password_ref": "env://KASA_PASSWORD"}
	raw.Cameras[0].Config["host"] = "camera.example"
	raw.Cameras = append(raw.Cameras, alias)
	catalog, err := config.Prepare(context.Background(), raw, config.EC70Provider())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Cameras) != 6 || catalog.Cameras[0].Sources["default"] != catalog.Cameras[5].Sources["default"] {
		t.Fatal("catalog or normalized alias identity")
	}
	typed, ok := catalog.Cameras[0].Camera.Config.(ec70.Config)
	if !ok || typed.UsernameRef.URI() != "env://KASA_USERNAME" {
		t.Fatal("typed unresolved binding")
	}
	if _, err = os.Stat(raw.Backend.RuntimeDir); !os.IsNotExist(err) {
		t.Fatal("offline validation created runtime state")
	}
	if raw.BackendConfig(nil).VerifyBoundary != nil || raw.BackendConfig(nil).AllowExec {
		t.Fatal("configuration manufactured containment or enabled exec")
	}
	raw.Access[0].Grants[0].Streams[0] = "changed"
	if catalog.Access[0].Grants[0].Streams[0] != "default" {
		t.Fatal("permission snapshot aliases input")
	}
}
func TestRejectsInvalidCatalogAndCapacity(t *testing.T) {
	cases := map[string]func(*config.Config){
		"installation":                   func(c *config.Config) { c.InstallationID = "" },
		"origin":                         func(c *config.Config) { c.Origin = "http://unsafe.example" },
		"public listener":                func(c *config.Config) { c.Listen = "0.0.0.0:8080" },
		"port collision":                 func(c *config.Config) { c.Listen = "127.0.0.1:35001" },
		"media pool":                     func(c *config.Config) { c.Backend.MediaPorts.Last = c.Backend.MediaPorts.First },
		"private pool":                   func(c *config.Config) { c.Backend.PrivatePorts.Last = c.Backend.PrivatePorts.First + 4 },
		"negative capacity":              func(c *config.Config) { c.Limits.MaxViewers = -1 },
		"short cleanup":                  func(c *config.Config) { c.Limits.CleanupTimeout = time.Millisecond },
		"short negotiation":              func(c *config.Config) { c.Limits.NegotiationTimeout = time.Millisecond },
		"unknown backend":                func(c *config.Config) { c.Backend.Driver = "other" },
		"unknown strategy":               func(c *config.Config) { c.Cameras[0].Strategy = "other" },
		"duplicate camera":               func(c *config.Config) { c.Cameras = append(c.Cameras, c.Cameras[0]) },
		"same endpoint different device": func(c *config.Config) { c.Cameras[1].Config["host"] = c.Cameras[0].Config["host"] },
		"same device different endpoint": func(c *config.Config) { c.Cameras[1].PhysicalID = c.Cameras[0].PhysicalID },
		"unknown profile":                func(c *config.Config) { c.Cameras[0].Streams = []cs.StreamID{"sub"} },
		"duplicate profile":              func(c *config.Config) { c.Cameras[0].Streams = []cs.StreamID{"default", "default"} },
		"inline secret":                  func(c *config.Config) { c.Cameras[0].Config["password"] = "DO_NOT_EXPOSE" },
		"bad reference":                  func(c *config.Config) { c.Cameras[0].Config["password_ref"] = "DO_NOT_EXPOSE" },
		"unsupported secret provider":    func(c *config.Config) { c.Cameras[0].Config["password_ref"] = "vault://secret" },
		"grant unknown camera":           func(c *config.Config) { c.Access[0].Grants[0].CameraID = "missing" },
		"grant unknown profile":          func(c *config.Config) { c.Access[0].Grants[0].Streams = []cs.StreamID{"sub"} },
		"duplicate login":                func(c *config.Config) { c.Access = append(c.Access, c.Access[0]) },
		"wildcard login":                 func(c *config.Config) { c.Access[0].Login = "*" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			raw := example(t)
			change(&raw)
			_, err := config.Prepare(context.Background(), raw, config.EC70Provider())
			if cs.DescribeError(err).Code != cs.CodeInvalidArgument {
				t.Fatal(err)
			}
			if strings.Contains(fmt.Sprintf("%+v", err), "DO_NOT_EXPOSE") {
				t.Fatal("input leaked")
			}
		})
	}
}
func TestStrictLoadingDoesNotExpandValues(t *testing.T) {
	body, err := os.ReadFile("config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("T06_SECRET", "DO_NOT_EXPOSE")
	path := filepath.Join(t.TempDir(), "config.yaml")
	body = []byte(strings.Replace(string(body), "name: Room 1", "name: '@env://T06_SECRET'", 1))
	if err = os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := config.Load(context.Background(), path)
	if err != nil || raw.Cameras[0].Name != "@env://T06_SECRET" {
		t.Fatal("expanded config value", err)
	}
	if err = os.WriteFile(path, append(body, []byte("\nunknown_key: DO_NOT_EXPOSE\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = config.Load(context.Background(), path)
	if err == nil || errors.Unwrap(err) != nil || strings.Contains(fmt.Sprintf("%+v", err), "DO_NOT_EXPOSE") {
		t.Fatal("unsafe strict decode failure")
	}
	_, err = config.Load(context.Background(), path+".missing")
	if err == nil {
		t.Fatal("missing explicit config silently accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = config.Load(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type customConfig string

func (customConfig) StrategyID() cs.StrategyID { return "custom" }

type customSource string

func (customSource) Kind() cs.SourceKind { return "example.test/custom" }

type customStrategy struct{}

func (customStrategy) ID() cs.StrategyID { return "custom" }
func (customStrategy) Streams(context.Context, cs.Camera) ([]cs.Stream, error) {
	return []cs.Stream{{ID: "main", Capabilities: cs.MediaCapabilities{Video: true}}}, nil
}
func (customStrategy) Resolve(_ context.Context, camera cs.Camera, _ cs.StreamID) (cs.StreamSpec, error) {
	return cs.StreamSpec{Key: cs.SourceKey(camera.PhysicalID), PhysicalCameraID: camera.PhysicalID, Source: customSource("private"), Capabilities: cs.MediaCapabilities{Video: true}}, nil
}
func TestInjectedProviderAndEmptyCatalog(t *testing.T) {
	raw := example(t)
	raw.Cameras = nil
	raw.Access = nil
	catalog, err := config.Prepare(context.Background(), raw)
	if err != nil || catalog.Cameras == nil || len(catalog.Cameras) != 0 {
		t.Fatal(catalog, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = config.Prepare(canceled, raw); !errors.Is(err, context.Canceled) {
		t.Fatal("empty catalog ignored cancellation", err)
	}
	raw.Cameras = []config.Camera{{ID: "custom", Name: "Custom", PhysicalID: "custom-one", Strategy: "custom", Config: map[string]any{"endpoint": "private"}}}
	provider := config.Provider{Strategy: customStrategy{}, Decode: func(input map[string]any) (config.Decoded, error) {
		body, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return config.Decoded{}, marshalErr
		}
		return config.Decoded{Config: customConfig(body), Endpoint: "device"}, nil
	}}
	catalog, err = config.Prepare(context.Background(), raw, provider)
	if err != nil || catalog.Cameras[0].Sources["main"] != "custom-one" {
		t.Fatal(err)
	}
	_, err = config.Prepare(context.Background(), raw, provider, provider)
	if err == nil {
		t.Fatal("duplicate decoder accepted")
	}
}

func TestManagementConfiguration(t *testing.T) {
	for _, m := range []config.Management{{}, {Enabled: true, Principals: []string{"manager"}, StateDir: "/private/managed", DestinationCIDRs: []string{"203.0.113.0/24"}}} {
		if m.Validate() != nil {
			t.Fatal("valid management policy rejected")
		}
	}
	for _, m := range []config.Management{{Enabled: true}, {StateDir: "relative"}, {Principals: []string{"*"}}, {Principals: []string{"same", "same"}}, {DestinationCIDRs: []string{"bad"}}, {DestinationCIDRs: []string{"203.0.113.1/24"}}} {
		if m.Validate() == nil {
			t.Fatal("invalid management policy accepted")
		}
	}
}

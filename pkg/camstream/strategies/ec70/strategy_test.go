package ec70_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
	secrets "github.com/goliatone/go-secrets"
)

func reference(t *testing.T, uri string) secrets.Reference {
	t.Helper()
	ref, err := secrets.ParseReference(uri)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
func camera(t *testing.T) camstream.Camera {
	t.Helper()
	return camstream.Camera{ID: "room1", PhysicalID: "device-one", Config: ec70.Config{Host: "CAMERA.Example.",
		UsernameRef: reference(t, "env://KASA_USERNAME"), PasswordRef: reference(t, "env://KASA_PASSWORD")}}
}
func resolve(t *testing.T, c camstream.Camera) camstream.StreamSpec {
	t.Helper()
	spec, err := (ec70.Strategy{}).Resolve(context.Background(), c, ec70.DefaultStream)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestDefaultProfileAndCanonicalIdentity(t *testing.T) {
	c := camera(t)
	strategy := ec70.Strategy{}
	streams, err := strategy.Streams(context.Background(), c)
	if err != nil || len(streams) != 1 || streams[0].ID != ec70.DefaultStream || strategy.ID() != ec70.ID {
		t.Fatal(streams, err)
	}
	first := resolve(t, c)
	alias := c
	alias.ID, alias.Name = "hall", "Same camera"
	cfg, ok := alias.Config.(ec70.Config)
	if !ok {
		t.Fatal("config type")
	}
	cfg.Host, cfg.Port = "camera.example", ec70.DefaultPort
	alias.Config = cfg
	second := resolve(t, alias)
	if first.Key != second.Key || first.PhysicalCameraID != c.PhysicalID || first.Constraints.MaxActiveSources != 1 || !first.Capabilities.Video || !first.Capabilities.Audio {
		t.Fatal("alias identity, device constraint or expected media mismatch")
	}
	if strings.Contains(string(first.Key), "KASA_USERNAME") || strings.Contains(string(first.Key), cfg.Host) {
		t.Fatal("source key exposes private configuration")
	}
	for _, change := range []func(*camstream.Camera, *ec70.Config){
		func(c *camstream.Camera, _ *ec70.Config) { c.PhysicalID = "other-device" },
		func(_ *camstream.Camera, c *ec70.Config) { c.Host = "other.example" },
		func(_ *camstream.Camera, c *ec70.Config) { c.Port++ },
		func(_ *camstream.Camera, c *ec70.Config) { c.UsernameRef = reference(t, "env://OTHER_USER") },
		func(_ *camstream.Camera, c *ec70.Config) { c.PasswordRef = reference(t, "env://OTHER_PASSWORD") },
	} {
		changed, config := alias, cfg
		change(&changed, &config)
		changed.Config = config
		if resolve(t, changed).Key == first.Key {
			t.Fatal("distinct source reused a key")
		}
	}
	streams[0].Name = "mutated"
	again, err := strategy.Streams(context.Background(), c)
	if err != nil || again[0].Name != "Default" {
		t.Fatal("mutable strategy state", err)
	}
}

func TestHostNormalization(t *testing.T) {
	for _, pair := range [][2]string{{"2001:0db8::1", "2001:db8::1"}, {"::ffff:192.0.2.1", "192.0.2.1"}, {"CAMERA.local.", "camera.local"}} {
		c := camera(t)
		cfg, ok := c.Config.(ec70.Config)
		if !ok {
			t.Fatal("config type")
		}
		cfg.Host = pair[0]
		c.Config = cfg
		first := resolve(t, c)
		cfg.Host = pair[1]
		c.Config = cfg
		if first.Key != resolve(t, c).Key {
			t.Fatal("equivalent addresses differ")
		}
	}
}

type foreignConfig struct{}

func (foreignConfig) StrategyID() camstream.StrategyID { return ec70.ID }

func TestRejectedConfigurationAndProfiles(t *testing.T) {
	for _, host := range []string{"", "https://camera", "user@camera", "camera:19443", "camera/path", "camera?x", "camera#x", " camera", "a..b", "-a", "a-", "[::1]", "fe80::1%en0", "0.0.0.0", "224.0.0.1"} {
		c := camera(t)
		cfg, ok := c.Config.(ec70.Config)
		if !ok {
			t.Fatal("config type")
		}
		cfg.Host = host
		c.Config = cfg
		_, err := (ec70.Strategy{}).Resolve(context.Background(), c, ec70.DefaultStream)
		if camstream.DescribeError(err).Code != camstream.CodeInvalidArgument {
			t.Fatalf("host %q accepted", host)
		}
	}
	c := camera(t)
	for _, config := range []camstream.CameraConfig{nil, foreignConfig{}, (*ec70.Config)(nil), &ec70.Config{}, ec70.Config{Host: "camera"}} {
		c.Config = config
		_, err := (ec70.Strategy{}).Streams(context.Background(), c)
		if camstream.DescribeError(err).Code != camstream.CodeInvalidArgument {
			t.Fatal("invalid config accepted")
		}
	}
	for _, field := range []string{"id", "physical"} {
		c = camera(t)
		if field == "id" {
			c.ID = ""
		} else {
			c.PhysicalID = ""
		}
		_, err := (ec70.Strategy{}).Streams(context.Background(), c)
		if camstream.DescribeError(err).Code != camstream.CodeInvalidArgument {
			t.Fatal("missing identity accepted")
		}
	}
	for _, profile := range []camstream.StreamID{"", "substream", "DEFAULT"} {
		_, err := (ec70.Strategy{}).Resolve(context.Background(), camera(t), profile)
		if camstream.DescribeError(err).Code != camstream.CodeUnsupported {
			t.Fatal("unknown profile accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (ec70.Strategy{}).Resolve(ctx, camera(t), ec70.DefaultStream)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

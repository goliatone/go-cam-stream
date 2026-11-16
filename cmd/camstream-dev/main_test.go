//go:build camstream_dev

package main

import (
	"testing"

	"github.com/goliatone/go-cam-stream/config"
)

func TestOverridePort(t *testing.T) {
	for _, tc := range []struct{ listen, port, want string }{
		{"127.0.0.1:8081", "", "127.0.0.1:8081"},
		{"127.0.0.1:8081", "8087", "127.0.0.1:8087"},
		{"[::1]:9000", "08087", "[::1]:8087"},
	} {
		cfg := config.Config{Listen: tc.listen, Origin: "https://example.test"}
		if err := overridePort(&cfg, tc.port); err != nil || cfg.Listen != tc.want || cfg.Origin != "https://example.test" {
			t.Fatalf("override %q: config=%+v err=%v", tc.port, cfg, err)
		}
	}
	for _, port := range []string{"0", "65536", "-1", "+80", "80x", " 80", "9999999999999999999"} {
		cfg := config.Config{Listen: "127.0.0.1:8081"}
		if err := overridePort(&cfg, port); err == nil || cfg.Listen != "127.0.0.1:8081" {
			t.Fatalf("invalid port %q admitted: %+v", port, cfg)
		}
	}
	// An override cannot make a non-loopback host valid or bypass worker port exclusions.
	for _, tc := range []struct{ listen, port string }{{"0.0.0.0:8081", "8087"}, {"127.0.0.1:8081", "35000"}} {
		cfg, err := config.Load(t.Context(), "../../config/config.example.yaml")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Listen = tc.listen
		if err = overridePort(&cfg, tc.port); err != nil {
			t.Fatal(err)
		}
		if cfg.Validate() == nil {
			t.Fatal("invalid effective listener admitted", cfg.Listen)
		}
	}
}

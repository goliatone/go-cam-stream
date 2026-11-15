package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/internal/transport/cli"
)

func TestMixedOfflineValidation(t *testing.T) {
	for _, key := range []string{"T04_KASA_USERNAME", "T04_KASA_PASSWORD", "T04_RTSP_USERNAME", "T04_RTSP_PASSWORD"} {
		t.Setenv(key, "")
	}
	body, err := os.ReadFile("../../../config/testdata/mixed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"valid":                string(body),
		"unknown strategy":     strings.Replace(string(body), "strategy: rtsp", "strategy: unknown", 1),
		"unknown field":        strings.Replace(string(body), "port: 8554", "url: rtsp://DO_NOT_EXPOSE", 1),
		"unknown nested field": strings.Replace(string(body), "audio: true", "unknown: DO_NOT_EXPOSE", 1),
		"zero port":            strings.Replace(string(body), "port: 8554", "port: 0", 1),
		"partial auth":         strings.Replace(string(body), "      password_ref: env://T04_RTSP_PASSWORD\n", "", 1),
		"bad profile grant":    strings.Replace(string(body), "streams: [main, sub]", "streams: [missing]", 1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err = os.WriteFile(path, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			code := cli.Run(context.Background(), []string{"config", "validate", "--config", path}, &out, &diagnostics, nil)
			if name == "valid" {
				if code != 0 || out.String() != "{\"valid\":true}\n" || diagnostics.Len() != 0 {
					t.Fatal(code, out.String(), diagnostics.String())
				}
			} else if code == 0 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "invalid_argument") {
				t.Fatal(code, out.String(), diagnostics.String())
			}
			for _, private := range []string{"DO_NOT_EXPOSE", "192.0.2.", "private-camera", "env://", "T04_"} {
				if strings.Contains(out.String()+diagnostics.String(), private) {
					t.Fatal("CLI leaked source details")
				}
			}
		})
	}
}

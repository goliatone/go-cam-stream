package viewer

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

func TestExactMacImport(t *testing.T) {
	body, err := os.ReadFile("../../data/api/management_examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]struct {
		Value json.RawMessage `json:"value"`
	}
	if err = json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	var request api.ImportDraftRequest
	if err = json.Unmarshal(fixtures["import_with_env"].Value, &request); err != nil {
		t.Fatal(err)
	}
	input, err := importCamera(request)
	if err != nil || input.Credentials == nil || input.Endpoint != "rtsp://localhost:18556/macbook" {
		t.Fatal("export failed", err)
	}
	request.Env = ""
	input, err = importCamera(request)
	if err != nil || input.Credentials != nil {
		t.Fatal("YAML-only import", err)
	}
	for _, env := range []string{"export MAC_CAMERA_USERNAME=camera\n", "MAC_CAMERA_USERNAME=$(id)\n", "MAC_CAMERA_PASSWORD=${SECRET}\n", "MAC_CAMERA_USERNAME=camera\nMAC_CAMERA_USERNAME=camera\n"} {
		request.Env = env
		if _, err = importCamera(request); err == nil {
			t.Fatal("shell/duplicate input accepted")
		}
	}
	request.Env = ""
	original := request.YAML
	for _, yaml := range []string{original + "\n---\ncameras: []", strings.Replace(original, "    strategy: rtsp", "    strategy: rtsp\n    strategy: rtsp", 1), strings.Replace(original, "name: MacBook Camera", "name: &name MacBook Camera", 1), strings.Replace(original, "audio: false", "audio: true", 1), strings.Replace(original, "env://MAC_CAMERA_PASSWORD", "file:///etc/passwd", 1)} {
		request.YAML = yaml
		if _, err = importCamera(request); err == nil {
			t.Fatal("unsupported YAML accepted")
		}
	}
}
func TestDestinationBindingPinnedAndConservative(t *testing.T) {
	policy, _ := NewManagementPolicy(config.Management{}, "installation")
	lookup := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("fd00::1"), netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.0.0.1")}, nil
	}
	binding, err := resolveBinding(context.Background(), policy, lookup, "camera.local", 554)
	if err != nil || binding.Selected != "10.0.0.1" || len(binding.Addresses) != 3 {
		t.Fatal("unstable selection", err)
	}
	mixed := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("8.8.8.8")}, nil
	}
	if _, err = resolveBinding(context.Background(), policy, mixed, "camera.local", 554); err == nil {
		t.Fatal("mixed DNS accepted")
	}
	local, err := resolveBinding(context.Background(), policy, mixed, "localhost", 18556)
	if err != nil || local.Selected != "127.0.0.1" {
		t.Fatal("localhost did not select IPv4")
	}
}

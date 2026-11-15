package go2rtc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

type testSource string

func (testSource) Kind() camstream.SourceKind { return "test/rtsp" }

type testEncoder struct{}

func (testEncoder) Kind() camstream.SourceKind { return "test/rtsp" }
func (testEncoder) Encode(ctx context.Context, s camstream.Source) (Input, error) {
	if err := ctx.Err(); err != nil {
		return Input{}, err
	}
	v, ok := s.(testSource)
	if !ok {
		return Input{}, errors.New("unexpected source type")
	}
	return Input{URL: string(v)}, nil
}
func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{Executable: "/usr/local/bin/go2rtc", RuntimeDir: filepath.Join(t.TempDir(), "runtime"), PrivatePorts: PortRange{35000, 35099}, MediaPorts: PortRange{35100, 35115}, MediaAddress: "127.0.0.1", CandidateAddress: "127.0.0.1", VerifyBoundary: ContainerBoundary, StartupTimeout: 4 * time.Second, FailureTimeout: 2 * time.Second, CleanupTimeout: 2 * time.Second, TermGrace: 100 * time.Millisecond, PollInterval: 100 * time.Millisecond}
}
func testSpec(key, url string) camstream.StreamSpec {
	return camstream.StreamSpec{Key: camstream.SourceKey(key), PhysicalCameraID: camstream.PhysicalCameraID(key), Source: testSource(url), Capabilities: camstream.MediaCapabilities{Video: true, Audio: true}}
}
func requireCode(t *testing.T, err error, code camstream.ErrorCode) {
	t.Helper()
	if err == nil || camstream.DescribeError(err).Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}
func TestConstructorDefinitionsAndNeutralContracts(t *testing.T) {
	cfg := testConfig(t)
	b, err := New(cfg, testEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(cfg.RuntimeDir); !os.IsNotExist(err) {
		t.Fatal("constructor performed runtime I/O")
	}
	ctx := context.Background()
	spec := testSpec("one", "rtsp://example.test/stream")
	first, err := b.EnsureStream(ctx, spec)
	if err != nil || first.Upstream != camstream.PresenceAbsent {
		t.Fatal(first, err)
	}
	_, err = b.EnsureStream(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Source = testSource("rtsp://different.test/stream")
	_, err = b.EnsureStream(ctx, spec)
	requireCode(t, err, camstream.CodeConflict)
	_, err = b.CreateSession(ctx, camstream.SessionSpec{ID: "not-started", SourceKey: "one", Offer: camstream.Offer{SDP: "v=0\r\n"}})
	requireCode(t, err, camstream.CodeInvalidArgument)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = b.EnsureStream(canceled, spec)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = b.RemoveStream(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	_, err = b.StreamStatus(ctx, "one")
	requireCode(t, err, camstream.CodeNotFound)
	_, err = New(cfg, testEncoder{}, testEncoder{})
	requireCode(t, err, camstream.CodeInvalidArgument)
	cfg.VerifyBoundary = nil
	_, err = New(cfg, testEncoder{})
	requireCode(t, err, camstream.CodeInvalidArgument)
}

func TestPinnedArtifactMatchesDeploymentMetadata(t *testing.T) {
	body, err := os.ReadFile("../../../../data/runtime/go2rtc.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		Version   string `json:"version"`
		Artifacts map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	}
	if err = json.Unmarshal(body, &pin); err != nil {
		t.Fatal(err)
	}
	if pin.Version != Version || pin.Artifacts["linux/arm64"].SHA256 != LinuxARM64SHA256 {
		t.Fatal("runtime pin drift")
	}
}

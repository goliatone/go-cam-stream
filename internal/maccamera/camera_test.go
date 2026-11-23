package maccamera

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestValidationAndDeviceSelection(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Listen = "192.168.1.2" }, func(c *Config) { c.Host = "rtsp://host" },
		func(c *Config) { c.Path = "foo#exec" }, func(c *Config) { c.Port = 0 },
		func(c *Config) { c.Width = 1279 }, func(c *Config) { c.FPS = 100 },
		func(c *Config) { c.Device = "Capture screen 0" }, func(c *Config) { c.Device = "0:0" },
	} {
		c := DefaultConfig()
		change(&c)
		if c.Validate() == nil {
			t.Fatalf("accepted invalid config: %+v", c)
		}
	}
	devices := parseDevices([]byte("[avf] AVFoundation video devices:\n[avf] [0] Phone Camera\n[avf] [1] MacBook Pro Desk View Camera\n[avf] [2] MacBook Pro Camera\n[avf] [3] Capture screen 0\n[avf] AVFoundation audio devices:\n[avf] [0] Microphone\n"))
	if len(devices) != 3 {
		t.Fatalf("unexpected devices: %v", devices)
	}
	name, err := selectDevice("auto", devices)
	if err != nil || name != "MacBook Pro Camera" {
		t.Fatalf("wrong built-in selection: %s %v", name, err)
	}
	if _, err = selectDevice("auto", devices[:1]); err == nil {
		t.Fatal("auto selected a phone")
	}
	if _, err = selectDevice("3", devices); err == nil {
		t.Fatal("selected screen")
	}
}

func TestWebcamIdentitySurvivesDuplicateNamesAndInventoryReordering(t *testing.T) {
	const first = `{"id":"model:serial-a","name":"USB Camera","builtin":false}`
	const second = `{"id":"model:serial-b","name":"USB Camera","builtin":false}`
	inventory := func(one, two string) []Device {
		return parseIdentifiedDevices([]byte(`{"version":1,"devices":[` + one + `,` + two + `]}`))
	}
	devices := inventory(first, second)
	if len(devices) != 2 || devices[0].ID == devices[1].ID {
		t.Fatal("webcams with matching names need distinct identities")
	}
	if _, err := selectDevice("USB Camera", devices); err != errCameraAmbiguous {
		t.Fatal("a legacy name should require an explicit choice", err)
	}
	selector, err := selectDevice("1", devices)
	if err != nil || selector != devices[1].ID {
		t.Fatal("numeric selection did not resolve to the chosen webcam", err)
	}
	reordered := inventory(second, first)
	if got, err := selectDevice(selector, reordered); err != nil || got != reordered[0].ID {
		t.Fatal("saved selection did not survive enumeration order change", err)
	}
	if _, err := selectDevice(selector, devices[:1]); err != errCameraUnavailable {
		t.Fatal("an unplugged selection must never use the other webcam", err)
	}
	c := DefaultConfig()
	c.Device = selector
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(ffmpegArgs(c), " ")
	if !strings.Contains(args, "-camstream_video_uid model:serial-b -i none:none") || strings.Contains(args, "USB Camera") {
		t.Fatal("FFmpeg did not receive the exact camera UID")
	}
}

func TestLegacyExportConvertsOnlyUnambiguousWebcamIDs(t *testing.T) {
	s := testService(t)
	c, _, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	const camera = `{"id":"camera-a","name":"USB Camera","builtin":false}`
	setInventory := func(items string) []Device {
		t.Helper()
		body := `{"version":1,"devices":[` + items + `]}`
		tool := filepath.Join(t.TempDir(), "ffmpeg")
		if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf '%s\\n' '"+body+"'\nexit 251\n"), 0700); err != nil {
			t.Fatal(err)
		}
		s.Tools.FFmpeg = tool
		return parseIdentifiedDevices([]byte(body))
	}
	c.Device = setInventory(camera)[0].ID
	if err = writeJSON(filepath.Join(s.Dir, "camera.json"), c); err != nil {
		t.Fatal(err)
	}
	path, err := s.ExportLegacy(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var legacy Config
	if err = readJSON(path, &legacy); err != nil || legacy.Device != "USB Camera" {
		t.Fatal("legacy export should contain the unique webcam name", err)
	}
	setInventory(camera + `,{"id":"camera-b","name":"USB Camera","builtin":false}`)
	if _, err := s.ExportLegacy(t.Context()); err == nil {
		t.Fatal("legacy export accepted an ambiguous webcam name")
	}
	if saved, _, err := load(s.Dir); err != nil || saved.Device != c.Device {
		t.Fatal("legacy export rewrote the active configuration", err)
	}
}

func TestAuthAndCapturePolicy(t *testing.T) {
	c := DefaultConfig()
	c.Device = "MacBook Pro Camera"
	secret, err := newCredentials()
	if err != nil {
		t.Fatal(err)
	}
	b, err := mediaConfig(c, secret)
	if err != nil {
		t.Fatal(err)
	}
	var conf struct {
		RTSPTransports    []string `json:"rtspTransports"` //nolint:tagliatelle // Upstream MediaMTX schema.
		AuthInternalUsers []struct {
			User        string
			Pass        string
			IPs         []string
			Permissions []struct {
				Action string
				Path   string
			}
		} `json:"authInternalUsers"` //nolint:tagliatelle // Upstream MediaMTX schema.
		API, WebRTC, HLS, RTMP, SRT, MOQ bool
	}
	if err = json.Unmarshal(b, &conf); err != nil {
		t.Fatal(err)
	}
	if conf.API || conf.WebRTC || conf.HLS || conf.RTMP || conf.SRT || conf.MOQ {
		t.Fatal("extra listeners enabled")
	}
	if len(conf.RTSPTransports) != 1 || conf.RTSPTransports[0] != "tcp" {
		t.Fatal("not TCP-only")
	}
	if len(conf.AuthInternalUsers) != 2 {
		t.Fatal("unexpected auth users")
	}
	pub, reader := conf.AuthInternalUsers[0], conf.AuthInternalUsers[1]
	if pub.User != "any" || len(pub.IPs) != 1 || pub.IPs[0] != "127.0.0.1" || len(pub.Permissions) != 1 || pub.Permissions[0].Action != "publish" {
		t.Fatal("publisher not constrained")
	}
	if reader.Pass != secret.Password || len(reader.Permissions) != 1 || reader.Permissions[0].Action != "read" {
		t.Fatal("reader auth invalid")
	}
	args := strings.Join(ffmpegArgs(c), " ")
	if !strings.Contains(args, "MacBook Pro Camera:none -an") || strings.Contains(args, secret.Password) || !strings.Contains(args, "-rtsp_transport tcp") {
		t.Fatal("capture policy invalid")
	}
}

func TestPrivateConfigAndDuplicateLock(t *testing.T) {
	s := testService(t)
	lock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.lock(); err == nil {
		t.Fatal("duplicate lock accepted")
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Dir, "camera.json")
	if err = os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = load(s.Dir); err == nil {
		t.Fatal("public configuration accepted")
	}
	if err = os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if err = writePrivate(p, []byte(`{"unknown":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = load(s.Dir); err == nil {
		t.Fatal("unknown fields accepted")
	}
}

func TestConfigureCommandPreservesCredentialsAndExports(t *testing.T) {
	s := testService(t)
	s.Tools = fakeTools(t)
	ctx := context.Background()
	_, before, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig()
	c.Host = "mac.example.test"
	c.Listen = "0.0.0.0"
	h := ConfigureHandler{Service: s}
	if err = h.Execute(ctx, Configure{Config: c}); err != nil {
		t.Fatal(err)
	}
	got, after, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || got.Device != "MacBook Pro Camera" {
		t.Fatal("credentials changed or built-in camera not selected")
	}
	snippet, err := os.ReadFile(filepath.Join(s.Dir, "camstream-camera.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(snippet), after.Password) || !strings.Contains(string(snippet), "env://MAC_CAMERA_PASSWORD") {
		t.Fatal("export does not use secret references")
	}
	for _, name := range []string{"camera.json", "credentials.json", "camstream-camera.env", "camstream-camera.yaml"} {
		info, statErr := os.Stat(filepath.Join(s.Dir, name))
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s permissions: %o", name, info.Mode().Perm())
		}
	}
	lock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close() //nolint:errcheck // Test cleanup.
	if err = h.Execute(ctx, Configure{Config: c}); err == nil {
		t.Fatal("configured an active instance")
	}
}

func testService(t *testing.T) *Service {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := privateDir(dir); err != nil {
		t.Fatal(err)
	}
	s := &Service{Dir: dir}
	c := DefaultConfig()
	c.Device = "MacBook Pro Camera"
	secret, err := newCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if err = writeJSON(filepath.Join(dir, "camera.json"), c); err != nil {
		t.Fatal(err)
	}
	if err = writeJSON(filepath.Join(dir, "credentials.json"), secret); err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := s.runtimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestSupervisorLifecycle(t *testing.T) {
	for _, action := range []string{"stop", "owner-loss", "producer-failure"} {
		t.Run(action, func(t *testing.T) {
			s := testService(t)
			s.Tools = fakeTools(t)
			if action == "producer-failure" {
				t.Setenv("CAMERA_TEST_EXIT", "1")
			}
			reader, writer := io.Pipe()
			defer reader.Close() //nolint:errcheck // Test cleanup.
			defer writer.Close() //nolint:errcheck // Test cleanup.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- s.Supervise(ctx, reader, io.Discard) }()
			if action != "producer-failure" {
				waitStreaming(t, ctx, s)
				if _, err := s.lock(); err == nil {
					t.Fatal("running instance did not hold lock")
				}
				if action == "stop" {
					if err := s.Stop(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			select {
			case err := <-done:
				if (err != nil) != (action == "producer-failure") {
					t.Fatalf("unexpected result: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("supervisor did not terminate")
			}
			status, err := s.Status(ctx)
			if err != nil || status.State != "stopped" {
				t.Fatalf("not stopped: %+v %v", status, err)
			}
			lock, err := s.lock()
			if err != nil {
				t.Fatal(err)
			}
			if err = lock.Close(); err != nil {
				t.Fatal(err)
			}
			dir, err := s.runtimeDir()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(dir, "mediamtx.json")); !os.IsNotExist(err) {
				t.Fatal("runtime credentials retained")
			}
		})
	}
}

func waitStreaming(t *testing.T, ctx context.Context, s *Service) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("never streamed")
		case <-ticker.C:
			status, err := s.Status(ctx)
			if err == nil && status.State == "streaming" {
				return
			}
		}
	}
}

// fakeTools links ffmpeg and mediamtx to this test binary, which TestMain runs
// as the fake runtime. A link to the running binary is not a new executable.
// macOS assesses each new executable, shell scripts included, before its first
// launch, one at a time across the system, so in a full parallel
// `go test ./...` a fresh script's first launch can wait seconds behind the
// other packages' new test binaries.
func fakeTools(t *testing.T) Tools {
	t.Helper()
	t.Setenv("CAMERA_RUNTIME_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"ffmpeg", "mediamtx"} {
		if err = os.Symlink(exe, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return Tools{FFmpeg: filepath.Join(dir, "ffmpeg"), MediaMTX: filepath.Join(dir, "mediamtx")}
}

// TestMain dispatches before m.Run so the testing flag parser never sees the
// fake tool's arguments.
func TestMain(m *testing.M) {
	if os.Getenv("CAMERA_RUNTIME_HELPER") == "1" {
		if name := filepath.Base(os.Args[0]); name == "ffmpeg" || name == "mediamtx" {
			runtimeHelper(name, os.Args[1:])
		}
	}
	os.Exit(m.Run())
}

func runtimeHelper(name string, args []string) {
	invocation := strings.Join(args, " ")
	if name == "mediamtx" {
		if len(args) > 0 && args[0] == "--version" {
			fmt.Println(MediaMTXVersion)
			os.Exit(0)
		}
		fmt.Println("[RTSP] started with listeners on 127.0.0.1 (TCP)")
	} else {
		if strings.Contains(invocation, "-list_devices") {
			fmt.Fprintln(os.Stderr, "[avf] AVFoundation video devices:\n[avf] [0] MacBook Pro Camera\n[avf] AVFoundation audio devices:")
			os.Exit(1)
		}
		if os.Getenv("CAMERA_TEST_EXIT") == "1" {
			os.Exit(2)
		}
		if strings.Contains(invocation, "-pix_fmt bgra") {
			previewRuntimeHelper()
			os.Exit(0)
		}
		fmt.Println("frame=10\nprogress=continue")
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	os.Exit(0)
}

func TestOwnedReadinessRejectsForeignListener(t *testing.T) {
	lc := net.ListenConfig{}
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close() //nolint:errcheck // Test cleanup.
	cmd := exec.CommandContext(context.Background(), "/usr/bin/false")
	server, err := startChild(cmd)
	if err != nil {
		t.Fatal(err)
	}
	err = waitServer(context.Background(), server, make(chan struct{}))
	if err == nil {
		t.Fatal("foreign listener treated as owned readiness")
	}
}

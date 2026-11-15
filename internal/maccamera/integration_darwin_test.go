package maccamera

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in: this test activates the real webcam, never records imagery,
// and only binds loopback. Use the exported/relocated folder under test.
func TestActualWebcam(t *testing.T) {
	bundle := os.Getenv("CAMSTREAM_CAMERA_BUNDLE")
	if bundle == "" {
		t.Skip("set CAMSTREAM_CAMERA_BUNDLE to authorize real webcam smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "camera")
	bin := filepath.Join(bundle, "camstream-camera")
	lc := net.ListenConfig{}
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	minimal := []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "TMPDIR=/tmp"}
	call := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, bin, append(args, "--dir", dir)...)
		cmd.Env = minimal
		return cmd
	}
	if output, configureErr := call("configure", "--port", port).CombinedOutput(); configureErr != nil {
		t.Fatalf("configure: %v %s", configureErr, output)
	}
	_, secret, err := load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Dir: dir}
	runtimeDir, err := s.runtimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Error(err)
		}
	})
	for _, mode := range []string{"stop", "owner-kill"} {
		t.Run(mode, func(t *testing.T) {
			cmd := call("start")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer stopCancel()
				if err := s.Stop(stopCtx); err != nil {
					t.Error(err)
				}
			})
			waitStreaming(t, ctx, s)
			if duplicate, err := call("start").CombinedOutput(); err == nil || !strings.Contains(string(duplicate), "already running") {
				t.Fatalf("duplicate start did not fail: %v", err)
			}
			if mode == "stop" {
				verifyReaders(t, ctx, bundle, port, secret, minimal)
				if err := s.Stop(ctx); err != nil {
					t.Fatal(err)
				}
				if err := cmd.Wait(); err != nil {
					t.Fatalf("start exit: %v %s", err, output.String())
				}
			} else {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				if err := cmd.Wait(); err == nil {
					t.Fatal("killed owner exited successfully")
				}
				waitStopped(t, ctx, s)
			}
			d := net.Dialer{Timeout: time.Second}
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
			if err == nil {
				_ = conn.Close() //nolint:errcheck // Test fails immediately if the listener remains.
				t.Fatal("RTSP listener survived stop")
			}
			t.Logf("%s: camera and RTSP listener cleaned", mode)
		})
	}
}

func waitStopped(t *testing.T, ctx context.Context, s *Service) {
	t.Helper()
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("test deadline")
		case <-timer.C:
			t.Fatal("owner loss did not stop camera")
		case <-ticker.C:
			status, err := s.Status(ctx)
			if err == nil && status.State == "stopped" {
				return
			}
		}
	}
}

func verifyReaders(t *testing.T, ctx context.Context, bundle, port string, secret credentials, env []string) {
	t.Helper()
	for _, auth := range []string{"", "camera:wrong@", secret.Username + ":" + secret.Password + "@"} {
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		url := "rtsp://" + auth + net.JoinHostPort("127.0.0.1", port) + "/macbook"
		cmd := exec.CommandContext(probeCtx, filepath.Join(bundle, "tools/ffmpeg"), "-hide_banner", "-loglevel", "info", "-rtsp_transport", "tcp", "-i", url, "-an", "-frames:v", "10", "-f", "null", "-")
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		cancel()
		if auth == "" || auth == "camera:wrong@" {
			if err == nil || !bytes.Contains(output, []byte("401 Unauthorized")) {
				t.Fatal("unauthorized reader was not rejected")
			}
		} else {
			if err != nil || !bytes.Contains(output, []byte("Video: h264")) {
				t.Fatal("authenticated H.264 decode failed (raw diagnostics omitted)")
			}
			t.Log("authenticated H.264/TCP decoded ten frames to null; anonymous/wrong password rejected")
		}
	}
}

// Keep the JSON response shape available to external smoke consumers.
func TestStatusJSONOmitsSecrets(t *testing.T) {
	b, err := json.Marshal(Status{State: "streaming", URL: "rtsp://localhost:8554/macbook", Frames: 10})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "password") || strings.Contains(string(b), "username") {
		t.Fatal("status exposes credentials")
	}
}

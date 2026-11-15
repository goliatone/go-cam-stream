package maccamera

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type sessionProbe struct {
	in      *io.PipeWriter
	replies <-chan AppReply
	done    <-chan error
}

func newSessionProbe(t *testing.T, s *Service, run func(context.Context) error) sessionProbe {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	in, writer := io.Pipe()
	out, output := io.Pipe()
	done := make(chan error, 1)
	replies := make(chan AppReply, 32)
	a := &appSession{service: s, encoder: json.NewEncoder(output), run: run}
	go func() {
		defer close(replies)
		decoder := json.NewDecoder(out)
		for {
			var r AppReply
			if decoder.Decode(&r) != nil {
				return
			}
			replies <- r
		}
	}()
	go func() {
		done <- a.serve(ctx, in)
		_ = output.Close() //nolint:errcheck // Test pipe cleanup.
	}()
	t.Cleanup(func() {
		cancel()
		_ = writer.Close() //nolint:errcheck // Test pipe cleanup.
		_ = out.Close()    //nolint:errcheck // Test pipe cleanup.
	})
	return sessionProbe{writer, replies, done}
}

func (p sessionProbe) receive(t *testing.T) AppReply {
	t.Helper()
	select {
	case r, ok := <-p.replies:
		if !ok {
			t.Fatal("session closed before reply")
		}
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("session reply timed out")
		return AppReply{}
	}
}

func (p sessionProbe) request(t *testing.T, method string, c *Config) AppReply {
	t.Helper()
	if err := json.NewEncoder(p.in).Encode(AppRequest{Version: 1, ID: method, Method: method, Config: c}); err != nil {
		t.Fatal(err)
	}
	r := p.receive(t)
	if r.ID != method {
		t.Fatalf("wrong request correlation: %+v", r)
	}
	return r
}

func (p sessionProbe) finished(t *testing.T) {
	t.Helper()
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session did not reap capture")
	}
}

func TestAppSessionOwnerCleanup(t *testing.T) {
	for _, action := range []string{"eof", "quit", "stop"} {
		t.Run(action, func(t *testing.T) {
			s := testService(t)
			started, reaped := make(chan struct{}), make(chan struct{})
			p := newSessionProbe(t, s, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(reaped)
				return nil
			})
			if p.receive(t).Event != "ready" {
				t.Fatal("missing ready handshake")
			}
			if r := p.request(t, "start", nil); r.Error != nil {
				t.Fatal(r.Error)
			}
			<-started
			if r := p.request(t, "start", nil); r.Error == nil || r.Error.Code != "busy" {
				t.Fatal("duplicate start accepted")
			}
			if action != "eof" {
				if r := p.request(t, action, nil); r.Error != nil {
					t.Fatal(r.Error)
				}
				select {
				case <-reaped:
				default:
					t.Fatal("acknowledged before reaping")
				}
			}
			if err := p.in.Close(); err != nil {
				t.Fatal(err)
			}
			p.finished(t)
			select {
			case <-reaped:
			default:
				t.Fatal("capture outlived owner")
			}
			lock, err := s.namedLock("app-session.lock")
			if err != nil {
				t.Fatal(err)
			}
			if err = lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAppSessionContractAndRedaction(t *testing.T) {
	s := testService(t)
	s.Tools = fakeTools(t)
	p := newSessionProbe(t, s, func(context.Context) error { return errors.New("rtsp://camera:secret@host raw producer diagnostic") })
	p.receive(t)
	c := DefaultConfig()
	for _, method := range []string{"devices", "config", "configure", "export", "status"} {
		var cfg *Config
		if method == "configure" {
			cfg = &c
		}
		if r := p.request(t, method, cfg); r.Error != nil {
			t.Fatalf("%s: %v", method, r.Error)
		}
	}
	_, secret, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	r := p.request(t, "config", nil)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret.Password) || strings.Contains(string(b), "password") {
		t.Fatal("configuration leaks credentials")
	}
	if _, err = io.WriteString(p.in, `{"version":2,"id":"secret","method":"rtsp://secret@host"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	r = p.receive(t)
	if r.Error == nil || r.Error.Code != "invalid_request" || r.ID != "" {
		t.Fatal("invalid request not classified")
	}
	p.request(t, "start", nil)
	r = p.receive(t)
	if r.Event != "capture_stopped" || r.Error == nil || r.Error.Code != "capture_failed" || strings.Contains(r.Error.Message, "secret") {
		t.Fatal("capture error leaked or missing")
	}
	p.request(t, "quit", nil)
	p.finished(t)
}

func TestAppSessionDuplicateAndNonOwnerStop(t *testing.T) {
	s := testService(t)
	owner := newSessionProbe(t, s, nil)
	owner.receive(t)
	other := newSessionProbe(t, s, nil)
	r := other.receive(t)
	if r.Event != "session_error" || r.Error == nil || r.Error.Code != "busy" {
		t.Fatal("duplicate session accepted")
	}
	other.finished(t)
	lock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close() //nolint:errcheck // Test cleanup.
	owner.request(t, "stop", nil)
	if _, err = s.lock(); err == nil {
		t.Fatal("non-owned instance unlocked")
	}
	owner.request(t, "quit", nil)
	owner.finished(t)
}

func TestAppSessionReadLimitAndBrokenOutput(t *testing.T) {
	s := testService(t)
	p := newSessionProbe(t, s, nil)
	p.receive(t)
	_, _ = io.WriteString(p.in, strings.Repeat("x", 17000)) //nolint:errcheck // Oversized input intentionally closes pipe.
	p.finished(t)
	a := &appSession{service: s, encoder: json.NewEncoder(errorWriter{})}
	in, writer := io.Pipe()
	defer writer.Close() //nolint:errcheck // Test cleanup.
	if a.serve(context.Background(), in) == nil {
		t.Fatal("broken output ignored")
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestBundledToolsNeverFallBackToPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Proof.app", "Contents", "Helpers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fallback := func(string) (string, error) { t.Fatal("app fell back to PATH"); return "", nil }
	exe := filepath.Join(dir, "camstream-camera")
	if _, err := resolveToolsAt(exe, fallback); err == nil {
		t.Fatal("missing runtime accepted")
	}
	for _, name := range []string{"ffmpeg", "mediamtx"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	tools, err := resolveToolsAt(exe, fallback)
	if err != nil || tools.FFmpeg != filepath.Join(dir, "ffmpeg") {
		t.Fatalf("bundled lookup failed: %v", err)
	}
}

func TestAppRequestRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	for _, input := range []string{
		`{"version":1,"id":"x","method":"status","extra":true}`,
		`{"version":1,"id":"x","method":"status"} {}`,
		`{"version":1,"id":"rtsp://secret","method":"status"}`,
		`{"version":1,"id":"x","method":"configure"}`,
		`{"version":1,"id":"x","method":"status","config":{}}`,
	} {
		if _, err := decodeAppRequest(bytes.TrimSpace([]byte(input))); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestAppSessionExportWaitsForOwnedCapture(t *testing.T) {
	s := testService(t)
	started := make(chan struct{})
	p := newSessionProbe(t, s, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	p.receive(t)
	p.request(t, "start", nil)
	<-started // Capture is owned, but the supervisor has not acquired its instance lock yet.
	r := p.request(t, "export", nil)
	if r.Error == nil || r.Error.Code != "export_failed" {
		t.Fatal("export accepted before owned capture stopped")
	}
	p.request(t, "stop", nil)
	if r = p.request(t, "export", nil); r.Error != nil {
		t.Fatal("export refused after owned capture reaped")
	}
	p.request(t, "quit", nil)
	p.finished(t)
}

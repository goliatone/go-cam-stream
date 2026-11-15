package maccamera

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func previewRuntimeHelper() {
	mode := os.Getenv("CAMERA_PREVIEW_MODE")
	if mode == "exit" {
		return
	}
	if mode == "partial" {
		_, _ = os.Stdout.Write([]byte{1, 2, 3}) //nolint:errcheck // Intentional truncated decoder output.
		return
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	ticker := time.NewTicker(time.Second / 15)
	defer ticker.Stop()
	frame := make([]byte, 640*360*4)
	for {
		select {
		case <-sig:
			return
		case <-ticker.C:
			if mode == "stall" {
				continue
			}
			frame[0]++
			if _, err := os.Stdout.Write(frame); err != nil {
				return
			}
		}
	}
}
func newPreviewManager(t *testing.T) (*previewManager, context.CancelFunc) {
	t.Helper()
	s := testService(t)
	c, secret, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	dir, err := s.runtimeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = privateDir(dir); err != nil {
		t.Fatal(err)
	}
	frames := &atomic.Int64{}
	p := &previewManager{ctx: ctx, config: c, secret: secret, tool: fakeTools(t).FFmpeg, dir: dir, frames: frames}
	t.Cleanup(func() { cancel(); p.close() })
	return p, cancel
}
func readPreviewFrame(t *testing.T, conn net.Conn, generation string) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	h := make([]byte, 48)
	if _, err := io.ReadFull(conn, h); err != nil {
		t.Fatal(err)
	}
	g, err := parsePreviewGeneration(generation)
	if err != nil {
		t.Fatal(err)
	}
	if string(h[:4]) != "CSPR" || binary.BigEndian.Uint16(h[4:]) != 1 || binary.BigEndian.Uint16(h[6:]) != 48 || binary.BigEndian.Uint64(h[8:]) != g {
		t.Fatal("invalid header")
	}
	size := binary.BigEndian.Uint32(h[44:])
	if size > previewMaxBytes || size == 0 {
		t.Fatal("unbounded frame")
	}
	frame := make([]byte, size)
	if _, err = io.ReadFull(conn, frame); err != nil {
		t.Fatal(err)
	}
	return frame
}
func TestPreviewLifecycleAndGeneration(t *testing.T) {
	p, _ := newPreviewManager(t)
	if _, err := p.attach(); err == nil {
		t.Fatal("attached before capture frames")
	}
	p.frames.Store(10)
	endpoint, err := p.attach()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(endpoint.Socket)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket not private")
	}
	if _, err = p.attach(); err == nil {
		t.Fatal("duplicate subscription accepted")
	}
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "unix", endpoint.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer closePreviewIO(conn)
	frame := readPreviewFrame(t, conn, endpoint.Generation)
	if len(frame) != previewMaxBytes {
		t.Fatal("wrong dimensions")
	}
	p.detach("0000000000000001") // An old detach must not revoke the active generation.
	readPreviewFrame(t, conn, endpoint.Generation)
	p.detach(endpoint.Generation)
	p.detach(endpoint.Generation)
	next, err := p.attach()
	if err != nil || next.Generation == endpoint.Generation {
		t.Fatal("generation not replaced")
	}
	conn2, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "unix", next.Socket)
	if err != nil {
		t.Fatal(err)
	}
	readPreviewFrame(t, conn2, next.Generation)
	closePreviewIO(conn2)
	select {
	case <-p.active.done:
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber loss did not reap")
	}
	files, err := filepath.Glob(filepath.Join(p.dir, "preview-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("preview files retained")
	}
}
func TestPreviewSlowReaderAndOwnerLoss(t *testing.T) {
	for _, mode := range []string{"slow", "owner"} {
		t.Run(mode, func(t *testing.T) {
			p, cancel := newPreviewManager(t)
			p.frames.Store(1)
			e, err := p.attach()
			if err != nil {
				t.Fatal(err)
			}
			conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "unix", e.Socket)
			if err != nil {
				t.Fatal(err)
			}
			defer closePreviewIO(conn)
			if mode == "owner" {
				readPreviewFrame(t, conn, e.Generation)
				cancel()
			}
			select {
			case <-p.active.done:
			case <-time.After(6 * time.Second):
				t.Fatal("decoder outlived slow client/owner")
			}
			if mode == "slow" && p.ctx.Err() != nil {
				t.Fatal("preview failure cancelled capture")
			}
		})
	}
}
func TestPreviewPolicyAndWireFixture(t *testing.T) {
	for _, c := range []Config{{Width: 1280, Height: 720}, {Width: 640, Height: 480}, {Width: 720, Height: 1280}, {Width: 1920, Height: 1080}} {
		w, h := previewDimensions(c)
		if w > 640 || h > 360 || w*h*4 > previewMaxBytes {
			t.Fatal("dimensions unbounded")
		}
		if abs(w*c.Height-h*c.Width) > c.Width+c.Height {
			t.Fatal("aspect ratio changed")
		}
	}
	b, err := os.ReadFile("testdata/preview-frame-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Header     string `json:"header_hex"`
		Generation string `json:"generation"`
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	expected, err := hex.DecodeString(fixture.Header)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := parsePreviewGeneration(fixture.Generation)
	if err != nil {
		t.Fatal(err)
	}
	actual := frameHeader(generation, 42, 2, 1)
	copy(actual[24:32], expected[24:32])
	if string(actual) != string(expected) {
		t.Fatal("wire fixture differs")
	}
	args := strings.Join(previewArgs("/private/reader.ffconcat", 640, 360), " ")
	if strings.Contains(args, "rtsp://") || !strings.Contains(args, "-f rawvideo pipe:1") {
		t.Fatal("unsafe decoder arguments")
	}
	for _, g := range []string{"", "0", "0000000000000000", "not-a-generation"} {
		if _, err = parsePreviewGeneration(g); err == nil {
			t.Fatal("bad generation accepted")
		}
	}
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func TestPreviewAppAdmission(t *testing.T) {
	s := testService(t)
	p := newSessionProbe(t, s, func(ctx context.Context) error { <-ctx.Done(); return nil })
	ready := p.receive(t)
	raw, err := json.Marshal(ready)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"preview":1`) {
		t.Fatal("preview version not advertised")
	}
	if r := p.request(t, "preview_attach", nil); r.Error == nil || r.Error.Code != "preview_unavailable" {
		t.Fatal("unowned preview admitted")
	}
	p.request(t, "start", nil)
	if r := p.request(t, "preview_attach", nil); r.Error == nil {
		t.Fatal("preview admitted during startup gap")
	}
	p.request(t, "quit", nil)
	p.finished(t)
	for _, v := range []string{`{"version":1,"id":"a","method":"preview_detach"}`, `{"version":1,"id":"a","method":"status","preview_generation":"0123456789abcdef"}`} {
		if _, err := decodeAppRequest([]byte(v)); err == nil {
			t.Fatal("invalid preview request accepted")
		}
	}
}

func TestPreviewDecoderFailureIsolation(t *testing.T) {
	for _, mode := range []string{"exit", "partial", "stall"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CAMERA_PREVIEW_MODE", mode)
			p, _ := newPreviewManager(t)
			p.frames.Store(1)
			e, err := p.attach()
			if err != nil {
				t.Fatal(err)
			}
			conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "unix", e.Socket)
			if err != nil {
				t.Fatal(err)
			}
			defer closePreviewIO(conn)
			select {
			case <-p.active.done:
			case <-time.After(12 * time.Second):
				t.Fatal("failed/stalled decoder not reaped")
			}
			if p.ctx.Err() != nil {
				t.Fatal("preview failure stopped capture")
			}
		})
	}
}

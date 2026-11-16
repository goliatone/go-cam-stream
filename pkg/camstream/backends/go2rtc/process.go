package go2rtc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

type listener struct {
	network string
	port    int
	address string
}

type worker struct {
	cmd              *exec.Cmd
	dir              string
	api, rtsp, media int
	reaped           bool
	lifetime         *os.File
}

func address(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
func (w *worker) endpoint() string         { return "http://" + address("127.0.0.1", w.api) }

func (b *Backend) reserve(r PortRange) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	for p := r.First; p <= r.Last; p++ {
		if !b.ports[p] {
			b.ports[p] = true
			return p
		}
	}
	return 0
}
func (b *Backend) free(w *worker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.ports, w.api)
	delete(b.ports, w.rtsp)
	delete(b.ports, w.media)
}
func (b *Backend) allocate(ingest bool) (*worker, error) {
	w := &worker{api: b.reserve(b.cfg.PrivatePorts)}
	if ingest {
		w.rtsp = b.reserve(b.cfg.PrivatePorts)
	} else {
		w.media = b.reserve(b.cfg.MediaPorts)
	}
	if w.api == 0 || (ingest && w.rtsp == 0) || (!ingest && w.media == 0) {
		b.free(w)
		return nil, failure(camstream.CodeCapacityExceeded, nil)
	}
	return w, nil
}
func (b *Backend) workerConfig(w *worker, input Input) ([]byte, error) {
	rtsp, media := "", ""
	networks := []string{}
	if w.rtsp != 0 {
		rtsp = address("127.0.0.1", w.rtsp)
	}
	if w.media != 0 {
		media = address(b.cfg.MediaAddress, w.media)
		if b.cfg.Network != "tcp+udp" {
			media += "/" + b.cfg.Network
		}
		if b.cfg.Network != "udp" {
			networks = append(networks, "tcp4")
		}
		if b.cfg.Network != "tcp" {
			networks = append(networks, "udp4")
		}
	}
	return json.Marshal(map[string]any{
		"api":     map[string]any{"listen": address("127.0.0.1", w.api)},
		"rtsp":    map[string]any{"listen": rtsp},
		"webrtc":  map[string]any{"listen": media, "ice_servers": []string{}, "candidates": []string{address(b.cfg.CandidateAddress, w.media)}, "filters": map[string]any{"loopback": true, "networks": networks, "ips": []string{b.cfg.MediaAddress}}},
		"streams": map[string]any{"stream": []string{input.URL}}, "log": map[string]any{"level": "error"},
	})
}

// launch returns ownership even on failure. The caller must stop a returned
// worker before releasing it; API responsiveness alone never establishes readiness.
func (b *Backend) launch(ctx context.Context, ingest bool, input Input) (*worker, error) {
	w, err := b.allocate(ingest)
	if err != nil {
		return nil, err
	}
	w.dir, err = os.MkdirTemp(b.dir, "worker-")
	if err != nil {
		b.free(w)
		return nil, failure(camstream.CodeBackendUnavailable, err)
	}
	body, err := b.workerConfig(w, input)
	if err == nil {
		err = os.WriteFile(filepath.Join(w.dir, "config.json"), body, 0600)
	}
	if err != nil {
		return w, failure(camstream.CodeBackendUnavailable, err)
	}
	w.cmd = exec.Command(b.executable, "-config", filepath.Join(w.dir, "config.json")) //nolint:noctx // Explicit group cleanup owns termination and reaping, independently of request cancellation.
	w.cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + w.dir}
	w.cmd.Dir = w.dir
	w.cmd.Stdout = io.Discard
	w.cmd.Stderr = io.Discard
	w.lifetime, err = launchProcess(w.cmd, b.lock)
	if err != nil {
		w.cmd = nil
		return w, failure(camstream.CodeBackendUnavailable, err)
	}
	for {
		if processExited(w.cmd.Process.Pid) {
			return w, failure(camstream.CodeBackendUnavailable, nil)
		}
		if b.listenersOwned(w) == nil {
			if _, err = b.request(ctx, w, "GET", "/api", nil); err == nil {
				return w, nil
			}
		}
		if err = pause(ctx, 20*time.Millisecond); err != nil {
			return w, failure(camstream.CodeBackendUnavailable, err)
		}
	}
}
func (b *Backend) listenersOwned(w *worker) error {
	checks := []listener{{"tcp", w.api, "127.0.0.1"}}
	if w.rtsp != 0 {
		checks = append(checks, listener{"tcp", w.rtsp, "127.0.0.1"})
	}
	if w.media != 0 {
		if b.cfg.Network != "udp" {
			checks = append(checks, listener{"tcp", w.media, b.cfg.MediaAddress})
		}
		if b.cfg.Network != "tcp" {
			checks = append(checks, listener{"udp", w.media, b.cfg.MediaAddress})
		}
	}
	return ownsListeners(w.cmd.Process.Pid, checks)
}
func (b *Backend) stop(ctx context.Context, w *worker) error {
	if w == nil {
		return nil
	}
	if w.cmd != nil && !w.reaped {
		if err := stopGroup(ctx, w.cmd, b.cfg.TermGrace); err != nil {
			return failure(camstream.CodeCleanupIncomplete, err)
		}
		w.reaped = true
	}
	if w.lifetime != nil {
		if err := w.lifetime.Close(); err != nil {
			return failure(camstream.CodeCleanupIncomplete, err)
		}
		w.lifetime = nil
	}
	if err := os.RemoveAll(w.dir); err != nil {
		return failure(camstream.CodeCleanupIncomplete, err)
	}
	b.free(w)
	return nil
}
func relay(w *worker) Input {
	return Input{URL: fmt.Sprintf("rtsp://%s/stream", address("127.0.0.1", w.rtsp))}
}

func (w *worker) dead() bool { return w.cmd == nil || w.reaped || processExited(w.cmd.Process.Pid) }

//go:build linux

package go2rtc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

// This endpoint exists only in the disposable crash fixture. It drives the
// production adapter from an independent host peer; it is not application ingress.
func TestIntegrationController(t *testing.T) {
	if os.Getenv("CAMSTREAM_CRASH_FIXTURE") != "1" {
		t.Skip("standalone controller-crash fixture")
	}
	b := integration(t)
	b.cfg.AllowExec = true
	b.cfg.Network = "tcp"
	addrs, addrErr := net.InterfaceAddrs()
	if addrErr != nil {
		t.Fatal(addrErr)
	}
	for _, addr := range addrs {
		ip, _, e := net.ParseCIDR(addr.String())
		if e == nil && ip.To4() != nil && !ip.IsLoopback() {
			b.cfg.MediaAddress = ip.String()
			break
		}
	}
	if _, err := b.EnsureStream(context.Background(), testSpec("helper", "exec:ffmpeg -hide_banner -loglevel error -re -stream_loop -1 -i /fixture/clip.mkv -c copy -rtsp_transport tcp -f rtsp {output}")); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	var seq atomic.Int64
	mux.HandleFunc("/api/webrtc", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			http.Error(w, "invalid offer", http.StatusBadRequest)
			return
		}
		state, err := b.CreateSession(r.Context(), camstream.SessionSpec{ID: camstream.SessionID(strconv.FormatInt(seq.Add(1), 10)), SourceKey: "helper", Offer: camstream.Offer{SDP: string(body)}})
		if err != nil {
			http.Error(w, string(camstream.DescribeError(err).Code), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		if _, err = io.WriteString(w, state.Answer.SDP); err != nil {
			if closeErr := b.CloseSession(context.Background(), state.ID); closeErr != nil {
				t.Error(closeErr)
			}
		}
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := fmt.Fprint(w, os.Getpid()); err != nil {
			return
		}
	})
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "0.0.0.0:35200")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() {
		if serveErr := server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			t.Error(serveErr)
		}
	}()
	t.Logf("controller_ready pid=%d", os.Getpid())
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM)
	defer signal.Stop(done)
	<-done
	if err = server.Close(); err != nil {
		t.Error(err)
	}
}
func TestIntegrationHelperCrashAndRuntimeCancellation(t *testing.T) {
	b := integration(t)
	b.cfg.AllowExec = true
	if _, err := b.EnsureStream(context.Background(), testSpec("helper", "exec:ffmpeg -hide_banner -loglevel error -re -stream_loop -1 -i /fixture/clip.mkv -c copy -rtsp_transport tcp -f rtsp {output}")); err != nil {
		t.Fatal(err)
	}
	peer := connect(t, b, "helper-viewer", "helper", "tcp")
	var leader int
	lockedSource(t, b, "helper", func(s *source) { leader = s.ingest.cmd.Process.Pid })
	helper := firstChild(leader)
	if helper == 0 {
		t.Fatal("owned FFmpeg helper not found")
	}
	before := peer.video.Load()
	var err error
	if err = syscall.Kill(helper, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool {
		next := firstChild(leader)
		return next != 0 && next != helper && peer.video.Load() > before
	})
	helper = firstChild(leader)
	if err = syscall.Kill(helper, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, func() bool {
		s, e := b.StreamStatus(context.Background(), "helper")
		return e == nil && s.Fault == camstream.CodeSourceUnavailable && s.Upstream == camstream.PresenceAbsent
	})
	if err = b.ResetSource(context.Background(), "helper"); err != nil {
		t.Fatal(err)
	}
	connect(t, b, "fresh", "helper", "tcp")
	b.cancel()
	eventually(t, 5*time.Second, func() bool {
		s, e := b.StreamStatus(context.Background(), "helper")
		return e == nil && s.Upstream == camstream.PresenceAbsent
	})
	if err = b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Log("native helper crash recovery observed; stalled helper bounded and latched; runtime cancellation drains owned groups")
}

func firstChild(leader int) int {
	tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", leader))
	if err != nil {
		return 0
	}
	for _, task := range tasks {
		data, e := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", leader, task.Name()))
		if e != nil {
			continue
		}
		for _, id := range strings.Fields(string(data)) {
			pid, e := strconv.Atoi(id)
			if e == nil && !processExited(pid) {
				return pid
			}
		}
	}
	return 0
}

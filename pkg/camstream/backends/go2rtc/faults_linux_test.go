//go:build linux

package go2rtc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

type faultProxy struct {
	ln       net.Listener
	target   string
	deny     atomic.Bool
	attempts atomic.Int64
	mu       sync.Mutex
	conns    []net.Conn
}

func proxy(t *testing.T, target string) *faultProxy {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &faultProxy{ln: ln, target: target}
	t.Cleanup(func() { closeFixture(ln); p.fail() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			p.attempts.Add(1)
			p.mu.Lock()
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			go p.serve(c)
		}
	}()
	return p
}
func (p *faultProxy) serve(c net.Conn) {
	defer closeFixture(c)
	if !p.deny.Load() {
		up, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", p.target)
		if err != nil {
			return
		}
		defer closeFixture(up)
		go func() {
			if _, copyErr := io.Copy(up, c); copyErr != nil {
				return
			}
		}()
		if _, copyErr := io.Copy(c, up); copyErr != nil {
			return
		}
		return
	}
	reader := bufio.NewReader(c)
	for {
		seq := "1"
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(strings.ToLower(line), "cseq:") {
				seq = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			}
			if line == "\r\n" {
				break
			}
		}
		if _, err := fmt.Fprintf(c, "RTSP/1.0 401 Unauthorized\r\nCSeq: %s\r\nWWW-Authenticate: Basic realm=\"synthetic\"\r\nContent-Length: 0\r\n\r\n", seq); err != nil {
			return
		}
	}
}
func (p *faultProxy) fail() {
	p.deny.Store(true)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		closeFixture(c)
	}
	p.conns = nil
}
func TestIntegrationFailureBudgetAndCancellation(t *testing.T) {
	b := integration(t)
	o := origin(t, b, 34000, 34001)
	p := proxy(t, address("127.0.0.1", o.rtsp))
	spec := testSpec("auth", "rtsp://probe:synthetic@"+p.ln.Addr().String()+"/stream")
	if _, err := b.EnsureStream(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	connect(t, b, "healthy", "auth", "tcp")
	p.fail()
	start := time.Now()
	eventually(t, 5*time.Second, func() bool {
		s, e := b.StreamStatus(context.Background(), "auth")
		return e == nil && s.Fault == camstream.CodeSourceUnavailable && s.Upstream == camstream.PresenceAbsent
	})
	t.Logf("recovery budget cleanup: %s", time.Since(start))
	atStop := p.attempts.Load()
	time.Sleep(400 * time.Millisecond)
	if p.attempts.Load() != atStop {
		t.Fatal("native retries continued after fault cleanup")
	}
	if err := b.ResetSource(context.Background(), "auth"); err != nil {
		t.Fatal(err)
	}
	viewer := newPeer(t, "tcp")
	start = time.Now()
	_, err := b.CreateSession(context.Background(), camstream.SessionSpec{ID: "initial-denied", SourceKey: "auth", Offer: offer(t, viewer)})
	requireCode(t, err, camstream.CodeSourceUnavailable)
	if time.Since(start) > b.cfg.StartupTimeout+b.cfg.CleanupTimeout {
		t.Fatal("initial failure exceeded bound")
	}
	atStop = p.attempts.Load()
	time.Sleep(400 * time.Millisecond)
	if p.attempts.Load() != atStop {
		t.Fatal("initial retries continued")
	}
	if err = b.ResetSource(context.Background(), "auth"); err != nil {
		t.Fatal(err)
	}
	cancelPeer := newPeer(t, "tcp")
	sdp := offer(t, cancelPeer)
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err = b.CreateSession(ctx, camstream.SessionSpec{ID: "canceled", SourceKey: "auth", Offer: sdp})
	if err == nil {
		t.Fatal("canceled create succeeded")
	}
	state, err := b.SessionStatus(context.Background(), "canceled")
	if err != nil || state.Resources != camstream.PresenceAbsent {
		t.Fatal(state, err)
	}
	source, err := b.StreamStatus(context.Background(), "auth")
	if err != nil || source.Upstream != camstream.PresenceAbsent {
		t.Fatal(source, err)
	}
	t.Log("auth/no-media retry cutoff, explicit reset, and in-flight cancellation cleanup passed")
}
func TestIntegrationCleanupRetentionAndPortOwnership(t *testing.T) {
	b := integration(t)
	o := origin(t, b, 34000, 34001)
	b.cfg.MediaPorts.Last = b.cfg.MediaPorts.First // configured before any admission
	if _, err := b.EnsureStream(context.Background(), testSpec("one", relay(o).URL)); err != nil {
		t.Fatal(err)
	}
	connect(t, b, "one", "one", "tcp")
	full := newPeer(t, "tcp")
	_, fullErr := b.CreateSession(context.Background(), camstream.SessionSpec{ID: "full", SourceKey: "one", Offer: offer(t, full)})
	requireCode(t, fullErr, camstream.CodeCapacityExceeded)
	var pid int
	lockedSource(t, b, "one", func(s *source) { pid = s.sessions["one"].worker.cmd.Process.Pid })
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := b.CloseSession(ctx, "one")
	requireCode(t, err, camstream.CodeCleanupIncomplete)
	st, err := b.SessionStatus(context.Background(), "one")
	if err != nil || st.Resources != camstream.PresenceUnknown {
		t.Fatal(st, err)
	}
	p := newPeer(t, "tcp")
	_, err = b.CreateSession(context.Background(), camstream.SessionSpec{ID: "exhausted", SourceKey: "one", Offer: offer(t, p)})
	requireCode(t, err, camstream.CodeCleanupIncomplete)
	if err = b.CloseSession(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	held, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address("127.0.0.1", b.cfg.MediaPorts.First))
	if err != nil {
		t.Fatal(err)
	}
	defer closeFixture(held)
	b.cfg.StartupTimeout = 300 * time.Millisecond
	p = newPeer(t, "tcp")
	_, err = b.CreateSession(context.Background(), camstream.SessionSpec{ID: "collision", SourceKey: "one", Offer: offer(t, p)})
	requireCode(t, err, camstream.CodeBackendUnavailable)
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", held.Addr().String())
	if err != nil {
		t.Fatal("unrelated listener affected", err)
	}
	closeFixture(conn)
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
	b.cfg.StartupTimeout = 4 * time.Second
	connect(t, b, "reused", "one", "tcp")
	t.Log("unknown cleanup retains slot; silent media bind collision rejected; unrelated listener preserved; port reused")
}
func TestIntegrationPinAndStaleRuntime(t *testing.T) {
	if os.Getenv("CAMSTREAM_GO2RTC_INTEGRATION") != "1" {
		t.Skip("integration runtime required")
	}
	cfg := testConfig(t)
	cfg.Executable = "/bin/true"
	b, err := New(cfg, testEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, b.Start(context.Background()), camstream.CodeBackendUnavailable)
	cfg = testConfig(t)
	if err = os.MkdirAll(cfg.RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cfg.RuntimeDir+"/unknown-worker", []byte("not a PID authority"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err = New(cfg, testEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, b.Start(context.Background()), camstream.CodeCleanupIncomplete)
	if _, err = os.Stat(cfg.RuntimeDir + "/unknown-worker"); err != nil {
		t.Fatal("unknown artifacts removed")
	}
}

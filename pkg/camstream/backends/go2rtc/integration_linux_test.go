//go:build linux

package go2rtc

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	pion "github.com/pion/webrtc/v4"
)

func integration(t *testing.T) *Backend {
	t.Helper()
	return integrationWith(t, testConfig(t))
}
func integrationWith(t *testing.T, cfg Config) *Backend {
	t.Helper()
	if os.Getenv("CAMSTREAM_GO2RTC_INTEGRATION") != "1" {
		t.Skip("requires pinned Linux arm64 --init container and controlled media")
	}
	b, err := New(cfg, testEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return b
}
func eventually(t *testing.T, limit time.Duration, check func() bool) {
	t.Helper()
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		if check() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func origin(t *testing.T, b *Backend, api, rtsp int) *worker {
	t.Helper()
	w := &worker{api: api, rtsp: rtsp, dir: t.TempDir()}
	body, err := b.workerConfig(w, Input{URL: "exec:ffmpeg -hide_banner -loglevel error -re -stream_loop -1 -i /fixture/clip.mkv -c copy -rtsp_transport tcp -f rtsp {output}"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.dir, "config.json")
	if err = os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	w.cmd = exec.Command(b.cfg.Executable, "-config", path) //nolint:noctx // Fixture uses the same explicit group termination/reap protocol.
	processGroup(w.cmd)
	w.cmd.Stdout = io.Discard
	w.cmd.Stderr = io.Discard
	if err = w.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := stopGroup(ctx, w.cmd, 100*time.Millisecond); err != nil {
			t.Error(err)
		}
	})
	eventually(t, time.Second, func() bool { return b.listenersOwned(w) == nil })
	return w
}

type peer struct {
	pc                     *pion.PeerConnection
	video, audio           atomic.Uint64
	videoCodec, audioCodec atomic.Value
}

func newPeer(t *testing.T, network string) *peer {
	t.Helper()
	engine := pion.SettingEngine{}
	nt := pion.NetworkTypeTCP4
	if network == "udp" {
		nt = pion.NetworkTypeUDP4
	}
	engine.SetNetworkTypes([]pion.NetworkType{nt})
	engine.SetIncludeLoopbackCandidate(true)
	engine.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	pc, err := pion.NewAPI(pion.WithSettingEngine(engine)).NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	p := &peer{pc: pc}
	pc.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
		if track.Kind() == pion.RTPCodecTypeVideo {
			p.videoCodec.Store(track.Codec().MimeType)
		} else {
			p.audioCodec.Store(track.Codec().MimeType)
		}
		for {
			_, _, readErr := track.ReadRTP()
			if readErr != nil {
				return
			}
			if track.Kind() == pion.RTPCodecTypeVideo {
				p.video.Add(1)
			} else {
				p.audio.Add(1)
			}
		}
	})
	for _, kind := range []pion.RTPCodecType{pion.RTPCodecTypeVideo, pion.RTPCodecTypeAudio} {
		if _, err = pc.AddTransceiverFromKind(kind, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := pc.Close(); err != nil {
			t.Error(err)
		}
	})
	return p
}
func offer(t *testing.T, p *peer) camstream.Offer {
	t.Helper()
	sdp, err := p.pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := pion.GatheringCompletePromise(p.pc)
	if err = p.pc.SetLocalDescription(sdp); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ICE gather deadline")
	}
	return camstream.Offer{SDP: p.pc.LocalDescription().SDP}
}
func connect(t *testing.T, b *Backend, id, key, network string) *peer {
	t.Helper()
	p := newPeer(t, network)
	state, err := b.CreateSession(context.Background(), camstream.SessionSpec{ID: camstream.SessionID(id), SourceKey: camstream.SourceKey(key), Offer: offer(t, p)})
	if err != nil {
		t.Fatal(err)
	}
	if state.Answer == nil {
		t.Fatal("no SDP answer")
	}
	if err = p.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: state.Answer.SDP}); err != nil {
		t.Fatal(err)
	}
	eventually(t, 3*time.Second, func() bool { return p.video.Load() > 0 && p.audio.Load() > 0 })
	return p
}
func lockedSource(t *testing.T, b *Backend, key string, fn func(*source)) {
	t.Helper()
	s, err := b.getSource(camstream.SourceKey(key))
	if err != nil {
		t.Fatal(err)
	}
	if err = enter(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	defer leave(s)
	fn(s)
}
func TestIntegrationSharedViewersAndFailures(t *testing.T) {
	b := integration(t)
	o1 := origin(t, b, 34000, 34001)
	o2 := origin(t, b, 34002, 34003)
	for _, v := range []struct {
		key  string
		port int
	}{{"one", o1.rtsp}, {"two", o2.rtsp}} {
		if _, err := b.EnsureStream(context.Background(), testSpec(v.key, relay(&worker{rtsp: v.port}).URL)); err != nil {
			t.Fatal(err)
		}
	}
	a, c := newPeer(t, "tcp"), newPeer(t, "udp")
	offers := []camstream.Offer{offer(t, a), offer(t, c)}
	states := make([]camstream.SessionState, 2)
	errs := make([]error, 2)
	_, invalidErr := b.CreateSession(context.Background(), camstream.SessionSpec{ID: "malformed", SourceKey: "one", Offer: camstream.Offer{SDP: "v=0\r\n"}})
	requireCode(t, invalidErr, camstream.CodeInvalidArgument)
	stInvalid, invalidErr := b.StreamStatus(context.Background(), "one")
	if invalidErr != nil || stInvalid.Fault != "" || stInvalid.Upstream != camstream.PresenceAbsent {
		t.Fatal("malformed offer affected source", stInvalid, invalidErr)
	}
	start := time.Now()
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			states[i], errs[i] = b.CreateSession(context.Background(), camstream.SessionSpec{ID: camstream.SessionID(fmt.Sprintf("first-%d", i)), SourceKey: "one", Offer: offers[i]})
		}()
	}
	wg.Wait()
	for i, p := range []*peer{a, c} {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if err := p.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: states[i].Answer.SDP}); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, 3*time.Second, func() bool {
		return a.video.Load() > 0 && a.audio.Load() > 0 && c.video.Load() > 0 && c.audio.Load() > 0
	})
	t.Logf("concurrent TCP/UDP first media: %s", time.Since(start))
	d := connect(t, b, "other", "two", "tcp")
	for i, p := range []*peer{a, c, d} {
		if p.videoCodec.Load() != "video/H264" || p.audioCodec.Load() != "audio/opus" {
			t.Fatal("unexpected codecs")
		}
		pair, pairErr := p.pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
		if pairErr != nil || pair == nil {
			t.Fatal("no selected ICE pair", pairErr)
		}
		want := "tcp"
		if i == 1 {
			want = "udp"
		}
		if pair.Remote.Protocol.String() != want {
			t.Fatal("unexpected media transport", pair)
		}
	}
	var workers, rssKiB int
	for _, key := range []string{"one", "two"} {
		lockedSource(t, b, key, func(s *source) {
			list := []*worker{s.ingest}
			for _, v := range s.sessions {
				list = append(list, v.worker)
			}
			for _, w := range list {
				workers++
				body, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", w.cmd.Process.Pid))
				if e != nil {
					t.Fatal(e)
				}
				for _, line := range strings.Split(string(body), "\n") {
					if strings.HasPrefix(line, "VmRSS:") {
						fields := strings.Fields(line)
						n, e := strconv.Atoi(fields[1])
						if e != nil {
							t.Fatal(e)
						}
						rssKiB += n
					}
				}
			}
		})
	}
	if workers != 5 {
		t.Fatal("C+V worker bound", workers)
	}
	t.Logf("C=2/V=3 workers=%d RSS=%d KiB; H264/Opus with selected TCP and UDP pairs", workers, rssKiB)

	obs, err := b.observe(context.Background(), o1)
	if err != nil || len(obs.Consumers) != 1 {
		t.Fatalf("shared origin: %+v %v", obs, err)
	}
	same, err := b.CreateSession(context.Background(), camstream.SessionSpec{ID: "first-0", SourceKey: "one", Offer: offers[0]})
	if err != nil || same.Answer.SDP != states[0].Answer.SDP {
		t.Fatal("duplicate negotiation changed", err)
	}
	_, err = b.CreateSession(context.Background(), camstream.SessionSpec{ID: "first-0", SourceKey: "one", Offer: offers[1]})
	requireCode(t, err, camstream.CodeConflict)
	var stopped, port int
	lockedSource(t, b, "one", func(s *source) {
		stopped = s.sessions["first-0"].worker.cmd.Process.Pid
		port = s.sessions["first-0"].worker.media
	})
	if err = syscall.Kill(stopped, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	if err = b.CloseSession(context.Background(), "first-0"); err != nil {
		t.Fatal(err)
	}
	t.Logf("forced close: %s", time.Since(start))
	beforeC, beforeD := c.video.Load(), d.video.Load()
	eventually(t, time.Second, func() bool { return c.video.Load() > beforeC && d.video.Load() > beforeD })
	connect(t, b, "replacement", "one", "tcp")
	lockedSource(t, b, "one", func(s *source) {
		if s.sessions["replacement"].worker.media != port {
			t.Fatal("released slot not reused")
		}
		if err = syscall.Kill(s.sessions["replacement"].worker.cmd.Process.Pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
	})
	eventually(t, 3*time.Second, func() bool {
		v, e := b.SessionStatus(context.Background(), "replacement")
		return e == nil && v.Resources == camstream.PresenceAbsent
	})
	beforeC = c.video.Load()
	eventually(t, time.Second, func() bool { return c.video.Load() > beforeC })
	lockedSource(t, b, "one", func(s *source) {
		if err = syscall.Kill(s.ingest.cmd.Process.Pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
	})
	eventually(t, 3*time.Second, func() bool {
		s, e := b.StreamStatus(context.Background(), "one")
		return e == nil && s.Fault == camstream.CodeSourceUnavailable && s.Upstream == camstream.PresenceAbsent
	})
	beforeD = d.video.Load()
	eventually(t, time.Second, func() bool { return d.video.Load() > beforeD })
	_, err = b.CreateSession(context.Background(), camstream.SessionSpec{ID: "latched", SourceKey: "one", Offer: offers[0]})
	requireCode(t, err, camstream.CodeSourceUnavailable)
	if err = b.ResetSource(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	connect(t, b, "reset", "one", "tcp")
	if err = b.CloseSession(context.Background(), "reset"); err != nil {
		t.Fatal(err)
	}
	eventually(t, time.Second, func() bool { o, e := b.observe(context.Background(), o1); return e == nil && len(o.Consumers) == 0 })
	if err = b.CloseSession(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	st, err := b.StreamStatus(context.Background(), "two")
	if err != nil || st.Upstream != camstream.PresenceAbsent {
		t.Fatal(st, err)
	}
	t.Log("shared acquisition, isolated close/crashes, slot reuse, source latch/reset and final release passed")
}

func closeFixture(c io.Closer) {
	_ = c.Close() //nolint:errcheck // Fault fixtures intentionally close already-closed/broken transports.
}

// The monitor skips a source while a negotiation holds it, so each negotiation
// must leave its own observation. Otherwise a viewer arriving just after finds
// none, or one too old for admission, and is refused (BUG-0003).
func TestIntegrationNegotiationRecordsObservation(t *testing.T) {
	cfg := testConfig(t)
	cfg.FailureTimeout, cfg.PollInterval = 30*time.Second, 20*time.Second // no monitor poll during the test
	b := integrationWith(t, cfg)
	o := origin(t, b, 34000, 34001)
	if _, err := b.EnsureStream(context.Background(), testSpec("one", relay(o).URL)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []camstream.SessionID{"first", "second"} {
		p := newPeer(t, "tcp")
		state, err := b.CreateSession(context.Background(), camstream.SessionSpec{ID: id, SourceKey: "one", Offer: offer(t, p)})
		if err != nil || state.Answer == nil || state.ObservedAt.IsZero() {
			t.Fatal(id, state, err)
		}
		// The source must be observed no earlier than the session's answer, however
		// long the negotiation took.
		st, err := b.StreamStatus(context.Background(), "one")
		if err != nil || st.Upstream != camstream.PresencePresent || st.ActiveSessions == nil || st.ObservedAt.Before(state.ObservedAt) {
			t.Fatalf("%s: source observed at %v, before the answer at %v: %+v %v", id, st.ObservedAt, state.ObservedAt, st, err)
		}
		if err = p.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: state.Answer.SDP}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegrationAbandonedNegotiation(t *testing.T) {
	b := integration(t)
	o := origin(t, b, 34000, 34001)
	if _, err := b.EnsureStream(context.Background(), testSpec("one", relay(o).URL)); err != nil {
		t.Fatal(err)
	}
	p := newPeer(t, "tcp")
	state, err := b.CreateSession(context.Background(), camstream.SessionSpec{ID: "unanswered", SourceKey: "one", Offer: offer(t, p)})
	if err != nil || state.Answer == nil {
		t.Fatal(state, err)
	}
	// Never apply the answer and never send a release request.
	eventually(t, 5*time.Second, func() bool {
		s, e := b.StreamStatus(context.Background(), "one")
		return e == nil && s.Upstream == camstream.PresenceAbsent
	})
	st, err := b.SessionStatus(context.Background(), "unanswered")
	if err != nil || st.Resources != camstream.PresenceAbsent {
		t.Fatal(st, err)
	}
	t.Log("unapplied answer/abandoned negotiation reclaimed without browser DELETE")
}

func TestIntegrationCanceledQueuedIDCannotBeReused(t *testing.T) {
	b := integration(t)
	if _, err := b.EnsureStream(context.Background(), testSpec("one", "rtsp://127.0.0.1:34001/stream")); err != nil {
		t.Fatal(err)
	}
	p := newPeer(t, "tcp")
	spec := camstream.SessionSpec{ID: "queued", SourceKey: "one", Offer: offer(t, p)}
	s, err := b.getSource("one")
	if err != nil {
		t.Fatal(err)
	}
	if err = enter(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, createErr := b.CreateSession(ctx, spec); done <- createErr }()
	eventually(t, time.Second, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.sessions[spec.ID] != nil })
	cancel()
	select {
	case err = <-done:
	case <-time.After(time.Second):
		leave(s)
		t.Fatal("queued cancellation blocked")
	}
	leave(s)
	if err == nil {
		t.Fatal("canceled queued create succeeded")
	}
	_, err = b.CreateSession(context.Background(), spec)
	requireCode(t, err, camstream.CodeConflict)
	state, err := b.SessionStatus(context.Background(), spec.ID)
	if err != nil || state.Resources != camstream.PresenceAbsent || state.State != camstream.StateFailed {
		t.Fatal(state, err)
	}
	if err = b.CloseSession(context.Background(), spec.ID); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	active := b.active
	b.mu.Unlock()
	if active != 0 {
		t.Fatal("queued reservation leaked")
	}
	t.Log("canceled queued ID remains a tombstone with no resource/capacity leak")
}

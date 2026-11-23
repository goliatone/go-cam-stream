package maccamera

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/conversations"
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

func TestAppSessionConversationOpenFailsClosedWithoutSettings(t *testing.T) {
	probe := newSessionProbe(t, testService(t), func(ctx context.Context) error {
		t.Error("opening an invitation started capture")
		<-ctx.Done()
		return nil
	})
	ready := probe.receive(t)
	if result, ok := ready.Result.(map[string]any); ready.Event != "ready" || !ok || result["conversations"] != float64(api.ConversationVersion) {
		t.Fatal("conversation capability missing from ready reply")
	}
	const secret = "private-invitation-fragment"
	request := AppRequest{Version: 1, ID: "open", Method: api.ConversationNativeOpen, InvitationURL: "https://unknown.test/r/fixture#" + secret}
	if err := json.NewEncoder(probe.in).Encode(request); err != nil {
		t.Fatal(err)
	}
	reply := probe.receive(t)
	encoded, err := json.Marshal(reply)
	if err != nil || reply.ID != request.ID || reply.Error == nil || reply.Error.Code != string(api.RoomPermissionDenied) || bytes.Contains(encoded, []byte(secret)) {
		t.Fatal("unexpected owner pipe rejection")
	}
	request.Method = "start"
	encoded, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeAppRequest(encoded); err == nil {
		t.Fatal("capture command accepted an invitation payload")
	}
	_ = probe.in.Close()
	probe.finished(t)
}

func TestAppSessionConversationListenerOutlivesCreateRequest(t *testing.T) {
	service := testService(t)
	if err := writeJSON(filepath.Join(service.Dir, "conversation-settings.json"), config.Conversations{Loopback: true}); err != nil {
		t.Fatal(err)
	}
	a := appSession{service: service, conversationAssets: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}}
	defer func() { a.conversation.Close() }()
	value, problem := a.dispatch(t.Context(), AppRequest{Method: api.ConversationNativeCreate})
	room, ok := value.(api.NativeConversationPrejoin)
	if !ok || problem != nil {
		t.Fatal("owner could not create a conversation")
	}
	page, err := url.Parse(room.PageURL)
	if err != nil {
		t.Fatal(err)
	}
	origin := page.Scheme + "://" + page.Host
	guest := testService(t)
	settings := config.Conversations{Loopback: true, TrustedOrigins: []string{origin}}
	if err := writeJSON(filepath.Join(guest.Dir, "conversation-settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	probe := newSessionProbe(t, guest, nil)
	probe.receive(t)
	open := AppRequest{Version: 1, ID: "open-link", Method: api.ConversationNativeOpen, InvitationURL: room.InvitationURL}
	if err := json.NewEncoder(probe.in).Encode(open); err != nil {
		t.Fatal(err)
	}
	if reply := probe.receive(t); reply.Error != nil || reply.ID != open.ID {
		t.Fatal("guest helper could not redeem after Create completed")
	}
	leave := AppRequest{Version: 1, ID: "leave", Method: api.ConversationNativeLeave, RoomID: room.Admission.Room.RoomID}
	if err := json.NewEncoder(probe.in).Encode(leave); err != nil {
		t.Fatal(err)
	}
	if reply := probe.receive(t); reply.Error != nil || reply.ID != leave.ID {
		t.Fatal("native leave before loading the page failed")
	}
	if err := json.NewEncoder(probe.in).Encode(open); err != nil {
		t.Fatal(err)
	}
	second := probe.receive(t)
	if second.Error != nil {
		t.Fatal("native leave retained the guest slot")
	}
	var admission api.NativeConversationPrejoin
	encoded, err := json.Marshal(second.Result)
	if err != nil || json.Unmarshal(encoded, &admission) != nil || admission.Admission.Role != api.RoomGuest {
		t.Fatal("missing scoped prior guest admission")
	}
	replacement := testService(t)
	if err := writeJSON(filepath.Join(replacement.Dir, "conversation-settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	retry := newSessionProbe(t, replacement, nil)
	retry.receive(t)
	if err := json.NewEncoder(retry.in).Encode(open); err != nil {
		t.Fatal(err)
	}
	if reply := retry.receive(t); reply.Error == nil || reply.Error.Code != string(api.RoomFull) {
		t.Fatal("the prior owner's admission did not hold the seat")
	}
	leave.GuestDeparture = &api.NativeGuestDeparture{PageURL: admission.PageURL, Token: admission.Admission.Token}
	if err := json.NewEncoder(retry.in).Encode(leave); err != nil {
		t.Fatal(err)
	}
	if reply := retry.receive(t); reply.Error != nil {
		t.Fatal("replacement could not release the old scoped seat")
	}
	if err := json.NewEncoder(retry.in).Encode(open); err != nil {
		t.Fatal(err)
	}
	if reply := retry.receive(t); reply.Error != nil {
		t.Fatal("the recovered seat could not be redeemed")
	}
	_ = retry.in.Close()
	retry.finished(t)
	_ = probe.in.Close()
	probe.finished(t)
	const lostToken = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"
	if _, err := conversations.RedeemRecoverableLink(t.Context(), room.InvitationURL, []string{origin}, true, lostToken); err != nil {
		t.Fatal("guest owner EOF retained the slot")
	}
	// Simulate a grant committed by the host whose reply never reached the
	// native app. A fresh owner has only the invitation and chosen token.
	lost := newSessionProbe(t, replacement, nil)
	lost.receive(t)
	cancelled := AppRequest{Version: 1, ID: "cancelled", Method: api.ConversationNativeLeave,
		InvitationURL: room.InvitationURL, GuestToken: lostToken}
	if err := json.NewEncoder(lost.in).Encode(cancelled); err != nil {
		t.Fatal(err)
	}
	if reply := lost.receive(t); reply.Error != nil || reply.ID != cancelled.ID {
		t.Fatal("replacement could not release an admission whose reply was lost")
	}
	if _, err := conversations.RedeemLoopbackLink(t.Context(), room.InvitationURL, []string{origin}); err != nil {
		t.Fatal("the cancelled admission retained the guest slot")
	}
	_ = lost.in.Close()
	lost.finished(t)
	if _, fault := a.dispatch(t.Context(), AppRequest{Method: api.ConversationNativeEnd, RoomID: room.Admission.Room.RoomID}); fault != nil {
		t.Fatal("owner could not end the room")
	}
	if _, err := conversations.RedeemLoopbackLink(t.Context(), room.InvitationURL, []string{origin}); err != conversations.Fault(api.RoomAccessEnded) {
		t.Fatal("ended room still permits admission")
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
	select {
	case err := <-other.done:
		if !errors.Is(err, ErrAppSessionBusy) {
			t.Fatalf("duplicate session exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("duplicate session did not exit")
	}
	lock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if reply := owner.request(t, "start", nil); reply.Error == nil || reply.Error.Code != "busy" || reply.Result != nil {
		t.Fatalf("start accepted while another supervisor holds the instance without a socket: %+v", reply)
	}
	if reply := owner.request(t, "stop", nil); reply.Error == nil || reply.Error.Code != "busy" || reply.Result != nil {
		t.Fatalf("stop confirmed release while another supervisor holds the instance: %+v", reply)
	}
	if _, err = s.lock(); !errors.Is(err, errCameraBusy) {
		t.Fatalf("stop changed the non-owned instance lock: %v", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	reply := owner.request(t, "stop", nil)
	status, ok := reply.Result.(map[string]any)
	if reply.Error != nil || !ok || status["state"] != "stopped" {
		t.Fatalf("stop did not confirm release after the supervisor exited: %+v", reply)
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

func TestAppSessionExportDoesNotStopOwnedCapture(t *testing.T) {
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
	if r.Error != nil {
		t.Fatal("export of saved settings refused during supervisor startup")
	}
	// Export neither replaces nor ends the owned capture, including the socket gap.
	if r = p.request(t, "start", nil); r.Error == nil || r.Error.Code != "busy" {
		t.Fatal("export released capture ownership")
	}
	p.request(t, "stop", nil)
	if r = p.request(t, "export", nil); r.Error != nil {
		t.Fatal("export refused after owned capture reaped")
	}
	p.request(t, "quit", nil)
	p.finished(t)
}

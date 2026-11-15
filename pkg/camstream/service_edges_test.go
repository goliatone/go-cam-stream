package camstream_test

import (
	"context"
	"errors"
	"testing"
	"time"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

func TestServiceShutdownContinuesPastFailedCamera(t *testing.T) {
	b := newMemoryBackend()
	s := testService(t, b, cs.ServiceOptions{})
	failed := openViewer(t, s, "one", "one", "main")
	healthy := openViewer(t, s, "two", "two", "main")
	b.mu.Lock()
	b.failedSource = failed.SourceKey
	b.mu.Unlock()
	code(t, s.Close(context.Background()), cs.CodeCleanupIncomplete)
	state, err := s.SessionStatus(context.Background(), healthy.ID)
	if err != nil || state.Resources != cs.PresenceAbsent {
		t.Fatal("failed camera prevented independent drain", state, err)
	}
	b.mu.Lock()
	b.failedSource = ""
	b.mu.Unlock()
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceQueuedCancellationAndCrossDeviceProgress(t *testing.T) {
	b := newMemoryBackend()
	entered, release := make(chan struct{}), make(chan struct{})
	b.create = func(ctx context.Context, spec cs.SessionSpec) error {
		if spec.SourceKey != "one/main" {
			return nil
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s := testService(t, b, cs.ServiceOptions{})
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateSession(context.Background(), testCamera("one", "one"), "main", cs.Offer{SDP: "offer"})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("create did not start")
	}
	openViewer(t, s, "two", "two", "main") // unrelated source does not wait on one
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	state, err := s.CreateSession(ctx, testCamera("alias", "one"), "main", cs.Offer{SDP: "offer"})
	if !errors.Is(err, context.DeadlineExceeded) || state.ID == "" || state.Resources != cs.PresenceAbsent {
		t.Fatal(state, err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	creates := b.creates
	b.mu.Unlock()
	if creates != 2 {
		t.Fatal("queued canceled request reached backend", creates)
	}
}

func TestServiceRuntimeCancellationAndDrainRetry(t *testing.T) {
	b := newMemoryBackend()
	s, err := cs.NewService(b, cs.ServiceOptions{CleanupTimeout: 100 * time.Millisecond, PollInterval: 5 * time.Millisecond}, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = s.Start(runtime); err != nil {
		t.Fatal(err)
	}
	v := openViewer(t, s, "room", "one", "main")
	b.mu.Lock()
	b.closeFailure = true
	b.mu.Unlock()
	cancel()
	code(t, s.Close(context.Background()), cs.CodeCleanupIncomplete)
	_, err = s.CreateSession(context.Background(), testCamera("room", "one"), "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeBackendUnavailable)
	b.mu.Lock()
	b.closeFailure = false
	b.mu.Unlock()
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := s.SessionStatus(context.Background(), v.ID)
	if err != nil || state.Resources != cs.PresenceAbsent {
		t.Fatal(state, err)
	}
	code(t, s.Start(context.Background()), cs.CodeConflict)
}

func TestServiceCloseCancelsInflightCreate(t *testing.T) {
	b := newMemoryBackend()
	entered := make(chan struct{})
	b.create = func(ctx context.Context, _ cs.SessionSpec) error { close(entered); <-ctx.Done(); return ctx.Err() }
	s := testService(t, b, cs.ServiceOptions{CleanupTimeout: time.Second})
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateSession(context.Background(), testCamera("one", "one"), "main", cs.Offer{SDP: "offer"})
		done <- err
	}()
	<-entered
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestServiceUnknownUpstreamRetainsDeviceAndRecovers(t *testing.T) {
	b := newMemoryBackend()
	s := testService(t, b, cs.ServiceOptions{})
	v := openViewer(t, s, "room", "one", "main")
	b.mu.Lock()
	b.sourceUnknown = true
	b.mu.Unlock()
	code(t, s.CloseSession(context.Background(), v.ID), cs.CodeCleanupIncomplete)
	_, err := s.CreateSession(context.Background(), testCamera("room", "one"), "sub", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCameraBusy)
	state, err := s.StreamStatus(context.Background(), testCamera("room", "one"), "main")
	if err != nil || state.Upstream != cs.PresenceUnknown || state.ActiveSessions != nil {
		t.Fatal(state, err)
	}
	b.mu.Lock()
	b.sourceUnknown = false
	b.mu.Unlock()
	if err = s.CloseSession(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	openViewer(t, s, "room", "one", "sub")
}

func TestServiceResetCancellationAndFailedReset(t *testing.T) {
	b := newMemoryBackend()
	entered := make(chan struct{})
	b.create = func(ctx context.Context, _ cs.SessionSpec) error { close(entered); <-ctx.Done(); return ctx.Err() }
	s := testService(t, b, cs.ServiceOptions{})
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateSession(context.Background(), testCamera("one", "one"), "main", cs.Offer{SDP: "offer"})
		done <- err
	}()
	<-entered
	ids, err := s.ResetSource(context.Background(), "one/main")
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.create = nil
	b.mu.Unlock()
	v := openViewer(t, s, "room", "one", "main")
	b.mu.Lock()
	b.closeFailure = true
	b.mu.Unlock()
	_, err = s.ResetSource(context.Background(), v.SourceKey)
	code(t, err, cs.CodeCleanupIncomplete)
	_, err = s.CreateSession(context.Background(), testCamera("room", "one"), "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCleanupIncomplete)
	b.mu.Lock()
	b.closeFailure = false
	b.mu.Unlock()
	if _, err = s.ResetSource(context.Background(), v.SourceKey); err != nil {
		t.Fatal(err)
	}
	openViewer(t, s, "room", "one", "sub")
}

func TestServiceResetRemovedIdleSource(t *testing.T) {
	for _, mode := range []string{"idle", "absence-unconfirmed", "live-demand"} {
		t.Run(mode, func(t *testing.T) {
			b := newMemoryBackend()
			s := testService(t, b, cs.ServiceOptions{})
			v := openViewer(t, s, "room", "one", "main")
			b.mu.Lock()
			spec := b.sources[v.SourceKey]
			b.mu.Unlock()
			if mode != "live-demand" {
				if err := s.CloseSession(context.Background(), v.ID); err != nil {
					t.Fatal(err)
				}
				b.mu.Lock()
				b.stale = true
				b.mu.Unlock()
				if _, err := s.StreamStatus(context.Background(), testCamera("room", "one"), "main"); err != nil {
					t.Fatal(err)
				}
			}
			b.mu.Lock()
			if mode == "live-demand" {
				delete(b.sources, v.SourceKey) // Backend loses registration while demand remains.
			}
			_, registered := b.sources[v.SourceKey]
			b.stale = false
			b.sourceUnknown = mode == "absence-unconfirmed"
			b.mu.Unlock()
			if registered {
				t.Fatal("idle source was not removed")
			}
			ids, err := s.ResetSource(context.Background(), v.SourceKey)
			if mode != "idle" {
				code(t, err, cs.CodeCleanupIncomplete)
				b.mu.Lock()
				b.sources[v.SourceKey], b.sourceUnknown = spec, false
				b.mu.Unlock()
				if _, err = s.ResetSource(context.Background(), v.SourceKey); err != nil {
					t.Fatal("reset after restored backend evidence", err)
				}
				return
			}
			if err != nil || len(ids) != 0 {
				t.Fatal(ids, err)
			}
			openViewer(t, s, "room", "one", "main")
		})
	}
}

func TestServiceConstructionConflictsAndBoundedRecords(t *testing.T) {
	b := newMemoryBackend()
	_, err := cs.NewService(b, cs.ServiceOptions{}, ownedStrategy{}, ownedStrategy{})
	code(t, err, cs.CodeInvalidArgument)
	_, err = cs.NewService(b, cs.ServiceOptions{MaxViewers: -1}, ownedStrategy{})
	code(t, err, cs.CodeInvalidArgument)
	s, err := cs.NewService(b, cs.ServiceOptions{MaxViewers: 1, MaxSessionRecords: 2}, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateSession(context.Background(), testCamera("room", "one"), "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeBackendUnavailable)
	b.mu.Lock()
	if len(b.sources) != 0 || b.creates != 0 {
		t.Fatal("constructor/closed admission performed backend I/O")
	}
	b.mu.Unlock()
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := s.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	state, err := s.StreamStatus(context.Background(), testCamera("room", "one"), "main")
	if err != nil || state.Upstream != cs.PresenceAbsent {
		t.Fatal(state, err)
	}
	v := openViewer(t, s, "room", "one", "main")
	changed := testCamera("alias", "one")
	changed.Config = ownedConfig("different input")
	_, err = s.CreateSession(context.Background(), changed, "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCapacityExceeded)
	if err = s.CloseSession(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateSession(context.Background(), changed, "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeConflict)
	_, err = s.CreateSession(context.Background(), testCamera("room", "one"), "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCapacityExceeded)
}

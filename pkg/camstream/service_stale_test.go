package camstream_test

import (
	"context"
	"errors"
	"testing"
	"time"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

// A source that is unknown only because its observations are old makes a new
// viewer wait for a newer observation rather than refusing it as unconfirmed
// cleanup (BUG-0003). Any other unknown still refuses at once.

func mainState(s *cs.Service) cs.State {
	state, err := s.StreamSnapshot(context.Background(), testCamera("room", "one"), "main")
	if err != nil {
		return ""
	}
	return state.State
}

// observed waits for a current, known observation of the camera's main stream.
// A stream not yet observed also reads as unknown, so an unknown seen after this
// comes from a later poll.
func observed(t *testing.T, s *cs.Service) {
	t.Helper()
	eventuallyService(t, func() bool { return mainState(s) != cs.StateUnknown })
}

// unknownSource applies a backend change, then waits until the service's own
// observation reports the camera's main stream as unknown.
func unknownSource(t *testing.T, b *memoryBackend, s *cs.Service, change func()) {
	t.Helper()
	b.mu.Lock()
	change()
	b.mu.Unlock()
	eventuallyService(t, func() bool { return mainState(s) == cs.StateUnknown })
}

// join opens another viewer of the camera's main stream and reports the result.
func join(ctx context.Context, s *cs.Service) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateSession(ctx, testCamera("room", "one"), "main", cs.Offer{SDP: "offer"})
		done <- err
	}()
	return done
}

func TestServiceNewcomerWaitsOutStaleObservation(t *testing.T) {
	for _, mode := range []string{"source", "viewer"} {
		t.Run(mode, func(t *testing.T) {
			b := newMemoryBackend()
			s := testService(t, b, cs.ServiceOptions{ObservationMaxAge: time.Second})
			openViewer(t, s, "room", "one", "main")
			observed(t, s)
			unknownSource(t, b, s, func() {
				b.stale = mode == "source"
				b.sessionStale = mode == "viewer"
			})
			done := join(context.Background(), s)
			select {
			case err := <-done:
				t.Fatal("refused instead of waiting for an observation", err)
			case <-time.After(50 * time.Millisecond): // ten polls
			}
			b.mu.Lock()
			b.stale, b.sessionStale = false, false
			b.mu.Unlock()
			if err := <-done; err != nil {
				t.Fatal("not admitted once observed again", err)
			}
		})
	}
}

func TestServiceUnobservedSourceRefusesRetryably(t *testing.T) {
	b := newMemoryBackend()
	s := testService(t, b, cs.ServiceOptions{MaxViewers: 2, ObservationMaxAge: 60 * time.Millisecond})
	openViewer(t, s, "room", "one", "main")
	observed(t, s)
	unknownSource(t, b, s, func() { b.stale = true })
	started := time.Now()
	err := <-join(context.Background(), s)
	code(t, err, cs.CodeBackendUnavailable)
	if !cs.DescribeError(err).Retryable {
		t.Fatal("the refusal is not retryable", err)
	}
	if waited := time.Since(started); waited < 60*time.Millisecond {
		t.Fatal("refused before ObservationMaxAge", waited)
	}
	b.mu.Lock()
	b.stale = false
	b.mu.Unlock()
	// The refused viewer held no slot while it waited, so a second one still fits.
	if err = <-join(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func TestServiceOtherUnknownRefusesAtOnce(t *testing.T) {
	for _, mode := range []string{"upstream", "session", "stale-and-upstream", "failed-close"} {
		t.Run(mode, func(t *testing.T) {
			b := newMemoryBackend()
			s := testService(t, b, cs.ServiceOptions{ObservationMaxAge: time.Second})
			v := openViewer(t, s, "room", "one", "main")
			observed(t, s)
			if mode == "failed-close" {
				b.mu.Lock()
				b.closeFailure = true
				b.mu.Unlock()
				code(t, s.CloseSession(context.Background(), v.ID), cs.CodeCleanupIncomplete)
			}
			unknownSource(t, b, s, func() {
				b.sourceUnknown = mode == "upstream" || mode == "stale-and-upstream"
				b.sessionUnknown = mode == "session"
				b.stale = mode == "stale-and-upstream"
			})
			started := time.Now()
			code(t, <-join(context.Background(), s), cs.CodeCleanupIncomplete)
			if waited := time.Since(started); waited > 500*time.Millisecond {
				t.Fatal("waited for an observation", waited)
			}
		})
	}
}

func TestServiceObservationWaitEndsOnCancelAndShutdown(t *testing.T) {
	b := newMemoryBackend()
	runtime, shutdown := context.WithCancel(context.Background())
	s, err := cs.NewService(b, cs.ServiceOptions{PollInterval: 5 * time.Millisecond, ObservationMaxAge: 5 * time.Second}, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(runtime); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.mu.Lock()
		b.stale = false
		b.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if closeErr := s.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	openViewer(t, s, "room", "one", "main")
	observed(t, s)
	unknownSource(t, b, s, func() { b.stale = true })

	ctx, cancel := context.WithCancel(context.Background())
	done := join(ctx, s)
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("canceling the request did not end the wait", err)
	}

	done = join(context.Background(), s)
	time.Sleep(20 * time.Millisecond)
	shutdown()
	select {
	case err = <-done:
		code(t, err, cs.CodeBackendUnavailable)
	case <-time.After(time.Second):
		t.Fatal("shutdown did not end the wait")
	}
}

package camstream_test

import (
	"context"
	"fmt"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"testing"
	"time"
)

type transientBackend struct{ *memoryBackend }

func (b transientBackend) RetireTransientSessions(_ context.Context, key cs.SourceKey, ids []cs.SessionID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range ids {
		if v, ok := b.sessions[id]; ok && (v.SourceKey != key || v.Resources != cs.PresenceAbsent) {
			return cs.NewError(cs.CodeCleanupIncomplete, nil)
		}
	}
	for _, id := range ids {
		delete(b.sessions, id)
	}
	return nil
}
func TestTransientRetirementBoundsMetadataAndPreservesViewers(t *testing.T) {
	backend := transientBackend{newMemoryBackend()}
	s, err := cs.NewService(backend, cs.ServiceOptions{MaxSources: 2, MaxViewers: 2, MaxSessionRecords: 2, PollInterval: time.Millisecond}, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	ordinary := testCamera("ordinary", "ordinary")
	viewer, err := s.CreateSession(ctx, ordinary, "main", cs.Offer{SDP: "offer"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RetireTransientSource(ctx, ordinary, "main"); err == nil {
		t.Fatal("ordinary source retired")
	}
	for n := range 20 {
		camera := testCamera(fmt.Sprintf("test-%d", n), fmt.Sprintf("device-%d", n))
		test, err := s.CreateTransientSession(ctx, camera, "main", cs.Offer{SDP: "offer"})
		if err != nil {
			t.Fatal("probe metadata exhausted", n, err)
		}
		if err = s.RetireTransientSource(ctx, camera, "main"); err == nil {
			t.Fatal("live probe retired")
		}
		if _, err = s.CreateSession(ctx, camera, "main", cs.Offer{SDP: "offer"}); err == nil {
			t.Fatal("ordinary viewer joined transient source")
		}
		if err = s.CloseSession(ctx, test.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.RetireTransientSource(ctx, camera, "main"); err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.SessionBinding(test.ID); err == nil {
			t.Fatal("terminal probe metadata retained")
		}
		if _, _, err = s.SessionBinding(viewer.ID); err != nil {
			t.Fatal("ordinary metadata lost")
		}
	}
}
func TestTransientUnknownCleanupRetainsReservation(t *testing.T) {
	backend := transientBackend{newMemoryBackend()}
	s, err := cs.NewService(backend, cs.ServiceOptions{MaxSources: 1}, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	camera := testCamera("probe", "probe")
	session, err := s.CreateTransientSession(ctx, camera, "main", cs.Offer{SDP: "offer"})
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.closeFailure = true
	backend.mu.Unlock()
	if s.CloseSession(ctx, session.ID) == nil {
		t.Fatal("unknown cleanup accepted")
	}
	if s.RetireTransientSource(ctx, camera, "main") == nil {
		t.Fatal("unknown source retired")
	}
	if _, err = s.CreateTransientSession(ctx, testCamera("other", "other"), "main", cs.Offer{SDP: "offer"}); err == nil {
		t.Fatal("reservation released")
	}
	backend.mu.Lock()
	backend.closeFailure = false
	backend.mu.Unlock()
	if err = s.CloseSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RetireTransientSource(ctx, camera, "main"); err != nil {
		t.Fatal(err)
	}
}

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

func TestPreparedSourceLendsOnlyItsOwnSlot(t *testing.T) {
	backend := transientBackend{newMemoryBackend()}
	ctx := context.Background()
	s, err := cs.NewService(backend, cs.ServiceOptions{MaxSources: 1}, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	camera := testCamera("camera", "device")
	offer := cs.Offer{SDP: "offer"}
	original, err := s.CreateSession(ctx, camera, "main", offer)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CloseSession(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareInactiveSource(ctx, camera, "main"); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTransientSession(ctx, testCamera("other", "other"), "main", offer)
	code(t, err, cs.CodeCapacityExceeded)
	probe, err := s.CreateTransientSession(ctx, camera, "sub", offer)
	if err != nil {
		t.Fatal("a prepared source should make room for its own test", err)
	}
	if err = s.CloseSession(ctx, probe.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RetireTransientSource(ctx, camera, "sub"); err != nil {
		t.Fatal(err)
	}
	if err = s.DiscardPreparedSource(original.SourceKey); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSession(ctx, camera, "sub", offer); err != nil {
		t.Fatal("the replacement could not use the released slot", err)
	}
}

func TestPreparedSourceAndProbeLeaveCapacityForUnrelatedCamera(t *testing.T) {
	backend := transientBackend{newMemoryBackend()}
	ctx := context.Background()
	s, err := cs.NewService(backend, cs.ServiceOptions{MaxSources: 2}, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	camera, other := testCamera("camera", "device"), testCamera("other", "other")
	offer := cs.Offer{SDP: "offer"}
	saved, err := s.CreateSession(ctx, camera, "main", offer)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CloseSession(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareInactiveSource(ctx, camera, "main"); err != nil {
		t.Fatal(err)
	}
	probe, err := s.CreateTransientSession(ctx, camera, "sub", offer)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.CreateSession(ctx, other, "main", offer)
	if err != nil {
		t.Fatal("credential preview used the unrelated camera slot", err)
	}
	_, err = s.CreateSession(ctx, testCamera("third", "third"), "main", offer)
	code(t, err, cs.CodeCapacityExceeded)
	_, err = s.CreateSession(ctx, camera, "main", offer)
	code(t, err, cs.CodeCameraBusy)

	if err = s.CloseSession(ctx, probe.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateSession(ctx, camera, "main", offer)
	code(t, err, cs.CodeCameraBusy)
	if err = s.RetireTransientSource(ctx, camera, "sub"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSession(ctx, camera, "main", offer); err != nil {
		t.Fatal("saved source could not reuse its slot after retirement", err)
	}
	if state, err := s.SessionStatus(ctx, viewer.ID); err != nil || state.Resources != cs.PresencePresent {
		t.Fatal("unrelated session interrupted", state, err)
	}
}

type alternateProfileStrategy struct{ ownedStrategy }

func (alternateProfileStrategy) Resolve(ctx context.Context, camera cs.Camera, stream cs.StreamID) (cs.StreamSpec, error) {
	if stream != "alternate" {
		return (ownedStrategy{}).Resolve(ctx, camera, stream)
	}
	spec, err := (ownedStrategy{}).Resolve(ctx, camera, "sub")
	spec.Key = cs.SourceKey(string(camera.PhysicalID) + "/alternate")
	return spec, err
}

type probeRetirementBackend struct {
	transientBackend
	failRetirement bool
}

func (b *probeRetirementBackend) RetireTransientSessions(ctx context.Context, key cs.SourceKey, ids []cs.SessionID) error {
	if b.failRetirement {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	return b.transientBackend.RetireTransientSessions(ctx, key, ids)
}

func TestUnconfirmedProbeRetirementFencesEveryProfileOnDevice(t *testing.T) {
	backend := &probeRetirementBackend{transientBackend{newMemoryBackend()}, true}
	ctx := context.Background()
	s, err := cs.NewService(backend, cs.ServiceOptions{MaxSources: 3}, alternateProfileStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	camera := testCamera("camera", "device")
	offer := cs.Offer{SDP: "offer"}
	saved, err := s.CreateSession(ctx, camera, "main", offer)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CloseSession(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareInactiveSource(ctx, camera, "main"); err != nil {
		t.Fatal(err)
	}
	probe, err := s.CreateTransientSession(ctx, camera, "sub", offer)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CloseSession(ctx, probe.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResetSource(ctx, probe.SourceKey); err != nil {
		t.Fatal(err)
	}
	code(t, s.RetireTransientSource(ctx, camera, "sub"), cs.CodeCleanupIncomplete)
	for _, profile := range []cs.StreamID{"main", "alternate"} {
		_, err = s.CreateSession(ctx, camera, profile, offer)
		code(t, err, cs.CodeCameraBusy)
	}
	other, err := s.CreateSession(ctx, testCamera("other", "other"), "main", offer)
	if err != nil {
		t.Fatal("unrelated camera blocked by failed retirement", err)
	}
	backend.failRetirement = false
	if err = s.RetireTransientSource(ctx, camera, "sub"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSession(ctx, camera, "alternate", offer); err != nil {
		t.Fatal("confirmed retirement did not release the device", err)
	}
	if state, err := s.SessionStatus(ctx, other.ID); err != nil || state.Resources != cs.PresencePresent {
		t.Fatal("unrelated session did not survive cleanup", state, err)
	}
}

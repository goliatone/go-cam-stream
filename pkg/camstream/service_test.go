package camstream_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type ownedConfig string

func (ownedConfig) StrategyID() cs.StrategyID { return "test" }

type ownedSource string

func (ownedSource) Kind() cs.SourceKind { return "example.test/source" }

type ownedStrategy struct{}

func (ownedStrategy) ID() cs.StrategyID { return "test" }
func (ownedStrategy) Streams(ctx context.Context, camera cs.Camera) ([]cs.Stream, error) {
	_, err := (ownedStrategy{}).Resolve(ctx, camera, "main")
	return []cs.Stream{{ID: "main", Capabilities: cs.MediaCapabilities{Video: true}}, {ID: "sub", Capabilities: cs.MediaCapabilities{Video: true}}}, err
}
func (ownedStrategy) Resolve(ctx context.Context, camera cs.Camera, profile cs.StreamID) (cs.StreamSpec, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamSpec{}, err
	}
	config, ok := camera.Config.(ownedConfig)
	if !ok || camera.PhysicalID == "" {
		return cs.StreamSpec{}, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	if profile != "main" && profile != "sub" {
		return cs.StreamSpec{}, cs.NewError(cs.CodeUnsupported, nil)
	}
	return cs.StreamSpec{Key: cs.SourceKey(string(camera.PhysicalID) + "/" + string(profile)), PhysicalCameraID: camera.PhysicalID,
		Source: ownedSource(config), Capabilities: cs.MediaCapabilities{Video: true}, Constraints: cs.DeviceConstraints{MaxActiveSources: 1}}, nil
}
func testCamera(alias, device string) cs.Camera {
	return cs.Camera{ID: cs.CameraID(alias), PhysicalID: cs.PhysicalCameraID(device), Config: ownedConfig("test-input")}
}

// The test backend implements only neutral contracts. It can leave partial
// resources on error and reports unknown/stale liveness explicitly.
type memoryBackend struct {
	mu                                                 sync.Mutex
	sources                                            map[cs.SourceKey]cs.StreamSpec
	sessions                                           map[cs.SessionID]cs.SessionState
	faults                                             map[cs.SourceKey]cs.ErrorCode
	create                                             func(context.Context, cs.SessionSpec) error
	closeFailure, sourceUnknown, sessionUnknown, stale bool
	failedSource                                       cs.SourceKey
	creates, closes, resets                            int
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{sources: map[cs.SourceKey]cs.StreamSpec{}, sessions: map[cs.SessionID]cs.SessionState{}, faults: map[cs.SourceKey]cs.ErrorCode{}}
}
func (b *memoryBackend) EnsureStream(ctx context.Context, spec cs.StreamSpec) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, exists := b.sources[spec.Key]; exists && old.Source != spec.Source {
		return cs.StreamState{}, cs.NewError(cs.CodeConflict, nil)
	}
	b.sources[spec.Key] = spec
	return b.sourceState(spec.Key), nil
}
func (b *memoryBackend) sourceState(key cs.SourceKey) cs.StreamState {
	n := 0
	for _, v := range b.sessions {
		if v.SourceKey == key && v.Resources != cs.PresenceAbsent {
			n++
		}
	}
	out := cs.StreamState{Key: key, State: cs.StateIdle, Upstream: cs.PresenceAbsent, ActiveSessions: &n, ObservedAt: time.Now(), Fault: b.faults[key]}
	if n > 0 {
		out.Upstream = cs.PresencePresent
	}
	if b.sourceUnknown {
		out.Upstream, out.ActiveSessions = cs.PresenceUnknown, nil
	}
	if b.stale {
		out.ObservedAt = time.Now().Add(-time.Hour)
	}
	return out
}
func (b *memoryBackend) StreamStatus(ctx context.Context, key cs.SourceKey) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.sources[key]; !ok {
		return cs.StreamState{}, cs.NewError(cs.CodeNotFound, nil)
	}
	return b.sourceState(key), nil
}
func (b *memoryBackend) RemoveStream(ctx context.Context, key cs.SourceKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.sourceState(key)
	if state.Upstream != cs.PresenceAbsent || b.faults[key] != "" {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	delete(b.sources, key)
	return nil
}
func (b *memoryBackend) CreateSession(ctx context.Context, spec cs.SessionSpec) (cs.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return cs.SessionState{}, err
	}
	b.mu.Lock()
	b.creates++
	if fault := b.faults[spec.SourceKey]; fault != "" {
		b.mu.Unlock()
		return cs.SessionState{}, cs.NewError(fault, nil)
	}
	v := cs.SessionState{ID: spec.ID, SourceKey: spec.SourceKey, State: cs.StateConnecting, Resources: cs.PresencePresent, ObservedAt: time.Now()}
	b.sessions[spec.ID] = v
	hook := b.create
	b.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, spec); err != nil {
			return v, err
		}
	}
	v.Answer = &cs.Answer{SDP: "test-answer"}
	return v, nil
}
func (b *memoryBackend) SessionStatus(ctx context.Context, id cs.SessionID) (cs.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return cs.SessionState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.sessions[id]
	if !ok {
		return cs.SessionState{}, cs.NewError(cs.CodeNotFound, nil)
	}
	v.ObservedAt = time.Now()
	if b.sessionUnknown {
		v.State, v.Resources = cs.StateUnknown, cs.PresenceUnknown
	}
	if b.stale {
		v.ObservedAt = time.Now().Add(-time.Hour)
	}
	return v, nil
}
func (b *memoryBackend) CloseSession(ctx context.Context, id cs.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closes++
	if b.closeFailure || (b.failedSource != "" && b.sessions[id].SourceKey == b.failedSource) {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	if v, ok := b.sessions[id]; ok {
		v.Resources, v.State = cs.PresenceAbsent, cs.StateClosed
		b.sessions[id] = v
	}
	return nil
}
func (b *memoryBackend) ResetSource(ctx context.Context, key cs.SourceKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resets++
	if _, exists := b.sources[key]; !exists {
		return cs.NewError(cs.CodeNotFound, nil)
	}
	if b.closeFailure {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	for id, v := range b.sessions {
		if v.SourceKey == key {
			v.Resources, v.State = cs.PresenceAbsent, cs.StateClosed
			b.sessions[id] = v
		}
	}
	delete(b.faults, key)
	return nil
}
func testService(t *testing.T, b *memoryBackend, options cs.ServiceOptions) *cs.Service {
	t.Helper()
	if options.PollInterval == 0 {
		options.PollInterval = 5 * time.Millisecond
	}
	s, err := cs.NewService(b, options, ownedStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.mu.Lock()
		b.closeFailure, b.sourceUnknown, b.sessionUnknown, b.stale = false, false, false, false
		b.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}
func openViewer(t *testing.T, s *cs.Service, alias, device, profile string) cs.SessionState {
	t.Helper()
	v, err := s.CreateSession(context.Background(), testCamera(alias, device), cs.StreamID(profile), cs.Offer{SDP: "test-offer"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func code(t *testing.T, err error, want cs.ErrorCode) {
	t.Helper()
	if cs.DescribeError(err).Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}
func eventuallyService(t *testing.T, check func() bool) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		if check() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func TestServiceAliasesBudgetsAndIndependentDevices(t *testing.T) {
	b := newMemoryBackend()
	s := testService(t, b, cs.ServiceOptions{MaxViewers: 3, PerSourceViewers: 2})
	a := openViewer(t, s, "room1", "one", "main")
	alias := openViewer(t, s, "hall", "one", "main")
	other := openViewer(t, s, "room2", "two", "main")
	if a.ID == alias.ID || a.SourceKey != alias.SourceKey {
		t.Fatal("alias/session identity")
	}
	_, err := s.CreateSession(context.Background(), testCamera("room1", "one"), "sub", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCameraBusy)
	_, err = s.CreateSession(context.Background(), testCamera("hall", "one"), "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCapacityExceeded)
	if err = s.CloseSession(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []cs.SessionID{alias.ID, other.ID} {
		state, statusErr := s.SessionStatus(context.Background(), id)
		if statusErr != nil || state.Resources != cs.PresencePresent {
			t.Fatal(state, statusErr)
		}
	}
	if err = s.CloseSession(context.Background(), alias.ID); err != nil {
		t.Fatal(err)
	}
	openViewer(t, s, "room1", "one", "sub")
	if err = s.CloseSession(context.Background(), alias.ID); err != nil {
		t.Fatal("non-idempotent close", err)
	}
	camera, stream, err := s.SessionBinding(other.ID)
	if err != nil || camera != "room2" || stream != "main" {
		t.Fatal(camera, stream, err)
	}
}

func TestServiceConcurrentAdmission(t *testing.T) {
	b := newMemoryBackend()
	s := testService(t, b, cs.ServiceOptions{MaxViewers: 8, PerSourceViewers: 4})
	var wg sync.WaitGroup
	results := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateSession(context.Background(), testCamera("alias", "one"), "main", cs.Offer{SDP: "offer"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			code(t, err, cs.CodeCapacityExceeded)
		}
	}
	if accepted != 4 {
		t.Fatalf("accepted %d, expected source cap 4", accepted)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sources) != 1 || len(b.sessions) != 4 {
		t.Fatal("duplicate upstream definitions or unreserved sessions")
	}
}

func TestServiceCanceledPartialCreateAndDeadline(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "canceled"}[explicit], func(t *testing.T) {
			b := newMemoryBackend()
			entered := make(chan struct{})
			b.create = func(ctx context.Context, _ cs.SessionSpec) error { close(entered); <-ctx.Done(); return ctx.Err() }
			s := testService(t, b, cs.ServiceOptions{NegotiationTimeout: 50 * time.Millisecond})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if explicit {
				go func() { <-entered; cancel() }()
			}
			state, err := s.CreateSession(ctx, testCamera("room", "one"), "main", cs.Offer{SDP: "offer"})
			want := context.DeadlineExceeded
			if explicit {
				want = context.Canceled
			}
			if !errors.Is(err, want) || state.ID == "" || state.Resources != cs.PresenceAbsent {
				t.Fatal(state, err)
			}
			b.mu.Lock()
			b.create = nil
			b.mu.Unlock()
			openViewer(t, s, "room", "one", "sub")
		})
	}
}

func TestServiceUnknownCleanupRetainsCapacity(t *testing.T) {
	b := newMemoryBackend()
	s := testService(t, b, cs.ServiceOptions{MaxViewers: 1})
	v := openViewer(t, s, "room", "one", "main")
	b.mu.Lock()
	b.closeFailure = true
	b.mu.Unlock()
	code(t, s.CloseSession(context.Background(), v.ID), cs.CodeCleanupIncomplete)
	_, err := s.CreateSession(context.Background(), testCamera("elsewhere", "two"), "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCapacityExceeded)
	_, err = s.CreateSession(context.Background(), testCamera("room", "one"), "sub", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCameraBusy)
	b.mu.Lock()
	b.closeFailure = false
	b.mu.Unlock()
	eventuallyService(t, func() bool {
		state, statusErr := s.SessionStatus(context.Background(), v.ID)
		return statusErr == nil && state.Resources == cs.PresenceAbsent
	})
	openViewer(t, s, "room", "one", "sub")
}

func TestServiceObservedLossNoViewingTTLAndStaleStatus(t *testing.T) {
	b := newMemoryBackend()
	s := testService(t, b, cs.ServiceOptions{MaxViewers: 1, NegotiationTimeout: 50 * time.Millisecond, ObservationMaxAge: 100 * time.Millisecond})
	v := openViewer(t, s, "room", "one", "main")
	time.Sleep(120 * time.Millisecond) // past negotiation timeout; active viewing must remain
	state, err := s.SessionStatus(context.Background(), v.ID)
	if err != nil || state.Resources != cs.PresencePresent || state.Answer != nil {
		t.Fatal(state, err)
	}
	b.mu.Lock()
	b.stale = true
	stored := b.sessions[v.ID]
	stored.Resources = cs.PresenceAbsent
	b.sessions[v.ID] = stored
	b.mu.Unlock()
	state, err = s.SessionStatus(context.Background(), v.ID)
	if err != nil || state.Resources != cs.PresenceUnknown {
		t.Fatal("stale absence released ownership", state, err)
	}
	_, err = s.CreateSession(context.Background(), testCamera("two", "two"), "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeCapacityExceeded)
	b.mu.Lock()
	b.stale = false
	b.mu.Unlock()
	// No client DELETE/status call is needed to reclaim either reservation.
	eventuallyService(t, func() bool {
		_, createErr := s.CreateSession(context.Background(), testCamera("room", "one"), "sub", cs.Offer{SDP: "offer"})
		return createErr == nil
	})
}

func TestServiceFaultLatchRequiresOperatorReset(t *testing.T) {
	b := newMemoryBackend()
	s := testService(t, b, cs.ServiceOptions{})
	v := openViewer(t, s, "room", "one", "main")
	other := openViewer(t, s, "room2", "two", "main")
	b.mu.Lock()
	b.faults[v.SourceKey] = cs.CodeSourceUnavailable
	b.mu.Unlock()
	_, err := s.CreateSession(context.Background(), testCamera("hall", "one"), "main", cs.Offer{SDP: "offer"})
	code(t, err, cs.CodeSourceUnavailable)
	if err = s.CloseSession(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err = s.CreateSession(context.Background(), testCamera("hall", "one"), "main", cs.Offer{SDP: "offer"})
		code(t, err, cs.CodeSourceUnavailable)
	}
	b.mu.Lock()
	creates, resets := b.creates, b.resets
	b.mu.Unlock()
	if creates != 2 || resets != 0 {
		t.Fatal("viewer retry reached media/reset", creates, resets)
	}
	if _, err = s.ResetSource(context.Background(), v.SourceKey); err != nil {
		t.Fatal(err)
	}
	openViewer(t, s, "hall", "one", "main")
	state, err := s.SessionStatus(context.Background(), other.ID)
	if err != nil || state.Resources != cs.PresencePresent {
		t.Fatal("reset affected another device", state, err)
	}
}

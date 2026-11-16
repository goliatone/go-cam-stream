package viewer_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	"github.com/goliatone/go-command"
)

type testBackend struct {
	mu                                       sync.Mutex
	sources                                  map[cs.SourceKey]bool
	sessions                                 map[cs.SessionID]cs.SessionState
	faults                                   map[cs.SourceKey]cs.ErrorCode
	statusFailure, resetFailure, blockCreate bool
	creates, resets                          int
}

func newBackend() *testBackend {
	return &testBackend{sources: map[cs.SourceKey]bool{}, sessions: map[cs.SessionID]cs.SessionState{}, faults: map[cs.SourceKey]cs.ErrorCode{}}
}
func (b *testBackend) state(key cs.SourceKey) cs.StreamState {
	n := 0
	for _, v := range b.sessions {
		if v.SourceKey == key && v.Resources == cs.PresencePresent {
			n++
		}
	}
	out := cs.StreamState{Key: key, State: cs.StateIdle, Upstream: cs.PresenceAbsent, ActiveSessions: &n, ObservedAt: time.Now(), Fault: b.faults[key]}
	if n > 0 {
		out.State, out.Upstream = cs.StatePlaying, cs.PresencePresent
	}
	return out
}
func (b *testBackend) EnsureStream(ctx context.Context, spec cs.StreamSpec) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sources[spec.Key] = true
	return b.state(spec.Key), nil
}
func (b *testBackend) StreamStatus(ctx context.Context, key cs.SourceKey) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.statusFailure {
		return cs.StreamState{}, cs.NewError(cs.CodeBackendUnavailable, errors.New("kasa://PRIVATE_PASSWORD@192.0.2.11"))
	}
	if !b.sources[key] {
		return cs.StreamState{}, cs.NewError(cs.CodeNotFound, nil)
	}
	return b.state(key), nil
}
func (b *testBackend) RemoveStream(ctx context.Context, key cs.SourceKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.faults[key] != "" || b.state(key).Upstream != cs.PresenceAbsent {
		return cs.NewError(cs.CodeConflict, nil)
	}
	delete(b.sources, key)
	return nil
}
func (b *testBackend) CreateSession(ctx context.Context, spec cs.SessionSpec) (cs.SessionState, error) {
	b.mu.Lock()
	b.creates++
	out := cs.SessionState{ID: spec.ID, SourceKey: spec.SourceKey, State: cs.StateConnecting, Resources: cs.PresencePresent, ObservedAt: time.Now()}
	b.sessions[spec.ID] = out
	block := b.blockCreate
	b.mu.Unlock()
	if block {
		<-ctx.Done()
		return out, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Answer = &cs.Answer{SDP: "v=0\r\n"}
	return out, nil
}
func (b *testBackend) SessionStatus(ctx context.Context, id cs.SessionID) (cs.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return cs.SessionState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.sessions[id]
	if !ok {
		return v, cs.NewError(cs.CodeNotFound, nil)
	}
	v.ObservedAt = time.Now()
	return v, nil
}
func (b *testBackend) CloseSession(ctx context.Context, id cs.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if v, ok := b.sessions[id]; ok {
		v.Resources, v.State = cs.PresenceAbsent, cs.StateClosed
		b.sessions[id] = v
	}
	return nil
}
func (b *testBackend) ResetSource(ctx context.Context, key cs.SourceKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resets++
	if b.resetFailure {
		return cs.NewError(cs.CodeCleanupIncomplete, errors.New("PRIVATE_PASSWORD"))
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
func fixture(t *testing.T) (*viewer.CameraService, *cs.Service, *testBackend, config.Catalog) {
	t.Helper()
	raw, err := config.Load(context.Background(), "../../config/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw.Cameras = raw.Cameras[:2]
	alias := raw.Cameras[0]
	alias.ID = "hall"
	alias.Name = "Room alias"
	raw.Cameras = append(raw.Cameras, alias)
	grant := func(id cs.CameraID, v, d, r bool) config.Grant {
		return config.Grant{CameraID: id, Streams: []cs.StreamID{"default"}, View: v, Diagnose: d, Reconnect: r}
	}
	raw.Access = []config.Access{
		{Login: "owner", Grants: []config.Grant{grant("room1", true, true, true), grant("hall", true, true, true), grant("room2", true, true, true)}},
		{Login: "alice", Grants: []config.Grant{grant("room1", true, false, false)}},
		{Login: "bob", Grants: []config.Grant{grant("room1", true, false, false)}},
		{Login: "partial", Grants: []config.Grant{grant("room1", false, true, true)}},
		{Login: "empty", Grants: []config.Grant{}},
	}
	catalog, err := config.Prepare(context.Background(), raw, config.EC70Provider())
	if err != nil {
		t.Fatal(err)
	}
	b := newBackend()
	core, err := cs.NewService(b, cs.ServiceOptions{PollInterval: 10 * time.Millisecond}, ec70.Strategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.mu.Lock()
		b.statusFailure, b.resetFailure, b.blockCreate = false, false, false
		b.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if closeErr := core.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	service, err := viewer.NewCameraService(core, catalog, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return service, core, b, catalog
}
func actor(login string) context.Context { return viewer.WithPrincipal(context.Background(), login) }
func assertCode(t *testing.T, err error, want cs.ErrorCode) {
	t.Helper()
	if cs.DescribeError(err).Code != want {
		t.Fatalf("got %v; want %s", err, want)
	}
}
func create(t *testing.T, s *viewer.CameraService, login string, camera cs.CameraID) api.Session {
	t.Helper()
	v, err := s.CreateSession(actor(login), camera, "default", api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPolicyDeniesUnknownScopeAndCrossPrincipalClose(t *testing.T) {
	s, _, _, catalog := fixture(t)
	for _, login := range []string{"", "unknown", "Owner"} {
		_, err := s.List(actor(login))
		assertCode(t, err, cs.CodeAccessDenied)
	}
	list, err := s.List(actor("alice"))
	if err != nil || len(list.Cameras) != 1 || list.Cameras[0].ID != "room1" {
		t.Fatal(list, err)
	}
	empty, err := s.List(actor("empty"))
	if err != nil || empty.Cameras == nil || len(empty.Cameras) != 0 {
		t.Fatal(empty, err)
	}
	_, err = s.Camera(actor("alice"), "room2")
	assertCode(t, err, cs.CodeAccessDenied)
	for _, login := range []string{"", "unknown"} {
		_, err = s.Camera(actor(login), "room1")
		assertCode(t, err, cs.CodeAccessDenied)
	}
	_, err = s.Camera(actor("owner"), "missing")
	assertCode(t, err, cs.CodeAccessDenied)
	canceled, stop := context.WithCancel(actor("alice"))
	stop()
	if _, err = s.Camera(canceled, "room1"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled detail", err)
	}
	if detail, detailErr := s.Camera(actor("alice"), "room1"); detailErr != nil || !reflect.DeepEqual(detail, list.Cameras[0]) {
		t.Fatal("detail differs from the list", detail, detailErr)
	}
	_, err = s.CreateSession(actor("alice"), "room2", "default", api.CreateSessionRequest{})
	assertCode(t, err, cs.CodeAccessDenied)
	_, err = s.Diagnose(actor("alice"), "room1", "")
	assertCode(t, err, cs.CodeAccessDenied)
	v := create(t, s, "alice", "room1")
	assertCode(t, s.CloseSession(actor("bob"), v.ID), cs.CodeAccessDenied)
	assertCode(t, s.CloseSession(actor("alice"), "unknown"), cs.CodeAccessDenied)
	if err = s.CloseSession(actor("owner"), v.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CloseSession(actor("alice"), v.ID); err != nil {
		t.Fatal("owned close not idempotent", err)
	}
	// Mutating caller-owned grants or summaries cannot alter the snapshot.
	catalog.Access[1].Grants[0].Reconnect = true
	list.Cameras[0].Streams[0].Name = "changed"
	_, err = s.Reconnect(actor("alice"), "room1", "default")
	assertCode(t, err, cs.CodeAccessDenied)
}

func TestReconnectRequiresAllAliasesAndConfirmsCleanup(t *testing.T) {
	s, core, b, _ := fixture(t)
	a := create(t, s, "alice", "room1")
	alias := create(t, s, "owner", "hall")
	other := create(t, s, "owner", "room2")
	_, err := s.Reconnect(actor("partial"), "room1", "default")
	assertCode(t, err, cs.CodeAccessDenied)
	b.mu.Lock()
	resets := b.resets
	b.resetFailure = true
	b.mu.Unlock()
	if resets != 0 {
		t.Fatal("denied reset invoked backend")
	}
	failed, err := s.Reconnect(actor("owner"), "room1", "default")
	assertCode(t, err, cs.CodeCleanupIncomplete)
	if len(failed.ClosedSessions) != 0 {
		t.Fatal("unconfirmed close reported success")
	}
	b.mu.Lock()
	b.resetFailure = false
	b.mu.Unlock()
	result, err := s.Reconnect(actor("owner"), "room1", "default")
	if err != nil || len(result.Affected) != 2 || len(result.ClosedSessions) != 2 {
		t.Fatal(result, err)
	}
	for _, id := range []cs.SessionID{a.ID, alias.ID} {
		state, statusErr := core.SessionStatus(context.Background(), id)
		if statusErr != nil || state.Resources != cs.PresenceAbsent {
			t.Fatal(state, statusErr)
		}
	}
	state, err := core.SessionStatus(context.Background(), other.ID)
	if err != nil || state.Resources != cs.PresencePresent {
		t.Fatal("another camera interrupted", state, err)
	}
}

func TestDiagnoseIsObservationAndSanitized(t *testing.T) {
	s, _, b, _ := fixture(t)
	idle, err := s.Diagnose(actor("partial"), "room1", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range idle.Streams[0].Checks {
		if check.State != api.CheckUnknown {
			t.Fatal("idle camera declared healthy", check)
		}
	}
	b.mu.Lock()
	calls := b.creates
	b.mu.Unlock()
	if calls != 0 {
		t.Fatal("diagnose acquired media")
	}
	create(t, s, "alice", "room1")
	healthy, err := s.Diagnose(actor("owner"), "room1", "default")
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range healthy.Streams[0].Checks {
		if check.State != api.CheckPassed {
			t.Fatal("current media observation not reflected", check)
		}
	}
	b.mu.Lock()
	b.statusFailure = true
	b.mu.Unlock()
	result, err := s.Diagnose(actor("owner"), "room1", "default")
	if err != nil {
		t.Fatal(err)
	}
	assertCode(t, admin.CommandResultFailure(result), cs.CodeBackendUnavailable)
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PRIVATE_PASSWORD", "192.0.2.11", "env://", "kasa://"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("diagnostic leaked privileged data")
		}
	}
}

func TestOwnedNamedDispatchCarriesTypedFailureAndClosesRegistration(t *testing.T) {
	s, _, b, _ := fixture(t)
	create(t, s, "alice", "room1")
	bus := admin.NewCommandBus(true)
	handle, err := viewer.Register(bus, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := handle.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		bus.Close()
	})
	payload := map[string]any{"camera_id": "room1", "stream_id": "default"}
	opts := command.DispatchOptions{Mode: command.ExecutionModeInline}
	_, err = bus.DispatchByNameWithOutcome(context.Background(), api.DiagnoseCommand, payload, nil, opts)
	if err == nil {
		t.Fatal("named dispatch bypassed principal")
	}
	for _, bad := range []map[string]any{{"camera_id": "room1", "actor": "owner"}, {"camera_id": 42}, {"camera_id": "room1", "stream_id": true}} {
		if _, err = bus.DispatchByNameWithOutcome(actor("owner"), api.DiagnoseCommand, bad, nil, opts); err == nil {
			t.Fatal("invalid/spoofed command accepted")
		}
	}
	b.mu.Lock()
	b.statusFailure = true
	b.mu.Unlock()
	out, err := bus.DispatchByNameWithOutcome(actor("owner"), api.DiagnoseCommand, payload, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := out.Result.(viewer.DiagnoseResult)
	if !ok || len(result.Streams) != 1 {
		t.Fatal("missing typed outcome")
	}
	assertCode(t, admin.CommandResultFailure(result), cs.CodeBackendUnavailable)
	b.mu.Lock()
	b.statusFailure = false
	b.mu.Unlock()
	out, err = bus.DispatchByNameWithOutcome(actor("owner"), api.ReconnectCommand, payload, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	reconnected, ok := out.Result.(api.ReconnectResponse)
	if !ok || len(reconnected.Affected) != 2 || len(reconnected.ClosedSessions) != 1 {
		t.Fatal("missing typed reconnect result")
	}
	if err = handle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = bus.DispatchByNameWithOutcome(actor("owner"), api.DiagnoseCommand, payload, nil, opts); err == nil {
		t.Fatal("closed registration still resolves")
	}
}

func TestConcurrentAuthorizedCreateAndCanceledNegotiation(t *testing.T) {
	s, core, b, _ := fixture(t)
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, err := s.CreateSession(actor("alice"), "room1", "default", api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	b.mu.Lock()
	b.blockCreate = true
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(actor("owner"), 30*time.Millisecond)
	defer cancel()
	v, err := s.CreateSession(ctx, "room2", "default", api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
	assertCode(t, err, cs.CodeDeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lost standard cancellation cause", err)
	}
	state, err := core.SessionStatus(context.Background(), v.ID)
	if err != nil || state.Resources != cs.PresenceAbsent {
		t.Fatal("canceled create retained resources", state, err)
	}
	if err = s.CloseSession(actor("owner"), v.ID); err != nil {
		t.Fatal("failed creation lost principal binding", err)
	}
}

func TestListReportsObservedAvailabilityWithoutWaiting(t *testing.T) {
	s, _, b, catalog := fixture(t)
	availability := func(login string) map[cs.CameraID]cs.State {
		t.Helper()
		list, err := s.List(actor(login))
		if err != nil {
			t.Fatal(err)
		}
		out := map[cs.CameraID]cs.State{}
		for _, camera := range list.Cameras {
			out[camera.ID] = camera.Availability
		}
		return out
	}
	until := func(check func() bool) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if check() {
				return
			}
		}
		t.Fatal("condition did not become true")
	}
	source := func(id cs.CameraID) cs.SourceKey {
		for _, camera := range catalog.Cameras {
			if camera.Camera.ID == id {
				return camera.Sources["default"]
			}
		}
		t.Fatal("no camera", id)
		return ""
	}
	if got := availability("owner"); len(got) != 3 || got["room1"] != cs.StateIdle || got["hall"] != cs.StateIdle || got["room2"] != cs.StateIdle {
		t.Fatal("undemanded cameras", got)
	}
	create(t, s, "alice", "room1")
	// Aliases of one source report the same observation.
	until(func() bool {
		got := availability("owner")
		return got["room1"] == cs.StatePlaying && got["hall"] == cs.StatePlaying && got["room2"] == cs.StateIdle
	})

	// A latched fault outlasts its sessions and is listed as failed, only to
	// principals who may view the camera.
	other := create(t, s, "owner", "room2")
	b.mu.Lock()
	b.faults[source("room2")] = cs.CodeCameraUnreachable
	b.mu.Unlock()
	until(func() bool { return availability("owner")["room2"] == cs.StateFailed })
	if err := s.CloseSession(actor("owner"), other.ID); err != nil {
		t.Fatal(err)
	}
	if got := availability("owner"); got["room2"] != cs.StateFailed || got["room1"] != cs.StatePlaying {
		t.Fatal("after close", got)
	}
	if got := availability("alice"); len(got) != 1 || got["room1"] != cs.StatePlaying {
		t.Fatal("alice", got)
	}
	// A camera's detail summarizes it as the list does.
	list, err := s.List(actor("owner"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Cameras {
		if detail, detailErr := s.Camera(actor("owner"), item.ID); detailErr != nil || !reflect.DeepEqual(detail, item) {
			t.Fatal("detail differs from the list", item.ID, detail, detailErr)
		}
	}

	// A source busy with a create does not hold up the list.
	b.mu.Lock()
	b.blockCreate = true
	before := b.creates
	b.mu.Unlock()
	ctx, cancel := context.WithCancel(actor("bob"))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateSession(ctx, "room1", "default", api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}})
		done <- err
	}()
	until(func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.creates > before
	})
	got := availability("owner")
	select {
	case err := <-done:
		t.Fatal("the create ended before the list was read", err)
	default:
	}
	if got["room1"] != cs.StatePlaying {
		t.Fatal("busy source", got)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled create succeeded")
	}
}

func TestListSummarizesOnlyViewableProfiles(t *testing.T) {
	raw, err := config.Load(context.Background(), "../../config/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := config.Prepare(context.Background(), raw, config.DefaultProviders()...)
	if err != nil {
		t.Fatal(err)
	}
	b := newBackend()
	core, err := cs.NewService(b, cs.ServiceOptions{PollInterval: 10 * time.Millisecond}, ec70.Strategy{}, rtsp.Strategy{})
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.mu.Lock()
		clear(b.faults)
		b.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if closeErr := core.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	s, err := viewer.NewCameraService(core, catalog, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	const operator, guest = "operator@example.test", "viewer@example.test"
	driveway := func(login string) cs.State {
		t.Helper()
		list, listErr := s.List(actor(login))
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, camera := range list.Cameras {
			if camera.ID == "driveway" {
				return camera.Availability
			}
		}
		t.Fatal("driveway not listed for", login)
		return ""
	}
	until := func(check func() bool) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if check() {
				return
			}
		}
		t.Fatal("condition did not become true")
	}
	var main cs.SourceKey
	for _, camera := range catalog.Cameras {
		if camera.Camera.ID == "driveway" {
			main = camera.Sources["main"]
		}
	}
	offer := api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}}
	v, err := s.CreateSession(actor(operator), "driveway", "main", offer)
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.faults[main] = cs.CodeAuthenticationFailed
	b.mu.Unlock()
	until(func() bool { return driveway(operator) == cs.StateFailed })
	// The guest may view only the substream, so the main profile's fault is not
	// theirs to see.
	if got := driveway(guest); got != cs.StateIdle {
		t.Fatal("guest saw another profile's state", got)
	}
	if err = s.CloseSession(actor(operator), v.ID); err != nil {
		t.Fatal(err)
	}
	// Video playing on one profile outranks a latched fault on another.
	until(func() bool {
		_, createErr := s.CreateSession(actor(guest), "driveway", "sub", offer)
		return createErr == nil
	})
	until(func() bool { return driveway(operator) == cs.StatePlaying && driveway(guest) == cs.StatePlaying })
}

// Command embedded demonstrates external typed sources and a neutral backend.
// It is an in-memory lifecycle example, not a player or application host.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type config string

func (config) StrategyID() cs.StrategyID { return "embedded" }

type source string

func (source) Kind() cs.SourceKind { return "example.test/embedded" }

type strategy struct{}

func (strategy) ID() cs.StrategyID { return "embedded" }
func (strategy) Streams(ctx context.Context, camera cs.Camera) ([]cs.Stream, error) {
	_, err := (strategy{}).Resolve(ctx, camera, "main")
	return []cs.Stream{{ID: "main", Capabilities: cs.MediaCapabilities{Video: true}}}, err
}
func (strategy) Resolve(ctx context.Context, camera cs.Camera, stream cs.StreamID) (cs.StreamSpec, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamSpec{}, err
	}
	cfg, ok := camera.Config.(config)
	if !ok || camera.PhysicalID == "" {
		return cs.StreamSpec{}, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	if stream != "main" {
		return cs.StreamSpec{}, cs.NewError(cs.CodeUnsupported, nil)
	}
	return cs.StreamSpec{Key: cs.SourceKey(camera.PhysicalID), PhysicalCameraID: camera.PhysicalID, Source: source(cfg), Capabilities: cs.MediaCapabilities{Video: true}, Constraints: cs.DeviceConstraints{MaxActiveSources: 1}}, nil
}

// This backend consumes the typed source itself. An encoder is optional and only
// needed when pairing a source with a backend-specific input (e.g. go2rtc/kasa).
type backend struct {
	mu       sync.Mutex
	sources  map[cs.SourceKey]source
	sessions map[cs.SessionID]cs.SessionState
}

func (b *backend) EnsureStream(ctx context.Context, spec cs.StreamSpec) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	input, ok := spec.Source.(source)
	if !ok {
		return cs.StreamState{}, cs.NewError(cs.CodeUnsupported, nil)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, exists := b.sources[spec.Key]; exists && old != input {
		return cs.StreamState{}, cs.NewError(cs.CodeConflict, nil)
	}
	b.sources[spec.Key] = input
	return b.status(spec.Key), nil
}
func (b *backend) status(key cs.SourceKey) cs.StreamState {
	n := 0
	for _, v := range b.sessions {
		if v.SourceKey == key && v.Resources == cs.PresencePresent {
			n++
		}
	}
	presence := cs.PresenceAbsent
	if n > 0 {
		presence = cs.PresencePresent
	}
	return cs.StreamState{Key: key, State: cs.StateIdle, Upstream: presence, ActiveSessions: &n, ObservedAt: time.Now()}
}
func (b *backend) StreamStatus(ctx context.Context, key cs.SourceKey) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.sources[key]; !ok {
		return cs.StreamState{}, cs.NewError(cs.CodeNotFound, nil)
	}
	return b.status(key), nil
}
func (b *backend) RemoveStream(ctx context.Context, key cs.SourceKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.status(key).Upstream != cs.PresenceAbsent {
		return cs.NewError(cs.CodeConflict, nil)
	}
	delete(b.sources, key)
	return nil
}
func (b *backend) CreateSession(ctx context.Context, spec cs.SessionSpec) (cs.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return cs.SessionState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.sessions[spec.ID]; ok {
		return cs.SessionState{}, cs.NewError(cs.CodeConflict, nil)
	}
	if _, ok := b.sources[spec.SourceKey]; !ok {
		return cs.SessionState{}, cs.NewError(cs.CodeNotFound, nil)
	}
	state := cs.SessionState{ID: spec.ID, SourceKey: spec.SourceKey, State: cs.StateConnecting, Resources: cs.PresencePresent, ObservedAt: time.Now()}
	b.sessions[spec.ID] = state
	state.Answer = &cs.Answer{SDP: "synthetic demonstration only"}
	return state, nil
}
func (b *backend) SessionStatus(ctx context.Context, id cs.SessionID) (cs.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return cs.SessionState{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.sessions[id]
	if !ok {
		return cs.SessionState{}, cs.NewError(cs.CodeNotFound, nil)
	}
	state.ObservedAt = time.Now()
	return state, nil
}
func (b *backend) CloseSession(ctx context.Context, id cs.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if state, ok := b.sessions[id]; ok {
		state.Resources, state.State = cs.PresenceAbsent, cs.StateClosed
		b.sessions[id] = state
	}
	return nil
}

func run() (err error) {
	b := &backend{sources: map[cs.SourceKey]source{}, sessions: map[cs.SessionID]cs.SessionState{}}
	service, err := cs.NewService(b, cs.ServiceOptions{MaxViewers: 2}, strategy{})
	if err != nil {
		return err
	}
	// A managed backend (such as go2rtc) must be explicitly started first, using
	// a verified deployment boundary; close it after draining this service.
	if err = service.Start(context.Background()); err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if closeErr := service.Close(ctx); closeErr != nil {
			err = closeErr
		}
	}()
	camera := cs.Camera{ID: "room1", PhysicalID: "device-one", Config: config("private-demo-input")}
	first, err := service.CreateSession(context.Background(), camera, "main", cs.Offer{SDP: "synthetic offer"})
	if err != nil {
		return err
	}
	camera.ID = "hall"
	second, err := service.CreateSession(context.Background(), camera, "main", cs.Offer{SDP: "synthetic offer"})
	if err != nil {
		return err
	}
	if err = service.CloseSession(context.Background(), first.ID); err != nil {
		return err
	}
	state, err := service.SessionStatus(context.Background(), second.ID)
	if err != nil {
		return err
	}
	fmt.Println("Aliases share source:", first.SourceKey == second.SourceKey)
	fmt.Println("Closing one preserves the other:", state.Resources == cs.PresencePresent)
	return nil
}
func main() {
	if err := run(); err != nil {
		log.Fatal(cs.DescribeError(err).Message)
	}
}

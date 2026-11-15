// Built and run from a separate temporary module through a file module proxy.
// This is a fake backend/provider for contract consumption, not application code.
package main

import (
	"context"
	"errors"
	"fmt"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type providerConfig struct{ Binding string }

func (providerConfig) StrategyID() cs.StrategyID { return "example.test/camera" }

type providerSource struct{ Binding string }

func (providerSource) Kind() cs.SourceKind { return "example.test/source" }

type encodedSource struct{ Binding string }
type encoder struct{}

func (encoder) Kind() cs.SourceKind { return "example.test/source" }
func (encoder) Encode(ctx context.Context, s cs.Source) (encodedSource, error) {
	if err := ctx.Err(); err != nil {
		return encodedSource{}, err
	}
	value, ok := s.(providerSource)
	if !ok {
		return encodedSource{}, cs.NewError(cs.CodeUnsupported, nil)
	}
	return encodedSource{Binding: value.Binding}, nil
}

type provider struct{}

func (provider) ID() cs.StrategyID { return "example.test/camera" }
func (provider) Streams(ctx context.Context, c cs.Camera) ([]cs.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := c.Config.(providerConfig); !ok {
		return nil, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return []cs.Stream{{ID: "default", Name: "Default", Capabilities: cs.MediaCapabilities{Video: true}}}, nil
}
func (provider) Resolve(ctx context.Context, c cs.Camera, id cs.StreamID) (cs.StreamSpec, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamSpec{}, err
	}
	cfg, ok := c.Config.(providerConfig)
	if !ok || id != "default" {
		return cs.StreamSpec{}, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return cs.StreamSpec{Key: cs.SourceKey(string(c.PhysicalID) + "/default/" + cfg.Binding), PhysicalCameraID: c.PhysicalID, Source: providerSource{Binding: cfg.Binding}, Capabilities: cs.MediaCapabilities{Video: true}}, nil
}

type fakeBackend struct {
	spec    cs.StreamSpec
	session cs.SessionState
}

func (b *fakeBackend) EnsureStream(ctx context.Context, s cs.StreamSpec) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	b.spec = s
	return b.StreamStatus(ctx, s.Key)
}
func (b *fakeBackend) StreamStatus(ctx context.Context, key cs.SourceKey) (cs.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return cs.StreamState{}, err
	}
	n := 0
	return cs.StreamState{Key: key, State: cs.StateIdle, Upstream: cs.PresenceAbsent, ActiveSessions: &n}, nil
}
func (b *fakeBackend) RemoveStream(ctx context.Context, _ cs.SourceKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.spec = cs.StreamSpec{}
	return nil
}
func (b *fakeBackend) CreateSession(ctx context.Context, s cs.SessionSpec) (cs.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return cs.SessionState{}, err
	}
	b.session = cs.SessionState{ID: s.ID, SourceKey: s.SourceKey, State: cs.StateConnecting, Resources: cs.PresencePresent, Answer: &cs.Answer{SDP: "v=0\r\n"}}
	return b.session, nil
}
func (b *fakeBackend) SessionStatus(ctx context.Context, _ cs.SessionID) (cs.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return cs.SessionState{}, err
	}
	return b.session, nil
}
func (b *fakeBackend) CloseSession(ctx context.Context, _ cs.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.session.State = cs.StateClosed
	b.session.Resources = cs.PresenceAbsent
	b.session.Answer = nil
	return nil
}

var _ cs.CameraConfig = providerConfig{}
var _ cs.Source = providerSource{}
var _ cs.Strategy = provider{}
var _ cs.SourceEncoder[encodedSource] = encoder{}
var _ cs.Backend = (*fakeBackend)(nil)

func main() {
	ctx := context.Background()
	camera := cs.Camera{ID: "room1", PhysicalID: "device1", Config: providerConfig{Binding: "credential-reference-1"}}
	var strategy cs.Strategy = provider{}
	streams, err := strategy.Streams(ctx, camera)
	check(err == nil && len(streams) == 1, "external typed config")
	spec, err := strategy.Resolve(ctx, camera, streams[0].ID)
	check(err == nil, "external resolution")
	encoded, err := encoder{}.Encode(ctx, spec.Source)
	check(err == nil && encoded.Binding == "credential-reference-1", "external typed encoder")
	_, err = encoder{}.Encode(ctx, wrongSource{})
	check(cs.DescribeError(err).Code == cs.CodeUnsupported, "concrete type check")
	var backend cs.Backend = &fakeBackend{}
	state, err := backend.EnsureStream(ctx, spec)
	check(err == nil && state.Upstream == cs.PresenceAbsent, "alternate backend")
	session, err := backend.CreateSession(ctx, cs.SessionSpec{ID: "unique-attempt-1", SourceKey: spec.Key, Offer: cs.Offer{SDP: "v=0\r\n"}})
	check(err == nil && session.Answer != nil, "backend-neutral SDP")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = backend.SessionStatus(canceled, session.ID)
	check(errors.Is(err, context.Canceled), "context propagation")
	check(backend.CloseSession(ctx, session.ID) == nil, "independent cleanup context")
	session, err = backend.SessionStatus(ctx, session.ID)
	check(err == nil && session.Resources == cs.PresenceAbsent, "confirmed release")
	check(backend.RemoveStream(ctx, spec.Key) == nil, "source removal")
	fmt.Println("external contracts: typed provider/config/encoder, alternate backend and cancellation compile and run without host imports")
}

type wrongSource struct{}

func (wrongSource) Kind() cs.SourceKind { return "example.test/source" }
func check(ok bool, message string) {
	if !ok {
		panic(message)
	}
}

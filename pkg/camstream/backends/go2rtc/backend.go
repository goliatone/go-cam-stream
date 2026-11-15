package go2rtc

import (
	"context"
	"crypto/sha256"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

type Backend struct {
	cfg             Config
	mu              sync.Mutex
	closeMu         sync.Mutex
	sources         map[camstream.SourceKey]*source
	sessions        map[camstream.SessionID]*session
	encoders        map[camstream.SourceKind]camstream.SourceEncoder[Input]
	ports           map[int]bool
	running, used   bool
	active          int
	runtime         context.Context
	cancel          context.CancelFunc
	dir, executable string
	lock            *os.File
	client          *http.Client
	monitorDone     chan struct{}
}

type source struct {
	kind         camstream.SourceKind
	gate         chan struct{}
	spec         camstream.StreamSpec
	input        Input
	fingerprint  [32]byte
	ingest       *worker
	sessions     map[camstream.SessionID]*session
	fault        camstream.ErrorCode
	lastProgress time.Time
	received     uint64
	observed     time.Time
	unknown      bool
	removed      bool
}

type session struct {
	aborted      bool // protected by Backend.mu; canceled before source-gate acquisition
	offerHash    [32]byte
	source       *source
	spec         camstream.SessionSpec
	worker       *worker
	state        camstream.SessionState
	answer       *camstream.Answer
	operation    context.Context
	cancel       context.CancelFunc // immutable once registered; safe without source gate
	lastProgress time.Time
	sent         uint64
}

var _ camstream.Backend = (*Backend)(nil)
var _ camstream.Runtime = (*Backend)(nil)
var _ camstream.SourceResetter = (*Backend)(nil)

func New(cfg Config, encoders ...camstream.SourceEncoder[Input]) (*Backend, error) {
	cfg.defaults()
	if !cfg.valid() {
		return nil, failure(camstream.CodeInvalidArgument, nil)
	}
	b := &Backend{cfg: cfg, sources: make(map[camstream.SourceKey]*source), sessions: make(map[camstream.SessionID]*session), ports: make(map[int]bool), encoders: make(map[camstream.SourceKind]camstream.SourceEncoder[Input])}
	for _, e := range encoders {
		if e == nil || e.Kind() == "" || b.encoders[e.Kind()] != nil {
			return nil, failure(camstream.CodeInvalidArgument, nil)
		}
		b.encoders[e.Kind()] = e
	}
	b.client = &http.Client{Timeout: cfg.StartupTimeout, Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 2}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return b, nil
}

func enter(ctx context.Context, s *source) error {
	select {
	case s.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func leave(s *source) { <-s.gate }

func (b *Backend) EnsureStream(ctx context.Context, spec camstream.StreamSpec) (camstream.StreamState, error) {
	if err := ctx.Err(); err != nil {
		return camstream.StreamState{}, err
	}
	input, err := b.encode(ctx, spec)
	if err != nil {
		return camstream.StreamState{}, err
	}
	kind := spec.Source.Kind()
	spec.Source = nil // retain immutable encoded input, never a caller-owned mutable object
	fingerprint := sha256.Sum256([]byte(input.URL))
	b.mu.Lock()
	s := b.sources[spec.Key]
	if s == nil && len(b.sources) < b.cfg.MaxSources {
		s = &source{kind: kind, gate: make(chan struct{}, 1), spec: spec, input: input, fingerprint: fingerprint, sessions: make(map[camstream.SessionID]*session)}
		b.sources[spec.Key] = s
	}
	b.mu.Unlock()
	if s == nil {
		return camstream.StreamState{}, failure(camstream.CodeCapacityExceeded, nil)
	}
	if err = enter(ctx, s); err != nil {
		return camstream.StreamState{}, err
	}
	defer leave(s)
	if !s.matches(spec, kind, fingerprint) {
		return camstream.StreamState{}, failure(camstream.CodeConflict, nil)
	}
	return s.snapshot(), nil
}

func (b *Backend) getSource(key camstream.SourceKey) (*source, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sources[key]
	if s == nil {
		return nil, failure(camstream.CodeNotFound, nil)
	}
	return s, nil
}
func (b *Backend) StreamStatus(ctx context.Context, key camstream.SourceKey) (camstream.StreamState, error) {
	s, err := b.getSource(key)
	if err != nil {
		return camstream.StreamState{}, err
	}
	if err = enter(ctx, s); err != nil {
		return camstream.StreamState{}, err
	}
	defer leave(s)
	return s.snapshot(), nil
}
func (s *source) snapshot() camstream.StreamState {
	n := 0
	unknownSessions := false
	for _, v := range s.sessions {
		if v.state.Resources == camstream.PresenceUnknown {
			unknownSessions = true
		}
		if v.state.Resources != camstream.PresenceAbsent {
			n++
		}
	}
	out := camstream.StreamState{Key: s.spec.Key, State: camstream.StateIdle, Upstream: camstream.PresenceAbsent, ActiveSessions: &n, ObservedAt: s.observed, Fault: s.fault}
	if s.ingest != nil {
		out.Upstream = camstream.PresencePresent
		out.State = camstream.StateConnecting
		if s.received > 0 {
			out.State = camstream.StatePlaying
		}
	}
	if unknownSessions {
		out.ActiveSessions = nil
		out.State = camstream.StateUnknown
	}
	if s.unknown {
		out.Upstream = camstream.PresenceUnknown
		out.ActiveSessions = nil
		out.State = camstream.StateUnknown
	}
	if s.fault != "" {
		out.State = camstream.StateFailed
	}
	return out
}

func (b *Backend) RemoveStream(ctx context.Context, key camstream.SourceKey) error {
	s, err := b.getSource(key)
	if err != nil {
		if camstream.DescribeError(err).Code == camstream.CodeNotFound {
			return nil
		}
		return err
	}
	if err = enter(ctx, s); err != nil {
		return err
	}
	defer leave(s)
	if s.fault != "" || s.ingest != nil || len(s.sessions) != 0 {
		return failure(camstream.CodeConflict, nil)
	}
	s.removed = true
	b.mu.Lock()
	delete(b.sources, key)
	b.mu.Unlock()
	return nil
}

func (b *Backend) ResetSource(ctx context.Context, key camstream.SourceKey) error {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.CleanupTimeout)
	defer cancel()
	s, err := b.getSource(key)
	if err != nil {
		return err
	}
	b.cancelSource(s)
	if err = enter(ctx, s); err != nil {
		return err
	}
	defer leave(s)
	if err = b.cleanSource(ctx, s); err != nil {
		return err
	}
	s.fault = ""
	s.unknown = false
	s.received = 0
	s.observed = time.Now()
	return nil
}
func (b *Backend) cancelSource(s *source) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, v := range b.sessions {
		if v.source == s {
			v.cancel()
		}
	}
}

func (b *Backend) encode(ctx context.Context, spec camstream.StreamSpec) (Input, error) {
	if spec.Key == "" || spec.PhysicalCameraID == "" || spec.Source == nil || spec.Constraints.MaxActiveSources < 0 {
		return Input{}, failure(camstream.CodeInvalidArgument, nil)
	}
	enc := b.encoders[spec.Source.Kind()]
	if enc == nil {
		return Input{}, failure(camstream.CodeUnsupported, nil)
	}
	in, err := enc.Encode(ctx, spec.Source)
	if err != nil {
		return Input{}, failure(camstream.CodeInvalidArgument, err)
	}
	if !b.cfg.validInput(in) {
		return Input{}, failure(camstream.CodeInvalidArgument, nil)
	}
	return in, nil
}
func (s *source) matches(spec camstream.StreamSpec, kind camstream.SourceKind, digest [32]byte) bool {
	return !s.removed && s.fingerprint == digest && s.spec.PhysicalCameraID == spec.PhysicalCameraID && s.spec.Capabilities == spec.Capabilities && s.spec.Constraints == spec.Constraints && s.kind == kind
}

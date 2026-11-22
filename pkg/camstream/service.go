package camstream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// ServiceOptions are generic admission/observation limits, not media retry
// policy. PerSourceViewers counts all aliases together. Zero selects defaults.
type ServiceOptions struct {
	MaxViewers, PerSourceViewers, MaxSources, MaxSessionRecords         int
	NegotiationTimeout, CleanupTimeout, PollInterval, ObservationMaxAge time.Duration
}

func (o *ServiceOptions) defaults() {
	if o.MaxViewers == 0 {
		o.MaxViewers = 32
	}
	if o.PerSourceViewers == 0 {
		o.PerSourceViewers = 8
	}
	if o.MaxSources == 0 {
		o.MaxSources = 16
	}
	if o.MaxSessionRecords == 0 {
		o.MaxSessionRecords = 10000
	}
	if o.NegotiationTimeout == 0 {
		o.NegotiationTimeout = 10 * time.Second
	}
	if o.CleanupTimeout == 0 {
		o.CleanupTimeout = 5 * time.Second
	}
	if o.PollInterval == 0 {
		o.PollInterval = 250 * time.Millisecond
	}
	if o.ObservationMaxAge == 0 {
		o.ObservationMaxAge = 5 * time.Second
	}
}

// Service owns admission and observations for one exclusively injected backend.
// NewService performs no I/O. The host starts any backend Runtime first, then
// Start; it closes the service before closing that backend Runtime. Do not call
// backend mutation methods outside this service while it owns admission.
type Service struct {
	backend           Backend
	options           ServiceOptions
	strategies        map[StrategyID]Strategy
	mu                sync.Mutex // metadata/budgets only; never held during provider/backend calls
	sources           map[SourceKey]*serviceSource
	devices           map[PhysicalCameraID]SourceKey
	sessions          map[SessionID]*serviceSession
	active            int
	started, stopping bool
	runtime           context.Context
	cancel            context.CancelFunc
	done              chan struct{}
}

type serviceSource struct {
	transient                       CameraID
	retired                         atomic.Bool
	parked                          bool           // verified idle definition retained for credential testing
	slotLender                      *serviceSource // parked definition whose slot this probe uses
	gate                            chan struct{}  // serializes backend operations for this source only
	key                             SourceKey
	device                          PhysicalCameraID
	kind                            SourceKind
	capabilities                    MediaCapabilities
	constraints                     DeviceConstraints
	live                            map[SessionID]*serviceSession
	fault                           ErrorCode
	state                           StreamState
	unknown, resetting, resetFailed bool
}

type serviceSession struct {
	source                     *serviceSource
	camera                     CameraID
	stream                     StreamID
	state                      SessionState
	reserved, pending, closing bool
	cancel                     context.CancelFunc
}

func NewService(backend Backend, options ServiceOptions, strategies ...Strategy) (*Service, error) {
	options.defaults()
	if backend == nil || options.MaxViewers < 1 || options.PerSourceViewers < 1 || options.MaxSources < 1 || options.MaxSessionRecords < options.MaxViewers || options.NegotiationTimeout <= 0 || options.CleanupTimeout <= 0 || options.PollInterval <= 0 || options.ObservationMaxAge < options.PollInterval {
		return nil, NewError(CodeInvalidArgument, nil)
	}
	s := &Service{backend: backend, options: options, strategies: make(map[StrategyID]Strategy), sources: make(map[SourceKey]*serviceSource), devices: make(map[PhysicalCameraID]SourceKey), sessions: make(map[SessionID]*serviceSession)}
	for _, strategy := range strategies {
		if strategy == nil || strategy.ID() == "" || s.strategies[strategy.ID()] != nil {
			return nil, NewError(CodeInvalidArgument, nil)
		}
		s.strategies[strategy.ID()] = strategy
	}
	return s, nil
}

// Start begins observation only; backend Runtime ownership stays with the host.
// A Service is single-use. Cancellation stops admission and initiates bounded
// cleanup; Close must still be called to establish its outcome.
func (s *Service) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopping {
		return NewError(CodeConflict, nil)
	}
	s.runtime, s.cancel = context.WithCancel(ctx)
	s.started, s.done = true, make(chan struct{})
	go s.monitorService()
	return nil
}

// Streams performs strategy validation only. Camera config belongs to the caller
// and must remain immutable during a call. It performs no backend I/O.
func (s *Service) Streams(ctx context.Context, camera Camera) ([]Stream, error) {
	strategy, err := s.strategy(camera)
	if err != nil {
		return nil, err
	}
	streams, err := strategy.Streams(ctx, camera)
	return streams, serviceError(err)
}

func (s *Service) strategy(camera Camera) (Strategy, error) {
	if camera.ID == "" || camera.PhysicalID == "" || camera.Config == nil {
		return nil, NewError(CodeInvalidArgument, nil)
	}
	strategy := s.strategies[camera.Config.StrategyID()]
	if strategy == nil {
		return nil, NewError(CodeUnsupported, nil)
	}
	return strategy, nil
}

// CreateSession allocates a unique ID before backend I/O. Failure may return that
// ID with retained ownership; callers must not turn an uncertain POST into an
// automatic retry. The deadline applies to negotiation, never active playback.
func (s *Service) CreateSession(ctx context.Context, camera Camera, stream StreamID, offer Offer) (SessionState, error) {
	return s.createSession(ctx, camera, stream, offer, false)
}

// CreateTransientSession marks a private source for explicit management retirement.
// It cannot share a source with normal playback or another transient camera.
func (s *Service) CreateTransientSession(ctx context.Context, camera Camera, stream StreamID, offer Offer) (SessionState, error) {
	return s.createSession(ctx, camera, stream, offer, true)
}
func (s *Service) createSession(ctx context.Context, camera Camera, stream StreamID, offer Offer, transient bool) (SessionState, error) {
	ctx, cancel := context.WithTimeout(ctx, s.options.NegotiationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return SessionState{}, err
	}
	spec, err := s.resolve(ctx, camera, stream)
	if err != nil {
		return SessionState{}, err
	}
	if offer.SDP == "" || len(offer.SDP) > 64<<10 {
		return SessionState{}, NewError(CodeInvalidArgument, nil)
	}
	v, err := s.reserve(spec, camera.ID, stream, cancel, transient)
	if err != nil {
		return SessionState{}, err
	}
	stop := context.AfterFunc(s.runtime, cancel)
	defer stop()
	if err = serviceEnter(ctx, v.source); err != nil {
		// No backend call was made for this ID. Release only its viewer slot;
		// another operation still owns the source gate and device observation.
		s.mu.Lock()
		s.releaseViewer(v, StateClosed, "")
		s.mu.Unlock()
		return s.sessionSnapshot(v), err
	}
	defer serviceLeave(v.source)
	result, err := s.create(ctx, v, spec, offer)
	if err != nil {
		cleanup, done := context.WithTimeout(context.Background(), s.options.CleanupTimeout)
		defer done()
		if closeErr := s.closeViewer(cleanup, v); closeErr != nil {
			return s.sessionSnapshot(v), closeErr
		}
		return s.sessionSnapshot(v), serviceError(err)
	}
	return result, nil
}

func (s *Service) reserve(spec StreamSpec, camera CameraID, stream StreamID, cancel context.CancelFunc, transient bool) (*serviceSession, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, NewError(CodeInternal, err)
	}
	id := SessionID(hex.EncodeToString(random[:]))
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopping || s.runtime.Err() != nil {
		return nil, NewError(CodeBackendUnavailable, nil)
	}
	if key := s.devices[spec.PhysicalCameraID]; key != "" && key != spec.Key {
		return nil, NewError(CodeCameraBusy, nil)
	}
	existing := s.sources[spec.Key]
	if existing != nil && ((transient && existing.transient != camera) || (!transient && existing.transient != "")) {
		return nil, NewError(CodeConflict, nil)
	}
	src, err := s.sourceDefinition(spec, transient)
	if err != nil {
		return nil, err
	}
	if transient && existing == nil {
		src.transient = camera
	}
	if src.fault != "" {
		return nil, NewError(src.fault, nil)
	}
	if src.unknown || src.resetting {
		return nil, NewError(CodeCleanupIncomplete, nil)
	}
	if s.active >= s.options.MaxViewers || len(src.live) >= s.options.PerSourceViewers || len(s.sessions) >= s.options.MaxSessionRecords || s.sessions[id] != nil {
		return nil, NewError(CodeCapacityExceeded, nil)
	}
	v := &serviceSession{source: src, camera: camera, stream: stream, cancel: cancel, reserved: true, pending: true,
		state: SessionState{ID: id, SourceKey: spec.Key, State: StateConnecting, Resources: PresenceUnknown}}
	s.sessions[id], src.live[id] = v, v
	src.parked = false
	s.active++
	s.devices[src.device] = src.key
	return v, nil
}

// Caller holds mu. The backend checks opaque concrete definitions in EnsureStream.
func (s *Service) sourceDefinition(spec StreamSpec, transient bool) (*serviceSource, error) {
	src := s.sources[spec.Key]
	if src != nil {
		if !sameSource(src, spec) {
			return nil, NewError(CodeConflict, nil)
		}
		if src.parked {
			for _, other := range s.sources {
				if other.slotLender == src {
					return nil, NewError(CodeCameraBusy, nil)
				}
			}
		}
		return src, nil
	}
	usedSlots := len(s.sources)
	var lender *serviceSource
	anotherProbe := false
	for _, other := range s.sources {
		if parked := other.slotLender; parked != nil && s.sources[parked.key] == parked {
			usedSlots--
		}
		if other.device != spec.PhysicalCameraID {
			continue
		}
		if other.constraints != spec.Constraints {
			return nil, NewError(CodeConflict, nil)
		}
		if other.parked {
			lender = other
		}
		anotherProbe = anotherProbe || other.transient != ""
	}
	// The parked definition and its probe share one slot until retirement.
	if !transient || anotherProbe {
		lender = nil
	}
	if usedSlots >= s.options.MaxSources && lender == nil {
		return nil, NewError(CodeCapacityExceeded, nil)
	}
	src = &serviceSource{key: spec.Key, device: spec.PhysicalCameraID, kind: spec.Source.Kind(), capabilities: spec.Capabilities, constraints: spec.Constraints, gate: make(chan struct{}, 1), live: make(map[SessionID]*serviceSession), slotLender: lender}
	s.sources[spec.Key] = src
	return src, nil
}

func (s *Service) create(ctx context.Context, v *serviceSession, spec StreamSpec, offer Offer) (SessionState, error) {
	if err := ctx.Err(); err != nil {
		return SessionState{}, err
	}
	state, err := s.backend.EnsureStream(ctx, spec)
	if err != nil {
		return SessionState{}, err
	}
	if err = s.checkAdmission(v, state); err != nil {
		return SessionState{}, err
	}
	result, err := s.backend.CreateSession(ctx, SessionSpec{ID: v.state.ID, SourceKey: spec.Key, Offer: offer})
	if err != nil {
		return SessionState{}, err
	}
	if err = ctx.Err(); err != nil {
		return SessionState{}, err
	}
	if result.ID != v.state.ID || result.SourceKey != spec.Key || result.Answer == nil || result.Answer.SDP == "" || result.Resources != PresencePresent {
		return SessionState{}, NewError(CodeSignalingFailed, nil)
	}
	s.mu.Lock()
	if err = ctx.Err(); err != nil {
		s.mu.Unlock()
		return SessionState{}, err
	}
	v.pending = false
	v.cancel = nil
	v.state = result
	v.state.Answer = nil // never retain SDP in service records
	s.mu.Unlock()
	copyAnswer := *result.Answer
	result.Answer = &copyAnswer
	return result, nil
}

func serviceEnter(ctx context.Context, source *serviceSource) error {
	select {
	case source.gate <- struct{}{}:
		if source.retired.Load() {
			serviceLeave(source)
			return NewError(CodeNotFound, nil)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func serviceLeave(source *serviceSource) { <-source.gate }
func serviceError(err error) error {
	if err == nil {
		return nil
	}
	return NewError(DescribeError(err).Code, err)
}

func (s *Service) checkAdmission(v *serviceSession, state StreamState) error {
	if state.Key != v.source.key {
		return NewError(CodeBackendUnavailable, nil)
	}
	s.mu.Lock()
	if state.Fault != "" {
		v.source.fault = DescribeError(NewError(state.Fault, nil)).Code
	}
	fault, blocked := v.source.fault, v.source.resetting || v.closing
	s.mu.Unlock()
	if fault != "" {
		return NewError(fault, nil)
	}
	if blocked {
		return NewError(CodeCanceled, context.Canceled)
	}
	if !knownStreamState(state) {
		return NewError(CodeCleanupIncomplete, nil)
	}
	return nil
}

func (s *Service) resolve(ctx context.Context, camera Camera, stream StreamID) (StreamSpec, error) {
	strategy, err := s.strategy(camera)
	if err != nil {
		return StreamSpec{}, err
	}
	spec, err := strategy.Resolve(ctx, camera, stream)
	if err != nil {
		return StreamSpec{}, serviceError(err)
	}
	if spec.Key == "" || spec.PhysicalCameraID != camera.PhysicalID || spec.Source == nil || spec.Source.Kind() == "" || spec.Constraints.MaxActiveSources < 0 || stream == "" {
		return StreamSpec{}, NewError(CodeInvalidArgument, nil)
	}
	return spec, nil
}

func knownStreamState(state StreamState) bool {
	return state.ActiveSessions != nil && *state.ActiveSessions >= 0 && (state.Upstream == PresenceAbsent || state.Upstream == PresencePresent)
}

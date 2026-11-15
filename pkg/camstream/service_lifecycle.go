package camstream

import (
	"context"
	"sync"
	"time"
)

func (s *Service) sessionSnapshot(v *serviceSession) SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return v.state
}

func (s *Service) lookupSession(id SessionID) (*serviceSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.sessions[id]
	if v == nil {
		return nil, NewError(CodeNotFound, nil)
	}
	return v, nil
}

// SessionBinding supports host access checks without exposing config or sources.
// Authentication/principal policy belongs to the embedding host, not this service.
func (s *Service) SessionBinding(id SessionID) (CameraID, StreamID, error) {
	v, err := s.lookupSession(id)
	if err != nil {
		return "", "", err
	}
	return v.camera, v.stream, nil
}

func (s *Service) SessionStatus(ctx context.Context, id SessionID) (SessionState, error) {
	v, err := s.lookupSession(id)
	if err != nil {
		return SessionState{}, err
	}
	if err = serviceEnter(ctx, v.source); err != nil {
		return SessionState{}, err
	}
	defer serviceLeave(v.source)
	err = s.observeViewer(ctx, v)
	return s.sessionSnapshot(v), serviceError(err)
}

// StreamStatus resolves a profile without acquiring it. Before first demand the
// service owns no resources for that key. Stale/error observations stay unknown.
func (s *Service) StreamStatus(ctx context.Context, camera Camera, stream StreamID) (StreamState, error) {
	spec, err := s.resolve(ctx, camera, stream)
	if err != nil {
		return StreamState{}, err
	}
	s.mu.Lock()
	src := s.sources[spec.Key]
	s.mu.Unlock()
	if src == nil {
		zero := 0
		return StreamState{Key: spec.Key, State: StateIdle, Upstream: PresenceAbsent, ActiveSessions: &zero, ObservedAt: time.Now()}, nil
	}
	if src.device != spec.PhysicalCameraID || src.kind != spec.Source.Kind() || src.constraints != spec.Constraints || src.capabilities != spec.Capabilities {
		return StreamState{}, NewError(CodeConflict, nil)
	}
	if err = serviceEnter(ctx, src); err != nil {
		return StreamState{}, err
	}
	defer serviceLeave(src)
	err = s.observeSource(ctx, src)
	s.mu.Lock()
	state := copyStreamState(src.state)
	s.mu.Unlock()
	return state, serviceError(err)
}

func copyStreamState(state StreamState) StreamState {
	if state.ActiveSessions != nil {
		count := *state.ActiveSessions
		state.ActiveSessions = &count
	}
	return state
}

// CloseSession cancels any in-flight negotiation before waiting for its source.
// Failed cleanup retains reservations and can be retried by this method or the
// observer. No other viewer is closed and no source fault is reset.
func (s *Service) CloseSession(ctx context.Context, id SessionID) error {
	v, err := s.lookupSession(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	v.closing = true
	if v.cancel != nil {
		v.cancel()
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, s.options.CleanupTimeout)
	defer cancel()
	if err = serviceEnter(ctx, v.source); err != nil {
		return NewError(CodeCleanupIncomplete, err)
	}
	defer serviceLeave(v.source)
	return s.closeViewer(ctx, v)
}

func (s *Service) closeViewer(ctx context.Context, v *serviceSession) error {
	s.mu.Lock()
	reserved := v.reserved
	v.pending, v.closing, v.cancel = false, true, nil
	s.mu.Unlock()
	if reserved {
		if err := s.backend.CloseSession(ctx, v.state.ID); err != nil {
			s.mu.Lock()
			v.state.State, v.state.Resources = StateClosing, PresenceUnknown
			v.source.unknown = true
			s.mu.Unlock()
			return NewError(CodeCleanupIncomplete, err)
		}
		s.mu.Lock()
		s.releaseViewer(v, StateClosed, v.state.Fault)
		s.mu.Unlock()
	}
	if err := s.observeSource(ctx, v.source); err != nil {
		return NewError(CodeCleanupIncomplete, err)
	}
	s.mu.Lock()
	unknown := v.source.unknown
	s.mu.Unlock()
	if unknown {
		return NewError(CodeCleanupIncomplete, nil)
	}
	return nil
}

// Caller holds mu; only a successful close or a fresh absence observation may
// release a viewer. Releasing the physical device additionally needs source proof.
func (s *Service) releaseViewer(v *serviceSession, state State, fault ErrorCode) {
	if v.reserved {
		v.reserved = false
		s.active--
		delete(v.source.live, v.state.ID)
	}
	v.pending = false
	v.cancel = nil
	v.state.State, v.state.Resources, v.state.Answer = state, PresenceAbsent, nil
	v.state.Fault, v.state.ObservedAt = fault, time.Now()
}

func (s *Service) observeViewer(ctx context.Context, v *serviceSession) error {
	s.mu.Lock()
	reserved, pending, closing := v.reserved, v.pending, v.closing
	s.mu.Unlock()
	if !reserved || pending {
		return nil
	}
	if closing {
		return s.closeViewer(ctx, v)
	}
	state, err := s.backend.SessionStatus(ctx, v.state.ID)
	s.mu.Lock()
	if err != nil || state.ID != v.state.ID || state.SourceKey != v.source.key || !s.fresh(state.ObservedAt) {
		v.state.State, v.state.Resources = StateUnknown, PresenceUnknown
		v.source.unknown = true
	} else {
		state.Answer = nil
		v.state = state
		if state.Resources == PresenceAbsent {
			s.releaseViewer(v, state.State, state.Fault)
		}
	}
	s.mu.Unlock()
	return err
}

func (s *Service) fresh(observed time.Time) bool {
	age := time.Since(observed)
	return !observed.IsZero() && age >= 0 && age <= s.options.ObservationMaxAge
}

func (s *Service) observeSource(ctx context.Context, src *serviceSource) error {
	state, err := s.sourceObservation(ctx, src)
	s.mu.Lock()
	defer s.mu.Unlock()
	if state.Fault != "" {
		src.fault = DescribeError(NewError(state.Fault, nil)).Code
	}
	src.unknown = src.resetFailed || err != nil || state.Key != src.key || !s.fresh(state.ObservedAt) || !knownStreamState(state)
	for _, v := range src.live {
		if !v.pending && v.state.Resources != PresencePresent {
			src.unknown = true
		}
	}
	src.state = copyStreamState(state)
	src.state.Key, src.state.Fault = src.key, src.fault
	if src.unknown {
		src.state.State, src.state.Upstream, src.state.ActiveSessions = StateUnknown, PresenceUnknown, nil
	}
	if !src.unknown && len(src.live) == 0 && idleStream(state) && s.devices[src.device] == src.key {
		delete(s.devices, src.device)
	}
	return err
}

func idleStream(state StreamState) bool {
	return state.Upstream == PresenceAbsent && state.ActiveSessions != nil && *state.ActiveSessions == 0
}

// ResetSource is exclusively an operator action. The host must authorize it
// separately from viewer retry. It returns the IDs whose demand was canceled;
// host catalog policy maps the source to affected aliases. No automatic path
// calls this method or clears a fault latch.
func (s *Service) ResetSource(ctx context.Context, key SourceKey) ([]SessionID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resetter, ok := s.backend.(SourceResetter)
	if !ok {
		return nil, NewError(CodeUnsupported, nil)
	}
	s.mu.Lock()
	src := s.sources[key]
	if src == nil {
		s.mu.Unlock()
		return nil, NewError(CodeNotFound, nil)
	}
	if src.resetting || s.stopping {
		s.mu.Unlock()
		return nil, NewError(CodeConflict, nil)
	}
	src.resetting = true
	ids := make([]SessionID, 0, len(src.live))
	for id, v := range src.live {
		ids = append(ids, id)
		if v.cancel != nil {
			v.cancel()
		}
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); src.resetting = false; s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, s.options.CleanupTimeout)
	defer cancel()
	if err := serviceEnter(ctx, src); err != nil {
		s.mu.Lock()
		src.unknown, src.resetFailed = true, true
		s.mu.Unlock()
		return ids, NewError(CodeCleanupIncomplete, err)
	}
	defer serviceLeave(src)
	if err := s.resetBackendSource(ctx, src, resetter); err != nil {
		s.mu.Lock()
		src.unknown, src.resetFailed = true, true
		s.mu.Unlock()
		return ids, NewError(CodeCleanupIncomplete, err)
	}
	s.mu.Lock()
	for _, v := range src.live {
		s.releaseViewer(v, StateClosed, "")
	}
	src.fault, src.unknown, src.resetFailed = "", false, false
	if s.devices[src.device] == src.key {
		delete(s.devices, src.device)
	}
	s.mu.Unlock()
	return ids, nil
}

// Caller holds the source gate and has blocked new admission with resetting.
func (s *Service) resetBackendSource(ctx context.Context, src *serviceSource, resetter SourceResetter) error {
	err := resetter.ResetSource(ctx, src.key)
	if DescribeError(err).Code != CodeNotFound {
		return err
	}
	s.mu.Lock()
	empty := len(src.live) == 0
	s.mu.Unlock()
	// Idle observation may already have removed the backend registration.
	// Missing registration alone is not proof that resources were released.
	if empty {
		return s.backend.RemoveStream(ctx, src.key)
	}
	return err
}

func (s *Service) sourceList() []*serviceSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*serviceSource, 0, len(s.sources))
	for _, src := range s.sources {
		list = append(list, src)
	}
	return list
}

func (s *Service) liveList(src *serviceSource) []*serviceSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*serviceSession, 0, len(src.live))
	for _, v := range src.live {
		list = append(list, v)
	}
	return list
}

func (s *Service) monitorService() {
	defer close(s.done)
	ticker := time.NewTicker(s.options.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.runtime.Done():
			ctx, cancel := context.WithTimeout(context.Background(), s.options.CleanupTimeout)
			defer cancel()
			// Close retries and reports any incomplete initial runtime drain.
			_ = s.drain(ctx) //nolint:errcheck // No caller at cancellation; Close reports the retained outcome.
			return
		case <-ticker.C:
			s.pollService()
		}
	}
}

func (s *Service) pollService() {
	var workers sync.WaitGroup
	for _, src := range s.sourceList() {
		select {
		case src.gate <- struct{}{}:
		default:
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer serviceLeave(src)
			ctx, cancel := context.WithTimeout(s.runtime, s.options.CleanupTimeout)
			defer cancel()
			for _, v := range s.liveList(src) {
				_ = s.observeViewer(ctx, v) //nolint:errcheck // Errors retain unknown state; polling is not a media retry.
			}
			_ = s.observeSource(ctx, src) //nolint:errcheck // Unknown state withholds admission until a later observation.
		}()
	}
	workers.Wait()
}

// Close stops admission, cancels pending creation and drains service-owned
// sessions. It does not close the host-owned backend Runtime. Cleanup errors
// retain ownership and may be retried; a canceled runtime is not cleanup proof.
func (s *Service) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.options.CleanupTimeout)
	defer cancel()
	s.mu.Lock()
	s.stopping = true
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.cancel()
	done := s.done
	for _, v := range s.sessions {
		if v.cancel != nil {
			v.cancel()
		}
	}
	s.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return NewError(CodeCleanupIncomplete, ctx.Err())
	}
	return s.drain(ctx)
}

func (s *Service) drain(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	for _, v := range s.sessions {
		if v.cancel != nil {
			v.cancel()
		}
	}
	s.mu.Unlock()
	sources := s.sourceList()
	results := make(chan error, len(sources))
	for _, src := range sources {
		go func() {
			if err := serviceEnter(ctx, src); err != nil {
				results <- NewError(CodeCleanupIncomplete, err)
				return
			}
			defer serviceLeave(src)
			results <- s.drainSource(ctx, src)
		}()
	}
	var first error
	for range sources {
		if err := <-results; err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Service) drainSource(ctx context.Context, src *serviceSource) error {
	var first error
	for _, v := range s.liveList(src) {
		if err := s.closeViewer(ctx, v); err != nil && first == nil {
			first = err
		}
	}
	if err := s.observeSource(ctx, src); err != nil {
		return NewError(CodeCleanupIncomplete, err)
	}
	s.mu.Lock()
	reserved := s.devices[src.device] == src.key
	s.mu.Unlock()
	if reserved {
		return NewError(CodeCleanupIncomplete, nil)
	}
	return first
}

// A timestamp-free idle snapshot is not release evidence. With no local demand,
// RemoveStream's success can instead prove absence, even when EnsureStream failed
// before the backend allocated anything. It must never clear a backend fault.
func (s *Service) sourceObservation(ctx context.Context, src *serviceSource) (StreamState, error) {
	state, err := s.backend.StreamStatus(ctx, src.key)
	s.mu.Lock()
	empty := len(src.live) == 0
	s.mu.Unlock()
	missing := DescribeError(err).Code == CodeNotFound
	unobservedIdle := err == nil && empty && idleStream(state) && state.Fault == "" && !s.fresh(state.ObservedAt)
	if !missing && !unobservedIdle {
		return state, err
	}
	if err = s.backend.RemoveStream(ctx, src.key); err != nil {
		return state, err
	}
	zero := 0
	return StreamState{Key: src.key, State: StateIdle, Upstream: PresenceAbsent, ActiveSessions: &zero, ObservedAt: time.Now()}, nil
}

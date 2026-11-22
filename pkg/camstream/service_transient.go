package camstream

import "context"

// RetireTransientSource releases private probe metadata only after its sessions
// are closed and the backend confirms absence. Unknown cleanup retains the source
// and admission block. Ordinary sources and aliases cannot use this operation.
func (s *Service) RetireTransientSource(ctx context.Context, camera Camera, stream StreamID) error {
	return s.retireInactiveSource(ctx, camera, stream, true, false)
}

// RetireInactiveSource releases an old definition during an authorized host
// configuration change. The host must fence creation for all aliases of it.
// Existing viewers are never closed by this operation.
func (s *Service) RetireInactiveSource(ctx context.Context, camera Camera, stream StreamID) error {
	return s.retireInactiveSource(ctx, camera, stream, false, false)
}

// PrepareInactiveSource frees an idle source's backend resources for a
// replacement probe while keeping the saved definition and fault latch.
func (s *Service) PrepareInactiveSource(ctx context.Context, camera Camera, stream StreamID) error {
	return s.retireInactiveSource(ctx, camera, stream, false, true)
}

// DiscardPreparedSource forgets an already prepared definition during durable
// replacement. The host still owns the admission fence for all its aliases.
func (s *Service) DiscardPreparedSource(key SourceKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.sources[key]
	if src == nil {
		return nil
	}
	if !src.parked || len(src.live) != 0 {
		return NewError(CodeConflict, nil)
	}
	src.retired.Store(true)
	delete(s.sources, key)
	return nil
}

func (s *Service) retireInactiveSource(ctx context.Context, camera Camera, stream StreamID, transient, preserve bool) error {
	spec, err := s.resolve(ctx, camera, stream)
	if err != nil {
		return err
	}
	retirer, ok := s.backend.(TransientRetirer)
	if transient && !ok {
		return NewError(CodeUnsupported, nil)
	}
	resetter, ok := s.backend.(SourceResetter)
	if !ok {
		return NewError(CodeUnsupported, nil)
	}
	s.mu.Lock()
	src := s.sources[spec.Key]
	if src == nil {
		s.mu.Unlock()
		return nil
	}
	if (transient && src.transient != camera.ID) || (!transient && src.transient != "") ||
		!sameSource(src, spec) || src.resetting || s.stopping {
		s.mu.Unlock()
		return NewError(CodeConflict, nil)
	}
	if len(src.live) != 0 {
		s.mu.Unlock()
		if !transient {
			return NewError(CodeCameraBusy, nil)
		}
		return NewError(CodeCleanupIncomplete, nil)
	}
	if preserve && src.parked {
		s.mu.Unlock()
		return nil
	}
	src.resetting = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); src.resetting = false; s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, s.options.CleanupTimeout)
	defer cancel()
	if err = serviceEnter(ctx, src); err != nil {
		return NewError(CodeCleanupIncomplete, nil)
	}
	defer serviceLeave(src)
	// Explicitly authorized maintenance. This also
	// handles a fault left by failed EnsureStream without assuming not-found means absent.
	if err = s.resetBackendSource(ctx, src, resetter); err == nil {
		err = s.backend.RemoveStream(ctx, src.key)
	}
	if err != nil {
		s.mu.Lock()
		src.unknown, src.resetFailed = true, true
		s.mu.Unlock()
		return NewError(CodeCleanupIncomplete, nil)
	}
	s.mu.Lock()
	ids := []SessionID{}
	for id, v := range s.sessions {
		if v.source == src {
			if v.reserved || v.pending || v.state.Resources != PresenceAbsent {
				s.mu.Unlock()
				return NewError(CodeCleanupIncomplete, nil)
			}
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	if transient {
		err = retirer.RetireTransientSessions(ctx, src.key, ids)
	}
	if err != nil {
		s.mu.Lock()
		src.unknown, src.resetFailed = true, true
		s.mu.Unlock()
		return NewError(CodeCleanupIncomplete, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.devices[src.device] == src.key {
		delete(s.devices, src.device)
	}
	if preserve {
		src.parked = true
		src.state = idleState(src.key)
		src.state.Fault = src.fault
		src.unknown, src.resetFailed = false, false
		return nil
	}
	src.retired.Store(true)
	if transient {
		for _, id := range ids {
			delete(s.sessions, id)
		}
	}
	delete(s.sources, src.key)
	return nil
}

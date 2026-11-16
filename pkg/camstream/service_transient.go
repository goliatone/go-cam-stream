package camstream

import "context"

// RetireTransientSource releases private probe metadata only after its sessions
// are closed and the backend confirms absence. Unknown cleanup retains the source
// and admission block. Ordinary sources and aliases cannot use this operation.
func (s *Service) RetireTransientSource(ctx context.Context, camera Camera, stream StreamID) error {
	spec, err := s.resolve(ctx, camera, stream)
	if err != nil {
		return err
	}
	retirer, ok := s.backend.(TransientRetirer)
	if !ok {
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
	if src.transient != camera.ID || !sameSource(src, spec) || src.resetting || s.stopping {
		s.mu.Unlock()
		return NewError(CodeConflict, nil)
	}
	if len(src.live) != 0 {
		s.mu.Unlock()
		return NewError(CodeCleanupIncomplete, nil)
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
	// Explicitly authorized transient reset, never a viewer retry. This also
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
	if err = retirer.RetireTransientSessions(ctx, src.key, ids); err != nil {
		s.mu.Lock()
		src.unknown, src.resetFailed = true, true
		s.mu.Unlock()
		return NewError(CodeCleanupIncomplete, nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src.retired.Store(true)
	for _, id := range ids {
		delete(s.sessions, id)
	}
	if s.devices[src.device] == src.key {
		delete(s.devices, src.device)
	}
	delete(s.sources, src.key)
	return nil
}

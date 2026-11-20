package sharing

import (
	"context"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

func (s *Service) AdmitViewer(ctx context.Context, msg AdmitShareViewer) (api.AdmitShareViewerResult, error) {
	if err := msg.Validate(); err != nil {
		return api.AdmitShareViewerResult{}, err
	}
	sh, viewer, previous, err := s.reserve(ctx, msg)
	if err != nil {
		return api.AdmitShareViewerResult{}, err
	}
	if err := s.retire(ctx, previous); err != nil {
		s.drop(sh, viewer)
		return api.AdmitShareViewerResult{}, err
	}

	result := api.AdmitShareViewerResult{ViewerID: viewer.scope.ViewerID, AttemptID: viewer.scope.AttemptID, ExpiresAt: sh.deadline}
	var host *HostRelay
	if s.opts.Relay != nil {
		grant := RelayGrant{Attempt: viewer.scope, NotAfter: sh.deadline}
		issueCtx, cancel := context.WithTimeout(ctx, min(negotiationTimeout, sh.deadline.Sub(s.opts.Clock())))
		credentials, issueErr := s.opts.Relay.Issue(issueCtx, grant)
		cancel()
		if issueErr == nil {
			var binding HostRelay
			binding, result.ICE, issueErr = credentials.ForAttempt(grant, s.opts.Clock())
			host = &binding
		}
		if issueErr != nil {
			s.drop(sh, viewer)
			return api.AdmitShareViewerResult{}, fault(api.ShareRelayUnavailable)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.active(sh.view.ShareID); err != nil {
		return api.AdmitShareViewerResult{}, err
	}
	if sh.viewers[viewer.scope.ViewerID] != viewer || !s.opts.Clock().Before(viewer.negotiation) {
		return api.AdmitShareViewerResult{}, fault(api.ShareStaleAttempt)
	}
	viewer.relay, viewer.ready = host, true
	return result, nil
}

func (s *Service) reserve(ctx context.Context, msg AdmitShareViewer) (*shareState, *viewerState, *viewerState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, cookie, err := s.guest(ctx, msg.ShareID)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(s.retired)+s.inflight >= maxShares*api.ShareMaxViewers {
		return nil, nil, nil, fault(api.ShareMediaUnreachable)
	}
	var previous *viewerState
	id := msg.ViewerID
	now := s.opts.Clock()
	if id == "" {
		if len(sh.viewers) >= sh.view.Limit {
			return nil, nil, nil, fault(api.ShareViewerAtCapacity)
		}
		id = handle()
	} else {
		previous = sh.viewers[id]
		if previous == nil || previous.cookie != cookie || previous.expired(now) || (previous.peer == "" && previous.reconnect.IsZero()) {
			return nil, nil, nil, fault(api.ShareStaleAttempt)
		}
		cancelViewer(previous)
	}
	viewer := &viewerState{
		scope: ViewerAttempt{ShareID: msg.ShareID, ViewerID: id, AttemptID: handle()}, cookie: cookie,
		negotiation: now.Add(negotiationTimeout),
	}
	sh.viewers[id] = viewer
	return sh, viewer, previous, nil
}

// MediaLive records media activity observed by the owned adapter for this peer.
func (s *Service) MediaLive(scope ViewerAttempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh := s.shares[scope.ShareID]
	if sh != nil {
		if v := sh.viewers[scope.ViewerID]; v != nil && v.scope == scope && v.reconnect.IsZero() {
			v.live = true
		}
	}
}

func (v *viewerState) expired(now time.Time) bool {
	if !v.reconnect.IsZero() {
		return !now.Before(v.reconnect)
	}
	return v.peer == "" && !now.Before(v.negotiation)
}

func (s *Service) drop(sh *shareState, v *viewerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh.viewers[v.scope.ViewerID] == v {
		delete(sh.viewers, v.scope.ViewerID)
	}
}

func (s *Service) OpenViewer(ctx context.Context, msg OpenShareViewer) (api.OpenShareViewerResult, error) {
	if err := msg.Validate(); err != nil {
		return api.OpenShareViewerResult{}, err
	}
	s.mu.Lock()
	sh, v, err := s.ownedViewer(ctx, msg.ShareID, msg.ViewerID, msg.AttemptID)
	if err != nil {
		s.mu.Unlock()
		return api.OpenShareViewerResult{}, err
	}
	if !v.ready || v.peer != "" || v.done != nil || !v.reconnect.IsZero() || v.expired(s.opts.Clock()) {
		s.mu.Unlock()
		return api.OpenShareViewerResult{}, fault(api.ShareStaleAttempt)
	}
	openCtx, cancel := context.WithTimeout(ctx, min(v.negotiation.Sub(s.opts.Clock()), sh.deadline.Sub(s.opts.Clock())))
	v.cancel, v.done = cancel, make(chan struct{})
	s.inflight++
	s.mu.Unlock()

	peer, openErr := s.opts.Media.Open(openCtx, MediaOffer{SourceID: sh.source, Attempt: v.scope, Offer: msg.Offer, Relay: v.relay})
	canceled := openCtx.Err() != nil
	cancel()

	s.mu.Lock()
	current := s.shares[msg.ShareID] == sh && sh.viewers[msg.ViewerID] == v && s.opts.Clock().Before(sh.deadline)
	if current && openErr == nil && !canceled && peer.ID != "" {
		v.peer = peer.ID
		close(v.done)
		v.done, v.cancel = nil, nil
		s.inflight--
		s.mu.Unlock()
		return api.OpenShareViewerResult{Answer: peer.Answer}, nil
	}
	if sh.viewers[msg.ViewerID] == v {
		delete(sh.viewers, msg.ViewerID)
	}
	s.mu.Unlock()
	s.closePeer(peer.ID)
	s.mu.Lock()
	close(v.done)
	s.inflight--
	s.mu.Unlock()
	if !current || canceled {
		return api.OpenShareViewerResult{}, fault(api.ShareStaleAttempt)
	}
	if f, ok := openErr.(Fault); ok {
		return api.OpenShareViewerResult{}, f
	}
	return api.OpenShareViewerResult{}, fault(api.ShareMediaUnreachable)
}

func (s *Service) ownedViewer(ctx context.Context, share, viewer, attempt string) (*shareState, *viewerState, error) {
	sh, cookie, err := s.guest(ctx, share)
	if err != nil {
		return nil, nil, err
	}
	v := sh.viewers[viewer]
	if v == nil || v.cookie != cookie || v.scope.AttemptID != attempt {
		return nil, nil, fault(api.ShareStaleAttempt)
	}
	return sh, v, nil
}

func (s *Service) CloseViewer(ctx context.Context, msg CloseShareViewer) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	sh, v, err := s.ownedViewer(ctx, msg.ShareID, msg.ViewerID, msg.AttemptID)
	if err != nil {
		s.mu.Unlock()
		// Old or repeated cleanup never touches a replacement.
		if err == fault(api.ShareStaleAttempt) || err == fault(api.ShareExpiryReached) || err == fault(api.ShareAccessRevoked) {
			return nil
		}
		return err
	}
	cancelViewer(v)
	delete(sh.viewers, msg.ViewerID)
	s.mu.Unlock()
	return s.retire(ctx, v)
}

// TransportLost is called by the owned media adapter for the matching peer.
func (s *Service) TransportLost(scope ViewerAttempt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh := s.shares[scope.ShareID]
	if sh == nil {
		return
	}
	v := sh.viewers[scope.ViewerID]
	if v == nil || v.scope != scope || v.peer == "" || !v.reconnect.IsZero() {
		return
	}
	v.reconnect = s.opts.Clock().Add(api.ShareReconnectWindow)
}

func cancelViewer(v *viewerState) {
	if v.cancel != nil {
		v.cancel()
	}
}

func (s *Service) retire(ctx context.Context, v *viewerState) error {
	if v == nil {
		return nil
	}
	if v.done != nil {
		select {
		case <-v.done:
		case <-ctx.Done():
			return fault(api.ShareMediaUnreachable)
		}
	}
	return s.closePeer(v.peer)
}

func (s *Service) closePeer(peer string) error {
	if peer == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := s.opts.Media.Close(ctx, peer)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.retired[peer] = struct{}{}
		return fault(api.ShareMediaUnreachable)
	}
	delete(s.retired, peer)
	return nil
}

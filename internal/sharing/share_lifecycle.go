package sharing

import (
	"context"
	"errors"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

func (s *Service) end(sh *shareState, reason api.ShareState) []*viewerState {
	id := sh.view.ShareID
	delete(s.shares, id)
	delete(s.cameras, sh.view.CameraID)
	for key, guest := range s.guests {
		if key.share == id {
			delete(s.guests, key)
			if len(s.guestHistory) == maxGuests {
				delete(s.terminalGuests, s.guestHistory[0])
				s.guestHistory = s.guestHistory[1:]
			}
			s.guestHistory = append(s.guestHistory, key)
			s.terminalGuests[key] = guest
		}
	}
	var peers []*viewerState
	for _, v := range sh.viewers {
		cancelViewer(v)
		peers = append(peers, v)
	}
	sh.view.State, sh.view.Media = reason, api.ShareMediaIdle
	sh.viewers, sh.link = nil, api.ShareLink{}
	if len(s.history) == maxTerminal {
		delete(s.terminal, s.history[0])
		s.history = s.history[1:]
	}
	s.history = append(s.history, id)
	s.terminal[id] = terminalShare{view: project(sh), digest: sh.digest}
	return peers
}

func (s *Service) Revoke(ctx context.Context, msg RevokeShare) (api.ShareView, error) {
	if !caller(ctx).operator {
		return api.ShareView{}, fault(api.SharePermissionDenied)
	}
	if err := msg.Validate(); err != nil {
		return api.ShareView{}, err
	}
	s.mu.Lock()
	sh := s.shares[msg.ShareID]
	if sh == nil {
		record, ok := s.terminal[msg.ShareID]
		s.mu.Unlock()
		if ok {
			view := record.view
			deadline := *view.ExpiresAt
			view.ExpiresAt = &deadline
			return view, nil
		}
		return api.ShareView{}, fault(api.ShareInvalidInvitation)
	}
	state := api.ShareRevoked
	if !s.opts.Clock().Before(sh.deadline) {
		state = api.ShareExpired
	}
	peers := s.end(sh, state)
	view := project(sh)
	s.mu.Unlock()
	return view, s.retireAll(ctx, peers)
}

func (s *Service) retireAll(ctx context.Context, peers []*viewerState) error {
	if len(peers) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.ShareHealthyStopLimit)
	defer cancel()
	results := make(chan error, len(peers))
	for _, v := range peers {
		go func() { results <- s.retire(ctx, v) }()
	}
	var err error
	for range peers {
		err = errors.Join(err, <-results)
	}
	return err
}

func (s *Service) StopCamera(ctx context.Context, camera string) error {
	s.mu.Lock()
	var peers []*viewerState
	if sh := s.shares[s.cameras[camera]]; sh != nil {
		peers = s.end(sh, api.ShareRevoked)
	}
	s.mu.Unlock()
	return s.retireAll(ctx, peers)
}

func (s *Service) Sweep(ctx context.Context) error {
	s.mu.Lock()
	var peers []*viewerState
	now := s.opts.Clock()
	for _, sh := range s.shares {
		if !now.Before(sh.deadline) {
			peers = append(peers, s.end(sh, api.ShareExpired)...)
			continue
		}
		for id, v := range sh.viewers {
			if v.expired(now) {
				cancelViewer(v)
				delete(sh.viewers, id)
				peers = append(peers, v)
			}
		}
	}
	var retry []string
	for peer := range s.retired {
		retry = append(retry, peer)
	}
	s.mu.Unlock()
	err := s.retireAll(ctx, peers)
	for _, peer := range retry {
		err = errors.Join(err, s.closePeer(peer))
	}
	return err
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Sweep(ctx)
		}
	}
}

func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = true
	var peers []*viewerState
	for _, sh := range s.shares {
		peers = append(peers, s.end(sh, api.ShareRevoked)...)
	}
	s.mu.Unlock()
	return errors.Join(s.retireAll(ctx, peers), s.Sweep(ctx), s.opts.Ingress.Stop(ctx))
}

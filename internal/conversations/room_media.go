package conversations

import (
	"context"
	"slices"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

func (s *Service) Join(ctx context.Context, m JoinRoom) (api.JoinedRoom, error) {
	if err := m.Validate(); err != nil {
		return api.JoinedRoom{}, err
	}
	s.mu.Lock()
	r, p, role, err := s.current(ctx, m.RoomID, m.Generation)
	if err != nil {
		s.mu.Unlock()
		return api.JoinedRoom{}, err
	}
	now := s.options.Clock()
	if s.options.Relay == nil || r.relay.validUntil(now, r.expiresAt) {
		defer s.mu.Unlock()
		return s.join(r, p, role, m), nil
	}
	deadline := r.expiresAt
	s.mu.Unlock()

	issueCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	relay, err := s.options.Relay.Issue(issueCtx, m.RoomRef, m.Generation, deadline)
	if err != nil || issueCtx.Err() != nil {
		return api.JoinedRoom{}, Fault(api.RoomMediaUnreachable)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, role, err = s.current(ctx, m.RoomID, m.Generation)
	if err != nil {
		return api.JoinedRoom{}, err
	}
	now = s.options.Clock()
	if !r.relay.validUntil(now, r.expiresAt) {
		if !relay.validUntil(now, r.expiresAt) {
			return api.JoinedRoom{}, Fault(api.RoomMediaUnreachable)
		}
		r.relay = &relay
	}
	return s.join(r, p, role, m), nil
}

func (s *Service) join(r *room, p *participant, role api.RoomRole, m JoinRoom) api.JoinedRoom {
	p.joined, p.lastSeen, p.lostAt = true, s.options.Clock(), time.Time{}
	p.hasJoined = true
	if role == api.RoomHost {
		r.hostTracking = true
	}
	p.publication = m.Publication
	result := api.JoinedRoom{Room: r.view(), RelayOnly: s.options.RelayOnly}
	if r.relay != nil {
		servers := r.relay.Host
		if role == api.RoomGuest {
			servers = r.relay.Guest
		}
		result.ICE = slices.Clone(servers)
		for i := range result.ICE {
			result.ICE[i].URLs = slices.Clone(result.ICE[i].URLs)
		}
	}
	return result
}

func (r *RoomRelay) validUntil(now, bound time.Time) bool {
	if r == nil {
		return false
	}
	for _, group := range [][]api.ShareICECredentials{r.Host, r.Guest} {
		if len(group) == 0 || len(group) > 8 {
			return false
		}
		for _, server := range group {
			if len(server.URLs) == 0 || len(server.URLs) > 8 || server.Username == "" || server.Credential == "" ||
				!server.ExpiresAt.After(now) || server.ExpiresAt.After(bound) {
				return false
			}
		}
	}
	return true
}

func (s *Service) PublishSignal(ctx context.Context, m PublishRoomSignal) (api.RoomView, error) {
	if err := m.Validate(); err != nil {
		return api.RoomView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, role, err := s.current(ctx, m.RoomID, m.Generation)
	if err != nil {
		return api.RoomView{}, err
	}
	if !p.joined || r.guest == nil || !r.host.joined || !r.guest.joined ||
		(role == api.RoomHost && m.RoomSignal.Type != "offer") ||
		(role == api.RoomGuest && (m.RoomSignal.Type != "answer" || r.host.signal == nil)) ||
		(p.signal != nil && *p.signal != m.RoomSignal) {
		return api.RoomView{}, Fault(api.RoomPermissionDenied)
	}
	signal := m.RoomSignal
	p.signal, p.lastSeen = &signal, s.options.Clock()
	return r.view(), nil
}

func (s *Service) SetPublication(ctx context.Context, m SetRoomPublication) (api.RoomView, error) {
	if err := m.Validate(); err != nil {
		return api.RoomView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, _, err := s.current(ctx, m.RoomID, m.Generation)
	if err != nil {
		return api.RoomView{}, err
	}
	if !p.joined {
		return api.RoomView{}, Fault(api.RoomPermissionDenied)
	}
	p.publication, p.lastSeen = m.Publication, s.options.Clock()
	return r.view(), nil
}

func (s *Service) SetConnection(ctx context.Context, m SetRoomConnection) (api.RoomView, error) {
	if err := m.Validate(); err != nil {
		return api.RoomView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, _, err := s.current(ctx, m.RoomID, m.Generation)
	if err != nil {
		return api.RoomView{}, err
	}
	if !p.joined || (m.Connected && (r.guest == nil || r.host.signal == nil || r.guest.signal == nil)) {
		return api.RoomView{}, Fault(api.RoomPermissionDenied)
	}
	now := s.options.Clock()
	if !m.Connected && (p.connected || p.signal != nil) {
		p.lostAt = now
		r.reset()
	} else {
		p.connected, p.lastSeen = m.Connected, now
	}
	return r.view(), nil
}

func (s *Service) Leave(ctx context.Context, m LeaveRoom) (api.RoomView, error) {
	if err := m.Validate(); err != nil {
		return api.RoomView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The participant grant identifies the seat across media generations. A
	// stale peer may leave its own seat but cannot mutate another one's media.
	r, _, role, err := s.lookup(ctx, m.RoomID)
	if err != nil {
		return api.RoomView{}, err
	}
	if r.terminal != "" {
		return r.view(), nil
	}
	if role == api.RoomHost {
		r.finish(api.RoomEnded)
	} else if r.guest.hasJoined {
		r.guest = nil
		r.reset()
	} else {
		r.guest = nil
	}
	return r.view(), nil
}

var _ Workflows = (*Service)(nil)

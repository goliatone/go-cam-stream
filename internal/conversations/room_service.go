package conversations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

// Workflows is shared by the native owner and conversation HTTP adapters.
// Adapters authenticate the caller and supply trusted authority in context.
type Workflows interface {
	Create(context.Context, CreateRoom) (api.CreatedRoom, error)
	Redeem(context.Context, RedeemRoom) (api.RoomAdmission, error)
	Join(context.Context, JoinRoom) (api.JoinedRoom, error)
	PublishSignal(context.Context, PublishRoomSignal) (api.RoomView, error)
	SetPublication(context.Context, SetRoomPublication) (api.RoomView, error)
	SetConnection(context.Context, SetRoomConnection) (api.RoomView, error)
	Leave(context.Context, LeaveRoom) (api.RoomView, error)
	End(context.Context, EndRoom) (api.RoomView, error)
	Poll(context.Context, api.RoomRef) (api.RoomPoll, error)
	HostView(context.Context, api.RoomRef) (api.RoomView, error)
}

type Fault api.RoomErrorCode

func (f Fault) Error() string { return string(f) }

func ProjectError(err error) api.RoomError {
	var fault Fault
	if errors.As(err, &fault) {
		code := api.RoomErrorCode(fault)
		return api.RoomError{Code: code, Retryable: code == api.RoomFull || code == api.RoomStaleGeneration || code == api.RoomMediaUnreachable}
	}
	if errors.Is(err, api.ErrInvalidConversationRequest) {
		return api.RoomError{Code: api.RoomInvalidRequest}
	}
	return api.RoomError{Code: api.RoomMediaUnreachable, Retryable: true}
}

type authorityKey struct{}
type authority struct {
	owner bool
	token string
}

func WithOwner(ctx context.Context) context.Context {
	return context.WithValue(ctx, authorityKey{}, authority{owner: true})
}

func WithParticipant(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, authorityKey{}, authority{token: token})
}

func caller(ctx context.Context) authority {
	a, _ := ctx.Value(authorityKey{}).(authority)
	return a
}

type RoomRelay struct {
	Host  []api.ShareICECredentials
	Guest []api.ShareICECredentials
}

type RelayIssuer interface {
	Issue(context.Context, api.RoomRef, api.RoomGeneration, time.Time) (RoomRelay, error)
}

type Options struct {
	PublicOrigin string
	Clock        func() time.Time
	Relay        RelayIssuer
	RelayOnly    bool
	Loopback     bool
}

type Service struct {
	mu      sync.Mutex
	rooms   roomRepository
	options Options
	key     [32]byte
	closed  bool
}

func NewService(options Options) (*Service, error) {
	origin, err := config.ConversationOrigin(options.PublicOrigin, options.Loopback)
	if err != nil || (options.RelayOnly && options.Relay == nil) {
		return nil, api.ErrInvalidConversationRequest
	}
	options.PublicOrigin = origin
	if options.Clock == nil {
		options.Clock = time.Now
	}
	s := &Service{rooms: roomRepository{rooms: make(map[string]*room)}, options: options}
	rand.Read(s.key[:])
	return s, nil
}

func handle() string {
	var value [32]byte
	rand.Read(value[:])
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func newParticipant(now time.Time) (*participant, string) {
	token := handle()
	return &participant{digest: sha256.Sum256([]byte(token)), lastSeen: now}, token
}

func equalDigest(secret string, want [32]byte) bool {
	got := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func (s *Service) Create(ctx context.Context, _ CreateRoom) (api.CreatedRoom, error) {
	if !caller(ctx).owner {
		return api.CreatedRoom{}, Fault(api.RoomPermissionDenied)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.options.Clock()
	if s.closed {
		return api.CreatedRoom{}, Fault(api.RoomAccessEnded)
	}
	active := 0
	var oldest *room
	for _, candidate := range s.rooms.rooms {
		candidate.refresh(now)
		if candidate.terminal == "" {
			active++
		} else if oldest == nil || candidate.expiresAt.Before(oldest.expiresAt) {
			oldest = candidate
		}
	}
	if active >= 16 {
		return api.CreatedRoom{}, Fault(api.RoomFull)
	}
	host, token := newParticipant(now)
	r := &room{id: handle(), generation: 1, host: host, expiresAt: now.UTC().Truncate(time.Second).Add(api.ConversationLifetime)}
	invitation, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject: r.id, Audience: jwt.ClaimStrings{"camstream-room-guest"},
		ID: handle(), ExpiresAt: jwt.NewNumericDate(r.expiresAt),
	}).SignedString(s.key[:])
	if err != nil {
		return api.CreatedRoom{}, Fault(api.RoomMediaUnreachable)
	}
	link, err := url.Parse(s.options.PublicOrigin)
	if err != nil {
		return api.CreatedRoom{}, Fault(api.RoomMediaUnreachable)
	}
	link.Path, link.Fragment = "/r/"+r.id, invitation
	r.inviteDigest = sha256.Sum256([]byte(invitation))
	if len(s.rooms.rooms) >= 64 && oldest != nil {
		s.rooms.remove(oldest.id)
	}
	s.rooms.put(r)
	return api.CreatedRoom{Admission: api.RoomAdmission{Room: r.view(), Role: api.RoomHost, Token: token}, InvitationURL: link.String()}, nil
}

func (s *Service) Redeem(_ context.Context, m RedeemRoom) (api.RoomAdmission, error) {
	if err := m.Validate(); err != nil {
		return api.RoomAdmission{}, err
	}
	if m.Version != api.ConversationVersion {
		return api.RoomAdmission{}, Fault(api.RoomIncompatibleClient)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.options.Clock()
	r := s.rooms.get(m.RoomID)
	if r == nil || !equalDigest(m.Invitation, r.inviteDigest) {
		return api.RoomAdmission{}, Fault(api.RoomInvitationInvalid)
	}
	r.refresh(now)
	if err := r.active(); err != nil {
		return api.RoomAdmission{}, err
	}
	if r.guest != nil {
		return api.RoomAdmission{}, Fault(api.RoomFull)
	}
	guest, token := newParticipant(now)
	r.guest = guest
	return api.RoomAdmission{Room: r.view(), Role: api.RoomGuest, Token: token}, nil
}

func (s *Service) lookup(ctx context.Context, id string) (*room, *participant, api.RoomRole, error) {
	token := caller(ctx).token
	r := s.rooms.get(id)
	if r == nil || !api.ValidShareID(token) {
		return nil, nil, "", Fault(api.RoomPermissionDenied)
	}
	r.refresh(s.options.Clock())
	if equalDigest(token, r.host.digest) {
		return r, r.host, api.RoomHost, nil
	}
	if r.guest != nil && equalDigest(token, r.guest.digest) {
		return r, r.guest, api.RoomGuest, nil
	}
	return nil, nil, "", Fault(api.RoomPermissionDenied)
}

func (s *Service) current(ctx context.Context, id string, generation api.RoomGeneration) (*room, *participant, api.RoomRole, error) {
	r, p, role, err := s.lookup(ctx, id)
	if err != nil {
		return nil, nil, "", err
	}
	if err := r.active(); err != nil {
		return nil, nil, "", err
	}
	if r.generation != generation {
		return nil, nil, "", Fault(api.RoomStaleGeneration)
	}
	return r, p, role, nil
}

func (s *Service) Poll(ctx context.Context, m api.RoomRef) (api.RoomPoll, error) {
	if err := m.Validate(); err != nil {
		return api.RoomPoll{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, p, role, err := s.lookup(ctx, m.RoomID)
	if err != nil {
		return api.RoomPoll{}, err
	}
	var result api.RoomPoll
	if r.terminal == "" && (p.lostAt.IsZero() || !p.hasJoined) {
		if role == api.RoomHost {
			r.hostTracking = true
		}
		p.lastSeen = s.options.Clock()
		p.lostAt = time.Time{}
		remote := r.host
		if role == api.RoomHost {
			remote = r.guest
		}
		if p.joined && remote != nil && remote.signal != nil {
			signal := *remote.signal
			result.Signal = &signal
		}
	}
	result.Room = r.view()
	return result, nil
}

func (s *Service) End(ctx context.Context, m EndRoom) (api.RoomView, error) {
	if err := m.Validate(); err != nil {
		return api.RoomView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms.get(m.RoomID)
	if !caller(ctx).owner {
		_, _, role, err := s.lookup(ctx, m.RoomID)
		if err != nil || role != api.RoomHost {
			return api.RoomView{}, Fault(api.RoomPermissionDenied)
		}
	}
	if r == nil {
		return api.RoomView{}, Fault(api.RoomInvitationInvalid)
	}
	r.refresh(s.options.Clock())
	r.finish(api.RoomEnded)
	return r.view(), nil
}

// OwnerStatus reports this helper's room without renewing a participant grant.
func (s *Service) OwnerStatus(ctx context.Context, ref api.RoomRef) (api.RoomView, error) {
	if !caller(ctx).owner || ref.Validate() != nil {
		return api.RoomView{}, Fault(api.RoomPermissionDenied)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms.get(ref.RoomID)
	if r == nil {
		return api.RoomView{}, Fault(api.RoomInvitationInvalid)
	}
	r.refresh(s.options.Clock())
	return r.view(), nil
}

func (s *Service) HostView(ctx context.Context, ref api.RoomRef) (api.RoomView, error) {
	if err := ref.Validate(); err != nil {
		return api.RoomView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, host, role, err := s.lookup(ctx, ref.RoomID)
	if err != nil || role != api.RoomHost {
		return api.RoomView{}, Fault(api.RoomPermissionDenied)
	}
	if err := r.active(); err != nil {
		return api.RoomView{}, err
	}
	if !r.hostTracking {
		r.hostTracking = true
		host.lastSeen = s.options.Clock()
	}
	return r.view(), nil
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, r := range s.rooms.rooms {
		r.finish(api.RoomEnded)
	}
}

func (r *room) active() error {
	switch r.terminal {
	case api.RoomExpired:
		return Fault(api.RoomAccessExpired)
	case api.RoomEnded:
		return Fault(api.RoomAccessEnded)
	}
	return nil
}

func (r *room) reset() {
	r.generation++
	r.relay = nil
	for _, p := range []*participant{r.host, r.guest} {
		if p != nil {
			p.joined, p.connected, p.signal = false, false, nil
			p.publication = api.RoomPublication{}
		}
	}
}

func (r *room) finish(state api.RoomState) {
	if r.terminal == "" {
		r.reset()
		r.terminal = state
	}
}

func (r *room) refresh(now time.Time) {
	if r.terminal != "" {
		return
	}
	if !now.Before(r.expiresAt) {
		r.finish(api.RoomExpired)
		return
	}
	reset := false
	for _, p := range []*participant{r.host, r.guest} {
		// The native owner can leave a new host in the chooser while copying
		// the invitation. Start heartbeat expiry when host prejoin connects.
		if p != nil && (p != r.host || r.hostTracking) && p.lostAt.IsZero() && !now.Before(p.lastSeen.Add(api.ConversationHeartbeat)) {
			p.lostAt = p.lastSeen.Add(api.ConversationHeartbeat)
			reset = reset || p.hasJoined
		}
	}
	if !r.host.lostAt.IsZero() && !now.Before(r.host.lostAt.Add(api.ConversationReconnect)) {
		r.finish(api.RoomEnded)
		return
	}
	if r.guest != nil && !r.guest.lostAt.IsZero() && !now.Before(r.guest.lostAt.Add(api.ConversationReconnect)) {
		r.guest = nil
	}
	if reset {
		r.reset()
	}
}

func (r *room) view() api.RoomView {
	v := api.RoomView{RoomRef: api.RoomRef{RoomID: r.id}, Version: api.ConversationVersion, ExpiresAt: r.expiresAt,
		Generation: r.generation, State: r.terminal, Participants: []api.RoomParticipantView{}}
	if r.terminal != "" {
		return v
	}
	v.State = api.RoomWaiting
	project := func(p *participant, role api.RoomRole) api.RoomParticipantView {
		return api.RoomParticipantView{Role: role, Joined: p.joined, Publication: p.publication}
	}
	v.Participants = append(v.Participants, project(r.host, api.RoomHost))
	if r.guest != nil {
		v.Participants = append(v.Participants, project(r.guest, api.RoomGuest))
		v.State = api.RoomJoining
		if r.host.connected && r.guest.connected {
			v.State = api.RoomConnected
		}
	}
	if !r.host.lostAt.IsZero() || (r.guest != nil && r.guest.hasJoined && !r.guest.lostAt.IsZero()) {
		v.State = api.RoomReconnecting
	}
	return v
}

package sharing

import (
	"context"
	"crypto/subtle"
	"net/url"
	"sync"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

const (
	maxShares          = 16
	maxGuests          = 512
	maxTerminal        = 256
	negotiationTimeout = 20 * time.Second
)

type Options struct {
	Clock func() time.Time
	// Source resolves only a camera that is already running under this owner.
	Source  func(context.Context, string) (string, error)
	Tokens  InvitationTokens
	Ingress Ingress
	Relay   RelayIssuer
	Media   Media
}

type Service struct {
	opts           Options
	mu             sync.Mutex
	shares         map[string]*shareState
	cameras        map[string]string
	guests         map[guestKey]guestState
	terminal       map[string]terminalShare
	terminalGuests map[guestKey]guestState
	guestHistory   []guestKey
	history        []string
	retired        map[string]struct{}
	inflight       int
	stopped        bool
}

func NewService(opts Options) (*Service, error) {
	if opts.Source == nil || opts.Tokens == nil || opts.Ingress == nil || opts.Media == nil {
		return nil, fault(api.ShareInvalidRequest)
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Service{
		opts: opts, shares: make(map[string]*shareState), cameras: make(map[string]string),
		guests: make(map[guestKey]guestState), terminal: make(map[string]terminalShare),
		terminalGuests: make(map[guestKey]guestState), retired: make(map[string]struct{}),
	}, nil
}

func (s *Service) Create(ctx context.Context, msg CreateShare) (api.CreateShareResult, error) {
	if !caller(ctx).operator {
		return api.CreateShareResult{}, fault(api.SharePermissionDenied)
	}
	if err := msg.Validate(); err != nil {
		return api.CreateShareResult{}, err
	}
	source, err := s.opts.Source(ctx, msg.CameraID)
	if err != nil || source == "" {
		return api.CreateShareResult{}, fault(api.ShareCameraUnavailable)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return api.CreateShareResult{}, fault(api.ShareIngressUnavailable)
	}
	if s.cameras[msg.CameraID] != "" {
		return api.CreateShareResult{}, fault(api.ShareAlreadyActive)
	}
	if len(s.shares) >= maxShares {
		return api.CreateShareResult{}, fault(api.ShareIngressUnavailable)
	}
	binding, err := s.opts.Ingress.Start(ctx)
	if err != nil {
		return api.CreateShareResult{}, fault(api.ShareIngressUnavailable)
	}
	origin, err := (config.SharingIngress{Mode: config.SharingIngressExternal, PublicBaseURL: binding.Origin}).PublicOrigin()
	if err != nil {
		return api.CreateShareResult{}, fault(api.ShareIngressUnavailable)
	}
	options := msg.Options.WithDefaults()
	ttl := time.Duration(options.DurationSeconds) * time.Second
	deadline := s.opts.Clock().UTC().Truncate(time.Second).Add(ttl)
	id := handle()
	claims := InvitationClaims{Version: InvitationVersion, Purpose: InvitationPurpose, ShareID: id, Nonce: handle()}
	token, err := s.opts.Tokens.Issue(claims, ttl)
	if err != nil {
		return api.CreateShareResult{}, fault(api.ShareIngressUnavailable)
	}
	link, err := url.Parse(origin)
	if err != nil {
		return api.CreateShareResult{}, fault(api.ShareIngressUnavailable)
	}
	link.Path, link.Fragment = "/s/"+id, string(token)
	view := api.ShareView{ShareID: id, CameraID: msg.CameraID, State: api.ShareAvailable, Media: api.ShareMediaIdle, Audience: binding.Audience, ExpiresAt: &deadline, Limit: options.ViewerLimit}
	sh := &shareState{view: view, source: source, link: api.ShareLink{URL: link.String()}, deadline: deadline, claims: claims, digest: digest(string(token)), viewers: make(map[string]*viewerState)}
	s.shares[id], s.cameras[msg.CameraID] = sh, id
	return api.CreateShareResult{Share: project(sh), Link: sh.link}, nil
}

func project(sh *shareState) api.ShareView {
	view := sh.view
	deadline := sh.deadline
	view.ExpiresAt = &deadline
	view.Viewers = len(sh.viewers)
	for _, viewer := range sh.viewers {
		switch {
		case viewer.live && viewer.reconnect.IsZero():
			view.Media = api.ShareMediaLive
			return view
		case !viewer.reconnect.IsZero():
			view.Media = api.ShareMediaReconnecting
		case view.Media == api.ShareMediaIdle:
			view.Media = api.ShareMediaConnecting
		}
	}
	return view
}

func (s *Service) active(id string) (*shareState, error) {
	sh := s.shares[id]
	if sh != nil {
		if !s.opts.Clock().Before(sh.deadline) {
			return nil, fault(api.ShareExpiryReached)
		}
		return sh, nil
	}
	switch s.terminal[id].view.State {
	case api.ShareExpired:
		return nil, fault(api.ShareExpiryReached)
	case api.ShareRevoked:
		return nil, fault(api.ShareAccessRevoked)
	}
	return nil, fault(api.ShareInvalidInvitation)
}

func (s *Service) Status(ctx context.Context, camera string) (api.ShareView, error) {
	if !caller(ctx).operator {
		return api.ShareView{}, fault(api.SharePermissionDenied)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sh := s.shares[s.cameras[camera]]
	if sh == nil {
		return api.ShareView{CameraID: camera, State: api.ShareStopped, Media: api.ShareMediaIdle}, nil
	}
	view := project(sh)
	if !s.opts.Clock().Before(sh.deadline) {
		view.State, view.Media = api.ShareExpired, api.ShareMediaIdle
	}
	return view, nil
}

func (s *Service) CopyLink(ctx context.Context, msg CopyShareLink) (api.ShareLink, error) {
	if !caller(ctx).operator {
		return api.ShareLink{}, fault(api.SharePermissionDenied)
	}
	if err := msg.Validate(); err != nil {
		return api.ShareLink{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, err := s.active(msg.ShareID)
	if err != nil {
		return api.ShareLink{}, err
	}
	return sh.link, nil
}

func (s *Service) Redeem(ctx context.Context, msg RedeemShare) (RedeemedGuest, error) {
	if caller(ctx).operator {
		return RedeemedGuest{}, fault(api.SharePermissionDenied)
	}
	if msg.Validate() != nil {
		return RedeemedGuest{}, fault(api.ShareInvalidInvitation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tokenDigest := digest(msg.Invitation)
	if previous, ok := s.terminal[msg.ShareID]; ok {
		if subtle.ConstantTimeCompare(tokenDigest[:], previous.digest[:]) != 1 {
			return RedeemedGuest{}, fault(api.ShareInvalidInvitation)
		}
	}
	sh, err := s.active(msg.ShareID)
	if err != nil {
		if expired := s.shares[msg.ShareID]; expired != nil && subtle.ConstantTimeCompare(tokenDigest[:], expired.digest[:]) != 1 {
			return RedeemedGuest{}, fault(api.ShareInvalidInvitation)
		}
		return RedeemedGuest{}, err
	}
	if subtle.ConstantTimeCompare(tokenDigest[:], sh.digest[:]) != 1 {
		return RedeemedGuest{}, fault(api.ShareInvalidInvitation)
	}
	claims, err := s.opts.Tokens.Verify(Secret(msg.Invitation))
	if err != nil || claims != sh.claims {
		return RedeemedGuest{}, fault(api.ShareInvalidInvitation)
	}

	// Reuse the cookie only if it already names a live server-issued session.
	cookie := caller(ctx).cookie
	hasCookie := false
	if api.ValidShareID(string(cookie)) {
		cookieHash := digest(string(cookie))
		for key, guest := range s.guests {
			if key.cookie == cookieHash && s.opts.Clock().Before(guest.deadline) {
				hasCookie = true
				break
			}
		}
	}
	if !hasCookie {
		cookie = Secret(handle())
	}
	key := guestKey{share: msg.ShareID, cookie: digest(string(cookie))}
	if _, exists := s.guests[key]; !exists && len(s.guests) >= maxGuests {
		return RedeemedGuest{}, fault(api.ShareViewerAtCapacity)
	}
	guest, ok := s.guests[key]
	if !ok {
		guest = guestState{csrf: Secret(handle()), deadline: sh.deadline}
		s.guests[key] = guest
	}
	return RedeemedGuest{Cookie: cookie, Result: api.RedeemShareResult{ExpiresAt: sh.deadline, CSRFToken: string(guest.csrf)}}, nil
}

func (s *Service) guest(ctx context.Context, id string) (*shareState, [32]byte, error) {
	auth := caller(ctx)
	cookie := digest(string(auth.cookie))
	if auth.operator || !api.ValidShareID(string(auth.cookie)) || !api.ValidShareID(string(auth.csrf)) {
		return nil, cookie, fault(api.SharePermissionDenied)
	}
	key := guestKey{share: id, cookie: cookie}
	guest, ok := s.guests[key]
	if !ok {
		guest, ok = s.terminalGuests[key]
	}
	if !ok || subtle.ConstantTimeCompare([]byte(auth.csrf), []byte(guest.csrf)) != 1 {
		return nil, cookie, fault(api.SharePermissionDenied)
	}
	sh, err := s.active(id)
	return sh, cookie, err
}

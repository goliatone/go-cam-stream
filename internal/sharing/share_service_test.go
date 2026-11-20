package sharing

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/goliatone/go-cam-stream/data/api"
)

type localIngress struct{}

func (localIngress) Start(context.Context) (IngressBinding, error) {
	return IngressBinding{Origin: "https://share.example.test", Audience: api.SharePublicAudience}, nil
}
func (localIngress) Stop(context.Context) error  { return nil }
func (localIngress) Check(context.Context) error { return nil }

type testMedia struct {
	opened atomic.Int32
	closed atomic.Int32
}

func (m *testMedia) Open(_ context.Context, offer MediaOffer) (MediaPeer, error) {
	m.opened.Add(1)
	return MediaPeer{ID: offer.Attempt.AttemptID, Answer: api.SDPAnswer{Type: "answer", SDP: "v=0\r\n"}}, nil
}
func (m *testMedia) Close(_ context.Context, _ string) error {
	m.closed.Add(1)
	return nil
}

func newSharingTest(t *testing.T, media Media) (*Service, *atomic.Int64) {
	t.Helper()
	tokens, err := NewInvitationTokens([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	clock := new(atomic.Int64)
	clock.Store(time.Now().Unix())
	s, err := NewService(Options{Clock: func() time.Time { return time.Unix(clock.Load(), 0) },
		Source: func(_ context.Context, id string) (string, error) {
			if id == "camera" {
				return "source", nil
			}
			return "", fault(api.ShareCameraUnavailable)
		},
		Tokens: tokens, Ingress: localIngress{}, Media: media})
	if err != nil {
		t.Fatal(err)
	}
	return s, clock
}

func TestSharingDispatchAndLifecycle(t *testing.T) {
	media := &testMedia{}
	s, clock := newSharingTest(t, media)
	commands := NewCommands(s)
	t.Cleanup(commands.Close)
	ctx := WithOperator(context.Background())
	request := CreateShare{CreateShareRequest: api.CreateShareRequest{CameraID: "camera"}}
	if _, err := Dispatch[api.CreateShareResult](context.Background(), commands, request); err == nil {
		t.Fatal("non-operator created a share")
	}
	created, err := Dispatch[api.CreateShareResult](ctx, commands, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Share.ExpiresAt.Unix() != clock.Load()+3600 {
		t.Fatal("wrong deadline")
	}
	if _, err = s.Create(ctx, request); err != fault(api.ShareAlreadyActive) {
		t.Fatal("duplicate camera share")
	}
	ref := api.ShareRef{ShareID: created.Share.ShareID}
	u, err := url.Parse(created.Link.URL)
	if err != nil || u.RawQuery != "" || u.Fragment == "" {
		t.Fatal("invitation secret was not a fragment")
	}
	redeem := RedeemShare{ShareRef: ref, RedeemShareRequest: api.RedeemShareRequest{Invitation: u.Fragment}}
	first, err := Dispatch[RedeemedGuest](context.Background(), commands, redeem)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Redeem(WithGuest(context.Background(), first.Cookie, ""), redeem)
	if err != nil || again.Result.CSRFToken == first.Result.CSRFToken || again.Cookie == first.Cookie {
		t.Fatal("redemption did not isolate the second tab")
	}
	guest := WithGuest(context.Background(), first.Cookie, Secret(first.Result.CSRFToken))
	if _, err = s.AdmitViewer(WithGuest(context.Background(), first.Cookie, "wrong"), AdmitShareViewer{ShareRef: ref}); err == nil {
		t.Fatal("admission omitted CSRF")
	}
	var wg sync.WaitGroup
	success := make(chan api.AdmitShareViewerResult, 8)
	for range 8 {
		wg.Go(func() {
			if out, e := s.AdmitViewer(guest, AdmitShareViewer{ShareRef: ref}); e == nil {
				success <- out
			}
		})
	}
	wg.Wait()
	close(success)
	if len(success) != 1 {
		t.Fatalf("capacity race admitted %d viewers", len(success))
	}
	admitted := <-success
	viewer := ShareViewerRef{ShareRef: ref, ViewerID: admitted.ViewerID}
	old := api.ShareViewerAttempt{AttemptID: admitted.AttemptID}
	offer := api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}
	if _, err = s.OpenViewer(guest, OpenShareViewer{ShareViewerRef: viewer, OpenShareViewerRequest: api.OpenShareViewerRequest{ShareViewerAttempt: old, Offer: offer}}); err != nil {
		t.Fatal(err)
	}
	s.TransportLost(ViewerAttempt{ShareID: ref.ShareID, ViewerID: admitted.ViewerID, AttemptID: admitted.AttemptID})
	clock.Add(5)
	retry, err := s.AdmitViewer(guest, AdmitShareViewer{ShareRef: ref, AdmitShareViewerRequest: api.AdmitShareViewerRequest{ViewerID: admitted.ViewerID}})
	if err != nil || retry.AttemptID == old.AttemptID || !admitted.ExpiresAt.Equal(retry.ExpiresAt) || media.closed.Load() != 1 {
		t.Fatal("reconnect lost scope or failed to retire previous peer")
	}
	if err = s.CloseViewer(guest, CloseShareViewer{ShareViewerRef: viewer, ShareViewerAttempt: old}); err != nil {
		t.Fatal(err)
	}
	next := api.ShareViewerAttempt{AttemptID: retry.AttemptID}
	if _, err = s.OpenViewer(guest, OpenShareViewer{ShareViewerRef: viewer, OpenShareViewerRequest: api.OpenShareViewerRequest{ShareViewerAttempt: next, Offer: offer}}); err != nil {
		t.Fatal(err)
	}
	s.MediaLive(ViewerAttempt{ShareID: ref.ShareID, ViewerID: viewer.ViewerID, AttemptID: retry.AttemptID})
	clock.Add(30)
	if err := s.Sweep(ctx); err != nil || media.closed.Load() != 1 {
		t.Fatal("replacement media that went live was evicted at the old reconnect deadline")
	}
	clock.Store(created.Share.ExpiresAt.Unix())
	if _, err = s.AdmitViewer(guest, AdmitShareViewer{ShareRef: ref}); err != fault(api.ShareExpiryReached) {
		t.Fatal("expiry failed to deny admission")
	}
	if err = s.Sweep(ctx); err != nil || media.closed.Load() != 2 {
		t.Fatal("expiry failed to close the active peer")
	}
	if _, err = s.Redeem(context.Background(), redeem); err != fault(api.ShareExpiryReached) {
		t.Fatal("expired token replayed")
	}
	fresh, err := s.Create(ctx, request)
	if err != nil || fresh.Share.ShareID == ref.ShareID {
		t.Fatal("new share reused a generation")
	}
}

func TestReconnectCannotExtendKeptPlace(t *testing.T) {
	media := &testMedia{}
	s, clock := newSharingTest(t, media)
	ctx := WithOperator(t.Context())
	created, err := s.Create(ctx, CreateShare{CreateShareRequest: api.CreateShareRequest{CameraID: "camera"}})
	if err != nil {
		t.Fatal(err)
	}
	ref := api.ShareRef{ShareID: created.Share.ShareID}
	link, err := url.Parse(created.Link.URL)
	if err != nil {
		t.Fatal(err)
	}
	redeemed, err := s.Redeem(t.Context(), RedeemShare{ShareRef: ref, RedeemShareRequest: api.RedeemShareRequest{Invitation: link.Fragment}})
	if err != nil {
		t.Fatal(err)
	}
	guest := WithGuest(t.Context(), redeemed.Cookie, Secret(redeemed.Result.CSRFToken))
	first, err := s.AdmitViewer(guest, AdmitShareViewer{ShareRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	viewer := ShareViewerRef{ShareRef: ref, ViewerID: first.ViewerID}
	offer := api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}
	open := func(attempt string) error {
		_, err := s.OpenViewer(guest, OpenShareViewer{ShareViewerRef: viewer, OpenShareViewerRequest: api.OpenShareViewerRequest{
			ShareViewerAttempt: api.ShareViewerAttempt{AttemptID: attempt}, Offer: offer}})
		return err
	}
	if err := open(first.AttemptID); err != nil {
		t.Fatal(err)
	}
	s.TransportLost(ViewerAttempt{ShareID: ref.ShareID, ViewerID: first.ViewerID, AttemptID: first.AttemptID})
	clock.Add(29)
	retry, err := s.AdmitViewer(guest, AdmitShareViewer{ShareRef: ref, AdmitShareViewerRequest: api.AdmitShareViewerRequest{ViewerID: first.ViewerID}})
	if err != nil {
		t.Fatal(err)
	}
	clock.Add(1)
	if err := open(retry.AttemptID); err != fault(api.ShareStaleAttempt) {
		t.Fatalf("replacement offer accepted at the reconnect deadline: %v", err)
	}
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if fresh, err := s.AdmitViewer(guest, AdmitShareViewer{ShareRef: ref}); err != nil || fresh.ViewerID == first.ViewerID {
		t.Fatalf("expired reconnect held the only slot: %v", err)
	}
}

func TestInvitationRejectsInvalidSignedClaims(t *testing.T) {
	key := strings.Repeat("k", 32)
	tokens, err := NewInvitationTokens([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	dat := map[string]any{"v": 1, "purpose": InvitationPurpose, "share_id": handle(), "nonce": handle()}
	now := time.Now()
	for _, tc := range []struct {
		method jwt.SigningMethod
		claims jwt.MapClaims
	}{
		{jwt.SigningMethodHS256, jwt.MapClaims{"dat": dat, "iat": now.Unix()}},
		{jwt.SigningMethodHS256, jwt.MapClaims{"dat": dat, "iat": now.Unix(), "exp": float64(now.Add(time.Hour).Unix()) + .5}},
		{jwt.SigningMethodHS384, jwt.MapClaims{"dat": dat, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}},
	} {
		signed, e := jwt.NewWithClaims(tc.method, tc.claims).SignedString([]byte(key))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tokens.Verify(Secret(signed)); e == nil {
			t.Fatal("invalid signed claim accepted")
		}
	}
}

type slowPeer struct {
	*testMedia
	entered chan struct{}
}

func (m slowPeer) Open(ctx context.Context, offer MediaOffer) (MediaPeer, error) {
	close(m.entered)
	<-ctx.Done()
	return MediaPeer{ID: offer.Attempt.AttemptID}, nil
}

func TestRevokeRetiresPeerReturnedAfterCancellation(t *testing.T) {
	media := slowPeer{testMedia: &testMedia{}, entered: make(chan struct{})}
	s, _ := newSharingTest(t, media)
	ctx := WithOperator(context.Background())
	created, err := s.Create(ctx, CreateShare{CreateShareRequest: api.CreateShareRequest{CameraID: "camera"}})
	if err != nil {
		t.Fatal(err)
	}
	ref := api.ShareRef{ShareID: created.Share.ShareID}
	link, err := url.Parse(created.Link.URL)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Redeem(context.Background(), RedeemShare{ShareRef: ref, RedeemShareRequest: api.RedeemShareRequest{Invitation: link.Fragment}})
	if err != nil {
		t.Fatal(err)
	}
	guest := WithGuest(context.Background(), first.Cookie, Secret(first.Result.CSRFToken))
	admitted, err := s.AdmitViewer(guest, AdmitShareViewer{ShareRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		_, err := s.OpenViewer(guest, OpenShareViewer{ShareViewerRef: ShareViewerRef{ShareRef: ref, ViewerID: admitted.ViewerID},
			OpenShareViewerRequest: api.OpenShareViewerRequest{ShareViewerAttempt: api.ShareViewerAttempt{AttemptID: admitted.AttemptID}, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}}})
		opened <- err
	}()
	select {
	case <-media.entered:
	case <-time.After(time.Second):
		t.Fatal("media did not enter negotiation")
	}
	if _, err = s.Revoke(ctx, RevokeShare{ShareRef: ref}); err != nil {
		t.Fatal(err)
	}
	if err = <-opened; err == nil || media.closed.Load() != 1 {
		t.Fatal("revocation failed to retire late media")
	}
	if _, err = s.AdmitViewer(guest, AdmitShareViewer{ShareRef: ref}); err != fault(api.ShareAccessRevoked) {
		t.Fatal("revoked guest did not receive terminal state")
	}
	unknown := WithGuest(context.Background(), Secret(handle()), Secret(handle()))
	if _, err = s.AdmitViewer(unknown, AdmitShareViewer{ShareRef: ref}); err != fault(api.SharePermissionDenied) {
		t.Fatal("unknown guest obtained terminal state")
	}
}

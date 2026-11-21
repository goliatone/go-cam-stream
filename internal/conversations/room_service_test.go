package conversations

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

func TestIdleSweepExpiresRoomWithoutParticipantTraffic(t *testing.T) {
	now := time.Now()
	service, err := NewService(Options{PublicOrigin: "https://call.example.test", Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	room, err := service.Create(WithOwner(t.Context()), CreateRoom{})
	if err != nil || !service.HasLiveRooms() {
		t.Fatalf("initial room was not live: %v", err)
	}
	now = room.Admission.Room.ExpiresAt
	if service.HasLiveRooms() || service.TerminalState(room.Admission.Room.RoomRef) != api.RoomExpired {
		t.Fatal("an unpolled room did not become terminal after its expiry")
	}
}

func TestTwoSlotsAndGenerationLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	s, err := NewService(Options{PublicOrigin: "https://call.example.test", Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	bus := NewCommands(s)
	defer bus.Close()
	owner := WithOwner(t.Context())
	if _, err := Dispatch[api.CreatedRoom](t.Context(), bus, CreateRoom{}); err != Fault(api.RoomPermissionDenied) {
		t.Fatal("guest created a room")
	}
	created, err := Dispatch[api.CreatedRoom](owner, bus, CreateRoom{})
	if err != nil {
		t.Fatal(err)
	}
	ref := created.Admission.Room.RoomRef
	link, err := url.Parse(created.InvitationURL)
	if err != nil {
		t.Fatal(err)
	}
	invite := RedeemRoom{RoomRef: ref, RoomRedeemRequest: api.RoomRedeemRequest{Version: 1, Invitation: link.Fragment}}
	var attempts sync.WaitGroup
	accepted := make(chan api.RoomAdmission, 8)
	for range 8 {
		attempts.Go(func() {
			admission, err := s.Redeem(t.Context(), invite)
			if err == nil {
				accepted <- admission
			} else if err != Fault(api.RoomFull) {
				t.Error("unexpected admission error", err)
			}
		})
	}
	attempts.Wait()
	close(accepted)
	if len(accepted) != 1 {
		t.Fatal("room accepted multiple guests")
	}
	guest := WithParticipant(t.Context(), (<-accepted).Token)
	host := WithParticipant(t.Context(), created.Admission.Token)
	if _, err := s.End(guest, EndRoom{ref}); err != Fault(api.RoomPermissionDenied) {
		t.Fatal("guest could end the room")
	}
	foreign, err := s.Create(owner, CreateRoom{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Poll(guest, foreign.Admission.Room.RoomRef); err != Fault(api.RoomPermissionDenied) {
		t.Fatal("guest read a different room")
	}
	gen := created.Admission.Room.Generation
	join := JoinRoom{RoomRef: ref, RoomJoinRequest: api.RoomJoinRequest{Generation: gen, Publication: api.RoomPublication{Camera: true}}}
	offer := PublishRoomSignal{RoomRef: ref, RoomSignal: api.RoomSignal{Generation: gen, Type: "offer", SDP: "v=0\r\n"}}
	if _, err := s.PublishSignal(host, offer); err != Fault(api.RoomPermissionDenied) {
		t.Fatal("offer accepted before Join")
	}
	for _, peer := range []context.Context{host, guest} {
		if _, err := Dispatch[api.JoinedRoom](peer, bus, join); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PublishSignal(guest, offer); err != Fault(api.RoomPermissionDenied) {
		t.Fatal("guest replaced the host offer")
	}
	if _, err := s.PublishSignal(host, offer); err != nil {
		t.Fatal(err)
	}
	poll, err := s.Poll(guest, ref)
	if err != nil || poll.Signal == nil || poll.Signal.Type != "offer" || poll.Room.State == api.RoomConnected {
		t.Fatal("room published the wrong offer or reported media prematurely")
	}
	answer := offer
	answer.RoomSignal.Type = "answer"
	if _, err := s.PublishSignal(guest, answer); err != nil {
		t.Fatal(err)
	}
	for _, peer := range []context.Context{host, guest} {
		if _, err := s.SetConnection(peer, SetRoomConnection{RoomRef: ref, RoomConnectionRequest: api.RoomConnectionRequest{Generation: gen, Connected: true}}); err != nil {
			t.Fatal(err)
		}
	}
	poll, err = s.Poll(host, ref)
	if err != nil || poll.Room.State != api.RoomConnected || poll.Signal == nil || poll.Signal.Type != "answer" {
		t.Fatal("host did not observe the confirmed connection and answer")
	}
	for range 5 {
		now = now.Add(9 * time.Second)
		if _, err := s.Poll(host, ref); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Join(guest, join); err != Fault(api.RoomPermissionDenied) {
		t.Fatal("expired guest reservation resumed")
	}
	if _, err := s.PublishSignal(host, offer); err != Fault(api.RoomStaleGeneration) {
		t.Fatal("old media generation remained valid")
	}
	if replacement, err := s.Redeem(t.Context(), invite); err != nil || replacement.Room.Generation <= gen {
		t.Fatal("guest slot did not release and rotate its generation")
	}
	for range 2 {
		if ended, err := s.End(host, EndRoom{ref}); err != nil || ended.State != api.RoomEnded {
			t.Fatal("host could not end the room idempotently")
		}
	}
	if _, err := s.Redeem(t.Context(), invite); err != Fault(api.RoomAccessEnded) {
		t.Fatal("ended invite replayed")
	}
}

func TestJoinRejectsProviderResultAfterExpiry(t *testing.T) {
	now := time.Now()
	relay := pausedRelay{entered: make(chan struct{}), proceed: make(chan struct{})}
	s, err := NewService(Options{PublicOrigin: "https://call.example.test", Clock: func() time.Time { return now }, Relay: relay})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.Create(WithOwner(t.Context()), CreateRoom{})
	if err != nil {
		t.Fatal(err)
	}
	host := WithParticipant(t.Context(), created.Admission.Token)
	result := make(chan error, 1)
	go func() {
		_, err := s.Join(host, JoinRoom{RoomRef: created.Admission.Room.RoomRef,
			RoomJoinRequest: api.RoomJoinRequest{Generation: 1}})
		result <- err
	}()
	<-relay.entered
	now = created.Admission.Room.ExpiresAt
	close(relay.proceed)
	if err := <-result; err != Fault(api.RoomAccessExpired) {
		t.Fatal("expired room accepted relay result", err)
	}
}

func TestConversationPrejoinDoesNotRequireMediaHeartbeat(t *testing.T) {
	now := time.Now()
	s, err := NewService(Options{PublicOrigin: "https://call.example.test", Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	owner := WithOwner(t.Context())
	created, err := s.Create(owner, CreateRoom{})
	if err != nil {
		t.Fatal(err)
	}
	ref := created.Admission.Room.RoomRef
	now = now.Add(api.ConversationHeartbeat + api.ConversationReconnect + time.Second)
	if room, err := s.OwnerStatus(owner, ref); err != nil || room.State != api.RoomWaiting {
		t.Fatal("host chooser expired before Join")
	}
	link, err := url.Parse(created.InvitationURL)
	if err != nil {
		t.Fatal(err)
	}
	invite := RedeemRoom{RoomRef: ref, RoomRedeemRequest: api.RoomRedeemRequest{Version: 1, Invitation: link.Fragment}}
	admission, err := s.Redeem(t.Context(), invite)
	if err != nil {
		t.Fatal(err)
	}
	guest := WithParticipant(t.Context(), admission.Token)
	now = now.Add(api.ConversationHeartbeat + time.Second)
	if poll, err := s.Poll(guest, ref); err != nil || poll.Room.Generation != 1 || poll.Room.State != api.RoomJoining {
		t.Fatal("prejoin heartbeat resumed through a media generation reset")
	}
	now = now.Add(api.ConversationReconnect)
	join := JoinRoom{RoomRef: ref, RoomJoinRequest: api.RoomJoinRequest{Generation: 1}}
	if _, err := s.Join(guest, join); err != nil {
		t.Fatal("returning prejoin lost its seat", err)
	}
	host := WithParticipant(t.Context(), created.Admission.Token)
	if _, err := s.Join(host, join); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Leave(guest, LeaveRoom{ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(t.Context(), invite); err != nil {
		t.Fatal("guest Leave did not free slot", err)
	}
	if _, err := s.Leave(guest, LeaveRoom{ref}); err != Fault(api.RoomPermissionDenied) {
		t.Fatal("released guest could revoke replacement")
	}
	now = now.Add(api.ConversationHeartbeat + api.ConversationReconnect + time.Second)
	if room, err := s.OwnerStatus(owner, ref); err != nil || room.State != api.RoomEnded {
		t.Fatal("host heartbeat loss did not end a joined room")
	}
	abandoned, err := s.Create(owner, CreateRoom{})
	if err != nil {
		t.Fatal(err)
	}
	absent := WithParticipant(t.Context(), abandoned.Admission.Token)
	if _, err := s.HostView(absent, abandoned.Admission.Room.RoomRef); err != nil {
		t.Fatal(err)
	}
	now = now.Add(api.ConversationHeartbeat + api.ConversationReconnect + time.Second)
	if room, err := s.OwnerStatus(owner, abandoned.Admission.Room.RoomRef); err != nil || room.State != api.RoomEnded {
		t.Fatal("an abandoned browser prejoin held the room open")
	}
}

type pausedRelay struct{ entered, proceed chan struct{} }

func (r pausedRelay) Issue(ctx context.Context, _ api.RoomRef, _ api.RoomGeneration, _ time.Time) (RoomRelay, error) {
	close(r.entered)
	select {
	case <-r.proceed:
		return RoomRelay{}, nil
	case <-ctx.Done():
		return RoomRelay{}, ctx.Err()
	}
}

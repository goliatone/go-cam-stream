package conversations

import (
	"context"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/sharing"
)

// SharedRelay adopts the existing provider contract while each room retains
// its own generation, lifetime and authorization policy.
type SharedRelay struct {
	Provider sharing.RelayIssuer
}

func (r SharedRelay) Issue(ctx context.Context, room api.RoomRef, _ api.RoomGeneration, expires time.Time) (RoomRelay, error) {
	grant := sharing.RelayGrant{
		Attempt:  sharing.ViewerAttempt{ShareID: room.RoomID, ViewerID: handle(), AttemptID: handle()},
		NotAfter: expires,
	}
	relay, err := r.Provider.Issue(ctx, grant)
	if err != nil {
		return RoomRelay{}, err
	}
	now := time.Now()
	host, guest, err := relay.ForAttempt(grant, now)
	if err != nil {
		return RoomRelay{}, err
	}
	servers, err := (sharing.MediaOffer{Attempt: grant.Attempt, Relay: &host}).HostICE(now)
	if err != nil {
		return RoomRelay{}, err
	}
	return RoomRelay{Host: servers, Guest: guest}, nil
}

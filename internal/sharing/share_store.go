package sharing

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

type shareState struct {
	view     api.ShareView
	source   string
	link     api.ShareLink
	deadline time.Time
	claims   InvitationClaims
	digest   [sha256.Size]byte
	viewers  map[string]*viewerState
}

type guestState struct {
	csrf     Secret
	deadline time.Time
}

type terminalShare struct {
	view   api.ShareView
	digest [sha256.Size]byte
}

type guestKey struct {
	share  string
	cookie [sha256.Size]byte
}

type viewerState struct {
	scope       ViewerAttempt
	cookie      [sha256.Size]byte
	negotiation time.Time
	reconnect   time.Time
	relay       *HostRelay
	peer        string
	cancel      context.CancelFunc
	done        chan struct{}
	ready       bool
	live        bool
}

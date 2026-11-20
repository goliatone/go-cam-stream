package sharing

import (
	"errors"
	"slices"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

var ErrInvalidRelayCredentials = errors.New("invalid sharing relay credentials")

// ViewerAttempt is resolved from service-owned admission state.
type ViewerAttempt struct {
	ShareID   string
	ViewerID  string
	AttemptID string
}

func (a ViewerAttempt) valid() bool {
	return api.ValidShareID(a.ShareID) && api.ValidShareID(a.ViewerID) && api.ValidShareID(a.AttemptID)
}

type RelayGrant struct {
	Attempt  ViewerAttempt
	NotAfter time.Time
}

// RelayCredentials keeps the two endpoints' material separate. Only the guest
// projection may be copied into the admission response.
type RelayCredentials struct {
	host  HostRelay
	guest []api.ShareICECredentials
}

func (RelayCredentials) String() string   { return "[relay credentials redacted]" }
func (RelayCredentials) GoString() string { return "[relay credentials redacted]" }

// HostRelay supplies private ICE credentials to the media adapter through
// MediaOffer.HostICE. It never appears in the browser response.
type HostRelay struct {
	grant   RelayGrant
	servers []api.ShareICECredentials
}

func (HostRelay) String() string   { return "[host relay redacted]" }
func (HostRelay) GoString() string { return "[host relay redacted]" }

func NewRelayCredentials(grant RelayGrant, now time.Time, host, guest []api.ShareICECredentials) (RelayCredentials, error) {
	if !grant.Attempt.valid() || !validRelayServers(host, grant, now) || !validRelayServers(guest, grant, now) {
		return RelayCredentials{}, ErrInvalidRelayCredentials
	}
	return RelayCredentials{
		host:  HostRelay{grant: grant, servers: cloneRelayServers(host)},
		guest: cloneRelayServers(guest),
	}, nil
}

// ForAttempt checks the provider result against the current admitted grant.
// Keep the host value with that attempt and send only the guest slice to Play.
func (r RelayCredentials) ForAttempt(grant RelayGrant, now time.Time) (HostRelay, []api.ShareICECredentials, error) {
	if r.host.grant.Attempt != grant.Attempt || !r.host.grant.NotAfter.Equal(grant.NotAfter) ||
		!validRelayServers(r.host.servers, grant, now) || !validRelayServers(r.guest, grant, now) {
		return HostRelay{}, nil, ErrInvalidRelayCredentials
	}
	return r.host, cloneRelayServers(r.guest), nil
}

// HostICE checks the binding and expiry again after browser ICE gathering.
// A nil Relay selects direct playback. Invalid relay data fails closed.
func (o MediaOffer) HostICE(now time.Time) ([]api.ShareICECredentials, error) {
	if !o.Attempt.valid() {
		return nil, ErrInvalidRelayCredentials
	}
	if o.Relay == nil {
		return nil, nil
	}
	if o.Relay.grant.Attempt != o.Attempt || !validRelayServers(o.Relay.servers, o.Relay.grant, now) {
		return nil, ErrInvalidRelayCredentials
	}
	return cloneRelayServers(o.Relay.servers), nil
}

func validRelayServers(servers []api.ShareICECredentials, grant RelayGrant, now time.Time) bool {
	if len(servers) == 0 || !grant.NotAfter.After(now) {
		return false
	}
	for _, server := range servers {
		if len(server.URLs) == 0 || server.Username == "" || server.Credential == "" ||
			!server.ExpiresAt.After(now) || server.ExpiresAt.After(grant.NotAfter) {
			return false
		}
	}
	return true
}

func cloneRelayServers(servers []api.ShareICECredentials) []api.ShareICECredentials {
	cloned := slices.Clone(servers)
	for index := range cloned {
		cloned[index].URLs = slices.Clone(cloned[index].URLs)
	}
	return cloned
}

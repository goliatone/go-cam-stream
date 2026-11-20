package sharing_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/sharing"
)

func TestRelayCredentialsStayWithTheirAttempt(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	attempt := sharing.ViewerAttempt{ShareID: strings.Repeat("A", 43), ViewerID: strings.Repeat("E", 43), AttemptID: strings.Repeat("I", 43)}
	grant := sharing.RelayGrant{Attempt: attempt, NotAfter: now.Add(time.Hour)}
	expires := now.Add(time.Minute)
	host := testRelayServers("host-only-secret", expires)
	guest := testRelayServers("guest-secret", expires)
	issued, err := sharing.NewRelayCredentials(grant, now, host, guest)
	if err != nil {
		t.Fatal(err)
	}
	host[0].URLs[0] = "turn:changed.example.test"
	guest[0].Credential = "changed-guest-secret"
	bound, browserICE, err := issued.ForAttempt(grant, now)
	if err != nil {
		t.Fatal(err)
	}
	offer := sharing.MediaOffer{SourceID: "camera", Attempt: attempt, Relay: &bound}
	hostICE, err := offer.HostICE(now)
	if err != nil || len(hostICE) != 1 || hostICE[0].URLs[0] != "turn:relay.example.test" || hostICE[0].Credential != "host-only-secret" {
		t.Fatal("wrong host credential bound to the offer")
	}
	if len(browserICE) != 1 || browserICE[0].Credential != "guest-secret" {
		t.Fatal("wrong guest credential bound to the admission")
	}
	admission, err := json.Marshal(api.AdmitShareViewerResult{ICE: browserICE})
	if err != nil || !strings.Contains(string(admission), "guest-secret") || strings.Contains(string(admission), "host-only-secret") {
		t.Fatal("admission must only serialize the guest credential")
	}
	if dump := fmt.Sprintf("%+v %#v %#v", issued, bound, offer); strings.Contains(dump, "secret") {
		t.Fatal("relay material escaped redaction")
	}
	hostICE[0].URLs[0] = "turn:another.example.test"
	browserICE[0].Credential = "another-guest-secret"
	hostICE, err = offer.HostICE(now)
	if err != nil || hostICE[0].URLs[0] != "turn:relay.example.test" {
		t.Fatal("media adapter changed the stored host binding")
	}
	_, browserICE, err = issued.ForAttempt(grant, now)
	if err != nil || browserICE[0].Credential != "guest-secret" {
		t.Fatal("guest projection changed the stored credentials")
	}
	for _, other := range []sharing.ViewerAttempt{
		{ShareID: strings.Repeat("M", 43), ViewerID: attempt.ViewerID, AttemptID: attempt.AttemptID},
		{ShareID: attempt.ShareID, ViewerID: strings.Repeat("M", 43), AttemptID: attempt.AttemptID},
		{ShareID: attempt.ShareID, ViewerID: attempt.ViewerID, AttemptID: strings.Repeat("M", 43)},
	} {
		if _, _, err = issued.ForAttempt(sharing.RelayGrant{Attempt: other, NotAfter: grant.NotAfter}, now); err == nil {
			t.Fatal("admission reused credentials from another scope")
		}
		offer.Attempt = other
		if _, err = offer.HostICE(now); err == nil {
			t.Fatal("media offer reused credentials from another scope")
		}
	}
	offer.Attempt = attempt
	if _, err = offer.HostICE(expires); err == nil {
		t.Fatal("media offer used an expired credential after ICE gathering")
	}
	offer.Relay = nil
	if direct, err := offer.HostICE(now); err != nil || len(direct) != 0 {
		t.Fatal("explicit direct playback should require no relay material")
	}
}

func TestRelayCredentialsRequireBothEndpointsWithinDeadline(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	attempt := sharing.ViewerAttempt{ShareID: strings.Repeat("A", 43), ViewerID: strings.Repeat("E", 43), AttemptID: strings.Repeat("I", 43)}
	grant := sharing.RelayGrant{Attempt: attempt, NotAfter: now.Add(time.Hour)}
	valid := testRelayServers("issued", grant.NotAfter)
	tooLong := testRelayServers("too-long", grant.NotAfter.Add(time.Second))
	expired := testRelayServers("expired", now)
	for _, tc := range []struct{ host, guest []api.ShareICECredentials }{
		{nil, valid}, {valid, nil}, {tooLong, valid}, {valid, tooLong}, {expired, valid}, {valid, expired},
	} {
		if _, err := sharing.NewRelayCredentials(grant, now, tc.host, tc.guest); err == nil {
			t.Fatal("incomplete or unbounded relay material was accepted")
		}
	}
	issued, err := sharing.NewRelayCredentials(grant, now, valid, valid)
	if err != nil {
		t.Fatal(err)
	}
	grant.NotAfter = grant.NotAfter.Add(time.Second)
	if _, _, err = issued.ForAttempt(grant, now); err == nil {
		t.Fatal("issuance reused after the grant deadline changed")
	}
}

func testRelayServers(credential string, expires time.Time) []api.ShareICECredentials {
	return []api.ShareICECredentials{{URLs: []string{"turn:relay.example.test"}, Username: "fixture-user", Credential: credential, ExpiresAt: expires}}
}

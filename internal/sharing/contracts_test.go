package sharing_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/sharing"
	"github.com/goliatone/go-command"
	"github.com/goliatone/go-urlkit/securelink"
)

func TestSharingCommandIdentitiesAndShape(t *testing.T) {
	ref := api.ShareRef{ShareID: strings.Repeat("A", 43)}
	viewer := sharing.ShareViewerRef{ShareRef: ref, ViewerID: strings.Repeat("A", 43)}
	attempt := api.ShareViewerAttempt{AttemptID: strings.Repeat("A", 43)}
	offer := api.OpenShareViewerRequest{ShareViewerAttempt: attempt, Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}}
	commands := map[string]command.Message{
		"sharing.create.v1":       sharing.CreateShare{CreateShareRequest: api.CreateShareRequest{CameraID: "camera"}},
		"sharing.copy_link.v1":    sharing.CopyShareLink{ShareRef: ref},
		"sharing.revoke.v1":       sharing.RevokeShare{ShareRef: ref},
		"sharing.redeem.v1":       sharing.RedeemShare{ShareRef: ref, RedeemShareRequest: api.RedeemShareRequest{Invitation: "abc.def.ghi"}},
		"sharing.admit_viewer.v1": sharing.AdmitShareViewer{ShareRef: ref},
		"sharing.open_viewer.v1":  sharing.OpenShareViewer{ShareViewerRef: viewer, OpenShareViewerRequest: offer},
		"sharing.close_viewer.v1": sharing.CloseShareViewer{ShareViewerRef: viewer, ShareViewerAttempt: attempt},
	}
	for name, message := range commands {
		if message.Type() != name || command.ValidateMessage(message) != nil {
			t.Fatalf("command contract mismatch: %s", name)
		}
	}
	if err := command.ValidateMessage(sharing.CloseShareViewer{ShareViewerRef: viewer}); err == nil {
		t.Fatal("attempt-less close accepted")
	}
}

func TestPinnedSecurelinkContract(t *testing.T) {
	manager, err := securelink.NewManager(securelink.Config{
		SigningKey: strings.Repeat("k", 32), Expiration: time.Hour,
		BaseURL: "https://view.example.test", QueryKey: "token", AsQuery: true,
		Routes: map[string]string{"share": "/s/" + strings.Repeat("A", 43)}, SigningMethod: jwt.SigningMethodHS256,
	})
	if err != nil {
		t.Fatal(err)
	}
	link, err := manager.Generate("share", securelink.Payload{
		"v": sharing.InvitationVersion, "purpose": sharing.InvitationPurpose,
		"share_id": strings.Repeat("A", 43), "nonce": strings.Repeat("A", 43),
	})
	if err != nil {
		t.Fatal("pinned securelink generation failed")
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal("pinned securelink returned an invalid URL")
	}
	token := parsed.Query().Get("token")
	if err = (api.RedeemShareRequest{Invitation: token}).Validate(); err != nil {
		t.Fatal("pinned securelink output does not fit the exchange envelope")
	}
	claims, err := manager.Validate(token)
	if err != nil || claims["purpose"] != sharing.InvitationPurpose || claims["v"] != float64(sharing.InvitationVersion) {
		t.Fatal("pinned securelink dat claim shape changed")
	}
	encoded, err := json.Marshal(sharing.RedeemedGuest{Cookie: sharing.Secret("private-cookie")})
	if err != nil || strings.Contains(string(encoded), "private-cookie") {
		t.Fatal("cookie would appear in a JSON command result")
	}
}

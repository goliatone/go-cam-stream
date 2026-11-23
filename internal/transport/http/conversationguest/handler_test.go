package conversationguest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/conversations"
)

func TestRoomHTTPUsesScopedTypedCommands(t *testing.T) {
	const origin = "https://call.example.test"
	s, err := conversations.NewService(conversations.Options{PublicOrigin: origin})
	if err != nil {
		t.Fatal(err)
	}
	bus := conversations.NewCommands(s)
	defer bus.Close()
	handler, err := New(origin, s, bus)
	if err != nil {
		t.Fatal(err)
	}
	created, err := conversations.Dispatch[api.CreatedRoom](conversations.WithOwner(t.Context()), bus, conversations.CreateRoom{})
	if err != nil {
		t.Fatal(err)
	}
	address, err := url.Parse(created.InvitationURL)
	if err != nil {
		t.Fatal(err)
	}
	route := origin + api.ConversationAPIPrefix + created.Admission.Room.RoomID
	request := func(method, path, source, token, body string, status int) []byte {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), method, route+path, strings.NewReader(body))
		r.Header.Set("Origin", source)
		r.Header.Set("Authorization", token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("unexpected %s %s reply: %d", method, path, w.Code)
		}
		return w.Body.Bytes()
	}
	body, err := json.Marshal(api.RoomRedeemRequest{Version: 1, Invitation: address.Fragment})
	if err != nil {
		t.Fatal(err)
	}
	request("POST", "/redeem", "https://elsewhere.test", "", string(body), http.StatusForbidden)
	raw := request("POST", "/redeem", origin, "", string(body), http.StatusOK)
	var guest api.RoomAdmission
	if err := json.Unmarshal(raw, &guest); err != nil || guest.Role != api.RoomGuest {
		t.Fatal("no guest projection")
	}
	request("GET", "", "", guest.Token, "", http.StatusForbidden)
	request("GET", "", "", "Bearer "+guest.Token, "", http.StatusOK)
	request("GET", "/host", "", "Bearer "+guest.Token, "", http.StatusForbidden)
	request("GET", "/host", "", "Bearer "+created.Admission.Token, "", http.StatusOK)
	request("GET", "/host", "", "Bearer "+created.Admission.Token, "", http.StatusForbidden)
	request("POST", "/browser-stopped", origin, "Bearer "+guest.Token, "", http.StatusForbidden)
	request("POST", "/end", origin, "Bearer "+guest.Token, "", http.StatusForbidden)
	request("POST", "/leave", origin, "Bearer "+guest.Token, `{"generation":999}`, http.StatusOK)
	request("POST", "/redeem", origin, "", string(body), http.StatusOK)
	request("POST", "/leave", origin, "Bearer "+guest.Token, `{}`, http.StatusForbidden)
	owner := conversations.WithOwner(t.Context())
	if _, err := s.End(owner, conversations.EndRoom{RoomRef: created.Admission.Room.RoomRef}); err != nil {
		t.Fatal(err)
	}
	status, err := s.OwnerStatus(owner, created.Admission.Room.RoomRef)
	if err != nil || status.BrowserCapturePending == nil || !*status.BrowserCapturePending {
		t.Fatal("End released the browser's capture reservation")
	}
	request("POST", "/browser-stopped", origin, "Bearer "+created.Admission.Token, "", http.StatusOK)
	status, err = s.OwnerStatus(owner, created.Admission.Room.RoomRef)
	if err != nil || status.BrowserCapturePending == nil || *status.BrowserCapturePending {
		t.Fatal("authenticated browser stop did not clear the reservation")
	}
	request("POST", "/browser-stopped", origin, "Bearer "+created.Admission.Token, "", http.StatusOK)
	request("POST", "/redeem", origin, "", string(body), http.StatusGone)
}

func TestNativeInvitationRedeemsThroughTrustedHTTPS(t *testing.T) {
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	service, err := conversations.NewService(conversations.Options{PublicOrigin: origin})
	if err != nil {
		t.Fatal(err)
	}
	bus := conversations.NewCommands(service)
	defer bus.Close()
	handler, err := New(origin, service, bus)
	if err != nil {
		t.Fatal(err)
	}
	var redirected atomic.Bool
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-target" {
			redirected.Store(true)
		} else if strings.Contains(r.URL.Path, "/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/") {
			http.Redirect(w, r, origin+"/redirect-target", http.StatusTemporaryRedirect)
			return
		}
		handler.ServeHTTP(w, r)
	})
	server.StartTLS()
	created, err := conversations.Dispatch[api.CreatedRoom](conversations.WithOwner(t.Context()), bus, conversations.CreateRoom{})
	if err != nil {
		t.Fatal(err)
	}
	prejoin, err := conversations.RedeemNativeLink(t.Context(), created.InvitationURL, []string{origin}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if prejoin.PageURL != origin+"/r/"+created.Admission.Room.RoomID || prejoin.Admission.Role != api.RoomGuest || prejoin.Admission.Token == created.Admission.Token {
		t.Fatal("invalid scoped guest prejoin")
	}
	for _, participant := range prejoin.Admission.Room.Participants {
		if participant.Joined || participant.Publication != (api.RoomPublication{}) {
			t.Fatal("invitation redemption started publication")
		}
	}
	if _, err := conversations.Dispatch[api.RoomView](conversations.WithParticipant(t.Context(), created.Admission.Token), bus, conversations.EndRoom{RoomRef: created.Admission.Room.RoomRef}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversations.RedeemNativeLink(t.Context(), created.InvitationURL, []string{origin}, server.Client()); conversations.ProjectError(err).Code != api.RoomAccessEnded {
		t.Fatal("stale invitation did not return the ended projection")
	}
	invitation, err := url.Parse(created.InvitationURL)
	if err != nil {
		t.Fatal(err)
	}
	invitation.Path = "/r/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := conversations.RedeemNativeLink(t.Context(), invitation.String(), []string{origin}, server.Client()); conversations.ProjectError(err).Code != api.RoomMediaUnreachable || redirected.Load() {
		t.Fatal("redemption followed an origin redirect")
	}
}

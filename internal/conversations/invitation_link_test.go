package conversations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
)

func TestReleaseNativeSeatRequiresRoomResponse(t *testing.T) {
	const roomID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	const token = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"gateway forbidden", http.StatusForbidden, `<html>Access denied</html>`, false},
		{"gateway success", http.StatusOK, `<html>Welcome</html>`, false},
		{"generic forbidden JSON", http.StatusForbidden, `{"error":{"code":"access_denied"}}`, false},
		{"typed stale grant", http.StatusForbidden, `{"error":{"code":"permission_denied","retryable":false}}`, true},
		{"typed expired room", http.StatusGone, `{"error":{"code":"expired","retryable":false}}`, true},
		{"matching room", http.StatusOK, `{"room_id":"` + roomID + `","version":1,"generation":1}`, true},
		{"wrong room", http.StatusOK, `{"room_id":"` + token + `","version":1,"generation":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != api.ConversationAPIPrefix+roomID+"/leave" ||
					r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("wrong Leave request")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			guest := api.NativeConversationPrejoin{PageURL: server.URL + "/r/" + roomID,
				Admission: api.RoomAdmission{Role: api.RoomGuest, Token: token,
					Room: api.RoomView{RoomRef: api.RoomRef{RoomID: roomID}}}}
			err := ReleaseNativeSeat(t.Context(), guest)
			if (err == nil) != tc.ok {
				t.Fatalf("release result = %v", err)
			}
		})
	}
}

func TestRecoverableAdmissionFallsBackOnlyForLegacyContract(t *testing.T) {
	const roomID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	const token = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA"
	const oldToken = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA"
	for _, legacy := range []bool{false, true} {
		tokens := make(chan string, 2)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request api.RoomRedeemRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error("could not read the admission request")
				return
			}
			tokens <- request.GuestToken
			if legacy && request.GuestToken != "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(api.RoomErrorResponse{Error: api.RoomError{Code: api.RoomInvalidRequest}})
				return
			}
			grant := request.GuestToken
			if grant == "" {
				grant = oldToken
			}
			_ = json.NewEncoder(w).Encode(api.RoomAdmission{Role: api.RoomGuest, Token: grant,
				Room: api.RoomView{RoomRef: api.RoomRef{RoomID: roomID}, Version: 1, Generation: 1}})
		}))
		link := server.URL + "/r/" + roomID + "#header.payload.signature"
		value, err := RedeemRecoverableLink(t.Context(), link, []string{server.URL}, true, token)
		server.Close()
		want := token
		if legacy {
			want = oldToken
		}
		if err != nil || value.Admission.Token != want || len(tokens) == 0 || <-tokens != token {
			t.Fatalf("recoverable admission failed: %v", err)
		}
		if legacy && (len(tokens) != 1 || <-tokens != "") {
			t.Fatal("the legacy fallback did not omit the guest token")
		}
	}
}

func TestParseNativeLink(t *testing.T) {
	const origin = "https://call.example.test"
	const roomID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	const invitation = "eyJhbGciOiJIUzI1NiJ9.eyJyb29tIjoiZml4dHVyZSJ9.fixture-signature"
	webLink := origin + "/r/" + roomID + "#" + invitation
	appLink := "camstream://conversation/join?origin=" + url.QueryEscape(origin) + "&room=" + roomID + "&v=1#" + invitation
	want := NativeLink{Origin: origin, Room: api.RoomRef{RoomID: roomID}, Invitation: invitation}
	for _, raw := range []string{webLink, appLink} {
		link, err := ParseNativeLink(raw, []string{origin})
		if err != nil || link != want {
			t.Fatalf("trusted invitation failed to parse: %v", err)
		}
	}
	for _, test := range []struct {
		name string
		link string
		code api.RoomErrorCode
	}{
		{"untrusted host", strings.Replace(webLink, "call.example.test", "elsewhere.test", 1), api.RoomPermissionDenied},
		{"cleartext", strings.Replace(webLink, "https:", "http:", 1), api.RoomInvitationInvalid},
		{"userinfo", strings.Replace(webLink, "https://", "https://user@", 1), api.RoomInvitationInvalid},
		{"query token", strings.Replace(webLink, "#", "?token=", 1), api.RoomInvitationInvalid},
		{"duplicate origin", strings.Replace(appLink, "&room=", "&origin="+url.QueryEscape(origin)+"&room=", 1), api.RoomInvitationInvalid},
		{"unexpected action", strings.Replace(appLink, "/join?", "/launch?", 1), api.RoomInvitationInvalid},
		{"other client version", strings.Replace(appLink, "&v=1", "&v=2", 1), api.RoomIncompatibleClient},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseNativeLink(test.link, []string{origin}); err == nil || ProjectError(err).Code != test.code {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
}

package conversations

import (
	"net/url"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
)

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

package maccamera

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

// Host setup failures cross the owner pipe as a closed classification with a
// stable room code. Paths, file contents and credentials stay in the helper.
func TestConversationSetupClassification(t *testing.T) {
	const secret = "private-fixture-secret-value"
	assets := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close() //nolint:errcheck // Test listener.
	external := config.Conversations{Ingress: config.SharingIngress{Mode: config.SharingIngressExternal,
		PublicBaseURL: "https://call.example.test", GuestListen: busy.Addr().String()}}
	relay := config.Conversations{Loopback: true, Relay: config.SharingRelay{Mode: config.SharingRelayCloudflare, CredentialRef: sharedCredentialRef}}
	for _, test := range []struct {
		name     string
		prepare  func(t *testing.T, dir string)
		assets   bool
		method   string
		code     api.RoomErrorCode
		setup    api.ConversationSetup
		retrying bool
	}{
		{name: "missing settings", prepare: func(*testing.T, string) {}, assets: true,
			method: api.ConversationNativeCreate, code: api.RoomMediaUnreachable, setup: api.ConversationSetupMissing},
		{name: "unparseable settings", prepare: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "conversation-settings.json"), `{"loopback": "`+secret+`"}`)
		}, assets: true, method: api.ConversationNativeCreate, code: api.RoomMediaUnreachable, setup: api.ConversationSetupInvalid},
		{name: "shared settings file", prepare: func(t *testing.T, dir string) {
			path := filepath.Join(dir, "conversation-settings.json")
			writeFixture(t, path, `{"loopback": true}`)
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}, assets: true, method: api.ConversationNativeCreate, code: api.RoomMediaUnreachable, setup: api.ConversationSetupInvalid},
		{name: "invalid settings", prepare: func(t *testing.T, dir string) {
			writeSettings(t, dir, config.Conversations{RelayOnly: true, Loopback: true})
		}, assets: true, method: api.ConversationNativeCreate, code: api.RoomMediaUnreachable, setup: api.ConversationSetupInvalid},
		{name: "missing relay credentials", prepare: func(t *testing.T, dir string) {
			writeSettings(t, dir, relay)
		}, assets: true, method: api.ConversationNativeCreate, code: api.RoomMediaUnreachable, setup: api.ConversationSetupInvalid},
		{name: "invalid relay credentials", prepare: func(t *testing.T, dir string) {
			writeSettings(t, dir, relay)
			writeFixture(t, filepath.Join(dir, "conversation-relay.json"), `{"api_token": "`+secret+`"}`)
		}, assets: true, method: api.ConversationNativeCreate, code: api.RoomMediaUnreachable, setup: api.ConversationSetupInvalid},
		{name: "listener address in use", prepare: func(t *testing.T, dir string) {
			writeSettings(t, dir, external)
		}, assets: true, method: api.ConversationNativeCreate, code: api.RoomMediaUnreachable, setup: api.ConversationSetupUnavailable, retrying: true},
		{name: "call page missing from the build", prepare: func(t *testing.T, dir string) {
			writeSettings(t, dir, config.Conversations{Loopback: true})
		}, method: api.ConversationNativeCreate, code: api.RoomMediaUnreachable, setup: api.ConversationSetupUnavailable, retrying: true},
		{name: "guest without settings", prepare: func(*testing.T, string) {}, assets: true,
			method: api.ConversationNativeOpen, code: api.RoomPermissionDenied, setup: api.ConversationSetupMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := testService(t)
			test.prepare(t, service.Dir)
			a := appSession{service: service}
			if test.assets {
				a.conversationAssets = assets
			}
			defer func() { a.conversation.Close() }()
			request := AppRequest{Version: 1, ID: "setup", Method: test.method}
			if test.method == api.ConversationNativeOpen {
				request.InvitationURL = "https://call.example.test/r/" + strings.Repeat("A", 43) + "#" + secret
			}
			value, fault := a.dispatch(t.Context(), request)
			if room, ok := value.(api.NativeConversationPrejoin); fault == nil || ok && room.PageURL != "" {
				t.Fatalf("setup failure returned a result: %v", fault)
			}
			if fault.Code != string(test.code) || fault.Setup != test.setup || fault.Retryable == nil || *fault.Retryable != test.retrying {
				t.Fatalf("got %s/%s retryable=%v, want %s/%s retryable=%v", fault.Code, fault.Setup, fault.Retryable, test.code, test.setup, test.retrying)
			}
			encoded, err := json.Marshal(AppReply{Version: 1, ID: request.ID, Error: fault})
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{secret, service.Dir, "conversation-settings", "conversation-relay", "127.0.0.1"} {
				if strings.Contains(string(encoded), private) {
					t.Fatalf("setup projection exposed %q", private)
				}
			}
		})
	}
}

func TestConversationSetupReadyCreatesLocalRoom(t *testing.T) {
	service := testService(t)
	writeSettings(t, service.Dir, config.Conversations{Loopback: true})
	a := appSession{service: service, conversationAssets: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}}
	defer func() { a.conversation.Close() }()
	value, fault := a.dispatch(t.Context(), AppRequest{Method: api.ConversationNativeCreate})
	room, ok := value.(api.NativeConversationPrejoin)
	if fault != nil || !ok || room.Admission.Role != api.RoomHost || !strings.HasPrefix(room.PageURL, "http://127.0.0.1:") {
		t.Fatalf("ready local setup did not create a room: %v", fault)
	}
}

func writeSettings(t *testing.T, dir string, settings config.Conversations) {
	t.Helper()
	if err := writeJSON(filepath.Join(dir, "conversation-settings.json"), settings); err != nil {
		t.Fatal(err)
	}
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

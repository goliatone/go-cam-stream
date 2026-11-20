package api_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

func TestConversationWireExamples(t *testing.T) {
	body, err := os.ReadFile("conversation_examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples struct {
		Waiting      api.RoomView          `json:"waiting"`
		Connected    api.RoomView          `json:"connected"`
		Reconnecting api.RoomView          `json:"reconnecting"`
		Ended        api.RoomView          `json:"ended"`
		Full         api.RoomErrorResponse `json:"full"`
		Denied       api.RoomErrorResponse `json:"denied"`
		Incompatible api.RoomErrorResponse `json:"incompatible"`
		SetupMissing struct {
			Error api.NativeConversationFault `json:"error"`
		} `json:"setup_missing"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&examples); err != nil {
		t.Fatal(err)
	}
	for _, room := range []api.RoomView{examples.Waiting, examples.Connected, examples.Reconnecting, examples.Ended} {
		if room.Validate() != nil || room.Version != api.ConversationVersion || room.Generation.Validate() != nil || room.ExpiresAt.IsZero() {
			t.Fatal("invalid room projection")
		}
	}
	if examples.Waiting.State != api.RoomWaiting || len(examples.Connected.Participants) != 2 ||
		examples.Reconnecting.Generation <= examples.Connected.Generation || examples.Ended.State != api.RoomEnded ||
		examples.Full.Error.Code != api.RoomFull || examples.Denied.Error.Code != api.RoomPermissionDenied ||
		examples.Incompatible.Error.Code != api.RoomIncompatibleClient {
		t.Fatal("conversation fixture lost a contract state")
	}
	setup := examples.SetupMissing.Error
	if setup.Code != api.RoomMediaUnreachable || setup.Setup != api.ConversationSetupMissing || setup.Retryable != setup.Setup.Retryable() {
		t.Fatal("native setup fixture lost its classification")
	}
	for _, value := range []api.ConversationSetup{api.ConversationSetupMissing, api.ConversationSetupInvalid} {
		if value.Retryable() {
			t.Fatalf("%s must require a settings change", value)
		}
	}
	if !api.ConversationSetupUnavailable.Retryable() || !api.ConversationSetupFailed.Retryable() {
		t.Fatal("runtime setup failures must stay retryable")
	}
}

func TestConversationRequestBounds(t *testing.T) {
	for _, request := range []interface{ Validate() error }{
		api.RoomRef{RoomID: "invalid"},
		api.RoomJoinRequest{},
		api.RoomSignal{Type: "offer", SDP: "v=0\r\n"},
		api.RoomSignal{Generation: 1, Type: "offer", SDP: "v=0\r\n" + strings.Repeat("v", api.ConversationSDPMax)},
		api.RoomSignal{Generation: 1, Type: "audio", SDP: "v=0\r\n"},
		api.RoomRedeemRequest{Version: 1, Invitation: "invalid"},
		config.Conversations{RelayOnly: true},
	} {
		if request.Validate() == nil {
			t.Fatalf("invalid %T accepted", request)
		}
	}
}

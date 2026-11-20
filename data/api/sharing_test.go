package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
)

func TestSharingWireExamples(t *testing.T) {
	body, err := os.ReadFile("sharing_examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples struct {
		Create             api.NativeShareRequest                     `json:"create"`
		Created            api.CreateShareResult                      `json:"created"`
		Setup              api.ShareSetupView                         `json:"setup"`
		SetupEmpty         api.ShareSetupView                         `json:"setup_empty"`
		Redeem             api.RedeemShareRequest                     `json:"redeem"`
		Redeemed           api.RedeemShareResult                      `json:"redeemed"`
		FirstPlay          api.AdmitShareViewerRequest                `json:"first_play"`
		Admitted           api.AdmitShareViewerResult                 `json:"admitted"`
		Offer              api.OpenShareViewerRequest                 `json:"offer"`
		AtCapacity         api.ShareErrorResponse                     `json:"at_capacity"`
		Expired            api.ShareErrorResponse                     `json:"expired"`
		Revoked            api.ShareErrorResponse                     `json:"revoked"`
		NativeReady        api.NativeShareEvent[api.NativeShareReady] `json:"native_ready"`
		NativeSetup        api.NativeShareRequest                     `json:"native_setup"`
		NativeSetupReply   api.NativeShareReply[api.ShareSetupView]   `json:"native_setup_reply"`
		NativeConfigure    api.NativeShareRequest                     `json:"native_configure"`
		NativeConfigured   api.NativeShareReply[api.ShareSetupView]   `json:"native_configured"`
		NativeRelayTest    api.NativeShareRequest                     `json:"native_relay_test"`
		NativeRelayFailure api.NativeShareReply[api.ShareSetupView]   `json:"native_relay_failure"`
		NativeChanged      api.NativeShareEvent[api.ShareView]        `json:"native_changed"`
		Reconnect          api.AdmitShareViewerRequest                `json:"reconnect"`
		Readmitted         api.AdmitShareViewerResult                 `json:"readmitted"`
		StaleAttempt       api.ShareErrorResponse                     `json:"stale_attempt"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&examples); err != nil {
		t.Fatal(err)
	}
	for _, request := range []interface{ Validate() error }{
		examples.Create, examples.Redeem, examples.FirstPlay, examples.Offer,
		examples.NativeSetup, examples.NativeConfigure, examples.NativeRelayTest, examples.Reconnect,
	} {
		if err = request.Validate(); err != nil {
			t.Fatalf("invalid wire example %T", request)
		}
	}
	if !api.ValidShareID(examples.Admitted.ViewerID) || !api.ValidShareID(examples.Admitted.AttemptID) {
		t.Fatal("viewer handles must be canonical")
	}
	// Producers can return a zero-value slice when no ingress is registered.
	examples.SetupEmpty.IngressModes = nil
	roundTrip, err := json.Marshal(examples)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err = json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(roundTrip, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("sharing examples and JSON contracts do not round-trip")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", examples.Created, examples.Redeem), "testonly") {
		t.Fatal("operator link or invitation escaped formatted diagnostics")
	}
	projections, err := json.Marshal([]any{examples.NativeSetupReply, examples.NativeConfigured, examples.NativeRelayFailure, examples.NativeChanged})
	if err != nil || strings.Contains(string(projections), "fixture-write-only") || strings.Contains(string(projections), "credential_ref") {
		t.Fatal("native output exposes credential input or reference")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", examples.NativeConfigure, examples.NativeConfigure), "fixture-write-only") {
		t.Fatal("native diagnostic exposes relay credential input")
	}
}

func TestSharingRequestBounds(t *testing.T) {
	want := api.ShareOptions{DurationSeconds: api.ShareDuration1Hour, ViewerLimit: 1}
	if got := (api.ShareOptions{}).WithDefaults(); got != want {
		t.Fatalf("incorrect default share options: %+v", got)
	}
	for _, request := range []interface{ Validate() error }{
		api.ShareOptions{DurationSeconds: 60},
		api.ShareOptions{ViewerLimit: 4},
		api.RedeemShareRequest{Invitation: "abc.def."},
		api.RedeemShareRequest{Invitation: strings.Repeat("a", api.ShareInvitationMax) + ".b.c"},
		api.AdmitShareViewerRequest{ViewerID: "operator"},
		api.OpenShareViewerRequest{Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}},
		api.NativeShareRequest{Version: 1, ID: "req-1", Method: api.ShareNativeSetup, Sharing: &api.NativeShareArguments{}},
		api.NativeShareRequest{Version: 1, ID: "req-1", Method: api.ShareNativeRevoke, Sharing: &api.NativeShareArguments{CameraID: "camera"}},
	} {
		if err := request.Validate(); err == nil {
			t.Fatalf("malformed %T accepted", request)
		}
	}
}

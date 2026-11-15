package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/pkg/camstream"
	gerrors "github.com/goliatone/go-errors"
)

func TestWireExamplesRoundTripWithoutHiddenOrMissingFields(t *testing.T) {
	body, err := os.ReadFile("examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples struct {
		EmptyCatalog      api.CameraList           `json:"empty_catalog"`
		Catalog           api.CameraList           `json:"catalog"`
		OfflineStatus     api.CameraStatus         `json:"offline_status"`
		UnknownStatus     api.StreamStatus         `json:"unknown_status"`
		Denied            api.ErrorResponse        `json:"denied"`
		Offer             api.CreateSessionRequest `json:"offer"`
		Session           api.Session              `json:"session"`
		DiagnoseRequest   api.DiagnoseRequest      `json:"diagnose_request"`
		DiagnoseResponse  api.DiagnoseResponse     `json:"diagnose_response"`
		ReconnectTarget   api.StreamRef            `json:"reconnect_target"`
		ReconnectResponse api.ReconnectResponse    `json:"reconnect_response"`
		CSRF              api.CSRFResponse         `json:"csrf"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&examples); err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(examples)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err = json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(result, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("wire fields drifted from examples")
	}
	if examples.UnknownStatus.Viewers != nil || examples.OfflineStatus.Streams[0].Viewers == nil {
		t.Fatal("unknown count collapsed into observed zero")
	}
}

func TestPublicErrorNeverRendersStructuredProviderText(t *testing.T) {
	raw := gerrors.New("rtsp://user:password@camera", gerrors.CategoryExternal).
		WithTextCode(string(camstream.CodeSourceUnavailable)).WithMetadata(map[string]any{"credential": "password"})
	raw.Source = context.DeadlineExceeded
	result := api.PublicError(raw)
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "password") || strings.Contains(string(body), "rtsp:") {
		t.Fatal("provider data escaped")
	}
	if result.Error.Code != camstream.CodeSourceUnavailable || result.Error.Retryable {
		t.Fatal("source latch classification lost")
	}
	if api.ErrorStatus(result.Error.Code) != 503 || api.ErrorStatus(camstream.CodeAccessDenied) != 403 {
		t.Fatal("status mapping drift")
	}
	if api.PublicError(nil).Error.Code != camstream.CodeInternal {
		t.Fatal("nil error projection must fail closed")
	}
}

func TestNegotiationShapeValidation(t *testing.T) {
	for _, request := range []api.CreateSessionRequest{
		{},
		{Offer: api.SDPOffer{Type: "answer", SDP: "v=0\r\n"}},
		{Offer: api.SDPOffer{Type: "offer", SDP: "not-sdp"}},
		{Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n" + strings.Repeat("a", api.MaxSDPBytes)}},
	} {
		if got := camstream.DescribeError(request.Validate()).Code; got != camstream.CodeInvalidArgument {
			t.Fatalf("invalid envelope accepted: %s", got)
		}
	}
	valid := api.CreateSessionRequest{Offer: api.SDPOffer{Type: "offer", SDP: "v=0\r\n"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

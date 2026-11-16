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

func TestManagementFixtures(t *testing.T) {
	body, err := os.ReadFile("management_examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if err = json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	types := map[string]reflect.Type{}
	for _, v := range []any{api.Capabilities{}, api.CreateDraftRequest{}, api.ImportDraftRequest{}, api.PatchDraftRequest{}, api.Mutation{}, api.DraftReview{}, api.CreateTestRequest{}, api.CompleteTestRequest{}, api.SaveCameraRequest{}, api.SaveCameraResponse{}, api.OperationResult{}, api.ManagementErrorResponse{}, api.CameraList{}} {
		typ := reflect.TypeOf(v)
		types[typ.Name()] = typ
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			typ, ok := types[fixture.Type]
			if !ok {
				t.Fatal("unregistered fixture type", fixture.Type)
			}
			v := reflect.New(typ).Interface()
			d := json.NewDecoder(bytes.NewReader(fixture.Value))
			d.DisallowUnknownFields()
			if err := d.Decode(v); err != nil {
				t.Fatal(err)
			}
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			if err = json.Unmarshal(fixture.Value, &before); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(out, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("fixture lost wire fields: %s", out)
			}
			if e, ok := v.(*api.ManagementErrorResponse); ok && e.Error.Code != "access_denied" {
				canonical := api.ManagementPublicError(api.ManagementFailure(api.ManagementErrorCode(e.Error.Code)))
				if canonical.Error != e.Error || api.ManagementErrorStatus(e.Error.Code) == 500 {
					t.Fatal("error mapping drift")
				}
			}
		})
	}
}

func TestManagementRequestFormattingRedactsInputs(t *testing.T) {
	input := api.DraftInput{Name: "private-name", Endpoint: "rtsp://private-host/path", Credentials: &api.Credentials{Username: "secret-user", Password: "secret-pass"}}
	for _, v := range []any{input, *input.Credentials, api.CreateDraftRequest{DraftInput: input}, api.PatchDraftRequest{DraftInput: input}, api.ImportDraftRequest{YAML: "secret-yaml", Env: "secret-env"}} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			text := fmt.Sprintf(format, v)
			if strings.Contains(text, "secret-") || strings.Contains(text, "private-") {
				t.Fatalf("request formatting leaked: %s", text)
			}
		}
	}
	// Unknown codes never reflect attacker-controlled text.
	e := api.ManagementPublicError(api.ManagementFailure("secret-provider-error"))
	if e.Error.Code != "internal" || strings.Contains(e.Error.Message, "secret-") {
		t.Fatal("unknown error reflected")
	}
}

func TestManagementValidation(t *testing.T) {
	good := api.Mutation{OperationKey: strings.Repeat("a", 32), RuntimeEpoch: "epoch"}
	if good.Validate() != nil {
		t.Fatal("valid key rejected")
	}
	for _, key := range []string{"", strings.Repeat("a", 31), strings.Repeat("A", 32), strings.Repeat("z", 32)} {
		m := good
		m.OperationKey = key
		if m.Validate() == nil {
			t.Fatal("bad key accepted")
		}
	}
	evidence := api.FrameEvidence{Method: "frames_decoded", Frames: 2, Width: 640, Height: 480}
	if evidence.Validate() != nil {
		t.Fatal("frame evidence rejected")
	}
	for _, bad := range []api.FrameEvidence{{Method: "playing", Frames: 20, Width: 640, Height: 480}, {Method: "frames_decoded", Frames: 1, Width: 640, Height: 480}, {Method: "playback_quality", Frames: 3, Width: 640, Height: 480, Paused: true}, {Method: "video_frame_callback", Frames: 3}} {
		if bad.Validate() == nil {
			t.Fatal("insufficient evidence accepted")
		}
	}
}

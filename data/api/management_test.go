package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	gerrors "github.com/goliatone/go-errors"
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
	for _, v := range []any{api.Capabilities{}, api.CreateDraftRequest{}, api.ImportDraftRequest{}, api.PatchDraftRequest{}, api.Mutation{}, api.DraftReview{}, api.CreateTestRequest{}, api.CompleteTestRequest{}, api.SaveCameraRequest{}, api.SaveCameraResponse{}, api.OperationResult{}, api.ManagementErrorResponse{}, api.CameraList{}, api.CredentialDraftRequest{}, api.CameraSettings{}, api.RenameCameraRequest{}, api.RemoveCameraRequest{}, api.CameraChange{}, api.StopViewersRequest{}} {
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
	revision := api.CatalogRevision{RuntimeEpoch: "epoch", Generation: 2}
	for name, tc := range map[string]struct {
		valid, invalid interface{ Validate() error }
	}{
		"rename": {api.RenameCameraRequest{Mutation: good, Name: "Porch", CatalogRevision: revision}, api.RenameCameraRequest{Mutation: good, CatalogRevision: revision}},
		"remove": {api.RemoveCameraRequest{Mutation: good, CatalogRevision: revision, StopViewers: true}, api.RemoveCameraRequest{Mutation: good}},
		"stop":   {api.StopViewersRequest{Mutation: good, ExpectedRevision: 1}, api.StopViewersRequest{Mutation: good}},
	} {
		if tc.valid.Validate() != nil || tc.invalid.Validate() == nil {
			t.Fatal(name, "request validation drift")
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

func TestManagementCommandErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		code   string
		status int
	}{
		{"denied", cs.NewError(cs.CodeAccessDenied, nil), "access_denied", 403},
		{"capacity", cs.NewError(cs.CodeCapacityExceeded, nil), "capacity_exceeded", 429},
		{"cleanup keeps precedence", cs.NewError(cs.CodeCleanupIncomplete, context.DeadlineExceeded), "cleanup_incomplete", 503},
		{"management", api.ManagementFailure(api.CodeCatalogConflict), "catalog_revision_conflict", 409},
		{"foreign", errors.New("PRIVATE provider payload"), "internal", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projected := api.ManagementCommandFailure(tc.err)
			wrapped := gerrors.Wrap(projected, gerrors.CategoryInternal, "PRIVATE dispatcher detail").WithTextCode("HANDLER_EXECUTION_FAILED")
			body := api.ManagementPublicError(wrapped)
			if body.Error.Code != tc.code || api.ManagementErrorStatus(body.Error.Code) != tc.status {
				t.Fatal(body)
			}
			if !errors.Is(wrapped, tc.err) {
				t.Fatal("lost cause identity")
			}
			encoded, _ := json.Marshal(body)
			if strings.Contains(string(encoded), "PRIVATE") {
				t.Fatal("private error leaked")
			}
		})
	}
	if api.ManagementCommandFailure(nil) != nil {
		t.Fatal("success became error")
	}
}

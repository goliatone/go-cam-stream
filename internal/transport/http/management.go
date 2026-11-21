package httptransport

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-command"
	router "github.com/goliatone/go-router"
)

type ManagementRoutes struct {
	Service  viewer.CameraManagement
	Recovery viewer.CameraRecovery
}

func managementResult(c router.Context, value any, err error) error {
	if err != nil {
		body := api.ManagementPublicError(err)
		return c.JSON(api.ManagementErrorStatus(body.Error.Code), body)
	}
	return c.JSON(http.StatusOK, value)
}
func (h Routes) managementDispatch(c router.Context, name string, msg any) error {
	out, err := h.Bus.DispatchByNameWithOutcome(c.Context(), name, map[string]any{"_message": msg}, nil, command.DispatchOptions{Mode: command.ExecutionModeInline})
	return managementResult(c, out.Result, err)
}
func decodeManagement(c router.Context, out any) error {
	hc, ok := c.(router.HTTPContext)
	if !ok {
		return cs.NewError(cs.CodeInternal, nil)
	}
	media, _, err := mime.ParseMediaType(c.Header("Content-Type"))
	if err != nil || media != "application/json" {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	body, err := io.ReadAll(io.LimitReader(hc.Request().Body, 128*1024+1))
	defer clear(body)
	if err != nil || len(body) > 128*1024 {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil || bytes.Equal(bytes.TrimSpace(fields["credentials"]), []byte("null")) {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return nil
}
func decodeImport(c router.Context) (api.ImportDraftRequest, error) {
	var request api.ImportDraftRequest
	hc, ok := c.(router.HTTPContext)
	if !ok {
		return request, cs.NewError(cs.CodeInternal, nil)
	}
	reader, err := hc.Request().MultipartReader()
	if err != nil {
		return request, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	seen := map[string]bool{}
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return request, cs.NewError(cs.CodeInvalidArgument, nil)
		}
		name := part.FormName()
		limit := 128
		switch name {
		case "yaml":
			limit = api.MaxImportYAMLBytes
		case "env":
			limit = api.MaxImportEnvBytes
		case "operation_key", "runtime_epoch":
		default:
			_ = part.Close()
			return request, cs.NewError(cs.CodeInvalidArgument, nil)
		}
		if seen[name] {
			_ = part.Close()
			return request, cs.NewError(cs.CodeInvalidArgument, nil)
		}
		seen[name] = true
		body, e := io.ReadAll(io.LimitReader(part, int64(limit)+1))
		closeErr := part.Close()
		if e != nil || closeErr != nil || len(body) > limit {
			clear(body)
			return request, cs.NewError(cs.CodeInvalidArgument, nil)
		}
		value := string(body)
		clear(body)
		switch name {
		case "yaml":
			request.YAML = value
		case "env":
			request.Env = value
		case "operation_key":
			request.OperationKey = value
		case "runtime_epoch":
			request.RuntimeEpoch = value
		}
	}
	return request, nil
}
func (h Routes) registerManagement(r admin.AdminRouter) {
	if h.Management.Service == nil {
		return
	}
	service := h.Management.Service
	r.Get(api.CapabilitiesPath, func(c router.Context) error {
		v, e := service.Capabilities(c.Context())
		return managementResult(c, v, e)
	})
	r.Get(api.DraftsPath+"/:draft_id", func(c router.Context) error {
		v, e := service.Draft(c.Context(), c.Param("draft_id"))
		return managementResult(c, v, e)
	})
	r.Get(api.DraftsPath+"/:draft_id/tests/:test_id", func(c router.Context) error {
		v, e := service.Test(c.Context(), c.Param("draft_id"), c.Param("test_id"))
		return managementResult(c, v, e)
	})
	r.Get(api.DraftsPath+"/:draft_id/tests", func(c router.Context) error {
		v, e := service.TestByAttempt(c.Context(), c.Param("draft_id"), c.Query("attempt_key"))
		return managementResult(c, v, e)
	})
	r.Get(api.OperationsPath+"/:key", func(c router.Context) error {
		if c.Query("kind") == string(api.OperationRecovery) && h.Management.Recovery != nil {
			v, e := h.Management.Recovery.RecoveryOperation(c.Context(), c.Param("key"), c.Query("runtime_epoch"))
			return managementResult(c, v, e)
		}
		v, e := service.Operation(c.Context(), c.Param("key"), c.Query("candidate_id"), c.Query("runtime_epoch"))
		return managementResult(c, v, e)
	})
	r.Post(api.DraftsPath, func(c router.Context) error {
		var q api.CreateDraftRequest
		if e := decodeManagement(c, &q); e != nil {
			return managementResult(c, nil, e)
		}
		return h.managementDispatch(c, api.CreateDraftCommand, viewer.CreateDraft{Request: q})
	})

	r.Post(api.DraftsPath+"/*draft_path", h.managementDraftPost)
	r.Patch(api.DraftsPath+"/:draft_id", func(c router.Context) error {
		var q api.PatchDraftRequest
		if e := decodeManagement(c, &q); e != nil {
			return managementResult(c, nil, e)
		}
		return h.managementDispatch(c, api.PatchDraftCommand, viewer.PatchDraft{DraftID: c.Param("draft_id"), Request: q})
	})
	r.Delete(api.DraftsPath+"/:draft_id", func(c router.Context) error {
		var q api.Mutation
		if e := decodeManagement(c, &q); e != nil {
			return managementResult(c, nil, e)
		}
		return h.managementDispatch(c, api.CancelDraftCommand, viewer.CancelDraft{DraftID: c.Param("draft_id"), Request: q})
	})

	r.Delete(api.DraftsPath+"/:draft_id/tests/:test_id", func(c router.Context) error {
		var q api.Mutation
		if e := decodeManagement(c, &q); e != nil {
			return managementResult(c, nil, e)
		}
		return h.managementDispatch(c, api.CancelTestCommand, viewer.CancelTest{DraftID: c.Param("draft_id"), TestID: c.Param("test_id"), Request: q})
	})
	r.Post("/api/cameras", func(c router.Context) error {
		var q api.SaveCameraRequest
		if e := decodeManagement(c, &q); e != nil {
			return managementResult(c, nil, e)
		}
		return h.managementDispatch(c, api.SaveCameraCommand, viewer.SaveCamera{Request: q})
	})
	if h.Management.Recovery != nil {
		r.Post("/api/cameras/:camera_id/streams/:stream_id/recovery", func(c router.Context) error {
			var q api.Mutation
			if e := decodeManagement(c, &q); e != nil {
				return managementResult(c, nil, e)
			}
			return h.managementDispatch(c, api.RecoverCameraCommand, viewer.RecoverCamera{Ref: api.StreamRef{CameraID: camera(c), StreamID: stream(c)}, Request: q})
		})
	}
}

// httprouter cannot mix the literal import segment with a wildcard sibling.
// Keep the published URLs under one POST subtree with the same auth/CSRF middleware.
func (h Routes) managementDraftPost(c router.Context) error {
	parts := strings.Split(strings.TrimPrefix(c.Param("draft_path"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "credentials":
		var q api.CredentialDraftRequest
		if e := decodeManagement(c, &q); e != nil {
			return managementResult(c, nil, e)
		}
		return h.managementDispatch(c, api.CredentialDraftCommand, viewer.CredentialDraft{Request: q})
	case len(parts) == 1 && parts[0] == "import":
		q, e := decodeImport(c)
		if e != nil {
			return managementResult(c, nil, e)
		}
		return h.managementDispatch(c, api.ImportDraftCommand, viewer.ImportDraft{Request: q})
	case len(parts) == 2 && parts[1] == "tests":
		var q api.CreateTestRequest
		if e := decodeManagement(c, &q); e != nil {
			return managementResult(c, nil, e)
		}
		if !h.Ready() {
			return managementResult(c, nil, cs.NewError(cs.CodeBackendUnavailable, nil))
		}
		return h.managementDispatch(c, api.TestDraftCommand, viewer.TestDraft{DraftID: parts[0], Request: q})
	case len(parts) == 4 && parts[1] == "tests" && parts[3] == "complete":
		var q api.CompleteTestRequest
		if e := decodeManagement(c, &q); e != nil {
			return managementResult(c, nil, e)
		}
		return h.managementDispatch(c, api.CompleteTestCommand, viewer.CompleteTest{DraftID: parts[0], TestID: parts[2], Request: q})
	default:
		return managementResult(c, nil, cs.NewError(cs.CodeNotFound, nil))
	}
}

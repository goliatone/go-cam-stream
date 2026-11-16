// Package httptransport owns request decoding and safe response rendering.
package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-admin/admin/routing"
	"github.com/goliatone/go-auth/middleware/csrf"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data"
	"github.com/goliatone/go-cam-stream/data/api"
	cameraadmin "github.com/goliatone/go-cam-stream/internal/admin"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-command"
	router "github.com/goliatone/go-router"
)

type Routes struct {
	Service    *viewer.CameraService
	Bus        *admin.CommandBus
	Ready      func() bool
	Management ManagementRoutes
}

// New mounts a minimal core go-admin host and the application API. Registration
// is completed before WrappedRouter seals routes. The caller owns the bus.
func New(ctx context.Context, cfg config.Config, service *viewer.CameraService, bus *admin.CommandBus, ready func() bool, logger admin.Logger, management ...ManagementRoutes) (http.Handler, error) {
	if service == nil || bus == nil || ready == nil || cfg.Validate() != nil {
		return nil, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	server := router.NewHTTPServer()
	r := server.Router()
	r.Use(authenticator{}.WrapHandler)
	r.Use(csrfProtection(cfg.InstallationID))
	csrf.RegisterRoutes(r, csrf.RouteConfig{Path: api.CSRFPath})
	h := Routes{Service: service, Bus: bus, Ready: ready}
	if len(management) > 0 {
		h.Management = management[0]
	}
	h.register(r)
	h.registerManagement(r)
	// Core go-admin starts a legacy process-global registry whenever its bus is
	// enabled during Initialize. Keep it disabled through bootstrap; our committed
	// owner runtimes are independent and enabled only after route registration.
	bus.Enable(false)
	defer bus.Enable(true)
	adm, err := admin.New(admin.Config{BasePath: "/admin", DefaultLocale: "en", Title: "Camera operations", DisabledDefaultModules: []admin.DefaultModuleID{admin.DefaultModuleFeatureFlags, admin.DefaultModuleActivity}, Routing: routing.Config{Enabled: true}}, admin.Dependencies{CommandBus: bus, Authenticator: authenticator{}, Authorizer: authenticator{}, Logger: logger})
	if err != nil {
		return nil, cs.NewError(cs.CodeInternal, err)
	}
	if err = adm.RegisterModule(cameraadmin.CameraModule{Diagnose: h.diagnose, Reconnect: h.reconnect}); err != nil {
		return nil, cs.NewError(cs.CodeInternal, err)
	}
	if err = adm.InitializeWithContext(ctx, r); err != nil {
		return nil, cs.NewError(cs.CodeInternal, err)
	}
	web, err := data.Web(server.WrappedRouter())
	if err != nil {
		return nil, cs.NewError(cs.CodeInternal, err)
	}
	return guardWithAdmission(web, cfg.Origin, service.AdmittedPrincipals, ready), nil
}
func (h Routes) register(r admin.AdminRouter) {
	r.Get("/api/health", func(c router.Context) error {
		return c.JSON(http.StatusOK, map[string]bool{"ok": true, "playback_ready": h.Ready()})
	})
	r.Get("/api/cameras", h.list)
	r.Get("/api/cameras/:camera_id", h.detail)
	r.Get("/api/cameras/:camera_id/status", h.cameraStatus)
	r.Get("/api/cameras/:camera_id/streams", h.streams)
	r.Get("/api/cameras/:camera_id/streams/:stream_id/status", h.status)
	r.Post("/api/cameras/:camera_id/streams/:stream_id/sessions", h.create)
	r.Delete("/api/playback/sessions/:session_id", h.close)
	r.Post("/api/cameras/:camera_id/diagnose", h.diagnose)
	r.Post("/api/cameras/:camera_id/streams/:stream_id/reconnect", h.reconnect)
}
func camera(c router.Context) cs.CameraID { return cs.CameraID(c.Param("camera_id")) }
func stream(c router.Context) cs.StreamID { return cs.StreamID(c.Param("stream_id")) }
func result(c router.Context, v any, err error) error {
	if err != nil {
		return replyError(c, err)
	}
	return c.JSON(http.StatusOK, v)
}
func (h Routes) list(c router.Context) error {
	v, e := h.Service.List(c.Context())
	return result(c, v, e)
}
func (h Routes) detail(c router.Context) error {
	v, e := h.Service.Camera(c.Context(), camera(c))
	return result(c, v, e)
}
func (h Routes) streams(c router.Context) error {
	v, e := h.Service.Camera(c.Context(), camera(c))
	return result(c, api.StreamList{Streams: v.Streams}, e)
}
func (h Routes) status(c router.Context) error {
	v, e := h.Service.Status(c.Context(), camera(c), stream(c))
	return result(c, v, e)
}
func (h Routes) cameraStatus(c router.Context) error {
	detail, err := h.Service.Camera(c.Context(), camera(c))
	if err != nil {
		return replyError(c, err)
	}
	out := api.CameraStatus{CameraID: detail.ID, Streams: []api.StreamStatus{}}
	for _, profile := range detail.Streams {
		state, e := h.Service.Status(c.Context(), detail.ID, profile.ID)
		if e != nil {
			return replyError(c, e)
		}
		out.Streams = append(out.Streams, state)
	}
	return c.JSON(http.StatusOK, out)
}
func (h Routes) create(c router.Context) error {
	if !h.Ready() {
		return replyError(c, cs.NewError(cs.CodeBackendUnavailable, nil))
	}
	var request api.CreateSessionRequest
	if err := decode(c, &request); err != nil {
		return replyError(c, err)
	}
	v, err := h.Service.CreateSession(c.Context(), camera(c), stream(c), request)
	if err != nil {
		return replyError(c, err)
	}
	return c.JSON(http.StatusCreated, v)
}
func (h Routes) close(c router.Context) error {
	if err := noBody(c); err != nil {
		return replyError(c, err)
	}
	if err := h.Service.CloseSession(c.Context(), cs.SessionID(c.Param("session_id"))); err != nil {
		return replyError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
func (h Routes) diagnose(c router.Context) error {
	var request api.DiagnoseRequest
	if err := decode(c, &request); err != nil {
		return replyError(c, err)
	}
	out, err := h.Bus.DispatchByNameWithOutcome(c.Context(), api.DiagnoseCommand, map[string]any{"camera_id": string(camera(c)), "stream_id": string(request.StreamID)}, nil, command.DispatchOptions{Mode: command.ExecutionModeInline})
	return result(c, out.Result, commandError(err)) // failed checks remain typed, as published by T02
}
func (h Routes) reconnect(c router.Context) error {
	if err := noBody(c); err != nil {
		return replyError(c, err)
	}
	if !h.Ready() {
		return replyError(c, cs.NewError(cs.CodeBackendUnavailable, nil))
	}
	out, err := h.Bus.DispatchByNameWithOutcome(c.Context(), api.ReconnectCommand, map[string]any{"camera_id": string(camera(c)), "stream_id": string(stream(c))}, nil, command.DispatchOptions{Mode: command.ExecutionModeInline})
	return result(c, out.Result, commandError(err))
}
func decode(c router.Context, out any) error {
	hc, ok := c.(router.HTTPContext)
	if !ok {
		return cs.NewError(cs.CodeInternal, nil)
	}
	content, _, err := mime.ParseMediaType(c.Header("Content-Type"))
	if err != nil || content != "application/json" {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	decoder := json.NewDecoder(hc.Request().Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return nil
}
func noBody(c router.Context) error {
	hc, ok := c.(router.HTTPContext)
	if !ok {
		return cs.NewError(cs.CodeInternal, nil)
	}
	b, err := io.ReadAll(io.LimitReader(hc.Request().Body, 1))
	if err != nil || len(b) > 0 {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	return nil
}

// The command runtime adds its own failure envelope. Recover only classified
// application codes from that trusted handler chain, never raw provider text.
func commandError(err error) error {
	for cause := err; cause != nil; {
		info := cs.DescribeError(cause)
		if info.Code != cs.CodeInternal {
			return cs.NewError(info.Code, nil)
		}
		var chain interface{ Unwrap() error }
		if !errors.As(cause, &chain) {
			break
		}
		cause = chain.Unwrap()
	}
	if err != nil {
		return cs.NewError(cs.CodeInternal, nil)
	}
	return nil
}

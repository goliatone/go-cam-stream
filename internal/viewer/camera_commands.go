package viewer

import (
	"context"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-command"
)

type Diagnose struct {
	CameraID cs.CameraID `json:"camera_id"`
	StreamID cs.StreamID `json:"stream_id,omitempty"`
}

func (Diagnose) Type() string { return api.DiagnoseCommand }
func (m Diagnose) Validate() error {
	if !config.ValidID(string(m.CameraID)) || (m.StreamID != "" && !config.ValidID(string(m.StreamID))) {
		return invalid()
	}
	return nil
}

type Reconnect struct {
	CameraID cs.CameraID `json:"camera_id"`
	StreamID cs.StreamID `json:"stream_id"`
}

func (Reconnect) Type() string { return api.ReconnectCommand }
func (m Reconnect) Validate() error {
	if !config.ValidID(string(m.CameraID)) || !config.ValidID(string(m.StreamID)) {
		return invalid()
	}
	return nil
}

type DiagnoseHandler struct{ Service *CameraService }

func (h DiagnoseHandler) Run(ctx context.Context, msg Diagnose) (DiagnoseResult, error) {
	if err := msg.Validate(); err != nil {
		return DiagnoseResult{}, err
	}
	if h.Service == nil {
		return DiagnoseResult{}, invalid()
	}
	return h.Service.Diagnose(ctx, msg.CameraID, msg.StreamID)
}
func (h DiagnoseHandler) Execute(ctx context.Context, msg Diagnose) error {
	value, err := h.Run(ctx, msg)
	if result := command.ResultFromContext[DiagnoseResult](ctx); result != nil {
		if err != nil {
			result.StoreError(err)
		} else {
			result.Store(value)
		}
	}
	return err
}

type ReconnectHandler struct{ Service *CameraService }

func (h ReconnectHandler) Run(ctx context.Context, msg Reconnect) (api.ReconnectResponse, error) {
	if err := msg.Validate(); err != nil {
		return api.ReconnectResponse{}, err
	}
	if h.Service == nil {
		return api.ReconnectResponse{}, invalid()
	}
	return h.Service.Reconnect(ctx, msg.CameraID, msg.StreamID)
}
func (h ReconnectHandler) Execute(ctx context.Context, msg Reconnect) error {
	value, err := h.Run(ctx, msg)
	if result := command.ResultFromContext[api.ReconnectResponse](ctx); result != nil {
		if err != nil {
			result.StoreError(err)
		} else {
			result.Store(value)
		}
	}
	return err
}

package viewer

import (
	"context"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

// Register publishes an owned set on the supplied bus. The host retains the
// handle and closes it after stopping/draining invocations. No global dispatcher,
// HTTP route, CLI entrypoint or backend lifecycle is installed here.
func Register(bus *admin.CommandBus, service *CameraService) (admin.CommandRegistrationHandle, error) {
	if bus == nil || service == nil {
		return nil, invalid()
	}
	set, err := bus.NewRegistrationSet("camstream.viewer")
	if err != nil {
		return nil, err
	}
	if err = admin.RegisterSetCommand(set, DiagnoseHandler{service}); err != nil {
		return nil, err
	}
	if err = admin.RegisterSetCommand(set, ReconnectHandler{service}); err != nil {
		return nil, err
	}
	err = admin.RegisterSetContextMessageResultFactory[Diagnose, DiagnoseResult](set, api.DiagnoseCommand, func(_ context.Context, payload map[string]any, ids []string) (Diagnose, error) {
		camera, stream, payloadErr := commandPayload(payload, ids)
		if payloadErr != nil {
			return Diagnose{}, payloadErr
		}
		msg := Diagnose{camera, stream}
		return msg, msg.Validate()
	})
	if err != nil {
		return nil, err
	}
	err = admin.RegisterSetContextMessageResultFactory[Reconnect, api.ReconnectResponse](set, api.ReconnectCommand, func(_ context.Context, payload map[string]any, ids []string) (Reconnect, error) {
		camera, stream, payloadErr := commandPayload(payload, ids)
		if payloadErr != nil {
			return Reconnect{}, payloadErr
		}
		msg := Reconnect{camera, stream}
		return msg, msg.Validate()
	})
	if err != nil {
		return nil, err
	}
	return set.Commit()
}
func commandPayload(payload map[string]any, ids []string) (cs.CameraID, cs.StreamID, error) {
	if len(ids) != 0 {
		return "", "", invalid()
	}
	for key := range payload {
		if key != "camera_id" && key != "stream_id" {
			return "", "", invalid()
		}
	}
	camera, ok := payload["camera_id"].(string)
	if !ok {
		return "", "", invalid()
	}
	stream := ""
	if value, exists := payload["stream_id"]; exists {
		var valid bool
		stream, valid = value.(string)
		if !valid {
			return "", "", invalid()
		}
	}
	return cs.CameraID(camera), cs.StreamID(stream), nil
}

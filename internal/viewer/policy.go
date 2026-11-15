package viewer

import (
	"context"
	"slices"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type principalKey struct{}

// WithPrincipal is for the trusted ingress adapter after verifying its trust
// boundary and decoding the exact Serve login. It does not authenticate headers.
func WithPrincipal(ctx context.Context, login string) context.Context {
	return context.WithValue(ctx, principalKey{}, login)
}
func principal(ctx context.Context) string {
	value, ok := ctx.Value(principalKey{}).(string)
	if !ok {
		return ""
	}
	return value
}

type permission uint8

const (
	view permission = iota
	diagnose
	reconnect
)

type rights struct{ view, diagnose, reconnect bool }
type scope struct {
	camera cs.CameraID
	stream cs.StreamID
}

func denied() error  { return cs.NewError(cs.CodeAccessDenied, nil) }
func invalid() error { return cs.NewError(cs.CodeInvalidArgument, nil) }

func (s *CameraService) allowed(ctx context.Context, camera cs.CameraID, stream cs.StreamID, action permission) bool {
	r := s.access[principal(ctx)][scope{camera, stream}]
	switch action {
	case view:
		return r.view
	case diagnose:
		return r.diagnose
	case reconnect:
		return r.reconnect
	default:
		return false
	}
}
func (s *CameraService) require(ctx context.Context, camera cs.CameraID, stream cs.StreamID, action permission) (config.CatalogCamera, error) {
	if err := ctx.Err(); err != nil {
		return config.CatalogCamera{}, err
	}
	if !s.allowed(ctx, camera, stream, action) {
		return config.CatalogCamera{}, denied()
	}
	item, ok := s.cameras[camera]
	if !ok || item.Sources[stream] == "" {
		return config.CatalogCamera{}, denied()
	}
	return item, nil
}

func (s *CameraService) loadCatalog(catalog config.Catalog) error {
	for _, item := range catalog.Cameras {
		if err := s.addCamera(item); err != nil {
			return err
		}
	}
	for _, entry := range catalog.Access {
		if !config.ValidLogin(entry.Login) || s.access[entry.Login] != nil {
			return invalid()
		}
		grants := map[scope]rights{}
		for _, grant := range entry.Grants {
			if len(grant.Streams) == 0 || (!grant.View && !grant.Diagnose && !grant.Reconnect) {
				return invalid()
			}
			for _, stream := range grant.Streams {
				key := scope{grant.CameraID, stream}
				if s.cameras[grant.CameraID].Sources[stream] == "" {
					return invalid()
				}
				if _, duplicate := grants[key]; duplicate {
					return invalid()
				}
				grants[key] = rights{grant.View, grant.Diagnose, grant.Reconnect}
			}
		}
		s.access[entry.Login] = grants
	}
	return nil
}

func (s *CameraService) List(ctx context.Context) (api.CameraList, error) {
	out := api.CameraList{Cameras: []api.CameraSummary{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if _, ok := s.access[principal(ctx)]; !ok {
		return out, denied()
	}
	for _, id := range s.order {
		camera := s.cameras[id]
		summary := api.CameraSummary{ID: id, Name: camera.Camera.Name, Availability: cs.StateUnknown, Streams: []api.StreamSummary{}}
		for _, stream := range camera.Streams {
			if s.allowed(ctx, id, stream.ID, view) {
				summary.Streams = append(summary.Streams, streamSummary(stream))
			}
		}
		if len(summary.Streams) > 0 {
			out.Cameras = append(out.Cameras, summary)
		}
	}
	return out, nil
}
func (s *CameraService) Camera(ctx context.Context, id cs.CameraID) (api.CameraDetail, error) {
	list, err := s.List(ctx)
	if err != nil {
		return api.CameraDetail{}, err
	}
	for _, item := range list.Cameras {
		if item.ID == id {
			return item, nil
		}
	}
	return api.CameraDetail{}, denied()
}
func (s *CameraService) Status(ctx context.Context, id cs.CameraID, stream cs.StreamID) (api.StreamStatus, error) {
	item, err := s.require(ctx, id, stream, view)
	if err != nil {
		return api.StreamStatus{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	state, err := s.core.StreamStatus(ctx, item.Camera, stream)
	return publicStatus(stream, state, err), nil
}
func streamSummary(stream cs.Stream) api.StreamSummary {
	return api.StreamSummary{ID: stream.ID, Name: stream.Name, Capabilities: api.MediaCapabilities{Video: stream.Capabilities.Video, Audio: stream.Capabilities.Audio}}
}
func publicStatus(id cs.StreamID, state cs.StreamState, err error) api.StreamStatus {
	out := api.StreamStatus{StreamID: id, State: state.State}
	if !slices.Contains([]cs.State{cs.StateIdle, cs.StateConnecting, cs.StatePlaying, cs.StateReconnecting, cs.StateClosing, cs.StateClosed, cs.StateOffline, cs.StateFailed}, out.State) {
		out.State = cs.StateUnknown
	}
	if state.ActiveSessions != nil {
		n := *state.ActiveSessions
		out.Viewers = &n
	}
	if !state.ObservedAt.IsZero() {
		observed := state.ObservedAt
		out.ObservedAt = &observed
	}
	if err == nil && state.Fault != "" {
		err = cs.NewError(state.Fault, nil)
	}
	if err != nil {
		body := api.PublicError(err).Error
		out.Error = &body
		out.State = cs.StateUnknown
		out.Viewers = nil
	}
	return out
}

func (s *CameraService) addCamera(item config.CatalogCamera) error {
	if !config.ValidID(string(item.Camera.ID)) || item.Camera.Config == nil || item.Camera.PhysicalID == "" || len(item.Streams) == 0 {
		return invalid()
	}
	if _, exists := s.cameras[item.Camera.ID]; exists {
		return invalid()
	}
	copyItem := config.CatalogCamera{Camera: item.Camera, Streams: append([]cs.Stream(nil), item.Streams...), Sources: map[cs.StreamID]cs.SourceKey{}}
	for _, stream := range item.Streams {
		key := item.Sources[stream.ID]
		if key == "" || !config.ValidID(string(stream.ID)) || copyItem.Sources[stream.ID] != "" {
			return invalid()
		}
		copyItem.Sources[stream.ID] = key
		s.aliases[key] = append(s.aliases[key], api.StreamRef{CameraID: item.Camera.ID, StreamID: stream.ID})
	}
	s.cameras[item.Camera.ID] = copyItem
	s.order = append(s.order, item.Camera.ID)
	return nil
}

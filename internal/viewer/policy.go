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
	if err := s.admit(ctx); err != nil {
		return out, err
	}
	for _, id := range s.order {
		if summary, ok := s.summary(ctx, id); ok {
			out.Cameras = append(out.Cameras, summary)
		}
	}
	return out, nil
}

// admit accepts a principal that the catalog grants any access.
func (s *CameraService) admit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := s.access[principal(ctx)]; !ok {
		return denied()
	}
	return nil
}

// summary describes a camera with the profiles this viewer may watch. A camera
// without one is not listed.
func (s *CameraService) summary(ctx context.Context, id cs.CameraID) (api.CameraSummary, bool) {
	camera, ok := s.cameras[id]
	if !ok {
		return api.CameraSummary{}, false
	}
	summary := api.CameraSummary{ID: id, Name: camera.Camera.Name, Streams: []api.StreamSummary{}}
	for _, stream := range camera.Streams {
		if s.allowed(ctx, id, stream.ID, view) {
			summary.Streams = append(summary.Streams, streamSummary(stream))
		}
	}
	if len(summary.Streams) == 0 {
		return api.CameraSummary{}, false
	}
	summary.Availability = s.availability(ctx, camera.Camera, summary.Streams)
	return summary, true
}

// Precedence of a camera's availability over its profiles' states.
var availabilityOrder = []cs.State{cs.StatePlaying, cs.StateFailed, cs.StateOffline, cs.StateReconnecting, cs.StateConnecting}

// availability summarizes the profiles this viewer may watch, whose status the
// viewer may already read, from the core's latest observations: no backend
// I/O and no waiting for a busy source. A latched fault counts as failed. The
// camera is playing when any profile plays, then failed, offline, reconnecting
// or connecting when any profile is; idle when all are idle; otherwise unknown.
func (s *CameraService) availability(ctx context.Context, camera cs.Camera, streams []api.StreamSummary) cs.State {
	seen := map[cs.State]bool{}
	for _, stream := range streams {
		state, err := s.core.StreamSnapshot(ctx, camera, stream.ID)
		if err == nil && state.Fault != "" {
			seen[cs.StateFailed] = true
		} else {
			seen[publicStatus(stream.ID, state, err).State] = true
		}
	}
	return summarize(seen)
}

// summarize applies availabilityOrder to the set of a camera's profile states.
func summarize(seen map[cs.State]bool) cs.State {
	for _, state := range availabilityOrder {
		if seen[state] {
			return state
		}
	}
	if len(seen) == 1 && seen[cs.StateIdle] {
		return cs.StateIdle
	}
	return cs.StateUnknown
}

// Camera summarizes only the requested camera, as List would list it.
func (s *CameraService) Camera(ctx context.Context, id cs.CameraID) (api.CameraDetail, error) {
	if err := s.admit(ctx); err != nil {
		return api.CameraDetail{}, err
	}
	summary, ok := s.summary(ctx, id)
	if !ok {
		return api.CameraDetail{}, denied()
	}
	return summary, nil
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

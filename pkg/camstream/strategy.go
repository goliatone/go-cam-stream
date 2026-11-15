package camstream

import "context"

// Strategy validates its concrete CameraConfig, lists supported profiles and
// resolves a profile to a backend-neutral source. Methods honor cancellation;
// they do not launch workers, start playback or resolve secret values.
// Config type mismatches and unknown profiles return classified errors.
type Strategy interface {
	ID() StrategyID
	Streams(context.Context, Camera) ([]Stream, error)
	Resolve(context.Context, Camera, StreamID) (StreamSpec, error)
}

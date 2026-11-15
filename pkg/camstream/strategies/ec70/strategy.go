package ec70

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

// Strategy is stateless; its zero value is ready to use.
type Strategy struct{}

// Source is an immutable, backend-neutral description produced by Resolve.
// Config returns a copy containing references, never credential values.
type Source struct{ config Config }

func (Source) Kind() camstream.SourceKind { return SourceKind }
func (s Source) Config() Config           { return s.config }
func (Source) String() string             { return "[ec70 source]" }
func (Source) GoString() string           { return "[ec70 source]" }

var _ camstream.Strategy = Strategy{}
var _ camstream.CameraConfig = Config{}
var _ camstream.Source = Source{}

func (Strategy) ID() camstream.StrategyID { return ID }

func (Strategy) Streams(ctx context.Context, camera camstream.Camera) ([]camstream.Stream, error) {
	if _, err := cameraConfig(ctx, camera); err != nil {
		return nil, err
	}
	return []camstream.Stream{{ID: DefaultStream, Name: "Default", Capabilities: capabilities()}}, nil
}

func (Strategy) Resolve(ctx context.Context, camera camstream.Camera, stream camstream.StreamID) (camstream.StreamSpec, error) {
	cfg, err := cameraConfig(ctx, camera)
	if err != nil {
		return camstream.StreamSpec{}, err
	}
	if stream != DefaultStream {
		return camstream.StreamSpec{}, camstream.NewError(camstream.CodeUnsupported, nil)
	}
	// Each part is free of NUL. Include full reference URIs, not their shortened
	// display fingerprints. Alias IDs/names and resolved secret values are absent.
	identity := strings.Join([]string{string(SourceKind), "v1", string(camera.PhysicalID), string(DefaultStream),
		"kasa-mixed", cfg.Host, strconv.Itoa(int(cfg.Port)), cfg.UsernameRef.URI(), cfg.PasswordRef.URI()}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	return camstream.StreamSpec{
		Key:              camstream.SourceKey(string(SourceKind) + ":" + hex.EncodeToString(digest[:])),
		PhysicalCameraID: camera.PhysicalID, Source: Source{config: cfg},
		Capabilities: capabilities(), Constraints: camstream.DeviceConstraints{MaxActiveSources: 1},
	}, nil
}

func cameraConfig(ctx context.Context, camera camstream.Camera) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}
	if !validID(string(camera.ID)) || !validID(string(camera.PhysicalID)) {
		return Config{}, camstream.NewError(camstream.CodeInvalidArgument, nil)
	}
	cfg, ok := camera.Config.(Config)
	if !ok {
		return Config{}, camstream.NewError(camstream.CodeInvalidArgument, nil)
	}
	return cfg.normalized()
}

// Expected H.264/PCMU from the pinned Kasa protocol, not a hardware assertion.
func capabilities() camstream.MediaCapabilities {
	return camstream.MediaCapabilities{Video: true, Audio: true}
}

package rtsp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

// Strategy is stateless; its zero value is ready for use.
type Strategy struct{}

// Source is an immutable, backend-neutral selection. Only Resolve constructs a
// valid Source. Its private config holds one selected profile, never secrets.
type Source struct{ config Config }

func (Source) Kind() camstream.SourceKind { return SourceKind }
func (Source) String() string             { return "[rtsp source]" }
func (Source) GoString() string           { return "[rtsp source]" }

// Config returns an independent copy with exactly the selected profile. An
// encoder must validate it and check the concrete Source type before use.
func (s Source) Config() Config {
	c := s.config
	c.Profiles = slices.Clone(c.Profiles)
	return c
}

var _ camstream.Strategy = Strategy{}
var _ camstream.CameraConfig = Config{}
var _ camstream.Source = Source{}

func (Strategy) ID() camstream.StrategyID { return ID }

func (Strategy) Streams(ctx context.Context, camera camstream.Camera) ([]camstream.Stream, error) {
	cfg, err := cameraConfig(ctx, camera)
	if err != nil {
		return nil, err
	}
	streams := make([]camstream.Stream, len(cfg.Profiles))
	for i, profile := range cfg.Profiles {
		streams[i] = camstream.Stream{ID: profile.ID, Name: profile.Name, Capabilities: capabilities(profile)}
	}
	return streams, nil
}

func (Strategy) Resolve(ctx context.Context, camera camstream.Camera, stream camstream.StreamID) (camstream.StreamSpec, error) {
	cfg, err := cameraConfig(ctx, camera)
	if err != nil {
		return camstream.StreamSpec{}, err
	}
	for _, profile := range cfg.Profiles {
		if profile.ID != stream {
			continue
		}
		// All parts reject NUL. Display/alias/profile IDs and resolved secrets
		// are intentionally absent; equivalent selections share an upstream.
		identity := strings.Join([]string{string(SourceKind), "v1", string(camera.PhysicalID),
			cfg.Host, strconv.Itoa(int(cfg.Port)), profile.Path, profile.Query,
			"tcp", "video", strconv.FormatBool(profile.Audio), cfg.UsernameRef.URI(), cfg.PasswordRef.URI()}, "\x00")
		digest := sha256.Sum256([]byte(identity))
		cfg.Profiles = []Profile{profile}
		return camstream.StreamSpec{
			Key:              camstream.SourceKey(string(SourceKind) + ":" + hex.EncodeToString(digest[:])),
			PhysicalCameraID: camera.PhysicalID, Source: Source{config: cfg},
			Capabilities: capabilities(profile), Constraints: camstream.DeviceConstraints{MaxActiveSources: 1},
		}, nil
	}
	return camstream.StreamSpec{}, camstream.NewError(camstream.CodeUnsupported, nil)
}

func cameraConfig(ctx context.Context, camera camstream.Camera) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}
	if !validID(string(camera.ID)) || !validID(string(camera.PhysicalID)) {
		return Config{}, invalid()
	}
	cfg, ok := camera.Config.(Config)
	if !ok {
		return Config{}, invalid()
	}
	normalized, err := cfg.Normalize()
	if err != nil {
		return Config{}, err
	}
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}
	return normalized, nil
}

func capabilities(profile Profile) camstream.MediaCapabilities {
	return camstream.MediaCapabilities{Video: true, Audio: profile.Audio}
}

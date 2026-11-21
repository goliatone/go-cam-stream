package config

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strconv"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/ec70"
	secrets "github.com/goliatone/go-secrets"
	"github.com/goliatone/go-secrets/providers/environment"
)

// Provider is a host-injected, I/O-free decoder and its matching strategy.
// Endpoint is an opaque canonical device endpoint for duplicate-identity checks.
// It excludes credentials and never appears in public results.
type Provider struct {
	Strategy cs.Strategy
	Decode   func(map[string]any) (Decoded, error)
}
type Decoded struct {
	Config   cs.CameraConfig
	Endpoint string
}
type CatalogCamera struct {
	Camera       cs.Camera
	Streams      []cs.Stream
	Sources      map[cs.StreamID]cs.SourceKey
	ManagedOwner string `json:"-"` // set only by the host's managed overlay
}
type Catalog struct {
	Cameras []CatalogCamera
	Access  []Access
}

// Prepare performs offline validation and returns a new startup snapshot. Custom
// providers must honor context through their Strategy and perform no I/O here.
func Prepare(ctx context.Context, raw Config, providers ...Provider) (Catalog, error) {
	if err := ctx.Err(); err != nil {
		return Catalog{}, err
	}
	if err := raw.Validate(); err != nil {
		return Catalog{}, err
	}
	registered, err := registerProviders(providers)
	if err != nil {
		return Catalog{}, err
	}
	out := Catalog{Cameras: make([]CatalogCamera, 0, len(raw.Cameras)), Access: make([]Access, 0, len(raw.Access))}
	ids := map[cs.CameraID]bool{}
	endpoints := map[string]cs.PhysicalCameraID{}
	devices := map[cs.PhysicalCameraID]string{}
	sources := map[cs.SourceKey]cs.StreamSpec{}
	for _, camera := range raw.Cameras {
		if err := ctx.Err(); err != nil {
			return Catalog{}, err
		}
		if ids[camera.ID] || !ValidID(string(camera.ID)) || !ValidID(string(camera.PhysicalID)) || camera.Name == "" {
			return Catalog{}, invalid()
		}
		ids[camera.ID] = true
		provider, ok := registered[camera.Strategy]
		if !ok {
			return Catalog{}, invalid()
		}
		decoded, err := decodeCamera(camera, provider, endpoints, devices)
		if err != nil {
			return Catalog{}, err
		}
		item, err := prepareCamera(ctx, camera, decoded.Config, provider.Strategy, sources)
		if err != nil {
			return Catalog{}, err
		}
		out.Cameras = append(out.Cameras, item)
	}
	if len(sources) > raw.Limits.MaxSources {
		return Catalog{}, invalid()
	}
	if err := prepareAccess(&out, raw.Access); err != nil {
		return Catalog{}, err
	}
	return out, nil
}
func prepareCamera(ctx context.Context, raw Camera, config cs.CameraConfig, strategy cs.Strategy, sources map[cs.SourceKey]cs.StreamSpec) (CatalogCamera, error) {
	item := CatalogCamera{Camera: cs.Camera{ID: raw.ID, Name: raw.Name, PhysicalID: raw.PhysicalID, Config: config}, Streams: []cs.Stream{}, Sources: map[cs.StreamID]cs.SourceKey{}}
	profiles, err := strategy.Streams(ctx, item.Camera)
	if err != nil {
		return item, invalid()
	}
	available := map[cs.StreamID]cs.Stream{}
	for _, profile := range profiles {
		if !ValidID(string(profile.ID)) {
			return item, invalid()
		}
		if _, duplicate := available[profile.ID]; duplicate {
			return item, invalid()
		}
		available[profile.ID] = profile
	}
	selected := raw.Streams
	if len(selected) == 0 {
		for _, profile := range profiles {
			selected = append(selected, profile.ID)
		}
	}
	if len(selected) == 0 {
		return item, invalid()
	}
	for _, id := range selected {
		profile, ok := available[id]
		if !ok || item.Sources[id] != "" {
			return item, invalid()
		}
		spec, resolveErr := strategy.Resolve(ctx, item.Camera, id)
		if resolveErr != nil {
			return item, invalid()
		}
		if err := recordSource(spec, raw.PhysicalID, profile.Capabilities, sources); err != nil {
			return item, err
		}
		item.Sources[id] = spec.Key
		item.Streams = append(item.Streams, profile)
	}
	return item, nil
}
func prepareAccess(out *Catalog, access []Access) error {
	profiles := map[cs.CameraID]map[cs.StreamID]cs.SourceKey{}
	for _, camera := range out.Cameras {
		profiles[camera.Camera.ID] = camera.Sources
	}
	logins := map[string]bool{}
	for _, entry := range access {
		if !ValidLogin(entry.Login) || logins[entry.Login] {
			return invalid()
		}
		logins[entry.Login] = true
		copyEntry := Access{Login: entry.Login, Grants: []Grant{}}
		seen := map[string]bool{}
		for _, grant := range entry.Grants {
			if len(grant.Streams) == 0 || (!grant.View && !grant.Diagnose && !grant.Reconnect) {
				return invalid()
			}
			for _, id := range grant.Streams {
				key := string(grant.CameraID) + "\x00" + string(id)
				if profiles[grant.CameraID][id] == "" || seen[key] {
					return invalid()
				}
				seen[key] = true
			}
			grant.Streams = append([]cs.StreamID(nil), grant.Streams...)
			copyEntry.Grants = append(copyEntry.Grants, grant)
		}
		out.Access = append(out.Access, copyEntry)
	}
	return nil
}

// EC70Provider decodes Kasa configurations. Only env references are
// enabled in this initial host policy; the reusable strategy/encoder is agnostic.
func EC70Provider() Provider { return Provider{Strategy: ec70.Strategy{}, Decode: decodeEC70} }
func decodeEC70(input map[string]any) (Decoded, error) {
	var wire struct {
		Host        string `json:"host"`
		Port        uint16 `json:"port"`
		UsernameRef string `json:"username_ref"`
		PasswordRef string `json:"password_ref"`
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Decoded{}, invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&wire); err != nil {
		return Decoded{}, invalid()
	}
	username, err := secrets.ParseReference(wire.UsernameRef)
	if err != nil {
		return Decoded{}, invalid()
	}
	password, err := secrets.ParseReference(wire.PasswordRef)
	if err != nil {
		return Decoded{}, invalid()
	}
	validator := environment.New(environment.Config{})
	if validator.ValidateReference(username) != nil || validator.ValidateReference(password) != nil {
		return Decoded{}, invalid()
	}
	config := ec70.Config{Host: wire.Host, Port: wire.Port, UsernameRef: username, PasswordRef: password}
	spec, err := (ec70.Strategy{}).Resolve(context.Background(), cs.Camera{ID: "validation", PhysicalID: "validation", Config: config}, ec70.DefaultStream)
	if err != nil {
		return Decoded{}, invalid()
	}
	source, ok := spec.Source.(ec70.Source)
	if !ok {
		return Decoded{}, invalid()
	}
	config = source.Config()
	return Decoded{Config: config, Endpoint: net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port)))}, nil
}

func registerProviders(providers []Provider) (map[cs.StrategyID]Provider, error) {
	registered := map[cs.StrategyID]Provider{}
	for _, provider := range providers {
		if provider.Strategy == nil || provider.Decode == nil || provider.Strategy.ID() == "" {
			return nil, invalid()
		}
		id := provider.Strategy.ID()
		if _, exists := registered[id]; exists {
			return nil, invalid()
		}
		registered[id] = provider
	}
	return registered, nil
}

func decodeCamera(camera Camera, provider Provider, endpoints map[string]cs.PhysicalCameraID, devices map[cs.PhysicalCameraID]string) (Decoded, error) {
	decoded, err := provider.Decode(camera.Config)
	if err != nil {
		return Decoded{}, invalid()
	}
	if decoded.Config == nil || decoded.Config.StrategyID() != camera.Strategy || decoded.Endpoint == "" {
		return Decoded{}, invalid()
	}
	endpoint := string(camera.Strategy) + "\x00" + decoded.Endpoint
	if old := endpoints[endpoint]; old != "" && old != camera.PhysicalID {
		return Decoded{}, invalid()
	}
	if old := devices[camera.PhysicalID]; old != "" && old != endpoint {
		return Decoded{}, invalid()
	}
	endpoints[endpoint], devices[camera.PhysicalID] = camera.PhysicalID, endpoint
	return decoded, nil
}

func recordSource(spec cs.StreamSpec, physical cs.PhysicalCameraID, capabilities cs.MediaCapabilities, sources map[cs.SourceKey]cs.StreamSpec) error {
	if spec.Key == "" || spec.PhysicalCameraID != physical || spec.Source == nil || spec.Source.Kind() == "" || spec.Constraints.MaxActiveSources < 0 || spec.Capabilities != capabilities {
		return invalid()
	}
	if old, exists := sources[spec.Key]; exists && !sameMetadata(old, spec) {
		return invalid()
	}
	for _, old := range sources {
		if old.PhysicalCameraID == spec.PhysicalCameraID && old.Constraints != spec.Constraints {
			return invalid()
		}
	}
	sources[spec.Key] = spec
	return nil
}
func sameMetadata(a, b cs.StreamSpec) bool {
	return a.PhysicalCameraID == b.PhysicalCameraID && a.Source.Kind() == b.Source.Kind() && a.Constraints == b.Constraints && a.Capabilities == b.Capabilities
}

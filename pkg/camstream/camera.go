// Package camstream defines camera-provider and streaming-backend contracts.
// It has no servers, global registries, process launcher or concrete backend.
// Provider configuration and sources are trusted Go values, not browser DTOs.
package camstream

type CameraID string
type PhysicalCameraID string
type StreamID string
type StrategyID string

// CameraConfig is implemented by a provider's typed configuration. Configuration
// should hold secret references, not resolved credentials. The host owns decoding.
type CameraConfig interface {
	StrategyID() StrategyID
}

// Camera identifies a catalog entry. Aliases of one device share PhysicalID.
// Config is excluded from JSON; HTTP projections belong to the host.
type Camera struct {
	ID         CameraID
	Name       string
	PhysicalID PhysicalCameraID
	Config     CameraConfig `json:"-"`
}

// Stream is a provider-supported, camera-local profile. A provider must not
// invent secondary profiles unsupported by the device.
type Stream struct {
	ID           StreamID
	Name         string
	Capabilities MediaCapabilities
}

// MediaCapabilities describes expected tracks, not successful negotiation or
// browser support. Video-only streams are valid.
type MediaCapabilities struct {
	Video bool
	Audio bool
}

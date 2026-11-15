package camstream

import "context"

type SourceKind string
type SourceKey string

// Source is deliberately unsealed. External providers own its concrete type.
// Kind is a namespaced discriminator (for example "example.test/rtsp"); the
// matching encoder must also check the concrete type. Sources never go to clients.
type Source interface {
	Kind() SourceKind
}

// StreamSpec describes one canonical upstream, independent of catalog aliases.
// Key is a provider-derived identity for physical device, profile, transport and
// credential binding, never the resolved credential. Equivalent aliases must
// yield the same key and definition. Reusing a key with conflicting definitions
// is an error. The core does not compare arbitrary provider values by reflection.
type StreamSpec struct {
	Key              SourceKey
	PhysicalCameraID PhysicalCameraID
	Source           Source `json:"-"`
	Capabilities     MediaCapabilities
	Constraints      DeviceConstraints
}

// SourceEncoder is an optional typed seam for a backend's constructor. Encoded is
// owned by that backend, not the service. Encoders resolve secrets only at their
// trusted boundary and must not include credentials in errors or logs. This
// contract installs no registry and imposes no encoding on alternate backends.
type SourceEncoder[Encoded any] interface {
	Kind() SourceKind
	Encode(context.Context, Source) (Encoded, error)
}

// DeviceConstraints are provider-declared limits for the physical device. Zero
// means unspecified, not unlimited; the MVP service still allows at most one
// active distinct source per device. A positive limit can only tighten host
// policy. Aliases must agree on device constraints; negative limits are invalid.
type DeviceConstraints struct{ MaxActiveSources int }

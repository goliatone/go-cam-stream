package camstream

import (
	"context"
	"time"
)

// Backend is injected into the streaming service. Implementations are
// concurrency-safe and own their sources, sessions, cleanup and failure latches.
// Constructors perform no I/O. EnsureStream records a definition, not an active
// camera connection; concurrent demand for a key shares one acquisition attempt.
// It must reject conflicting redefinitions and must not clear a source fault.
// RemoveStream is idempotent only after all owned resources are confirmed absent;
// it must not remove live sessions or silently clear a fault latch.
type Backend interface {
	EnsureStream(context.Context, StreamSpec) (StreamState, error)
	StreamStatus(context.Context, SourceKey) (StreamState, error)
	RemoveStream(context.Context, SourceKey) error
	CreateSession(context.Context, SessionSpec) (SessionState, error)
	SessionStatus(context.Context, SessionID) (SessionState, error)
	CloseSession(context.Context, SessionID) error
}

// Runtime is an optional lifecycle capability, required by the default managed
// backend. The host explicitly starts it using a runtime-lifetime context and
// closes it with an independent bounded cleanup context. Runtime cancellation
// initiates shutdown; it does not establish completed cleanup. Close returns nil
// only after owned resources are gone. An error requires retaining ownership and
// readiness/capacity restrictions; Close may be retried. No PID/port semantics
// are imposed on other backends.
type Runtime interface {
	Start(context.Context) error
	Close(context.Context) error
}

// SourceResetter is an optional operator capability, required by the default
// backend. The host authorizes it separately from viewer retry. ResetSource
// terminates affected sessions and confirms upstream cleanup before clearing the
// source fault; demand can subsequently reacquire it. Failure retains the latch
// and ownership. It never evicts another source. Aliases are mapped by the service.
type SourceResetter interface {
	ResetSource(context.Context, SourceKey) error
}

// Presence describes observed resource ownership, independently of media state.
// Unknown (including the empty zero value) must never be treated as absent for
// releasing a reservation.
type Presence string

const (
	PresenceUnknown Presence = "unknown"
	PresencePresent Presence = "present"
	PresenceAbsent  Presence = "absent"
)

type StreamState struct {
	Key            SourceKey
	State          State
	Upstream       Presence
	ActiveSessions *int // nil when the count is unknown; zero is an observed zero
	ObservedAt     time.Time
	Fault          ErrorCode // empty when no fault is known; no backend-native error text
}

// TransientRetirer is an optional host-management capability. It forgets only
// the supplied terminal session records after confirmed source cleanup. Ordinary
// playback tombstones retain their existing lifetime. The coordinator guarantees
// these IDs were generated for an exclusively transient source and never reused.
type TransientRetirer interface {
	RetireTransientSessions(context.Context, SourceKey, []SessionID) error
}

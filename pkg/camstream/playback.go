package camstream

import "time"

type SessionID string
type State string

const (
	StateUnknown      State = "unknown"
	StateIdle         State = "idle"
	StateConnecting   State = "connecting"
	StatePlaying      State = "playing"
	StateReconnecting State = "reconnecting"
	StateClosing      State = "closing"
	StateClosed       State = "closed"
	StateOffline      State = "offline"
	StateFailed       State = "failed"
)

// Offer/Answer use complete WebRTC SDP, including gathered ICE candidates.
// MVP does not require a WebSocket or trickle-ICE endpoint. SDP contains necessary
// media candidates; it must contain no camera credentials or management URLs.
type Offer struct{ SDP string }
type Answer struct{ SDP string }

// SessionSpec is supplied by the trusted service, not decoded from browser JSON.
// The service allocates a unique ID before calling CreateSession and reserves
// capacity first. IDs must not be reused across attempts/runtime generations.
// Repeating an ID with the same offer must reuse the result or return conflict,
// never allocate another peer; a changed source/offer returns conflict.
type SessionSpec struct {
	ID        SessionID
	SourceKey SourceKey
	Offer     Offer
}

// SessionState is an observation, not a lease. CreateSession returning an answer
// does not mean media is playing. Active sessions have no fixed TTL/heartbeat.
// On creation error/cancellation, any allocated resources remain addressable by
// the supplied ID until cleanup is confirmed. The service can call CloseSession
// with its cleanup context even if CreateSession returned no usable answer.
// CloseSession is idempotent; nil means owned resources are confirmed absent.
// Errors/unknown observations withhold reservations, never imply release.
// Answer is set only on successful negotiation/reused negotiation, not status
// polling. Status is backend-observed media/resource state, not browser decoding.
type SessionState struct {
	ID         SessionID
	SourceKey  SourceKey
	State      State
	Resources  Presence
	ObservedAt time.Time
	Fault      ErrorCode
	Answer     *Answer
}

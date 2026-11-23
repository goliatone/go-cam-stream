package api

import (
	"errors"
	"strings"
	"time"
)

const (
	ConversationVersion          = 1
	ConversationPage             = "/r/{room_id}"
	ConversationAPIPrefix        = "/conversation-api/v1/rooms/"
	ConversationRoomPath         = ConversationAPIPrefix + "{room_id}"
	ConversationJoinScheme       = "camstream"
	ConversationLifetime         = time.Hour
	ConversationReconnect        = 30 * time.Second
	ConversationHeartbeat        = 12 * time.Second
	ConversationSDPMax           = 32 * 1024
	ConversationNativeCreate     = "conversation_create"
	ConversationNativeLocal      = "conversation_create_local"
	ConversationNativeCheckRoute = "conversation_check_route"
	ConversationNativeConnector  = "conversation_connector_status"
	ConversationNativeReconnect  = "conversation_connector_reconnect"
	ConversationNativeOpen       = "conversation_open_link"
	ConversationNativeStatus     = "conversation_status"
	ConversationNativeEnd        = "conversation_end"
	ConversationNativeLeave      = "conversation_leave"
)

var ErrInvalidConversationRequest = errors.New("invalid conversation request")

type RoomState string
type RoomRole string
type RoomErrorCode string

const (
	RoomWaiting      RoomState = "waiting"
	RoomJoining      RoomState = "joining"
	RoomConnected    RoomState = "connected"
	RoomReconnecting RoomState = "reconnecting"
	RoomEnded        RoomState = "ended"
	RoomExpired      RoomState = "expired"
	RoomFailed       RoomState = "failed"

	RoomHost  RoomRole = "host"
	RoomGuest RoomRole = "guest"

	RoomPermissionDenied   RoomErrorCode = "permission_denied"
	RoomFull               RoomErrorCode = "room_full"
	RoomInvitationInvalid  RoomErrorCode = "invitation_invalid"
	RoomAccessExpired      RoomErrorCode = "expired"
	RoomAccessEnded        RoomErrorCode = "ended"
	RoomDeviceUnavailable  RoomErrorCode = "device_unavailable"
	RoomMediaUnreachable   RoomErrorCode = "media_unreachable"
	RoomIncompatibleClient RoomErrorCode = "incompatible_client"
	RoomStaleGeneration    RoomErrorCode = "stale_generation"
	RoomInvalidRequest     RoomErrorCode = "invalid_request"
)

type RoomRef struct {
	RoomID string `json:"room_id"`
}

func (r RoomRef) Validate() error {
	if !ValidShareID(r.RoomID) {
		return ErrInvalidConversationRequest
	}
	return nil
}

type RoomGeneration uint32

func (g RoomGeneration) Validate() error {
	if g == 0 {
		return ErrInvalidConversationRequest
	}
	return nil
}

type RoomPublication struct {
	Microphone bool `json:"microphone"`
	Camera     bool `json:"camera"`
}

type RoomParticipantView struct {
	Role        RoomRole        `json:"role"`
	Joined      bool            `json:"joined"`
	Publication RoomPublication `json:"publication"`
}

type RoomView struct {
	RoomRef
	Version      int                   `json:"version"`
	State        RoomState             `json:"state"`
	Generation   RoomGeneration        `json:"generation"`
	ExpiresAt    time.Time             `json:"expires_at"`
	Participants []RoomParticipantView `json:"participants"`
	// Present only in the local owner's status projection.
	BrowserCapturePending *bool `json:"browser_capture_pending,omitempty"`
}

type RoomError struct {
	Code      RoomErrorCode `json:"code"`
	Retryable bool          `json:"retryable"`
}

type RoomErrorResponse struct {
	Error RoomError `json:"error"`
}

type RoomRedeemRequest struct {
	Version    int    `json:"version"`
	Invitation string `json:"invitation"`
	GuestToken string `json:"guest_token,omitempty"`
}

func (r RoomRedeemRequest) Validate() error {
	if len(r.Invitation) > ShareInvitationMax || !compactInvitation.MatchString(r.Invitation) ||
		(r.GuestToken != "" && !ValidShareID(r.GuestToken)) {
		return ErrInvalidConversationRequest
	}
	return nil
}

func (RoomRedeemRequest) String() string   { return "[room redemption redacted]" }
func (RoomRedeemRequest) GoString() string { return "[room redemption redacted]" }

type RoomAdmission struct {
	Room  RoomView `json:"room"`
	Role  RoomRole `json:"role"`
	Token string   `json:"token"`
}

func (RoomAdmission) String() string   { return "[room admission redacted]" }
func (RoomAdmission) GoString() string { return "[room admission redacted]" }

type CreatedRoom struct {
	Admission     RoomAdmission `json:"admission"`
	InvitationURL string        `json:"invitation_url"`
}

func (CreatedRoom) String() string   { return "[room creation redacted]" }
func (CreatedRoom) GoString() string { return "[room creation redacted]" }

type RoomJoinRequest struct {
	Generation  RoomGeneration  `json:"generation"`
	Publication RoomPublication `json:"publication"`
}

func (r RoomJoinRequest) Validate() error { return r.Generation.Validate() }

type JoinedRoom struct {
	Room      RoomView              `json:"room"`
	ICE       []ShareICECredentials `json:"ice_servers,omitempty"`
	RelayOnly bool                  `json:"relay_only"`
}

func (JoinedRoom) String() string   { return "[room join result redacted]" }
func (JoinedRoom) GoString() string { return "[room join result redacted]" }

// RoomSignal exchanges a complete SDP offer or answer with gathered ICE
// candidates. The room service checks role, join state and generation.
type RoomSignal struct {
	Generation RoomGeneration `json:"generation"`
	Type       string         `json:"type"`
	SDP        string         `json:"sdp"`
}

func (r RoomSignal) Validate() error {
	if r.Generation.Validate() != nil || (r.Type != "offer" && r.Type != "answer") ||
		len(r.SDP) > ConversationSDPMax || !strings.HasPrefix(r.SDP, "v=0\r\n") {
		return ErrInvalidConversationRequest
	}
	return nil
}

func (RoomSignal) String() string   { return "[room signal redacted]" }
func (RoomSignal) GoString() string { return "[room signal redacted]" }

type RoomPoll struct {
	Room   RoomView    `json:"room"`
	Signal *RoomSignal `json:"signal,omitempty"`
}

type RoomConnectionRequest struct {
	Generation RoomGeneration `json:"generation"`
	Connected  bool           `json:"connected"`
}

func (r RoomConnectionRequest) Validate() error { return r.Generation.Validate() }

type NativeConversationRequest struct {
	Version        int                   `json:"version"`
	ID             string                `json:"id"`
	Method         string                `json:"method"`
	RoomID         string                `json:"room_id,omitempty"`
	InvitationURL  string                `json:"invitation_url,omitempty"`
	SetupProgress  bool                  `json:"setup_progress,omitempty"`
	GuestDeparture *NativeGuestDeparture `json:"guest_departure,omitempty"`
	GuestToken     string                `json:"guest_token,omitempty"`
}

type NativeGuestDeparture struct {
	PageURL string `json:"page_url"`
	Token   string `json:"token"`
}

func (r NativeConversationRequest) Validate() error {
	if r.Version != 1 || len(r.ID) == 0 || len(r.ID) > 64 ||
		(r.SetupProgress && r.Method != ConversationNativeCreate && r.Method != ConversationNativeLocal) ||
		(r.GuestToken != "" && (!ValidShareID(r.GuestToken) || (r.Method != ConversationNativeOpen && r.Method != ConversationNativeLeave))) ||
		(r.GuestDeparture != nil && (r.Method != ConversationNativeLeave ||
			len(r.GuestDeparture.PageURL) == 0 || len(r.GuestDeparture.PageURL) > 2048 || !ValidShareID(r.GuestDeparture.Token))) {
		return ErrInvalidConversationRequest
	}
	switch r.Method {
	case ConversationNativeCreate, ConversationNativeLocal:
		if r.RoomID == "" && r.InvitationURL == "" {
			return nil
		}
	case ConversationNativeConnector, ConversationNativeReconnect:
		if r.RoomID == "" && r.InvitationURL == "" {
			return nil
		}
	case ConversationNativeOpen:
		if r.RoomID == "" && r.InvitationURL != "" && len(r.InvitationURL) <= 8192 {
			return nil
		}
	case ConversationNativeLeave:
		if r.GuestToken != "" {
			if r.GuestDeparture == nil && r.RoomID == "" && r.InvitationURL != "" && len(r.InvitationURL) <= 8192 {
				return nil
			}
		} else if r.InvitationURL == "" {
			return (RoomRef{RoomID: r.RoomID}).Validate()
		}
	case ConversationNativeStatus, ConversationNativeEnd, ConversationNativeCheckRoute:
		if r.InvitationURL == "" {
			return (RoomRef{RoomID: r.RoomID}).Validate()
		}
	}
	return ErrInvalidConversationRequest
}

func (NativeConversationRequest) String() string   { return "[native room request redacted]" }
func (NativeConversationRequest) GoString() string { return "[native room request redacted]" }

// ConversationSetup classifies why the owner could not prepare conversations
// on this Mac. It is operator-only and never carries paths, file contents,
// provider credentials or invitation material. A ready classification makes
// no claim that the origin or relay works from another network.
type ConversationSetup string

const (
	ConversationSetupMissing         ConversationSetup = "not_configured"
	ConversationSetupInvalid         ConversationSetup = "invalid_configuration"
	ConversationSetupUnavailable     ConversationSetup = "runtime_unavailable"
	ConversationSetupFailed          ConversationSetup = "start_failed"
	ConversationSetupPortInUse       ConversationSetup = "listener_in_use"
	ConversationSetupUnreachable     ConversationSetup = "public_route_unreachable"
	ConversationSetupAssets          ConversationSetup = "call_assets_unavailable"
	ConversationSetupRelayConfig     ConversationSetup = "invalid_relay_configuration"
	ConversationSetupRelay           ConversationSetup = "relay_unavailable"
	ConversationSetupConnector       ConversationSetup = "connector_unavailable"
	ConversationSetupConnectorConfig ConversationSetup = "invalid_connector_configuration"
)

// Retryable reports whether trying again without changing settings can help.
func (s ConversationSetup) Retryable() bool {
	return s == ConversationSetupUnavailable || s == ConversationSetupFailed ||
		s == ConversationSetupPortInUse || s == ConversationSetupUnreachable || s == ConversationSetupRelay || s == ConversationSetupConnector
}

type ConversationSetupStage string

const (
	ConversationSetupListenerStage  ConversationSetupStage = "listener"
	ConversationSetupRouteStage     ConversationSetupStage = "public_route"
	ConversationSetupRelayStage     ConversationSetupStage = "relay"
	ConversationSetupConnectorStage ConversationSetupStage = "connector"
)

type CallConnectorState string

const (
	CallConnectorExternal    CallConnectorState = "external"
	CallConnectorStopped     CallConnectorState = "stopped"
	CallConnectorConnecting  CallConnectorState = "connecting"
	CallConnectorConnected   CallConnectorState = "connected"
	CallConnectorUnavailable CallConnectorState = "unavailable"
	CallConnectorSetup       CallConnectorState = "setup_required"
)

// NativeConversationFault is the owner-pipe error for conversation requests.
// Older helpers omit Setup; clients then use neutral recovery text.
type NativeConversationFault struct {
	Code      RoomErrorCode     `json:"code"`
	Retryable bool              `json:"retryable"`
	Setup     ConversationSetup `json:"setup,omitempty"`
	Port      uint16            `json:"port,omitempty"`
}

type NativeConversationPrejoin struct {
	PageURL       string        `json:"page_url"`
	Admission     RoomAdmission `json:"admission"`
	InvitationURL string        `json:"invitation_url,omitempty"`
}

func (NativeConversationPrejoin) String() string   { return "[native room prejoin redacted]" }
func (NativeConversationPrejoin) GoString() string { return "[native room prejoin redacted]" }

// RoomDownload is populated by distribution only once a signed artifact
// and its digest are available. Clients do not launch this URL automatically.
type RoomDownload struct {
	URL     string `json:"url"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/config"
)

const (
	SharePagePath          = "/s/{share_id}"
	ShareAssetPrefix       = "/share-assets/"
	ShareAssetEntry        = "index.html"
	ShareAPIPrefix         = "/share-api/v1/shares/"
	ShareRedeemPath        = ShareAPIPrefix + "{share_id}/redeem"
	ShareViewersPath       = ShareAPIPrefix + "{share_id}/viewers"
	ShareViewerPath        = ShareViewersPath + "/{viewer_id}"
	ShareOfferPath         = ShareViewerPath + "/offer"
	ShareCSRFHeader        = "X-CSRF-Token"
	ShareGuestCookiePrefix = "__Host-camstream-share-"

	ShareCreateCommand      = "sharing.create.v1"
	ShareCopyCommand        = "sharing.copy_link.v1"
	ShareRevokeCommand      = "sharing.revoke.v1"
	ShareRedeemCommand      = "sharing.redeem.v1"
	ShareAdmitViewerCommand = "sharing.admit_viewer.v1"
	ShareOpenViewerCommand  = "sharing.open_viewer.v1"
	ShareCloseViewerCommand = "sharing.close_viewer.v1"

	ShareNativeVersion    = 1
	ShareNativeCreate     = "sharing_create"
	ShareNativeCopy       = "sharing_copy_link"
	ShareNativeStatus     = "sharing_status"
	ShareNativeRevoke     = "sharing_revoke"
	ShareNativeSetup      = "sharing_setup"
	ShareNativeConfigure  = "sharing_configure"
	ShareNativeTestRelay  = "sharing_test_relay"
	ShareNativeHostSleep  = "sharing_host_sleep"
	ShareNativeChanged    = "sharing_changed"
	ShareInvitationMax    = 4096
	ShareOfferMax         = 32 * 1024
	ShareReconnectWindow  = 30 * time.Second
	ShareHealthyStopLimit = 2 * time.Second
)

var ErrInvalidSharingRequest = errors.New("invalid sharing request")

// Durations are selected before creation. The service fixes the deadline from its clock.
type ShareDurationSeconds int

const (
	ShareDuration15Minutes ShareDurationSeconds = 15 * 60
	ShareDuration1Hour     ShareDurationSeconds = 60 * 60
	ShareDuration8Hours    ShareDurationSeconds = 8 * 60 * 60
	ShareDefaultViewers                         = 1
	ShareMaxViewers                             = 3
)

type ShareOptions struct {
	DurationSeconds ShareDurationSeconds `json:"duration_seconds,omitempty"`
	ViewerLimit     int                  `json:"viewer_limit,omitempty"`
}

func (o ShareOptions) WithDefaults() ShareOptions {
	if o.DurationSeconds == 0 {
		o.DurationSeconds = ShareDuration1Hour
	}
	if o.ViewerLimit == 0 {
		o.ViewerLimit = ShareDefaultViewers
	}
	return o
}

func (o ShareOptions) Validate() error {
	o = o.WithDefaults()
	switch o.DurationSeconds {
	case ShareDuration15Minutes, ShareDuration1Hour, ShareDuration8Hours:
	default:
		return ErrInvalidSharingRequest
	}
	if o.ViewerLimit < 1 || o.ViewerLimit > ShareMaxViewers {
		return ErrInvalidSharingRequest
	}
	return nil
}

type CreateShareRequest struct {
	CameraID string       `json:"camera_id"`
	Options  ShareOptions `json:"options"`
}

func (r CreateShareRequest) Validate() error {
	if !config.ValidID(r.CameraID) {
		return ErrInvalidSharingRequest
	}
	return r.Options.Validate()
}

// ShareRef selects a specific generation. A delayed Stop or Copy cannot target
// a share that replaced it on the same camera.
type ShareRef struct {
	ShareID string `json:"share_id"`
}

func (r ShareRef) Validate() error {
	if !ValidShareID(r.ShareID) {
		return ErrInvalidSharingRequest
	}
	return nil
}

// Public handles are independently generated, canonical 32-byte base64url values.
func ValidShareID(value string) bool {
	if len(value) != base64.RawURLEncoding.EncodedLen(32) {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == 32
}

type ShareState string
type ShareMediaState string
type ShareAudience string

const (
	ShareStopped   ShareState = "stopped"
	ShareStarting  ShareState = "starting"
	ShareAvailable ShareState = "available"
	ShareDegraded  ShareState = "degraded"
	ShareExpired   ShareState = "expired"
	ShareRevoked   ShareState = "revoked"
	ShareFailed    ShareState = "error"

	ShareMediaIdle         ShareMediaState = "idle"
	ShareMediaConnecting   ShareMediaState = "connecting"
	ShareMediaLive         ShareMediaState = "live"
	ShareMediaReconnecting ShareMediaState = "reconnecting"
	ShareMediaUnavailable  ShareMediaState = "unavailable"

	SharePublicAudience  ShareAudience = "public"
	ShareTailnetAudience ShareAudience = "private_tailnet"
)

// ShareView is an operator status projection. Link material is returned only
// from authenticated Create or Copy Link calls.
type ShareView struct {
	ShareID   string          `json:"share_id,omitempty"`
	CameraID  string          `json:"camera_id"`
	State     ShareState      `json:"state"`
	Media     ShareMediaState `json:"media"`
	Audience  ShareAudience   `json:"audience,omitempty"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	Viewers   int             `json:"viewers"`
	Limit     int             `json:"viewer_limit"`
	Error     *ShareErrorBody `json:"error,omitempty"`
}

type ShareLink struct {
	URL string `json:"url"`
}

func (ShareLink) String() string   { return "[share link redacted]" }
func (ShareLink) GoString() string { return "[share link redacted]" }

type CreateShareResult struct {
	Share ShareView `json:"share"`
	Link  ShareLink `json:"link"`
}

func (CreateShareResult) String() string   { return "[share creation result redacted]" }
func (CreateShareResult) GoString() string { return "[share creation result redacted]" }

type RedeemShareRequest struct {
	Invitation string `json:"invitation"`
}

func (RedeemShareRequest) String() string   { return "[invitation redacted]" }
func (RedeemShareRequest) GoString() string { return "[invitation redacted]" }

var compactInvitation = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

func (r RedeemShareRequest) Validate() error {
	if len(r.Invitation) > ShareInvitationMax || !compactInvitation.MatchString(r.Invitation) {
		return ErrInvalidSharingRequest
	}
	return nil
}

type RedeemShareResult struct {
	ExpiresAt time.Time `json:"expires_at"`
	CSRFToken string    `json:"csrf_token"`
}

func (RedeemShareResult) String() string   { return "[guest exchange redacted]" }
func (RedeemShareResult) GoString() string { return "[guest exchange redacted]" }

// A first Play sends an empty ViewerID. A reconnect identifies the same tab's
// viewer. Authority still requires the matching guest cookie and share.
type AdmitShareViewerRequest struct {
	ViewerID string `json:"viewer_id,omitempty"`
}

func (r AdmitShareViewerRequest) Validate() error {
	if r.ViewerID != "" && !ValidShareID(r.ViewerID) {
		return ErrInvalidSharingRequest
	}
	return nil
}

// Admit returns bounded ICE credentials before the browser creates its offer.
type ShareICECredentials struct {
	URLs       []string  `json:"urls"`
	Username   string    `json:"username"`
	Credential string    `json:"credential"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (ShareICECredentials) String() string   { return "[ICE credentials redacted]" }
func (ShareICECredentials) GoString() string { return "[ICE credentials redacted]" }

type AdmitShareViewerResult struct {
	ViewerID  string                `json:"viewer_id"`
	AttemptID string                `json:"attempt_id"`
	ExpiresAt time.Time             `json:"expires_at"`
	ICE       []ShareICECredentials `json:"ice_servers,omitempty"`
}

func (AdmitShareViewerResult) String() string   { return "[guest admission redacted]" }
func (AdmitShareViewerResult) GoString() string { return "[guest admission redacted]" }

// Each negotiation gets one attempt. Late cleanup from an old peer cannot
// release a replacement attempt's slot or media.
type ShareViewerAttempt struct {
	AttemptID string `json:"attempt_id"`
}

func (a ShareViewerAttempt) Validate() error {
	if !ValidShareID(a.AttemptID) {
		return ErrInvalidSharingRequest
	}
	return nil
}

type OpenShareViewerRequest struct {
	ShareViewerAttempt
	Offer SDPOffer `json:"offer"`
}

func (OpenShareViewerRequest) String() string   { return "[guest offer redacted]" }
func (OpenShareViewerRequest) GoString() string { return "[guest offer redacted]" }

// Validate covers the envelope. The T05 media boundary parses the SDP and
// requires exactly one receive-only video media section before contacting MediaMTX.
func (r OpenShareViewerRequest) Validate() error {
	if r.Offer.Type != "offer" || len(r.Offer.SDP) > ShareOfferMax || !strings.HasPrefix(r.Offer.SDP, "v=0\r\n") {
		return ErrInvalidSharingRequest
	}
	return r.ShareViewerAttempt.Validate()
}

type OpenShareViewerResult struct {
	Answer SDPAnswer `json:"answer"`
}

func (OpenShareViewerResult) String() string   { return "[guest answer redacted]" }
func (OpenShareViewerResult) GoString() string { return "[guest answer redacted]" }

type ShareErrorCode string

const (
	ShareInvalidRequest     ShareErrorCode = "invalid_request"
	ShareInvalidInvitation  ShareErrorCode = "invalid_invitation"
	ShareExpiryReached      ShareErrorCode = "expired"
	ShareAccessRevoked      ShareErrorCode = "revoked"
	ShareViewerAtCapacity   ShareErrorCode = "capacity_reached"
	ShareAlreadyActive      ShareErrorCode = "share_already_active"
	ShareStaleAttempt       ShareErrorCode = "stale_viewer_attempt"
	ShareCameraUnavailable  ShareErrorCode = "camera_unavailable"
	ShareIngressUnavailable ShareErrorCode = "ingress_unavailable"
	ShareRelayUnavailable   ShareErrorCode = "relay_unavailable"
	ShareMediaUnreachable   ShareErrorCode = "media_unreachable"
	ShareUnsupportedOffer   ShareErrorCode = "unsupported_offer"
	SharePermissionDenied   ShareErrorCode = "permission_denied"
	ShareRateLimited        ShareErrorCode = "rate_limited"
)

// Project only allowlisted copy from the service into this response.
type ShareErrorBody struct {
	Code      ShareErrorCode `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
}

type ShareErrorResponse struct {
	Error ShareErrorBody `json:"error"`
}

type ShareSetupState string

const (
	ShareSetupNeeded ShareSetupState = "setup_needed"
	ShareSetupReady  ShareSetupState = "ready"
	ShareSetupError  ShareSetupState = "error"
)

type ShareIngressCapability struct {
	Mode     string        `json:"mode"`
	Audience ShareAudience `json:"audience"`
}

type ShareSetupView struct {
	IngressModes []ShareIngressCapability `json:"ingress_modes"`
	IngressMode  string                   `json:"ingress_mode"`
	Ingress      ShareSetupState          `json:"ingress_state"`
	Relay        ShareSetupState          `json:"relay_state"`
	Saved        *ShareSavedSettings      `json:"saved,omitempty"`
	Error        *ShareErrorBody          `json:"error,omitempty"`
}

// ShareSavedSettings is the operator readback, with explicit credential presence.
type ShareSavedSettings struct {
	PublicBaseURL string                  `json:"public_base_url,omitempty"`
	GuestListen   string                  `json:"guest_listen,omitempty"`
	RelayMode     config.SharingRelayMode `json:"relay_mode"`
	RelayURLs     []string                `json:"relay_urls,omitempty"`
	CredentialSet bool                    `json:"relay_credential_set"`
}

type NativeShareReady struct {
	Protocol      int `json:"protocol"`
	Preview       int `json:"preview,omitempty"`
	Audio         int `json:"audio,omitempty"`
	Sharing       int `json:"sharing,omitempty"`
	Conversations int `json:"conversations,omitempty"`
}

type NativeShareEvent[T any] struct {
	Version int    `json:"version"`
	Event   string `json:"event"`
	Result  T      `json:"result"`
}

type NativeShareReply[T any] struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Result  *T              `json:"result,omitempty"`
	Error   *ShareErrorBody `json:"error,omitempty"`
}

// MarshalJSON keeps ingress_modes an array even when no adapters are registered.
func (s ShareSetupView) MarshalJSON() ([]byte, error) {
	type wire ShareSetupView
	if s.IngressModes == nil {
		s.IngressModes = []ShareIngressCapability{}
	}
	return json.Marshal(wire(s))
}

// NativeShareRequest is the additive version-one helper envelope. Capability
// discovery advertises ready.result.sharing before the app sends these methods.
type NativeShareRequest struct {
	Version int                   `json:"version"`
	ID      string                `json:"id"`
	Method  string                `json:"method"`
	Sharing *NativeShareArguments `json:"sharing,omitempty"`
}

type NativeShareArguments struct {
	CameraID         string                  `json:"camera_id,omitempty"`
	ShareID          string                  `json:"share_id,omitempty"`
	Options          *ShareOptions           `json:"options,omitempty"`
	Settings         *config.Sharing         `json:"settings,omitempty"`
	RelayCredentials *NativeRelayCredentials `json:"relay_credentials,omitempty"`
}

// NativeRelayCredentials is write-only on the owner pipe. The helper resolves
// these values to a host-private reference before saving settings.
type NativeRelayCredentials struct {
	KeyID        string `json:"key_id,omitempty"`
	APIToken     string `json:"api_token,omitempty"`
	SharedSecret string `json:"shared_secret,omitempty"`
}

func (NativeRelayCredentials) String() string   { return "[relay input redacted]" }
func (NativeRelayCredentials) GoString() string { return "[relay input redacted]" }
func (NativeShareArguments) String() string     { return "[sharing arguments redacted]" }
func (NativeShareArguments) GoString() string   { return "[sharing arguments redacted]" }

func (c NativeRelayCredentials) valid(mode config.SharingRelayMode) bool {
	valid := func(value string, max int) bool {
		return len(value) > 0 && len(value) <= max && strings.IndexFunc(value, func(r rune) bool {
			return r < 0x21 || r > 0x7e
		}) < 0
	}
	switch mode {
	case config.SharingRelayCloudflare:
		return config.ValidID(c.KeyID) && valid(c.APIToken, 4096) && c.SharedSecret == ""
	case config.SharingRelayCoturn:
		return c.KeyID == "" && c.APIToken == "" && valid(c.SharedSecret, 4096)
	}
	return false
}

// Validate checks payload shape. The native owner is derived from the local
// app session in T06 and is never accepted as a payload field.
func (r NativeShareRequest) Validate() error {
	if r.Version != 1 || !config.ValidID(r.ID) {
		return ErrInvalidSharingRequest
	}
	a := r.Sharing
	if r.Method == ShareNativeSetup || r.Method == ShareNativeTestRelay || r.Method == ShareNativeHostSleep {
		if a == nil {
			return nil
		}
		return ErrInvalidSharingRequest
	}
	if a == nil {
		return ErrInvalidSharingRequest
	}
	return a.validate(r.Method)
}

func (a NativeShareArguments) validate(method string) error {
	switch method {
	case ShareNativeCreate:
		if a != (NativeShareArguments{CameraID: a.CameraID, Options: a.Options}) {
			return ErrInvalidSharingRequest
		}
		request := CreateShareRequest{CameraID: a.CameraID}
		if a.Options != nil {
			request.Options = *a.Options
		}
		return request.Validate()
	case ShareNativeStatus:
		if a == (NativeShareArguments{CameraID: a.CameraID}) && config.ValidID(a.CameraID) {
			return nil
		}
	case ShareNativeCopy, ShareNativeRevoke:
		if a == (NativeShareArguments{ShareID: a.ShareID}) && ValidShareID(a.ShareID) {
			return nil
		}
	case ShareNativeConfigure:
		if a != (NativeShareArguments{Settings: a.Settings, RelayCredentials: a.RelayCredentials}) || a.Settings == nil {
			return ErrInvalidSharingRequest
		}
		if a.RelayCredentials != nil && (a.Settings.Relay.CredentialRef != "" || !a.RelayCredentials.valid(a.Settings.Relay.Mode)) {
			return ErrInvalidSharingRequest
		}
		return a.Settings.ValidateSetup()
	}
	return ErrInvalidSharingRequest
}

// Package sharing owns the camera sharing application contracts.
package sharing

import (
	"context"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

const (
	InvitationVersion     = 1
	InvitationPurpose     = "camera_view"
	VideoMaxWidth         = 1280
	VideoMaxHeight        = 720
	VideoMaxFramesPerSec  = 30
	VideoTargetBitsPerSec = 2_000_000
)

// InvitationClaims are the precise v1 securelink dat payload. The share ID is
// its generation and is replaced on every new share. The service also compares
// the digest of the exact issued token and its own fixed deadline.
type InvitationClaims struct {
	Version int    `json:"v"`
	Purpose string `json:"purpose"`
	ShareID string `json:"share_id"`
	Nonce   string `json:"nonce"`
}

type InvitationTokens interface {
	Issue(claims InvitationClaims, ttl time.Duration) (Secret, error)
	Verify(token Secret) (InvitationClaims, error)
}

// Secret is application-only. An adapter explicitly converts it to a string
// when setting an authorized response cookie or calling a private provider.
type Secret string

func (Secret) String() string   { return "[secret redacted]" }
func (Secret) GoString() string { return "[secret redacted]" }

type RedeemedGuest struct {
	Result api.RedeemShareResult `json:"result"`
	Cookie Secret                `json:"-"`
}

// Workflows is the service boundary used by command handlers. Authorities are
// verified by the transport and carried in trusted context. The service owns
// authorization, deadlines, capacity, attempts and cleanup for every caller.
type Workflows interface {
	Create(context.Context, CreateShare) (api.CreateShareResult, error)
	CopyLink(context.Context, CopyShareLink) (api.ShareLink, error)
	Revoke(context.Context, RevokeShare) (api.ShareView, error)
	Redeem(context.Context, RedeemShare) (RedeemedGuest, error)
	AdmitViewer(context.Context, AdmitShareViewer) (api.AdmitShareViewerResult, error)
	OpenViewer(context.Context, OpenShareViewer) (api.OpenShareViewerResult, error)
	CloseViewer(context.Context, CloseShareViewer) error
}

type IngressBinding struct {
	Origin   string
	Audience api.ShareAudience
}

// Ingress represents explicitly configured ingress, independent of relay or
// invitation policy. External HTTPS validates its binding and leaves the
// operator's route under operator control. Stop only acts on owned resources.
type Ingress interface {
	Start(context.Context) (IngressBinding, error)
	Stop(context.Context) error
}

// RelayIssuer returns bounded credentials for both ICE endpoints. The service
// issues only for an admitted attempt and never renews beyond its deadline.
type RelayIssuer interface {
	Issue(context.Context, RelayGrant) (RelayCredentials, error)
}

// SourceID is a host-resolved source reference. The peer and any provider
// resource URL stay private to the service and media adapter.
type MediaOffer struct {
	SourceID string
	Attempt  ViewerAttempt
	Offer    api.SDPOffer
	Relay    *HostRelay `json:"-"` // nil for direct-only playback
}

func (MediaOffer) String() string   { return "[media offer redacted]" }
func (MediaOffer) GoString() string { return "[media offer redacted]" }

type MediaPeer struct {
	ID     string
	Answer api.SDPAnswer
}

func (MediaPeer) String() string   { return "[media peer redacted]" }
func (MediaPeer) GoString() string { return "[media peer redacted]" }

// Media opens a peer from a video-only path and rejects offers with additional
// media sections. It checks HostICE before opening and applies any relay
// credentials only to that peer. Close waits for owned peer teardown and is idempotent.
type Media interface {
	Open(context.Context, MediaOffer) (MediaPeer, error)
	Close(context.Context, string) error
}

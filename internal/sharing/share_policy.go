package sharing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"github.com/goliatone/go-cam-stream/data/api"
)

type authorityKey struct{}
type authority struct {
	operator bool
	cookie   Secret
	csrf     Secret
}

// WithOperator is used only by the authenticated native owner adapter.
func WithOperator(ctx context.Context) context.Context {
	return context.WithValue(ctx, authorityKey{}, authority{operator: true})
}

// WithGuest supplies the browser cookie and CSRF header from the trusted adapter.
func WithGuest(ctx context.Context, cookie, csrf Secret) context.Context {
	return context.WithValue(ctx, authorityKey{}, authority{cookie: cookie, csrf: csrf})
}

func caller(ctx context.Context) authority {
	current, _ := ctx.Value(authorityKey{}).(authority)
	return current
}

func handle() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:]) // crypto/rand.Read fills the buffer or terminates.
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func digest(raw string) [sha256.Size]byte { return sha256.Sum256([]byte(raw)) }

type Fault api.ShareErrorCode

func (f Fault) Error() string { return string(f) }

func fault(code api.ShareErrorCode) Fault { return Fault(code) }

func ErrorProjection(err error) api.ShareErrorBody {
	code := api.ShareMediaUnreachable
	var failure Fault
	if errors.As(err, &failure) {
		code = api.ShareErrorCode(failure)
	} else if errors.Is(err, api.ErrInvalidSharingRequest) {
		code = api.ShareInvalidRequest
	}
	switch code {
	case api.ShareRateLimited:
		return api.ShareErrorBody{Code: code, Message: "Wait a moment before trying again.", Retryable: true}
	case api.ShareInvalidRequest:
		return api.ShareErrorBody{Code: code, Message: "Check the sharing request."}
	case api.ShareInvalidInvitation, api.SharePermissionDenied:
		return api.ShareErrorBody{Code: code, Message: "This viewing request is unavailable."}
	case api.ShareExpiryReached:
		return api.ShareErrorBody{Code: code, Message: "This viewing invitation has expired."}
	case api.ShareAccessRevoked:
		return api.ShareErrorBody{Code: code, Message: "This viewing invitation is no longer available."}
	case api.ShareViewerAtCapacity:
		return api.ShareErrorBody{Code: code, Message: "This camera is already being viewed.", Retryable: true}
	case api.ShareStaleAttempt:
		return api.ShareErrorBody{Code: code, Message: "Restart the viewing connection.", Retryable: true}
	case api.ShareAlreadyActive:
		return api.ShareErrorBody{Code: code, Message: "This camera already has an active link."}
	case api.ShareCameraUnavailable:
		return api.ShareErrorBody{Code: code, Message: "Start the camera before sharing.", Retryable: true}
	case api.ShareIngressUnavailable:
		return api.ShareErrorBody{Code: code, Message: "Check the configured HTTPS guest route.", Retryable: true}
	case api.ShareRelayUnavailable:
		return api.ShareErrorBody{Code: code, Message: "Check the configured connection relay.", Retryable: true}
	case api.ShareUnsupportedOffer:
		return api.ShareErrorBody{Code: code, Message: "This viewing connection requires video-only WebRTC."}
	default:
		return api.ShareErrorBody{Code: api.ShareMediaUnreachable, Message: "The camera media connection is unavailable.", Retryable: true}
	}
}

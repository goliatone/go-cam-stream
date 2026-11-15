// Package api owns the reference application's browser/CLI JSON contracts.
// It is not the reusable streaming service and installs no routes or middleware.
package api

import (
	"net/http"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

const (
	ContractVersion  = "v1"
	ServeLoginHeader = "Tailscale-User-Login"
	CSRFPath         = "/api/csrf"
	CSRFHeader       = "X-CSRF-Token"
	DiagnoseCommand  = "camera.diagnose.v1"
	ReconnectCommand = "camera.reconnect.v1"
)

type MediaCapabilities struct {
	Video bool `json:"video"`
	Audio bool `json:"audio"`
}

type StreamSummary struct {
	ID           camstream.StreamID `json:"id"`
	Name         string             `json:"name"`
	Capabilities MediaCapabilities  `json:"capabilities"`
}

type CameraSummary struct {
	ID           camstream.CameraID `json:"id"`
	Name         string             `json:"name"`
	Availability camstream.State    `json:"availability"`
	Streams      []StreamSummary    `json:"streams"`
}

type CameraList struct {
	Cameras []CameraSummary `json:"cameras"`
}
type CameraDetail = CameraSummary
type StreamList struct {
	Streams []StreamSummary `json:"streams"`
}

type StreamStatus struct {
	StreamID   camstream.StreamID `json:"stream_id"`
	State      camstream.State    `json:"state"`
	Viewers    *int               `json:"viewers"`     // null means unknown, never zero by inference
	ObservedAt *time.Time         `json:"observed_at"` // null until an observation exists
	Error      *ErrorBody         `json:"error,omitempty"`
}

type CameraStatus struct {
	CameraID camstream.CameraID `json:"camera_id"`
	Streams  []StreamStatus     `json:"streams"`
}

type SDPOffer struct {
	Type string `json:"type"` // exactly "offer"
	SDP  string `json:"sdp"`
}
type SDPAnswer struct {
	Type string `json:"type"` // exactly "answer"
	SDP  string `json:"sdp"`
}

type CreateSessionRequest struct {
	Offer SDPOffer `json:"offer"`
}
type Playback struct {
	Protocol string    `json:"protocol"` // exactly "webrtc"
	Answer   SDPAnswer `json:"answer"`
}
type Session struct {
	ID       camstream.SessionID `json:"id"`
	CameraID camstream.CameraID  `json:"camera_id"`
	StreamID camstream.StreamID  `json:"stream_id"`
	State    camstream.State     `json:"state"`
	Playback Playback            `json:"playback"`
}

// Operator bodies contain no actor fields. The host establishes authority and
// takes camera_id from the route before creating typed service-backed commands.
type DiagnoseRequest struct {
	StreamID camstream.StreamID `json:"stream_id,omitempty"`
}
type CheckState string

const (
	CheckUnknown CheckState = "unknown"
	CheckPassed  CheckState = "passed"
	CheckFailed  CheckState = "failed"
)

// Diagnostic checks report sanitized observations, never raw provider output.
// Unknown means not established; successful dispatch alone is not a passed check.
type DiagnosticCheck struct {
	Name  string     `json:"name"` // source, backend or media
	State CheckState `json:"state"`
	Error *ErrorBody `json:"error,omitempty"`
}
type StreamDiagnosis struct {
	StreamStatus
	Capabilities MediaCapabilities `json:"capabilities"`
	Checks       []DiagnosticCheck `json:"checks"`
}
type DiagnoseResponse struct {
	CameraID camstream.CameraID `json:"camera_id"`
	Streams  []StreamDiagnosis  `json:"streams"`
}
type StreamRef struct {
	CameraID camstream.CameraID `json:"camera_id"`
	StreamID camstream.StreamID `json:"stream_id"`
}
type ReconnectResponse struct {
	Affected       []StreamRef           `json:"affected"`
	ClosedSessions []camstream.SessionID `json:"closed_sessions"`
}

// CSRFResponse matches go-auth/middleware/csrf.RegisterRoutes, including its
// snake_case metadata. The token is bound to the authenticated principal by the
// package middleware; it is not an authentication credential or a camera secret.
type CSRFResponse struct {
	Token      string `json:"token"`
	FieldName  string `json:"field_name"`
	HeaderName string `json:"header_name"`
}

type ErrorBody struct {
	Code      camstream.ErrorCode `json:"code"`
	Message   string              `json:"message"`
	Retryable bool                `json:"retryable"`
}
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// PublicError always uses allowlisted text, never a provider's raw Message,
// Source, Metadata or diagnostic rendering. Caller must not call it for success.
func PublicError(err error) ErrorResponse {
	info := camstream.DescribeError(err)
	if err == nil {
		info = camstream.DescribeError(camstream.NewError(camstream.CodeInternal, nil))
	}
	return ErrorResponse{Error: ErrorBody{Code: info.Code, Message: info.Message, Retryable: info.Retryable}}
}

func ErrorStatus(code camstream.ErrorCode) int {
	switch code {
	case camstream.CodeInvalidArgument, camstream.CodeUnsupported, camstream.CodeUnsupportedBrowser:
		return http.StatusBadRequest
	case camstream.CodeNotFound:
		return http.StatusNotFound
	case camstream.CodeAccessDenied, camstream.CodeCSRFInvalid:
		return http.StatusForbidden
	case camstream.CodeConflict, camstream.CodeCameraBusy, camstream.CodeControllerConflict:
		return http.StatusConflict
	case camstream.CodeCapacityExceeded:
		return http.StatusTooManyRequests
	case camstream.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case camstream.CodeCanceled:
		return http.StatusRequestTimeout
	case camstream.CodeAuthenticationFailed, camstream.CodeCameraUnreachable, camstream.CodeStreamUnavailable,
		camstream.CodeBackendUnavailable, camstream.CodeSourceUnavailable, camstream.CodeServerUnavailable,
		camstream.CodeCleanupIncomplete:
		return http.StatusServiceUnavailable
	case camstream.CodeSignalingFailed, camstream.CodeMediaFailed:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

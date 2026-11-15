package camstream

import (
	"context"
	"errors"

	gerrors "github.com/goliatone/go-errors"
)

type ErrorCode string

const (
	CodeInvalidArgument      ErrorCode = "invalid_argument"
	CodeNotFound             ErrorCode = "not_found"
	CodeConflict             ErrorCode = "conflict"
	CodeAuthenticationFailed ErrorCode = "authentication_failed"
	CodeCameraUnreachable    ErrorCode = "camera_unreachable"
	CodeStreamUnavailable    ErrorCode = "stream_unavailable"
	CodeBackendUnavailable   ErrorCode = "backend_unavailable"
	CodeSourceUnavailable    ErrorCode = "source_unavailable"
	CodeSignalingFailed      ErrorCode = "signaling_failed"
	CodeMediaFailed          ErrorCode = "media_connection_failed"
	CodeUnsupported          ErrorCode = "unsupported_capability"
	CodeUnsupportedBrowser   ErrorCode = "unsupported_browser"
	CodeCapacityExceeded     ErrorCode = "capacity_exceeded"
	CodeCameraBusy           ErrorCode = "camera_busy"
	CodeCleanupIncomplete    ErrorCode = "cleanup_incomplete"
	CodeControllerConflict   ErrorCode = "controller_conflict"
	CodeServerUnavailable    ErrorCode = "server_unavailable"
	CodeAccessDenied         ErrorCode = "access_denied"
	CodeCSRFInvalid          ErrorCode = "csrf_invalid"
	CodeCanceled             ErrorCode = "canceled"
	CodeDeadlineExceeded     ErrorCode = "deadline_exceeded"
	CodeInternal             ErrorCode = "internal"
)

// ErrorInfo is an allowlisted description, never a rendering of a cause,
// metadata, stack, secret reference, provider response or caller-supplied text.
// Retryable allows viewer-level retry; it never authorizes clearing a source
// fault or releasing reservations. Unknown cleanup is deliberately not retryable.
type ErrorInfo struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	Category  gerrors.Category
}

var errorCatalog = map[ErrorCode]ErrorInfo{
	CodeInvalidArgument:      {CodeInvalidArgument, "Invalid request.", false, gerrors.CategoryValidation},
	CodeNotFound:             {CodeNotFound, "Resource not found.", false, gerrors.CategoryNotFound},
	CodeConflict:             {CodeConflict, "Request conflicts with existing state.", false, gerrors.CategoryConflict},
	CodeAuthenticationFailed: {CodeAuthenticationFailed, "Source authentication failed; operator action is required.", false, gerrors.CategoryExternal},
	CodeCameraUnreachable:    {CodeCameraUnreachable, "Camera is unreachable.", false, gerrors.CategoryExternal},
	CodeStreamUnavailable:    {CodeStreamUnavailable, "Stream is unavailable.", false, gerrors.CategoryExternal},
	CodeBackendUnavailable:   {CodeBackendUnavailable, "Streaming backend is unavailable.", true, gerrors.CategoryExternal},
	CodeSourceUnavailable:    {CodeSourceUnavailable, "Source is unavailable; operator action is required.", false, gerrors.CategoryExternal},
	CodeSignalingFailed:      {CodeSignalingFailed, "Playback negotiation failed.", true, gerrors.CategoryExternal},
	CodeMediaFailed:          {CodeMediaFailed, "Playback media connection failed.", true, gerrors.CategoryExternal},
	CodeUnsupported:          {CodeUnsupported, "Requested capability is not supported.", false, gerrors.CategoryValidation},
	CodeUnsupportedBrowser:   {CodeUnsupportedBrowser, "Browser capability is not supported.", false, gerrors.CategoryValidation},
	CodeCapacityExceeded:     {CodeCapacityExceeded, "Viewer capacity is exhausted.", true, gerrors.CategoryConflict},
	CodeCameraBusy:           {CodeCameraBusy, "Camera is using another source profile.", true, gerrors.CategoryConflict},
	CodeCleanupIncomplete:    {CodeCleanupIncomplete, "Resource cleanup is not confirmed.", false, gerrors.CategoryOperation},
	CodeControllerConflict:   {CodeControllerConflict, "Another controller owns this installation.", false, gerrors.CategoryConflict},
	CodeServerUnavailable:    {CodeServerUnavailable, "Application server is unavailable.", true, gerrors.CategoryExternal},
	CodeAccessDenied:         {CodeAccessDenied, "Access denied.", false, gerrors.CategoryAuthz},
	CodeCSRFInvalid:          {CodeCSRFInvalid, "Request verification failed; obtain a new CSRF token.", false, gerrors.CategoryAuthz},
	CodeCanceled:             {CodeCanceled, "Operation canceled.", false, gerrors.CategoryOperation},
	CodeDeadlineExceeded:     {CodeDeadlineExceeded, "Operation deadline exceeded.", true, gerrors.CategoryOperation},
	CodeInternal:             {CodeInternal, "An internal error occurred.", false, gerrors.CategoryInternal},
}

// NewError returns a classified error with a fixed safe message. The cause is
// retained for errors.Is/As; it must not be serialized/logged at public boundaries.
// Unknown codes normalize to internal. Adapters must use authentication_failed
// only with reliable evidence, never infer it from missing media.
func NewError(code ErrorCode, cause error) *gerrors.Error {
	info := errorInfo(code)
	err := gerrors.New(info.Message, info.Category).WithTextCode(string(info.Code))
	err.Source = cause
	return err
}

// DescribeError classifies the outer structured error first, preserving e.g.
// cleanup_incomplete even when its cause is a context deadline. A nil error has
// no description. Unclassified errors become internal, with no raw text exposed.
func DescribeError(err error) ErrorInfo {
	if err == nil {
		return ErrorInfo{}
	}
	var structured *gerrors.Error
	if errors.As(err, &structured) && structured != nil {
		return errorInfo(ErrorCode(structured.TextCode))
	}
	if errors.Is(err, context.Canceled) {
		return errorInfo(CodeCanceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errorInfo(CodeDeadlineExceeded)
	}
	return errorInfo(CodeInternal)
}

func errorInfo(code ErrorCode) ErrorInfo {
	if info, ok := errorCatalog[code]; ok {
		return info
	}
	return errorCatalog[CodeInternal]
}

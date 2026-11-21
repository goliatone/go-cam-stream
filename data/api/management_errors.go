package api

import (
	"errors"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type ManagementErrorCode string

const (
	CodeDraftLost                  ManagementErrorCode = "draft_lost"
	CodeDraftExpired               ManagementErrorCode = "draft_expired"
	CodeTestExpired                ManagementErrorCode = "test_expired"
	CodeRevisionConflict           ManagementErrorCode = "draft_revision_conflict"
	CodeCatalogConflict            ManagementErrorCode = "catalog_revision_conflict"
	CodeOperationConflict          ManagementErrorCode = "operation_conflict"
	CodeOperationInProgress        ManagementErrorCode = "operation_in_progress"
	CodeSaveUnknown                ManagementErrorCode = "save_outcome_unknown"
	CodeStorageUnavailable         ManagementErrorCode = "storage_unavailable"
	CodeRecoveryStorageUnavailable ManagementErrorCode = "recovery_storage_unavailable"
	CodeReceiptExpired             ManagementErrorCode = "receipt_expired"
	CodeRecoveryLost               ManagementErrorCode = "recovery_lost"
	CodeDestinationDenied          ManagementErrorCode = "destination_denied"
	CodeAddressUnavailable         ManagementErrorCode = "address_unavailable"
	CodeIdentityUnverified         ManagementErrorCode = "identity_unverified"
	CodeDuplicateCamera            ManagementErrorCode = "duplicate_camera"
	CodeCredentialsRequired        ManagementErrorCode = "credentials_required"
	CodeCredentialsChanged         ManagementErrorCode = "credentials_changed"
	CodeImportInvalid              ManagementErrorCode = "import_invalid"
	CodeEvidenceUnavailable        ManagementErrorCode = "playback_evidence_unavailable"
	CodeEvidenceInsufficient       ManagementErrorCode = "playback_evidence_insufficient"
)

type ManagementErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	CameraID  string `json:"camera_id,omitempty"`
}
type ManagementErrorResponse struct {
	Error ManagementErrorBody `json:"error"`
}

// ManagementError renders only an allowlisted code/message. A command boundary
// may retain a cause for errors.Is/As; it is never included in public responses.
type ManagementError struct {
	Code     ManagementErrorCode
	cause    error
	cameraID string
}

func (e *ManagementError) Error() string               { return managementErrorBody(e.Code).Message }
func ManagementFailure(code ManagementErrorCode) error { return &ManagementError{Code: code} }

// DuplicateManagedCamera is used after authorization to open this camera's settings.
func DuplicateManagedCamera(id string) error {
	return &ManagementError{Code: CodeDuplicateCamera, cameraID: id}
}

func (e *ManagementError) Unwrap() error { return e.cause }

// ManagementCommandFailure preserves the public domain classification across
// structured dispatcher wrappers without exposing their messages or metadata.
func ManagementCommandFailure(err error) error {
	if err == nil {
		return nil
	}
	body := ManagementPublicError(err).Error
	return &ManagementError{Code: ManagementErrorCode(body.Code), cause: err, cameraID: body.CameraID}
}
func managementErrorBody(code ManagementErrorCode) ManagementErrorBody {
	switch code {
	case CodeDraftLost:
		return ManagementErrorBody{Code: string(code), Message: "This draft was lost when the server restarted. Re-enter the camera.", Retryable: false}
	case CodeDraftExpired:
		return ManagementErrorBody{Code: string(code), Message: "This draft has expired. Create a new draft.", Retryable: false}
	case CodeTestExpired:
		return ManagementErrorBody{Code: string(code), Message: "Test the current draft again before saving.", Retryable: false}
	case CodeRevisionConflict:
		return ManagementErrorBody{Code: string(code), Message: "Refresh the current draft before continuing.", Retryable: false}
	case CodeCatalogConflict:
		return ManagementErrorBody{Code: string(code), Message: "Refresh the catalog before explicitly rebasing this save.", Retryable: false}
	case CodeOperationConflict:
		return ManagementErrorBody{Code: string(code), Message: "This operation key already identifies a different request.", Retryable: false}
	case CodeOperationInProgress:
		return ManagementErrorBody{Code: string(code), Message: "Wait for the current operation to finish.", Retryable: true}
	case CodeSaveUnknown:
		return ManagementErrorBody{Code: string(code), Message: "The save outcome is unknown. Look up the original operation.", Retryable: true}
	case CodeStorageUnavailable:
		return ManagementErrorBody{Code: string(code), Message: "Private camera storage is unavailable.", Retryable: true}
	case CodeRecoveryStorageUnavailable:
		return ManagementErrorBody{Code: string(code), Message: "Enable browser site storage before managing cameras.", Retryable: false}
	case CodeReceiptExpired:
		return ManagementErrorBody{Code: string(code), Message: "Resolve the original camera creation before starting another save.", Retryable: false}
	case CodeRecoveryLost:
		return ManagementErrorBody{Code: string(code), Message: "Refresh source status before explicitly reconnecting again.", Retryable: false}
	case CodeDestinationDenied:
		return ManagementErrorBody{Code: string(code), Message: "The initial camera address is not permitted.", Retryable: false}
	case CodeAddressUnavailable:
		return ManagementErrorBody{Code: string(code), Message: "The initial camera address could not be verified.", Retryable: true}
	case CodeIdentityUnverified:
		return ManagementErrorBody{Code: string(code), Message: "An existing camera identity could not be verified.", Retryable: true}
	case CodeDuplicateCamera:
		return ManagementErrorBody{Code: string(code), Message: "This address overlaps an existing camera or reserved test.", Retryable: false}
	case CodeCredentialsRequired:
		return ManagementErrorBody{Code: string(code), Message: "Supply both camera username and password.", Retryable: false}
	case CodeCredentialsChanged:
		return ManagementErrorBody{Code: string(code), Message: "The saved credentials changed. Start a new update and test again."}
	case CodeImportInvalid:
		return ManagementErrorBody{Code: string(code), Message: "Use one supported Mac camera YAML file and optional literal credentials file.", Retryable: false}
	case CodeEvidenceUnavailable:
		return ManagementErrorBody{Code: string(code), Message: "This browser cannot report decoded preview frames.", Retryable: false}
	case CodeEvidenceInsufficient:
		return ManagementErrorBody{Code: string(code), Message: "Preview needs advancing decoded frames before saving.", Retryable: false}
	default:
		e := PublicError(cs.NewError(cs.ErrorCode(code), nil)).Error
		return ManagementErrorBody{Code: string(e.Code), Message: e.Message, Retryable: e.Retryable}
	}
}
func ManagementPublicError(err error) ManagementErrorResponse {
	if managed, ok := errors.AsType[*ManagementError](err); ok {
		body := managementErrorBody(managed.Code)
		if managed.Code == CodeDuplicateCamera {
			body.CameraID = managed.cameraID
		}
		return ManagementErrorResponse{body}
	}
	e := PublicError(err).Error
	return ManagementErrorResponse{ManagementErrorBody{Code: string(e.Code), Message: e.Message, Retryable: e.Retryable}}
}
func ManagementErrorStatus(code string) int {
	switch ManagementErrorCode(code) {
	case CodeDraftLost:
		return 410
	case CodeDraftExpired:
		return 410
	case CodeTestExpired:
		return 409
	case CodeRevisionConflict:
		return 409
	case CodeCatalogConflict:
		return 409
	case CodeOperationConflict:
		return 409
	case CodeOperationInProgress:
		return 409
	case CodeSaveUnknown:
		return 503
	case CodeStorageUnavailable:
		return 503
	case CodeRecoveryStorageUnavailable:
		return 409
	case CodeReceiptExpired:
		return 410
	case CodeRecoveryLost:
		return 410
	case CodeDestinationDenied:
		return 400
	case CodeAddressUnavailable:
		return 503
	case CodeIdentityUnverified:
		return 503
	case CodeDuplicateCamera, CodeCredentialsChanged:
		return 409
	case CodeCredentialsRequired:
		return 400
	case CodeImportInvalid:
		return 400
	case CodeEvidenceUnavailable:
		return 400
	case CodeEvidenceInsufficient:
		return 409
	default:
		return ErrorStatus(cs.ErrorCode(code))
	}
}

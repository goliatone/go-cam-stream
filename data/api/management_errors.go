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
	CodeImportInvalid              ManagementErrorCode = "import_invalid"
	CodeEvidenceUnavailable        ManagementErrorCode = "playback_evidence_unavailable"
	CodeEvidenceInsufficient       ManagementErrorCode = "playback_evidence_insufficient"
)

type ManagementErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type ManagementErrorResponse struct {
	Error ManagementErrorBody `json:"error"`
}

// ManagementError renders only an allowlisted code/message. A command boundary
// may retain a cause for errors.Is/As; it is never included in public responses.
type ManagementError struct {
	Code  ManagementErrorCode
	cause error
}

func (e *ManagementError) Error() string               { return managementErrorBody(e.Code).Message }
func ManagementFailure(code ManagementErrorCode) error { return &ManagementError{Code: code} }
func (e *ManagementError) Unwrap() error               { return e.cause }

// ManagementCommandFailure preserves the public domain classification across
// structured dispatcher wrappers without exposing their messages or metadata.
func ManagementCommandFailure(err error) error {
	if err == nil {
		return nil
	}
	return &ManagementError{Code: ManagementErrorCode(ManagementPublicError(err).Error.Code), cause: err}
}
func managementErrorBody(code ManagementErrorCode) ManagementErrorBody {
	switch code {
	case CodeDraftLost:
		return ManagementErrorBody{string(code), "This draft was lost when the server restarted. Re-enter the camera.", false}
	case CodeDraftExpired:
		return ManagementErrorBody{string(code), "This draft has expired. Create a new draft.", false}
	case CodeTestExpired:
		return ManagementErrorBody{string(code), "Test the current draft again before saving.", false}
	case CodeRevisionConflict:
		return ManagementErrorBody{string(code), "Refresh the current draft before continuing.", false}
	case CodeCatalogConflict:
		return ManagementErrorBody{string(code), "Refresh the catalog before explicitly rebasing this save.", false}
	case CodeOperationConflict:
		return ManagementErrorBody{string(code), "This operation key already identifies a different request.", false}
	case CodeOperationInProgress:
		return ManagementErrorBody{string(code), "Wait for the current operation to finish.", true}
	case CodeSaveUnknown:
		return ManagementErrorBody{string(code), "The save outcome is unknown. Look up the original operation.", true}
	case CodeStorageUnavailable:
		return ManagementErrorBody{string(code), "Private camera storage is unavailable.", true}
	case CodeRecoveryStorageUnavailable:
		return ManagementErrorBody{string(code), "Enable browser site storage before managing cameras.", false}
	case CodeReceiptExpired:
		return ManagementErrorBody{string(code), "Resolve the original camera creation before starting another save.", false}
	case CodeRecoveryLost:
		return ManagementErrorBody{string(code), "Refresh source status before explicitly reconnecting again.", false}
	case CodeDestinationDenied:
		return ManagementErrorBody{string(code), "The initial camera address is not permitted.", false}
	case CodeAddressUnavailable:
		return ManagementErrorBody{string(code), "The initial camera address could not be verified.", true}
	case CodeIdentityUnverified:
		return ManagementErrorBody{string(code), "An existing camera identity could not be verified.", true}
	case CodeDuplicateCamera:
		return ManagementErrorBody{string(code), "This address overlaps an existing camera or reserved test.", false}
	case CodeCredentialsRequired:
		return ManagementErrorBody{string(code), "Supply both camera username and password.", false}
	case CodeImportInvalid:
		return ManagementErrorBody{string(code), "Use one supported Mac camera YAML file and optional literal credentials file.", false}
	case CodeEvidenceUnavailable:
		return ManagementErrorBody{string(code), "This browser cannot report decoded preview frames.", false}
	case CodeEvidenceInsufficient:
		return ManagementErrorBody{string(code), "Preview needs advancing decoded frames before saving.", false}
	default:
		e := PublicError(cs.NewError(cs.ErrorCode(code), nil)).Error
		return ManagementErrorBody{string(e.Code), e.Message, e.Retryable}
	}
}
func ManagementPublicError(err error) ManagementErrorResponse {
	if managed, ok := errors.AsType[*ManagementError](err); ok {
		return ManagementErrorResponse{managementErrorBody(managed.Code)}
	}
	e := PublicError(err).Error
	return ManagementErrorResponse{ManagementErrorBody{string(e.Code), e.Message, e.Retryable}}
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
	case CodeDuplicateCamera:
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

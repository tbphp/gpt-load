package errors

import (
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

// APIError defines a standard error structure for API responses.
type APIError struct {
	HTTPStatus int
	Code       string
	Message    string
	Data       any
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return e.Message
}

// Predefined API errors
var (
	ErrBadRequest                             = &APIError{HTTPStatus: http.StatusBadRequest, Code: "BAD_REQUEST", Message: "Invalid request parameters"}
	ErrInvalidJSON                            = &APIError{HTTPStatus: http.StatusBadRequest, Code: "INVALID_JSON", Message: "Invalid JSON format"}
	ErrRequestTooLarge                        = &APIError{HTTPStatus: http.StatusRequestEntityTooLarge, Code: "REQUEST_TOO_LARGE", Message: "Request body is too large"}
	ErrValidation                             = &APIError{HTTPStatus: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "Input validation failed"}
	ErrDuplicateResource                      = &APIError{HTTPStatus: http.StatusConflict, Code: "DUPLICATE_RESOURCE", Message: "Resource already exists"}
	ErrResourceNotFound                       = &APIError{HTTPStatus: http.StatusNotFound, Code: "NOT_FOUND", Message: "Resource not found"}
	ErrGroupInUse                             = &APIError{HTTPStatus: http.StatusConflict, Code: "GROUP_IN_USE", Message: "Group is referenced by access keys"}
	ErrInvalidCredentialState                 = &APIError{HTTPStatus: http.StatusConflict, Code: "INVALID_CREDENTIAL_STATE", Message: "Credential cannot be restored from its current state"}
	ErrInternalServer                         = &APIError{HTTPStatus: http.StatusInternalServerError, Code: "INTERNAL_SERVER_ERROR", Message: "An unexpected error occurred"}
	ErrDatabase                               = &APIError{HTTPStatus: http.StatusInternalServerError, Code: "DATABASE_ERROR", Message: "Database operation failed"}
	ErrUnauthorized                           = &APIError{HTTPStatus: http.StatusUnauthorized, Code: "UNAUTHORIZED", Message: "Authentication failed"}
	ErrForbidden                              = &APIError{HTTPStatus: http.StatusForbidden, Code: "FORBIDDEN", Message: "Access is forbidden"}
	ErrAuthLocked                             = &APIError{HTTPStatus: http.StatusTooManyRequests, Code: "AUTH_LOCKED", Message: "Authentication is temporarily locked"}
	ErrChannelTargetConflict                  = &APIError{HTTPStatus: http.StatusConflict, Code: "CHANNEL_TARGET_CONFLICT", Message: "Channel target conflicts with an existing group"}
	ErrModelNameConflict                      = &APIError{HTTPStatus: http.StatusConflict, Code: "MODEL_NAME_CONFLICT", Message: "Client model names conflict within the group"}
	ErrNoActiveCredential                     = &APIError{HTTPStatus: http.StatusConflict, Code: "NO_ACTIVE_CREDENTIAL", Message: "No active credential is available for this group"}
	ErrBadGateway                             = &APIError{HTTPStatus: http.StatusBadGateway, Code: "BAD_GATEWAY", Message: "Upstream service error"}
	ErrIdempotencyKeyRequired                 = &APIError{HTTPStatus: http.StatusPreconditionRequired, Code: "IDEMPOTENCY_KEY_REQUIRED", Message: "Idempotency-Key is required"}
	ErrInvalidIdempotencyKey                  = &APIError{HTTPStatus: http.StatusBadRequest, Code: "INVALID_IDEMPOTENCY_KEY", Message: "Idempotency-Key must be a canonical UUID v4"}
	ErrIdempotencyKeyReused                   = &APIError{HTTPStatus: http.StatusConflict, Code: "IDEMPOTENCY_KEY_REUSED", Message: "Idempotency-Key was already used for another request"}
	ErrIdempotencyResultExpired               = &APIError{HTTPStatus: http.StatusGone, Code: "IDEMPOTENCY_RESULT_EXPIRED", Message: "The idempotent result retention period expired"}
	ErrControlOperationIncomplete             = &APIError{HTTPStatus: http.StatusServiceUnavailable, Code: "CONTROL_OPERATION_INCOMPLETE", Message: "The resource was committed but runtime recovery is incomplete"}
	ErrControlRecoveryPending                 = &APIError{HTTPStatus: http.StatusServiceUnavailable, Code: "CONTROL_RECOVERY_PENDING", Message: "An earlier committed operation is still recovering"}
	ErrModelPriceUnpricedConfirmationRequired = &APIError{HTTPStatus: http.StatusConflict, Code: "MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED", Message: "Marking a model price as unpriced requires explicit confirmation"}
	ErrModelPriceReferenced                   = &APIError{HTTPStatus: http.StatusConflict, Code: "MODEL_PRICE_REFERENCED", Message: "Model price is referenced by Groups"}
	ErrModelPriceAutomaticDeleteForbidden     = &APIError{HTTPStatus: http.StatusConflict, Code: "MODEL_PRICE_AUTOMATIC_DELETE_FORBIDDEN", Message: "Automatic model prices cannot be deleted manually"}
	ErrOAuthFileInvalid                       = &APIError{HTTPStatus: http.StatusBadRequest, Code: "OAUTH_FILE_INVALID", Message: "OAuth credential file is invalid"}
	ErrOAuthFileTooLarge                      = &APIError{HTTPStatus: http.StatusRequestEntityTooLarge, Code: "OAUTH_FILE_TOO_LARGE", Message: "OAuth credential file is too large"}
	ErrAuthorizationUnavailable               = &APIError{HTTPStatus: http.StatusServiceUnavailable, Code: "AUTHORIZATION_UNAVAILABLE", Message: "Browser authorization is unavailable"}
	ErrAuthorizationStateInvalid              = &APIError{HTTPStatus: http.StatusBadRequest, Code: "AUTHORIZATION_STATE_INVALID", Message: "Authorization state is invalid"}
	ErrAuthorizationExchangeFailed            = &APIError{HTTPStatus: http.StatusBadGateway, Code: "AUTHORIZATION_EXCHANGE_FAILED", Message: "Authorization exchange failed"}
	ErrStagedCredentialNotReady               = &APIError{HTTPStatus: http.StatusConflict, Code: "STAGED_CREDENTIAL_NOT_READY", Message: "Staged credential is not ready"}
	ErrStagedCredentialExpired                = &APIError{HTTPStatus: http.StatusGone, Code: "STAGED_CREDENTIAL_EXPIRED", Message: "Staged credential expired"}
	ErrStagedCredentialConsumed               = &APIError{HTTPStatus: http.StatusConflict, Code: "STAGED_CREDENTIAL_CONSUMED", Message: "Staged credential was already consumed"}
	ErrStagedCredentialMismatch               = &APIError{HTTPStatus: http.StatusConflict, Code: "STAGED_CREDENTIAL_MISMATCH", Message: "Staged credential does not match the target"}
	ErrDuplicateCredentialIdentity            = &APIError{HTTPStatus: http.StatusConflict, Code: "DUPLICATE_CREDENTIAL_IDENTITY", Message: "Subscription account already exists in the group"}
	ErrCredentialReauthorizationRequired      = &APIError{HTTPStatus: http.StatusConflict, Code: "CREDENTIAL_REAUTHORIZATION_REQUIRED", Message: "Credential requires reauthorization"}
	ErrCredentialAuthOutcomeUnknown           = &APIError{HTTPStatus: http.StatusConflict, Code: "CREDENTIAL_AUTH_OUTCOME_UNKNOWN", Message: "Credential authorization outcome is unknown"}
	ErrCredentialVersionConflict              = &APIError{HTTPStatus: http.StatusConflict, Code: "CREDENTIAL_VERSION_CONFLICT", Message: "Credential changed since it was loaded"}
	ErrResetCreditUnavailable                 = &APIError{HTTPStatus: http.StatusConflict, Code: "RESET_CREDIT_UNAVAILABLE", Message: "No reset credit is currently available"}
	ErrResetCreditRejected                    = &APIError{HTTPStatus: http.StatusBadGateway, Code: "RESET_CREDIT_REJECTED", Message: "The reset credit was rejected by the upstream"}
	ErrResetCreditOutcomeUnknown              = &APIError{HTTPStatus: http.StatusServiceUnavailable, Code: "RESET_CREDIT_OUTCOME_UNKNOWN", Message: "The reset credit outcome is unknown; retry with the same idempotency key"}
)

var ErrCredentialRefreshTemporarilyUnavailable = &APIError{
	HTTPStatus: http.StatusServiceUnavailable,
	Code:       "CREDENTIAL_REFRESH_TEMPORARILY_UNAVAILABLE",
	Message:    "Credential refresh is temporarily unavailable",
}

// NewAPIErrorWithData creates a copy of an APIError with response data.
func NewAPIErrorWithData(base *APIError, data any) *APIError {
	return &APIError{
		HTTPStatus: base.HTTPStatus,
		Code:       base.Code,
		Message:    base.Message,
		Data:       data,
	}
}

// ParseDBError intelligently converts a GORM error into a standard APIError.
func ParseDBError(err error) *APIError {
	if err == nil {
		return nil
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrResourceNotFound
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrDuplicateResource
	}

	// Keep a message fallback for driver paths that return the native error
	// instead of GORM's translated sentinel. The response remains generic and
	// never exposes the database error text.
	normalized := strings.ToLower(err.Error())
	if strings.Contains(normalized, "unique constraint failed") ||
		strings.Contains(normalized, "duplicate key value violates unique constraint") ||
		strings.Contains(normalized, "duplicate entry") {
		return ErrDuplicateResource
	}

	return ErrDatabase
}

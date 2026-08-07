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
	ErrInvalidKeyState                        = &APIError{HTTPStatus: http.StatusConflict, Code: "INVALID_KEY_STATE", Message: "Key cannot be restored from its current state"}
	ErrInternalServer                         = &APIError{HTTPStatus: http.StatusInternalServerError, Code: "INTERNAL_SERVER_ERROR", Message: "An unexpected error occurred"}
	ErrDatabase                               = &APIError{HTTPStatus: http.StatusInternalServerError, Code: "DATABASE_ERROR", Message: "Database operation failed"}
	ErrUnauthorized                           = &APIError{HTTPStatus: http.StatusUnauthorized, Code: "UNAUTHORIZED", Message: "Authentication failed"}
	ErrAuthLocked                             = &APIError{HTTPStatus: http.StatusTooManyRequests, Code: "AUTH_LOCKED", Message: "Authentication is temporarily locked"}
	ErrUpstreamURLConflict                    = &APIError{HTTPStatus: http.StatusConflict, Code: "UPSTREAM_URL_CONFLICT", Message: "Upstream URL conflicts with an existing group"}
	ErrModelNameConflict                      = &APIError{HTTPStatus: http.StatusConflict, Code: "MODEL_NAME_CONFLICT", Message: "Client model names conflict within the group"}
	ErrUpstreamURLChangeConfirmationRequired  = &APIError{HTTPStatus: http.StatusConflict, Code: "UPSTREAM_URL_CHANGE_CONFIRMATION_REQUIRED", Message: "Upstream URL change requires explicit confirmation"}
	ErrNoActiveUpstreamKey                    = &APIError{HTTPStatus: http.StatusConflict, Code: "NO_ACTIVE_UPSTREAM_KEY", Message: "No active upstream key available for this group"}
	ErrBadGateway                             = &APIError{HTTPStatus: http.StatusBadGateway, Code: "BAD_GATEWAY", Message: "Upstream service error"}
	ErrIdempotencyKeyRequired                 = &APIError{HTTPStatus: http.StatusPreconditionRequired, Code: "IDEMPOTENCY_KEY_REQUIRED", Message: "Idempotency-Key is required"}
	ErrInvalidIdempotencyKey                  = &APIError{HTTPStatus: http.StatusBadRequest, Code: "INVALID_IDEMPOTENCY_KEY", Message: "Idempotency-Key must be a canonical UUID v4"}
	ErrIdempotencyKeyReused                   = &APIError{HTTPStatus: http.StatusConflict, Code: "IDEMPOTENCY_KEY_REUSED", Message: "Idempotency-Key was already used for another request"}
	ErrIdempotencyResultExpired               = &APIError{HTTPStatus: http.StatusGone, Code: "IDEMPOTENCY_RESULT_EXPIRED", Message: "The idempotent result retention period expired"}
	ErrControlOperationIncomplete             = &APIError{HTTPStatus: http.StatusServiceUnavailable, Code: "CONTROL_OPERATION_INCOMPLETE", Message: "The resource was committed but runtime recovery is incomplete"}
	ErrControlRecoveryPending                 = &APIError{HTTPStatus: http.StatusServiceUnavailable, Code: "CONTROL_RECOVERY_PENDING", Message: "An earlier committed operation is still recovering"}
	ErrSettingsPreconditionRequired           = &APIError{HTTPStatus: http.StatusPreconditionRequired, Code: "SETTINGS_PRECONDITION_REQUIRED", Message: "If-Match is required"}
	ErrSettingsVersionConflict                = &APIError{HTTPStatus: http.StatusPreconditionFailed, Code: "SETTINGS_VERSION_CONFLICT", Message: "Settings changed since they were loaded"}
	ErrModelPriceUnpricedConfirmationRequired = &APIError{HTTPStatus: http.StatusConflict, Code: "MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED", Message: "Marking a model price as unpriced requires explicit confirmation"}
	ErrModelPriceReferenced                   = &APIError{HTTPStatus: http.StatusConflict, Code: "MODEL_PRICE_REFERENCED", Message: "Model price is referenced by Groups"}
	ErrModelPriceAutomaticDeleteForbidden     = &APIError{HTTPStatus: http.StatusConflict, Code: "MODEL_PRICE_AUTOMATIC_DELETE_FORBIDDEN", Message: "Automatic model prices cannot be deleted manually"}
	ErrModelPriceVersionConflict              = &APIError{HTTPStatus: http.StatusPreconditionFailed, Code: "MODEL_PRICE_VERSION_CONFLICT", Message: "Model price changed since it was loaded"}
)

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

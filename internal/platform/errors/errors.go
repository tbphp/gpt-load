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
	ErrBadRequest          = &APIError{HTTPStatus: http.StatusBadRequest, Code: "BAD_REQUEST", Message: "Invalid request parameters"}
	ErrInvalidJSON         = &APIError{HTTPStatus: http.StatusBadRequest, Code: "INVALID_JSON", Message: "Invalid JSON format"}
	ErrRequestTooLarge     = &APIError{HTTPStatus: http.StatusRequestEntityTooLarge, Code: "REQUEST_TOO_LARGE", Message: "Request body is too large"}
	ErrValidation          = &APIError{HTTPStatus: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "Input validation failed"}
	ErrDuplicateResource   = &APIError{HTTPStatus: http.StatusConflict, Code: "DUPLICATE_RESOURCE", Message: "Resource already exists"}
	ErrResourceNotFound    = &APIError{HTTPStatus: http.StatusNotFound, Code: "NOT_FOUND", Message: "Resource not found"}
	ErrInternalServer      = &APIError{HTTPStatus: http.StatusInternalServerError, Code: "INTERNAL_SERVER_ERROR", Message: "An unexpected error occurred"}
	ErrDatabase            = &APIError{HTTPStatus: http.StatusInternalServerError, Code: "DATABASE_ERROR", Message: "Database operation failed"}
	ErrUnauthorized        = &APIError{HTTPStatus: http.StatusUnauthorized, Code: "UNAUTHORIZED", Message: "Authentication failed"}
	ErrUpstreamURLConflict = &APIError{HTTPStatus: http.StatusConflict, Code: "UPSTREAM_URL_CONFLICT", Message: "Upstream URL conflicts with an existing group"}
	ErrNoActiveUpstreamKey = &APIError{HTTPStatus: http.StatusConflict, Code: "NO_ACTIVE_UPSTREAM_KEY", Message: "No active upstream key available for this group"}
	ErrBadGateway          = &APIError{HTTPStatus: http.StatusBadGateway, Code: "BAD_GATEWAY", Message: "Upstream service error"}
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

	// The SQLite driver does not translate every constraint error into
	// gorm.ErrDuplicatedKey, so retain a driver-independent message fallback.
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
		return ErrDuplicateResource
	}

	return ErrDatabase
}

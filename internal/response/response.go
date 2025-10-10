// Package response provides standardized JSON response helpers.
package response

import (
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/i18n"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SuccessResponse defines the standard JSON success response structure.
type SuccessResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ErrorResponse defines the standard JSON error response structure.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Success sends a standardized success response.
func Success(c *gin.Context, data any) {
	message := i18n.Message(c, "common.success")
	c.JSON(http.StatusOK, SuccessResponse{
		Code:    0,
		Message: message,
		Data:    data,
	})
}

// Error sends a standardized error response using an APIError.
func Error(c *gin.Context, apiErr *app_errors.APIError) {
	c.JSON(apiErr.HTTPStatus, ErrorResponse{
		Code:    apiErr.Code,
		Message: apiErr.Message,
	})
}

// SuccessI18n sends a standardized success response with i18n message.
func SuccessI18n(c *gin.Context, msgID string, data any, templateData ...map[string]any) {
	message := i18n.Message(c, msgID, templateData...)
	c.JSON(http.StatusOK, SuccessResponse{
		Code:    0,
		Message: message,
		Data:    data,
	})
}

// ErrorI18n sends a standardized error response with i18n message.
func ErrorI18n(c *gin.Context, httpStatus int, code string, msgID string, templateData ...map[string]any) {
	message := i18n.Message(c, msgID, templateData...)
	c.JSON(httpStatus, ErrorResponse{
		Code:    code,
		Message: message,
	})
}

// ErrorI18nFromAPIError sends a standardized error response using an APIError with i18n message.
func ErrorI18nFromAPIError(c *gin.Context, apiErr *app_errors.APIError, msgID string, templateData ...map[string]any) {
	message := i18n.Message(c, msgID, templateData...)
	c.JSON(apiErr.HTTPStatus, ErrorResponse{
		Code:    apiErr.Code,
		Message: message,
	})
}

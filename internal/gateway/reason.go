package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

type reason struct {
	Status  int
	Code    string
	Message string
}

var (
	reasonInvalidAccessKey     = reason{Status: http.StatusUnauthorized, Code: "invalid_access_key", Message: "Invalid access key."}
	reasonEndpointNotFound     = reason{Status: http.StatusNotFound, Code: "protocol_endpoint_not_found", Message: "Protocol endpoint not found."}
	reasonCannotExtractModel   = reason{Status: http.StatusBadRequest, Code: "cannot_extract_model", Message: "Cannot extract model from request."}
	reasonNoCandidate          = reason{Status: http.StatusServiceUnavailable, Code: "no_available_candidate", Message: "No available upstream candidate."}
	reasonUpstreamConnect      = reason{Status: http.StatusBadGateway, Code: "upstream_connect_failed", Message: "Could not connect to an upstream service."}
	reasonUpstreamTimeout      = reason{Status: http.StatusGatewayTimeout, Code: "upstream_timeout", Message: "Upstream request timed out."}
	reasonUpstreamProtocol     = reason{Status: http.StatusBadGateway, Code: "upstream_protocol_error", Message: "Upstream returned an unsupported response."}
	reasonRequestTooLarge      = reason{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "Request body is too large."}
	reasonModelListTooLarge    = reason{Status: http.StatusInternalServerError, Code: "model_list_too_large", Message: "Model list is too large."}
	reasonAccessKeyRateLimited = reason{
		Status:  http.StatusTooManyRequests,
		Code:    "access_key_rate_limited",
		Message: "Access key rate limit exceeded.",
	}
)

func (handler *Handler) writeReason(context *gin.Context, value reason) error {
	body, err := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: value.Code, Message: value.Message})
	if err != nil {
		return err
	}
	return handler.writeBufferedResponse(
		context,
		value.Status,
		http.Header{"Content-Type": {"application/json; charset=utf-8"}},
		body,
	)
}

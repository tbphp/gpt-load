package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type reason struct {
	Status  int
	Code    string
	Message string
}

var (
	reasonInvalidAccessKey   = reason{Status: http.StatusUnauthorized, Code: "invalid_access_key", Message: "Invalid access key."}
	reasonEndpointNotFound   = reason{Status: http.StatusNotFound, Code: "protocol_endpoint_not_found", Message: "Protocol endpoint not found."}
	reasonCannotExtractModel = reason{Status: http.StatusBadRequest, Code: "cannot_extract_model", Message: "Cannot extract model from request."}
	reasonNoCandidate        = reason{Status: http.StatusServiceUnavailable, Code: "no_available_candidate", Message: "No available upstream candidate."}
	reasonUpstreamConnect    = reason{Status: http.StatusBadGateway, Code: "upstream_connect_failed", Message: "Could not connect to an upstream service."}
	reasonUpstreamTimeout    = reason{Status: http.StatusGatewayTimeout, Code: "upstream_timeout", Message: "Upstream request timed out."}
	reasonUpstreamProtocol   = reason{Status: http.StatusBadGateway, Code: "upstream_protocol_error", Message: "Upstream returned an unsupported streaming response."}
	reasonRequestTooLarge    = reason{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "Request body is too large."}
)

func writeReason(context *gin.Context, value reason) {
	context.JSON(value.Status, gin.H{"code": value.Code, "message": value.Message})
}

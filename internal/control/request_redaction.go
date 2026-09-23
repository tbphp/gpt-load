package control

import (
	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/response"
	"gpt-load/internal/requestredact"
)

func (s *Server) handleValidateRequestRedaction(c *gin.Context) {
	var request struct {
		Rules []requestredact.Rule `json:"rules"`
	}
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "validate_request_redaction", mapControlJSONError(err))
		return
	}
	response.SuccessI18n(c, "common.success", requestredact.Validate(request.Rules))
}

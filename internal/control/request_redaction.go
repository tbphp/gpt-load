package control

import (
	"errors"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
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

func (s *Server) handlePreviewRequestRedaction(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var request struct {
		Rules []requestredact.Rule `json:"rules"`
		Text  *string              `json:"text"`
	}
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "preview_request_redaction", mapControlJSONError(err))
		return
	}
	if request.Rules == nil || request.Text == nil {
		writeServiceError(c, "preview_request_redaction", app_errors.ErrValidation)
		return
	}
	compiled, err := requestredact.Compile(request.Rules)
	if err != nil {
		writeServiceError(c, "preview_request_redaction", app_errors.NewAPIErrorWithData(app_errors.ErrValidation,
			map[string]any{"request_redaction": requestredact.Validate(request.Rules)}))
		return
	}
	preview, err := compiled.PreviewText(*request.Text)
	if err != nil {
		reason := "invalid_text"
		if errors.Is(err, requestredact.ErrPreviewMatches) {
			reason = "too_many_matches"
		}
		writeServiceError(c, "preview_request_redaction", app_errors.NewAPIErrorWithData(app_errors.ErrValidation,
			map[string]any{"preview": reason}))
		return
	}
	response.SuccessI18n(c, "common.success", preview)
}

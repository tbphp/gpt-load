package control

import (
	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/response"
)

func (s *Server) handleListProjectModels(c *gin.Context) {
	query, apiErr := parseProjectModelListQuery(c.Request.URL.RawQuery, c.Request.URL.ForceQuery)
	if apiErr != nil {
		writeServiceError(c, "list_project_models", apiErr)
		return
	}
	result, err := s.service.ListProjectModels(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_project_models", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

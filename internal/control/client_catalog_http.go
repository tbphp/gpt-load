package control

import (
	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/response"
)

func (s *Server) handleGetClientCatalog(c *gin.Context) {
	result, err := s.service.GetClientCatalog(c.Request.Context())
	if err != nil {
		writeServiceError(c, "get_client_catalog", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handlePreviewClientCatalog(c *gin.Context) {
	var request ClientCatalogRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "preview_client_catalog", mapControlJSONError(err))
		return
	}
	result, err := s.service.PreviewClientCatalog(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "preview_client_catalog", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateClientCatalog(c *gin.Context) {
	var request ClientCatalogRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_client_catalog", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateClientCatalog(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "update_client_catalog", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

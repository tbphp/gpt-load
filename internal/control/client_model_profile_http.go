package control

import (
	"net/url"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/storage/models"
)

func parseClientModelProfileQuery(rawQuery string, forceQuery bool) (string, *app_errors.APIError) {
	if forceQuery && rawQuery == "" {
		return "", app_errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", app_errors.ErrBadRequest
	}
	entries, exists := values["model"]
	if !exists || len(entries) != 1 || len(values) != 1 || validateClientModelName(entries[0]) != nil {
		return "", app_errors.ErrBadRequest
	}
	return entries[0], nil
}

func (s *Server) handleGetClientModelProfile(c *gin.Context) {
	model, apiErr := parseClientModelProfileQuery(c.Request.URL.RawQuery, c.Request.URL.ForceQuery)
	if apiErr != nil {
		writeServiceError(c, "get_client_model_profile", apiErr)
		return
	}
	result, err := s.service.GetClientModelProfile(c.Request.Context(), model)
	if err != nil {
		writeServiceError(c, "get_client_model_profile", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateClientModelProfile(c *gin.Context) {
	var request ClientModelProfileUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_client_model_profile", mapControlJSONError(err))
		return
	}
	if request.ClientModel != "" {
		setMutationResourceLocator(c, "client-model:"+models.ClientModelHash(request.ClientModel))
	}
	result, err := s.service.UpdateClientModelProfile(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "update_client_model_profile", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

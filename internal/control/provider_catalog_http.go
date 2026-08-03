package control

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
)

const providerCatalogMaxQueryRunes = 200

func (s *Server) handleListProviderSuggestions(c *gin.Context) {
	query, apiErr := parseProviderSuggestionQuery(c.Request.URL.RawQuery, c.Request.URL.ForceQuery)
	if apiErr != nil {
		writeServiceError(c, "list_provider_suggestions", apiErr)
		return
	}
	result, err := s.service.ListProviderSuggestions(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_provider_suggestions", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleListProviderModels(c *gin.Context) {
	query, apiErr := parseProviderModelQuery(c.Request.URL.RawQuery, c.Request.URL.ForceQuery)
	if apiErr != nil {
		writeServiceError(c, "list_provider_models", apiErr)
		return
	}
	result, err := s.service.ListProviderModels(c.Request.Context(), c.Param("provider_id"), query)
	if err != nil {
		writeServiceError(c, "list_provider_models", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleSyncModelPrices(c *gin.Context) {
	if c.Request.URL.ForceQuery || c.Request.URL.RawQuery != "" {
		writeServiceError(c, "sync_model_prices", app_errors.ErrBadRequest)
		return
	}
	result, err := s.service.SyncModelPrices(c.Request.Context())
	if err != nil {
		writeServiceError(c, "sync_model_prices", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func parseProviderSuggestionQuery(rawQuery string, forceQuery bool) (string, *app_errors.APIError) {
	values, apiErr := parseProviderCatalogValues(rawQuery, forceQuery, map[string]struct{}{"q": {}})
	if apiErr != nil {
		return "", apiErr
	}
	query := strings.TrimSpace(values.Get("q"))
	if utf8.RuneCountInString(query) > providerCatalogMaxQueryRunes {
		return "", app_errors.ErrBadRequest
	}
	return query, nil
}

func parseProviderModelQuery(rawQuery string, forceQuery bool) (ProviderModelQuery, *app_errors.APIError) {
	values, apiErr := parseProviderCatalogValues(
		rawQuery,
		forceQuery,
		map[string]struct{}{"q": {}, "status": {}},
	)
	if apiErr != nil {
		return ProviderModelQuery{}, apiErr
	}
	query := ProviderModelQuery{Query: strings.TrimSpace(values.Get("q"))}
	if utf8.RuneCountInString(query.Query) > providerCatalogMaxQueryRunes {
		return ProviderModelQuery{}, app_errors.ErrBadRequest
	}
	if values.Has("status") {
		query.Status = PricingStatus(values.Get("status"))
		if query.Status != PricingStatusPending && query.Status != PricingStatusConfigured {
			return ProviderModelQuery{}, app_errors.ErrBadRequest
		}
	}
	return query, nil
}

func parseProviderCatalogValues(
	rawQuery string,
	forceQuery bool,
	allowed map[string]struct{},
) (url.Values, *app_errors.APIError) {
	if forceQuery && rawQuery == "" {
		return nil, app_errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 {
			return nil, app_errors.ErrBadRequest
		}
	}
	return values, nil
}

func (s *Service) SyncModelPrices(ctx context.Context) (CatalogSyncStatus, error) {
	if s.catalogSync == nil {
		return CatalogSyncStatus{}, fmt.Errorf("catalog sync is unavailable: %w", app_errors.ErrInternalServer)
	}
	status, err := s.catalogSync.Sync(ctx, CatalogSyncManual)
	if err != nil {
		return status, app_errors.NewAPIErrorWithData(app_errors.ErrBadGateway, status)
	}
	return status, nil
}

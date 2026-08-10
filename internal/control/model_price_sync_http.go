package control

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
)

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

package control

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

const maxModelPriceBatchSize = 100

type ModelPriceCatalogReferenceRequest struct {
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
}

type ModelPriceBatchRequest struct {
	IDs       []uint                             `json:"ids"`
	Mode      string                             `json:"mode"`
	Price     *ModelPriceUpdateRequest           `json:"price,omitempty"`
	Reference *ModelPriceCatalogReferenceRequest `json:"reference,omitempty"`
}

type ModelPriceBatchResponse struct {
	Items []ModelPriceDTO `json:"items"`
}

func (s *Server) handleBatchModelPrices(c *gin.Context) {
	if !modelPriceMutationQueryIsEmpty(c, "batch_model_prices") {
		return
	}
	var request ModelPriceBatchRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "batch_model_prices", mapControlJSONError(err))
		return
	}
	result, err := s.service.BatchModelPrices(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "batch_model_prices", err)
		return
	}
	setMutationResourceLocator(c, fmt.Sprintf("model-prices:%s:%v", request.Mode, request.IDs))
	response.SuccessI18n(c, "common.success", result)
}

func (s *Service) BatchModelPrices(ctx context.Context, request ModelPriceBatchRequest) (ModelPriceBatchResponse, error) {
	result := ModelPriceBatchResponse{Items: []ModelPriceDTO{}}
	if len(request.IDs) == 0 || len(request.IDs) > maxModelPriceBatchSize {
		return result, app_errors.ErrValidation
	}
	seen := make(map[uint]bool, len(request.IDs))
	ids := make([]uint, 0, len(request.IDs))
	for _, id := range request.IDs {
		if id == 0 || uint64(id) > uint64(maxSafeInteger) {
			return result, app_errors.ErrValidation
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var desired models.ModelPrice
	switch request.Mode {
	case "copy":
		if request.Price == nil || request.Reference != nil {
			return result, app_errors.ErrValidation
		}
		if err := request.Price.validate(); err != nil {
			return result, err
		}
		if modelPriceUpdateAllNull(*request.Price) && !request.Price.ConfirmUnpriced {
			return result, app_errors.NewAPIErrorWithData(app_errors.ErrModelPriceUnpricedConfirmationRequired, ModelPriceIDData{ID: ids[0]})
		}
		var err error
		desired, err = modelPriceRequestValues(*request.Price)
		if err != nil {
			return result, err
		}
	case "link":
		if request.Reference == nil || request.Price != nil || request.Reference.ModelID == "" ||
			len(request.Reference.ModelID) > 255 || !utf8.ValidString(request.Reference.ModelID) ||
			strings.TrimSpace(request.Reference.ModelID) != request.Reference.ModelID {
			return result, app_errors.ErrValidation
		}
		if _, err := pricing.ProviderScopeKey(request.Reference.ProviderID); err != nil {
			return result, app_errors.ErrValidation
		}
	default:
		return result, app_errors.ErrValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return result, err
	}
	var snapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		snapshot = s.catalogRuntime.Load()
	}
	if request.Mode == "link" {
		model, available := lookupCatalogPrice(snapshot, request.Reference.ProviderID, request.Reference.ModelID)
		if !available {
			return result, app_errors.ErrModelPriceSourceUnavailable
		}
		var err error
		desired, err = automaticCatalogValues(model.Cost)
		if err != nil {
			return result, app_errors.ErrValidation
		}
		desired.ReferenceProviderID = request.Reference.ProviderID
		desired.ReferenceModelID = request.Reference.ModelID
	}
	var table *pricing.Table
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		var rows []models.ModelPrice
		if err := tx.Where("id IN ?", ids).Order("id ASC").Find(&rows).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		if len(rows) != len(ids) {
			return app_errors.ErrResourceNotFound
		}
		references, err := loadPriceReferenceSnapshot(tx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			next := desired
			next.ID, next.ChannelID, next.ModelID = row.ID, row.ChannelID, row.ModelID
			next.CreatedAtMS, next.UpdatedAtMS = row.CreatedAtMS, row.UpdatedAtMS
			if !modelPriceMutableValuesEqual(row, next) {
				next.UpdatedAtMS, err = safeEpochMilliseconds(s.now())
				if err != nil {
					return app_errors.ErrInternalServer
				}
				if err := tx.Model(&models.ModelPrice{}).Where("id = ?", row.ID).Updates(map[string]any{
					"input_price_nano_usd_per_million_tokens":       next.InputPriceNanoUSDPerMillionTokens,
					"output_price_nano_usd_per_million_tokens":      next.OutputPriceNanoUSDPerMillionTokens,
					"cache_read_price_nano_usd_per_million_tokens":  next.CacheReadPriceNanoUSDPerMillionTokens,
					"cache_write_price_nano_usd_per_million_tokens": next.CacheWritePriceNanoUSDPerMillionTokens,
					"context_price_tiers":                           next.ContextPriceTiers, "mode_price_schedules": next.ModePriceSchedules,
					"is_manual": next.IsManual, "reference_provider_id": next.ReferenceProviderID,
					"reference_model_id": next.ReferenceModelID, "updated_at_ms": next.UpdatedAtMS,
				}).Error; err != nil {
					return app_errors.ParseDBError(err)
				}
			}
			projected, err := projectModelPriceRow(next, references, snapshot)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, projected.dto)
		}
		table, err = loadPriceTable(ctx, tx)
		return err
	})
	if err != nil {
		return ModelPriceBatchResponse{}, err
	}
	s.priceRuntime.Publish(table)
	return result, nil
}

func modelPriceRequestValues(request ModelPriceUpdateRequest) (models.ModelPrice, error) {
	tiers, err := buildContextPriceTiersJSON(request.ContextTiers.tiers)
	if err != nil {
		return models.ModelPrice{}, err
	}
	modes, err := buildModePriceSchedulesJSON(request.ModeSchedules.schedules)
	if err != nil {
		return models.ModelPrice{}, err
	}
	return models.ModelPrice{
		InputPriceNanoUSDPerMillionTokens:      cloneModelPriceValue(request.Input.nanoUSD),
		OutputPriceNanoUSDPerMillionTokens:     cloneModelPriceValue(request.Output.nanoUSD),
		CacheReadPriceNanoUSDPerMillionTokens:  cloneModelPriceValue(request.CacheRead.nanoUSD),
		CacheWritePriceNanoUSDPerMillionTokens: cloneModelPriceValue(request.CacheWrite.nanoUSD),
		ContextPriceTiers:                      tiers, ModePriceSchedules: modes, IsManual: true,
	}, nil
}

package control

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func samePriceSource(left, right *uint) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

// resolveModelPriceReferences 按依赖顺序展开有效价格，关系仍指向用户选择的直接来源。
func resolveModelPriceReferences(rows []models.ModelPrice) error {
	byID := make(map[uint]*models.ModelPrice, len(rows))
	for index := range rows {
		byID[rows[index].ID] = &rows[index]
	}
	state := make(map[uint]uint8, len(rows))
	var visit func(*models.ModelPrice) error
	visit = func(row *models.ModelPrice) error {
		if state[row.ID] == 1 {
			return app_errors.ErrModelPriceReferenceCycle
		}
		if state[row.ID] == 2 {
			return nil
		}
		state[row.ID] = 1
		if row.SourcePriceID != nil {
			if row.IsManual {
				return app_errors.ErrValidation
			}
			source := byID[*row.SourcePriceID]
			if source == nil {
				return app_errors.ErrModelPriceSourceUnavailable
			}
			if err := visit(source); err != nil {
				return err
			}
			row.InputPriceNanoUSDPerMillionTokens = source.InputPriceNanoUSDPerMillionTokens
			row.OutputPriceNanoUSDPerMillionTokens = source.OutputPriceNanoUSDPerMillionTokens
			row.CacheReadPriceNanoUSDPerMillionTokens = source.CacheReadPriceNanoUSDPerMillionTokens
			row.CacheWritePriceNanoUSDPerMillionTokens = source.CacheWritePriceNanoUSDPerMillionTokens
			row.ContextPriceTiers = source.ContextPriceTiers
			row.ModePriceSchedules = source.ModePriceSchedules
		}
		state[row.ID] = 2
		return nil
	}
	for index := range rows {
		if err := visit(&rows[index]); err != nil {
			return err
		}
	}
	return nil
}

// refreshPriceTable 在同一控制面事务中更新下游，提交后调用方再发布内存表。
func refreshPriceTable(ctx context.Context, tx *gorm.DB) (*pricing.Table, error) {
	tx = tx.WithContext(ctx)
	var rows []models.ModelPrice
	if err := tx.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	previous := append([]models.ModelPrice(nil), rows...)
	if err := resolveModelPriceReferences(rows); err != nil {
		return nil, err
	}
	for index, row := range rows {
		if row.SourcePriceID == nil || catalogPriceValuesEqual(previous[index], row) {
			continue
		}
		row.UpdatedAtMS = max(time.Now().UnixMilli(), previous[index].UpdatedAtMS+1)
		if err := tx.Model(&models.ModelPrice{}).Where("id = ?", row.ID).Updates(modelPriceAssignments(row)).Error; err != nil {
			return nil, app_errors.ParseDBError(err)
		}
	}
	return compilePriceTable(rows)
}

func modelPriceAssignments(row models.ModelPrice) map[string]any {
	return map[string]any{
		"input_price_nano_usd_per_million_tokens":       row.InputPriceNanoUSDPerMillionTokens,
		"output_price_nano_usd_per_million_tokens":      row.OutputPriceNanoUSDPerMillionTokens,
		"cache_read_price_nano_usd_per_million_tokens":  row.CacheReadPriceNanoUSDPerMillionTokens,
		"cache_write_price_nano_usd_per_million_tokens": row.CacheWritePriceNanoUSDPerMillionTokens,
		"context_price_tiers":                           row.ContextPriceTiers,
		"mode_price_schedules":                          row.ModePriceSchedules,
		"is_manual":                                     row.IsManual,
		"source_price_id":                               row.SourcePriceID,
		"updated_at_ms":                                 row.UpdatedAtMS,
	}
}

// validateGroupedPriceSources 阻止最后一个分组中的来源消失；错误携带可供界面定位的名称。
func validateGroupedPriceSources(rows []models.ModelPrice, references map[pricing.Identity]referencedPrice) error {
	byID := make(map[uint]models.ModelPrice, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	for _, row := range rows {
		if row.SourcePriceID == nil || references[pricing.Identity{ChannelID: row.ChannelID, ModelID: row.ModelID}].referenceCount == 0 {
			continue
		}
		source, exists := byID[*row.SourcePriceID]
		if exists && references[pricing.Identity{ChannelID: source.ChannelID, ModelID: source.ModelID}].referenceCount > 0 {
			continue
		}
		dependents := make([]models.ModelPrice, 0)
		for _, candidate := range rows {
			if candidate.SourcePriceID != nil && *candidate.SourcePriceID == *row.SourcePriceID && references[pricing.Identity{ChannelID: candidate.ChannelID, ModelID: candidate.ModelID}].referenceCount > 0 {
				dependents = append(dependents, candidate)
			}
		}
		return modelPriceSourceInUse(source, dependents)
	}
	return nil
}

func modelPriceSourceInUse(source models.ModelPrice, dependents []models.ModelPrice) error {
	label := func(row models.ModelPrice) string {
		descriptor, _ := modelPriceChannelRegistry.Get(channel.ID(row.ChannelID))
		return descriptor.Name + " · " + row.ModelID
	}
	names := make([]string, 0, len(dependents))
	for _, row := range dependents {
		names = append(names, label(row))
	}
	return app_errors.NewAPIErrorWithData(app_errors.ErrModelPriceSourceReferenced, map[string]any{
		"source": label(source), "dependents": strings.Join(names, ", "),
	})
}

package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type ModelCandidate struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Sources       []string      `json:"sources"`
	PricingStatus PricingStatus `json:"pricing_status"`
	PricingSource *string       `json:"pricing_source"`
}

func loadModelPriceRows(
	ctx context.Context,
	db *gorm.DB,
) (modelPriceRows, error) {
	var rows []models.ModelPrice
	if err := db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load model price status: %w", app_errors.ParseDBError(err))
	}
	result := make(modelPriceRows, len(rows))
	for index := range rows {
		identity := pricing.Identity{ChannelID: rows[index].ChannelID, ModelID: rows[index].ModelID}
		if _, duplicate := result[identity]; duplicate {
			return nil, fmt.Errorf("duplicate persisted price identity: %w", app_errors.ErrInternalServer)
		}
		result[identity] = &rows[index]
	}
	return result, nil
}

type modelPriceRows map[pricing.Identity]*models.ModelPrice

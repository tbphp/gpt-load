package control

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

// PriceRuntime owns the current immutable pricing table.
type PriceRuntime struct {
	current atomic.Pointer[pricing.Table]
}

func NewPriceRuntime() *PriceRuntime {
	return &PriceRuntime{}
}

func (runtime *PriceRuntime) Load() *pricing.Table {
	if runtime == nil {
		return nil
	}
	return runtime.current.Load()
}

func (runtime *PriceRuntime) Publish(table *pricing.Table) {
	if runtime == nil || table == nil {
		return
	}
	runtime.current.Store(table)
}

func loadPriceTable(ctx context.Context, tx *gorm.DB) (*pricing.Table, error) {
	var rows []models.ModelPrice
	if err := tx.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load persisted model prices: %w", app_errors.ParseDBError(err))
	}
	rules := make([]pricing.Rule, 0, len(rows))
	for _, row := range rows {
		rule, err := persistedPriceRule(row)
		if err != nil {
			return nil, fmt.Errorf("decode persisted model price: %w", app_errors.ErrInternalServer)
		}
		rules = append(rules, rule)
	}
	table, err := pricing.NewTable(rules)
	if err != nil {
		return nil, fmt.Errorf("compile persisted model prices: %w", app_errors.ErrInternalServer)
	}
	return table, nil
}

func persistedPriceRule(row models.ModelPrice) (pricing.Rule, error) {
	identity, err := PriceIdentityForChannelModel(row.ChannelID, row.ModelID)
	if err != nil {
		return pricing.Rule{}, err
	}
	rule := pricing.Rule{
		Identity: identity,
		Prices: pricing.Prices{
			Input:      priceFromStoragePointer(row.InputPriceNanoUSDPerMillionTokens),
			Output:     priceFromStoragePointer(row.OutputPriceNanoUSDPerMillionTokens),
			CacheRead:  priceFromStoragePointer(row.CacheReadPriceNanoUSDPerMillionTokens),
			CacheWrite: priceFromStoragePointer(row.CacheWritePriceNanoUSDPerMillionTokens),
		},
		IsManual: row.IsManual,
	}
	if len(row.ContextPriceTiers) == 0 {
		return rule, nil
	}
	normalized, err := models.NormalizeContextPriceTiers(row.ContextPriceTiers)
	if err != nil {
		return pricing.Rule{}, err
	}
	var tiers []models.ContextPriceTier
	if err := json.Unmarshal(normalized, &tiers); err != nil {
		return pricing.Rule{}, err
	}
	rule.ContextTiers = make([]pricing.ContextTier, 0, len(tiers))
	for _, tier := range tiers {
		rule.ContextTiers = append(rule.ContextTiers, pricing.ContextTier{
			InputThresholdTokens: tier.ThresholdTokens,
			Prices: pricing.Prices{
				Input:      priceFromStoragePointer(tier.InputPriceNanoUSDPerMillionTokens),
				Output:     priceFromStoragePointer(tier.OutputPriceNanoUSDPerMillionTokens),
				CacheRead:  priceFromStoragePointer(tier.CacheReadPriceNanoUSDPerMillionTokens),
				CacheWrite: priceFromStoragePointer(tier.CacheWritePriceNanoUSDPerMillionTokens),
			},
		})
	}
	return rule, nil
}

func priceFromStoragePointer(value *int64) pricing.Price {
	if value == nil {
		return pricing.Price{}
	}
	return pricing.Price{NanoUSDPerMillion: pricing.NanoUSD(*value), Set: true}
}

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

func loadPriceTable(
	ctx context.Context,
	tx *gorm.DB,
) (*pricing.Table, error) {
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

func persistedPriceRule(
	row models.ModelPrice,
) (pricing.Rule, error) {
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
	rule.ContextTiers, err = decodePersistedContextTiers(row.ContextPriceTiers)
	if err != nil {
		return pricing.Rule{}, err
	}
	if len(row.ModePriceSchedules) == 0 {
		return rule, nil
	}
	normalizedSchedules, err := models.NormalizeModePriceSchedules(row.ModePriceSchedules)
	if err != nil {
		return pricing.Rule{}, err
	}
	var schedules map[string]models.ModePriceSchedule
	if err := json.Unmarshal(normalizedSchedules, &schedules); err != nil {
		return pricing.Rule{}, err
	}
	rule.ModeSchedules = make(map[pricing.Mode]pricing.Schedule, len(schedules))
	for mode, schedule := range schedules {
		tiers, err := pricingContextTiersFromStorage(schedule.ContextPriceTiers)
		if err != nil {
			return pricing.Rule{}, err
		}
		rule.ModeSchedules[pricing.Mode(mode)] = pricing.Schedule{
			Prices: pricing.Prices{
				Input:      priceFromStoragePointer(schedule.Prices.InputPriceNanoUSDPerMillionTokens),
				Output:     priceFromStoragePointer(schedule.Prices.OutputPriceNanoUSDPerMillionTokens),
				CacheRead:  priceFromStoragePointer(schedule.Prices.CacheReadPriceNanoUSDPerMillionTokens),
				CacheWrite: priceFromStoragePointer(schedule.Prices.CacheWritePriceNanoUSDPerMillionTokens),
			},
			ContextTiers: tiers,
		}
	}
	return rule, nil
}

func decodePersistedContextTiers(raw models.JSON) ([]pricing.ContextTier, error) {
	normalized, err := models.NormalizeContextPriceTiers(raw)
	if err != nil || len(normalized) == 0 {
		return nil, err
	}
	var tiers []models.ContextPriceTier
	if err := json.Unmarshal(normalized, &tiers); err != nil {
		return nil, err
	}
	return pricingContextTiersFromStorage(tiers)
}

func pricingContextTiersFromStorage(tiers []models.ContextPriceTier) ([]pricing.ContextTier, error) {
	result := make([]pricing.ContextTier, 0, len(tiers))
	for _, tier := range tiers {
		result = append(result, pricing.ContextTier{
			InputThresholdTokens: tier.ThresholdTokens,
			Prices: pricing.Prices{
				Input:      priceFromStoragePointer(tier.InputPriceNanoUSDPerMillionTokens),
				Output:     priceFromStoragePointer(tier.OutputPriceNanoUSDPerMillionTokens),
				CacheRead:  priceFromStoragePointer(tier.CacheReadPriceNanoUSDPerMillionTokens),
				CacheWrite: priceFromStoragePointer(tier.CacheWritePriceNanoUSDPerMillionTokens),
			},
		})
	}
	return result, nil
}

func priceFromStoragePointer(value *int64) pricing.Price {
	if value == nil {
		return pricing.Price{}
	}
	return pricing.Price{NanoUSDPerMillion: pricing.NanoUSD(*value), Set: true}
}

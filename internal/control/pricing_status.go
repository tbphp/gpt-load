package control

import (
	"gpt-load/internal/catalog"
	"gpt-load/internal/storage/models"
)

type PricingStatus string

const (
	PricingStatusPending    PricingStatus = "pending"
	PricingStatusConfigured PricingStatus = "configured"
)

func resolvePricingStatus(row *models.ModelPrice) PricingStatus {
	if row == nil {
		return PricingStatusPending
	}
	if row.IsManual ||
		row.InputPriceNanoUSDPerMillionTokens != nil ||
		row.OutputPriceNanoUSDPerMillionTokens != nil ||
		row.CacheReadPriceNanoUSDPerMillionTokens != nil ||
		row.CacheWritePriceNanoUSDPerMillionTokens != nil {
		return PricingStatusConfigured
	}
	return PricingStatusPending
}

func resolveCandidatePricingStatus(
	row *models.ModelPrice,
	model *catalog.Model,
) PricingStatus {
	if row != nil {
		return resolvePricingStatus(row)
	}
	if model == nil || model.Cost == nil {
		return PricingStatusPending
	}
	prices := model.Cost.Prices
	if prices.Input.Set || prices.Output.Set || prices.CacheRead.Set || prices.CacheWrite.Set ||
		len(model.Cost.ContextTiers) > 0 {
		return PricingStatusConfigured
	}
	return PricingStatusPending
}

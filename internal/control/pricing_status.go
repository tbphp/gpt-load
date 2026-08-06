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
	if row.IsManual || modelPriceHasConfiguredValue(*row) {
		return PricingStatusConfigured
	}
	return PricingStatusPending
}

func modelPriceHasConfiguredValue(row models.ModelPrice) bool {
	return row.InputPriceNanoUSDPerMillionTokens != nil ||
		row.OutputPriceNanoUSDPerMillionTokens != nil ||
		row.CacheReadPriceNanoUSDPerMillionTokens != nil ||
		row.CacheWritePriceNanoUSDPerMillionTokens != nil ||
		len(row.ContextPriceTiers) > 0
}

func resolveCandidatePricingStatus(
	row *models.ModelPrice,
	snapshot *catalog.Snapshot,
	scopeProviderID string,
	modelID string,
) PricingStatus {
	if row != nil {
		return resolvePricingStatus(row)
	}
	if _, _, ok := resolveAutomaticPrice(snapshot, scopeProviderID, modelID); ok {
		return PricingStatusConfigured
	}
	return PricingStatusPending
}

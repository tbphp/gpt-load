package control

import "gpt-load/internal/storage/models"

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

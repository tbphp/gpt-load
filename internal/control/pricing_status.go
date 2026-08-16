package control

import (
	"strings"

	"gpt-load/internal/catalog"
	"gpt-load/internal/pricing"
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
		len(row.ContextPriceTiers) > 0 ||
		len(row.ModePriceSchedules) > 0
}

func resolveCandidatePricing(
	row *models.ModelPrice,
	snapshot *catalog.Snapshot,
	identity pricing.Identity,
) (PricingStatus, *string) {
	if row != nil {
		status := resolvePricingStatus(row)
		if status != PricingStatusConfigured || !modelPriceHasConfiguredValue(*row) || row.IsManual {
			return status, nil
		}
		if row.ChannelID != identity.ChannelID || row.ModelID != identity.ModelID {
			return status, nil
		}
		if match, ok := resolveAutomaticPriceForIdentity(snapshot, identity); ok {
			source := pricingSourceName(snapshot, match.providerID, match.providerID)
			return status, &source
		}
		return status, nil
	}
	if match, ok := resolveAutomaticPriceForIdentity(snapshot, identity); ok {
		source := pricingSourceName(snapshot, match.providerID, match.providerID)
		return PricingStatusConfigured, &source
	}
	return PricingStatusPending, nil
}

func pricingSourceName(snapshot *catalog.Snapshot, providerID string, fallback string) string {
	if snapshot != nil && providerID != "" {
		if provider, exists := snapshot.Providers[providerID]; exists {
			if name := strings.TrimSpace(provider.Name); name != "" {
				return name
			}
		}
	}
	return fallback
}

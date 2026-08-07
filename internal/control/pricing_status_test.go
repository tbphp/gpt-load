package control

import (
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestResolveCandidatePricingUsesCurrentAutomaticPriceMatch(t *testing.T) {
	price := int64(0)
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai", Name: "OpenAI",
			Models: map[string]catalog.Model{
				"gpt-4o": {
					ID: "gpt-4o",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{
						Input: pricing.Price{NanoUSDPerMillion: 1, Set: true},
					}},
				},
			},
		},
	}}
	status, source := resolveCandidatePricing(
		&models.ModelPrice{InputPriceNanoUSDPerMillionTokens: &price},
		snapshot,
		"gpt-4o",
	)
	if status != PricingStatusConfigured || source == nil || *source != "OpenAI" {
		t.Fatalf("pricing = %q, %v, want configured and automatic Provider match", status, source)
	}
}

func TestResolveCandidatePricingHidesManuallyUnpricedSource(t *testing.T) {
	status, source := resolveCandidatePricing(
		&models.ModelPrice{IsManual: true},
		nil,
		"gpt-4o",
	)
	if status != PricingStatusConfigured || source != nil {
		t.Fatalf("pricing = %q, %v, want configured without source", status, source)
	}
}

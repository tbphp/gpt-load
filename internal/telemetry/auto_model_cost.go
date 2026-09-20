package telemetry

import (
	"gpt-load/internal/automodel"
	"gpt-load/internal/pricing"
)

// TotalPricing sums the separately rounded quotes without merging token usage.
func TotalPricing(answer PricingObservation, decision *automodel.Decision) PricingObservation {
	if decision == nil {
		return answer
	}
	amount, ok := pricing.CheckedAddNanoUSD(pricing.NanoUSD(answer.EstimatedCostNanoUSD), pricing.NanoUSD(decision.EstimatedCostNanoUSD))
	if !ok {
		return PricingObservation{CostState: "unpriced", PricingCompleteness: "unavailable"}
	}
	result := PricingObservation{EstimatedCostNanoUSD: int64(amount)}
	known := answer.CostState == "priced" || decision.CostState == "priced"
	unknown := answer.CostState == "unpriced" || decision.CostState == "unpriced"
	partial := answer.PricingCompleteness == "partial" || decision.PricingCompleteness == "partial"
	switch {
	case known:
		result.CostState, result.PricingCompleteness = "priced", "complete"
		if unknown || partial {
			result.PricingCompleteness = "partial"
		}
	case unknown:
		result.CostState, result.PricingCompleteness = "unpriced", "unavailable"
	default:
		result.CostState, result.PricingCompleteness = "not_applicable", "not_applicable"
	}
	return result
}

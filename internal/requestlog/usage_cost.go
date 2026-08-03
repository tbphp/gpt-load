package requestlog

import (
	"gpt-load/internal/pricing"
	"gpt-load/internal/usage"
)

func ValidateUsageCostState(
	usageState usage.State,
	costState pricing.CostState,
	completeness pricing.Completeness,
	estimatedCostNanoUSD int64,
) error {
	return validateFrozenPricingState(usageState, costState, completeness, estimatedCostNanoUSD)
}

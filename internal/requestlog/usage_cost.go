package requestlog

import (
	"gpt-load/internal/pricing"
	"gpt-load/internal/usage"
	"gpt-load/internal/usagecost"
)

func ValidateUsageCostState(
	usageState usage.State,
	costState pricing.CostState,
	estimatedCostNanoUSD int64,
) error {
	return usagecost.Validate(usageState, costState, estimatedCostNanoUSD)
}

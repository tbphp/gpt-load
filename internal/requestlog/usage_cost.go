package requestlog

import (
	"fmt"
	"math"

	"gpt-load/internal/pricing"
	"gpt-load/internal/usage"
)

func ValidateUsageCostState(
	usageState usage.State,
	costState pricing.CostState,
	cost float64,
) error {
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return fmt.Errorf("invalid estimated cost")
	}
	validCombination := false
	switch usageState {
	case usage.StateComplete, usage.StatePartial:
		validCombination = costState == pricing.CostStatePriced ||
			costState == pricing.CostStateUnpriced
	case usage.StateMissing:
		validCombination = costState == pricing.CostStateUnpriced
	case usage.StateNotApplicable:
		validCombination = costState == pricing.CostStateNotApplicable
	}
	if !validCombination {
		return fmt.Errorf("invalid usage and cost state combination")
	}
	if costState != pricing.CostStatePriced && cost != 0 {
		return fmt.Errorf("unpriced or not-applicable cost must be zero")
	}
	return nil
}

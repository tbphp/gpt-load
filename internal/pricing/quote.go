package pricing

import (
	"math"

	"gpt-load/internal/usage"
)

const tokensPerMillion = 1_000_000

// Quote prices a finalized usage result without modifying it.
func (table *Table) Quote(upstreamModel string, result usage.Result) Quote {
	switch result.State {
	case usage.StateNotApplicable:
		return Quote{State: CostStateNotApplicable}
	case usage.StateMissing:
		return Quote{State: CostStateUnpriced}
	case usage.StateComplete, usage.StatePartial:
	default:
		return Quote{State: CostStateUnpriced}
	}

	rule, ok := table.Match(upstreamModel)
	if !ok || hasBlockingDiagnostic(result.Diagnostics) {
		return Quote{State: CostStateUnpriced}
	}

	components := [...]struct {
		tokens int64
		price  Price
	}{
		{tokens: result.Tokens.UncachedInput, price: rule.Prices.UncachedInput},
		{tokens: result.Tokens.CacheRead, price: rule.Prices.CacheRead},
		{tokens: result.Tokens.CacheWrite5M, price: rule.Prices.CacheWrite5M},
		{tokens: result.Tokens.CacheWrite1H, price: rule.Prices.CacheWrite1H},
		{tokens: result.Tokens.Output, price: rule.Prices.Output},
	}

	totalTokens := int64(0)
	cost := float64(0)
	for _, component := range components {
		if component.tokens < 0 {
			return Quote{State: CostStateUnpriced}
		}
		var added bool
		totalTokens, added = usage.CheckedAdd(totalTokens, component.tokens)
		if !added {
			return Quote{State: CostStateUnpriced}
		}
		if component.tokens == 0 {
			continue
		}
		if !component.price.Set {
			return Quote{State: CostStateUnpriced}
		}
		componentCost := component.price.Value * float64(component.tokens) / tokensPerMillion
		if !isFinite(componentCost) {
			return Quote{State: CostStateUnpriced}
		}
		cost += componentCost
		if !isFinite(cost) {
			return Quote{State: CostStateUnpriced}
		}
	}
	return Quote{State: CostStatePriced, Cost: cost}
}

func hasBlockingDiagnostic(diagnostics usage.Diagnostics) bool {
	for _, code := range [...]usage.DiagnosticCode{
		usage.DiagnosticUnsupportedBillableDetail,
		usage.DiagnosticNegativeValue,
		usage.DiagnosticInvalidNumber,
		usage.DiagnosticMissingRequiredField,
		usage.DiagnosticInconsistentTotal,
		usage.DiagnosticInvalidEventSequence,
	} {
		if diagnostics.Has(code) {
			return true
		}
	}
	return false
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

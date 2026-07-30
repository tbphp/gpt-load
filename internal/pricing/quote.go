package pricing

import "gpt-load/internal/usage"

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

	inputTokens, ok := checkedInputTokens(result.Tokens)
	if !ok {
		return Quote{State: CostStateUnpriced}
	}
	inputMultiplier := Multiplier{Numerator: 1, Denominator: 1}
	outputMultiplier := Multiplier{Numerator: 1, Denominator: 1}
	if policy := rule.LongContextPolicy; policy != nil &&
		inputTokens > policy.InputThresholdTokens {
		inputMultiplier = policy.InputMultiplier
		outputMultiplier = policy.OutputMultiplier
	}

	components := [...]struct {
		tokens     int64
		price      Price
		multiplier Multiplier
	}{
		{tokens: result.Tokens.UncachedInput, price: rule.Prices.UncachedInput, multiplier: inputMultiplier},
		{tokens: result.Tokens.CacheRead, price: rule.Prices.CacheRead, multiplier: inputMultiplier},
		{tokens: result.Tokens.CacheWrite5M, price: rule.Prices.CacheWrite5M, multiplier: inputMultiplier},
		{tokens: result.Tokens.CacheWrite1H, price: rule.Prices.CacheWrite1H, multiplier: inputMultiplier},
		{tokens: result.Tokens.Output, price: rule.Prices.Output, multiplier: outputMultiplier},
	}

	totalTokens := int64(0)
	cost := NanoUSD(0)
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
		componentCost, ok := QuoteComponent(
			component.tokens,
			component.price.NanoUSDPerMillion,
			component.multiplier,
		)
		if !ok {
			return Quote{State: CostStateUnpriced}
		}
		cost, ok = CheckedAddNanoUSD(cost, componentCost)
		if !ok {
			return Quote{State: CostStateUnpriced}
		}
	}
	return Quote{State: CostStatePriced, EstimatedCostNanoUSD: cost}
}

func checkedInputTokens(tokens usage.Tokens) (int64, bool) {
	total := int64(0)
	for _, value := range [...]int64{
		tokens.UncachedInput,
		tokens.CacheRead,
		tokens.CacheWrite5M,
		tokens.CacheWrite1H,
	} {
		if value < 0 {
			return 0, false
		}
		var ok bool
		total, ok = usage.CheckedAdd(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
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

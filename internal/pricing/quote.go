package pricing

import "gpt-load/internal/usage"

const tokensPerMillion = 1_000_000

var (
	directPriceMultiplier       = Multiplier{Numerator: 1, Denominator: 1}
	cacheWriteOneHourMultiplier = Multiplier{Numerator: 8, Denominator: 5}
)

// Quote prices a finalized usage result without modifying it.
func (table *Table) Quote(identity Identity, result usage.Result) Quote {
	switch result.State {
	case usage.StateNotApplicable:
		return Quote{State: CostStateNotApplicable, Completeness: CompletenessNotApplicable}
	case usage.StateMissing:
		return unavailableQuote()
	case usage.StateComplete, usage.StatePartial:
	default:
		return unavailableQuote()
	}

	rule, ok := table.Lookup(identity)
	if !ok {
		return unavailableQuote()
	}
	if _, ok := usage.CheckedTotal(result.Tokens); !ok {
		return unavailableQuote()
	}
	inputTokens, ok := checkedInputTokens(result.Tokens)
	if !ok {
		return unavailableQuote()
	}

	prices := rule.Prices
	for _, tier := range rule.ContextTiers {
		if inputTokens < tier.InputThresholdTokens {
			break
		}
		prices = tier.Prices
	}

	components := [...]struct {
		tokens     int64
		price      Price
		multiplier Multiplier
	}{
		{tokens: result.Tokens.UncachedInput, price: prices.Input, multiplier: directPriceMultiplier},
		{tokens: result.Tokens.CacheRead, price: prices.CacheRead, multiplier: directPriceMultiplier},
		{tokens: result.Tokens.CacheWrite5M, price: prices.CacheWrite, multiplier: directPriceMultiplier},
		{tokens: result.Tokens.CacheWrite1H, price: prices.CacheWrite, multiplier: cacheWriteOneHourMultiplier},
		{tokens: result.Tokens.Output, price: prices.Output, multiplier: directPriceMultiplier},
	}

	positiveBillable := result.Tokens.CacheWriteUnknown > 0
	pricedPositive := false
	partial := result.State == usage.StatePartial ||
		result.Tokens.CacheWriteUnknown > 0 ||
		hasIncompleteDiagnostic(result.Diagnostics)
	cost := NanoUSD(0)
	for _, component := range components {
		if component.tokens == 0 {
			continue
		}
		positiveBillable = true
		if !component.price.Set {
			partial = true
			continue
		}
		componentCost, ok := QuoteComponent(
			component.tokens,
			component.price.NanoUSDPerMillion,
			component.multiplier,
		)
		if !ok {
			return unavailableQuote()
		}
		cost, ok = CheckedAddNanoUSD(cost, componentCost)
		if !ok {
			return unavailableQuote()
		}
		pricedPositive = true
	}

	if positiveBillable && !pricedPositive {
		return unavailableQuote()
	}
	completeness := CompletenessComplete
	if partial {
		completeness = CompletenessPartial
	}
	return Quote{
		State:                CostStatePriced,
		Completeness:         completeness,
		EstimatedCostNanoUSD: cost,
	}
}

func checkedInputTokens(tokens usage.Tokens) (int64, bool) {
	total := int64(0)
	for _, value := range [...]int64{
		tokens.UncachedInput,
		tokens.CacheRead,
		tokens.CacheWrite5M,
		tokens.CacheWrite1H,
		tokens.CacheWriteUnknown,
	} {
		var ok bool
		total, ok = usage.CheckedAdd(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

func unavailableQuote() Quote {
	return Quote{State: CostStateUnpriced, Completeness: CompletenessUnavailable}
}

func hasIncompleteDiagnostic(diagnostics usage.Diagnostics) bool {
	for _, code := range [...]usage.DiagnosticCode{
		usage.DiagnosticUnsupportedBillableDetail,
		usage.DiagnosticNegativeValue,
		usage.DiagnosticInvalidNumber,
		usage.DiagnosticMissingRequiredField,
		usage.DiagnosticInconsistentTotal,
		usage.DiagnosticInvalidPayload,
		usage.DiagnosticInvalidEventSequence,
	} {
		if diagnostics.Has(code) {
			return true
		}
	}
	return false
}

package pricing

import "gpt-load/internal/usage"

const tokensPerMillion = 1_000_000

var (
	directPriceMultiplier       = Multiplier{Numerator: 1, Denominator: 1}
	cacheWriteOneHourMultiplier = Multiplier{Numerator: 8, Denominator: 5}
)

// Quote prices a finalized usage result without modifying it.
func (table *Table) Quote(identity Identity, result usage.Result) Quote {
	quote, _ := table.QuoteForModeWithReceipt(identity, result, ModeStandard)
	return quote
}

// QuoteWithReceipt prices a finalized usage result and freezes the exact rule,
// tier, rates, multipliers, and component amounts used for the calculation.
func (table *Table) QuoteWithReceipt(
	identity Identity,
	result usage.Result,
) (Quote, *Receipt) {
	return table.QuoteForModeWithReceipt(identity, result, ModeStandard)
}

// QuoteForMode prices a finalized usage result. A matched standard context
// tier takes precedence over the requested mode schedule, which otherwise
// falls back to the standard base schedule when unavailable.
func (table *Table) QuoteForMode(identity Identity, result usage.Result, mode Mode) Quote {
	quote, _ := table.QuoteForModeWithReceipt(identity, result, mode)
	return quote
}

// QuoteForModeWithMultipliers 在原计价完成后统一调整请求总费用。
func (table *Table) QuoteForModeWithMultipliers(identity Identity, result usage.Result, mode Mode, multipliers PriceMultipliers) (Quote, *Receipt) {
	if !multipliers.Group.Valid() || !multipliers.AccessKey.Valid() {
		return unavailableQuote(), nil
	}
	quote, receipt := table.QuoteForModeWithReceipt(identity, result, mode)
	if receipt == nil {
		return quote, nil
	}
	baseTotal := receipt.TotalNanoUSD
	if quote.State == CostStatePriced {
		adjusted, ok := applyPriceMultipliers(quote.EstimatedCostNanoUSD, multipliers)
		if !ok {
			return unavailableQuote(), nil
		}
		quote.EstimatedCostNanoUSD = adjusted
		receipt.TotalNanoUSD = int64(adjusted)
	}
	receipt.SchemaVersion = 6
	receipt.BaseTotalNanoUSD = &baseTotal
	receipt.PriceMultipliers = &multipliers
	return quote, receipt
}

// QuoteForModeWithReceipt freezes the exact tier or mode schedule used. A mode
// without a matching persisted schedule is indistinguishable from a standard
// quote.
func (table *Table) QuoteForModeWithReceipt(
	identity Identity,
	result usage.Result,
	mode Mode,
) (Quote, *Receipt) {
	switch result.State {
	case usage.StateNotApplicable:
		return Quote{State: CostStateNotApplicable, Completeness: CompletenessNotApplicable}, nil
	case usage.StateMissing:
		return unavailableQuote(), nil
	case usage.StateComplete, usage.StatePartial:
	default:
		return unavailableQuote(), nil
	}

	rule, ok := table.Lookup(identity)
	if !ok {
		return unavailableQuote(), nil
	}
	if _, ok := usage.CheckedTotal(result.Tokens); !ok {
		return unavailableQuote(), nil
	}
	inputTokens, ok := checkedInputTokens(result.Tokens)
	if !ok {
		return unavailableQuote(), nil
	}

	selectedMode := ModeStandard
	prices, selectedThreshold := selectSchedulePrices(rule.Prices, rule.ContextTiers, inputTokens)
	if selectedThreshold == nil && mode != "" && mode != ModeStandard {
		if schedule, exists := rule.ModeSchedules[mode]; exists {
			selectedMode = mode
			prices, selectedThreshold = selectSchedulePrices(
				schedule.Prices,
				schedule.ContextTiers,
				inputTokens,
			)
		}
	}

	components := [...]struct {
		code       string
		tokens     int64
		price      Price
		multiplier Multiplier
	}{
		{code: "input", tokens: result.Tokens.UncachedInput, price: prices.Input, multiplier: directPriceMultiplier},
		{code: "cache_read", tokens: result.Tokens.CacheRead, price: prices.CacheRead, multiplier: directPriceMultiplier},
		{code: "cache_write_5m", tokens: result.Tokens.CacheWrite5M, price: prices.CacheWrite, multiplier: directPriceMultiplier},
		{code: "cache_write_1h", tokens: result.Tokens.CacheWrite1H, price: prices.CacheWrite, multiplier: cacheWriteOneHourMultiplier},
		{code: "output", tokens: result.Tokens.Output, price: prices.Output, multiplier: directPriceMultiplier},
	}
	receipt := &Receipt{
		SchemaVersion: 4,
		Method:        ReceiptMethodUnitRateSum,
		MethodVersion: 1,
		Currency:      "USD",
		PricingMode:   selectedMode,
		Rule: ReceiptRule{
			ChannelID: identity.ChannelID,
			ModelID:   identity.ModelID,
		},
		ContextThresholdTokens: selectedThreshold,
		LineItems:              make([]ReceiptLine, 0, len(components)+1),
	}

	positiveBillable := result.Tokens.CacheWriteUnknown > 0
	pricedPositive := false
	partial := result.State == usage.StatePartial ||
		result.Tokens.CacheWriteUnknown > 0
	cost := NanoUSD(0)
	for _, component := range components {
		if component.tokens == 0 {
			continue
		}
		positiveBillable = true
		line := ReceiptLine{
			Code:       component.code,
			Quantity:   component.tokens,
			Multiplier: component.multiplier,
			State:      ReceiptLineUnpriced,
		}
		if !component.price.Set {
			partial = true
			receipt.LineItems = append(receipt.LineItems, line)
			continue
		}
		componentCost, ok := QuoteComponent(
			component.tokens,
			component.price.NanoUSDPerMillion,
			component.multiplier,
		)
		if !ok {
			return unavailableQuote(), nil
		}
		cost, ok = CheckedAddNanoUSD(cost, componentCost)
		if !ok {
			return unavailableQuote(), nil
		}
		rate := int64(component.price.NanoUSDPerMillion)
		amount := int64(componentCost)
		line.RateNanoUSDPerMillion = &rate
		line.AmountNanoUSD = &amount
		line.State = ReceiptLinePriced
		receipt.LineItems = append(receipt.LineItems, line)
		pricedPositive = true
	}
	// Billed passes that ran on another model (Anthropic advisor_message iterations) are priced at the
	// price row of the model that ran them, in the same channel, and added to the request total.
	passes, passesOK := extraPasses(result)
	if !passesOK {
		return unavailableQuote(), nil
	}
	for _, extra := range passes {
		extraCost, lines, extraPartial, extraPriced, ok := table.quoteExtraPass(identity, extra, mode)
		if !ok {
			return unavailableQuote(), nil
		}
		if cost, ok = CheckedAddNanoUSD(cost, extraCost); !ok {
			return unavailableQuote(), nil
		}
		receipt.LineItems = append(receipt.LineItems, lines...)
		partial = partial || extraPartial
		pricedPositive = pricedPositive || extraPriced
		positiveBillable = positiveBillable || len(lines) > 0
	}
	if result.Tokens.CacheWriteUnknown > 0 {
		receipt.LineItems = append(receipt.LineItems, ReceiptLine{
			Code:       "cache_write",
			Quantity:   result.Tokens.CacheWriteUnknown,
			Multiplier: directPriceMultiplier,
			State:      ReceiptLineUnpriced,
		})
	}
	receipt.TotalNanoUSD = int64(cost)

	if positiveBillable && !pricedPositive {
		return unavailableQuote(), receipt
	}
	completeness := CompletenessComplete
	if partial {
		completeness = CompletenessPartial
	}
	return Quote{
		State:                CostStatePriced,
		Completeness:         completeness,
		EstimatedCostNanoUSD: cost,
	}, receipt
}

func selectSchedulePrices(
	base Prices,
	tiers []ContextTier,
	inputTokens int64,
) (Prices, *int64) {
	prices := base
	var selectedThreshold *int64
	for _, tier := range tiers {
		if inputTokens < tier.InputThresholdTokens {
			break
		}
		prices = tier.Prices
		threshold := tier.InputThresholdTokens
		selectedThreshold = &threshold
	}
	return prices, selectedThreshold
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

// extraPasses returns the extra passes with the passes of one model summed (an advisor consulted three times is one
// receipt line per component, not three lines with the same code). ok is false when a sum overflows.
func extraPasses(result usage.Result) (passes []usage.ModelTokens, ok bool) {
	if result.Extra == nil {
		return nil, true
	}
	index := make(map[string]int, len(result.Extra.Passes))
	for _, pass := range result.Extra.Passes {
		at, seen := index[pass.Model]
		if !seen {
			index[pass.Model] = len(passes)
			passes = append(passes, pass)
			continue
		}
		sum := &passes[at].Tokens
		for _, add := range [...]struct {
			to   *int64
			from int64
		}{
			{&sum.UncachedInput, pass.Tokens.UncachedInput}, {&sum.CacheRead, pass.Tokens.CacheRead},
			{&sum.CacheWrite5M, pass.Tokens.CacheWrite5M}, {&sum.CacheWrite1H, pass.Tokens.CacheWrite1H},
			{&sum.CacheWriteUnknown, pass.Tokens.CacheWriteUnknown}, {&sum.Output, pass.Tokens.Output},
		} {
			if *add.to, ok = usage.CheckedAdd(*add.to, add.from); !ok {
				return nil, false
			}
		}
	}
	return passes, true
}

// ExtraLinePrefix starts the receipt line code of a pass priced at another model's row:
// "pass:<model>:<component>".
const ExtraLinePrefix = "pass:"

// quoteExtraPass prices one extra pass at the (channel, model) row of the model that ran it. A model without a
// price row leaves its lines unpriced and the quote partial; it is never priced at zero silently.
func (table *Table) quoteExtraPass(
	identity Identity,
	extra usage.ModelTokens,
	mode Mode,
) (cost NanoUSD, lines []ReceiptLine, partial, priced, ok bool) {
	rule, found := table.Lookup(Identity{ChannelID: identity.ChannelID, ModelID: extra.Model})
	inputTokens, inputOK := checkedInputTokens(extra.Tokens)
	if _, totalOK := usage.CheckedTotal(extra.Tokens); !totalOK || !inputOK {
		return 0, nil, false, false, false
	}
	var prices Prices
	if found {
		prices, _ = selectSchedulePrices(rule.Prices, rule.ContextTiers, inputTokens)
		if mode != "" && mode != ModeStandard {
			if schedule, exists := rule.ModeSchedules[mode]; exists {
				prices, _ = selectSchedulePrices(schedule.Prices, schedule.ContextTiers, inputTokens)
			}
		}
	}
	components := [...]struct {
		code       string
		tokens     int64
		price      Price
		multiplier Multiplier
	}{
		{code: "input", tokens: extra.Tokens.UncachedInput, price: prices.Input, multiplier: directPriceMultiplier},
		{code: "cache_read", tokens: extra.Tokens.CacheRead, price: prices.CacheRead, multiplier: directPriceMultiplier},
		{code: "cache_write_5m", tokens: extra.Tokens.CacheWrite5M, price: prices.CacheWrite, multiplier: directPriceMultiplier},
		{code: "cache_write_1h", tokens: extra.Tokens.CacheWrite1H, price: prices.CacheWrite, multiplier: cacheWriteOneHourMultiplier},
		{code: "output", tokens: extra.Tokens.Output, price: prices.Output, multiplier: directPriceMultiplier},
	}
	for _, component := range components {
		if component.tokens == 0 {
			continue
		}
		line := ReceiptLine{
			Code:       ExtraLinePrefix + extra.Model + ":" + component.code,
			Quantity:   component.tokens,
			Multiplier: component.multiplier,
			State:      ReceiptLineUnpriced,
		}
		if !found || !component.price.Set {
			partial = true
			lines = append(lines, line)
			continue
		}
		componentCost, componentOK := QuoteComponent(component.tokens, component.price.NanoUSDPerMillion, component.multiplier)
		if !componentOK {
			return 0, nil, false, false, false
		}
		if cost, componentOK = CheckedAddNanoUSD(cost, componentCost); !componentOK {
			return 0, nil, false, false, false
		}
		rate := int64(component.price.NanoUSDPerMillion)
		amount := int64(componentCost)
		line.RateNanoUSDPerMillion = &rate
		line.AmountNanoUSD = &amount
		line.State = ReceiptLinePriced
		lines = append(lines, line)
		priced = true
	}
	if extra.Tokens.CacheWriteUnknown > 0 {
		partial = true
		lines = append(lines, ReceiptLine{
			Code:       ExtraLinePrefix + extra.Model + ":cache_write",
			Quantity:   extra.Tokens.CacheWriteUnknown,
			Multiplier: directPriceMultiplier,
			State:      ReceiptLineUnpriced,
		})
	}
	return cost, lines, partial, priced, true
}

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
	receipt.SchemaVersion = 7
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
		LineItems:              make([]ReceiptLine, 0, 6),
	}

	lines := quoteLines{receipt: receipt, partial: result.State == usage.StatePartial}
	if !lines.add("", "", result.Tokens, prices) {
		return unavailableQuote(), nil
	}
	if result.Advisor != nil {
		for _, advisor := range result.Advisor.Models {
			if !table.addAdvisorLines(&lines, identity.ChannelID, advisor) {
				return unavailableQuote(), nil
			}
		}
	}
	receipt.TotalNanoUSD = int64(lines.cost)

	if lines.positiveBillable && !lines.pricedPositive {
		return unavailableQuote(), receipt
	}
	completeness := CompletenessComplete
	if lines.partial {
		completeness = CompletenessPartial
	}
	return Quote{
		State:                CostStatePriced,
		Completeness:         completeness,
		EstimatedCostNanoUSD: lines.cost,
	}, receipt
}

// advisorLinePrefix 标记按 advisor 模型计价的明细行，仅 v7 明细允许出现。
const advisorLinePrefix = "advisor_"

// quoteLines 逐项写入明细行并累计请求总价。
type quoteLines struct {
	receipt          *Receipt
	cost             NanoUSD
	positiveBillable bool
	pricedPositive   bool
	partial          bool
}

// add 按 prices 为 tokens 的每个正数分量追加明细行；prefix 与 modelID 只用于 advisor 行。
func (lines *quoteLines) add(prefix, modelID string, tokens usage.Tokens, prices Prices) bool {
	for _, component := range [...]struct {
		code       string
		tokens     int64
		price      Price
		multiplier Multiplier
	}{
		{code: "input", tokens: tokens.UncachedInput, price: prices.Input, multiplier: directPriceMultiplier},
		{code: "cache_read", tokens: tokens.CacheRead, price: prices.CacheRead, multiplier: directPriceMultiplier},
		{code: "cache_write_5m", tokens: tokens.CacheWrite5M, price: prices.CacheWrite, multiplier: directPriceMultiplier},
		{code: "cache_write_1h", tokens: tokens.CacheWrite1H, price: prices.CacheWrite, multiplier: cacheWriteOneHourMultiplier},
		{code: "output", tokens: tokens.Output, price: prices.Output, multiplier: directPriceMultiplier},
	} {
		if component.tokens == 0 {
			continue
		}
		lines.positiveBillable = true
		line := ReceiptLine{
			Code:       prefix + component.code,
			ModelID:    modelID,
			Quantity:   component.tokens,
			Multiplier: component.multiplier,
			State:      ReceiptLineUnpriced,
		}
		if !component.price.Set {
			lines.partial = true
			lines.receipt.LineItems = append(lines.receipt.LineItems, line)
			continue
		}
		componentCost, ok := QuoteComponent(
			component.tokens,
			component.price.NanoUSDPerMillion,
			component.multiplier,
		)
		if !ok {
			return false
		}
		lines.cost, ok = CheckedAddNanoUSD(lines.cost, componentCost)
		if !ok {
			return false
		}
		rate := int64(component.price.NanoUSDPerMillion)
		amount := int64(componentCost)
		line.RateNanoUSDPerMillion = &rate
		line.AmountNanoUSD = &amount
		line.State = ReceiptLinePriced
		lines.receipt.LineItems = append(lines.receipt.LineItems, line)
		lines.pricedPositive = true
	}
	if tokens.CacheWriteUnknown > 0 {
		lines.positiveBillable = true
		lines.partial = true
		lines.receipt.LineItems = append(lines.receipt.LineItems, ReceiptLine{
			Code:       prefix + "cache_write",
			ModelID:    modelID,
			Quantity:   tokens.CacheWriteUnknown,
			Multiplier: directPriceMultiplier,
			State:      ReceiptLineUnpriced,
		})
	}
	return true
}

// addAdvisorLines 按同一渠道下 advisor 模型的标准价计价；缺少价格行时各项记为未计价。
func (table *Table) addAdvisorLines(lines *quoteLines, channelID string, advisor usage.ModelTokens) bool {
	if validateModelID(advisor.Model) != nil {
		return false
	}
	if _, ok := usage.CheckedTotal(advisor.Tokens); !ok {
		return false
	}
	inputTokens, ok := checkedInputTokens(advisor.Tokens)
	if !ok {
		return false
	}
	var prices Prices
	if rule, found := table.Lookup(Identity{ChannelID: channelID, ModelID: advisor.Model}); found {
		prices, _ = selectSchedulePrices(rule.Prices, rule.ContextTiers, inputTokens)
	}
	return lines.add(advisorLinePrefix, advisor.Model, advisor.Tokens, prices)
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

package pricing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"gpt-load/internal/usage"
)

func TestQuotePriceMultipliersAdjustsCompletedBaseTotal(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	for _, test := range []struct {
		name   string
		tokens int64
		rate   NanoUSD
		group  PriceMultiplier
		key    PriceMultiplier
		want   NanoUSD
	}{
		{"preserve original component rounding", 1, 490_000, 3_000_000, DefaultPriceMultiplier, 0},
		{"avoid rounding between factors", 1, 1_000_000, 500_000, 1_500_000, 1},
		{"half up at final nanodollar", 1, 1_000_000, 500_000, DefaultPriceMultiplier, 1},
		{"exact fractional product", 1_000_000, 100, 800_000, 1_500_000, 120},
		{"zero group", 1_000_000, 100, 0, 1_500_000, 0},
		{"zero access key", 1_000_000, 100, 800_000, 0, 0},
		{"full six decimal precision", 1_000_000, 1_000_000_000_000, 123_456, 654_321, 80_779_853_376},
		{"large intermediate stays exact", 1_000_000, math.MaxInt64, 1_000_000_000, 1_000, math.MaxInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(test.rate)}})
			multipliers := PriceMultipliers{Group: test.group, AccessKey: test.key}
			quote, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
				State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: test.tokens},
			}, ModeStandard, multipliers)
			if quote != (Quote{State: CostStatePriced, Completeness: CompletenessComplete, EstimatedCostNanoUSD: test.want}) {
				t.Fatalf("quote = %#v, want amount %d", quote, test.want)
			}
			if receipt == nil || receipt.SchemaVersion != 6 || receipt.PriceMultipliers == nil || *receipt.PriceMultipliers != multipliers {
				t.Fatalf("receipt did not freeze v6 multipliers: %#v", receipt)
			}
			baseQuote, baseReceipt := table.QuoteForModeWithReceipt(identity, usage.Result{
				State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: test.tokens},
			}, ModeStandard)
			if baseReceipt == nil || !reflect.DeepEqual(receipt.LineItems, baseReceipt.LineItems) {
				t.Fatalf("adjustment changed original line items: %#v", receipt.LineItems)
			}

			if err := ValidateReceipt(*receipt); err != nil {
				t.Fatalf("generated v6 receipt: %v", err)
			}
			encoded, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			var frozen struct {
				BaseTotal *int64 `json:"base_total_nano_usd"`
			}
			if err := json.Unmarshal(encoded, &frozen); err != nil {
				t.Fatal(err)
			}
			if frozen.BaseTotal == nil || *frozen.BaseTotal != int64(baseQuote.EstimatedCostNanoUSD) {
				t.Fatalf("receipt base total = %#v, want %d", frozen, baseQuote.EstimatedCostNanoUSD)
			}
			var decoded Receipt
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if err := ValidateReceipt(decoded); err != nil {
				t.Fatalf("round-tripped v6 receipt: %v", err)
			}
		})
	}
}

func TestQuotePriceMultipliersAppliesAfterSummingOriginalRoundedComponents(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{
		Identity: identity,
		Prices:   Prices{Input: fixedPrice(600_000), Output: fixedPrice(600_000)},
	})
	quote, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1, Output: 1},
	}, ModeStandard, PriceMultipliers{Group: 2_000_000, AccessKey: DefaultPriceMultiplier})
	// 原计价先把两个 0.6 纳美元分项各舍入为 1，基础总额 2 再乘 2 得到 4。
	if quote != (Quote{State: CostStatePriced, Completeness: CompletenessComplete, EstimatedCostNanoUSD: 4}) {
		t.Fatalf("quote = %#v, want original total 2 adjusted to 4", quote)
	}
	if receipt == nil || receipt.TotalNanoUSD != 4 || len(receipt.LineItems) != 2 {
		t.Fatalf("receipt = %#v, want two base lines with adjusted total 4", receipt)
	}
	for index, code := range []string{"input", "output"} {
		line := receipt.LineItems[index]
		if line.Code != code || line.AmountNanoUSD == nil || *line.AmountNanoUSD != 1 {
			t.Fatalf("receipt line = %#v, want %s amount 1", line, code)
		}
	}
	if err := ValidateReceipt(*receipt); err != nil {
		t.Fatalf("individually rounded receipt: %v", err)
	}
}

func TestQuotePriceMultipliersPreserveContextFastAndCacheSelection(t *testing.T) {
	identity := Identity{ChannelID: "anthropic", ModelID: "model"}
	table := mustTable(t, Rule{
		Identity:      identity,
		Prices:        Prices{Input: fixedPrice(10), CacheWrite: fixedPrice(25)},
		ContextTiers:  []ContextTier{{InputThresholdTokens: 2_000_000, Prices: Prices{Input: fixedPrice(20), CacheWrite: fixedPrice(50)}}},
		ModeSchedules: map[Mode]Schedule{ModeFast: {Prices: Prices{Input: fixedPrice(30), CacheWrite: fixedPrice(75)}}},
	})
	for _, test := range []struct {
		name     string
		mode     Mode
		tokens   usage.Tokens
		wantMode Mode
		wantTier bool
		want     NanoUSD
	}{
		{"standard base", ModeStandard, usage.Tokens{CacheWrite1H: 1_000_000}, ModeStandard, false, 48},
		{"fast base", ModeFast, usage.Tokens{CacheWrite1H: 1_000_000}, ModeFast, false, 144},
		{"tier wins over fast", ModeFast, usage.Tokens{UncachedInput: 1_000_000, CacheWrite1H: 1_000_000}, ModeStandard, true, 120},
	} {
		t.Run(test.name, func(t *testing.T) {
			quote, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{State: usage.StateComplete, Tokens: test.tokens}, test.mode, PriceMultipliers{Group: 800_000, AccessKey: 1_500_000})
			if quote.EstimatedCostNanoUSD != test.want || receipt == nil || receipt.PricingMode != test.wantMode || (receipt.ContextThresholdTokens != nil) != test.wantTier {
				t.Fatalf("quote = %#v, receipt = %#v", quote, receipt)
			}
			line := receipt.LineItems[len(receipt.LineItems)-1]
			if line.Multiplier != cacheWriteOneHourMultiplier {
				t.Fatalf("cache multiplier = %#v", line.Multiplier)
			}
			if err := ValidateReceipt(*receipt); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQuotePriceMultipliersPreserveUnavailableAndPartialStates(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(100)}})
	zero := PriceMultipliers{Group: 0, AccessKey: DefaultPriceMultiplier}
	for _, test := range []struct {
		name        string
		identity    Identity
		result      usage.Result
		want        Quote
		wantReceipt bool
	}{
		{"missing usage", identity, usage.Result{State: usage.StateMissing}, unavailableQuote(), false},
		{"not applicable", identity, usage.Result{State: usage.StateNotApplicable}, Quote{State: CostStateNotApplicable, Completeness: CompletenessNotApplicable}, false},
		{"missing rule", Identity{ChannelID: "openai", ModelID: "missing"}, usage.Result{State: usage.StateComplete}, unavailableQuote(), false},
		{"all prices missing", identity, usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: 10}}, unavailableQuote(), true},
		{"one price missing", identity, usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, Output: 10}}, Quote{State: CostStatePriced, Completeness: CompletenessPartial}, true},
		{"unknown cache duration", identity, usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, CacheWriteUnknown: 10}}, Quote{State: CostStatePriced, Completeness: CompletenessPartial}, true},
		{"partial usage", identity, usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 10}}, Quote{State: CostStatePriced, Completeness: CompletenessPartial}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			quote, receipt := table.QuoteForModeWithMultipliers(test.identity, test.result, ModeStandard, zero)
			if quote != test.want || (receipt != nil) != test.wantReceipt {
				t.Fatalf("quote = %#v, receipt = %#v; want %#v, receipt %t", quote, receipt, test.want, test.wantReceipt)
			}
			if receipt != nil {
				if err := ValidateReceipt(*receipt); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestQuotePriceMultipliersFailClosedForInvalidFactorsAndOverflow(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(math.MaxInt64), Output: fixedPrice(math.MaxInt64)}})
	for _, test := range []struct {
		name        string
		multipliers PriceMultipliers
		tokens      usage.Tokens
	}{
		{"invalid group", PriceMultipliers{Group: -1, AccessKey: DefaultPriceMultiplier}, usage.Tokens{UncachedInput: 1}},
		{"invalid access key", PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: 1_000_000_001}, usage.Tokens{UncachedInput: 1}},
		{"component overflow", PriceMultipliers{Group: 2_000_000, AccessKey: DefaultPriceMultiplier}, usage.Tokens{UncachedInput: 1_000_000}},
		{"sum overflow", PriceMultipliers{Group: 2_000_000, AccessKey: DefaultPriceMultiplier}, usage.Tokens{UncachedInput: 250_000, Output: 250_000}},
		{"base overflow is not rescued by discount", PriceMultipliers{Group: 500_000, AccessKey: DefaultPriceMultiplier}, usage.Tokens{UncachedInput: 2_000_000}},
		{"base overflow is not rescued by zero", PriceMultipliers{Group: 0, AccessKey: DefaultPriceMultiplier}, usage.Tokens{UncachedInput: 2_000_000}},
		{"negative tokens even at zero", PriceMultipliers{}, usage.Tokens{UncachedInput: -1}},
		{"token total overflow even at zero", PriceMultipliers{}, usage.Tokens{UncachedInput: math.MaxInt64, Output: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			quote, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{State: usage.StateComplete, Tokens: test.tokens}, ModeStandard, test.multipliers)
			if quote != unavailableQuote() || receipt != nil {
				t.Fatalf("quote = %#v, receipt = %#v; want unavailable with no receipt", quote, receipt)
			}
		})
	}
}

func TestQuotePolicyFactorsAppliesExactArithmetic(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(100)}})
	baseMultipliers := PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}

	// 1. x2, x3, x0.5 => net x3 (Base 100 -> 300)
	f2, _ := ParsePriceMultiplier("2")
	f3, _ := ParsePriceMultiplier("3")
	f05, _ := ParsePriceMultiplier("0.5")
	factors := []PolicyFactor{
		{RuleID: "p1", NameSnapshot: "Double", BindingScope: "group", Revision: 1, Factor: "2", Multiplier: f2},
		{RuleID: "p2", NameSnapshot: "Triple", BindingScope: "group", Revision: 1, Factor: "3", Multiplier: f3},
		{RuleID: "p3", NameSnapshot: "Half", BindingScope: "credential", Revision: 1, Factor: "0.5", Multiplier: f05},
	}
	quote, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, baseMultipliers, factors...)
	if quote.EstimatedCostNanoUSD != 300 {
		t.Fatalf("quote.EstimatedCostNanoUSD = %d, want 300 (x2 x3 x0.5 => 3)", quote.EstimatedCostNanoUSD)
	}
	if receipt == nil || receipt.SchemaVersion != 7 || len(receipt.PolicyFactors) != 3 {
		t.Fatalf("receipt invalid for v7 factors: %#v", receipt)
	}
	if err := ValidateReceipt(*receipt); err != nil {
		t.Fatalf("ValidateReceipt error: %v", err)
	}

	// 2. Zero policy factor => 0 cost
	f0, _ := ParsePriceMultiplier("0")
	zeroFactors := []PolicyFactor{
		{RuleID: "p0", NameSnapshot: "Free", BindingScope: "group", Revision: 1, Factor: "0", Multiplier: f0},
	}
	zeroQuote, zeroReceipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, baseMultipliers, zeroFactors...)
	if zeroQuote.EstimatedCostNanoUSD != 0 || zeroReceipt.TotalNanoUSD != 0 {
		t.Fatalf("zeroQuote = %d, want 0", zeroQuote.EstimatedCostNanoUSD)
	}
	if err := ValidateReceipt(*zeroReceipt); err != nil {
		t.Fatalf("ValidateReceipt zero factor error: %v", err)
	}

	// 3. Product > 1000 without single-factor cap: 800 * 2 = 1600x (base 100 -> 160000)
	f800, _ := ParsePriceMultiplier("800")
	bigFactors := []PolicyFactor{
		{RuleID: "p_big1", NameSnapshot: "Big1", BindingScope: "group", Revision: 1, Factor: "800", Multiplier: f800},
		{RuleID: "p_big2", NameSnapshot: "Big2", BindingScope: "credential", Revision: 1, Factor: "2", Multiplier: f2},
	}
	bigQuote, bigReceipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, baseMultipliers, bigFactors...)
	if bigQuote.EstimatedCostNanoUSD != 160_000 || bigReceipt.TotalNanoUSD != 160_000 {
		t.Fatalf("bigQuote = %d, want 160000 (800x * 2x = 1600x)", bigQuote.EstimatedCostNanoUSD)
	}
	if err := ValidateReceipt(*bigReceipt); err != nil {
		t.Fatalf("ValidateReceipt product > 1000 error: %v", err)
	}

	// 4. One final rounding (half-up at final step, no intermediate float or truncation)
	// Base 1 NanoUSD * Group 1.5 * Factor 1.5 = 2.25 => rounds to 2
	f15, _ := ParsePriceMultiplier("1.5")
	roundingFactors := []PolicyFactor{
		{RuleID: "p_round", NameSnapshot: "Round", BindingScope: "group", Revision: 1, Factor: "1.5", Multiplier: f15},
	}
	roundQuote, roundReceipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10_000}, // 10000 * 100 / 1000000 = 1 NanoUSD
	}, ModeStandard, PriceMultipliers{Group: 1_500_000, AccessKey: DefaultPriceMultiplier}, roundingFactors...)
	if roundQuote.EstimatedCostNanoUSD != 2 || roundReceipt.TotalNanoUSD != 2 {
		t.Fatalf("roundQuote = %d, want 2 (1 * 1.5 * 1.5 = 2.25 => 2)", roundQuote.EstimatedCostNanoUSD)
	}

	// 5. Overflow fail-closed
	overflowTable := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(math.MaxInt64)}})
	overflowQuote, overflowReceipt := overflowTable.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, baseMultipliers, factors...)
	if overflowQuote != unavailableQuote() || overflowReceipt != nil {
		t.Fatalf("overflow quote = %#v, want unavailable", overflowQuote)
	}

	// 6. No policy: produces v6 receipt without policy factors
	noPolicyQuote, noPolicyReceipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, baseMultipliers)
	if noPolicyQuote.EstimatedCostNanoUSD != 100 || noPolicyReceipt.SchemaVersion != 6 || noPolicyReceipt.PolicyFactors != nil {
		t.Fatalf("noPolicyReceipt = %#v, want v6 without policy factors", noPolicyReceipt)
	}

	// 7. Factor x1 preserves base total exactly
	f1, _ := ParsePriceMultiplier("1")
	x1Factors := []PolicyFactor{
		{RuleID: "p_one", NameSnapshot: "Identity", BindingScope: "group", Revision: 1, Factor: "1", Multiplier: f1},
	}
	x1Quote, x1Receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, baseMultipliers, x1Factors...)
	if x1Quote.EstimatedCostNanoUSD != 100 || x1Receipt.TotalNanoUSD != 100 {
		t.Fatalf("x1Quote = %d, want 100", x1Quote.EstimatedCostNanoUSD)
	}
	if err := ValidateReceipt(*x1Receipt); err != nil {
		t.Fatalf("ValidateReceipt x1 error: %v", err)
	}

	// 8. Cancel-to-1 chain (x2 then x0.5 => net 1)
	cancelFactors := []PolicyFactor{
		{RuleID: "p_up", NameSnapshot: "Double", BindingScope: "group", Revision: 1, Factor: "2", Multiplier: f2},
		{RuleID: "p_down", NameSnapshot: "Halve", BindingScope: "credential", Revision: 1, Factor: "0.5", Multiplier: f05},
	}
	cancelQuote, cancelReceipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, baseMultipliers, cancelFactors...)
	if cancelQuote.EstimatedCostNanoUSD != 100 || cancelReceipt.TotalNanoUSD != 100 {
		t.Fatalf("cancelQuote = %d, want 100 (net 1)", cancelQuote.EstimatedCostNanoUSD)
	}
	if err := ValidateReceipt(*cancelReceipt); err != nil {
		t.Fatalf("ValidateReceipt cancel-to-1 error: %v", err)
	}

	// 9. Precision-half chain (exact 0.5 nanodollar rounds up)
	// Base 1 NanoUSD * Group 1.0 * AccessKey 1.0 * Factor 1.5 = 1.5 NanoUSD => rounds half-up to 2
	halfFactors := []PolicyFactor{
		{RuleID: "p_half_round", NameSnapshot: "OneAndHalf", BindingScope: "group", Revision: 1, Factor: "1.5", Multiplier: f15},
	}
	halfQuote, halfReceipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10_000},
	}, ModeStandard, baseMultipliers, halfFactors...)
	if halfQuote.EstimatedCostNanoUSD != 2 || halfReceipt.TotalNanoUSD != 2 {
		t.Fatalf("halfQuote = %d, want 2", halfQuote.EstimatedCostNanoUSD)
	}

	// 10. Cross-binding duplicate rule IDs allowed (group rule "dup" and credential rule "dup")
	crossDupFactors := []PolicyFactor{
		{RuleID: "dup", NameSnapshot: "Group Rule", BindingScope: "group", Revision: 1, Factor: "2", Multiplier: f2},
		{RuleID: "dup", NameSnapshot: "Cred Rule", BindingScope: "credential", Revision: 2, Factor: "3", Multiplier: f3},
	}
	dupQuote, dupReceipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, baseMultipliers, crossDupFactors...)
	if dupQuote.EstimatedCostNanoUSD != 600 || dupReceipt.TotalNanoUSD != 600 {
		t.Fatalf("dupQuote = %d, want 600", dupQuote.EstimatedCostNanoUSD)
	}
	if err := ValidateReceipt(*dupReceipt); err != nil {
		t.Fatalf("ValidateReceipt cross-binding duplicate rule ID error: %v", err)
	}
}

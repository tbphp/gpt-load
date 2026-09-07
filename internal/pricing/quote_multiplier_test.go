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

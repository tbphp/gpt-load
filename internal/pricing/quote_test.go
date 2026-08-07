package pricing

import (
	"math"
	"testing"

	"gpt-load/internal/usage"
)

func TestQuoteMapsUsageStatesBeforeLookup(t *testing.T) {
	var table *Table
	tests := []struct {
		name  string
		state usage.State
		want  Quote
	}{
		{name: "not applicable", state: usage.StateNotApplicable, want: Quote{State: CostStateNotApplicable, Completeness: CompletenessNotApplicable}},
		{name: "missing", state: usage.StateMissing, want: Quote{State: CostStateUnpriced, Completeness: CompletenessUnavailable}},
		{name: "unknown", state: usage.State("unknown"), want: Quote{State: CostStateUnpriced, Completeness: CompletenessUnavailable}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := table.Quote(Identity{}, usage.Result{State: test.state}); got != test.want {
				t.Fatalf("Quote() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestQuoteUsesHighestEligibleTierAtInclusiveBoundary(t *testing.T) {
	identity := Identity{ModelID: "gpt-4.1"}
	table := mustTable(t, Rule{
		Identity: identity,
		Prices:   Prices{Input: fixedPrice(1)},
		ContextTiers: []ContextTier{
			{InputThresholdTokens: 0, Prices: Prices{Input: fixedPrice(2)}},
			{InputThresholdTokens: 1_000_000, Prices: Prices{Input: fixedPrice(3)}},
		},
	})

	quote := table.Quote(identity, usage.Result{
		Tokens: usage.Tokens{UncachedInput: 1_000_000},
		State:  usage.StateComplete,
	})
	want := Quote{State: CostStatePriced, Completeness: CompletenessComplete, EstimatedCostNanoUSD: 3}
	if quote != want {
		t.Fatalf("Quote() = %#v, want highest inclusive tier %#v", quote, want)
	}
}

func TestQuoteTierReplacesBaseSlotsWithoutFallback(t *testing.T) {
	identity := Identity{ModelID: "claude-sonnet"}
	table := mustTable(t, Rule{
		Identity: identity,
		Prices:   Prices{Input: fixedPrice(10), Output: fixedPrice(20)},
		ContextTiers: []ContextTier{{
			InputThresholdTokens: 1,
			Prices:               Prices{Input: fixedPrice(30)},
		}},
	})

	quote := table.Quote(identity, usage.Result{
		Tokens: usage.Tokens{UncachedInput: 1_000_000, Output: 1_000_000},
		State:  usage.StateComplete,
	})
	want := Quote{State: CostStatePriced, Completeness: CompletenessPartial, EstimatedCostNanoUSD: 30}
	if quote != want {
		t.Fatalf("Quote() = %#v, want tier input only with no output fallback %#v", quote, want)
	}
}

func TestQuotePricesCacheWriteFiveMinutesDirectlyAndOneHourAtExactEightFifths(t *testing.T) {
	identity := Identity{ModelID: "claude-sonnet"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{CacheWrite: fixedPrice(5)}})
	quote := table.Quote(identity, usage.Result{
		Tokens: usage.Tokens{CacheWrite5M: 1_000_000, CacheWrite1H: 1_000_000},
		State:  usage.StateComplete,
	})
	want := Quote{State: CostStatePriced, Completeness: CompletenessComplete, EstimatedCostNanoUSD: 13}
	if quote != want {
		t.Fatalf("Quote() = %#v, want direct 5m plus exact 8/5 1h %#v", quote, want)
	}
}

func TestQuoteIncludesUnknownCacheWriteInTierButNeverChargesIt(t *testing.T) {
	identity := Identity{ModelID: "claude-sonnet"}
	table := mustTable(t, Rule{
		Identity: identity,
		Prices:   Prices{Input: fixedPrice(1), CacheWrite: fixedPrice(math.MaxInt64)},
		ContextTiers: []ContextTier{{
			InputThresholdTokens: 1_000_000,
			Prices:               Prices{Input: fixedPrice(2), CacheWrite: fixedPrice(math.MaxInt64)},
		}},
	})
	quote := table.Quote(identity, usage.Result{
		Tokens: usage.Tokens{UncachedInput: 999_999, CacheWriteUnknown: 1},
		State:  usage.StateComplete,
	})
	want := Quote{State: CostStatePriced, Completeness: CompletenessPartial, EstimatedCostNanoUSD: 2}
	if quote != want {
		t.Fatalf("Quote() = %#v, want unknown to select tier but remain unbilled %#v", quote, want)
	}
}

func TestQuotePreservesKnownCostWhenAnotherPositiveComponentIsUnavailable(t *testing.T) {
	identity := Identity{ModelID: "gpt-4.1"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(7)}})
	quote := table.Quote(identity, usage.Result{
		Tokens: usage.Tokens{UncachedInput: 1_000_000, Output: 1},
		State:  usage.StateComplete,
	})
	want := Quote{State: CostStatePriced, Completeness: CompletenessPartial, EstimatedCostNanoUSD: 7}
	if quote != want {
		t.Fatalf("Quote() = %#v, want known input cost retained %#v", quote, want)
	}
}

func TestQuoteCompleteUsageDiagnosticsDoNotMakeKnownCostPartial(t *testing.T) {
	identity := Identity{ModelID: "gpt-4.1"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(9)}})
	tests := []struct {
		name             string
		diagnostic       usage.DiagnosticCode
		wantCompleteness Completeness
	}{
		{name: "unsupported billable detail", diagnostic: usage.DiagnosticUnsupportedBillableDetail, wantCompleteness: CompletenessComplete},
		{name: "negative value", diagnostic: usage.DiagnosticNegativeValue, wantCompleteness: CompletenessComplete},
		{name: "invalid number", diagnostic: usage.DiagnosticInvalidNumber, wantCompleteness: CompletenessComplete},
		{name: "missing required field", diagnostic: usage.DiagnosticMissingRequiredField, wantCompleteness: CompletenessComplete},
		{name: "inconsistent total", diagnostic: usage.DiagnosticInconsistentTotal, wantCompleteness: CompletenessComplete},
		{name: "invalid payload", diagnostic: usage.DiagnosticInvalidPayload, wantCompleteness: CompletenessComplete},
		{name: "invalid event sequence", diagnostic: usage.DiagnosticInvalidEventSequence, wantCompleteness: CompletenessComplete},
		{name: "cache write defaulted 5m", diagnostic: usage.DiagnosticCacheWriteDefaulted5M, wantCompleteness: CompletenessComplete},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var diagnostics usage.Diagnostics
			diagnostics.Add(test.diagnostic)
			quote := table.Quote(identity, usage.Result{
				Tokens:      usage.Tokens{UncachedInput: 1_000_000},
				State:       usage.StateComplete,
				Diagnostics: diagnostics,
			})
			want := Quote{State: CostStatePriced, Completeness: test.wantCompleteness, EstimatedCostNanoUSD: 9}
			if quote != want {
				t.Fatalf("Quote() = %#v, want known cost with completeness %q", quote, test.wantCompleteness)
			}
		})
	}
}

func TestQuoteSeparatesPricedStateFromCompleteness(t *testing.T) {
	identity := Identity{ModelID: "gpt-4.1"}
	tests := []struct {
		name   string
		result usage.Result
		want   Quote
	}{
		{name: "positive all null", result: usage.Result{Tokens: usage.Tokens{Output: 1}, State: usage.StateComplete}, want: Quote{State: CostStateUnpriced, Completeness: CompletenessUnavailable}},
		{name: "zero exact rule", result: usage.Result{State: usage.StateComplete}, want: Quote{State: CostStatePriced, Completeness: CompletenessComplete}},
		{name: "zero partial usage", result: usage.Result{State: usage.StatePartial}, want: Quote{State: CostStatePriced, Completeness: CompletenessPartial}},
	}
	for _, manual := range []bool{false, true} {
		allNull := mustTable(t, Rule{Identity: identity, IsManual: manual})
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				if got := allNull.Quote(identity, test.result); got != test.want {
					t.Fatalf("Quote() = %#v, want %#v", got, test.want)
				}
			})
		}

		if got := allNull.Quote(Identity{ModelID: "missing"}, usage.Result{State: usage.StateComplete}); got != (Quote{State: CostStateUnpriced, Completeness: CompletenessUnavailable}) {
			t.Fatalf("missing exact rule Quote() = %#v, want unpriced unavailable", got)
		}
	}

	explicitZero := mustTable(t, Rule{Identity: identity, Prices: Prices{Output: fixedPrice(0)}})
	want := Quote{State: CostStatePriced, Completeness: CompletenessComplete}
	if got := explicitZero.Quote(identity, usage.Result{Tokens: usage.Tokens{Output: 1}, State: usage.StateComplete}); got != want {
		t.Fatalf("explicit zero Quote() = %#v, want priced complete zero %#v", got, want)
	}
}

func TestQuoteRejectsNegativeTokensAndOverflowFailClosed(t *testing.T) {
	identity := Identity{ModelID: "gpt-4.1"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{
		Input: fixedPrice(math.MaxInt64), Output: fixedPrice(math.MaxInt64),
		CacheRead: fixedPrice(0), CacheWrite: fixedPrice(0),
	}})
	want := Quote{State: CostStateUnpriced, Completeness: CompletenessUnavailable}
	tests := []struct {
		name   string
		tokens usage.Tokens
	}{
		{name: "negative input", tokens: usage.Tokens{UncachedInput: -1}},
		{name: "negative cache read", tokens: usage.Tokens{CacheRead: -1}},
		{name: "negative cache write 5m", tokens: usage.Tokens{CacheWrite5M: -1}},
		{name: "negative cache write 1h", tokens: usage.Tokens{CacheWrite1H: -1}},
		{name: "negative cache write unknown", tokens: usage.Tokens{CacheWriteUnknown: -1}},
		{name: "negative output", tokens: usage.Tokens{Output: -1}},
		{name: "input class overflow", tokens: usage.Tokens{UncachedInput: math.MaxInt64, CacheRead: 1}},
		{name: "component cost overflow", tokens: usage.Tokens{UncachedInput: math.MaxInt64}},
		{name: "summed cost overflow", tokens: usage.Tokens{UncachedInput: 500_000, Output: 500_000}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := table.Quote(identity, usage.Result{Tokens: test.tokens, State: usage.StateComplete}); got != want {
				t.Fatalf("Quote() = %#v, want fail-closed %#v", got, want)
			}
		})
	}
}

func TestQuotePartialUsageKeepsKnownCostAndPartialCompleteness(t *testing.T) {
	identity := Identity{ModelID: "gemini-2.5-pro"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Output: fixedPrice(4)}})
	var diagnostics usage.Diagnostics
	diagnostics.Add(usage.DiagnosticInvalidPayload)
	quote := table.Quote(identity, usage.Result{
		Tokens:      usage.Tokens{Output: 1_000_000},
		State:       usage.StatePartial,
		Diagnostics: diagnostics,
	})
	want := Quote{State: CostStatePriced, Completeness: CompletenessPartial, EstimatedCostNanoUSD: 4}
	if quote != want {
		t.Fatalf("Quote() = %#v, want partial known cost %#v", quote, want)
	}
}

func TestQuoteWithReceiptFreezesExactTieredCalculation(t *testing.T) {
	identity := Identity{ModelID: "claude-sonnet"}
	threshold := int64(1_000)
	table := mustTable(t, Rule{
		Identity: identity,
		Prices: Prices{
			Input: fixedPrice(2), Output: fixedPrice(8),
			CacheRead: fixedPrice(1), CacheWrite: fixedPrice(5),
		},
		ContextTiers: []ContextTier{{
			InputThresholdTokens: threshold,
			Prices: Prices{
				Input: fixedPrice(3), Output: fixedPrice(9),
				CacheRead: fixedPrice(1), CacheWrite: fixedPrice(5),
			},
		}},
	})

	quote, receipt := table.QuoteWithReceipt(identity, usage.Result{
		Tokens: usage.Tokens{
			UncachedInput: 1_000_000,
			CacheRead:     2_000_000,
			CacheWrite5M:  1_000_000,
			CacheWrite1H:  1_000_000,
			Output:        1_000_000,
		},
		State: usage.StateComplete,
	})
	if quote != (Quote{
		State: CostStatePriced, Completeness: CompletenessComplete,
		EstimatedCostNanoUSD: 27,
	}) {
		t.Fatalf("QuoteWithReceipt() quote = %#v", quote)
	}
	if receipt == nil || receipt.SchemaVersion != 2 || receipt.Method != "unit_rate_sum" ||
		receipt.MethodVersion != 1 || receipt.Currency != "USD" ||
		receipt.Rule != (ReceiptRule{ModelID: identity.ModelID}) || receipt.ContextThresholdTokens == nil ||
		*receipt.ContextThresholdTokens != threshold || receipt.TotalNanoUSD != 27 {
		t.Fatalf("QuoteWithReceipt() receipt = %#v", receipt)
	}
	if got, want := len(receipt.LineItems), 5; got != want {
		t.Fatalf("receipt line items = %d, want %d: %#v", got, want, receipt.LineItems)
	}
	if receipt.LineItems[3].Code != "cache_write_1h" ||
		receipt.LineItems[3].Multiplier != (Multiplier{Numerator: 8, Denominator: 5}) ||
		receipt.LineItems[3].AmountNanoUSD == nil || *receipt.LineItems[3].AmountNanoUSD != 8 {
		t.Fatalf("one-hour cache receipt line = %#v", receipt.LineItems[3])
	}
}

func TestQuoteWithReceiptPreservesUnpricedPositiveComponents(t *testing.T) {
	identity := Identity{ModelID: "gpt-4.1"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(7)}})

	quote, receipt := table.QuoteWithReceipt(identity, usage.Result{
		Tokens: usage.Tokens{UncachedInput: 1_000_000, Output: 10},
		State:  usage.StateComplete,
	})
	if quote.Completeness != CompletenessPartial || receipt == nil || len(receipt.LineItems) != 2 {
		t.Fatalf("QuoteWithReceipt() = %#v, %#v", quote, receipt)
	}
	output := receipt.LineItems[1]
	if output.Code != "output" || output.State != ReceiptLineUnpriced ||
		output.RateNanoUSDPerMillion != nil || output.AmountNanoUSD != nil {
		t.Fatalf("unpriced output receipt line = %#v", output)
	}
}

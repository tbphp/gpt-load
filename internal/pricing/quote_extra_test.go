package pricing

import (
	"testing"

	"gpt-load/internal/usage"
)

func TestQuotePricesExtraPassAtItsOwnModelRow(t *testing.T) {
	executor := Identity{ChannelID: "anthropic", ModelID: "executor"}
	advisor := Identity{ChannelID: "anthropic", ModelID: "advisor"}
	table := mustTable(t,
		Rule{Identity: executor, Prices: Prices{Input: fixedPrice(1_000_000_000), Output: fixedPrice(5_000_000_000)}},
		Rule{Identity: advisor, Prices: Prices{Input: fixedPrice(5_000_000_000), Output: fixedPrice(25_000_000_000)}},
	)
	result := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 1000, Output: 100},
		Extra:  &usage.ExtraUsage{Passes: []usage.ModelTokens{{Model: "advisor", Tokens: usage.Tokens{UncachedInput: 300, Output: 400}}}},
	}
	quote, receipt := table.QuoteWithReceipt(executor, result)
	// executor 1000*$1/M + 100*$5/M = 1,500,000 ; advisor 300*$5/M + 400*$25/M = 11,500,000 nano USD
	want := Quote{State: CostStatePriced, Completeness: CompletenessComplete, EstimatedCostNanoUSD: 13_000_000}
	if quote != want {
		t.Fatalf("Quote() = %#v, want %#v", quote, want)
	}
	if err := ValidateReceipt(*receipt); err != nil {
		t.Fatalf("receipt with an extra-pass line must validate: %v", err)
	}
}

func TestQuoteExtraPassWithoutPriceRowIsPartialNotZero(t *testing.T) {
	executor := Identity{ChannelID: "anthropic", ModelID: "executor"}
	table := mustTable(t, Rule{Identity: executor, Prices: Prices{Input: fixedPrice(1_000_000_000), Output: fixedPrice(5_000_000_000)}})
	result := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 1000, Output: 100},
		Extra:  &usage.ExtraUsage{Passes: []usage.ModelTokens{{Model: "advisor", Tokens: usage.Tokens{UncachedInput: 300, Output: 400}}}},
	}
	quote, receipt := table.QuoteWithReceipt(executor, result)
	want := Quote{State: CostStatePriced, Completeness: CompletenessPartial, EstimatedCostNanoUSD: 1_500_000}
	if quote != want {
		t.Fatalf("Quote() = %#v, want %#v", quote, want)
	}
	if err := ValidateReceipt(*receipt); err != nil {
		t.Fatalf("receipt with unpriced extra-pass lines must validate: %v", err)
	}
}

func TestQuoteSumsRepeatedPassesOfOneModelIntoOneReceiptLine(t *testing.T) {
	executor := Identity{ChannelID: "anthropic", ModelID: "executor"}
	advisor := Identity{ChannelID: "anthropic", ModelID: "advisor"}
	table := mustTable(t,
		Rule{Identity: executor, Prices: Prices{Input: fixedPrice(1_000_000_000), Output: fixedPrice(5_000_000_000)}},
		Rule{Identity: advisor, Prices: Prices{Input: fixedPrice(5_000_000_000), Output: fixedPrice(25_000_000_000)}},
	)
	result := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 1000, Output: 100},
		Extra: &usage.ExtraUsage{Passes: []usage.ModelTokens{
			{Model: "advisor", Tokens: usage.Tokens{UncachedInput: 100, Output: 200}},
			{Model: "advisor", Tokens: usage.Tokens{UncachedInput: 200, Output: 200}},
		}},
	}
	quote, receipt := table.QuoteWithReceipt(executor, result)
	// executor 1,500,000 ; advisor (100+200)*$5/M + (200+200)*$25/M = 11,500,000 nano USD
	if quote.EstimatedCostNanoUSD != 13_000_000 {
		t.Fatalf("EstimatedCostNanoUSD = %d, want 13000000", quote.EstimatedCostNanoUSD)
	}
	if err := ValidateReceipt(*receipt); err != nil {
		t.Fatalf("three consults of one advisor must give a valid receipt: %v", err)
	}
	if len(receipt.LineItems) != 4 {
		t.Fatalf("receipt lines = %#v, want input+output for the executor and for the advisor", receipt.LineItems)
	}
	if len(result.Extra.Passes) != 2 || result.Extra.Passes[0].Tokens.UncachedInput != 100 {
		t.Fatalf("quoting must not modify the caller's usage: %#v", result.Extra)
	}
}

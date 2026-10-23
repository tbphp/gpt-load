package pricing

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/usage"
)

var (
	advisorExecutor = Identity{ChannelID: "anthropic", ModelID: "claude-haiku-4-5"}
	advisorModel    = Identity{ChannelID: "anthropic", ModelID: "claude-opus-4-8"}
	advisorDefaults = PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}
)

func advisorResult() usage.Result {
	return usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 1000, Output: 100},
		Advisor: &usage.AdvisorUsage{Models: []usage.ModelTokens{
			{Model: advisorModel.ModelID, Tokens: usage.Tokens{UncachedInput: 300, CacheRead: 1000, Output: 400}},
		}},
	}
}

func TestQuotePricesAdvisorAtItsOwnModel(t *testing.T) {
	table := mustTable(t,
		Rule{Identity: advisorExecutor, Prices: Prices{Input: fixedPrice(1_000_000_000), Output: fixedPrice(5_000_000_000)}},
		Rule{Identity: advisorModel, Prices: Prices{
			Input: fixedPrice(5_000_000_000), CacheRead: fixedPrice(500_000_000), Output: fixedPrice(25_000_000_000),
		}},
	)
	quote, receipt := table.QuoteForModeWithMultipliers(advisorExecutor, advisorResult(), ModeStandard, advisorDefaults)
	// 执行模型 1,500,000；advisor 1,500,000 + 500,000 + 10,000,000 nano USD。
	want := Quote{State: CostStatePriced, Completeness: CompletenessComplete, EstimatedCostNanoUSD: 13_500_000}
	if quote != want || receipt == nil || receipt.SchemaVersion != 7 {
		t.Fatalf("quote = %+v, receipt = %+v, want %+v with a v7 receipt", quote, receipt, want)
	}
	advisorLines := 0
	for _, line := range receipt.LineItems {
		if line.ModelID == "" {
			continue
		}
		advisorLines++
		if line.ModelID != advisorModel.ModelID || line.State != ReceiptLinePriced {
			t.Fatalf("advisor line = %+v", line)
		}
	}
	if advisorLines != 3 {
		t.Fatalf("receipt lines = %+v, want three advisor lines", receipt.LineItems)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Receipt
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if err := ValidateReceipt(decoded); err != nil {
		t.Fatalf("ValidateReceipt() error = %v", err)
	}
}

func TestQuoteAdvisorWithoutPriceRuleIsPartial(t *testing.T) {
	table := mustTable(t,
		Rule{Identity: advisorExecutor, Prices: Prices{Input: fixedPrice(1_000_000_000), Output: fixedPrice(5_000_000_000)}},
	)
	quote, receipt := table.QuoteForModeWithMultipliers(advisorExecutor, advisorResult(), ModeStandard, advisorDefaults)
	want := Quote{State: CostStatePriced, Completeness: CompletenessPartial, EstimatedCostNanoUSD: 1_500_000}
	if quote != want || receipt == nil {
		t.Fatalf("quote = %+v, want %+v", quote, want)
	}
	for _, line := range receipt.LineItems {
		if line.ModelID != "" && line.State != ReceiptLineUnpriced {
			t.Fatalf("advisor line without a price rule = %+v", line)
		}
	}
	if err := ValidateReceipt(*receipt); err != nil {
		t.Fatalf("ValidateReceipt() error = %v", err)
	}
}

func TestValidateReceiptAdvisorLineContract(t *testing.T) {
	table := mustTable(t,
		Rule{Identity: advisorExecutor, Prices: Prices{Input: fixedPrice(1_000_000_000), Output: fixedPrice(5_000_000_000)}},
		Rule{Identity: advisorModel, Prices: Prices{Input: fixedPrice(5_000_000_000), Output: fixedPrice(25_000_000_000)}},
	)
	result := advisorResult()
	result.Advisor.Models[0].Tokens.CacheRead = 0
	_, valid := table.QuoteForModeWithMultipliers(advisorExecutor, result, ModeStandard, advisorDefaults)
	if valid == nil || ValidateReceipt(*valid) != nil {
		t.Fatalf("receipt = %+v, want a valid advisor receipt", valid)
	}
	clone := func() Receipt {
		receipt := *valid
		receipt.LineItems = append([]ReceiptLine(nil), valid.LineItems...)
		return receipt
	}
	for _, test := range []struct {
		name   string
		mutate func(*Receipt)
	}{
		{name: "advisor lines need v7", mutate: func(receipt *Receipt) { receipt.SchemaVersion = 6 }},
		{name: "advisor line needs a model", mutate: func(receipt *Receipt) {
			for index := range receipt.LineItems {
				if receipt.LineItems[index].Code == "advisor_output" {
					receipt.LineItems[index].ModelID = ""
				}
			}
		}},
		{name: "request line has no model", mutate: func(receipt *Receipt) {
			receipt.LineItems[0].ModelID = advisorModel.ModelID
		}},
		{name: "advisor line is unique per model", mutate: func(receipt *Receipt) {
			receipt.LineItems = append(receipt.LineItems, receipt.LineItems[len(receipt.LineItems)-1])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			receipt := clone()
			test.mutate(&receipt)
			if err := ValidateReceipt(receipt); err == nil {
				t.Fatalf("ValidateReceipt(%+v) succeeded", receipt.LineItems)
			}
		})
	}
}

package dialect

import (
	"testing"

	"gpt-load/internal/pricing"
	"gpt-load/internal/usage"
)

// 顶层只含执行模型轮次；advisor 轮次按自己的模型单独列出，compaction 需要并入。
const anthropicAdvisorUsage = `{"input_tokens":1760,"cache_read_input_tokens":412,"output_tokens":531,"iterations":[` +
	`{"type":"message","input_tokens":412,"output_tokens":89},` +
	`{"type":"advisor_message","model":"claude-opus-4-8","input_tokens":823,"cache_read_input_tokens":100,"output_tokens":1612},` +
	`{"type":"compaction","input_tokens":50,"output_tokens":5},` +
	`{"type":"fallback_message","input_tokens":7,"output_tokens":7},` +
	`{"type":"advisor_message","model":"claude-opus-4-8","input_tokens":900,"output_tokens":400},` +
	`{"type":"advisor_message","model":"claude-sonnet-5-5","input_tokens":30,"output_tokens":40},` +
	`{"type":"message","input_tokens":1348,"cache_read_input_tokens":412,"output_tokens":442}]}`

func requireAnthropicAdvisorUsage(t *testing.T, result usage.Result) {
	t.Helper()
	wantTokens := usage.Tokens{UncachedInput: 1810, CacheRead: 412, Output: 536}
	if result.State != usage.StateComplete || result.Tokens != wantTokens {
		t.Fatalf("usage = %+v, want complete %+v", result, wantTokens)
	}
	requireUsageDiagnostics(t, result.Diagnostics)
	want := []usage.ModelTokens{
		{Model: "claude-opus-4-8", Tokens: usage.Tokens{UncachedInput: 823, CacheRead: 100, Output: 1612}},
		{Model: "claude-opus-4-8", Tokens: usage.Tokens{UncachedInput: 900, Output: 400}},
		{Model: "claude-sonnet-5-5", Tokens: usage.Tokens{UncachedInput: 30, Output: 40}},
	}
	if result.Advisor == nil || len(result.Advisor.Calls) != len(want) {
		t.Fatalf("advisor usage = %+v, want %+v", result.Advisor, want)
	}
	for index, model := range want {
		if result.Advisor.Calls[index] != model {
			t.Fatalf("advisor usage[%d] = %+v, want %+v", index, result.Advisor.Calls[index], model)
		}
	}
}

func TestUsageAnthropicNonStreamAdvisorIterations(t *testing.T) {
	result, err := NewAnthropic().ExtractUsage([]byte(`{"type":"message","usage":` + anthropicAdvisorUsage + `}`))
	if err != nil {
		t.Fatal(err)
	}
	requireAnthropicAdvisorUsage(t, result)
}

func TestUsageAnthropicStreamAdvisorIterations(t *testing.T) {
	stream := NewAnthropic().NewUsageStreamExtractor()
	for _, event := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":412,"cache_read_input_tokens":0,"output_tokens":1}}}`,
		`{"type":"message_delta","usage":` + anthropicAdvisorUsage + `}`,
		`{"type":"message_stop"}`,
	} {
		if err := stream.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	result, _ := stream.Finalize()
	requireAnthropicAdvisorUsage(t, result)
}

func TestUsageAnthropicUnpricedIterationsAreFlagged(t *testing.T) {
	for _, iteration := range []string{
		`{"type":"future_pass","input_tokens":9,"output_tokens":9}`,
		`{"type":"advisor_message","input_tokens":9,"output_tokens":9}`,
		`{"input_tokens":9,"output_tokens":9}`,
	} {
		t.Run(iteration, func(t *testing.T) {
			usageObject := `{"input_tokens":10,"output_tokens":5,"iterations":[` + iteration + `]}`
			result, err := NewAnthropic().ExtractUsage([]byte(`{"usage":` + usageObject + `}`))
			if err != nil {
				t.Fatal(err)
			}
			if result.Tokens != (usage.Tokens{UncachedInput: 10, Output: 5}) || result.Advisor != nil {
				t.Fatalf("non-stream usage = %+v", result)
			}
			requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticUnsupportedBillableDetail)

			stream := NewAnthropic().NewUsageStreamExtractor()
			for _, event := range []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
				`{"type":"message_delta","usage":` + usageObject + `}`,
				`{"type":"message_stop"}`,
			} {
				if err := stream.Observe([]byte(event)); err != nil {
					t.Fatal(err)
				}
			}
			streamed, _ := stream.Finalize()
			if streamed.Tokens != (usage.Tokens{UncachedInput: 10, Output: 5}) || streamed.Advisor != nil {
				t.Fatalf("stream usage = %+v", streamed)
			}
			requireUsageDiagnostics(t, streamed.Diagnostics, usage.DiagnosticUnsupportedBillableDetail)
		})
	}
}

func TestUsageAnthropicAdvisorCallsArePricedOneByOne(t *testing.T) {
	// 每次 advisor 调用单独按提示长度选价格档：两次各 60K 不能合并成 120K 落入高价档。
	advisorCall := `{"type":"advisor_message","model":"claude-haiku-5-5","input_tokens":60000,"output_tokens":100}`
	body := `{"usage":{"input_tokens":1000,"output_tokens":100,"iterations":[` +
		`{"type":"message","input_tokens":400,"output_tokens":40},` + advisorCall + `,` +
		`{"type":"message","input_tokens":600,"output_tokens":60},` + advisorCall + `]}}`
	result, err := NewAnthropic().ExtractUsage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	price := func(value pricing.NanoUSD) pricing.Price { return pricing.Price{NanoUSDPerMillion: value, Set: true} }
	executor := pricing.Identity{ChannelID: "anthropic", ModelID: "claude-haiku-4-5"}
	table, err := pricing.NewTable([]pricing.Rule{
		{Identity: executor, Prices: pricing.Prices{Input: price(1_000_000_000), Output: price(5_000_000_000)}},
		{Identity: pricing.Identity{ChannelID: "anthropic", ModelID: "claude-haiku-5-5"},
			Prices: pricing.Prices{Input: price(100_000_000), Output: price(500_000_000)},
			ContextTiers: []pricing.ContextTier{{InputThresholdTokens: 100_000,
				Prices: pricing.Prices{Input: price(500_000_000), Output: price(2_500_000_000)}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	quote, receipt := table.QuoteForModeWithMultipliers(executor, result, pricing.ModeStandard, pricing.PriceMultipliers{
		Group: pricing.DefaultPriceMultiplier, AccessKey: pricing.DefaultPriceMultiplier,
	})
	// 执行模型 1,500,000；advisor 120,000 × $0.10/M + 200 × $0.50/M = 12,100,000 nano USD。
	if quote.EstimatedCostNanoUSD != 13_600_000 || receipt == nil {
		t.Fatalf("quote = %+v, want 13600000 nano USD", quote)
	}
	if err := pricing.ValidateReceipt(*receipt); err != nil {
		t.Fatalf("ValidateReceipt() error = %v", err)
	}
}

func TestUsageAnthropicStreamAdvisorSnapshotIsNotAdditive(t *testing.T) {
	const delta = `{"output_tokens":20,"iterations":[{"type":"advisor_message","model":"claude-opus-4-8","input_tokens":300,"output_tokens":400}]}`
	stream := NewAnthropic().NewUsageStreamExtractor()
	for _, event := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":100,"output_tokens":1}}}`,
		`{"type":"message_delta","usage":` + delta + `}`,
		`{"type":"message_delta","usage":` + delta + `}`,
		`{"type":"message_delta","usage":{"output_tokens":30}}`,
		`{"type":"message_stop"}`,
	} {
		if err := stream.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	result, _ := stream.Finalize()
	want := usage.ModelTokens{Model: "claude-opus-4-8", Tokens: usage.Tokens{UncachedInput: 300, Output: 400}}
	if result.Tokens != (usage.Tokens{UncachedInput: 100, Output: 30}) || result.Advisor == nil ||
		len(result.Advisor.Calls) != 1 || result.Advisor.Calls[0] != want {
		t.Fatalf("usage = %+v, advisor = %+v", result, result.Advisor)
	}
}

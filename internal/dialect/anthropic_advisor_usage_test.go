package dialect

import (
	"testing"

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
		{Model: "claude-opus-4-8", Tokens: usage.Tokens{UncachedInput: 1723, CacheRead: 100, Output: 2012}},
		{Model: "claude-sonnet-5-5", Tokens: usage.Tokens{UncachedInput: 30, Output: 40}},
	}
	if result.Advisor == nil || len(result.Advisor.Models) != len(want) {
		t.Fatalf("advisor usage = %+v, want %+v", result.Advisor, want)
	}
	for index, model := range want {
		if result.Advisor.Models[index] != model {
			t.Fatalf("advisor usage[%d] = %+v, want %+v", index, result.Advisor.Models[index], model)
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
		len(result.Advisor.Models) != 1 || result.Advisor.Models[0] != want {
		t.Fatalf("usage = %+v, advisor = %+v", result, result.Advisor)
	}
}

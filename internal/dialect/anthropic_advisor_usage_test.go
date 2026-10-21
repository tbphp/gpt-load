package dialect

import (
	"testing"

	"gpt-load/internal/usage"
)

const advisorIterations = `"iterations":[
	{"type":"compaction","input_tokens":1000,"output_tokens":100},
	{"type":"message","input_tokens":200,"output_tokens":20},
	{"type":"advisor_message","model":"claude-opus-4-8","input_tokens":300,"output_tokens":400},
	{"type":"message","input_tokens":250,"output_tokens":30},
	{"type":"advisor_message","model":"claude-opus-4-8","input_tokens":350,"output_tokens":500}]`

func checkAdvisorUsage(t *testing.T, result usage.Result) {
	t.Helper()
	// top-level usage covers the answer rounds (450/50); the compaction pass folds into the request model's tokens
	want := usage.Tokens{UncachedInput: 450 + 1000, Output: 50 + 100}
	if result.State != usage.StateComplete || result.Tokens != want {
		t.Fatalf("result = %#v, want complete with %#v", result, want)
	}
	if result.Extra == nil || len(result.Extra.Passes) != 2 {
		t.Fatalf("Extra = %#v, want two advisor passes", result.Extra)
	}
	for i, tokens := range []usage.Tokens{{UncachedInput: 300, Output: 400}, {UncachedInput: 350, Output: 500}} {
		pass := result.Extra.Passes[i]
		if pass.Model != "claude-opus-4-8" || pass.Tokens != tokens {
			t.Fatalf("pass %d = %#v, want claude-opus-4-8 %#v", i, pass, tokens)
		}
	}
	if result.Diagnostics.Has(usage.DiagnosticUnsupportedBillableDetail) {
		t.Fatalf("known iteration types must not raise unsupported_billable_detail: %#v", result.Diagnostics)
	}
}

func TestAnthropicNonStreamFoldsCompactionAndPricesAdvisorPassesSeparately(t *testing.T) {
	body := `{"usage":{"input_tokens":450,"output_tokens":50,` + advisorIterations + `}}`
	result, err := NewAnthropic().ExtractUsage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	checkAdvisorUsage(t, result)
}

func TestAnthropicStreamFoldsCompactionAndPricesAdvisorPassesSeparately(t *testing.T) {
	stream := NewAnthropic().NewUsageStreamExtractor()
	for _, event := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":200,"output_tokens":1}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":450,"output_tokens":50,` + advisorIterations + `}}`,
		`{"type":"message_stop"}`,
	} {
		if err := stream.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	result, ok := stream.Finalize()
	if !ok {
		t.Fatal("Finalize() = false")
	}
	checkAdvisorUsage(t, result)
}

func TestAnthropicUnknownIterationTypeIsFlaggedNotSilentlyDropped(t *testing.T) {
	body := `{"usage":{"input_tokens":10,"output_tokens":5,"iterations":[{"type":"mystery_pass","input_tokens":9,"output_tokens":9}]}}`
	result, err := NewAnthropic().ExtractUsage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Diagnostics.Has(usage.DiagnosticUnsupportedBillableDetail) {
		t.Fatalf("unknown iteration type must raise unsupported_billable_detail: %#v", result.Diagnostics)
	}
	if result.Extra != nil {
		t.Fatalf("Extra = %#v, want none", result.Extra)
	}
}

func TestGeminiToolUsePromptTokensAreBilledInput(t *testing.T) {
	body := `{"usageMetadata":{"promptTokenCount":18,"candidatesTokenCount":220,"thoughtsTokenCount":120,"toolUsePromptTokenCount":10309,"totalTokenCount":10667}}`
	result, err := NewGemini().ExtractUsage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := usage.Tokens{UncachedInput: 18 + 10309, Output: 340}
	if result.Tokens != want || result.Diagnostics.Has(usage.DiagnosticInconsistentTotal) {
		t.Fatalf("result = %#v, want %#v and a consistent total", result, want)
	}
}

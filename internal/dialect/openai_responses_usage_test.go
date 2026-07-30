package dialect

import (
	"net/http"
	"testing"

	"gpt-load/internal/usage"
)

func TestOpenAIResponsesUsageNonStreaming(t *testing.T) {
	t.Parallel()

	extractor, ok := any(NewOpenAIResponses(http.DefaultClient)).(UsageExtractor)
	if !ok {
		t.Fatal("OpenAI Responses does not expose UsageExtractor capability")
	}
	tests := []struct {
		name        string
		body        string
		want        usage.Tokens
		state       usage.State
		diagnostics []usage.DiagnosticCode
		wantDelta   *int64
	}{
		{
			name: "complete",
			body: `{
				"id":"resp_123",
				"usage":{
					"input_tokens":100,
					"input_tokens_details":{"cached_tokens":20},
					"output_tokens":30,
					"total_tokens":130
				}
			}`,
			want:  usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			state: usage.StateComplete,
		},
		{
			name:  "missing usage",
			body:  `{"id":"resp_123"}`,
			state: usage.StateMissing,
		},
		{
			name:  "cached defaults to zero",
			body:  `{"usage":{"input_tokens":100,"output_tokens":30,"total_tokens":130}}`,
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
			state: usage.StateComplete,
		},
		{
			name:  "cached exceeds input",
			body:  `{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":120},"output_tokens":30}}`,
			want:  usage.Tokens{CacheRead: 120, Output: 30},
			state: usage.StateComplete,
			diagnostics: []usage.DiagnosticCode{
				usage.DiagnosticNegativeValue,
			},
		},
		{
			name:  "invalid required number",
			body:  `{"usage":{"input_tokens":1.5,"output_tokens":30}}`,
			want:  usage.Tokens{Output: 30},
			state: usage.StateComplete,
			diagnostics: []usage.DiagnosticCode{
				usage.DiagnosticInvalidNumber,
			},
		},
		{
			name:  "total mismatch",
			body:  `{"usage":{"input_tokens":100,"output_tokens":30,"total_tokens":140}}`,
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
			state: usage.StateComplete,
			diagnostics: []usage.DiagnosticCode{
				usage.DiagnosticInconsistentTotal,
			},
			wantDelta: int64Pointer(10),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := extractor.ExtractUsage([]byte(test.body))
			if err != nil {
				t.Fatalf("ExtractUsage() error = %v", err)
			}
			if result.State != test.state || result.Tokens != test.want {
				t.Fatalf(
					"ExtractUsage() = %#v, want state=%q tokens=%#v",
					result,
					test.state,
					test.want,
				)
			}
			requireUsageDiagnostics(t, result.Diagnostics, test.diagnostics...)
			if test.wantDelta != nil {
				delta, present := result.Diagnostics.TotalDelta()
				if !present || delta != *test.wantDelta {
					t.Fatalf(
						"TotalDelta() = %d, %t, want %d, true",
						delta,
						present,
						*test.wantDelta,
					)
				}
			}
		})
	}
}

func TestOpenAIResponsesUsageStreamingFinality(t *testing.T) {
	t.Parallel()

	extractor := NewOpenAIResponses(http.DefaultClient)
	tests := []struct {
		name  string
		steps []string
		want  usage.Tokens
		state usage.State
	}{
		{
			name: "completed terminal usage",
			steps: []string{
				`{"type":"response.output_text.delta","delta":"hello"}`,
				`{"type":"response.completed","response":{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":20},"output_tokens":30,"total_tokens":130}}}`,
			},
			want:  usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			state: usage.StateComplete,
		},
		{
			name: "incomplete terminal usage",
			steps: []string{
				`{"type":"response.incomplete","response":{"usage":{"input_tokens":50,"output_tokens":5,"total_tokens":55}}}`,
			},
			want:  usage.Tokens{UncachedInput: 50, Output: 5},
			state: usage.StateComplete,
		},
		{
			name: "failed terminal usage",
			steps: []string{
				`{"type":"response.failed","response":{"error":{"code":"server_error"},"usage":{"input_tokens":25,"output_tokens":2,"total_tokens":27}}}`,
			},
			want:  usage.Tokens{UncachedInput: 25, Output: 2},
			state: usage.StateComplete,
		},
		{
			name: "terminal without usage discards earlier partial",
			steps: []string{
				`{"type":"response.output_item.added","response":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
				`{"type":"response.completed","response":{}}`,
			},
			state: usage.StateMissing,
		},
		{
			name: "usage before terminal is partial",
			steps: []string{
				`{"type":"response.output_item.added","response":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
			},
			want:  usage.Tokens{UncachedInput: 10, Output: 1},
			state: usage.StatePartial,
		},
		{
			name: "no usage is missing",
			steps: []string{
				`{"type":"response.output_text.delta","delta":"hello"}`,
			},
			state: usage.StateMissing,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stream := extractor.NewUsageStreamExtractor()
			for _, step := range test.steps {
				if err := stream.Observe([]byte(step)); err != nil {
					t.Fatalf("Observe() error = %v", err)
				}
			}
			result, finalized := stream.Finalize()
			if !finalized || result.State != test.state ||
				result.Tokens != test.want {
				t.Fatalf(
					"Finalize() = %#v, %t, want state=%q tokens=%#v",
					result,
					finalized,
					test.state,
					test.want,
				)
			}
		})
	}
}

func TestOpenAIResponsesUsageUsesExplicitSSEEventForFinality(t *testing.T) {
	t.Parallel()

	stream := NewOpenAIResponses(http.DefaultClient).NewUsageStreamExtractor()
	observer, ok := stream.(UsageStreamEventObserver)
	if !ok {
		t.Fatal("OpenAI Responses usage stream does not observe SSE event names")
	}
	err := observer.ObserveStreamEvent(StreamEvent{
		Name: "response.completed",
		Payload: []byte(
			`{"response":{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":20},"output_tokens":30,"total_tokens":130}}}`,
		),
	})
	if err != nil {
		t.Fatalf("ObserveStreamEvent() error = %v", err)
	}
	result, finalized := stream.Finalize()
	want := usage.Result{
		State: usage.StateComplete,
		Tokens: usage.Tokens{
			UncachedInput: 80,
			CacheRead:     20,
			Output:        30,
		},
	}
	if !finalized || result != want {
		t.Fatalf("Finalize() = %#v, %t, want %#v, true", result, finalized, want)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

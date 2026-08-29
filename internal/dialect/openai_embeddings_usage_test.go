package dialect

import (
	"strings"
	"testing"

	"gpt-load/internal/usage"
)

func TestOpenAIEmbeddingsUsageNonStreaming(t *testing.T) {
	t.Parallel()

	extractor, ok := any(NewOpenAIEmbeddings()).(UsageExtractor)
	if !ok {
		t.Fatal("OpenAI Embeddings does not expose UsageExtractor capability")
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
			name:  "complete input-only usage",
			body:  `{"object":"list","usage":{"prompt_tokens":7,"total_tokens":7}}`,
			want:  usage.Tokens{UncachedInput: 7},
			state: usage.StateComplete,
		},
		{
			name:  "missing usage",
			body:  `{"object":"list"}`,
			state: usage.StateMissing,
		},
		{
			name:  "null usage",
			body:  `{"object":"list","usage":null}`,
			state: usage.StateMissing,
		},
		{
			name:        "missing prompt tokens",
			body:        `{"usage":{"total_tokens":0}}`,
			state:       usage.StateMissing,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:        "missing total tokens",
			body:        `{"usage":{"prompt_tokens":7}}`,
			want:        usage.Tokens{UncachedInput: 7},
			state:       usage.StateComplete,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:        "negative prompt tokens",
			body:        `{"usage":{"prompt_tokens":-1,"total_tokens":0}}`,
			state:       usage.StateMissing,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
		{
			name:        "fractional prompt tokens",
			body:        `{"usage":{"prompt_tokens":1.5,"total_tokens":0}}`,
			state:       usage.StateMissing,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "overflowing prompt tokens",
			body:        `{"usage":{"prompt_tokens":9223372036854775808,"total_tokens":9223372036854775808}}`,
			state:       usage.StateMissing,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "inconsistent total",
			body:        `{"usage":{"prompt_tokens":7,"total_tokens":9}}`,
			want:        usage.Tokens{UncachedInput: 7},
			state:       usage.StateComplete,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInconsistentTotal},
			wantDelta:   int64Pointer(2),
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
					t.Fatalf("TotalDelta() = %d, %t, want %d, true", delta, present, *test.wantDelta)
				}
			}
		})
	}
}

func TestOpenAIEmbeddingsUsageRejectsMalformedBodies(t *testing.T) {
	t.Parallel()

	extractor := NewOpenAIEmbeddings()
	for _, body := range [][]byte{[]byte(`[]`), []byte(`{"usage":{}} {}`)} {
		if _, err := extractor.ExtractUsage(body); err == nil {
			t.Fatalf("ExtractUsage(%q) succeeded", body)
		}
	}
}

func TestOpenAIEmbeddingsUsageEnvelopeSkipsVectorData(t *testing.T) {
	t.Parallel()

	vector := strings.Repeat("A", 1<<20)
	usageJSON := `{"prompt_tokens":7,"total_tokens":7}`
	body := []byte(`{"object":"list","data":[{"embedding":"` + vector + `"}],"usage":` + usageJSON + `}`)
	envelope, err := decodeOpenAIEmbeddingsUsageEnvelope(body)
	if err != nil || string(envelope.Usage) != usageJSON {
		t.Fatalf("decodeOpenAIEmbeddingsUsageEnvelope() = %#v, %v", envelope, err)
	}
	result, err := NewOpenAIEmbeddings().ExtractUsage(body)
	if err != nil || result.State != usage.StateComplete ||
		result.Tokens != (usage.Tokens{UncachedInput: 7}) {
		t.Fatalf("ExtractUsage() = %#v, %v", result, err)
	}
}

func TestOpenAIEmbeddingsUsageStreamIsNotApplicable(t *testing.T) {
	t.Parallel()

	stream := NewOpenAIEmbeddings().NewUsageStreamExtractor()
	if err := stream.Observe([]byte(`{"usage":{"prompt_tokens":1,"total_tokens":1}}`)); err == nil {
		t.Fatal("Observe() error = nil for unsupported Embeddings stream")
	}
	result, ok := stream.Finalize()
	if !ok || result != (usage.Result{State: usage.StateNotApplicable}) {
		t.Fatalf("Finalize() = %#v, %t", result, ok)
	}
	if second, ok := stream.Finalize(); ok || second != (usage.Result{}) {
		t.Fatalf("second Finalize() = %#v, %t", second, ok)
	}
}

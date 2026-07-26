package dialect

import (
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/usage"
)

func TestUsageGeminiCanonicalFixtures(t *testing.T) {
	provider := Dialect(NewGemini(http.DefaultClient))
	extractor, ok := provider.(UsageExtractor)
	if !ok {
		t.Fatal("Gemini does not implement UsageExtractor")
	}

	want := usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}
	result, err := extractor.ExtractUsage(readUsageFixture(t, "gemini", "nonstream.json"))
	if err != nil {
		t.Fatal(err)
	}
	if result.State != usage.StateComplete || result.Tokens != want {
		t.Fatalf("non-stream result = %#v, want complete with %#v", result, want)
	}

	stream := extractor.NewUsageStreamExtractor()
	observeUsageJSONL(t, stream, readUsageFixture(t, "gemini", "stream.jsonl"))
	streamResult, finalized := stream.Finalize()
	if !finalized || streamResult.State != usage.StateComplete || streamResult.Tokens != want {
		t.Fatalf("stream result = %#v, %t, want complete with %#v", streamResult, finalized, want)
	}
	if _, finalized := stream.Finalize(); finalized {
		t.Fatal("second Finalize() succeeded")
	}
}

func TestUsageGeminiNonStreamOptionalFieldsAndArithmetic(t *testing.T) {
	extractor := NewGemini(http.DefaultClient)
	tests := []struct {
		name        string
		body        string
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
		delta       *int64
	}{
		{
			name: "cached and thoughts omitted do not create cache presence",
			body: `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":25,"totalTokenCount":125}}`,
			want: usage.Tokens{UncachedInput: 100, Output: 25},
		},
		{
			name: "candidates and thoughts are added",
			body: `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"candidatesTokenCount":25,"thoughtsTokenCount":5,"totalTokenCount":130}}`,
			want: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
		},
		{
			name:        "candidate and thought overflow is safe",
			body:        `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":9223372036854775807,"thoughtsTokenCount":1}}`,
			want:        usage.Tokens{UncachedInput: 100},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "cached greater than prompt is clamped",
			body:        `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":120,"candidatesTokenCount":25,"totalTokenCount":145}}`,
			want:        usage.Tokens{CacheRead: 120, Output: 25},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
		{
			name: "total match",
			body: `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"candidatesTokenCount":25,"thoughtsTokenCount":5,"totalTokenCount":130}}`,
			want: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
		},
		{
			name:        "total mismatch has signed delta",
			body:        `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"candidatesTokenCount":25,"thoughtsTokenCount":5,"totalTokenCount":140}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInconsistentTotal},
			delta:       usageInt64(10),
		},
		{
			name:        "unmapped tool use remains visible through total delta",
			body:        `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"candidatesTokenCount":25,"thoughtsTokenCount":5,"toolUsePromptTokenCount":7,"totalTokenCount":137}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInconsistentTotal},
			delta:       usageInt64(7),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractor.ExtractUsage([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.State != usage.StateComplete || result.Tokens != tt.want {
				t.Fatalf("result = %#v, want complete with %#v", result, tt.want)
			}
			requireUsageDiagnostics(t, result.Diagnostics, tt.diagnostics...)
			if tt.delta == nil {
				if _, ok := result.Diagnostics.TotalDelta(); ok {
					t.Fatalf("TotalDelta() unexpectedly present: %#v", result.Diagnostics)
				}
			} else if delta, ok := result.Diagnostics.TotalDelta(); !ok || delta != *tt.delta {
				t.Fatalf("TotalDelta() = %d, %t, want %d, true", delta, ok, *tt.delta)
			}
		})
	}
}

func TestUsageGeminiRequiredFieldsAndStrictNumbers(t *testing.T) {
	extractor := NewGemini(http.DefaultClient)
	tests := []struct {
		name        string
		body        string
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
	}{
		{
			name:        "prompt missing preserves candidates",
			body:        `{"usageMetadata":{"candidatesTokenCount":25}}`,
			want:        usage.Tokens{Output: 25},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:        "prompt null preserves candidates",
			body:        `{"usageMetadata":{"promptTokenCount":null,"candidatesTokenCount":25}}`,
			want:        usage.Tokens{Output: 25},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:        "negative prompt is diagnosed",
			body:        `{"usageMetadata":{"promptTokenCount":-1,"candidatesTokenCount":25}}`,
			want:        usage.Tokens{Output: 25},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
		{
			name:        "fractional prompt is rejected",
			body:        `{"usageMetadata":{"promptTokenCount":1.5,"candidatesTokenCount":25}}`,
			want:        usage.Tokens{Output: 25},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "exponent prompt is rejected",
			body:        `{"usageMetadata":{"promptTokenCount":1e2,"candidatesTokenCount":25}}`,
			want:        usage.Tokens{Output: 25},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "string prompt is rejected",
			body:        `{"usageMetadata":{"promptTokenCount":"100","candidatesTokenCount":25}}`,
			want:        usage.Tokens{Output: 25},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "out of range prompt is rejected",
			body:        `{"usageMetadata":{"promptTokenCount":9223372036854775808,"candidatesTokenCount":25}}`,
			want:        usage.Tokens{Output: 25},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name: "all explicit zeroes remain complete",
			body: `{"usageMetadata":{"promptTokenCount":0,"cachedContentTokenCount":0,"candidatesTokenCount":0,"thoughtsTokenCount":0,"totalTokenCount":0}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractor.ExtractUsage([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.State != usage.StateComplete || result.Tokens != tt.want {
				t.Fatalf("result = %#v, want complete with %#v", result, tt.want)
			}
			requireUsageDiagnostics(t, result.Diagnostics, tt.diagnostics...)
		})
	}
}

func TestUsageGeminiStreamSnapshotsFinalityAndMalformedPayload(t *testing.T) {
	extractor := NewGemini(http.DefaultClient)
	tests := []struct {
		name        string
		steps       []string
		state       usage.State
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
	}{
		{
			name: "latest snapshot replaces disappeared fields and diagnostics",
			steps: []string{
				`{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":120,"candidatesTokenCount":10}}`,
				`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":30}}`,
			},
			state: usage.StateComplete,
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
		},
		{
			name:  "nonfinal usage remains partial",
			steps: []string{`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":30}}`},
			state: usage.StatePartial,
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
		},
		{
			name:  "no usage is missing",
			steps: []string{`{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`},
			state: usage.StateMissing,
		},
		{
			name:  "prompt feedback block is final",
			steps: []string{`{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":100}}`},
			state: usage.StateComplete,
			want:  usage.Tokens{UncachedInput: 100},
		},
		{
			name: "invalid metadata preserves confirmed snapshot",
			steps: []string{
				`{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"candidatesTokenCount":10}}`,
				`{"usageMetadata":[]}`,
			},
			state:       usage.StatePartial,
			want:        usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 10},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name: "invalid metadata diagnostic survives a later valid snapshot",
			steps: []string{
				`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10}}`,
				`{"usageMetadata":[]}`,
				`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":30}}`,
			},
			state:       usage.StateComplete,
			want:        usage.Tokens{UncachedInput: 100, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := extractor.NewUsageStreamExtractor()
			for _, step := range tt.steps {
				if err := stream.Observe([]byte(step)); err != nil {
					t.Fatal(err)
				}
			}
			result, finalized := stream.Finalize()
			if !finalized || result.State != tt.state || result.Tokens != tt.want {
				t.Fatalf("Finalize() = %#v, %t, want state %q tokens %#v", result, finalized, tt.state, tt.want)
			}
			requireUsageDiagnostics(t, result.Diagnostics, tt.diagnostics...)
		})
	}

	stream := extractor.NewUsageStreamExtractor()
	if err := stream.Observe([]byte(`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10}}`)); err != nil {
		t.Fatal(err)
	}
	canary := []byte(`{"usage-secret-CANARY":"do-not-leak",`)
	if err := stream.Observe(canary); err == nil {
		t.Fatal("Observe(malformed) succeeded")
	} else if strings.Contains(err.Error(), "usage-secret-CANARY") || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("malformed error leaked payload: %q", err)
	}
	if err := stream.Observe([]byte(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":30}}`)); err != nil {
		t.Fatal(err)
	}
	result, finalized := stream.Finalize()
	if !finalized || result.State != usage.StateComplete || result.Tokens != (usage.Tokens{UncachedInput: 100, Output: 30}) {
		t.Fatalf("Finalize() = %#v, %t", result, finalized)
	}
	requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticInvalidPayload)
}

func TestUsageGeminiMalformedBodiesAreSanitized(t *testing.T) {
	extractor := NewGemini(http.DefaultClient)
	for _, body := range [][]byte{
		[]byte(`[]`),
		[]byte(`{"usageMetadata":{"promptTokenCount":100}} {}`),
		[]byte(`{"usage-secret-CANARY":"do-not-leak",`),
	} {
		if _, err := extractor.ExtractUsage(body); err == nil {
			t.Fatalf("ExtractUsage(%q) succeeded", body)
		} else if strings.Contains(err.Error(), "usage-secret-CANARY") || strings.Contains(err.Error(), "do-not-leak") {
			t.Fatalf("malformed error leaked payload: %q", err)
		}
	}
}

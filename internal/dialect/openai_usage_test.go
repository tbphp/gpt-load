package dialect

import (
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/usage"
)

func TestUsageOpenAICanonicalFixtures(t *testing.T) {
	extractor := NewOpenAI(http.DefaultClient)
	result, err := extractor.ExtractUsage(readUsageFixture(t, "openai", "nonstream.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}
	if result.State != usage.StateComplete || result.Tokens != want {
		t.Fatalf("non-stream result = %#v", result)
	}

	stream := extractor.NewUsageStreamExtractor()
	observeUsageJSONL(t, stream, readUsageFixture(t, "openai", "stream.jsonl"))
	streamResult, ok := stream.Finalize()
	if !ok || streamResult.State != usage.StateComplete || streamResult.Tokens != want {
		t.Fatalf("stream result = %#v, %t", streamResult, ok)
	}
	if _, ok := stream.Finalize(); ok {
		t.Fatal("second Finalize() succeeded")
	}
}

func TestUsageOpenAINonStreamOptionalFields(t *testing.T) {
	extractor := NewOpenAI(http.DefaultClient)
	tests := []struct {
		name        string
		body        string
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
	}{
		{
			name: "cached missing",
			body: `{"usage":{"prompt_tokens":100,"completion_tokens":30,"total_tokens":130,"prompt_tokens_details":{}}}`,
			want: usage.Tokens{UncachedInput: 100, Output: 30},
		},
		{
			name: "cached zero and cache write zero",
			body: `{"usage":{"prompt_tokens":100,"completion_tokens":30,"total_tokens":130,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}}}`,
			want: usage.Tokens{UncachedInput: 100, Output: 30},
		},
		{
			name:        "positive cache write is unrepresentable",
			body:        `{"usage":{"prompt_tokens":100,"completion_tokens":30,"total_tokens":130,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":5}}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticUnsupportedBillableDetail},
		},
		{
			name:        "negative cache write is clamped",
			body:        `{"usage":{"prompt_tokens":100,"completion_tokens":30,"total_tokens":130,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":-5}}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
		{
			name:        "invalid cache write is diagnosed",
			body:        `{"usage":{"prompt_tokens":100,"completion_tokens":30,"total_tokens":130,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":1.5}}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
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
			if _, ok := result.Diagnostics.TotalDelta(); ok {
				t.Fatalf("TotalDelta() unexpectedly present: %#v", result.Diagnostics)
			}
		})
	}
}

func TestUsageOpenAINonStreamRequiredAndStrictNumbers(t *testing.T) {
	extractor := NewOpenAI(http.DefaultClient)
	tests := []struct {
		name       string
		body       string
		want       usage.Tokens
		state      usage.State
		diagnostic usage.DiagnosticCode
	}{
		{
			name:       "prompt missing",
			body:       `{"usage":{"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`,
			want:       usage.Tokens{CacheRead: 20, Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticMissingRequiredField,
		},
		{
			name:       "prompt null",
			body:       `{"usage":{"prompt_tokens":null,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`,
			want:       usage.Tokens{CacheRead: 20, Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticMissingRequiredField,
		},
		{
			name:       "completion missing",
			body:       `{"usage":{"prompt_tokens":100}}`,
			want:       usage.Tokens{UncachedInput: 100},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticMissingRequiredField,
		},
		{
			name:       "completion null",
			body:       `{"usage":{"prompt_tokens":100,"completion_tokens":null}}`,
			want:       usage.Tokens{UncachedInput: 100},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticMissingRequiredField,
		},
		{
			name:       "negative prompt",
			body:       `{"usage":{"prompt_tokens":-100,"completion_tokens":30}}`,
			want:       usage.Tokens{Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticNegativeValue,
		},
		{
			name:       "fractional prompt",
			body:       `{"usage":{"prompt_tokens":1.5,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`,
			want:       usage.Tokens{CacheRead: 20, Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticInvalidNumber,
		},
		{
			name:       "exponent prompt",
			body:       `{"usage":{"prompt_tokens":1e2,"completion_tokens":30}}`,
			want:       usage.Tokens{Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticInvalidNumber,
		},
		{
			name:       "string prompt",
			body:       `{"usage":{"prompt_tokens":"100","completion_tokens":30}}`,
			want:       usage.Tokens{Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticInvalidNumber,
		},
		{
			name:       "boolean prompt",
			body:       `{"usage":{"prompt_tokens":true,"completion_tokens":30}}`,
			want:       usage.Tokens{Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticInvalidNumber,
		},
		{
			name:       "object prompt",
			body:       `{"usage":{"prompt_tokens":{},"completion_tokens":30}}`,
			want:       usage.Tokens{Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticInvalidNumber,
		},
		{
			name:       "array prompt",
			body:       `{"usage":{"prompt_tokens":[],"completion_tokens":30}}`,
			want:       usage.Tokens{Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticInvalidNumber,
		},
		{
			name:       "overflow prompt",
			body:       `{"usage":{"prompt_tokens":9223372036854775808,"completion_tokens":30}}`,
			want:       usage.Tokens{Output: 30},
			state:      usage.StateComplete,
			diagnostic: usage.DiagnosticInvalidNumber,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractor.ExtractUsage([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.State != tt.state || result.Tokens != tt.want || !result.Diagnostics.Has(tt.diagnostic) {
				t.Fatalf("result = %#v, want state %q tokens %#v and diagnostic %q", result, tt.state, tt.want, tt.diagnostic)
			}
		})
	}
}

func TestUsageOpenAIClampsCachedAndChecksTotal(t *testing.T) {
	extractor := NewOpenAI(http.DefaultClient)
	tests := []struct {
		name      string
		body      string
		want      usage.Tokens
		wantDelta int64
		code      usage.DiagnosticCode
	}{
		{
			name: "cached exceeds prompt",
			body: `{"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":120}}}`,
			want: usage.Tokens{CacheRead: 120, Output: 30},
			code: usage.DiagnosticNegativeValue,
		},
		{
			name:      "total mismatch",
			body:      `{"usage":{"prompt_tokens":100,"completion_tokens":30,"total_tokens":140,"prompt_tokens_details":{"cached_tokens":20}}}`,
			want:      usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			wantDelta: 10,
			code:      usage.DiagnosticInconsistentTotal,
		},
		{
			name:      "total shortfall",
			body:      `{"usage":{"prompt_tokens":100,"completion_tokens":30,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":20}}}`,
			want:      usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			wantDelta: -10,
			code:      usage.DiagnosticInconsistentTotal,
		},
		{
			name: "total overflow",
			body: `{"usage":{"prompt_tokens":100,"completion_tokens":30,"total_tokens":9223372036854775808}}`,
			want: usage.Tokens{UncachedInput: 100, Output: 30},
			code: usage.DiagnosticInvalidNumber,
		},
		{
			name: "normalized total overflow",
			body: `{"usage":{"prompt_tokens":9223372036854775807,"completion_tokens":1,"total_tokens":9223372036854775807}}`,
			want: usage.Tokens{UncachedInput: 9223372036854775807, Output: 1},
			code: usage.DiagnosticInvalidNumber,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractor.ExtractUsage([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.Tokens != tt.want || !result.Diagnostics.Has(tt.code) {
				t.Fatalf("result = %#v, want tokens %#v and diagnostic %q", result, tt.want, tt.code)
			}
			if tt.code == usage.DiagnosticInconsistentTotal {
				delta, ok := result.Diagnostics.TotalDelta()
				if !ok || delta != tt.wantDelta {
					t.Fatalf("TotalDelta() = %d, %t, want %d, true", delta, ok, tt.wantDelta)
				}
			}
		})
	}
}

func TestUsageOpenAIInvalidUsageObjectIsDiagnosedAndDoesNotDiscardStreamState(t *testing.T) {
	extractor := NewOpenAI(http.DefaultClient)
	result, err := extractor.ExtractUsage([]byte(`{"usage":[]}`))
	if err != nil || result.State != usage.StateMissing || result.Tokens != (usage.Tokens{}) {
		t.Fatalf("invalid non-stream usage = %#v, %v", result, err)
	}
	requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticInvalidNumber)

	partial := extractor.NewUsageStreamExtractor()
	for _, payload := range []string{
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`,
		`{"usage":[]}`,
	} {
		if err := partial.Observe([]byte(payload)); err != nil {
			t.Fatal(err)
		}
	}
	partialResult, ok := partial.Finalize()
	if !ok || partialResult.State != usage.StatePartial || partialResult.Tokens != (usage.Tokens{UncachedInput: 100, Output: 10}) {
		t.Fatalf("partial invalid usage result = %#v, %t", partialResult, ok)
	}
	requireUsageDiagnostics(t, partialResult.Diagnostics, usage.DiagnosticInvalidNumber)

	continued := extractor.NewUsageStreamExtractor()
	for _, payload := range []string{
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`,
		`{"usage":[]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":30}}`,
	} {
		if err := continued.Observe([]byte(payload)); err != nil {
			t.Fatal(err)
		}
	}
	continuedResult, ok := continued.Finalize()
	if !ok || continuedResult.State != usage.StateComplete || continuedResult.Tokens != (usage.Tokens{UncachedInput: 100, Output: 30}) {
		t.Fatalf("continued invalid usage result = %#v, %t", continuedResult, ok)
	}
	requireUsageDiagnostics(t, continuedResult.Diagnostics, usage.DiagnosticInvalidNumber)
}

func TestUsageOpenAIStreamSnapshotsAndFinality(t *testing.T) {
	extractor := NewOpenAI(http.DefaultClient)
	tests := []struct {
		name  string
		steps []string
		state usage.State
		want  usage.Tokens
	}{
		{
			name: "latest snapshot wins",
			steps: []string{
				`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`,
				`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`,
				`{"choices":[ ],"usage":{"prompt_tokens":100,"completion_tokens":30}}`,
			},
			state: usage.StateComplete,
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
		},
		{
			name:  "usage without final marker is partial",
			steps: []string{`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":30}}`},
			state: usage.StatePartial,
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
		},
		{
			name:  "no usage is missing",
			steps: []string{`{"choices":[{"delta":{"content":"hello"}}]}`},
			state: usage.StateMissing,
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
			result, ok := stream.Finalize()
			if !ok || result.State != tt.state || result.Tokens != tt.want {
				t.Fatalf("Finalize() = %#v, %t, want state %q tokens %#v", result, ok, tt.state, tt.want)
			}
		})
	}
}

func TestUsageOpenAIStreamReplaceSnapshotClearsAbsentTokensAndDiagnostics(t *testing.T) {
	stream := NewOpenAI(http.DefaultClient).NewUsageStreamExtractor()
	for _, payload := range []string{
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":1.5}}}`,
		`{"choices":[],"usage":{"prompt_tokens":100}}`,
	} {
		if err := stream.Observe([]byte(payload)); err != nil {
			t.Fatal(err)
		}
	}

	result, ok := stream.Finalize()
	if !ok || result.State != usage.StateComplete || result.Tokens != (usage.Tokens{UncachedInput: 100}) {
		t.Fatalf("Finalize() = %#v, %t, want latest complete snapshot without stale tokens", result, ok)
	}
	requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticMissingRequiredField)
}

func TestUsageOpenAIStreamMalformedPayloadKeepsDiagnosticAndState(t *testing.T) {
	stream := NewOpenAI(http.DefaultClient).NewUsageStreamExtractor()
	if err := stream.Observe([]byte(`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`)); err != nil {
		t.Fatal(err)
	}
	canary := []byte(`{"usage-secret-CANARY":"do-not-leak",`)
	if err := stream.Observe(canary); err == nil {
		t.Fatal("Observe(malformed) succeeded")
	} else if strings.Contains(err.Error(), "usage-secret-CANARY") || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("malformed error leaked payload: %q", err)
	}
	if err := stream.Observe([]byte(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":30}}`)); err != nil {
		t.Fatal(err)
	}
	result, ok := stream.Finalize()
	if !ok || result.State != usage.StateComplete || result.Tokens != (usage.Tokens{UncachedInput: 100, Output: 30}) ||
		!result.Diagnostics.Has(usage.DiagnosticInvalidPayload) {
		t.Fatalf("Finalize() = %#v, %t, want latest complete usage with invalid payload diagnostic", result, ok)
	}
}

func TestUsageOpenAIMalformedBodiesAndObserveOwnership(t *testing.T) {
	extractor := NewOpenAI(http.DefaultClient)
	for _, body := range [][]byte{[]byte(`{}`), []byte(`{"usage":null}`)} {
		result, err := extractor.ExtractUsage(body)
		if err != nil || result.State != usage.StateMissing || result.Tokens != (usage.Tokens{}) {
			t.Fatalf("ExtractUsage(%q) = %#v, %v, want missing zero result without error", body, result, err)
		}
	}
	for _, body := range [][]byte{[]byte(`[]`), []byte(`{"usage":{}} {}`)} {
		if _, err := extractor.ExtractUsage(body); err == nil {
			t.Fatalf("ExtractUsage(%q) succeeded", body)
		}
	}

	stream := extractor.NewUsageStreamExtractor()
	payload := []byte(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":30}}`)
	if err := stream.Observe(payload); err != nil {
		t.Fatal(err)
	}
	copy(payload, []byte(`this caller mutation must not be retained`))
	result, ok := stream.Finalize()
	if !ok || result.State != usage.StateComplete || result.Tokens != (usage.Tokens{UncachedInput: 100, Output: 30}) {
		t.Fatalf("Finalize() after caller mutation = %#v, %t", result, ok)
	}
}

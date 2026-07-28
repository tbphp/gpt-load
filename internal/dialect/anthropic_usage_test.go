package dialect

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/pricing"
	"gpt-load/internal/usage"
)

func TestUsageAnthropicCanonicalFixtures(t *testing.T) {
	extractor, ok := any(NewAnthropic(http.DefaultClient)).(UsageExtractor)
	if !ok {
		t.Fatal("Anthropic does not expose UsageExtractor capability")
	}

	want := usage.Tokens{
		UncachedInput: 80,
		CacheRead:     20,
		CacheWrite5M:  5,
		CacheWrite1H:  7,
		Output:        30,
	}
	result, err := extractor.ExtractUsage(readUsageFixture(t, "anthropic", "nonstream.json"))
	if err != nil {
		t.Fatal(err)
	}
	if result.State != usage.StateComplete || result.Tokens != want {
		t.Fatalf("non-stream result = %#v, want complete with %#v", result, want)
	}

	stream := extractor.NewUsageStreamExtractor()
	observeUsageJSONL(t, stream, readUsageFixture(t, "anthropic", "stream.jsonl"))
	streamResult, finalized := stream.Finalize()
	if !finalized || streamResult.State != usage.StateComplete || streamResult.Tokens != want {
		t.Fatalf("stream result = %#v, %t, want complete with %#v", streamResult, finalized, want)
	}
	if _, finalized := stream.Finalize(); finalized {
		t.Fatal("second Finalize() succeeded")
	}
}

func TestUsageAnthropicCacheCreationMapping(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name        string
		body        string
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
		delta       *int64
	}{
		{
			name: "detail wins when aggregate matches",
			body: `{"usage":{"input_tokens":80,"output_tokens":30,"cache_read_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7},"cache_creation_input_tokens":12}}`,
			want: usage.Tokens{UncachedInput: 80, CacheRead: 20, CacheWrite5M: 5, CacheWrite1H: 7, Output: 30},
		},
		{
			name:        "aggregate falls back to five minutes",
			body:        `{"usage":{"input_tokens":80,"output_tokens":30,"cache_creation_input_tokens":12}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheWrite5M: 12, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticCacheWriteDefaulted5M},
		},
		{
			name:        "aggregate surplus records positive delta",
			body:        `{"usage":{"input_tokens":80,"output_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7},"cache_creation_input_tokens":15}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheWrite5M: 5, CacheWrite1H: 7, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInconsistentTotal},
			delta:       usageInt64(3),
		},
		{
			name:        "aggregate shortfall records negative delta",
			body:        `{"usage":{"input_tokens":80,"output_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7},"cache_creation_input_tokens":10}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheWrite5M: 5, CacheWrite1H: 7, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInconsistentTotal},
			delta:       usageInt64(-2),
		},
		{
			name:        "detail sum overflow is diagnosed without wrapping",
			body:        `{"usage":{"input_tokens":80,"output_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":9223372036854775807,"ephemeral_1h_input_tokens":1},"cache_creation_input_tokens":9223372036854775807}}`,
			want:        usage.Tokens{UncachedInput: 80, CacheWrite5M: math.MaxInt64, CacheWrite1H: 1, Output: 30},
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

func TestUsageAnthropicInvalidCacheCreationDoesNotFallback(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	for _, cacheCreation := range []string{`[]`, `"unsupported"`} {
		result, err := extractor.ExtractUsage([]byte(`{"usage":{"input_tokens":80,"output_tokens":30,"cache_creation":` + cacheCreation + `,"cache_creation_input_tokens":12}}`))
		if err != nil {
			t.Fatal(err)
		}
		if result.State != usage.StateComplete || result.Tokens != (usage.Tokens{UncachedInput: 80, Output: 30}) {
			t.Fatalf("cache_creation %s result = %#v", cacheCreation, result)
		}
		requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticInvalidNumber)
		if _, ok := result.Diagnostics.TotalDelta(); ok {
			t.Fatalf("cache_creation %s unexpectedly recorded total delta", cacheCreation)
		}
	}
}

func TestUsageAnthropicDetailReconciliationRequiresUsableComponents(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name        string
		value       string
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
	}{
		{
			name:        "invalid component",
			value:       `"bad"`,
			want:        usage.Tokens{UncachedInput: 80, CacheWrite1H: 7, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "overflow component",
			value:       `9223372036854775808`,
			want:        usage.Tokens{UncachedInput: 80, CacheWrite1H: 7, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "negative component",
			value:       `-1`,
			want:        usage.Tokens{UncachedInput: 80, CacheWrite1H: 7, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"usage":{"input_tokens":80,"output_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":` + tt.value + `,"ephemeral_1h_input_tokens":7},"cache_creation_input_tokens":12}}`
			result, err := extractor.ExtractUsage([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if result.State != usage.StateComplete || result.Tokens != tt.want {
				t.Fatalf("result = %#v, want %#v", result, tt.want)
			}
			requireUsageDiagnostics(t, result.Diagnostics, tt.diagnostics...)
			if _, ok := result.Diagnostics.TotalDelta(); ok {
				t.Fatalf("invalid component unexpectedly recorded total delta: %#v", result.Diagnostics)
			}
		})
	}
}

func TestUsageAnthropicStrictNumbersAndRequiredFields(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name        string
		body        string
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
	}{
		{
			name: "all required and optional values may be zero",
			body: `{"usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"cache_creation_input_tokens":0}}`,
		},
		{
			name:        "input missing preserves output",
			body:        `{"usage":{"output_tokens":30}}`,
			want:        usage.Tokens{Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:        "output null preserves input",
			body:        `{"usage":{"input_tokens":80,"output_tokens":null}}`,
			want:        usage.Tokens{UncachedInput: 80},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:        "negative required value is safely zeroed",
			body:        `{"usage":{"input_tokens":-80,"output_tokens":30}}`,
			want:        usage.Tokens{Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
		{
			name: "optional missing and null values are zero",
			body: `{"usage":{"input_tokens":80,"output_tokens":30,"cache_read_input_tokens":null,"cache_creation_input_tokens":null}}`,
			want: usage.Tokens{UncachedInput: 80, Output: 30},
		},
		{
			name:        "negative optional aggregate is not a defaulted aggregate",
			body:        `{"usage":{"input_tokens":80,"output_tokens":30,"cache_creation_input_tokens":-1}}`,
			want:        usage.Tokens{UncachedInput: 80, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
		{
			name:        "fractional required value is rejected",
			body:        `{"usage":{"input_tokens":1.5,"output_tokens":30}}`,
			want:        usage.Tokens{Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "exponent required value is rejected",
			body:        `{"usage":{"input_tokens":1e2,"output_tokens":30}}`,
			want:        usage.Tokens{Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "string optional value is rejected",
			body:        `{"usage":{"input_tokens":80,"output_tokens":30,"cache_read_input_tokens":"20"}}`,
			want:        usage.Tokens{UncachedInput: 80, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "fractional optional aggregate is rejected",
			body:        `{"usage":{"input_tokens":80,"output_tokens":30,"cache_creation_input_tokens":1.5}}`,
			want:        usage.Tokens{UncachedInput: 80, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "overflow required value is rejected",
			body:        `{"usage":{"input_tokens":9223372036854775808,"output_tokens":30}}`,
			want:        usage.Tokens{Output: 30},
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
		})
	}
}

func TestUsageAnthropicRequiredPresenceControlsState(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name        string
		body        string
		state       usage.State
		diagnostics []usage.DiagnosticCode
	}{
		{
			name:  "usage absent is missing without diagnostics",
			body:  `{}`,
			state: usage.StateMissing,
		},
		{
			name:  "usage null is missing without diagnostics",
			body:  `{"usage":null}`,
			state: usage.StateMissing,
		},
		{
			name:        "empty usage is missing",
			body:        `{"usage":{}}`,
			state:       usage.StateMissing,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:  "explicit required zero is complete",
			body:  `{"usage":{"input_tokens":0,"output_tokens":0}}`,
			state: usage.StateComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractor.ExtractUsage([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.State != tt.state || result.Tokens != (usage.Tokens{}) {
				t.Fatalf("result = %#v, want state %q", result, tt.state)
			}
			requireUsageDiagnostics(t, result.Diagnostics, tt.diagnostics...)
			if _, ok := result.Diagnostics.TotalDelta(); ok {
				t.Fatalf("TotalDelta() unexpectedly present: %#v", result.Diagnostics)
			}
		})
	}
}

func TestUsageAnthropicStreamCompletesOnlyAtMessageStop(t *testing.T) {
	start := `{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_read_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7},"output_tokens":0}}}`
	delta := `{"type":"message_delta","usage":{"output_tokens":30}}`

	partial := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
	for _, event := range []string{start, delta} {
		if err := partial.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	partialResult, finalized := partial.Finalize()
	want := usage.Tokens{UncachedInput: 80, CacheRead: 20, CacheWrite5M: 5, CacheWrite1H: 7, Output: 30}
	if !finalized || partialResult.State != usage.StatePartial || partialResult.Tokens != want {
		t.Fatalf("stream without stop = %#v, %t, want partial with %#v", partialResult, finalized, want)
	}
	requireUsageDiagnostics(t, partialResult.Diagnostics)

	complete := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
	for _, event := range []string{start, delta, `{"type":"message_stop"}`} {
		if err := complete.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	completeResult, finalized := complete.Finalize()
	if !finalized || completeResult.State != usage.StateComplete || completeResult.Tokens != want {
		t.Fatalf("stream with stop = %#v, %t, want complete with %#v", completeResult, finalized, want)
	}
	requireUsageDiagnostics(t, completeResult.Diagnostics)
}

func TestUsageAnthropicStreamMergesCumulativeDeltaFields(t *testing.T) {
	stream := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
	for _, event := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_read_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7},"output_tokens":0}}}`,
		`{"type":"message_delta","usage":{"output_tokens":10}}`,
		`{"type":"message_delta","usage":{"cache_read_input_tokens":25}}`,
		`{"type":"message_delta","usage":{"input_tokens":90,"cache_creation":{"ephemeral_5m_input_tokens":8}}}`,
		`{"type":"message_delta","usage":{"output_tokens":30}}`,
		`{"type":"message_stop"}`,
	} {
		if err := stream.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	result, finalized := stream.Finalize()
	want := usage.Tokens{UncachedInput: 90, CacheRead: 25, CacheWrite5M: 8, CacheWrite1H: 7, Output: 30}
	if !finalized || result.State != usage.StateComplete || result.Tokens != want {
		t.Fatalf("Finalize() = %#v, %t, want complete with %#v", result, finalized, want)
	}
	requireUsageDiagnostics(t, result.Diagnostics)

	fallback := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
	for _, event := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_creation_input_tokens":12}}}`,
		`{"type":"message_stop"}`,
	} {
		if err := fallback.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	fallbackResult, finalized := fallback.Finalize()
	if !finalized || fallbackResult.State != usage.StateComplete ||
		fallbackResult.Tokens != (usage.Tokens{UncachedInput: 80, CacheWrite5M: 12}) {
		t.Fatalf("aggregate fallback = %#v, %t", fallbackResult, finalized)
	}
	requireUsageDiagnostics(t, fallbackResult.Diagnostics, usage.DiagnosticCacheWriteDefaulted5M)
}

func TestUsageAnthropicStreamReconcilesAggregateAfterCumulativeMerge(t *testing.T) {
	tests := []struct {
		name        string
		events      []string
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
	}{
		{
			name: "aggregate-only delta reuses trusted detail breakdown",
			events: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7},"cache_creation_input_tokens":12}}}`,
				`{"type":"message_delta","usage":{"cache_creation_input_tokens":12}}`,
				`{"type":"message_stop"}`,
			},
			want: usage.Tokens{UncachedInput: 80, CacheWrite5M: 5, CacheWrite1H: 7},
		},
		{
			name: "partial delta detail reconciles with trusted omitted field",
			events: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_creation":{"ephemeral_1h_input_tokens":7}}}}`,
				`{"type":"message_delta","usage":{"cache_creation":{"ephemeral_5m_input_tokens":8},"cache_creation_input_tokens":15}}`,
				`{"type":"message_stop"}`,
			},
			want: usage.Tokens{UncachedInput: 80, CacheWrite5M: 8, CacheWrite1H: 7},
		},
		{
			name: "real detail replaces provisional aggregate fallback",
			events: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_creation_input_tokens":12}}}`,
				`{"type":"message_delta","usage":{"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7},"cache_creation_input_tokens":12}}`,
				`{"type":"message_stop"}`,
			},
			want:        usage.Tokens{UncachedInput: 80, CacheWrite5M: 5, CacheWrite1H: 7},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticCacheWriteDefaulted5M},
		},
		{
			name: "invalid detail does not trigger aggregate fallback",
			events: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_creation":[],"cache_creation_input_tokens":12}}}`,
				`{"type":"message_stop"}`,
			},
			want:        usage.Tokens{UncachedInput: 80},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
			for _, event := range tt.events {
				if err := stream.Observe([]byte(event)); err != nil {
					t.Fatal(err)
				}
			}
			result, finalized := stream.Finalize()
			if !finalized || result.State != usage.StateComplete || result.Tokens != tt.want {
				t.Fatalf("Finalize() = %#v, %t, want complete with %#v", result, finalized, tt.want)
			}
			requireUsageDiagnostics(t, result.Diagnostics, tt.diagnostics...)
		})
	}
}

func TestUsageAnthropicStreamRejectsOutOfOrderAndRegressingCumulatives(t *testing.T) {
	t.Run("delta and stop without valid start", func(t *testing.T) {
		stream := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
		for _, event := range []string{
			`{"type":"message_delta","usage":{"output_tokens":30}}`,
			`{"type":"message_stop"}`,
		} {
			if err := stream.Observe([]byte(event)); err != nil {
				t.Fatal(err)
			}
		}
		result, finalized := stream.Finalize()
		if !finalized || result.State != usage.StateMissing || result.Tokens != (usage.Tokens{}) {
			t.Fatalf("Finalize() = %#v, %t, want missing", result, finalized)
		}
		requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticInvalidEventSequence)
	})

	t.Run("last good cumulatives survive invalid events", func(t *testing.T) {
		stream := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
		for _, event := range []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":80,"output_tokens":0}}}`,
			`{"type":"message_delta","usage":{"output_tokens":10,"cache_read_input_tokens":4}}`,
			`{"type":"message_delta","usage":{"output_tokens":5}}`,
			`{"type":"message_delta","usage":{"cache_read_input_tokens":-1}}`,
			`{"type":"message_delta","usage":{"input_tokens":"invalid"}}`,
			`{"type":"message_stop"}`,
			`{"type":"message_delta","usage":{"output_tokens":20}}`,
			`{"type":"message_stop"}`,
		} {
			if err := stream.Observe([]byte(event)); err != nil {
				t.Fatal(err)
			}
		}
		result, finalized := stream.Finalize()
		want := usage.Tokens{UncachedInput: 80, CacheRead: 4, Output: 10}
		if !finalized || result.State != usage.StateComplete || result.Tokens != want {
			t.Fatalf("Finalize() = %#v, %t, want complete with %#v", result, finalized, want)
		}
		requireUsageDiagnostics(
			t,
			result.Diagnostics,
			usage.DiagnosticNegativeValue,
			usage.DiagnosticInvalidNumber,
			usage.DiagnosticInvalidEventSequence,
		)
	})
}

func TestUsageAnthropicStreamStartStopWithoutDelta(t *testing.T) {
	stream := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
	for _, event := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_read_input_tokens":20,"output_tokens":0}}}`,
		`{"type":"message_stop"}`,
	} {
		if err := stream.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	result, finalized := stream.Finalize()
	want := usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 0}
	if !finalized || result.State != usage.StateComplete || result.Tokens != want {
		t.Fatalf("Finalize() = %#v, %t, want complete with %#v", result, finalized, want)
	}
	requireUsageDiagnostics(t, result.Diagnostics)
}

func TestUsageAnthropicStreamEOFRemainsPartial(t *testing.T) {
	stream := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
	for _, event := range []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":80}}}`,
		`{"type":"message_delta","usage":{"output_tokens":30}}`,
	} {
		if err := stream.Observe([]byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	result, finalized := stream.Finalize()
	want := usage.Tokens{UncachedInput: 80, Output: 30}
	if !finalized || result.State != usage.StatePartial || result.Tokens != want {
		t.Fatalf("Finalize() = %#v, %t, want partial with %#v", result, finalized, want)
	}
	requireUsageDiagnostics(t, result.Diagnostics)
}

func TestUsageAnthropicServerToolDetailRemainsUnpriced(t *testing.T) {
	stream := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
	observeUsageJSONL(t, stream, readUsageFixture(t, "anthropic", "server-tool.jsonl"))
	result, finalized := stream.Finalize()
	if !finalized || result.State != usage.StateComplete ||
		result.Tokens != (usage.Tokens{UncachedInput: 80, Output: 30}) {
		t.Fatalf("Finalize() = %#v, %t", result, finalized)
	}
	requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticUnsupportedBillableDetail)

	table, err := pricing.Compile([]pricing.Rule{{
		Pattern: "claude-test",
		Prices: pricing.Prices{
			UncachedInput: pricing.Price{Value: 1, Set: true},
			Output:        pricing.Price{Value: 1, Set: true},
		},
		Source: pricing.SourceUser,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if quote := table.Quote("claude-test", result); quote.State != pricing.CostStateUnpriced || quote.Cost != 0 {
		t.Fatalf("Quote() = %+v, want unpriced zero", quote)
	}
}

func TestUsageAnthropicNonStreamServerToolDetailPricing(t *testing.T) {
	table, err := pricing.Compile([]pricing.Rule{{
		Pattern: "claude-test",
		Prices: pricing.Prices{
			UncachedInput: pricing.Price{Value: 1, Set: true},
			Output:        pricing.Price{Value: 1, Set: true},
		},
		Source: pricing.SourceUser,
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name        string
		count       int
		diagnostics []usage.DiagnosticCode
		costState   pricing.CostState
	}{
		{
			name:        "nonzero detail is unpriced",
			count:       2,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticUnsupportedBillableDetail},
			costState:   pricing.CostStateUnpriced,
		},
		{
			name:      "zero detail is not diagnosed",
			count:     0,
			costState: pricing.CostStatePriced,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"usage":{"input_tokens":80,"output_tokens":30,"server_tool_use":{"web_search_requests":%d}}}`,
				tt.count,
			)
			result, extractErr := NewAnthropic(http.DefaultClient).ExtractUsage([]byte(body))
			if extractErr != nil {
				t.Fatal(extractErr)
			}
			if result.State != usage.StateComplete ||
				result.Tokens != (usage.Tokens{UncachedInput: 80, Output: 30}) {
				t.Fatalf("ExtractUsage() = %#v", result)
			}
			requireUsageDiagnostics(t, result.Diagnostics, tt.diagnostics...)
			if quote := table.Quote("claude-test", result); quote.State != tt.costState {
				t.Fatalf("Quote() = %+v, want state %q", quote, tt.costState)
			}
		})
	}
}

func TestUsageAnthropicStreamPatchState(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name        string
		steps       []string
		state       usage.State
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
	}{
		{
			name:  "start only remains partial",
			steps: []string{`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_read_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":7}}}}`},
			state: usage.StatePartial,
			want:  usage.Tokens{UncachedInput: 80, CacheRead: 20, CacheWrite5M: 5, CacheWrite1H: 7},
		},
		{
			name:        "delta only is rejected without trusted tokens",
			steps:       []string{`{"type":"message_delta","usage":{"output_tokens":30}}`},
			state:       usage.StateMissing,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidEventSequence},
		},
		{
			name: "start then missing output remains partial",
			steps: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80}}}`,
				`{"type":"message_delta","usage":{}}`,
			},
			state: usage.StatePartial,
			want:  usage.Tokens{UncachedInput: 80},
		},
		{
			name: "start then invalid output remains partial",
			steps: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80}}}`,
				`{"type":"message_delta","usage":{"output_tokens":1.5}}`,
			},
			state:       usage.StatePartial,
			want:        usage.Tokens{UncachedInput: 80},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name: "repeated delta overwrites output",
			steps: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80}}}`,
				`{"type":"message_delta","usage":{"output_tokens":10}}`,
				`{"type":"message_delta","usage":{"output_tokens":30}}`,
				`{"type":"message_stop"}`,
			},
			state: usage.StateComplete,
			want:  usage.Tokens{UncachedInput: 80, Output: 30},
		},
		{
			name: "delta before start cannot become complete retroactively",
			steps: []string{
				`{"type":"message_delta","usage":{"output_tokens":30}}`,
				`{"type":"message_start","message":{"usage":{"input_tokens":80}}}`,
			},
			state:       usage.StatePartial,
			want:        usage.Tokens{UncachedInput: 80},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidEventSequence},
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
}

func TestUsageAnthropicStreamPresenceAndInvalidStartHandling(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name        string
		steps       []string
		state       usage.State
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
	}{
		{
			name: "repeated start without optional fields preserves cache",
			steps: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_read_input_tokens":20}}}`,
				`{"type":"message_start","message":{"usage":{"input_tokens":80}}}`,
				`{"type":"message_delta","usage":{"output_tokens":30}}`,
				`{"type":"message_stop"}`,
			},
			state:       usage.StateComplete,
			want:        usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidEventSequence},
		},
		{
			name: "invalid input cannot establish start",
			steps: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":"bad"}}}`,
				`{"type":"message_delta","usage":{"output_tokens":30}}`,
			},
			state:       usage.StateMissing,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber, usage.DiagnosticInvalidEventSequence},
		},
		{
			name: "negative input cannot establish start",
			steps: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":-1}}}`,
				`{"type":"message_delta","usage":{"output_tokens":30}}`,
			},
			state:       usage.StateMissing,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue, usage.DiagnosticInvalidEventSequence},
		},
		{
			name: "negative output is not final",
			steps: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80}}}`,
				`{"type":"message_delta","usage":{"output_tokens":-1}}`,
			},
			state:       usage.StatePartial,
			want:        usage.Tokens{UncachedInput: 80},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
		{
			name: "invalid nested object keeps prior state for later delta",
			steps: []string{
				`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_read_input_tokens":20}}}`,
				`{"type":"message_start","message":{"usage":{"input_tokens":80,"cache_creation":[]}}}`,
				`{"type":"message_delta","usage":{"output_tokens":30}}`,
				`{"type":"message_stop"}`,
			},
			state:       usage.StateComplete,
			want:        usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber, usage.DiagnosticInvalidEventSequence},
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
}

func TestUsageAnthropicMalformedPayloadPreservesStateAndDoesNotLeak(t *testing.T) {
	stream := NewAnthropic(http.DefaultClient).NewUsageStreamExtractor()
	if err := stream.Observe([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":80}}}`)); err != nil {
		t.Fatal(err)
	}
	canary := []byte(`{"usage-secret-CANARY":"do-not-leak",`)
	if err := stream.Observe(canary); err == nil {
		t.Fatal("Observe(malformed) succeeded")
	} else if strings.Contains(err.Error(), "usage-secret-CANARY") || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("malformed error leaked payload: %q", err)
	}
	if err := stream.Observe([]byte(`{"type":"message_delta","usage":{"output_tokens":30}}`)); err != nil {
		t.Fatal(err)
	}
	if err := stream.Observe([]byte(`{"type":"message_stop"}`)); err != nil {
		t.Fatal(err)
	}
	result, finalized := stream.Finalize()
	if !finalized || result.State != usage.StateComplete || result.Tokens != (usage.Tokens{UncachedInput: 80, Output: 30}) {
		t.Fatalf("Finalize() = %#v, %t", result, finalized)
	}
	requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticInvalidPayload)
}

func TestUsageAnthropicInvalidUsageObjectIsDiagnosed(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	result, err := extractor.ExtractUsage([]byte(`{"usage":[]}`))
	if err != nil || result.State != usage.StateMissing || result.Tokens != (usage.Tokens{}) {
		t.Fatalf("invalid non-stream usage = %#v, %v", result, err)
	}
	requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticInvalidNumber)

	stream := extractor.NewUsageStreamExtractor()
	if err := stream.Observe([]byte(`{"type":"message_start","message":{"usage":[]}}`)); err != nil {
		t.Fatal(err)
	}
	result, finalized := stream.Finalize()
	if !finalized || result.State != usage.StateMissing || result.Tokens != (usage.Tokens{}) {
		t.Fatalf("invalid stream usage = %#v, %t", result, finalized)
	}
	requireUsageDiagnostics(t, result.Diagnostics, usage.DiagnosticInvalidNumber)
}

func TestUsageAnthropicMalformedBodiesReturnSanitizedErrors(t *testing.T) {
	extractor := NewAnthropic(http.DefaultClient)
	for _, body := range [][]byte{
		[]byte(`[]`),
		[]byte(`{"usage":{"input_tokens":80,"output_tokens":30}} {}`),
		[]byte(`{"usage-secret-CANARY":"do-not-leak",`),
	} {
		if _, err := extractor.ExtractUsage(body); err == nil {
			t.Fatalf("ExtractUsage(%q) succeeded", body)
		} else if strings.Contains(err.Error(), "usage-secret-CANARY") || strings.Contains(err.Error(), "do-not-leak") {
			t.Fatalf("malformed error leaked payload: %q", err)
		}
	}
}

func usageInt64(value int64) *int64 {
	return &value
}

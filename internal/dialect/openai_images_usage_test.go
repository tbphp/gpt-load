package dialect

import (
	"testing"

	"gpt-load/internal/usage"
)

func TestOpenAIImagesUsageNonStreaming(t *testing.T) {
	t.Parallel()

	extractor, ok := any(NewOpenAIImages()).(UsageExtractor)
	if !ok {
		t.Fatal("OpenAI Images does not expose UsageExtractor capability")
	}
	tests := []struct {
		name        string
		body        string
		want        usage.Tokens
		diagnostics []usage.DiagnosticCode
		wantDelta   *int64
	}{
		{
			name: "generation includes text and image input details",
			body: `{
				"created": 123,
				"data": [{"b64_json":"AA=="}],
				"usage": {
					"input_tokens": 100,
					"input_tokens_details": {"text_tokens": 4, "image_tokens": 96},
					"output_tokens": 30,
					"total_tokens": 130
				}
			}`,
			want: usage.Tokens{UncachedInput: 100, Output: 30},
		},
		{
			name: "edit uses the same standard usage fields",
			body: `{
				"created": 123,
				"data": [{"b64_json":"AA=="}],
				"usage": {"input_tokens": 100, "output_tokens": 30, "total_tokens": 130}
			}`,
			want: usage.Tokens{UncachedInput: 100, Output: 30},
		},
		{
			name:        "negative input is diagnosed",
			body:        `{"usage":{"input_tokens":-1,"output_tokens":30,"total_tokens":30}}`,
			want:        usage.Tokens{Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticNegativeValue},
		},
		{
			name:        "fractional input is diagnosed",
			body:        `{"usage":{"input_tokens":1.5,"output_tokens":30,"total_tokens":30}}`,
			want:        usage.Tokens{Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidNumber},
		},
		{
			name:        "missing required input is diagnosed",
			body:        `{"usage":{"output_tokens":30}}`,
			want:        usage.Tokens{Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:        "missing required output is diagnosed",
			body:        `{"usage":{"input_tokens":100}}`,
			want:        usage.Tokens{UncachedInput: 100},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticMissingRequiredField},
		},
		{
			name:        "inconsistent total is diagnosed",
			body:        `{"usage":{"input_tokens":100,"output_tokens":30,"total_tokens":140}}`,
			want:        usage.Tokens{UncachedInput: 100, Output: 30},
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInconsistentTotal},
			wantDelta:   int64Pointer(10),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := extractor.ExtractUsage([]byte(test.body))
			if err != nil {
				t.Fatalf("ExtractUsage() error = %v", err)
			}
			if result.State != usage.StateComplete || result.Tokens != test.want {
				t.Fatalf("ExtractUsage() = %#v, want complete with %#v", result, test.want)
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

func TestOpenAIImagesUsageStreamingCompletedEvents(t *testing.T) {
	t.Parallel()

	extractor, ok := any(NewOpenAIImages()).(UsageExtractor)
	if !ok {
		t.Fatal("OpenAI Images does not expose UsageExtractor capability")
	}
	tests := []struct {
		name        string
		events      []StreamEvent
		want        usage.Tokens
		state       usage.State
		diagnostics []usage.DiagnosticCode
	}{
		{
			name: "generation completed event finalizes usage",
			events: []StreamEvent{
				{Payload: []byte(`{"type":"image_generation.partial_image","usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}`)},
				{Payload: []byte(`{"type":"image_generation.completed","usage":{"input_tokens":100,"input_tokens_details":{"text_tokens":4,"image_tokens":96},"output_tokens":30,"total_tokens":130}}`)},
			},
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
			state: usage.StateComplete,
		},
		{
			name: "edit completed event can use explicit SSE name",
			events: []StreamEvent{
				{Name: "image_edit.completed", Payload: []byte(`{"usage":{"input_tokens":100,"output_tokens":30,"total_tokens":130}}`)},
			},
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
			state: usage.StateComplete,
		},
		{
			name: "completed without usage discards earlier partial snapshot",
			events: []StreamEvent{
				{Payload: []byte(`{"type":"image_generation.partial_image","usage":{"input_tokens":100,"output_tokens":10}}`)},
				{Payload: []byte(`{"type":"image_generation.completed","b64_json":"AA=="}`)},
			},
			state: usage.StateMissing,
		},
		{
			name: "usage without completed event remains partial",
			events: []StreamEvent{
				{Payload: []byte(`{"type":"image_edit.partial_image","usage":{"input_tokens":100,"output_tokens":30}}`)},
			},
			want:  usage.Tokens{UncachedInput: 100, Output: 30},
			state: usage.StatePartial,
		},
		{
			name: "event after completed is diagnosed and ignored",
			events: []StreamEvent{
				{Payload: []byte(`{"type":"image_generation.completed","usage":{"input_tokens":100,"output_tokens":30,"total_tokens":130}}`)},
				{Payload: []byte(`{"type":"image_generation.partial_image","usage":{"input_tokens":200,"output_tokens":40,"total_tokens":240}}`)},
			},
			want:        usage.Tokens{UncachedInput: 100, Output: 30},
			state:       usage.StateComplete,
			diagnostics: []usage.DiagnosticCode{usage.DiagnosticInvalidEventSequence},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stream := extractor.NewUsageStreamExtractor()
			observer, ok := stream.(UsageStreamEventObserver)
			if !ok {
				t.Fatal("OpenAI Images usage stream does not observe SSE event names")
			}
			for _, event := range test.events {
				if err := observer.ObserveStreamEvent(event); err != nil {
					t.Fatalf("ObserveStreamEvent() error = %v", err)
				}
			}
			result, finalized := stream.Finalize()
			if !finalized || result.State != test.state || result.Tokens != test.want {
				t.Fatalf("Finalize() = %#v, %t, want state %q tokens %#v", result, finalized, test.state, test.want)
			}
			requireUsageDiagnostics(t, result.Diagnostics, test.diagnostics...)
		})
	}
}

func TestOpenAIImagesUsageStreamingRejectsEventNameConflict(t *testing.T) {
	t.Parallel()

	extractor, ok := any(NewOpenAIImages()).(UsageExtractor)
	if !ok {
		t.Fatal("OpenAI Images does not expose UsageExtractor capability")
	}
	stream := extractor.NewUsageStreamExtractor()
	observer, ok := stream.(UsageStreamEventObserver)
	if !ok {
		t.Fatal("OpenAI Images usage stream does not observe SSE event names")
	}
	if err := observer.ObserveStreamEvent(StreamEvent{
		Name:    "image_generation.completed",
		Payload: []byte(`{"type":"image_edit.completed","usage":{"input_tokens":100,"output_tokens":30}}`),
	}); err != nil {
		t.Fatalf("ObserveStreamEvent() error = %v", err)
	}
	result, finalized := stream.Finalize()
	if !finalized || result.State != usage.StateMissing ||
		!result.Diagnostics.Has(usage.DiagnosticInvalidPayload) {
		t.Fatalf("Finalize() = %#v, %t, want missing invalid-payload result", result, finalized)
	}
}

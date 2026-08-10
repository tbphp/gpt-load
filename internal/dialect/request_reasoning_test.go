package dialect

import (
	"net/http"
	"reflect"
	"testing"

	"gpt-load/internal/reasoning"
)

func TestInspectRequestCapturesExplicitReasoningConfiguration(t *testing.T) {
	t.Parallel()

	budget4096 := int64(4096)
	dynamicBudget := int64(-1)
	tests := []struct {
		name    string
		value   Dialect
		request *ParsedRequest
		want    reasoning.Config
	}{
		{
			name:  "OpenAI completions effort",
			value: NewOpenAI(),
			request: &ParsedRequest{
				Body: []byte(`{"model":"gpt-5","reasoning_effort":"HIGH"}`),
			},
			want: reasoning.Config{Effort: "high"},
		},
		{
			name:  "OpenAI Responses mode and effort",
			value: NewOpenAIResponses(),
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{"model":"gpt-5","reasoning":{"mode":"pro","effort":"XHIGH"}}`),
			},
			want: reasoning.Config{Mode: "pro", Effort: "xhigh"},
		},
		{
			name:  "Anthropic adaptive effort",
			value: NewAnthropic(),
			request: &ParsedRequest{
				Body: []byte(`{"model":"claude-sonnet-4-6","thinking":{"type":"adaptive"},"output_config":{"effort":"HIGH"}}`),
			},
			want: reasoning.Config{Mode: "adaptive", Effort: "high"},
		},
		{
			name:  "Anthropic manual budget",
			value: NewAnthropic(),
			request: &ParsedRequest{
				Body: []byte(`{"model":"claude-sonnet-4-5","thinking":{"type":"enabled","budget_tokens":4096}}`),
			},
			want: reasoning.Config{Mode: "enabled", BudgetTokens: &budget4096},
		},
		{
			name:  "Gemini level and dynamic budget",
			value: NewGemini(),
			request: &ParsedRequest{
				Path: "/v1beta/models/gemini-3-flash:generateContent",
				Body: []byte(`{"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH","thinkingBudget":-1}}}`),
			},
			want: reasoning.Config{Effort: "high", BudgetTokens: &dynamicBudget},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := test.value.InspectRequest(test.request)
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if !reflect.DeepEqual(metadata.Reasoning, test.want) {
				t.Fatalf("InspectRequest().Reasoning = %#v, want %#v", metadata.Reasoning, test.want)
			}
		})
	}
}

func TestInspectRequestIgnoresInvalidReasoningMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   Dialect
		request *ParsedRequest
		want    reasoning.Config
	}{
		{
			name:    "OpenAI completions wrong effort type",
			value:   NewOpenAI(),
			request: &ParsedRequest{Body: []byte(`{"model":"gpt-5","reasoning_effort":3}`)},
		},
		{
			name:  "OpenAI Responses unknown reasoning object",
			value: NewOpenAIResponses(),
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{"model":"gpt-5","reasoning":"high"}`),
			},
		},
		{
			name:    "Anthropic wrong budget type",
			value:   NewAnthropic(),
			request: &ParsedRequest{Body: []byte(`{"model":"claude-sonnet-4-5","thinking":{"type":"enabled","budget_tokens":"4096"}}`)},
			want:    reasoning.Config{Mode: "enabled"},
		},
		{
			name:  "Gemini invalid body remains pass through metadata",
			value: NewGemini(),
			request: &ParsedRequest{
				Path: "/v1beta/models/gemini-3-flash:generateContent",
				Body: []byte("{"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := test.value.InspectRequest(test.request)
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if !reflect.DeepEqual(metadata.Reasoning, test.want) {
				t.Fatalf("InspectRequest().Reasoning = %#v, want %#v", metadata.Reasoning, test.want)
			}
		})
	}
}

package dialect

import (
	"net/http"
	"reflect"
	"testing"
)

func TestResponseModelInspectorsObserveProtocolFields(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		payload string
		want    []string
	}{
		{
			name:    "openai chat completion",
			dialect: NewOpenAI(http.DefaultClient),
			payload: `{"id":"chatcmpl-1","model":"gpt-4.1"}`,
			want:    []string{"gpt-4.1"},
		},
		{
			name:    "openai responses object",
			dialect: NewOpenAIResponses(http.DefaultClient),
			payload: `{"id":"resp-1","model":"gpt-5"}`,
			want:    []string{"gpt-5"},
		},
		{
			name:    "openai responses stream event",
			dialect: NewOpenAIResponses(http.DefaultClient),
			payload: `{"type":"response.completed","response":{"id":"resp-1","model":"gpt-5.1"}}`,
			want:    []string{"gpt-5.1"},
		},
		{
			name:    "openai responses preserves conflicting declarations",
			dialect: NewOpenAIResponses(http.DefaultClient),
			payload: `{"model":"gpt-5","response":{"model":"gpt-5.1"}}`,
			want:    []string{"gpt-5", "gpt-5.1"},
		},
		{
			name:    "anthropic message",
			dialect: NewAnthropic(http.DefaultClient),
			payload: `{"type":"message","model":"claude-sonnet-4"}`,
			want:    []string{"claude-sonnet-4"},
		},
		{
			name:    "anthropic message start",
			dialect: NewAnthropic(http.DefaultClient),
			payload: `{"type":"message_start","message":{"model":"claude-sonnet-4-20250514"}}`,
			want:    []string{"claude-sonnet-4-20250514"},
		},
		{
			name:    "gemini generate content",
			dialect: NewGemini(http.DefaultClient),
			payload: `{"modelVersion":"gemini-2.5-pro"}`,
			want:    []string{"gemini-2.5-pro"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspector, ok := test.dialect.(interface {
				InspectResponseModels([]byte) []string
			})
			if !ok {
				t.Fatalf("%T does not expose response model inspection", test.dialect)
			}
			if got := inspector.InspectResponseModels([]byte(test.payload)); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("InspectResponseModels() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResponseModelInspectorsIgnoreMissingInvalidAndEmptyModels(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		payload string
	}{
		{name: "openai missing", dialect: NewOpenAI(http.DefaultClient), payload: `{"id":"chatcmpl-1"}`},
		{name: "openai whitespace", dialect: NewOpenAI(http.DefaultClient), payload: `{"model":"  \t\n"}`},
		{name: "responses null", dialect: NewOpenAIResponses(http.DefaultClient), payload: `{"model":null}`},
		{name: "anthropic empty", dialect: NewAnthropic(http.DefaultClient), payload: `{"model":""}`},
		{name: "gemini wrong type", dialect: NewGemini(http.DefaultClient), payload: `{"modelVersion":42}`},
		{name: "gemini whitespace", dialect: NewGemini(http.DefaultClient), payload: `{"modelVersion":"　"}`},
		{name: "malformed", dialect: NewOpenAI(http.DefaultClient), payload: `{"model":`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspector, ok := test.dialect.(interface {
				InspectResponseModels([]byte) []string
			})
			if !ok {
				t.Fatalf("%T does not expose response model inspection", test.dialect)
			}
			if got := inspector.InspectResponseModels([]byte(test.payload)); len(got) != 0 {
				t.Fatalf("InspectResponseModels() = %#v, want no observation", got)
			}
		})
	}
}

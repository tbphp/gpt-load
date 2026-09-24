package gateway

import (
	"strconv"
	"strings"
	"testing"

	"gpt-load/internal/protocol"
)

func TestRequestDeclaresJSONOutput(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol protocol.Protocol
		body     string
		want     bool
	}{
		{"Chat JSON schema", protocol.OpenAICompletions, `{"response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object"}}}}`, true},
		{"Chat JSON object", protocol.OpenAICompletions, `{"response_format":{"type":"json_object"}}`, true},
		{"Chat text", protocol.OpenAICompletions, `{"response_format":{"type":"text"},"messages":[{"content":"{\"value\":\"looks like JSON\"}"}]}`, false},
		{"Responses JSON schema", protocol.OpenAIResponses, `{"text":{"format":{"type":"json_schema","schema":{"type":"object"}}}}`, true},
		{"Responses JSON object", protocol.OpenAIResponses, `{"text":{"format":{"type":"json_object"}}}`, true},
		{"Responses plain text", protocol.OpenAIResponses, `{"text":{"format":{"type":"text"}},"input":"Return JSON"}`, false},
		{"Anthropic JSON schema", protocol.Anthropic, `{"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}}}`, true},
		{"Anthropic other format", protocol.Anthropic, `{"output_config":{"format":{"type":"text"}},"messages":[{"content":"{\"a\":1}"}]}`, false},
		{"Gemini JSON MIME", protocol.Gemini, `{"generationConfig":{"responseMimeType":"application/json"}}`, true},
		{"Gemini JSON schema", protocol.Gemini, `{"generationConfig":{"responseSchema":{"type":"OBJECT"}}}`, true},
		{"Gemini text MIME", protocol.Gemini, `{"generationConfig":{"responseMimeType":"text/plain"},"contents":[{"parts":[{"text":"{\"a\":1}"}]}]}`, false},
		{"Gemini null schema", protocol.Gemini, `{"generationConfig":{"responseSchema":null}}`, false},
		{"wrong protocol field", protocol.OpenAIResponses, `{"response_format":{"type":"json_schema"}}`, false},
		{"JSON-looking plain content", protocol.OpenAICompletions, `{"messages":[{"role":"user","content":"{\"response_format\":{\"type\":\"json_schema\"}}"}]}`, false},
		{"malformed request", protocol.OpenAICompletions, `{"response_format":{"type":"json_schema"}`, false},
		{"non-object request", protocol.OpenAICompletions, `[{"response_format":{"type":"json_schema"}}]`, false},
		{"unsupported protocol", protocol.OpenAIImages, `{"response_format":{"type":"json_schema"}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := requestDeclaresJSONOutput(test.protocol, []byte(test.body)); got != test.want {
				t.Fatalf("requestDeclaresJSONOutput() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRequestDeclaresJSONOutputIgnoresLargeMessageContent(t *testing.T) {
	message := strings.Repeat(`{"response_format":{"type":"json_schema"}}`, 1<<14)
	for _, test := range []struct {
		name   string
		format string
		want   bool
	}{
		{"explicit format", `"response_format":{"type":"json_schema"},`, true},
		{"message content only", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{` + test.format + `"messages":[{"content":` + strconv.Quote(message) + `}]}`)
			if got := requestDeclaresJSONOutput(protocol.OpenAICompletions, body); got != test.want {
				t.Fatalf("requestDeclaresJSONOutput() = %t, want %t", got, test.want)
			}
		})
	}
}

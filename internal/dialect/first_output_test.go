package dialect

import (
	"testing"

	"gpt-load/internal/protocol"
)

func TestFirstOutputIgnoresMetadataAndCountsGeneratedContent(t *testing.T) {
	tests := []struct {
		protocol protocol.Protocol
		payload  string
		want     bool
	}{
		{protocol.OpenAICompletions, `{"choices":[{"delta":{"role":"assistant","content":""}}]}`, false},
		{protocol.OpenAICompletions, `{"choices":[{"delta":{"content":"hello"}}]}`, true},
		{protocol.OpenAICompletions, `{"choices":[{"delta":{"reasoning_content":"think"}}]}`, true},
		{protocol.OpenAICompletions, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":"f","arguments":""}}]}}]}`, false},
		{protocol.OpenAICompletions, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`, true},
		{protocol.OpenAICompletions, `{"choices":[],"usage":{"completion_tokens":12}}`, false},
		{protocol.OpenAICompletions, `{"choices":[{"delta":{"extra_content":{"signature":"opaque"}}}]}`, false},
		{protocol.Anthropic, `{"type":"message_start","message":{"role":"assistant"}}`, false},
		{protocol.Anthropic, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`, true},
		{protocol.Anthropic, `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"think"}}`, true},
		{protocol.Anthropic, `{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{}"}}`, true},
		{protocol.Anthropic, `{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"opaque"}}`, false},
		{protocol.Anthropic, `{"type":"content_block_start","content_block":{"type":"tool_use","id":"call","name":"f","input":{}}}`, false},
		{protocol.Anthropic, `{"type":"content_block_start","content_block":{"type":"text","text":"hello"}}`, true},
		{protocol.Gemini, `{"candidates":[{"content":{"parts":[{"thought":true,"text":"think"}]}}]}`, true},
		{protocol.Gemini, `{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`, true},
		{protocol.Gemini, `{"candidates":[{"content":{"parts":[{"thoughtSignature":"opaque"}]}}]}`, false},
		{protocol.Gemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{"x":1}}}]}}]}`, true},
		{protocol.Gemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{}}}]}}]}`, true},
		{protocol.Gemini, `{"usageMetadata":{"candidatesTokenCount":12}}`, false},
		{protocol.OpenAIResponses, `{"type":"response.created","response":{}}`, false},
		{protocol.OpenAIResponses, `{"type":"response.output_text.delta","delta":"hello"}`, true},
		{protocol.OpenAIResponses, `{"type":"response.reasoning_summary_text.delta","delta":"think"}`, true},
		{protocol.OpenAIResponses, `{"type":"response.function_call_arguments.delta","delta":"{}"}`, true},
		{protocol.OpenAIResponses, `{"type":"response.custom_tool_call_input.delta","delta":"print(1)"}`, true},
		{protocol.OpenAIResponses, `{"type":"response.output_item.added","item":{"type":"function_call","name":"f","arguments":""}}`, false},
		{protocol.OpenAIResponses, `{"type":"response.reasoning_signature.delta","delta":"opaque"}`, false},
		{protocol.OpenAIResponses, `{"type":"error","error":{"message":"failed"}}`, false},
		{protocol.OpenAIResponses, `{"type":"response.done","response":{"status":"failed","output":[{"type":"message","content":[{"text":"error"}]}]}}`, false},
		{protocol.OpenAIImages, `{"type":"response.output_text.delta","delta":"hello"}`, false},
		{protocol.OpenAICompletions, `[DONE]`, false},
		{protocol.OpenAIResponses, `{broken`, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.protocol)+"/"+tt.payload, func(t *testing.T) {
			observer := FirstOutputObserver{Protocol: tt.protocol}
			if got := observer.Observe(StreamEvent{Payload: []byte(tt.payload)}); got != tt.want {
				t.Fatalf("output=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestFirstOutputDoesNotCountResponseSnapshotsTwice(t *testing.T) {
	observer := FirstOutputObserver{Protocol: protocol.OpenAIResponses}
	if !observer.Observe(StreamEvent{Payload: []byte(`{"type":"response.output_text.delta","delta":"hello"}`)}) {
		t.Fatal("text delta missed")
	}
	for _, payload := range []string{
		`{"type":"response.output_text.done","text":"hello"}`,
		`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"hello"}]}}`,
		`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}}`,
		`{"type":"response.output_text.delta","delta":"late"}`,
	} {
		if observer.Observe(StreamEvent{Payload: []byte(payload)}) {
			t.Fatalf("duplicate or late content counted: %s", payload)
		}
	}
	observer = FirstOutputObserver{Protocol: protocol.OpenAIResponses}
	if !observer.Observe(StreamEvent{Payload: []byte(`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"all at once"}]}]}}`)}) {
		t.Fatal("single completed response has no first output")
	}
}

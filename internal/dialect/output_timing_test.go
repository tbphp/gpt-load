package dialect

import (
	"fmt"
	"strings"
	"testing"

	"gpt-load/internal/protocol"
)

func TestOutputTimingIgnoresMetadataAndCountsGeneratedContent(t *testing.T) {
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
			observer := OutputTimingObserver{Protocol: tt.protocol}
			if got := observer.Observe(StreamEvent{Payload: []byte(tt.payload)}); got != tt.want {
				t.Fatalf("output=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestOutputTimingDoesNotCountResponseSnapshotsTwice(t *testing.T) {
	observer := OutputTimingObserver{Protocol: protocol.OpenAIResponses}
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
	observer = OutputTimingObserver{Protocol: protocol.OpenAIResponses}
	if !observer.Observe(StreamEvent{Payload: []byte(`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"all at once"}]}]}}`)}) {
		t.Fatal("single completed response has no first output")
	}
}

func TestOutputTimingContinuesAcrossChoicesAndOutputItems(t *testing.T) {
	for _, value := range []protocol.Protocol{protocol.OpenAICompletions, protocol.Gemini} {
		observer := OutputTimingObserver{Protocol: value}
		for _, index := range []int{0, 1} {
			var payload string
			if value == protocol.OpenAICompletions {
				payload = fmt.Sprintf(`{"choices":[{"index":%d,"delta":{"content":"text"},"finish_reason":"stop"}]}`, index)
			} else {
				payload = fmt.Sprintf(`{"candidates":[{"index":%d,"content":{"parts":[{"text":"text"}]},"finishReason":"STOP"}]}`, index)
			}
			if !observer.Observe(StreamEvent{Payload: []byte(payload)}) {
				t.Fatalf("later choice output lost: %s", payload)
			}
		}
	}
	observer := OutputTimingObserver{Protocol: protocol.OpenAIResponses}
	for _, payload := range []string{
		`{"type":"response.output_text.delta","output_index":0,"delta":"hello"}`,
		`{"type":"response.function_call_arguments.done","output_index":1,"arguments":"{}"}`,
	} {
		if !observer.Observe(StreamEvent{Payload: []byte(payload)}) {
			t.Fatalf("new output item lost: %s", payload)
		}
	}
	if observer.Observe(StreamEvent{Payload: []byte(`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","arguments":"{}"}}`)}) {
		t.Fatal("tool arguments counted twice")
	}
}

func TestOutputTimingResponsesTracksDistinctParts(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		want   []bool
	}{
		{
			name: "text snapshots",
			events: []string{
				`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"first"}`,
				`{"type":"response.output_text.done","output_index":0,"content_index":1,"text":"second"}`,
				`{"type":"response.content_part.done","output_index":0,"content_index":1,"part":{"type":"output_text","text":"second"}}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"content":[{"text":"first"},{"text":"second"}]}}`,
				`{"type":"response.completed","response":{"output":[{"content":[{"text":"first"},{"text":"second"}]}]}}`,
			},
			want: []bool{true, true, false, false, false},
		},
		{
			name: "refusal snapshots",
			events: []string{
				`{"type":"response.refusal.done","output_index":0,"content_index":0,"refusal":"declined"}`,
				`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":"declined"}}`,
				`{"type":"response.content_part.done","output_index":0,"content_index":1,"part":{"type":"refusal","refusal":"another refusal"}}`,
			},
			want: []bool{true, false, true},
		},
		{
			name: "content part snapshots",
			events: []string{
				`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"first"}}`,
				`{"type":"response.content_part.done","output_index":0,"content_index":1,"part":{"type":"output_text","text":"second"}}`,
				`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"first"}`,
			},
			want: []bool{true, true, false},
		},
		{
			name: "reasoning summary snapshots",
			events: []string{
				`{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":"first"}}`,
				`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":1,"text":"second"}`,
				`{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":1,"part":{"type":"summary_text","text":"second"}}`,
				`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"reasoning"}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"summary":[{"text":"first"},{"text":"second"}]}}`,
			},
			want: []bool{true, true, false, true, false},
		},
		{
			name: "deltas and snapshots",
			events: []string{
				`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"first"}`,
				`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":" more"}`,
				`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"first more"}`,
				`{"type":"response.output_text.done","output_index":0,"content_index":1,"text":"second"}`,
			},
			want: []bool{true, true, false, true},
		},
		{
			name: "item identifier fallback",
			events: []string{
				`{"type":"response.output_text.done","item_id":"msg_1","content_index":0,"text":"first"}`,
				`{"type":"response.output_text.done","item_id":"msg_1","content_index":1,"text":"second"}`,
				`{"type":"response.output_item.done","item":{"id":"msg_1","content":[{"text":"first"},{"text":"second"}]}}`,
			},
			want: []bool{true, true, false},
		},
		{
			name: "initial item snapshot",
			events: []string{
				`{"type":"response.output_item.added","output_index":0,"item":{"content":[{"text":"initial"}]}}`,
				`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"initial"}`,
				`{"type":"response.content_part.added","output_index":0,"content_index":1,"part":{"type":"output_text","text":"next"}}`,
				`{"type":"response.content_part.done","output_index":0,"content_index":1,"part":{"type":"output_text","text":"next"}}`,
			},
			want: []bool{true, false, true, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := OutputTimingObserver{Protocol: protocol.OpenAIResponses}
			for i, payload := range tt.events {
				if got := observer.Observe(StreamEvent{Payload: []byte(payload)}); got != tt.want[i] {
					t.Errorf("event %d output=%v, want %v: %s", i, got, tt.want[i], payload)
				}
			}
		})
	}
}

func TestOutputTimingResponsesStopsAtRetentionLimits(t *testing.T) {
	tests := []struct {
		name    string
		payload func(int) string
		count   int
	}{
		{"identifier length", func(i int) string {
			return fmt.Sprintf(`{"type":"response.output_text.delta","item_id":%q,"delta":"x"}`, strings.Repeat("x", maxOutputTimingKeyBytes+1))
		}, 1},
		{"identifier count", func(i int) string {
			return fmt.Sprintf(`{"type":"response.output_text.delta","output_index":%d,"delta":"x"}`, i)
		}, maxOutputTimingKeys + 1},
		{"part count", func(i int) string {
			return fmt.Sprintf(`{"type":"response.output_text.done","output_index":0,"content_index":%d,"text":"x"}`, i)
		}, maxOutputTimingKeys + 1},
		{"snapshot part count", func(i int) string {
			parts := strings.TrimSuffix(strings.Repeat(`{"text":"x"},`, maxOutputTimingKeys), ",")
			return fmt.Sprintf(`{"type":"response.output_item.added","output_index":0,"item":{"content":[%s]}}`, parts)
		}, 1},
		{"retained bytes", func(i int) string {
			return fmt.Sprintf(`{"type":"response.output_text.delta","item_id":%q,"delta":"x"}`, fmt.Sprintf("%d_%s", i, strings.Repeat("x", 1024)))
		}, 1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := OutputTimingObserver{Protocol: protocol.OpenAIResponses}
			stopped := false
			for i := range tt.count {
				if !observer.Observe(StreamEvent{Payload: []byte(tt.payload(i))}) {
					stopped = true
					break
				}
			}
			if !stopped {
				t.Fatal("observation kept retaining unbounded timing keys")
			}
			if !observer.Overflowed() || observer.items != nil || observer.keyBytes != 0 {
				t.Fatal("discarded observation retained identifiers")
			}
			for _, payload := range []string{
				`{"type":"response.output_text.delta","output_index":0,"delta":"late"}`,
				`{"type":"response.completed","response":{"output":[{"content":[{"text":"snapshot"}]}]}}`,
			} {
				if observer.Observe(StreamEvent{Payload: []byte(payload)}) {
					t.Fatal("discarded observation resumed sampling")
				}
			}
		})
	}
}

func TestOutputTimingResponsesDoesNotLimitRepeatedDeltas(t *testing.T) {
	observer := OutputTimingObserver{Protocol: protocol.OpenAIResponses}
	event := StreamEvent{Payload: []byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"x"}`)}
	for range maxOutputTimingKeys + 1 {
		if !observer.Observe(event) {
			t.Fatal("repeated delta consumed the distinct-key budget")
		}
	}
	if observer.Overflowed() {
		t.Fatal("normal stream exceeded the retention budget")
	}
}

package dialect

import "testing"

// TestStreamEventsReportProducedContent 固定各方言对「该事件是否产出了可见内容」
// 的判定。空回检测依赖它区分「模型确实在输出」与「流走完但什么都没产出」。
func TestStreamEventsReportProducedContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		classifier StreamContentClassifier
		event      StreamEvent
		want       bool
	}{
		{
			name: "OpenAI text delta", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{"content":"hi"}}]}`)}, want: true,
		},
		{
			name: "OpenAI role preamble", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{"role":"assistant"}}]}`)}, want: false,
		},
		{
			name: "OpenAI empty content delta", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{"content":""}}]}`)}, want: false,
		},
		{
			name: "OpenAI tool call delta", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup"}}]}}]}`,
			)}, want: true,
		},
		{
			name: "OpenAI reasoning delta", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{"reasoning_content":"think"}}]}`)}, want: true,
		},
		{
			name: "OpenAI finish reason only", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)}, want: false,
		},
		{
			name: "OpenAI done", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte("[DONE]")}, want: false,
		},
		{
			name: "Anthropic message start", classifier: NewAnthropic(),
			event: StreamEvent{Name: "message_start", Payload: []byte(
				`{"type":"message_start","message":{"content":[]}}`,
			)}, want: false,
		},
		{
			name: "Anthropic ping", classifier: NewAnthropic(),
			event: StreamEvent{Name: "ping", Payload: []byte(`{"type":"ping"}`)}, want: false,
		},
		{
			name: "Anthropic empty text block start", classifier: NewAnthropic(),
			event: StreamEvent{Name: "content_block_start", Payload: []byte(
				`{"type":"content_block_start","content_block":{"type":"text","text":""}}`,
			)}, want: false,
		},
		{
			name: "Anthropic tool use block start", classifier: NewAnthropic(),
			event: StreamEvent{Name: "content_block_start", Payload: []byte(
				`{"type":"content_block_start","content_block":{"type":"tool_use","name":"lookup"}}`,
			)}, want: true,
		},
		{
			name: "Anthropic text delta", classifier: NewAnthropic(),
			event: StreamEvent{Name: "content_block_delta", Payload: []byte(
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`,
			)}, want: true,
		},
		{
			name: "Anthropic thinking delta", classifier: NewAnthropic(),
			event: StreamEvent{Name: "content_block_delta", Payload: []byte(
				`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"reason"}}`,
			)}, want: true,
		},
		{
			name: "Anthropic tool input delta", classifier: NewAnthropic(),
			event: StreamEvent{Name: "content_block_delta", Payload: []byte(
				`{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
			)}, want: true,
		},
		{
			name: "Anthropic empty text delta", classifier: NewAnthropic(),
			event: StreamEvent{Name: "content_block_delta", Payload: []byte(
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}`,
			)}, want: false,
		},
		{
			name: "Anthropic message stop", classifier: NewAnthropic(),
			event: StreamEvent{Name: "message_stop", Payload: []byte(`{"type":"message_stop"}`)}, want: false,
		},
		{
			name: "Gemini text part", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(
				`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`,
			)}, want: true,
		},
		{
			name: "Gemini function call part", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(
				`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup"}}]}}]}`,
			)}, want: true,
		},
		{
			name: "Gemini empty text part", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(
				`{"candidates":[{"content":{"parts":[{"text":""}]}}]}`,
			)}, want: false,
		},
		{
			name: "Gemini safety block", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(
				`{"candidates":[{"finishReason":"SAFETY"}]}`,
			)}, want: false,
		},
		{
			name: "Gemini prompt blocked", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(
				`{"promptFeedback":{"blockReason":"SAFETY"}}`,
			)}, want: false,
		},
		{
			name: "Responses created", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.created", Payload: []byte(
				`{"type":"response.created","response":{"id":"resp_1"}}`,
			)}, want: false,
		},
		{
			name: "Responses in progress", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.in_progress", Payload: []byte(
				`{"type":"response.in_progress","response":{"id":"resp_1"}}`,
			)}, want: false,
		},
		{
			name: "Responses output text delta", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.output_text.delta", Payload: []byte(
				`{"type":"response.output_text.delta","delta":"hi"}`,
			)}, want: true,
		},
		{
			name: "Responses empty output text delta", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.output_text.delta", Payload: []byte(
				`{"type":"response.output_text.delta","delta":""}`,
			)}, want: false,
		},
		{
			name: "Responses function call arguments delta", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.function_call_arguments.delta", Payload: []byte(
				`{"type":"response.function_call_arguments.delta","delta":"{\"a\":"}`,
			)}, want: true,
		},
		{
			name: "Responses reasoning summary delta", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.reasoning_summary_text.delta", Payload: []byte(
				`{"type":"response.reasoning_summary_text.delta","delta":"think"}`,
			)}, want: true,
		},
		{
			name: "Responses function call item added", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.output_item.added", Payload: []byte(
				`{"type":"response.output_item.added","item":{"type":"function_call","name":"lookup"}}`,
			)}, want: true,
		},
		{
			name: "Responses reasoning item added", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.output_item.added", Payload: []byte(
				`{"type":"response.output_item.added","item":{"type":"reasoning"}}`,
			)}, want: false,
		},
		{
			name: "Responses completed", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.completed", Payload: []byte(
				`{"type":"response.completed","response":{"id":"resp_1"}}`,
			)}, want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.classifier.ClassifyStreamContent(test.event).Produced; got != test.want {
				t.Fatalf("Produced = %v, want %v", got, test.want)
			}
		})
	}
}

// TestStreamEventsReportContentRobustly 固定容易误判的产出形态：兼容渠道的
// 非标准增量字段、Anthropic 脱敏推理块，以及 Responses 的空消息条目。
func TestStreamEventsReportContentRobustly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		classifier StreamContentClassifier
		event      StreamEvent
		want       bool
	}{
		{
			name: "OpenAI compatible images delta", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(
				`{"choices":[{"delta":{"images":[{"type":"image_url"}]}}]}`,
			)}, want: true,
		},
		{
			name: "OpenAI unknown non-role field", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(
				`{"choices":[{"delta":{"annotations":[{"type":"url_citation"}]}}]}`,
			)}, want: true,
		},
		{
			name: "OpenAI role with null content", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(
				`{"choices":[{"delta":{"role":"assistant","content":null,"tool_calls":null}}]}`,
			)}, want: false,
		},
		{
			name: "Anthropic redacted thinking block", classifier: NewAnthropic(),
			event: StreamEvent{Name: "content_block_start", Payload: []byte(
				`{"type":"content_block_start","content_block":{"type":"redacted_thinking","data":"opaque"}}`,
			)}, want: true,
		},
		{
			name: "Responses empty message item added", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.output_item.added", Payload: []byte(
				`{"type":"response.output_item.added","item":{"type":"message","role":"assistant","content":[]}}`,
			)}, want: false,
		},
		{
			name: "Responses empty message item done", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.output_item.done", Payload: []byte(
				`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":""}]}}`,
			)}, want: false,
		},
		{
			name: "Responses message item done with text", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.output_item.done", Payload: []byte(
				`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"hi"}]}}`,
			)}, want: true,
		},
		{
			name: "Responses message item done with refusal", classifier: NewOpenAIResponses(),
			event: StreamEvent{Name: "response.output_item.done", Payload: []byte(
				`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"refusal","refusal":"no"}]}}`,
			)}, want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.classifier.ClassifyStreamContent(test.event).Produced; got != test.want {
				t.Fatalf("Produced = %v, want %v", got, test.want)
			}
		})
	}
}

// TestStreamEventsReportExplainedStop 固定「自然结束之外」的终止原因。只有
// 自然结束却没有产出才是值得重试的空回；预算耗尽、过滤、拒答都是确定性结果。
func TestStreamEventsReportExplainedStop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		classifier StreamContentClassifier
		event      StreamEvent
		want       bool
	}{
		{
			name: "OpenAI natural stop", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)}, want: false,
		},
		{
			name: "OpenAI no finish reason", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{"role":"assistant"}}]}`)}, want: false,
		},
		{
			name: "OpenAI length", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{},"finish_reason":"length"}]}`)}, want: true,
		},
		{
			name: "OpenAI content filter", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"delta":{},"finish_reason":"content_filter"}]}`)}, want: true,
		},
		{
			name: "Anthropic end turn", classifier: NewAnthropic(),
			event: StreamEvent{Name: "message_delta", Payload: []byte(
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			)}, want: false,
		},
		{
			name: "Anthropic stop sequence", classifier: NewAnthropic(),
			event: StreamEvent{Name: "message_delta", Payload: []byte(
				`{"type":"message_delta","delta":{"stop_reason":"stop_sequence"}}`,
			)}, want: false,
		},
		{
			name: "Anthropic max tokens", classifier: NewAnthropic(),
			event: StreamEvent{Name: "message_delta", Payload: []byte(
				`{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`,
			)}, want: true,
		},
		{
			name: "Anthropic refusal", classifier: NewAnthropic(),
			event: StreamEvent{Name: "message_delta", Payload: []byte(
				`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`,
			)}, want: true,
		},
		{
			name: "Gemini natural stop", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(`{"candidates":[{"finishReason":"STOP"}]}`)}, want: false,
		},
		{
			name: "Gemini max tokens", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(`{"candidates":[{"finishReason":"MAX_TOKENS"}]}`)}, want: true,
		},
		{
			name: "Gemini safety", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(`{"candidates":[{"finishReason":"SAFETY"}]}`)}, want: true,
		},
		{
			name: "Gemini prompt blocked", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`)}, want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.classifier.ClassifyStreamContent(test.event).ExplainedStop; got != test.want {
				t.Fatalf("ExplainedStop = %v, want %v", got, test.want)
			}
		})
	}
}

// TestOpenAIClassifierToleratesIrregularChoices 保证产出判定不会收紧协议校验。
// 生命周期分类对所有流生效（与空回开关无关）：兼容渠道输出不规范的 choices 时，
// 流必须照常转发；产出判定则按有产出处理，不把它误判为空回。
func TestOpenAIClassifierToleratesIrregularChoices(t *testing.T) {
	t.Parallel()

	payloads := []string{
		`{"choices":{"unexpected":true}}`,
		`{"choices":[{"delta":"text"}]}`,
		`{"choices":[{"delta":{"content":"hi"},"finish_reason":1}]}`,
		`{"choices":"none"}`,
	}
	for _, payload := range payloads {
		t.Run(payload, func(t *testing.T) {
			t.Parallel()
			got, err := NewOpenAI().ClassifyStreamEvent(StreamEvent{Payload: []byte(payload)})
			if err != nil {
				t.Fatalf("ClassifyStreamEvent() error = %v, want tolerated", err)
			}
			if got.Disposition != StreamEventContinue {
				t.Fatalf("Disposition = %v, want continue", got.Disposition)
			}
			if !NewOpenAI().ClassifyStreamContent(StreamEvent{Payload: []byte(payload)}).Produced {
				t.Fatal("Produced = false, want unrecognized choices treated as content")
			}
		})
	}
}

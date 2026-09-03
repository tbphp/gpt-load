package dialect

import "testing"

func TestStreamingDialectsClassifyTerminalAndErrorEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		classifier    StreamEventClassifier
		event         StreamEvent
		want          StreamEventDisposition
		wantAuxiliary bool
	}{
		{
			name: "OpenAI done", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte("[DONE]")}, want: StreamEventCompleted,
		},
		{
			name: "OpenAI finish reason precedes usage trailer and done", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[{"finish_reason":"stop"}]}`)}, want: StreamEventContinue,
		},
		{
			name: "OpenAI error", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"error":{"message":"failed"}}`)}, want: StreamEventFailed,
		},
		{
			name: "OpenAI empty choices metadata", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":[],"cost":"0"}`)},
			want:  StreamEventContinue, wantAuxiliary: true,
		},
		{
			name: "OpenAI null choices remains ordinary data", classifier: NewOpenAI(),
			event: StreamEvent{Payload: []byte(`{"choices":null,"cost":"0"}`)},
			want:  StreamEventContinue,
		},
		{
			name: "Anthropic stop", classifier: NewAnthropic(),
			event: StreamEvent{Name: "message_stop", Payload: []byte(`{"type":"message_stop"}`)}, want: StreamEventCompleted,
		},
		{
			name: "Anthropic error", classifier: NewAnthropic(),
			event: StreamEvent{Name: "error", Payload: []byte(`{"type":"error","error":{"message":"failed"}}`)}, want: StreamEventFailed,
		},
		{
			name: "Gemini finish reason", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(`{"candidates":[{"finishReason":"STOP"}]}`)}, want: StreamEventCompleted,
		},
		{
			name: "Gemini prompt blocked", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`)}, want: StreamEventCompleted,
		},
		{
			name: "Gemini error", classifier: NewGemini(),
			event: StreamEvent{Payload: []byte(`{"error":{"message":"failed"}}`)}, want: StreamEventFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !test.classifier.RequiresTerminalEvent() {
				t.Fatal("stream must require a terminal event")
			}
			got, err := test.classifier.ClassifyStreamEvent(test.event)
			if err != nil {
				t.Fatalf("ClassifyStreamEvent() error = %v", err)
			}
			if got.Disposition != test.want {
				t.Fatalf("Disposition = %v, want %v", got.Disposition, test.want)
			}
			if got.IsAuxiliary() != test.wantAuxiliary {
				t.Fatalf("IsAuxiliary() = %t, want %t", got.IsAuxiliary(), test.wantAuxiliary)
			}
		})
	}
}

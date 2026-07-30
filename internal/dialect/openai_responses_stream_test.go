package dialect

import (
	"net/http"
	"testing"
)

func TestOpenAIResponsesClassifiesStreamEvents(t *testing.T) {
	t.Parallel()

	classifier, ok := any(NewOpenAIResponses(http.DefaultClient)).(StreamEventClassifier)
	if !ok {
		t.Fatal("OpenAI Responses does not expose StreamEventClassifier")
	}
	if !classifier.RequiresTerminalEvent() {
		t.Fatal("OpenAI Responses stream must require a terminal event")
	}

	tests := []struct {
		name  string
		event StreamEvent
		want  StreamEventDisposition
	}{
		{
			name: "matching explicit and payload completed",
			event: StreamEvent{
				Name:    "response.completed",
				Payload: []byte(`{"type":"response.completed","response":{"id":"resp_1"}}`),
			},
			want: StreamEventCompleted,
		},
		{
			name: "explicit incomplete without payload type",
			event: StreamEvent{
				Name:    "response.incomplete",
				Payload: []byte(`{"response":{"id":"resp_1"}}`),
			},
			want: StreamEventIncomplete,
		},
		{
			name: "payload failed without explicit event",
			event: StreamEvent{
				Payload: []byte(`{"type":"response.failed","response":{"error":{"code":"server_error"}}}`),
			},
			want: StreamEventFailed,
		},
		{
			name: "generic error event",
			event: StreamEvent{
				Name:    "error",
				Payload: []byte(`{"error":{"message":"failed"}}`),
			},
			want: StreamEventFailed,
		},
		{
			name: "top level error without event name",
			event: StreamEvent{
				Payload: []byte(`{"error":{"message":"failed"}}`),
			},
			want: StreamEventFailed,
		},
		{
			name: "unknown event remains pass through",
			event: StreamEvent{
				Name:    "response.output_text.delta",
				Payload: []byte(`{"type":"response.output_text.delta","delta":"ok"}`),
			},
			want: StreamEventContinue,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := classifier.ClassifyStreamEvent(test.event)
			if err != nil {
				t.Fatalf("ClassifyStreamEvent() error = %v", err)
			}
			if got.Disposition != test.want {
				t.Fatalf("Disposition = %v, want %v", got.Disposition, test.want)
			}
		})
	}
}

func TestOpenAIResponsesRejectsConflictingStreamEventNames(t *testing.T) {
	t.Parallel()

	classifier := any(NewOpenAIResponses(http.DefaultClient)).(StreamEventClassifier)
	_, err := classifier.ClassifyStreamEvent(StreamEvent{
		Name:    "response.completed",
		Payload: []byte(`{"type":"response.failed","response":{"error":{"code":"server_error"}}}`),
	})
	if err == nil {
		t.Fatal("ClassifyStreamEvent() error = nil, want event-name conflict")
	}
}

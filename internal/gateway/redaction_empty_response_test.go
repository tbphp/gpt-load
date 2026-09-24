package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/execution"
)

func TestRedactionEmptyResponsePreread(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("synthetic-business-value")
	if err != nil {
		t.Fatal(err)
	}
	preamble := "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"
	for _, tc := range []struct {
		name, content, ending string
		empty                 bool
	}{
		{"empty LF", "", "data: [DONE]\n\n", true},
		{"empty CR", "", "data: [DONE]\r\r", true},
		{"protected content", token, "data: [DONE]\r\r", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := []string{preamble}
			if tc.content != "" {
				chunks = append(chunks, string(redactionBoundaryChat(t, map[string]any{"content": tc.content}, "stop")))
			}
			chunks = append(chunks, tc.ending)
			executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
				header := http.Header{"Content-Type": {"text/event-stream"}}
				if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: header}); err != nil {
					t.Fatal(err)
				}
				for i, chunk := range chunks {
					if err := sink(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: []byte(chunk)}); err != nil {
						t.Fatal(err)
					}
				}
				return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: header}
			}}
			input := executionForwardInput()
			input.EmptyResponseRetry, input.RedactionCipher = true, cipher
			recorder := httptest.NewRecorder()
			result := NewExecutionForwarder(executor).ForwardStream(context.Background(), input, recorder)
			if result.EmptyResponseBeforeCommit != tc.empty || result.Committed == tc.empty {
				t.Fatalf("empty=%v committed=%v; want empty=%v", result.EmptyResponseBeforeCommit, result.Committed, tc.empty)
			}
			got := recorder.Body.String()
			if tc.empty {
				got = string(result.Body)
				if recorder.Body.Len() != 0 {
					t.Fatal("empty response was committed")
				}
			}
			want := strings.ReplaceAll(strings.Join(chunks, ""), token, "synthetic-business-value")
			if got != want {
				t.Fatalf("event order changed: got %q want %q", got, want)
			}
		})
	}
}

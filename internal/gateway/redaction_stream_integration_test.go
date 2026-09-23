package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/testutil/encryptiontest"
)

func TestExecutionForwarderRestoresSplitChatSSEWithoutChangingEventOrder(t *testing.T) {
	service := encryptiontest.Service(t, "redaction-sse-integration-test")
	cipher, err := service.NewRedactionCipher(7)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	first := fmt.Sprintf("data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Send to %s\"},\"finish_reason\":null}]}\n\n", token[:len(token)/2])
	second := fmt.Sprintf("data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%s\"},\"finish_reason\":\"stop\"}]}\n\n", token[len(token)/2:])
	executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
		if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady,
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
			t.Fatal(err)
		}
		for index, chunk := range []string{first, second, "data: [DONE]\n\n"} {
			if err := sink(execution.StreamEvent{Sequence: uint64(index + 2), Kind: execution.StreamEventData, Data: []byte(chunk)}); err != nil {
				t.Fatal(err)
			}
		}
		return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}
	}}
	input := executionForwardInput()
	input.RedactionCipher = cipher
	recorder := httptest.NewRecorder()
	result := NewExecutionForwarder(executor).ForwardStream(context.Background(), input, recorder)
	if result.Err != nil || result.Stream.EndReason != StreamEndCleanEOF || !result.Committed {
		t.Fatalf("forward result: %#v", result)
	}
	body := recorder.Body.String()
	if strings.Contains(body, token) || strings.Contains(body, "gld1_") ||
		!strings.Contains(body, `"content":"Send to "`) ||
		!strings.Contains(body, `"content":"alice@example.com"`) ||
		strings.Count(body, "data: ") != 3 || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream content or order not restored: %q", body)
	}
}

func TestExecutionForwarderAttributesCorruptCiphertextToLocalRestore(t *testing.T) {
	service := encryptiontest.Service(t, "redaction-sse-corruption-test")
	cipher, err := service.NewRedactionCipher(7)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	last := "A"
	if strings.HasSuffix(token, last) {
		last = "B"
	}
	broken := token[:len(token)-1] + last
	chunk := fmt.Sprintf("data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%s\"},\"finish_reason\":\"stop\"}]}\n\n", broken)
	executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
		if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady,
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
			t.Fatal(err)
		}
		if err := sink(execution.StreamEvent{Sequence: 2, Kind: execution.StreamEventData, Data: []byte(chunk)}); !errors.Is(err, errRedactionStream) {
			t.Fatalf("restore error = %v", err)
		}
		return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}
	}}
	input := executionForwardInput()
	input.RedactionCipher = cipher
	recorder := httptest.NewRecorder()
	result := NewExecutionForwarder(executor).ForwardStream(context.Background(), input, recorder)
	if !errors.Is(result.Err, errRedactionStream) || result.Committed || recorder.Body.Len() != 0 ||
		result.ExecutionError == nil || result.ExecutionError.OriginHint != execution.ErrorOriginInternal {
		t.Fatalf("restore failure leaked or misattributed: %#v", result)
	}
	decision := judgeUpstreamResult(result, time.Now(), health.DecisionContext{Method: http.MethodPost})
	if decision.Origin != execution.ErrorOriginInternal || decision.Effect != health.EffectNone {
		t.Fatalf("local restore changed credential health: %#v", decision)
	}
}

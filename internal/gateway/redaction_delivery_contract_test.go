package gateway

import (
	"context"
	"errors"
	"gpt-load/internal/accessquota"
	"gpt-load/internal/execution"
	"gpt-load/internal/usage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRedactionDeliveryContract(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	t.Run("usage-handler-log", func(t *testing.T) {
		executor := fakeExecutionExecutor{unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
			return execution.AttemptResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"choices":[{"message":{"content":"gld1_3_abc"}}]}`), Usage: &execution.UsageEvidence{Normalized: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1000000, Output: 20}}}}
		}}
		sink := &recordingRequestLogSink{}
		engine, h, _, _ := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(executor), &recordingAccessKeyRPMLimiter{}, sink, "sk-synthetic-usage")
		quota := accessquota.NewRuntime()
		if err := quota.Reconcile(map[uint][]accessquota.Rule{1: {{ID: 103, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 10000000000}}}); err != nil {
			t.Fatal(err)
		}
		h.accessQuota = quota
		h.priceTables = &mutableGatewayPriceTableProvider{table: mustGatewayPriceTable(t, 2000000000, false)}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`))
		req.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		events := sink.snapshot()
		if len(events) != 1 {
			t.Fatalf("events=%d", len(events))
		}
		if recorder.Code != 502 || events[0].ErrorCode != "response_redaction_failed" {
			t.Fatal("restore error response changed")
		}
		if events[0].Usage.Result.State != usage.StateComplete || events[0].Usage.Pricing.EstimatedCostNanoUSD <= 0 {
			t.Fatal("paid usage missing from request log")
		}
		view := quota.Snapshot(1, time.Now())
		if len(view.Rules) != 1 || view.Rules[0].UsedNanoUSD != events[0].Usage.Pricing.EstimatedCostNanoUSD {
			t.Fatal("quota settlement differs from request log")
		}
	})
	t.Run("credential-conflict", func(t *testing.T) {
		credential := "synthetic-upstream-credential-12345"
		token, err := c.EncryptToken(credential)
		if err != nil {
			t.Fatal(err)
		}
		in := executionForwardInput()
		in.RedactionCipher = c
		in.APIKey = credential
		body := redactionBoundaryJSON(t, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": token}}}})
		_, err = (&responseProcessor{}).prepareSuccessRepresentation(in, 200, http.Header{}, body, []string{credential})
		if err == nil {
			t.Fatal("unary accepted a credential conflict")
		}
		executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
			if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
				t.Fatal(err)
			}
			for i, part := range []string{token[:12], token[12:]} {
				if err := sink(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: redactionBoundaryChat(t, map[string]any{"content": part}, nil)}); err != nil {
					return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}
				}
			}
			if err := sink(execution.StreamEvent{Sequence: 4, Kind: execution.StreamEventData, Data: []byte("data: [DONE]\n\n")}); err != nil {
				t.Fatal(err)
			}
			return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}
		}}
		recorder := httptest.NewRecorder()
		got := NewExecutionForwarder(executor).ForwardStream(context.Background(), in, recorder)
		if !errors.Is(got.Err, errRedactionStream) || strings.Contains(recorder.Body.String(), credential) {
			t.Fatal("SSE did not reject restored credential")
		}
	})
	t.Run("first-error-CR", func(t *testing.T) {
		for _, eol := range []string{"\n", "\r"} {
			executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
				if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
					t.Fatal(err)
				}
				if err := sink(execution.StreamEvent{Sequence: 2, Kind: execution.StreamEventData, Data: []byte("event: error" + eol + "data: {\"error\":{\"message\":\"synthetic\",\"code\":\"rate_limit_exceeded\"}}" + eol + eol)}); err != nil {
					t.Fatal(err)
				}
				return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}
			}}
			in := executionForwardInput()
			in.RedactionCipher = c
			recorder := httptest.NewRecorder()
			got := NewExecutionForwarder(executor).ForwardStream(context.Background(), in, recorder)
			if got.Committed || !got.ProviderErrorBeforeCommit || recorder.Body.Len() != 0 {
				t.Fatalf("first upstream error was committed for eol=%q", eol)
			}
		}
	})
}

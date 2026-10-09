package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestWebsocketInterruptLogClassification(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		eventType    string
		reason       string
		requested    bool
		delayedWrite bool
		writeErr     error
		executionErr *execution.ErrorEvidence
		wantStatus   telemetry.RequestStatus
	}{
		{name: "requested incomplete", eventType: "response.incomplete", reason: "interrupted", requested: true, wantStatus: telemetry.RequestStatusSuccess},
		{name: "requested done", eventType: "response.done", reason: "interrupted", requested: true, wantStatus: telemetry.RequestStatusSuccess},
		{name: "terminal before write returns", eventType: "response.incomplete", reason: "interrupted", requested: true, delayedWrite: true, wantStatus: telemetry.RequestStatusSuccess},
		{name: "write fails after terminal", eventType: "response.incomplete", reason: "interrupted", requested: true, delayedWrite: true, writeErr: errors.New("interrupt write failed"), wantStatus: telemetry.RequestStatusIncomplete},
		{name: "unsolicited interrupted", eventType: "response.incomplete", reason: "interrupted", wantStatus: telemetry.RequestStatusIncomplete},
		{name: "token limit", eventType: "response.incomplete", reason: "max_output_tokens", wantStatus: telemetry.RequestStatusIncomplete},
		{name: "requested but token limit", eventType: "response.incomplete", reason: "max_output_tokens", requested: true, wantStatus: telemetry.RequestStatusIncomplete},
		{name: "requested but missing reason", eventType: "response.incomplete", requested: true, wantStatus: telemetry.RequestStatusIncomplete},
		{name: "upstream failure after terminal", eventType: "response.incomplete", reason: "interrupted", requested: true, executionErr: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: 502, Code: "upstream_error"}, wantStatus: telemetry.RequestStatusIncomplete},
		{name: "timeout after terminal", eventType: "response.incomplete", reason: "interrupted", requested: true, executionErr: &execution.ErrorEvidence{Kind: execution.ErrorKindTimeout, Code: "timeout"}, wantStatus: telemetry.RequestStatusIncomplete},
		{name: "cancellation after terminal", eventType: "response.incomplete", reason: "interrupted", requested: true, executionErr: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Code: "canceled"}, wantStatus: telemetry.RequestStatusIncomplete},
		{name: "no terminal", eventType: "response.output_text.delta", requested: true, wantStatus: telemetry.RequestStatusIncomplete},
		{name: "failed terminal", eventType: "response.failed", requested: true, wantStatus: telemetry.RequestStatusError},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			releaseWrite := make(chan struct{})
			finishWrite := sync.OnceFunc(func() { close(releaseWrite) })
			session := &interruptibleWebsocketFixture{interrupts: make(chan []byte, 1)}
			session.done = make(chan struct{})
			session.afterInterrupt = func(ctx context.Context) error {
				if scenario.delayedWrite {
					select {
					case <-releaseWrite:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return scenario.writeErr
			}
			status := "incomplete"
			if scenario.eventType == "response.failed" {
				status = "failed"
			}
			terminal := []byte(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_1","object":"response","status":%q,"incomplete_details":{"reason":%q},"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2}}}`, scenario.eventType, status, scenario.reason))
			if scenario.eventType == "response.output_text.delta" {
				terminal = []byte(`{"type":"response.output_text.delta","delta":"partial"}`)
			}
			session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
				result := execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: scenario.executionErr}
				if emit(ctx, []byte(`{"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`)) != nil {
					return result
				}
				if scenario.requested {
					select {
					case <-session.interrupts:
					case <-ctx.Done():
						return result
					}
				}
				_ = emit(ctx, terminal)
				return result
			}
			h, engine, _ := websocketTestHandler(t, "http://unused.invalid", channel.OpenAI)
			h.forwarder = websocketScriptForwarder{open: func(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
				return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
			}}
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			defer finishWrite()
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"question","store":false}`)); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Fatal(err)
			}
			if scenario.requested {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt","response_id":"resp_1","mode":"discard_partial_items"}`)); err != nil {
					t.Fatal(err)
				}
			}
			_, received, err := conn.ReadMessage()
			if err != nil || string(received) != string(terminal) {
				t.Fatalf("upstream terminal changed: got=%s err=%v", received, err)
			}
			finishWrite()
			event := waitWebsocketLogs(t, sink, 1)[0]
			if event.Status != scenario.wantStatus || len(event.Attempts) != 1 {
				t.Fatalf("status=%s attempts=%d, want %s/1", event.Status, len(event.Attempts), scenario.wantStatus)
			}
			attempt := event.Attempts[0]
			if scenario.wantStatus == telemetry.RequestStatusSuccess {
				if event.ErrorCode != "" || event.ErrorSummary != "" || attempt.ErrorCode != "" || attempt.ErrorSummary != "" || attempt.FailureCategory != telemetry.FailureCategoryOK {
					t.Fatalf("normal interruption retained failure fields: %+v", event)
				}
			} else if event.ErrorCode == "" || attempt.ErrorCode == "" || attempt.FailureCategory == telemetry.FailureCategoryOK {
				t.Fatalf("unsuccessful interruption lost failure evidence: %+v", event)
			}
			if scenario.eventType != "response.output_text.delta" && (event.Usage.Result.State != usage.StateComplete || event.Usage.Result.Tokens.UncachedInput != 2 || event.Usage.Result.Tokens.Output != 0) {
				t.Fatalf("usage changed: %+v", event.Usage.Result)
			}
		})
	}
}

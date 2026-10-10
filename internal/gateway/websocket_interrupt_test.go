package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/pricing"
	"gpt-load/internal/rpm"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

type interruptibleWebsocketFixture struct {
	websocketScriptSession
	interrupts     chan []byte
	afterInterrupt func(context.Context) error
}

func (s *interruptibleWebsocketFixture) Interrupt(ctx context.Context, payload []byte) error {
	select {
	case s.interrupts <- bytes.Clone(payload):
		if s.afterInterrupt != nil {
			return s.afterInterrupt(ctx)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWebsocketInterruptBypassesActiveTurnAndContinues(t *testing.T) {
	interrupt := []byte(`{"type":"response.interrupt","response_id":"resp_1","mode":"discard_partial_items","extension":"preserve"}`)
	session := &interruptibleWebsocketFixture{interrupts: make(chan []byte, 1)}
	session.done = make(chan struct{})
	var turns, opens atomic.Int32
	session.turn = func(ctx context.Context, payload []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
		if turns.Add(1) == 1 {
			if err := emit(ctx, []byte(`{"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"upstream"}}`)); err != nil {
				return execution.WebsocketResult{}
			}
			select {
			case body := <-session.interrupts:
				if !bytes.Equal(body, interrupt) {
					t.Errorf("interrupt changed: %s", body)
				}
			case <-ctx.Done():
				return execution.WebsocketResult{}
			}
			_ = emit(ctx, []byte(`{"type":"response.incomplete","response":{"id":"resp_1","object":"response","status":"incomplete","model":"upstream","incomplete_details":{"reason":"interrupted"},"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`))
		} else {
			var request struct {
				Previous string `json:"previous_response_id"`
			}
			if json.Unmarshal(payload, &request) != nil || request.Previous != "resp_1" {
				t.Errorf("continuation lost: %s", payload)
			}
			_ = emit(ctx, websocketCompleted("resp_2", ""))
		}
		return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
	}
	h, engine, _ := websocketTestHandler(t, "http://unused.invalid", channel.OpenAI)
	prices, err := pricing.NewTable([]pricing.Rule{{Identity: pricing.Identity{ChannelID: "openai", ModelID: "upstream"}, Prices: pricing.Prices{
		Input: pricing.Price{NanoUSDPerMillion: 1_000_000, Set: true}, Output: pricing.Price{NanoUSDPerMillion: 1_000_000, Set: true},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	h.priceTables = &mutableGatewayPriceTableProvider{table: prices}
	store := rpm.NewStore()
	h.forwarder = websocketScriptForwarder{open: func(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		opens.Add(1)
		return &rpmObservedWebsocketSession{WebsocketSession: session, store: store, credentialID: 1}, execution.WebsocketResult{}
	}}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	limiter := &recordingAccessKeyRPMLimiter{}
	h.limiter = limiter
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	read := func(want string) {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, body, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("expected %s while the connection remains usable: %v", want, err)
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(body, &event) != nil || event.Type != want {
			t.Fatalf("expected %s, got %s", want, body)
		}
	}
	if err := conn.WriteMessage(websocket.TextMessage, interrupt); err != nil {
		t.Fatal(err)
	}
	read("error")
	if opens.Load() != 0 {
		t.Fatal("idle interrupt opened an upstream session")
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"question","store":false}`)); err != nil {
		t.Fatal(err)
	}
	read("response.created")
	for _, invalid := range []string{
		`{"type":"response.interrupt"}`,
		`{"type":"response.interrupt","response_id":42}`,
		`{"type":"response.interrupt","response_id":"other_connection"}`,
		`{"type":"response.interrupt","response_id":"resp_1","stream_id":"other_lane"}`,
	} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(invalid)); err != nil {
			t.Fatal(err)
		}
		read("error")
	}
	if err := conn.WriteMessage(websocket.TextMessage, interrupt); err != nil {
		t.Fatal(err)
	}
	read("response.incomplete")
	// 迟到或重复的中断不能留下 error 帧，影响下一轮读取。
	for range 2 {
		if err := conn.WriteMessage(websocket.TextMessage, interrupt); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","previous_response_id":"resp_1","input":"answer","store":false}`)); err != nil {
		t.Fatal(err)
	}
	read("response.completed")
	events := waitWebsocketLogs(t, sink, 2)
	if opens.Load() != 1 || turns.Load() != 2 || len(events) != 2 {
		t.Fatalf("opens=%d turns=%d logs=%d", opens.Load(), turns.Load(), len(events))
	}
	if len(limiter.snapshot()) != 2 || events[0].Status != telemetry.RequestStatusSuccess || events[1].Status != telemetry.RequestStatusSuccess {
		t.Fatalf("RPM or terminal accounting changed: calls=%d statuses=%s/%s", len(limiter.snapshot()), events[0].Status, events[1].Status)
	}
	if got := store.Current(rpm.Credential, 1, time.Now()).Requests; got != 2 {
		t.Fatalf("credential RPM=%d, want two inference attempts", got)
	}
	for _, event := range events {
		if len(event.Attempts) != 1 || event.ClientModel != "public" {
			t.Fatalf("control frame was counted as inference: %+v", event)
		}
		attempt := event.Attempts[0]
		if event.ErrorCode != "" || event.ErrorSummary != "" || attempt.ErrorCode != "" || attempt.ErrorSummary != "" || attempt.FailureCategory != telemetry.FailureCategoryOK {
			t.Fatalf("successful interaction was logged as a failure: %+v", event)
		}
		if event.Usage.Result.State != usage.StateComplete || event.Usage.Result.Tokens.UncachedInput != 2 || event.Usage.Result.Tokens.Output != 1 {
			t.Fatalf("successful interruption changed usage: %+v", event.Usage.Result)
		}
		if event.Usage.Pricing.CostState != "priced" || event.Usage.Pricing.EstimatedCostNanoUSD != 3 {
			t.Fatalf("successful interruption changed pricing: %+v", event.Usage.Pricing)
		}
	}
}

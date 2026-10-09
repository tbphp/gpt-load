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
	"gpt-load/internal/rpm"
	"gpt-load/internal/telemetry"
)

type interruptibleWebsocketFixture struct {
	websocketScriptSession
	interrupts chan []byte
}

func (s *interruptibleWebsocketFixture) Interrupt(ctx context.Context, payload []byte) error {
	select {
	case s.interrupts <- bytes.Clone(payload):
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
	if err := conn.WriteMessage(websocket.TextMessage, interrupt); err != nil {
		t.Fatal(err)
	}
	read("error")
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","previous_response_id":"resp_1","input":"answer","store":false}`)); err != nil {
		t.Fatal(err)
	}
	read("response.completed")
	events := waitWebsocketLogs(t, sink, 2)
	if opens.Load() != 1 || turns.Load() != 2 || len(events) != 2 {
		t.Fatalf("opens=%d turns=%d logs=%d", opens.Load(), turns.Load(), len(events))
	}
	if len(limiter.snapshot()) != 2 || events[0].Status != telemetry.RequestStatusIncomplete || events[1].Status != telemetry.RequestStatusSuccess {
		t.Fatalf("RPM or terminal accounting changed: calls=%d statuses=%s/%s", len(limiter.snapshot()), events[0].Status, events[1].Status)
	}
	if got := store.Current(rpm.Credential, 1, time.Now()).Requests; got != 2 {
		t.Fatalf("credential RPM=%d, want two inference attempts", got)
	}
	for _, event := range events {
		if len(event.Attempts) != 1 || event.ClientModel != "public" {
			t.Fatalf("control frame was counted as inference: %+v", event)
		}
	}
}

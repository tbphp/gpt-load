package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/rpm"
	"gpt-load/internal/state"
)

func TestWebsocketInterruptBypassesQueueAndPreservesConnection(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) { testWebsocketInterrupt(t, false) })
	t.Run("accepted_before_output", func(t *testing.T) { testWebsocketInterrupt(t, true) })
}

func testWebsocketInterrupt(t *testing.T, accepted bool) {
	var connections, interrupts atomic.Int32
	h, engine, input := websocketTestHandler(t, "https://example.invalid", channel.OpenAI)
	input.Groups[0].ChannelID = channel.Codex
	input.Groups[0].ConnectionType = "subscription"
	input.Groups[0].Params = json.RawMessage(`{}`)
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	encrypted, err := h.encryption.Encrypt(`{"type":"codex","access_token":"fixture-access","refresh_token":"fixture-refresh","account_id":"fixture-account"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.registry.(*state.CredentialRegistry).ReplaceCredentials([]state.CredentialEntry{{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "fixture-codex", Status: state.CredentialStatusActive, EncryptedValue: encrypted}}); err != nil {
		t.Fatal(err)
	}
	interruptReceived := make(chan struct{}, 1)
	session := &interruptScriptSession{websocketScriptSession: &websocketScriptSession{done: make(chan struct{})}}
	session.interrupt = func(_ context.Context, responseID, lane string) error {
		if responseID != "resp_interrupt" || lane != "" {
			t.Errorf("wrong interrupt target: %q %q", responseID, lane)
		}
		interrupts.Add(1)
		interruptReceived <- struct{}{}
		return nil
	}
	turns := 0
	session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
		turns++
		send := func(body []byte) {
			if err := emit(ctx, body); err != nil {
				t.Error(err)
			}
		}
		if turns == 1 {
			send([]byte(`{"type":"response.created","response":{"id":"resp_interrupt","object":"response","status":"in_progress","model":"upstream"}}`))
			select {
			case <-interruptReceived:
			case <-ctx.Done():
				t.Error("interrupt was queued behind completion")
				return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
			}
			if accepted {
				send([]byte(`{"type":"response.interrupt.accepted","response_id":"resp_interrupt"}`))
				send([]byte(`{"type":"response.incomplete","response":{"id":"resp_interrupt","object":"response","status":"incomplete","model":"upstream","output":[],"incomplete_details":{"reason":"interrupted"},"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2}}}`))
			} else {
				send([]byte(`{"type":"response.interrupt.failed","response_id":"resp_interrupt","error":{"code":"interrupt_not_supported"}}`))
				send([]byte(`{"type":"response.output_text.delta","response_id":"resp_interrupt","delta":"continued"}`))
				send(websocketCompleted("resp_interrupt", ""))
			}
		} else {
			send(websocketCompleted("resp_next", ""))
		}
		return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
	}
	// Keep the production RPM wrapper in the path; interruption is not a new turn.
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		connections.Add(1)
		return &rpmObservedWebsocketSession{WebsocketSession: session, store: rpm.NewStore(), credentialID: 1}, execution.WebsocketResult{}
	}}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","store":false,"input":"test"}`))
	if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), `"type":"response.created"`) {
		t.Fatalf("expected active response: %s %v", body, err)
	}
	// Unknown or cross-lane targets must not affect the active upstream response.
	for _, frame := range []string{
		`{"type":"response.interrupt","response_id":"not_owned","mode":"discard_partial_items"}`,
		`{"type":"response.interrupt","response_id":"resp_interrupt","mode":"discard_partial_items","stream_id":"other"}`,
	} {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(frame))
		var result struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := conn.ReadJSON(&result); err != nil || result.Error.Code != "interrupt_response_not_found" {
			t.Fatalf("target isolation: %+v %v", result, err)
		}
	}
	frame := []byte(`{"type":"response.interrupt","response_id":"resp_interrupt","mode":"discard_partial_items"}`)
	_ = conn.WriteMessage(websocket.TextMessage, frame)
	_, control, controlErr := conn.ReadMessage()
	controlType := "response.interrupt.failed"
	terminalType := "response.completed"
	if accepted {
		controlType, terminalType = "response.interrupt.accepted", "response.incomplete"
	}
	if controlErr != nil || !strings.Contains(string(control), `"type":"`+controlType+`"`) {
		t.Fatalf("lost upstream control result: %s %v", control, controlErr)
	}
	if !accepted {
		_, delta, deltaErr := conn.ReadMessage()
		if deltaErr != nil || !strings.Contains(string(delta), `"type":"response.output_text.delta"`) {
			t.Fatalf("control result interrupted generation: %s %v", delta, deltaErr)
		}
	}
	_, body, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"type":"`+terminalType+`"`) || !strings.Contains(string(body), `"model":"public"`) {
		t.Fatalf("lost genuine terminal/alias: %s", body)
	}
	_ = conn.WriteMessage(websocket.TextMessage, frame) // late duplicate: already completed
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","store":false,"previous_response_id":"resp_interrupt","input":"next"}`))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	logs := waitWebsocketLogs(t, sink, 2)
	if len(logs) != 2 || connections.Load() != 1 || interrupts.Load() != 1 {
		t.Fatalf("control counted/replayed: logs=%d conns=%d interrupts=%d", len(logs), connections.Load(), interrupts.Load())
	}
	for i, log := range logs {
		if string(log.Status) != "success" || log.ErrorCode != "" {
			t.Fatalf("control failure terminated model request: %s %s", log.Status, log.ErrorCode)
		}
		if len(log.Attempts) != 1 {
			t.Fatal("control created an extra model attempt")
		}
		output := int64(1)
		if accepted && i == 0 {
			output = 0
		}
		if tokens := log.Usage.Result.Tokens; tokens.UncachedInput != 2 || tokens.Output != output {
			t.Fatalf("lost or duplicated real terminal usage: %+v", tokens)
		}
	}
}

func TestWebsocketInterruptStrictFields(t *testing.T) {
	valid := `{"type":"response.interrupt","response_id":"resp_1","mode":"discard_partial_items"}`
	if _, err := inspectWebsocketInterrupt([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"type":"response.interrupt","response_id":"resp_1","mode":"unknown"}`,
		`{"type":"response.interrupt","response_id":"resp_1","mode":"discard_partial_items","input":"must not be lost"}`,
		`{"type":"response.interrupt","response_id":null,"mode":"discard_partial_items"}`,
		`{"type":"response.interrupt","response_id":123,"mode":"discard_partial_items"}`,
		`{"type":"response.interrupt","response_id":"","mode":"discard_partial_items"}`,
	} {
		if _, err := inspectWebsocketInterrupt([]byte(body)); err == nil {
			t.Fatal("accepted invalid control")
		}
	}
	var fields map[string]string
	_ = json.Unmarshal([]byte(valid), &fields)
	fields["response_id"] = strings.Repeat("x", 4097)
	body, _ := json.Marshal(fields)
	if _, err := inspectWebsocketInterrupt(body); err == nil {
		t.Fatal("accepted oversized ID")
	}
}

type interruptScriptSession struct {
	*websocketScriptSession
	interrupt func(context.Context, string, string) error
}

func (s *interruptScriptSession) Interrupt(ctx context.Context, responseID, lane string) error {
	return s.interrupt(ctx, responseID, lane)
}

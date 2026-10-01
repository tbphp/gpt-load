package gateway

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/reasoning"
)

func TestWebsocketLogsAppliedReasoningAcrossTurns(t *testing.T) {
	handler, engine, _ := websocketTestHandler(t, "http://127.0.0.1:1/v1", channel.OpenAI)
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	wantEfforts := []string{"high", "low", "low"}
	handler.forwarder = websocketScriptForwarder{AttemptForwarder: handler.forwarder, open: func(_ context.Context, _ ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		session := &websocketScriptSession{done: make(chan struct{})}
		turn := 0
		session.turn = func(ctx context.Context, payload []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			if gjson.GetBytes(payload, "reasoning.effort").String() != "high" {
				t.Error("request baseline changed")
			}
			if turn == 1 && gjson.GetBytes(payload, "input.0.reasoning.effort").String() != "low" {
				t.Error("configuration update was lost")
			}
			if err := emit(ctx, websocketCompleted(fmt.Sprintf("resp_effort_%d", turn), "")); err != nil {
				t.Error(err)
			}
			result := execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, AppliedReasoning: &reasoning.Config{Effort: wantEfforts[turn]}}
			turn++
			return result
		}
		return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	}}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for turn, want := range wantEfforts {
		previous, update := "", ""
		if turn > 0 {
			previous = fmt.Sprintf(`,"previous_response_id":"resp_effort_%d"`, turn-1)
		}
		if turn == 1 {
			update = `{"type":"configuration_update","reasoning":{"effort":"low"}},`
		}
		payload := fmt.Sprintf(`{"type":"response.create","model":"public","reasoning":{"effort":"high"}%s,"input":[%s{"role":"user","content":"hello"}]}`, previous, update)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
		log := waitWebsocketLogs(t, sink, turn+1)[turn]
		if len(log.Attempts) != 1 {
			t.Fatalf("turn=%d attempts=%d", turn, len(log.Attempts))
		}
		if log.Reasoning.Effort != want || log.Attempts[0].Reasoning.Effort != want {
			t.Errorf("turn=%d request=%q attempt=%q, want %q", turn, log.Reasoning.Effort, log.Attempts[0].Reasoning.Effort, want)
		}
	}
}

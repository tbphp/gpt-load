package gateway

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

func TestWebsocketCodexReasoningStatusReplayAndFailover(t *testing.T) {
	// Devin 不支持原生 Responses WebSocket；以 OpenAI 作为该路径的非 Codex 后备候选。
	// CLIProxyAPI 对照组与 Codex 都是串行连接，对照组不启用 reasoning.status 清理。
	var baseline []map[string]any
	for _, first := range []channel.ID{channel.CLIProxyAPI, channel.Codex} {
		observed := &encryptedRecoveryBodies{}
		h, engine := reasoningStatusGateway(t, nil, first, channel.OpenAI)
		h.forwarder = websocketScriptForwarder{open: func(_ context.Context, input ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
			session := &websocketScriptSession{done: make(chan struct{})}
			session.turn = func(ctx context.Context, body []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
				observed.add(input.ChannelID, string(body))
				_, bodies := observed.snapshot()
				reply := websocketCompleted("resp_status_fallback", "")
				switch len(bodies) {
				case 1:
					reply = []byte(websocketRejection("invalid_encrypted_content", "The encrypted content could not be verified."))
				case 2:
					reply = []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`)
				}
				if err := emit(ctx, reply); err != nil {
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: err.Error()}}
				}
				return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
			}
			return session, execution.WebsocketResult{}
		}}
		server := httptest.NewServer(engine)
		t.Cleanup(server.Close)
		conn := dialGatewayWebsocket(t, server.URL)
		body := strings.Replace(codexReasoningStatusRequest, `{"model":`, `{"type":"response.create","model":`, 1)
		if reply := websocketContextRecoveryExchange(t, conn, body); !strings.Contains(reply, `"response.completed"`) {
			t.Fatalf("failover failed: %s", reply)
		}
		channels, bodies := observed.snapshot()
		if !reflect.DeepEqual(channels, []string{string(first), string(first), string(channel.OpenAI)}) {
			t.Fatalf("want rejection, same-credential replay, then failover: %v", channels)
		}
		assertReasoningStatusInput(t, []byte(bodies[0]), first == channel.Codex)
		for _, sent := range bodies[:2] {
			items := reasoningStatusInput(t, []byte(sent))
			if _, status := items[0]["status"]; status != (first != channel.Codex) {
				t.Fatalf("wrong reasoning.status on %s: %s", first, sent)
			}
		}
		if !strings.Contains(bodies[2], `"candidate_marker":"openai"`) {
			t.Fatalf("failover inherited another candidate's body: %s", bodies[2])
		}
		got := reasoningStatusInput(t, []byte(bodies[2]))
		if baseline == nil {
			baseline = got
		} else if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("Codex cleanup leaked to non-Codex WebSocket candidate: %s", bodies[2])
		}
	}
}

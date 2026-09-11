package bifrost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestNativeWebsocketUsesChannelEndpointAndCredential(t *testing.T) {
	for _, channelID := range []channel.ID{channel.OpenAI, channel.XAI, channel.GPTLoad, channel.CLIProxyAPI, channel.Sub2API} {
		t.Run(string(channelID), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/team/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture-key" || r.URL.Query().Get("api_key") != "" {
					t.Error("incorrect WS target or credential")
				}
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if _, _, err = conn.ReadMessage(); err != nil {
					return
				}
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","object":"response"}}`))
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()
			registry := channel.NewRegistry()
			manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, registry)
			if err != nil {
				t.Fatal(err)
			}
			if err = manager.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer manager.Shutdown()
			opener, ok := any(manager).(execution.WebsocketOpener)
			if !ok {
				t.Fatal("native WebSocket adapter unavailable")
			}
			base := server.URL + "/team"
			if channelID == channel.OpenAI || channelID == channel.XAI {
				base += "/v1"
			}
			target, err := registry.Resolve(channelID, json.RawMessage(`{"base_url":"`+base+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			spec := freezeTestAttempt(execution.AttemptSpec{
				RequestID: "ws-request", AttemptID: "ws-attempt", Sequence: 1,
				ChannelID: string(channelID), ClientProtocol: protocol.OpenAIResponses,
				Operation: execution.OperationResponsesCreate, ClientModel: "model", UpstreamModel: "model",
				Method: http.MethodPost, Path: "/v1/responses", RawQuery: "api_key=downstream&trace=kept",
				Body: []byte(`{"model":"model","input":"hello"}`), TargetConfig: target.TargetConfig,
				Credential: execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":"fixture-key"}`)),
			})
			s, result := opener.OpenWebsocket(t.Context(), spec)
			if result.Error != nil || s == nil {
				t.Fatalf("open: %+v", result)
			}
			defer s.Close()
			result = s.ExecuteTurn(t.Context(), spec.Body, func(context.Context, []byte) error { return nil })
			if result.Error != nil {
				t.Fatalf("execute: %+v", result)
			}
		})
	}
}

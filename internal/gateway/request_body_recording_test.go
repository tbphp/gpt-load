package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
)

func TestHandlerRecordsRequestBodyOnlyForOptedInAccessKey(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
			}}}
			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-one")
			if _, err := manager.Publish(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "openai", ChannelID: channel.OpenAI,
					Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
				}},
				Credentials: []state.CredentialConfig{{
					ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1",
				}},
				AccessKeys: []state.AccessKeyConfig{{
					ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
					Status: state.AccessKeyStatusActive, RecordRequestBody: enabled,
				}},
			}); err != nil {
				t.Fatal(err)
			}

			body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			events := sink.snapshot()
			if response.Code != http.StatusOK || len(events) != 1 {
				t.Fatalf("response = %d, events = %d", response.Code, len(events))
			}
			if enabled && string(events[0].RequestBody) != body {
				t.Fatalf("recorded body = %q, want original client body", events[0].RequestBody)
			}
			if !enabled && events[0].RequestBody != nil {
				t.Fatalf("access key without recording logged body %q", events[0].RequestBody)
			}
		})
	}
}

func TestWebsocketRecordsTurnRequestForOptedInAccessKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_1", ""))
	}))
	defer upstream.Close()
	handler, engine, input := websocketTestHandler(t, upstream.URL, channel.OpenAI)
	input.AccessKeys[0].RecordRequestBody = true
	if _, err := handler.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()

	frame := `{"type":"response.create","model":"public","input":"hello"}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if event := waitWebsocketLogs(t, sink, 1)[0]; string(event.RequestBody) != frame {
		t.Fatalf("recorded turn body = %q, want client frame", event.RequestBody)
	}
}

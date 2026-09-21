package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func sharedAliasModels() []state.ModelConfig {
	return []state.ModelConfig{{ID: "upstream-a", Alias: "public"}, {ID: "upstream-b", Alias: "public"}, {ID: "upstream-c", Alias: "public"}}
}

func TestModelRotationHTTPPreservesAliasAndCredentialContinuity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			type observation struct{ Model, Authorization string }
			requests := make(chan observation, 3)
			var sequence atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests <- observation{request.Model, r.Header.Get("Authorization")}
				response := fmt.Sprintf(`{"id":"resp_%d","object":"response","status":"completed","model":%q,"output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, sequence.Add(1), request.Model)
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, response)
				}
			}))
			defer upstream.Close()
			handler, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
			input.Groups[0].Models = sharedAliasModels()
			input.Credentials = append(input.Credentials, testCredentialConfig(2, 1))
			registry := handler.registry.(*state.CredentialRegistry)
			if err := registry.ApplyCredentialImport(1, []state.CredentialEntry{testCredentialEntry(t, handler.encryption, 2, 1, "other-key")}); err != nil {
				t.Fatal(err)
			}
			if _, err := handler.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			sink := &recordingRequestLogSink{}
			handler.requestLogSink = sink
			for index, want := range []string{"upstream-a", "upstream-b", "upstream-c"} {
				previous := ""
				if index == 2 {
					previous = `,"previous_response_id":"resp_1"`
				}
				body := fmt.Sprintf(`{"model":"public","input":"hello","prompt_cache_key":"same-cache","stream":%t%s}`, stream, previous)
				response := serveContinuation(t, engine, "gl-client", body, http.StatusOK)
				observed := <-requests
				if observed.Model != want || observed.Authorization != "Bearer upstream-key" || !strings.Contains(response.Body.String(), `"model":"public"`) {
					t.Fatalf("request %d model=%q, alias response=%s", index, observed.Model, response.Body)
				}
			}
			assertAffinityHits(t, sink.snapshot(), []bool{false, true, true})
			for index, event := range sink.snapshot() {
				if event.ClientModel != "public" || event.UpstreamModel != sharedAliasModels()[index].ID {
					t.Fatalf("recorded models = %q/%q", event.ClientModel, event.UpstreamModel)
				}
			}
			if got := visibleModelIDs(handler.manager.Current(), handler.manager.Current().AccessKeysByID[1], protocol.OpenAIResponses); !reflect.DeepEqual(got, []string{"public"}) {
				t.Fatalf("visible models = %v", got)
			}
		})
	}
}

func TestModelRotationWebsocketKeepsConnectionAndAllowsTemporaryContinuation(t *testing.T) {
	var connections atomic.Int32
	requests := make(chan string, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		connections.Add(1)
		for index := 1; index <= 3; index++ {
			var request struct {
				Model    string `json:"model"`
				Previous string `json:"previous_response_id"`
			}
			if err := conn.ReadJSON(&request); err != nil {
				t.Error(err)
				return
			}
			requests <- request.Model
			if index > 1 && request.Previous != "resp_1" {
				t.Error("lost previous response")
			}
			body := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","object":"response","status":"completed","model":%q,"output":[]}}`, index, request.Model)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
				t.Error(err)
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	handler, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	input.Groups[0].Models = sharedAliasModels()
	if _, err := handler.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for index, model := range sharedAliasModels() {
		request := map[string]any{"type": "response.create", "model": "public", "input": "hello", "store": false}
		if index > 0 {
			request["previous_response_id"] = "resp_1"
		}
		if err := conn.WriteJSON(request); err != nil {
			t.Fatal(err)
		}
		_, body, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"model":"public"`) || !strings.Contains(string(body), `"response.completed"`) {
			t.Fatalf("response=%s", body)
		}
		if got := <-requests; got != model.ID {
			t.Fatalf("model=%s, want %s", got, model.ID)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("connections=%d", connections.Load())
	}
	if _, found := handler.responseBindings.Lookup(1, "resp_1"); found {
		t.Fatal("temporary response became stored")
	}
}

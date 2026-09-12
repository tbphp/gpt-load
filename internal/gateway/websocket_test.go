package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/pricing"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/testutil/encryptiontest"
)

func websocketTestHandler(t *testing.T, endpoint string, id channel.ID) (*Handler, *gin.Engine, state.CompileInput) {
	t.Helper()
	service := encryptiontest.Service(t, "websocket-fixture-key")
	input := state.CompileInput{ChannelRegistry: channel.NewRegistry(),
		Groups:      []state.GroupConfig{{ID: 1, Name: "ws", ConnectionType: "api_key", ChannelID: id, Params: json.RawMessage(fmt.Sprintf(`{"base_url":%q}`, endpoint)), Models: []state.ModelConfig{{ID: "upstream", Alias: "public"}}, Enabled: true}},
		Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
		AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: service.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
	}
	manager := state.NewManager()
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, service, 1, 1, "upstream-key")}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(manager, registry, service, newTestExecutionForwarder(t), dialect.NewSet(dialect.NewOpenAIResponses()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, nil, nil)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	return h, engine, input
}

func websocketCompleted(id, lane string) []byte {
	label := ""
	if lane != "" {
		label = fmt.Sprintf(`,"stream_id":%q`, lane)
	}
	return []byte(fmt.Sprintf(`{"type":"response.completed"%s,"response":{"id":%q,"object":"response","status":"completed","model":"upstream","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`, label, id))
}

func waitWebsocketLogs(t *testing.T, sink *recordingRequestLogSink, count int) []telemetry.RequestEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		events := sink.snapshot()
		if len(events) >= count {
			return events
		}
		select {
		case <-deadline:
			t.Fatalf("logs=%d want %d", len(events), count)
		case <-ticker.C:
		}
	}
}

func TestWebsocketIdleUpstreamDisconnectClosesClient(t *testing.T) {
	closeUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_1", ""))
		<-closeUpstream
	}))
	defer upstream.Close()
	h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	waitWebsocketLogs(t, sink, 1)
	close(closeUpstream)
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("downstream remained open")
	}
	if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
		t.Fatal("idle upstream disconnect did not close downstream")
	}
}

func TestWebsocketQuotaCountsConcurrentTurnsOnceWithoutReservation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var lanes []string
		for len(lanes) < 2 {
			var req struct {
				StreamID string `json:"stream_id"`
			}
			if conn.ReadJSON(&req) != nil {
				return
			}
			lanes = append(lanes, req.StreamID)
		}
		for _, lane := range lanes {
			if conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_"+lane, lane)) != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	limiter := &recordingAccessKeyRPMLimiter{}
	h.limiter = limiter
	runtime := accessquota.NewRuntime()
	h.accessQuota = runtime
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {{ID: 1, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 4}}}); err != nil {
		t.Fatal(err)
	}
	table, err := pricing.NewTable([]pricing.Rule{{Identity: pricing.Identity{ChannelID: "openai", ModelID: "upstream"}, Prices: pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 1_000_000, Set: true}, Output: pricing.Price{NanoUSDPerMillion: 1_000_000, Set: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	h.priceTables = &mutableGatewayPriceTableProvider{table: table}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	for _, lane := range []string{"a", "b"} {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.create","stream_id":%q,"model":"public","input":"hello"}`, lane)))
	}
	for i := 0; i < 2; i++ {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	events := waitWebsocketLogs(t, sink, 2)
	for _, event := range events {
		if event.Status != telemetry.RequestStatusSuccess || event.Usage.Pricing.EstimatedCostNanoUSD != 3 || len(event.Attempts) != 1 {
			t.Fatalf("wrong per-turn record: %+v", event)
		}
	}
	if got := runtime.Snapshot(1, time.Now()).Rules[0].UsedNanoUSD; got != 6 {
		t.Fatalf("used=%d want 6 (existing in-flight overspend)", got)
	}
	if len(limiter.snapshot()) != 2 {
		t.Fatal("RPM counted handshake or missed a turn")
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","stream_id":"a","model":"public","input":"over quota"}`))
	var rejected struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := conn.ReadJSON(&rejected); err != nil || rejected.Error.Code != "access_key_cost_limit_exceeded" || rejected.Error.Type != "usage_limit_reached" {
		t.Fatalf("quota rejection=%+v err=%v", rejected, err)
	}
}

func TestWebsocketHandlerRemainsTrackedUntilShutdown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	h, _, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	h.lifecycle = httplifecycle.NewCoordinator()
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if h.lifecycle.Wait(ctx) == nil {
		t.Fatal("WS handler returned before socket closed")
	}
	h.lifecycle.BeginShutdown()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := h.lifecycle.Wait(ctx2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("shutdown left WS socket open")
	}
	h.websocketBudget.mu.Lock()
	defer h.websocketBudget.mu.Unlock()
	if h.websocketBudget.connections != 0 || h.websocketBudget.input != 0 {
		t.Fatal("shutdown leaked connection/input budget")
	}
}

func TestWebsocketStoredAndTemporaryOwnershipRemainSeparate(t *testing.T) {
	var turns, connections atomic.Int32
	requests := make(chan map[string]any, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		for {
			var req map[string]any
			if conn.ReadJSON(&req) != nil {
				return
			}
			requests <- req
			n := turns.Add(1)
			if conn.WriteMessage(websocket.TextMessage, websocketCompleted(fmt.Sprintf("resp_%d", n), "")) != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	first := dialGatewayWebsocket(t, server.URL)
	// 省略 store 沿用上游默认；真实预热不能由网关合成响应。
	_ = first.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"warm","generate":false}`))
	if _, _, err := first.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if req := <-requests; req["generate"] != false {
		t.Fatal("prewarm flag lost")
	}
	waitWebsocketLogs(t, sink, 1)
	if _, ok := h.responseBindings.Lookup(1, "resp_1"); !ok {
		t.Fatal("omitted store did not use native storage contract")
	}
	_ = first.Close()
	second := dialGatewayWebsocket(t, server.URL)
	_ = second.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"continue","previous_response_id":"resp_1","store":false}`))
	if _, _, err := second.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if req := <-requests; req["previous_response_id"] != "resp_1" || req["store"] != false {
		t.Fatal("persistent parent or child store changed")
	}
	waitWebsocketLogs(t, sink, 2)
	if _, ok := h.responseBindings.Lookup(1, "resp_2"); ok {
		t.Fatal("store:false escaped into persistent index")
	}
	_ = second.Close()
	third := dialGatewayWebsocket(t, server.URL)
	_ = third.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","previous_response_id":"resp_2","input":"missing"}`))
	var rejection struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := third.ReadJSON(&rejection); err != nil || rejection.Error.Code != "response_binding_not_found" {
		t.Fatalf("temporary restore=%+v err=%v", rejection, err)
	}
	if turns.Load() != 2 || connections.Load() != 2 {
		t.Fatal("missing parent was scanned or resent upstream")
	}
}

func TestWebsocketBoundSerialTargetRejectsNamedFlow(t *testing.T) {
	var turns atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
			turns.Add(1)
			if conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_1", "")) != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	_, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.XAI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`))
	if _, body, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(body), `"type":"response.completed"`) {
		t.Fatalf("first turn: %s", body)
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","stream_id":"child","model":"public","input":"hello"}`))
	var event struct {
		StreamID string `json:"stream_id"`
		Error    struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := conn.ReadJSON(&event); err != nil || event.StreamID != "child" || event.Error.Code != "websocket_capability_unavailable" {
		t.Fatalf("named flow=%+v err=%v", event, err)
	}
	if turns.Load() != 1 {
		t.Fatal("named flow was stripped or executed on serial upstream")
	}
}

func TestWebsocketUpgradeValidationAndHTTPBoundary(t *testing.T) {
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	_, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	for _, test := range []struct {
		name, auth, key, upgrade string
		status                   int
	}{
		{"auth first", "bad", "", "websocket", 401},
		{"missing key", "gl-client", "", "websocket", 400},
		{"invalid key", "gl-client", "short", "websocket", 400},
		{"partial intent", "gl-client", "dGhlIHNhbXBsZSBub25jZQ==", "", 400},
		{"ordinary GET", "gl-client", "", "", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			request.Header.Set("Authorization", "Bearer "+test.auth)
			if test.upgrade != "" {
				request.Header.Set("Upgrade", test.upgrade)
				request.Header.Set("Connection", "keep-alive, Upgrade")
				request.Header.Set("Sec-WebSocket-Version", "13")
			}
			if test.key != "" {
				request.Header.Set("Sec-WebSocket-Key", test.key)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if forwarded.Load() != 0 {
		t.Fatal("malformed handshakes reached HTTP upstream")
	}
}

func TestWebsocketTerminalEvidence(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		success       bool
	}{
		{"completed done", `{"type":"response.done","response":{"id":"resp_1","object":"response","status":"completed","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`, true},
		{"failed done", `{"type":"response.done","response":{"id":"resp_1","object":"response","status":"failed","error":{"code":"failed"}}}`, false},
		{"missing ID", `{"type":"response.completed","response":{"object":"response","status":"completed"}}`, false},
		{"contradictory status", `{"type":"response.completed","response":{"id":"resp_1","object":"response","status":"failed"}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err = conn.ReadMessage(); err == nil {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(test.payload))
				}
				_, _, _ = conn.ReadMessage()
			}))
			defer upstream.Close()
			h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`))
			_, _, _ = conn.ReadMessage()
			events := waitWebsocketLogs(t, sink, 1)
			if (events[0].Status == telemetry.RequestStatusSuccess) != test.success {
				t.Fatalf("terminal status=%s expected success=%t", events[0].Status, test.success)
			}
		})
	}
}

func TestWebsocketPreparationPreservesExplicitControls(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"public","input":"warm","stream":true,"generate":false,"store":true,"previous_response_id":"resp_parent"}`)
	original, err := inspectWebsocketRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	upstreamModel := "upstream"
	for _, test := range []struct {
		name, field string
		value       any
		reject      bool
	}{
		{"plain", "", nil, false}, {"store downgrade", "store", false, true}, {"fake prewarm", "generate", true, true}, {"parent replacement", "previous_response_id", "resp_other", true}, {"new stream", "stream_id", "child", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection := scheduler.Selection{UpstreamModelID: &upstreamModel}
			if test.field != "" {
				rules, err := parameteroverride.Compile([]any{map[string]any{"match": map[string]any{"protocol": "openai-responses", "model": "public"}, "set": map[string]any{test.field: test.value}}})
				if err != nil {
					if test.field == "store" && test.reject {
						return
					}
					t.Fatal(err)
				}
				selection.Group.ParameterOverrides = rules
			}
			payload, _, err := prepareWebsocketPayload(body, original, selection)
			if (err != nil) != test.reject {
				t.Fatalf("reject=%t err=%v payload=%s", test.reject, err, payload)
			}
			if !test.reject {
				var req map[string]any
				_ = json.Unmarshal(payload, &req)
				if req["type"] != nil || req["stream"] != nil || req["model"] != "upstream" || req["generate"] != false || req["previous_response_id"] != "resp_parent" {
					t.Fatalf("session create-body contract violated: %s", payload)
				}
			}
		})
	}
}

func TestWebsocketStreamFieldValidation(t *testing.T) {
	for _, test := range []struct {
		name, field string
		valid       bool
	}{
		{"absent", "", true},
		{"true", `,"stream":true`, true},
		{"false", `,"stream":false`, true},
		{"null", `,"stream":null`, false},
		{"string", `,"stream":"true"`, false},
		{"number", `,"stream":1`, false},
		{"duplicate", `,"stream":true,"stream":false`, false},
		{"case collision", `,"stream":true,"Stream":false`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := inspectWebsocketRequest([]byte(`{"type":"response.create","model":"public","input":[]` + test.field + `}`))
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t err=%v", test.valid, err)
			}
			if err == nil && !request.metadata.Stream {
				t.Fatal("WS request stopped being an event stream")
			}
		})
	}
}

func TestWebsocketStreamFieldKeepsNativeEventsAcrossChannels(t *testing.T) {
	for _, id := range []channel.ID{channel.OpenAI, channel.XAI, channel.CLIProxyAPI, channel.Sub2API, channel.GPTLoad} {
		t.Run(string(id), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				for index := 0; index < 3; index++ {
					var request map[string]any
					if conn.ReadJSON(&request) != nil {
						return
					}
					if _, exists := request["stream"]; exists {
						t.Error("redundant stream field reached upstream WS")
					}
					responseID := fmt.Sprintf("resp_%d", index)
					if index == 0 {
						if request["generate"] != false {
							t.Error("prewarm lost generate:false")
						}
					} else {
						if request["previous_response_id"] != fmt.Sprintf("resp_%d", index-1) {
							t.Error("continuation lost parent response")
						}
						if err := conn.WriteJSON(map[string]any{"type": "response.output_text.delta", "response_id": responseID, "delta": "OK"}); err != nil {
							return
						}
					}
					if err := conn.WriteMessage(websocket.TextMessage, websocketCompleted(responseID, "")); err != nil {
						return
					}
				}
				_, _, _ = conn.ReadMessage()
			}))
			t.Cleanup(upstream.Close)
			endpoint := upstream.URL
			if id == channel.OpenAI || id == channel.XAI {
				endpoint += "/v1"
			}
			_, engine, _ := websocketTestHandler(t, endpoint, id)
			server := httptest.NewServer(engine)
			t.Cleanup(server.Close)
			conn := dialGatewayWebsocket(t, server.URL)
			for index, stream := range []bool{true, true, false} {
				request := map[string]any{"type": "response.create", "model": "public", "input": []any{}, "store": false, "stream": stream}
				wantEvents := []string{"response.completed"}
				if index == 0 {
					request["generate"] = false
				} else {
					request["previous_response_id"] = fmt.Sprintf("resp_%d", index-1)
					wantEvents = []string{"response.output_text.delta", "response.completed"}
				}
				if err := conn.WriteJSON(request); err != nil {
					t.Fatal(err)
				}
				for _, want := range wantEvents {
					var event struct {
						Type string `json:"type"`
					}
					if err := conn.ReadJSON(&event); err != nil || event.Type != want {
						t.Fatalf("turn=%d want=%s event=%+v err=%v", index, want, event, err)
					}
				}
			}
		})
	}
}

func TestWebsocketForkAndParentCacheErrorBoundaries(t *testing.T) {
	var sends atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		cache := map[string]string{}
		for {
			var req struct{ StreamID, Input, Previous string }
			var body map[string]json.RawMessage
			if conn.ReadJSON(&body) != nil {
				return
			}
			_ = json.Unmarshal(body["stream_id"], &req.StreamID)
			_ = json.Unmarshal(body["input"], &req.Input)
			_ = json.Unmarshal(body["previous_response_id"], &req.Previous)
			n := sends.Add(1)
			code := ""
			if req.Previous != "" {
				parentLane, exists := cache[req.Previous]
				if !exists {
					code = "previous_response_not_found"
				} else if req.Input == "fail" {
					code = "invalid_request_error"
					if parentLane == req.StreamID {
						delete(cache, req.Previous)
					}
				}
			}
			var event []byte
			if code != "" {
				event = []byte(fmt.Sprintf(`{"type":"error","stream_id":%q,"status":400,"error":{"code":%q,"message":"fixture failure"}}`, req.StreamID, code))
			} else {
				id := fmt.Sprintf("resp_%d", n)
				cache[id] = req.StreamID
				event = websocketCompleted(id, req.StreamID)
			}
			if conn.WriteMessage(websocket.TextMessage, event) != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	_, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	for _, step := range []struct{ lane, input, parent, want string }{{"a", "parent", "", "response.completed"}, {"b", "fail", "resp_1", "invalid_request_error"}, {"b", "fork", "resp_1", "response.completed"}, {"a", "fail", "resp_1", "invalid_request_error"}, {"a", "again", "resp_1", "previous_response_not_found"}} {
		request := map[string]any{"type": "response.create", "stream_id": step.lane, "model": "public", "input": step.input, "store": false}
		if step.parent != "" {
			request["previous_response_id"] = step.parent
		}
		if err := conn.WriteJSON(request); err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type     string `json:"type"`
			StreamID string `json:"stream_id"`
			Error    struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		got := event.Type
		if got == "error" {
			got = event.Error.Code
		}
		if got != step.want || event.StreamID != step.lane {
			t.Fatalf("got %+v want %s", event, step.want)
		}
	}
	if sends.Load() != 5 {
		t.Fatal("state-dependent turn was replayed or locally fabricated")
	}
}

func TestWebsocketQueuedTurnUsesCurrentModelAndRevocation(t *testing.T) {
	firstRead, release := make(chan struct{}), make(chan struct{})
	models := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for n := 1; n <= 2; n++ {
			var req struct {
				Model string `json:"model"`
			}
			if conn.ReadJSON(&req) != nil {
				return
			}
			models <- req.Model
			if n == 1 {
				close(firstRead)
				<-release
			}
			if conn.WriteMessage(websocket.TextMessage, websocketCompleted(fmt.Sprintf("resp_%d", n), "")) != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	for i := 0; i < 2; i++ {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`))
		if i == 0 {
			select {
			case <-firstRead:
			case <-time.After(time.Second):
				t.Fatal("first turn missing")
			}
		}
	}
	input.Groups[0].Models[0].ID = "new-upstream"
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	if first, second := <-models, <-models; first != "upstream" || second != "new-upstream" {
		t.Fatalf("models=%s,%s", first, second)
	}
	input.AccessKeys = nil
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("revoked AccessKey kept WS open")
	}
}

func TestWebsocketCascadePreservesNativeFlow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var req map[string]any
		if conn.ReadJSON(&req) != nil {
			return
		}
		if req["model"] != "upstream" || req["stream_id"] != "lane" || req["generate"] != false {
			t.Error("cascade changed protocol controls")
		}
		_ = conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_cascade", "lane"))
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	_, innerEngine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	inner := httptest.NewServer(innerEngine)
	defer inner.Close()
	h, outerEngine, input := websocketTestHandler(t, inner.URL, channel.GPTLoad)
	input.Groups[0].Models = []state.ModelConfig{{ID: "public"}}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	registry := h.registry.(*state.CredentialRegistry)
	if err := registry.ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "gl-client")}); err != nil {
		t.Fatal(err)
	}
	outer := httptest.NewServer(outerEngine)
	defer outer.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(outer.URL, "http")+"/v1/responses", http.Header{"Authorization": {"Bearer gl-client"}, "Origin": {outer.URL}})
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","stream_id":"lane","input":"warm","generate":false}`))
	var event struct {
		Type     string `json:"type"`
		StreamID string `json:"stream_id"`
	}
	if err := conn.ReadJSON(&event); err != nil || event.Type != "response.completed" || event.StreamID != "lane" {
		t.Fatalf("cascade=%+v err=%v", event, err)
	}
}

func TestWebsocketBoundsNormalizedInputBeforeDispatch(t *testing.T) {
	var sends atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err == nil {
			sends.Add(1)
			_ = conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_1", ""))
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	h.websocketLimits.input = 1024
	h.websocketLimits.globalInput = 2048
	rules, err := parameteroverride.Compile([]any{map[string]any{"set": map[string]any{"instructions": strings.Repeat("x", 4096)}}})
	if err != nil {
		t.Fatal(err)
	}
	group := h.manager.Current().Groups[1]
	group.ParameterOverrides = rules
	h.manager.Current().Groups[1] = group
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`))
	var event struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "error" || sends.Load() != 0 {
		t.Fatal("expanded input escaped memory budget")
	}
}

func TestWebsocketQueueOverflowClosesWithoutInjectingSameLaneError(t *testing.T) {
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err == nil {
			close(started)
		}
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	h.websocketLimits.pending = 1
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"one"}`))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not start")
	}
	for i := 0; i < 2; i++ {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","input":"invalid"}`))
	}
	if _, body, err := conn.ReadMessage(); err == nil {
		t.Fatalf("queue overflow emitted a request event into active turn: %s", body)
	}
}

type websocketScriptForwarder struct {
	AttemptForwarder
	open func(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult)
}

func (f websocketScriptForwarder) OpenWebsocket(ctx context.Context, input ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
	return f.open(ctx, input)
}

type websocketScriptSession struct {
	turn func(context.Context, []byte, func(context.Context, []byte) error) execution.WebsocketResult
	done chan struct{}
	once sync.Once
}

func (s *websocketScriptSession) ExecuteTurn(ctx context.Context, body []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
	return s.turn(ctx, body, emit)
}
func (s *websocketScriptSession) Done() <-chan struct{} { return s.done }
func (s *websocketScriptSession) Close() error          { s.once.Do(func() { close(s.done) }); return nil }

func TestWebsocketRetriesOnlyUnsentUnboundTurns(t *testing.T) {
	for _, dispatch := range []execution.DispatchState{execution.DispatchNotSent, execution.DispatchMaybeSent} {
		t.Run(string(dispatch), func(t *testing.T) {
			h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1", channel.CLIProxyAPI)
			input.Credentials = append(input.Credentials, testCredentialConfig(2, 1))
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			registry := h.registry.(*state.CredentialRegistry)
			if err := registry.ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "first-key"), testCredentialEntry(t, h.encryption, 2, 1, "second-key")}); err != nil {
				t.Fatal(err)
			}
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			var opens atomic.Int32
			h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
				n := opens.Add(1)
				session := &websocketScriptSession{done: make(chan struct{})}
				session.turn = func(ctx context.Context, body []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
					if n == 1 {
						_ = session.Close()
						return execution.WebsocketResult{DispatchState: dispatch, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindTransport, Code: "websocket_handshake_failed", OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeRequest}}
					}
					if err := emit(ctx, websocketCompleted("resp_retry", "")); err != nil {
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
					}
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
				}
				return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
			}}
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello","store":false}`))
			_, _, _ = conn.ReadMessage()
			events := waitWebsocketLogs(t, sink, 1)
			if dispatch == execution.DispatchNotSent {
				if opens.Load() != 2 || events[0].Status != telemetry.RequestStatusSuccess || len(events[0].Attempts) != 2 {
					t.Fatalf("unsent recovery opens=%d status=%s attempts=%d", opens.Load(), events[0].Status, len(events[0].Attempts))
				}
			} else if opens.Load() != 1 {
				t.Fatal("maybe-sent turn was replayed")
			}
		})
	}
}

func dialGatewayWebsocket(t *testing.T, endpoint string) *websocket.Conn {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(endpoint, "http")+"/v1/responses", http.Header{"Authorization": {"Bearer gl-client"}})
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("gateway WS handshake: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func TestWebsocketDefaultLaneFIFOAndContinuation(t *testing.T) {
	var connections, turns atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			n := turns.Add(1)
			if req["model"] != "upstream" || req["type"] != "response.create" {
				t.Error("model or protocol not preserved")
			}
			if n == 1 {
				close(started)
				<-release
			} else if req["previous_response_id"] != "resp_1" {
				t.Error("continuation ID lost")
			}
			if err = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","object":"response","status":"completed","model":"upstream","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`, n))); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	_, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"one","store":false}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first turn not sent")
	}
	// 本地无效的第二轮仍须排在第一轮终态之后。
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","input":"invalid"}`))
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"two","previous_response_id":"resp_1","store":false}`))
	close(release)
	for _, want := range []string{"response.completed", "error", "response.completed"} {
		_, body, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var event map[string]any
		_ = json.Unmarshal(body, &event)
		if event["type"] != want {
			t.Fatalf("event=%s want %s", body, want)
		}
		if want == "response.completed" && event["response"].(map[string]any)["model"] != "public" {
			t.Fatal("response alias lost")
		}
	}
	if turns.Load() != 2 || connections.Load() != 1 {
		t.Fatalf("turns=%d connections=%d", turns.Load(), connections.Load())
	}
}

func TestWebsocketConcurrentFirstTurnsShareOneSession(t *testing.T) {
	var connections atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		lanes := []string{}
		for len(lanes) < 2 {
			var req struct {
				StreamID string `json:"stream_id"`
			}
			if conn.ReadJSON(&req) != nil {
				return
			}
			lanes = append(lanes, req.StreamID)
		}
		for _, kind := range []string{"response.created", "response.completed"} {
			for _, lane := range lanes {
				if conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":%q,"stream_id":%q,"response":{"id":%q,"object":"response","status":"completed","model":"upstream","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`, kind, lane, "resp_"+lane))) != nil {
					return
				}
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	_, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	for _, lane := range []string{"a", "b"} {
		if conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.create","stream_id":%q,"model":"public","input":"hello"}`, lane))) != nil {
			t.Fatal("write")
		}
	}
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		var event struct {
			StreamID string `json:"stream_id"`
		}
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		seen[event.StreamID]++
	}
	if connections.Load() != 1 || seen["a"] != 2 || seen["b"] != 2 {
		t.Fatalf("connections=%d events=%v", connections.Load(), seen)
	}
}

func TestWebsocketFirstEventTimeoutClosesSession(t *testing.T) {
	closed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(closed)
		for {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	group := h.manager.Current().Groups[1]
	group.Timeouts.FirstByte = 30 * time.Millisecond
	h.manager.Current().Groups[1] = group
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`))
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("first-event deadline did not stop upstream")
	}
}

func TestWebsocketCacheAffinityAndConnectionBindingKinds(t *testing.T) {
	var ids atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, websocketCompleted(fmt.Sprintf("cache-%d", ids.Add(1)), "")); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	send := func(conn *websocket.Conn, key, prefix string, count int) {
		t.Helper()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"response.create","model":"public","input":%q,"prompt_cache_key":%q}`, prefix, key))); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
		waitWebsocketLogs(t, sink, count)
	}
	first := dialGatewayWebsocket(t, server.URL)
	send(first, "a", "initial", 1)
	first.Close()
	second := dialGatewayWebsocket(t, server.URL)
	defer second.Close()
	send(second, "a", "different", 2)
	send(second, "b", "bound turn", 3)
	third := dialGatewayWebsocket(t, server.URL)
	defer third.Close()
	send(third, "b", "another", 4)
	events := sink.snapshot()
	assertAffinityHits(t, events, []bool{false, true, true, false})
	if events[1].AffinityKind != telemetry.AffinityPromptCacheKey || events[2].AffinityKind != telemetry.AffinityResponseContinuity {
		t.Fatal("WebSocket affinity kind mismatch")
	}
}

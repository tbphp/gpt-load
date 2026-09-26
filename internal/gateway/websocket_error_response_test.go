package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/channel"
	"gpt-load/internal/state"
)

func TestWebsocketCooldownExhaustionPreservesRecovery(t *testing.T) {
	h, engine, _ := websocketTestHandler(t, "http://127.0.0.1:1/v1", channel.OpenAI)
	registry := h.registry.(*state.CredentialRegistry)
	ref, _ := registry.CredentialRef(1)
	until := time.Now().Add(5 * time.Minute)
	registry.SetModelCooldown(ref, "upstream", until, time.Now())
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "test"}); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Status int                           `json:"status"`
		Error  accessKeyCostLimitClientError `json:"error"`
	}
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event.Status != http.StatusTooManyRequests || event.Error.Code != reasonUpstreamRateLimited.Code ||
		event.Error.ResetsAt == nil || *event.Error.ResetsAt != (until.UnixMilli()+999)/1000 {
		t.Fatalf("cooldown response lost recovery information: %+v", event)
	}
}

func TestWebsocketTransportFailureSurvivesExhaustion(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := upstream.URL
	upstream.Close()
	for _, retries := range []int{0, 2} {
		t.Run(fmt.Sprint(retries), func(t *testing.T) {
			h, engine, _ := websocketTestHandler(t, endpoint+"/v1", channel.OpenAI)
			h.manager.Current().Settings.RetryCount = retries
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "test"}); err != nil {
				t.Fatal(err)
			}
			_, body, err := conn.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			events := waitWebsocketLogs(t, sink, 1)
			if !strings.Contains(string(body), "upstream_connect_failed") || len(events[0].Attempts) != 1 ||
				events[0].ErrorCode != "upstream_connect_failed" || events[0].Attempts[0].ErrorCode != "upstream_connect_failed" || events[0].Attempts[0].ErrorSummary == "" {
				t.Fatalf("connection failure lost after exhaustion: response=%s log=%+v", body, events[0])
			}
		})
	}
}

func TestWebsocketMalformedUpstreamIsProtocolFailure(t *testing.T) {
	for name, payload := range map[string]string{
		"native validation":  `{"type":"response.completed","response":{"object":"response","status":"completed"}}`,
		"gateway validation": fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"object":"response","status":"in_progress"}}`, strings.Repeat("x", 4097)),
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Error(err)
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
					t.Error(err)
					return
				}
				_, _, _ = conn.ReadMessage() // 等待网关在协议失败后关闭连接。
			}))
			defer upstream.Close()
			h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "test"}); err != nil {
				t.Fatal(err)
			}
			_, body, err := conn.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			events := waitWebsocketLogs(t, sink, 1)
			if !strings.Contains(string(body), "upstream_protocol_error") || len(events[0].Attempts) != 1 ||
				events[0].Attempts[0].ErrorCode != "upstream_protocol_error" || events[0].Attempts[0].WillRetry {
				t.Fatalf("protocol failure misclassified or replayed: response=%s log=%+v", body, events[0])
			}
		})
	}
}

func TestWebsocketQuotaRecoveryMatchesHTTP(t *testing.T) {
	for _, kind := range []accessquota.Kind{accessquota.KindPeriodic, accessquota.KindTotal} {
		t.Run(string(kind), func(t *testing.T) {
			h, engine, _ := websocketTestHandler(t, "http://127.0.0.1:1/v1", channel.OpenAI)
			runtime := accessquota.NewRuntime()
			h.accessQuota = runtime
			rule := accessquota.Rule{ID: 1, Revision: 1, Kind: kind, LimitNanoUSD: 1}
			if kind == accessquota.KindPeriodic {
				rule.PeriodSeconds = 300
			}
			if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {rule}}); err != nil {
				t.Fatal(err)
			}
			ticket, decision := runtime.Admit(1, time.Now())
			if !decision.Allowed {
				t.Fatal("quota setup rejected")
			}
			runtime.Complete(ticket, 1)
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "test"}); err != nil {
				t.Fatal(err)
			}
			var event struct {
				Status int                           `json:"status"`
				Error  accessKeyCostLimitClientError `json:"error"`
				Data   accessKeyCostLimitErrorData   `json:"data"`
			}
			if err := conn.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"public","input":"test"}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer gl-client")
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var httpBody accessKeyCostLimitErrorBody
			if err := json.NewDecoder(response.Body).Decode(&httpBody); err != nil {
				t.Fatal(err)
			}
			periodic := kind == accessquota.KindPeriodic
			if event.Status != http.StatusTooManyRequests || response.StatusCode != event.Status ||
				!reflect.DeepEqual(event.Error, httpBody.Error) || !reflect.DeepEqual(event.Data, httpBody.Data) ||
				event.Error.Code != reasonAccessKeyCostLimitExceeded.Code || event.Data.Recoverable != periodic ||
				(event.Error.ResetsAt != nil) != periodic || len(event.Data.BlockingRules) != 1 {
				t.Fatalf("quota recovery differs: WS=%+v HTTP=%+v", event, httpBody)
			}
		})
	}
}

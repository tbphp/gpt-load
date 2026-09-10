package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/state"
)

func TestWebsocketErrorPreservesLaneAndRedactsSecrets(t *testing.T) {
	const lane = "gl-stream123"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","stream_id":"gl-stream123","status":400,"error":{"code":"invalid_request_error","message":"invalid input for upstream-key"}}`))
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	_, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "stream_id": lane, "model": "public", "input": "hello"}); err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type     string `json:"type"`
		StreamID string `json:"stream_id"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "error" || event.StreamID != lane || strings.Contains(string(body), "upstream-key") {
		t.Fatalf("error routing/redaction failed: %s", body)
	}
}

func TestWebsocketRateLimitAppliesExistingModelCooldown(t *testing.T) {
	for _, payload := range []string{
		`{"type":"error","status":429,"error":{"code":"rate_limit_exceeded","message":"model rate limit"}}`,
		`{"type":"response.failed","response":{"id":"resp_limited","object":"response","status":"failed","error":{"code":"rate_limit_exceeded","message":"model rate limit"}}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err = conn.ReadMessage(); err != nil {
					return
				}
				_ = conn.WriteMessage(websocket.TextMessage, []byte(payload))
				_, _, _ = conn.ReadMessage()
			}))
			defer upstream.Close()
			h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello"}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Fatal(err)
			}
			waitWebsocketLogs(t, sink, 1)
			registry := h.registry.(*state.CredentialRegistry)
			if until := registry.ModelCooldowns(1, time.Now())["upstream"]; !until.After(time.Now()) {
				t.Fatal("known rate limit did not cool the selected model")
			}
			if until, _ := registry.CredentialCooldownUntil(1); !until.IsZero() {
				t.Fatal("model rate limit cooled the entire credential")
			}
		})
	}
}

func TestWebsocketInvalidTurnsConsumeRPM(t *testing.T) {
	h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1", channel.OpenAI)
	input.AccessKeys[0].RPMLimit = 1
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	h.limiter = ratelimit.NewAccessKeyRPM()
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	for index, want := range []string{reasonInvalidProtocolRequest.Code, reasonAccessKeyRateLimited.Code} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create"}`)); err != nil {
			t.Fatal(err)
		}
		var event struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event.Error.Code != want {
			t.Fatalf("turn %d: code=%s want=%s", index+1, event.Error.Code, want)
		}
	}
}

func TestWebsocketHandshakeUsesFirstByteBudget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_slow_handshake", ""))
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	h, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	group := h.manager.Current().Groups[1]
	group.Timeouts.FirstByte, group.Timeouts.Request = 20*time.Millisecond, time.Second
	h.manager.Current().Groups[1] = group
	h.manager.Current().Settings.RetryCount = 0
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	started := time.Now()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello"}); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type  string `json:"type"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 120*time.Millisecond || event.Type != "error" || event.Error.Code != reasonUpstreamTimeout.Code {
		t.Fatalf("first byte excludes handshake: elapsed=%s event=%+v", elapsed, event)
	}
}

func TestWebsocketInvalidFirstTurnKeepsUnboundDeadline(t *testing.T) {
	h, engine, _ := websocketTestHandler(t, "http://127.0.0.1:1", channel.OpenAI)
	h.websocketLimits.firstRequest = 30 * time.Millisecond
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("unbound connection did not expire: %v", err)
	}
}

func TestWebsocketNamedStreamLimitPreservesExistingLanes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for index := 0; ; index++ {
			var request struct {
				StreamID string `json:"stream_id"`
			}
			if conn.ReadJSON(&request) != nil {
				return
			}
			if conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_"+strconv.Itoa(index), request.StreamID)) != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	_, engine, _ := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	for index := 0; index < 35; index++ {
		lane := "lane_" + strconv.Itoa(index)
		if index == 33 {
			lane = "lane_0"
		} else if index == 34 {
			lane = ""
		}
		request := map[string]any{"type": "response.create", "model": "public", "input": "hello"}
		if lane != "" {
			request["stream_id"] = lane
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
			t.Fatalf("turn %d closed existing lanes: %v", index, err)
		}
		if index == 32 {
			if event.Type != "error" || event.StreamID != lane || event.Error.Code != "websocket_stream_limit_reached" {
				t.Fatalf("limit error lost scope: %+v", event)
			}
		} else if event.Type != "response.completed" || event.StreamID != lane {
			t.Fatalf("existing lane failed: %+v", event)
		}
	}
}

func TestWebsocketSubscriptionRefreshRespectsDispatchAndBudget(t *testing.T) {
	for _, test := range []struct {
		name       string
		dispatch   execution.DispatchState
		retries    int
		alwaysFail bool
		wantOpens  int32
		wantType   string
	}{
		{"refresh once", execution.DispatchNotSent, 2, false, 2, "response.completed"},
		{"refresh failed", execution.DispatchNotSent, 2, true, 2, "error"},
		{"no budget", execution.DispatchNotSent, 0, false, 1, "error"},
		{"maybe sent", execution.DispatchMaybeSent, 2, false, 1, "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1", channel.OpenAI)
			input.Groups[0].ChannelID = channel.Codex
			input.Groups[0].ConnectionType = "subscription"
			input.Groups[0].Params = json.RawMessage(`{}`)
			input.SystemSettings = config.Settings{state.SettingRetryCount: test.retries}
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			encrypted, err := h.encryption.Encrypt(`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account-1"}`)
			if err != nil {
				t.Fatal(err)
			}
			registry := h.registry.(*state.CredentialRegistry)
			if err := registry.ReplaceCredentials([]state.CredentialEntry{{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "subscription-account", Status: state.CredentialStatusActive, EncryptedValue: encrypted}}); err != nil {
				t.Fatal(err)
			}
			var opens atomic.Int32
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			limiter := &recordingAccessKeyRPMLimiter{}
			h.limiter = limiter
			h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, input ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
				index := opens.Add(1)
				if input.Credential.ID != 1 || input.ForceCredentialRefresh != (index == 2) {
					t.Errorf("attempt %d: credential=%d force=%t", index, input.Credential.ID, input.ForceCredentialRefresh)
				}
				session := &websocketScriptSession{done: make(chan struct{})}
				session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
					if index == 1 || test.alwaysFail {
						_ = session.Close()
						return execution.WebsocketResult{DispatchState: test.dispatch, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: 401, Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream, Hint: execution.FailureHintRefreshRequired, ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing}}
					}
					if err := emit(ctx, websocketCompleted("resp_refreshed", "")); err != nil {
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
					}
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
				}
				return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
			}}
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello", "store": false}); err != nil {
				t.Fatal(err)
			}
			var event struct {
				Type string `json:"type"`
			}
			if err := conn.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			logs := waitWebsocketLogs(t, sink, 1)
			if opens.Load() != test.wantOpens || event.Type != test.wantType || len(logs[0].Attempts) != int(test.wantOpens) {
				t.Fatalf("opens=%d event=%+v attempts=%+v", opens.Load(), event, logs[0].Attempts)
			}
			limiter.mu.Lock()
			calls := len(limiter.calls)
			limiter.mu.Unlock()
			if calls != 1 {
				t.Fatalf("one turn charged RPM %d times", calls)
			}
			registry.SchedulingState().WithLock(func(ledger *state.SchedulingLedger) {
				if ledger.Sequence != uint64(test.wantOpens) {
					t.Fatalf("allocation sequence=%d want=%d", ledger.Sequence, test.wantOpens)
				}
			})
		})
	}
}

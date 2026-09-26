package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/state"
)

func TestWebsocketCapabilityFailureRequiresMatchingRoute(t *testing.T) {
	_, engine, _ := websocketTestHandler(t, "http://127.0.0.1:1", channel.CLIProxyAPI)
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "stream_id": "named", "input": "hello"}); err != nil {
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
	if event.Error.Code != reasonWebsocketCapability.Code {
		t.Fatalf("event=%+v", event)
	}
}

func TestWebsocketOverrideFailureSkipsGroupWithoutSpendingForwardBudget(t *testing.T) {
	upstream := websocketSettingsUpstream(t, false)
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	addWebsocketErrorBackup(t, h, input)
	h.manager.Current().Settings.RetryCount = 0
	rules, err := parameteroverride.Compile([]any{map[string]any{"set": map[string]any{"stream_id": "changed"}}})
	if err != nil {
		t.Fatal(err)
	}
	group := h.manager.Current().Groups[1]
	group.ParameterOverrides = rules
	h.manager.Current().Groups[1] = group
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello"}); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	logs := waitWebsocketLogs(t, sink, 1)
	if event.Type != "response.completed" || len(logs[0].Attempts) != 1 || logs[0].Attempts[0].GroupID != 2 {
		t.Fatalf("event=%+v logs=%+v", event, logs)
	}
}

func TestWebsocketNoRouteIsNotCapabilityFailure(t *testing.T) {
	for _, missing := range []string{"model", "credential", "model filter"} {
		t.Run(missing, func(t *testing.T) {
			h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1/v1", channel.OpenAI)
			model := "public"
			switch missing {
			case "model":
				model = "unknown"
			case "credential":
				if err := h.registry.(*state.CredentialRegistry).ReplaceCredentials(nil); err != nil {
					t.Fatal(err)
				}
			case "model filter":
				input.AccessKeys[0].Filters.Models = map[string]struct{}{"other": {}}
				if _, err := h.manager.Publish(input); err != nil {
					t.Fatal(err)
				}
			}
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": model, "input": "hello"}); err != nil {
				t.Fatal(err)
			}
			var event struct {
				Status int `json:"status"`
				Error  struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := conn.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			logs := waitWebsocketLogs(t, sink, 1)
			if event.Status != 503 || event.Error.Code != reasonNoCandidate.Code || len(logs[0].Attempts) != 0 {
				t.Fatalf("event=%+v logs=%+v", event, logs)
			}
		})
	}
}

func addWebsocketErrorBackup(t *testing.T, h *Handler, input state.CompileInput) {
	t.Helper()
	second := input.Groups[0]
	second.ID, second.Name = 2, "backup"
	input.Groups = append(input.Groups, second)
	input.Credentials = append(input.Credentials, testCredentialConfig(2, 2))
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.(*state.CredentialRegistry).ReplaceCredentials([]state.CredentialEntry{
		testCredentialEntry(t, h.encryption, 1, 1, "fixture-key-one"),
		testCredentialEntry(t, h.encryption, 2, 2, "fixture-key-two"),
	}); err != nil {
		t.Fatal(err)
	}
	h.manager.Current().Settings.RetryCount = 2
}

func TestWebsocketPreparationFailureSelectsBackup(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		if _, _, e = c.ReadMessage(); e == nil {
			_ = c.WriteMessage(websocket.TextMessage, websocketCompleted("resp_backup", ""))
		}
		_, _, _ = c.ReadMessage()
	}))
	defer up.Close()
	h, engine, input := websocketTestHandler(t, up.URL+"/v1", channel.OpenAI)
	addWebsocketErrorBackup(t, h, input)
	h.manager.Current().Settings.RetryCount = 0
	h.websocketLimits.input = 8000
	rules, err := parameteroverride.Compile([]any{map[string]any{"set": map[string]any{"instructions": strings.Repeat("x", 6000)}}})
	if err != nil {
		t.Fatal(err)
	}
	for id, group := range h.manager.Current().Groups {
		group.ParameterOverrides = rules
		h.manager.Current().Groups[id] = group
	}
	registry := h.registry.(*state.CredentialRegistry)
	bad := testCredentialEntry(t, h.encryption, 1, 1, "fixture-key-one")
	bad.EncryptedValue = "broken-fixture-ciphertext"
	if e := registry.ReplaceCredentials([]state.CredentialEntry{bad, testCredentialEntry(t, h.encryption, 2, 2, "fixture-key-two")}); e != nil {
		t.Fatal(e)
	}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	srv := httptest.NewServer(engine)
	defer srv.Close()
	c := dialGatewayWebsocket(t, srv.URL)
	defer c.Close()
	if err := c.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "test"}); err != nil {
		t.Fatal(err)
	}
	_, body, e := c.ReadMessage()
	if e != nil {
		t.Fatal(e)
	}
	events := waitWebsocketLogs(t, sink, 1)
	if calls.Load() != 1 || !strings.Contains(string(body), "response.completed") || len(events[0].Attempts) != 2 || events[0].Attempts[0].ErrorCode != "credential_decrypt_failed" {
		t.Fatalf("preparation failure prevented backup: calls=%d response=%s attempts=%+v", calls.Load(), body, events[0].Attempts)
	}
}

func TestWebsocketRefreshFailureSelectsBackup(t *testing.T) {
	h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1/v1", channel.OpenAI)
	input.Groups[0].ChannelID = channel.Codex
	input.Groups[0].ConnectionType = "subscription"
	input.Groups[0].Params = json.RawMessage(`{}`)
	addWebsocketErrorBackup(t, h, input)
	encrypted, e := h.encryption.Encrypt(`{"type":"codex","access_token":"fixture-access","refresh_token":"fixture-refresh","account_id":"fixture-account"}`)
	if e != nil {
		t.Fatal(e)
	}
	registry := h.registry.(*state.CredentialRegistry)
	if e := registry.ReplaceCredentials([]state.CredentialEntry{
		{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "fixture-1", Status: state.CredentialStatusActive, EncryptedValue: encrypted},
		{ID: 2, GroupID: 2, Version: 1, IdentityGeneration: 1, Fingerprint: "fixture-2", Status: state.CredentialStatusActive, EncryptedValue: encrypted},
	}); e != nil {
		t.Fatal(e)
	}
	var opens atomic.Int32
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, in ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		n := opens.Add(1)
		if in.ForceCredentialRefresh != (n == 2) || (in.Credential.ID == 2) != (n == 3) {
			t.Errorf("attempt %d: credential=%d force refresh=%t", n, in.Credential.ID, in.ForceCredentialRefresh)
		}
		if n == 1 {
			return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: 401, Hint: execution.FailureHintRefreshRequired, Code: "auth_expired", OriginHint: execution.ErrorOriginUpstream, ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing}}
		}
		if n == 2 {
			return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: 503, Hint: execution.FailureHintRefreshUnavailable, Code: "refresh_unavailable", OriginHint: execution.ErrorOriginUpstream}}
		}
		session := &websocketScriptSession{done: make(chan struct{})}
		session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			if err := emit(ctx, websocketCompleted("resp_backup", "")); err != nil {
				t.Error(err)
			}
			return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
		}
		return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	}}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	srv := httptest.NewServer(engine)
	defer srv.Close()
	c := dialGatewayWebsocket(t, srv.URL)
	defer c.Close()
	if err := c.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "test", "store": false}); err != nil {
		t.Fatal(err)
	}
	_, body, e := c.ReadMessage()
	if e != nil {
		t.Fatal(e)
	}
	events := waitWebsocketLogs(t, sink, 1)
	if opens.Load() != 3 || !strings.Contains(string(body), "response.completed") || len(events[0].Attempts) != 3 || events[0].Attempts[2].CredentialID != 2 {
		t.Fatalf("refresh failure prevented backup: opens=%d response=%s attempts=%+v", opens.Load(), body, events[0].Attempts)
	}
}

func TestWebsocketHandshakeRejectionSelectsBackup(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
		effect string
	}{
		{401, "invalid_api_key", "record_credential_failure"},
		{429, "rate_limit_exceeded", "cooldown_model"},
		{503, "server_error", "skip_group"},
	} {
		t.Run(test.code, func(t *testing.T) {
			var rejected atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				rejected.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(test.status)
				if _, err := fmt.Fprintf(w, `{"error":{"code":%q}}`, test.code); err != nil {
					t.Error(err)
				}
			}))
			defer upstream.Close()
			backup := websocketSettingsUpstream(t, false)
			h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
			second := input.Groups[0]
			second.ID, second.Name = 2, "backup"
			second.Params = json.RawMessage(fmt.Sprintf(`{"base_url":%q}`, backup.URL+"/v1"))
			input.Groups = append(input.Groups, second)
			input.Credentials = append(input.Credentials, testCredentialConfig(2, 2))
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			registry := h.registry.(*state.CredentialRegistry)
			if err := registry.ReplaceCredentials([]state.CredentialEntry{
				testCredentialEntry(t, h.encryption, 1, 1, "fixture-key-one"),
				testCredentialEntry(t, h.encryption, 2, 2, "fixture-key-two"),
			}); err != nil {
				t.Fatal(err)
			}
			h.manager.Current().Settings.RetryCount = 1
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "test"}); err != nil {
				t.Fatal(err)
			}
			var event struct {
				Type string `json:"type"`
			}
			if err := conn.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			logs := waitWebsocketLogs(t, sink, 1)
			attempts := logs[0].Attempts
			if event.Type != "response.completed" || rejected.Load() != 1 || len(attempts) != 2 {
				t.Fatalf("handshake failed to select backup: event=%+v attempts=%+v", event, attempts)
			}
			if !attempts[0].WillRetry || attempts[0].Committed || string(attempts[0].Effect) != test.effect ||
				attempts[0].ErrorCode == "" || attempts[0].ErrorSummary == "" || attempts[1].CredentialID != 2 {
				t.Fatalf("handshake recovery accounting=%+v", attempts)
			}
			if test.status == 429 && !registry.ModelCooldowns(1, time.Now())["upstream"].After(time.Now().Add(100*time.Second)) {
				t.Fatal("handshake rate limit did not apply upstream cooldown")
			}
		})
	}
}

func TestWebsocketExhaustionKeepsLastUpstreamError(t *testing.T) {
	for _, retries := range []int{1, 2} {
		for _, rejection := range []string{"event", "handshake"} {
			t.Run(fmt.Sprintf("%s/retries=%d", rejection, retries), func(t *testing.T) {
				h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1", channel.CLIProxyAPI)
				addWebsocketErrorBackup(t, h, input)
				h.manager.Current().Settings.RetryCount = retries
				var opens atomic.Int32
				h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, _ ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
					if opens.Add(1) == 2 {
						return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent,
							Error: &execution.ErrorEvidence{Kind: execution.ErrorKindTransport, OriginHint: execution.ErrorOriginUpstream, Code: "websocket_connect_failed"}}
					}
					if rejection == "handshake" {
						return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent, Header: http.Header{"Retry-After": {"120"}},
							Error: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, OriginHint: execution.ErrorOriginUpstream, StatusCode: 429, Code: "usage_limit_reached"}}
					}
					session := &websocketScriptSession{done: make(chan struct{})}
					session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
						if err := emit(ctx, []byte(`{"type":"error","status":429,"error":{"code":"usage_limit_reached","message":"quota exhausted"}}`)); err != nil {
							t.Error(err)
						}
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent,
							Error: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, OriginHint: execution.ErrorOriginUpstream, StatusCode: 429, Code: "usage_limit_reached"}}
					}
					return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
				}}
				sink := &recordingRequestLogSink{}
				h.requestLogSink = sink
				server := httptest.NewServer(engine)
				defer server.Close()
				conn := dialGatewayWebsocket(t, server.URL)
				defer conn.Close()
				if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "test", "store": false}); err != nil {
					t.Fatal(err)
				}
				var event struct {
					Type   string                        `json:"type"`
					Status int                           `json:"status"`
					Error  accessKeyCostLimitClientError `json:"error"`
				}
				if err := conn.ReadJSON(&event); err != nil {
					t.Fatal(err)
				}
				logs := waitWebsocketLogs(t, sink, 1)
				attempts := logs[0].Attempts
				if event.Type != "error" || event.Status != http.StatusTooManyRequests || event.Error.Code != "usage_limit_reached" ||
					logs[0].StatusCode != http.StatusTooManyRequests || opens.Load() != 2 || len(attempts) != 2 {
					t.Fatalf("later connection failure replaced upstream rejection: response=%+v log=%+v", event, logs[0])
				}
				if !attempts[0].WillRetry || attempts[1].WillRetry || attempts[1].ErrorCode != "upstream_connect_failed" {
					t.Fatalf("attempt accounting=%+v", attempts)
				}
				if rejection == "event" && event.Error.Message != "quota exhausted" {
					t.Fatalf("upstream rejection message lost: %+v", event)
				}
				if rejection == "handshake" {
					until := attempts[0].CooldownUntil
					if !until.After(time.Now().Add(100*time.Second)) || event.Error.ResetsAt == nil || *event.Error.ResetsAt != (until.UnixMilli()+999)/1000 {
						t.Fatalf("handshake recovery information lost: response=%+v cooldown=%v", event, until)
					}
				}
			})
		}
	}
}

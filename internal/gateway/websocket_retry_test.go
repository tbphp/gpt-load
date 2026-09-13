package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

func TestWebsocketFirstRejectionRetry(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		retries, credentials, wantAttempts            int
		partial, unknown, exhausted, bound, multiplex bool
		host, invalid                                 bool
	}{
		{name: "quota switches credential", retries: 2, credentials: 2, wantAttempts: 2},
		{name: "host error skips group", retries: 2, credentials: 2, wantAttempts: 2, host: true},
		{name: "explicit request rejection stops", retries: 2, credentials: 2, wantAttempts: 1, invalid: true},
		{name: "zero budget preserves error", credentials: 2, wantAttempts: 1},
		{name: "no candidate preserves error", retries: 2, credentials: 1, wantAttempts: 1},
		{name: "exhausted budget preserves last error", retries: 1, credentials: 2, wantAttempts: 2, exhausted: true},
		{name: "output already committed", retries: 2, credentials: 2, wantAttempts: 1, partial: true},
		{name: "unknown replay safety", retries: 2, credentials: 2, wantAttempts: 1, unknown: true},
		{name: "continuation stays bound", retries: 2, credentials: 2, wantAttempts: 1, bound: true},
		{name: "multiplex stays bound", retries: 2, credentials: 2, wantAttempts: 1, multiplex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := channel.CLIProxyAPI
			if tc.multiplex {
				id = channel.OpenAI
			}
			h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1", id)
			input.SystemSettings = config.Settings{state.SettingRetryCount: tc.retries}
			registry := h.registry.(*state.CredentialRegistry)
			entries := []state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "first-key")}
			if tc.credentials == 2 {
				input.Credentials = append(input.Credentials, testCredentialConfig(2, 1))
				entries = append(entries, testCredentialEntry(t, h.encryption, 2, 1, "second-key"))
			}
			if tc.host {
				backup := input.Groups[0]
				backup.ID, backup.Name = 2, "backup"
				input.Groups = append(input.Groups, backup)
				input.Credentials = append(input.Credentials, testCredentialConfig(3, 2))
				entries = append(entries, testCredentialEntry(t, h.encryption, 3, 2, "third-key"))
			}
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			if err := registry.ReplaceCredentials(entries); err != nil {
				t.Fatal(err)
			}
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			var attempted []uint
			h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, in ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
				session := &websocketScriptSession{done: make(chan struct{})}
				turns := 0
				session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
					turns++
					if tc.bound && turns == 1 {
						_ = emit(ctx, websocketCompleted("resp_prior", ""))
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
					}
					attempted = append(attempted, in.Credential.ID)
					if in.Credential.ID != 1 && !tc.exhausted {
						_ = emit(ctx, websocketCompleted("resp_recovered", ""))
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
					}
					if tc.partial {
						_ = emit(ctx, []byte(`{"type":"response.created","response":{"id":"resp_partial","object":"response","status":"in_progress","model":"upstream"}}`))
					}
					status, errorType := 429, "usage_limit_reached"
					if tc.host {
						status, errorType = 503, "server_error"
					}
					if tc.invalid {
						status, errorType = 400, "invalid_parameter"
					}
					payload := []byte(fmt.Sprintf(`{"type":"error","status":%d,"error":{"type":%q,"message":"quota-%d %s","resets_in_seconds":60}}`, status, errorType, in.Credential.ID, in.APIKey))
					if err := emit(ctx, payload); err != nil {
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
					}
					safety := execution.ReplaySafety("")
					if tc.unknown {
						safety = execution.ReplaySafetyUnknown
					}
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: status, Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream, ReplaySafety: safety, Summary: "upstream rejected request"}}
				}
				return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
			}}
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			request := map[string]any{"type": "response.create", "model": "public", "input": "hello", "store": false}
			if tc.bound {
				if err := conn.WriteJSON(request); err != nil {
					t.Fatal(err)
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Fatal(err)
				}
				waitWebsocketLogs(t, sink, 1)
				request["previous_response_id"] = "resp_prior"
			}
			if err := conn.WriteJSON(request); err != nil {
				t.Fatal(err)
			}
			_, body, err := conn.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			if tc.partial {
				if !strings.Contains(string(body), "response.created") {
					t.Fatalf("missing prior output: %s", body)
				}
				_, body, err = conn.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
			}
			count := 1
			if tc.bound {
				count++
			}
			events := waitWebsocketLogs(t, sink, count)
			event := events[count-1]
			if len(attempted) != tc.wantAttempts || len(event.Attempts) != tc.wantAttempts {
				t.Fatalf("attempted=%v logs=%+v response=%s", attempted, event.Attempts, body)
			}
			if registry.ModelCooldowns(1, time.Now())["upstream"].After(time.Now()) != (!tc.host && !tc.invalid) {
				t.Fatal("unexpected model cooldown")
			}
			if tc.host && attempted[1] != 3 {
				t.Fatalf("failed group was not skipped: %v", attempted)
			}
			if until, _ := registry.CredentialCooldownUntil(1); !until.IsZero() {
				t.Fatal("entire credential cooled")
			}
			var response map[string]any
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			recovered := tc.wantAttempts == 2 && !tc.exhausted
			if recovered {
				if response["type"] != "response.completed" || !strings.Contains(string(body), "resp_recovered") {
					t.Fatalf("error leaked instead of recovery: %s", body)
				}
				if !event.Attempts[0].WillRetry || event.Attempts[0].Committed || attempted[0] == attempted[1] {
					t.Fatalf("retry accounting=%+v", event.Attempts)
				}
			} else {
				if response["type"] != "error" || !strings.Contains(string(body), fmt.Sprintf("quota-%d", attempted[len(attempted)-1])) || strings.Contains(string(body), "first-key") {
					t.Fatalf("original sanitized error lost: %s", body)
				}
			}
		})
	}
}

func TestWebsocketNativeFirstQuotaErrorRetries(t *testing.T) {
	var mu sync.Mutex
	var attempts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		key := r.Header.Get("Authorization")
		mu.Lock()
		attempts = append(attempts, key)
		mu.Unlock()
		body := websocketCompleted("resp_recovered", "")
		if key == "Bearer first-key" {
			body = []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`)
		}
		_ = conn.WriteMessage(websocket.TextMessage, body)
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.CLIProxyAPI)
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
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello", "store": false}); err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	logs := waitWebsocketLogs(t, sink, 1)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(attempts, ",") != "Bearer first-key,Bearer second-key" || !strings.Contains(string(body), "resp_recovered") {
		t.Fatalf("attempts=%v response=%s", attempts, body)
	}
	if len(logs[0].Attempts) != 2 || logs[0].Attempts[0].Committed || !logs[0].Attempts[0].WillRetry {
		t.Fatalf("logs=%+v", logs)
	}
}

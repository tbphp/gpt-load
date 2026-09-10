package gateway

import (
	"context"
	"encoding/json"
	"io"
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
	"gpt-load/internal/state"
)

func websocketSettingsUpstream(t *testing.T, active bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"resp_http","object":"response","status":"completed","model":"upstream","output":[]}`)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for index := 0; ; index++ {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			body := websocketCompleted("resp_"+strconv.Itoa(index), "")
			if active {
				body = []byte(`{"type":"response.created","response":{"id":"resp_running","object":"response","status":"in_progress","model":"upstream"}}`)
			}
			if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestWebsocketSwitchInheritanceLeavesHTTPAvailable(t *testing.T) {
	on, off := true, false
	for _, test := range []struct {
		name          string
		global, group *bool
		allowed       bool
	}{
		{"default", nil, nil, true},
		{"global off", &off, nil, false},
		{"group enables", &off, &on, true},
		{"group disables", &on, &off, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := websocketSettingsUpstream(t, false)
			h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
			if test.global != nil {
				input.SystemSettings = config.Settings{state.SettingResponsesWebsocketEnabled: *test.global}
			}
			if test.group != nil {
				input.Groups[0].Settings = config.Settings{state.SettingResponsesWebsocketEnabled: *test.group}
			}
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(engine)
			defer server.Close()
			conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", http.Header{"Authorization": {"Bearer gl-client"}})
			if conn != nil {
				defer conn.Close()
			}
			if response != nil && response.Body != nil {
				defer response.Body.Close()
			}
			if test.allowed {
				if err != nil {
					t.Fatal(err)
				}
				if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello"}); err != nil {
					t.Fatal(err)
				}
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				var event struct {
					Type string `json:"type"`
				}
				if err := conn.ReadJSON(&event); err != nil || event.Type != "response.completed" {
					t.Fatalf("enabled WS failed: %+v %v", event, err)
				}
			} else if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
				t.Fatalf("disabled WS upgraded: response=%v err=%v", response, err)
			}
			request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"public","input":"hello"}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer gl-client")
			response, err = server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("WS switch affected HTTP: status=%d", response.StatusCode)
			}
		})
	}
}

func TestWebsocketSwitchImmediatelyClosesAffectedConnections(t *testing.T) {
	for _, phase := range []string{"unbound", "idle", "active"} {
		t.Run(phase, func(t *testing.T) {
			upstream := websocketSettingsUpstream(t, phase == "active")
			h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			if phase != "unbound" {
				if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello"}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Fatal(err)
				}
			}
			input.SystemSettings = config.Settings{state.SettingResponsesWebsocketEnabled: false}
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
				t.Fatalf("disabled %s connection remained open: %v", phase, err)
			}
		})
	}
}

func TestWebsocketMissingBoundGroupClosesConnections(t *testing.T) {
	for _, change := range []string{"disabled", "deleted", "disabled and global WS off"} {
		for _, phase := range []string{"idle", "active"} {
			t.Run(change+"/"+phase, func(t *testing.T) {
				upstream := websocketSettingsUpstream(t, phase == "active")
				h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
				server := httptest.NewServer(engine)
				t.Cleanup(server.Close)
				conn := dialGatewayWebsocket(t, server.URL)
				if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello"}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Fatal(err)
				}
				if change == "deleted" {
					input.Groups = nil
					input.Credentials = nil
					h.registry.(*state.CredentialRegistry).RemoveGroup(1)
				} else {
					input.Groups[0].Enabled = false
				}
				if change == "disabled and global WS off" {
					input.SystemSettings = config.Settings{state.SettingResponsesWebsocketEnabled: false}
				}
				if _, err := h.manager.Publish(input); err != nil {
					t.Fatal(err)
				}
				if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
					t.Fatalf("%s connection remained open after group %s: %v", phase, change, err)
				}
			})
		}
	}
}

func TestWebsocketGroupOverrideSurvivesGlobalDisable(t *testing.T) {
	upstream := websocketSettingsUpstream(t, false)
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	input.Groups[0].Settings = config.Settings{state.SettingResponsesWebsocketEnabled: true}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	for index := 0; index < 2; index++ {
		if index == 1 {
			input.SystemSettings = config.Settings{state.SettingResponsesWebsocketEnabled: false}
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
		}
		if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello"}); err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type string `json:"type"`
		}
		if err := conn.ReadJSON(&event); err != nil || event.Type != "response.completed" {
			t.Fatalf("group override ignored: event=%+v err=%v", event, err)
		}
	}
}

func TestWebsocketSchedulerSkipsDisabledGroup(t *testing.T) {
	upstream := websocketSettingsUpstream(t, false)
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	input.Groups[0].Settings = config.Settings{state.SettingResponsesWebsocketEnabled: false}
	other := input.Groups[0]
	other.ID, other.Name, other.Settings = 2, "enabled", config.Settings{}
	input.Groups = append(input.Groups, other)
	input.Credentials = append(input.Credentials, testCredentialConfig(2, 2))
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	registry := h.registry.(*state.CredentialRegistry)
	if err := registry.ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "first-key"), testCredentialEntry(t, h.encryption, 2, 2, "second-key")}); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	if err := conn.WriteMessage(websocket.TextMessage, json.RawMessage(`{"type":"response.create","model":"public","input":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	logs := waitWebsocketLogs(t, sink, 1)
	if len(logs[0].Attempts) != 1 || logs[0].Attempts[0].GroupID != 2 {
		t.Fatalf("WS selected disabled group: %+v", logs[0].Attempts)
	}
}

func TestWebsocketDisableDuringBindingDoesNotDispatchOrMigrate(t *testing.T) {
	h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1", channel.OpenAI)
	other := input.Groups[0]
	other.ID, other.Name = 2, "still-enabled"
	input.Groups = append(input.Groups, other)
	input.Credentials = append(input.Credentials, testCredentialConfig(2, 2))
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	registry := h.registry.(*state.CredentialRegistry)
	if err := registry.ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "first-key"), testCredentialEntry(t, h.encryption, 2, 2, "second-key")}); err != nil {
		t.Fatal(err)
	}
	opening, release := make(chan struct{}), make(chan struct{})
	var sends atomic.Int32
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(ctx context.Context, input ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		if input.Group.ID != 1 {
			t.Errorf("unexpected binding group %d", input.Group.ID)
		}
		close(opening)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, execution.WebsocketResult{DispatchState: execution.DispatchNotSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
		}
		session := &websocketScriptSession{done: make(chan struct{}), turn: func(context.Context, []byte, func(context.Context, []byte) error) execution.WebsocketResult {
			sends.Add(1)
			return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
		}}
		return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	}}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opening:
	case <-time.After(time.Second):
		t.Fatal("binding did not start")
	}
	input.Groups[0].Settings = config.Settings{state.SettingResponsesWebsocketEnabled: false}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	close(release)
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
		t.Fatalf("stale binding remained usable: %v", err)
	}
	if sends.Load() != 0 {
		t.Fatal("disabled binding dispatched a model request")
	}
}

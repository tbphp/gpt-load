package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestHandlerParameterOverrideCacheKeepsOnlyLastGroup(t *testing.T) {
	failure := UpstreamResult{StatusCode: http.StatusUnauthorized, Header: make(http.Header),
		Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true}
	success := UpstreamResult{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true}
	forwarder := &scriptedForwarder{results: []UpstreamResult{failure, failure, failure, success, success}}
	settings := func(field string) config.Settings {
		return config.Settings{
			state.SettingRetryCount:         3,
			state.SettingParameterOverrides: []any{map[string]any{"set": map[string]any{field: true}}},
		}
	}
	engine, registry := newDialectGatewayEngineWithForwarder(t, protocol.OpenAICompletions, "public",
		dialect.NewSet(dialect.NewOpenAI()), forwarder,
		dialectGatewayGroup{id: 1, name: "first", upstreamURL: "https://first.example", apiKeys: []string{"sk-one", "sk-two", "sk-three"}, settings: settings("first")},
		dialectGatewayGroup{id: 2, name: "second", upstreamURL: "https://second.example", apiKeys: []string{"sk-four"}, settings: settings("second")},
	)
	// 暂时冷却第三个凭据，形成同分组重试、跨分组、返回原分组的顺序。
	until := time.Now().Add(time.Hour)
	forwarder.onCall = func(index int) {
		switch index {
		case 1:
			if !registry.SetCooldown(3, until) {
				t.Fatal("set credential cooldown")
			}
		case 2:
			if !registry.ClearCooldownIfMatch(3, until) {
				t.Fatal("clear credential cooldown")
			}
		}
	}
	for _, original := range []string{"one", "two"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"public","original":"`+original+`"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	}
	if len(forwarder.inputs) != 5 {
		t.Fatalf("attempts = %d, want 5", len(forwarder.inputs))
	}
	for index, groupID := range []uint{1, 1, 2, 1, 1} {
		input := forwarder.inputs[index]
		if input.Group.ID != groupID {
			t.Fatalf("attempt %d group = %d, want %d", index, input.Group.ID, groupID)
		}
		if groupID == 1 && bytes.Contains(input.Request.Body, []byte(`"second"`)) {
			t.Fatalf("attempt %d inherited another group's override", index)
		}
	}
	if forwarder.inputs[0].Request != forwarder.inputs[1].Request {
		t.Fatal("consecutive attempts in the same group did not reuse the prepared request")
	}
	if forwarder.inputs[0].Request == forwarder.inputs[3].Request {
		t.Fatal("previous group's prepared request remained cached after switching groups")
	}
	if forwarder.inputs[3].Request == forwarder.inputs[4].Request ||
		!bytes.Contains(forwarder.inputs[4].Request.Body, []byte(`"original":"two"`)) {
		t.Fatal("prepared request was shared across client requests")
	}
}

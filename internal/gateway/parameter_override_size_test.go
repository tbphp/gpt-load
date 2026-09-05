package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestHandlerParameterOverrideBodyLimits(t *testing.T) {
	for _, test := range []struct {
		name       string
		character  string
		count      int
		fallback   bool
		wantStatus int
		wantGroup  uint
	}{
		{name: "10 MiB request is overridden", character: "x", count: 10 << 20, wantStatus: http.StatusOK, wantGroup: 1},
		// JSON 编码将 < 展开成六字节转义，验证合并后而非入口超限。
		{name: "expanded body exceeds 128 MiB", character: "<", count: int(maxRequestBodyBytes/6) + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "another group can forward original body", character: "<", count: int(maxRequestBodyBytes/6) + 1, fallback: true, wantStatus: http.StatusOK, wantGroup: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
			}}}
			groups := []dialectGatewayGroup{{
				id: 1, name: "override", upstreamURL: "https://first.example", apiKeys: []string{"sk-first"},
				settings: config.Settings{state.SettingParameterOverrides: []any{
					map[string]any{"set": map[string]any{"temperature": 0.5}},
				}},
			}}
			if test.fallback {
				groups = append(groups, dialectGatewayGroup{id: 2, name: "fallback", upstreamURL: "https://second.example", apiKeys: []string{"sk-second"}})
			}
			engine, _ := newDialectGatewayEngineWithForwarder(t, protocol.OpenAICompletions, "public",
				dialect.NewSet(dialect.NewOpenAI()), forwarder, groups...)
			body := `{"model":"public","messages":[{"role":"user","content":"` + strings.Repeat(test.character, test.count) + `"}]}`
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("response = %d %s, want %d", response.Code, response.Body.String(), test.wantStatus)
			}
			if test.wantGroup == 0 {
				if len(forwarder.inputs) != 0 || !strings.Contains(response.Body.String(), `"code":"request_too_large"`) {
					t.Fatalf("oversized response = %s, attempts = %d", response.Body.String(), len(forwarder.inputs))
				}
				return
			}
			if len(forwarder.inputs) != 1 || forwarder.inputs[0].Group.ID != test.wantGroup {
				t.Fatalf("unexpected forwarded group or attempt count: %d", len(forwarder.inputs))
			}
			got := forwarder.inputs[0].Request.Body
			if test.fallback {
				if string(got) != body {
					t.Fatal("fallback did not use original body")
				}
			} else if !bytes.Contains(got, []byte(`"temperature":0.5`)) {
				t.Fatal("large request was forwarded without the configured override")
			}
		})
	}
}

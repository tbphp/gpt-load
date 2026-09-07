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
		name      string
		character string
		count     int
	}{
		{name: "10 MiB request is overridden", character: "x", count: 10 << 20},
		{name: "HTML characters stay compact", character: "<>&", count: (10 << 20) / 3},
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
			engine, _ := newDialectGatewayEngineWithForwarder(t, protocol.OpenAICompletions, "public",
				dialect.NewSet(dialect.NewOpenAI()), forwarder, groups...)
			body := `{"model":"public","messages":[{"role":"user","content":"` + strings.Repeat(test.character, test.count) + `"}]}`
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("response = %d %s, want 200", response.Code, response.Body.String())
			}
			if len(forwarder.inputs) != 1 || forwarder.inputs[0].Group.ID != 1 {
				t.Fatalf("unexpected forwarded group or attempt count: %d", len(forwarder.inputs))
			}
			got := forwarder.inputs[0].Request.Body
			if !bytes.Contains(got, []byte(`"temperature":0.5`)) {
				t.Fatal("large request was forwarded without the configured override")
			}
			if len(got) > len(body)+32 {
				t.Fatalf("request expanded unexpectedly: input = %d, output = %d", len(body), len(got))
			}
		})
	}
}

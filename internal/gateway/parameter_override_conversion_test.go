package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestParameterOverridesPreserveConvertedExtensions(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol protocol.Protocol
		path     string
		body     string
	}{
		{"Responses", protocol.OpenAIResponses, "/v1/responses", `{"model":"public-model","store":false,"input":"hello","client_extra":"drop"}`},
		{"Chat", protocol.OpenAICompletions, "/v1/chat/completions", `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"client_extra":"drop"}`},
		{"Gemini", protocol.Gemini, "/v1beta/models/public-model:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"client_extra":"drop"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			bodies := make(chan map[string]any, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				bodies <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"provider-model","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`)
			}))
			defer upstream.Close()
			params, err := json.Marshal(map[string]string{"base_url": upstream.URL})
			if err != nil {
				t.Fatal(err)
			}
			set := map[string]any{"cache_control": map[string]any{"type": "ephemeral"}, "top_k": 7, "custom_option": map[string]any{"enabled": true}}
			switch test.protocol {
			case protocol.OpenAIResponses:
				set["max_output_tokens"] = 16
			case protocol.OpenAICompletions:
				set["max_tokens"] = 16
			case protocol.Gemini:
				set["generationConfig"] = map[string]any{"maxOutputTokens": 16}
			}
			engine, _ := newDialectGatewayEngine(t, test.protocol, "public-model", dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses(), dialect.NewGemini()), dialectGatewayGroup{
				id: 1, name: "Anthropic", channelID: channel.Anthropic, params: params, apiKeys: []string{"synthetic-key"},
				models:   []state.ModelConfig{{ID: "provider-model", Alias: "public-model"}},
				settings: config.Settings{state.SettingParameterOverrides: []any{map[string]any{"match": map[string]any{"protocol": string(test.protocol)}, "set": set}}},
			})
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			select {
			case body := <-bodies:
				if body["cache_control"] == nil || body["top_k"] != float64(7) {
					t.Errorf("configured extensions lost: %#v", body)
				}
				if body["model"] != "provider-model" || body["max_tokens"] != float64(16) || body["max_output_tokens"] != nil || body["generationConfig"] != nil || body["client_extra"] != nil || body["custom_option"] != nil {
					t.Errorf("conversion semantics changed: %#v", body)
				}
			default:
				t.Fatal("upstream was not called")
			}
		})
	}
}

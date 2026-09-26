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
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestClineGatewayNormalizesWrappedCompletions(t *testing.T) {
	for _, responses := range []bool{false, true} {
		t.Run(map[bool]string{false: "Chat", true: "Responses"}[responses], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/api/v1/chat/completions" {
					t.Errorf("unexpected Cline path %s", request.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("ETag", `"wrapped"`)
				_, _ = io.WriteString(w, `{"success":true,"data":{"id":"cline-id","object":"chat.completion","created":1,"model":"deepseek/deepseek-v4.1-flash","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":31,"completion_tokens":72,"total_tokens":103}}}`)
			}))
			defer upstream.Close()
			value, path := protocol.OpenAICompletions, "/v1/chat/completions"
			body := `{"model":"public-model","messages":[{"role":"user","content":"ping"}]}`
			if responses {
				value, path = protocol.OpenAIResponses, "/v1/responses"
				body = `{"model":"public-model","input":"ping"}`
			}
			params, err := json.Marshal(map[string]string{"base_url": upstream.URL + "/api/v1"})
			if err != nil {
				t.Fatal(err)
			}
			engine, _ := newDialectGatewayEngine(t, value, "public-model", dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses()), dialectGatewayGroup{
				id: 1, name: "Cline", channelID: channel.Cline, params: params,
				apiKeys: []string{"synthetic-cline-key"},
				models:  []state.ModelConfig{{ID: "deepseek/deepseek-v4.1-flash", Alias: "public-model"}},
			})
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			var result struct {
				ID    string          `json:"id"`
				Model string          `json:"model"`
				Data  json.RawMessage `json:"data"`
				Store bool            `json:"store"`
				Usage struct {
					PromptTokens int `json:"prompt_tokens"`
					InputTokens  int `json:"input_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || result.ID != "cline-id" || result.Model != "public-model" || result.Data != nil || result.Store || !strings.Contains(recorder.Body.String(), "pong") || recorder.Header().Get("ETag") != "" {
				t.Fatalf("Cline gateway response = %d %s", recorder.Code, recorder.Body.String())
			}
			if responses && result.Usage.InputTokens != 31 || !responses && result.Usage.PromptTokens != 31 {
				t.Fatalf("Cline usage missing: %s", recorder.Body.String())
			}
		})
	}
}

func TestClineGatewayRejectsInvalidConvertedInput(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol protocol.Protocol
		path     string
		body     string
	}{
		{"Responses invalid input", protocol.OpenAIResponses, "/v1/responses", `{"model":"public-model","input":42}`},
		{"Anthropic empty messages", protocol.Anthropic, "/v1/messages", `{"model":"public-model","max_tokens":64,"messages":[]}`},
		{"Anthropic invalid messages", protocol.Anthropic, "/v1/messages", `{"model":"public-model","max_tokens":64,"messages":"invalid"}`},
		{"Gemini empty contents", protocol.Gemini, "/v1beta/models/public-model:generateContent", `{"contents":[]}`},
		{"Gemini invalid contents", protocol.Gemini, "/v1beta/models/public-model:generateContent", `{"contents":"invalid"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("invalid client input reached Cline")
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer upstream.Close()
			params, err := json.Marshal(map[string]string{"base_url": upstream.URL + "/api/v1"})
			if err != nil {
				t.Fatal(err)
			}
			groups := []dialectGatewayGroup{
				{id: 1, name: "Cline A", channelID: channel.Cline, params: params, apiKeys: []string{"synthetic-key-a"}},
				{id: 2, name: "Cline B", channelID: channel.Cline, params: params, apiKeys: []string{"synthetic-key-b"}},
			}
			engine, _ := newDialectGatewayEngine(t, test.protocol, "public-model",
				dialect.NewSet(dialect.NewOpenAIResponses(), dialect.NewAnthropic(), dialect.NewGemini()), groups...)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_protocol_request"`) {
				t.Fatalf("invalid input response = %d %s", recorder.Code, recorder.Body.String())
			}
			if attempts := recorder.Header().Get(debugHeaderAttempts); attempts != "1" {
				t.Fatalf("invalid input retried: attempts=%s", attempts)
			}
		})
	}
}

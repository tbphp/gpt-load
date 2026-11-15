package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestGatewayConvertedOpenRouterProviderPreferences(t *testing.T) {
	preferences := map[string]any{"only": []any{"modal/fp4"}, "allow_fallbacks": false}
	for _, test := range []struct {
		name     string
		channel  channel.ID
		provider any
		preserve bool
	}{
		{"OpenRouter preferences", channel.OpenRouter, preferences, true},
		{"OpenRouter empty preferences", channel.OpenRouter, map[string]any{}, true},
		{"OpenRouter scalar control", channel.OpenRouter, "anthropic", false},
		{"OpenRouter null control", channel.OpenRouter, nil, false},
		{"OpenRouter array control", channel.OpenRouter, []any{"modal/fp4"}, false},
		{"OpenAI control", channel.OpenAI, preferences, false},
	} {
		for _, client := range []protocol.Protocol{protocol.Anthropic, protocol.Gemini} {
			for _, configured := range []bool{false, true} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/configured=%t/stream=%t", test.name, client, configured, stream), func(t *testing.T) {
						var calls atomic.Int64
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer sk-upstream" {
								t.Errorf("unexpected upstream route or credential: %s %s", r.Method, r.URL.Path)
							}
							var body map[string]any
							if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
								t.Error(err)
								return
							}
							if body["model"] != "anthropic/claude-sonnet-4.6" || (body["stream"] == true) != stream {
								t.Errorf("model or stream changed: %#v", body)
							}
							if client == protocol.Anthropic {
								if body["max_output_tokens"] != float64(16) {
									t.Errorf("converted output limit = %#v", body["max_output_tokens"])
								}
								if test.channel == channel.OpenRouter {
									input := body["input"].([]any)
									blocks := input[0].(map[string]any)["content"].([]any)
									block := blocks[0].(map[string]any)
									if block["text"] != "shared prefix" || !reflect.DeepEqual(block["prompt_cache_breakpoint"], map[string]any{"mode": "explicit"}) {
										t.Errorf("cache marker not preserved: %#v", block)
									}
								}
							}
							if test.preserve {
								if !reflect.DeepEqual(body["provider"], test.provider) {
									t.Errorf("provider preferences = %#v, want %#v", body["provider"], test.provider)
								}
							} else if _, exists := body["provider"]; exists {
								t.Errorf("provider control reached upstream: %#v", body["provider"])
							}
							for _, key := range []string{"Provider", "fallback", "fallbacks", "api_key", "Authorization", "proxy-authorization", "x-api-key", "x-goog-api-key", "client_extra", "custom_option", "top_k"} {
								if _, exists := body[key]; exists {
									t.Errorf("control %q reached upstream", key)
								}
							}
							response := `{"id":"resp_1","object":"response","model":"anthropic/claude-sonnet-4.6","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
							w.Header().Set("Content-Type", "application/json")
							if stream {
								w.Header().Set("Content-Type", "text/event-stream")
								_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":"+response+"}\n\n")
							} else {
								_, _ = io.WriteString(w, response)
							}
						}))
						defer upstream.Close()
						controls := map[string]any{"provider": test.provider, "Provider": preferences, "fallback": "other", "fallbacks": []any{"other"}, "api_key": "client-key", "Authorization": "client-key", "proxy-authorization": "client-key", "x-api-key": "client-key", "x-goog-api-key": "client-key", "custom_option": true, "top_k": 7}
						body := map[string]any{"model": "public-model", "stream": stream, "client_extra": "drop"}
						path := "/v1/messages"
						if client == protocol.Anthropic {
							body["max_tokens"] = 16
							body["system"] = []any{map[string]any{"type": "text", "text": "shared prefix", "cache_control": map[string]any{"type": "ephemeral"}}}
							body["messages"] = []any{map[string]any{"role": "user", "content": "hello"}}
						} else {
							path = "/v1beta/models/public-model:generateContent"
							if stream {
								path = "/v1beta/models/public-model:streamGenerateContent?alt=sse"
							}
							body["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}
						}
						var settings config.Settings
						if configured {
							body["provider"] = "client-control"
							settings = config.Settings{state.SettingParameterOverrides: []any{map[string]any{"set": controls}}}
						} else {
							for key, value := range controls {
								body[key] = value
							}
						}
						params, _ := json.Marshal(map[string]string{"base_url": upstream.URL})
						engine, _ := newDialectGatewayEngine(t, client, "public-model", dialect.NewSet(dialect.NewAnthropic(), dialect.NewGemini()), dialectGatewayGroup{
							id: 1, name: "test", channelID: test.channel, params: params, apiKeys: []string{"sk-upstream"},
							models: []state.ModelConfig{{ID: "anthropic/claude-sonnet-4.6", Alias: "public-model"}}, settings: settings,
						})
						encoded, err := json.Marshal(body)
						if err != nil {
							t.Fatal(err)
						}
						request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
						request.Header.Set("Authorization", "Bearer gl-client")
						recorder := httptest.NewRecorder()
						engine.ServeHTTP(recorder, request)
						if recorder.Code != http.StatusOK || calls.Load() != 1 {
							t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls.Load(), recorder.Body)
						}
					})
				}
			}
		}
	}
}

func TestGatewayOpenRouterProviderKeepsToolPreparation(t *testing.T) {
	const originalPattern = `^a\0(?=b)b$`
	const normalizedPattern = `^a\x00b$`
	for _, model := range []string{"deepseek/deepseek-chat", "moonshotai/kimi-k2", "anthropic/claude-sonnet-4.6"} {
		for _, client := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
			for _, preferences := range []bool{false, true} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/provider=%t/stream=%t", model, client, preferences, stream), func(t *testing.T) {
						var calls atomic.Int64
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							var body map[string]any
							if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
								t.Error(err)
								return
							}
							if body["model"] != model || r.Header.Get("Authorization") != "Bearer sk-upstream" {
								t.Errorf("upstream model or credential changed: %#v", body)
							}
							tool := body["tools"].([]any)[0].(map[string]any)
							if client == protocol.OpenAICompletions {
								tool = tool["function"].(map[string]any)
							}
							properties := tool["parameters"].(map[string]any)["properties"].(map[string]any)
							value := properties["value"].(map[string]any)
							wantPattern, wantFormat := originalPattern, "password"
							converted := client == protocol.Anthropic || client == protocol.Gemini
							if converted && model != "anthropic/claude-sonnet-4.6" {
								wantPattern = normalizedPattern
							}
							if converted && model == "moonshotai/kimi-k2" {
								wantFormat = ""
							}
							if value["pattern"] != wantPattern {
								t.Errorf("pattern = %q, want %q", value["pattern"], wantPattern)
							}
							gotFormat, _ := value["format"].(string)
							if gotFormat != wantFormat {
								t.Errorf("format = %q, want %q", gotFormat, wantFormat)
							}
							if properties["email"].(map[string]any)["format"] != "email" {
								t.Error("allowed format lost")
							}
							provider, present := body["provider"]
							if present != preferences || present && !reflect.DeepEqual(provider, map[string]any{"only": []any{"modal/fp4"}}) {
								t.Errorf("provider = %#v", provider)
							}
							response := `{"id":"resp_1","object":"response","model":"test","status":"completed","output":[]}`
							streamResponse := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
							path := "/v1/responses"
							if client == protocol.OpenAICompletions {
								path = "/v1/chat/completions"
								response = `{"id":"chatcmpl-1","object":"chat.completion","model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
								streamResponse = "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
							}
							if r.URL.Path != path {
								t.Errorf("path = %s, want %s", r.URL.Path, path)
							}
							w.Header().Set("Content-Type", "application/json")
							if stream {
								w.Header().Set("Content-Type", "text/event-stream")
								_, _ = io.WriteString(w, streamResponse)
							} else {
								_, _ = io.WriteString(w, response)
							}
						}))
						defer upstream.Close()
						schema := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string", "pattern": originalPattern, "format": "password"}, "email": map[string]any{"type": "string", "format": "email"}}}
						body := map[string]any{"model": "public-model", "stream": stream}
						path := "/v1/responses"
						switch client {
						case protocol.OpenAICompletions:
							path = "/v1/chat/completions"
							body["messages"] = []any{map[string]any{"role": "user", "content": "hello"}}
							body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": schema}}}
						case protocol.OpenAIResponses:
							body["input"] = "hello"
							body["tools"] = []any{map[string]any{"type": "function", "name": "lookup", "parameters": schema}}
						case protocol.Anthropic:
							path = "/v1/messages"
							body["max_tokens"] = 16
							body["messages"] = []any{map[string]any{"role": "user", "content": "hello"}}
							body["tools"] = []any{map[string]any{"name": "lookup", "input_schema": schema}}
						case protocol.Gemini:
							path = "/v1beta/models/public-model:generateContent"
							if stream {
								path = "/v1beta/models/public-model:streamGenerateContent?alt=sse"
							}
							body["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}}
							body["tools"] = []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": "lookup", "parametersJsonSchema": schema}}}}
						}
						if preferences {
							body["provider"] = map[string]any{"only": []any{"modal/fp4"}}
						}
						params, _ := json.Marshal(map[string]string{"base_url": upstream.URL})
						engine, _ := newDialectGatewayEngine(t, client, "public-model", dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses(), dialect.NewAnthropic(), dialect.NewGemini()), dialectGatewayGroup{
							id: 1, name: "test", channelID: channel.OpenRouter, params: params, apiKeys: []string{"sk-upstream"}, models: []state.ModelConfig{{ID: model, Alias: "public-model"}},
						})
						encoded, err := json.Marshal(body)
						if err != nil {
							t.Fatal(err)
						}
						request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
						request.Header.Set("Authorization", "Bearer gl-client")
						recorder := httptest.NewRecorder()
						engine.ServeHTTP(recorder, request)
						if recorder.Code != http.StatusOK || calls.Load() != 1 {
							t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls.Load(), recorder.Body)
						}
					})
				}
			}
		}
	}
}

package gateway

import (
	"context"
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
	bifrostexecutor "gpt-load/internal/execution/bifrost"
	"gpt-load/internal/state"
)

func TestGatewayOpenRouterProviderPreferences(t *testing.T) {
	const preferences = `{"only":["modal/fp4"],"allow_fallbacks":false,"order":["modal/fp4"],"require_parameters":true}`
	for _, test := range []struct {
		name     string
		channel  channel.ID
		provider string
		preserve bool
	}{
		{"OpenRouter preferences", channel.OpenRouter, preferences, true},
		{"OpenRouter scalar control", channel.OpenRouter, `"anthropic"`, false},
		{"OpenRouter null control", channel.OpenRouter, `null`, false},
		{"OpenRouter array control", channel.OpenRouter, `["modal/fp4"]`, false},
		{"OpenRouter empty preferences", channel.OpenRouter, `{}`, true},
		{"OpenAI control", channel.OpenAI, preferences, false},
		{"DeepSeek control", channel.DeepSeek, preferences, false},
	} {
		for _, responses := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/responses=%t/stream=%t", test.name, responses, stream), func(t *testing.T) {
					path := "/v1/chat/completions"
					input := `"messages":[{"role":"user","content":"hello"}]`
					response := `{"id":"chatcmpl-1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
					streamResponse := "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
					if responses {
						path = "/v1/responses"
						input = `"input":"hello"`
						response = `{"id":"resp_1","object":"response","model":"upstream-model","status":"completed","output":[]}`
						streamResponse = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
					}
					var calls atomic.Int64
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, strings.TrimPrefix(path, "/v1")) || r.Header.Get("Authorization") != "Bearer sk-upstream" {
							t.Errorf("unexpected upstream route or credential: %s %s", r.Method, r.URL.Path)
						}
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							return
						}
						if string(body["model"]) != `"upstream-model"` || string(body["stream"]) != fmt.Sprint(stream) {
							t.Errorf("model or stream changed: %s", body)
						}
						if test.preserve {
							var got, want any
							if err := json.Unmarshal(body["provider"], &got); err != nil {
								t.Errorf("provider preferences missing: %s", body)
							}
							if err := json.Unmarshal([]byte(test.provider), &want); err != nil {
								t.Error(err)
								return
							}
							if !reflect.DeepEqual(got, want) {
								t.Errorf("provider preferences = %#v, want %#v", got, want)
							}
						} else if _, exists := body["provider"]; exists {
							t.Errorf("provider control reached upstream: %s", body["provider"])
						}
						for _, key := range []string{"Provider", "fallback", "fallbacks", "api_key", "Authorization", "proxy-authorization", "x-api-key", "x-goog-api-key"} {
							if _, exists := body[key]; exists {
								t.Errorf("control %q reached upstream", key)
							}
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
					runtime, err := bifrostexecutor.NewRuntime(context.Background(), channel.NewRegistry())
					if err != nil {
						t.Fatal(err)
					}
					defer runtime.Shutdown()
					engine, handler, manager, _ := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(runtime), &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-upstream")
					handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses())
					params, _ := json.Marshal(map[string]string{"base_url": upstream.URL})
					_, err = manager.Publish(state.CompileInput{
						ChannelRegistry: channel.NewRegistry(),
						Groups:          []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "test", ChannelID: test.channel, Params: params, Models: []state.ModelConfig{{ID: "upstream-model", Alias: "public"}}, Enabled: true}},
						Credentials:     []state.CredentialConfig{testCredentialConfig(1, 1)},
						AccessKeys:      []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
					})
					if err != nil {
						t.Fatal(err)
					}
					body := fmt.Sprintf(`{"model":"public",%s,"stream":%t,"provider":%s,"Provider":{"only":["other"]},"fallback":"other","fallbacks":["other"],"api_key":"client-key","Authorization":"client-key","proxy-authorization":"client-key","x-api-key":"client-key","x-goog-api-key":"client-key"}`, input, stream, test.provider)
					request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
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

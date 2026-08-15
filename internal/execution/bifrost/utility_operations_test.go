package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOfficialNativeListModelsPreservesWireResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		channelID    channel.ID
		protocol     protocol.Protocol
		path         string
		body         string
		credential   string
		runtime      func(*testing.T, string) *testRuntime
		wantUpstream string
		wantProtocol protocol.Protocol
	}{
		{name: "openai", channelID: channel.OpenAI, protocol: protocol.OpenAICompletions, path: "/v1/models", body: `{"object":"list","data":[{"id":"gpt-test","object":"model"}],"vendor":{"precise":1.2300}}`, credential: "Authorization", wantUpstream: "/v1/models", wantProtocol: protocol.OpenAICompletions, runtime: func(t *testing.T, base string) *testRuntime {
			return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
		}},
		{name: "openai responses models", channelID: channel.OpenAI, protocol: protocol.OpenAIResponses, path: "/v1/models", body: `{"object":"list","data":[{"id":"gpt-test","object":"model"}]}`, credential: "Authorization", wantUpstream: "/v1/models", wantProtocol: protocol.OpenAICompletions, runtime: func(t *testing.T, base string) *testRuntime {
			return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
		}},
		{name: "anthropic", channelID: channel.Anthropic, protocol: protocol.Anthropic, path: "/v1/models", body: `{"data":[{"id":"claude-test","type":"model"}],"has_more":false,"vendor":{"precise":1.2300}}`, credential: "X-Api-Key", wantUpstream: "/v1/models", wantProtocol: protocol.Anthropic, runtime: func(t *testing.T, base string) *testRuntime {
			return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
		}},
		{name: "gemini", channelID: channel.Gemini, protocol: protocol.Gemini, path: "/v1beta/models", body: `{"models":[{"name":"models/gemini-test"}],"vendor":{"precise":1.2300}}`, credential: "X-Goog-Api-Key", wantUpstream: "/v1beta/models", wantProtocol: protocol.Gemini, runtime: func(t *testing.T, base string) *testRuntime {
			return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.Method != http.MethodGet || request.URL.Path != test.wantUpstream || request.URL.Query().Get("page") != "next" {
					t.Errorf("target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
				}
				wantCredential := testAPIKey
				if test.credential == "Authorization" {
					wantCredential = "Bearer " + testAPIKey
				}
				if request.Header.Get(test.credential) != wantCredential {
					t.Errorf("credential header %s = %q", test.credential, request.Header.Get(test.credential))
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "models-"+test.name)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()

			runtime := test.runtime(t, server.URL)
			spec := utilitySpec(test.channelID, test.protocol, execution.OperationListModels, http.MethodGet, test.path, nil)
			spec.Query.Set("page", "next")
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if string(result.Body) != test.body || result.UpstreamRequestID != "models-"+test.name ||
				result.UpstreamProtocol != test.wantProtocol {
				t.Fatalf("result/body = %+v/%s", result, result.Body)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d", calls.Load())
			}
		})
	}
}

func TestOfficialNativeProbeUsesSelectedModelAndProtocolTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		channelID channel.ID
		protocol  protocol.Protocol
		runtime   func(*testing.T, string) *testRuntime
		response  string
		assert    func(*testing.T, *http.Request, map[string]any)
	}{
		{name: "openai", channelID: channel.OpenAI, protocol: protocol.OpenAICompletions, runtime: func(t *testing.T, base string) *testRuntime {
			return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
		}, response: `{"id":"chat_1","object":"chat.completion","created":1,"model":"probe-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, assert: func(t *testing.T, request *http.Request, payload map[string]any) {
			if request.URL.Path != "/v1/chat/completions" || payload["model"] != "probe-upstream" {
				t.Errorf("OpenAI probe = %s %#v", request.URL.Path, payload)
			}
			if stream, exists := payload["stream"]; exists && stream != false {
				t.Errorf("OpenAI probe stream = %#v", stream)
			}
		}},
		{name: "anthropic", channelID: channel.Anthropic, protocol: protocol.Anthropic, runtime: func(t *testing.T, base string) *testRuntime {
			return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
		}, response: `{"id":"msg_1","type":"message","role":"assistant","model":"probe-upstream","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, assert: func(t *testing.T, request *http.Request, payload map[string]any) {
			if request.URL.Path != "/v1/messages" || payload["model"] != "probe-upstream" {
				t.Errorf("Anthropic probe = %s %#v", request.URL.Path, payload)
			}
			if stream, exists := payload["stream"]; exists && stream != false {
				t.Errorf("Anthropic probe stream = %#v", stream)
			}
			if payload["max_tokens"] != float64(1) {
				t.Errorf("Anthropic max_tokens = %#v", payload["max_tokens"])
			}
		}},
		{name: "gemini", channelID: channel.Gemini, protocol: protocol.Gemini, runtime: func(t *testing.T, base string) *testRuntime {
			return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
		}, response: `{"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"probe-upstream"}`, assert: func(t *testing.T, request *http.Request, payload map[string]any) {
			if request.URL.Path != "/v1beta/models/probe-upstream:generateContent" {
				t.Errorf("Gemini probe path = %s", request.URL.Path)
			}
			generationConfig, _ := payload["generationConfig"].(map[string]any)
			if generationConfig["maxOutputTokens"] != float64(1) {
				t.Errorf("Gemini generationConfig = %#v", generationConfig)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(request.Body)
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Errorf("decode probe: %v", err)
				}
				test.assert(t, request, payload)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			runtime := test.runtime(t, server.URL)
			spec := utilitySpec(test.channelID, test.protocol, execution.OperationProbe, "", "", nil)
			spec.ClientModel = "probe-client"
			spec.UpstreamModel = "probe-upstream"
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if calls.Load() != 1 || result.Error != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
			}
		})
	}
}

func utilitySpec(channelID channel.ID, clientProtocol protocol.Protocol, operation execution.Operation, method, path string, body []byte) execution.AttemptSpec {
	spec := execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "utility-request", AttemptID: "utility-attempt", Sequence: 1,
		ChannelID: string(channelID), ClientProtocol: clientProtocol, Operation: operation,
		Method: method, Path: path, Query: make(map[string][]string), Header: http.Header{"Authorization": {"Bearer client"}}, Body: body,
		TargetConfig: json.RawMessage(`{}`), Credential: execution.NewCredentialSnapshot(10, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	})
	if _, err := channel.NewRegistry().ResolveExecutionTarget(channelID, spec.TargetConfig); err == nil {
		return freezeTestAttempt(spec)
	}
	return spec
}

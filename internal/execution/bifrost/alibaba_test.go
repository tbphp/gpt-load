package bifrost

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// 百炼的 Chat 与 Responses 在 OpenAI 兼容前缀下，Anthropic 在同域的 /apps/anthropic 下。
func TestAlibabaPresetSendsNativeProtocolRequests(t *testing.T) {
	for _, test := range []struct {
		name         string
		protocol     protocol.Protocol
		operation    execution.Operation
		method       string
		path         string
		upstreamPath string
		upstream     protocol.Protocol
		body         string
		response     string
		// bodyField 非空时要求上游收到的请求体保留该字段。
		bodyField string
	}{
		{
			name: "chat", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion,
			method: http.MethodPost, path: "/v1/chat/completions", upstreamPath: "/compatible-mode/v1/chat/completions",
			upstream: protocol.OpenAICompletions,
			body:     `{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`,
			response: `{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "chat probe", protocol: protocol.OpenAICompletions, operation: execution.OperationProbe,
			upstreamPath: "/compatible-mode/v1/chat/completions", upstream: protocol.OpenAICompletions,
			response: `{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "responses", protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate,
			method: http.MethodPost, path: "/v1/responses", upstreamPath: "/compatible-mode/v1/responses",
			upstream:  protocol.OpenAIResponses,
			body:      `{"model":"client-model","input":"hello","store":false,"tools":[{"type":"web_search"}]}`,
			bodyField: "tools",
			response:  `{"id":"resp_1","object":"response","status":"completed","model":"upstream-model","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "responses probe", protocol: protocol.OpenAIResponses, operation: execution.OperationProbe,
			upstreamPath: "/compatible-mode/v1/responses", upstream: protocol.OpenAIResponses,
			response: `{"id":"resp_1","object":"response","status":"completed","model":"upstream-model","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "anthropic", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion,
			method: http.MethodPost, path: "/v1/messages", upstreamPath: "/apps/anthropic/v1/messages",
			upstream:  protocol.Anthropic,
			body:      `{"model":"client-model","max_tokens":16,"output_config":{"effort":"low"},"messages":[{"role":"user","content":"hello"}]}`,
			bodyField: "output_config",
			response:  `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			name: "anthropic probe", protocol: protocol.Anthropic, operation: execution.OperationProbe,
			upstreamPath: "/apps/anthropic/v1/messages", upstream: protocol.Anthropic,
			response: `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			// Anthropic 端点不提供模型列表，继续读取 OpenAI 兼容前缀下的列表。
			name: "anthropic models", protocol: protocol.Anthropic, operation: execution.OperationListModels,
			method: http.MethodGet, path: "/v1/models", upstreamPath: "/compatible-mode/v1/models",
			upstream: protocol.OpenAICompletions,
			response: `{"object":"list","data":[{"id":"upstream-model","object":"model","created":1,"owned_by":"system"}]}`,
		},
		{
			name: "gemini", protocol: protocol.Gemini, operation: execution.OperationChatCompletion,
			method: http.MethodPost, path: "/v1beta/models/client-model:generateContent",
			upstreamPath: "/compatible-mode/v1/chat/completions", upstream: protocol.OpenAICompletions,
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			response: `{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			// 转换路由的凭据测试拿到的是 Chat 响应，不能按原生 Gemini 格式校验。
			name: "gemini probe", protocol: protocol.Gemini, operation: execution.OperationProbe,
			upstreamPath: "/compatible-mode/v1/chat/completions", upstream: protocol.OpenAICompletions,
			response: `{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantMethod := test.method
				if wantMethod == "" {
					wantMethod = http.MethodPost
				}
				if r.Method != wantMethod || r.URL.Path != test.upstreamPath {
					t.Errorf("unexpected target %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer "+testAPIKey && r.Header.Get("X-Api-Key") != testAPIKey {
					t.Error("selected credential did not replace client authentication")
				}
				if test.bodyField != "" {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					var payload map[string]json.RawMessage
					if err := json.Unmarshal(body, &payload); err != nil {
						t.Error(err)
						return
					}
					if string(payload["model"]) != `"upstream-model"` || payload[test.bodyField] == nil {
						t.Errorf("unexpected upstream body %s", body)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()
			manager, target := newOpenCodeManagerForTest(t, channel.Alibaba, server.URL+"/compatible-mode/v1")
			spec := utilitySpec(channel.Alibaba, test.protocol, test.operation, test.method, test.path, []byte(test.body))
			spec.ClientModel, spec.UpstreamModel = "client-model", "upstream-model"
			spec.TargetConfig = target.TargetConfig
			if test.body == "" {
				spec.Body = nil
			} else {
				spec.Header.Set("Content-Type", "application/json")
			}
			result := manager.Execute(t.Context(), freezeTestAttempt(spec))
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || result.UpstreamProtocol != test.upstream {
				t.Fatalf("result=%+v error=%+v contract=%v", result, result.Error, err)
			}
		})
	}
}

func TestAlibabaAnthropicBaseURLKeepsHostAndPrefix(t *testing.T) {
	for base, want := range map[string]string{
		"https://dashscope.aliyuncs.com/compatible-mode/v1":                  "https://dashscope.aliyuncs.com/apps/anthropic",
		"https://dashscope-intl.aliyuncs.com/compatible-mode/v1/":            "https://dashscope-intl.aliyuncs.com/apps/anthropic",
		"https://ws-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1": "https://ws-example.cn-beijing.maas.aliyuncs.com/apps/anthropic",
		"https://proxy.example/dashscope/compatible-mode/v1":                 "https://proxy.example/dashscope/apps/anthropic",
		"https://proxy.example/v1":                                           "https://proxy.example/apps/anthropic",
		"https://proxy.example":                                              "https://proxy.example/apps/anthropic",
	} {
		if got := alibabaAnthropicBaseURL(base); got != want {
			t.Errorf("alibabaAnthropicBaseURL(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestAlibabaPresetStreamsNativeProtocols(t *testing.T) {
	for _, test := range []struct {
		protocol     protocol.Protocol
		operation    execution.Operation
		path         string
		upstreamPath string
		body         string
		wire         string
		terminal     string
	}{
		{
			protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate,
			path: "/v1/responses", upstreamPath: "/compatible-mode/v1/responses",
			body:     `{"model":"client-model","input":"hello","store":false,"stream":true}`,
			terminal: "response.completed",
			wire:     "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"upstream-model\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
		},
		{
			protocol: protocol.Anthropic, operation: execution.OperationChatCompletion,
			path: "/v1/messages", upstreamPath: "/apps/anthropic/v1/messages",
			body:     `{"model":"client-model","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			terminal: "message_stop",
			wire: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"upstream-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"cache_read_input_tokens\":0}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.upstreamPath {
					t.Errorf("unexpected target %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.wire)
			}))
			defer server.Close()
			manager, target := newOpenCodeManagerForTest(t, channel.Alibaba, server.URL+"/compatible-mode/v1")
			spec := utilitySpec(channel.Alibaba, test.protocol, test.operation, http.MethodPost, test.path, []byte(test.body))
			spec.ClientModel, spec.UpstreamModel = "client-model", "upstream-model"
			spec.TargetConfig = target.TargetConfig
			spec.Header.Set("Content-Type", "application/json")
			var output strings.Builder
			result := manager.ExecuteStream(t.Context(), freezeTestAttempt(spec), func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					output.Write(event.Data)
				}
				return nil
			})
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || result.UpstreamProtocol != test.protocol {
				t.Fatalf("result=%+v error=%+v contract=%v", result, result.Error, err)
			}
			// 网关会把流里的模型名换回客户端模型，这里只确认终止事件原样送达。
			if !strings.Contains(output.String(), test.terminal) || !strings.Contains(output.String(), `"client-model"`) {
				t.Fatalf("unexpected stream %q", output.String())
			}
		})
	}
}

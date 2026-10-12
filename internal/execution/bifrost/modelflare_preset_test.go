package bifrost

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// Modelflare 的 OpenAI、Anthropic 与 Gemini 接口都挂在站点根下，预设地址不带 /v1。
func TestModelflarePresetSendsNativeRequestsBelowSiteRoot(t *testing.T) {
	for _, test := range []struct {
		protocol   protocol.Protocol
		operations []execution.Operation
		path       string
		// upstreamPath 为空时与客户端路径相同；Gemini 的模型在路径里，会换成上游模型。
		upstreamPath string
		body         string
		response     string
	}{
		{
			protocol: protocol.OpenAICompletions, path: "/v1/chat/completions",
			operations: []execution.Operation{execution.OperationChatCompletion, execution.OperationProbe},
			body:       `{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`,
			response:   `{"id":"chat_1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			// 上游 Responses 只支持流式，不声明 Responses 凭据测试。
			protocol: protocol.OpenAIResponses, path: "/v1/responses",
			operations: []execution.Operation{execution.OperationResponsesCreate},
			body:       `{"model":"client-model","input":"hello","store":false}`,
			response:   `{"id":"resp_1","object":"response","status":"completed","model":"upstream-model","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			protocol: protocol.OpenAIImages, path: "/v1/images/generations",
			operations: []execution.Operation{execution.OperationImagesGenerate},
			body:       `{"model":"client-model","prompt":"hello"}`,
			response:   `{"created":1,"data":[{"url":"https://images.example/result.png"}]}`,
		},
		{
			protocol: protocol.Gemini, path: "/v1beta/models/client-model:generateContent",
			upstreamPath: "/v1beta/models/upstream-model:generateContent",
			operations:   []execution.Operation{execution.OperationChatCompletion, execution.OperationProbe},
			body:         `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			response:     `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"upstream-model"}`,
		},
		{
			protocol: protocol.Anthropic, path: "/v1/messages",
			operations: []execution.Operation{execution.OperationChatCompletion, execution.OperationProbe},
			body:       `{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`,
			response:   `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
	} {
		for _, operation := range test.operations {
			t.Run(string(test.protocol)+"/"+string(operation), func(t *testing.T) {
				wantPath := test.path
				if test.upstreamPath != "" {
					wantPath = test.upstreamPath
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != wantPath {
						t.Errorf("unexpected target %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, test.response)
				}))
				defer server.Close()
				manager, target := newOpenCodeManagerForTest(t, channel.Modelflare, server.URL)
				spec := utilitySpec(channel.Modelflare, test.protocol, operation, http.MethodPost, test.path, []byte(test.body))
				spec.ClientModel, spec.UpstreamModel = "client-model", "upstream-model"
				spec.TargetConfig = target.TargetConfig
				spec.Header.Set("Content-Type", "application/json")
				if operation == execution.OperationProbe {
					spec.Method, spec.Path = "", ""
					spec.Body = nil
				}
				result := manager.Execute(t.Context(), freezeTestAttempt(spec))
				if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || result.UpstreamProtocol != test.protocol {
					t.Fatalf("result=%+v error=%+v contract=%v", result, result.Error, err)
				}
			})
		}
	}
}

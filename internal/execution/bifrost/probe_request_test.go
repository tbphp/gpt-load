package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// TestGatewayProbeAcceptsStreamingUpstreamResponse 覆盖上游忽略 stream=false 仍返回 SSE 的情况。
// 此时凭据与模型都能正常调用，探测不应判为失败。
func TestGatewayProbeAcceptsStreamingUpstreamResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\","+
			"\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	manager, resolved := newGPTLoadManagerForTest(t, server.URL+"/gateway")
	spec := execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "probe-request", AttemptID: "probe-attempt", Sequence: 1,
		ChannelID:      string(channel.GPTLoad),
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationProbe,
		ClientModel:    "deepseek-v4.1-flash",
		UpstreamModel:  "deepseek-v4.1-flash",
		Header:         http.Header{},
		TargetConfig:   resolved.TargetConfig,
		Credential:     execution.NewCredentialSnapshot(10, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	})
	spec = freezeTestAttempt(spec)
	result := manager.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("streaming probe result = %+v, validation=%v", result, err)
	}
}

// TestProbeRequestAsksForNonStreamingCompletion 保证凭据测试显式请求非流式响应。
// Bifrost 的请求体对 stream 使用 omitempty，缺省时上游可能直接返回 SSE，
// 而探测按非流式解析，就会得到「failed to unmarshal response from provider API」。
func TestProbeRequestAsksForNonStreamingCompletion(t *testing.T) {
	t.Parallel()
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/gateway/v1/chat/completions" {
			t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
		}
		captured, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-probe","object":"chat.completion","created":1,`+
			`"model":"deepseek-v4.1-flash","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	manager, resolved := newGPTLoadManagerForTest(t, server.URL+"/gateway")
	spec := execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "probe-request", AttemptID: "probe-attempt", Sequence: 1,
		ChannelID:      string(channel.GPTLoad),
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationProbe,
		ClientModel:    "deepseek-v4.1-flash",
		UpstreamModel:  "deepseek-v4.1-flash",
		Header:         http.Header{},
		TargetConfig:   resolved.TargetConfig,
		Credential:     execution.NewCredentialSnapshot(10, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	})
	spec = freezeTestAttempt(spec)
	result := manager.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("probe result = %+v, validation=%v", result, err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("decode probe request body %s: %v", captured, err)
	}
	stream, ok := payload["stream"]
	if !ok {
		t.Fatalf("probe request body = %s, want an explicit stream field", captured)
	}
	if !bytes.Equal(bytes.TrimSpace(stream), []byte("false")) {
		t.Fatalf("probe request stream = %s, want false", stream)
	}
}

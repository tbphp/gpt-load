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
	"gpt-load/internal/usage"
)

func TestOfficialAnthropicMessagesNativeUnary(t *testing.T) {
	t.Parallel()

	const responseBody = `{"id": "msg_1", "type":"message", "role":"assistant", "model":"served-claude", "content":[{"type":"text","text":"ok"}], "stop_reason":"end_turn", "usage":{"input_tokens":6,"cache_read_input_tokens":4,"output_tokens":3}, "vendor_response":{"precise":1.2300}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
			t.Errorf("request target = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Api-Key") != testAPIKey || request.Header.Get("Authorization") != "" {
			t.Errorf("credential headers = x-api-key:%q authorization:%q", request.Header.Get("X-Api-Key"), request.Header.Get("Authorization"))
		}
		if request.Header.Get("Anthropic-Version") == "" {
			t.Error("anthropic-version was not injected")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "upstream-claude" || payload["stream"] != false {
			t.Errorf("request controls = %#v", payload)
		}
		for _, field := range []string{"provider", "fallbacks", "authorization", "api_key"} {
			if _, exists := payload[field]; exists {
				t.Errorf("control field %q reached upstream", field)
			}
		}
		if !bytes.Contains(body, []byte(`"precise":1.2300`)) {
			t.Errorf("unknown field changed: %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Request-Id", "anthropic-unary")
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL})
	spec := anthropicSpec(false)
	spec.ClientModel = spec.UpstreamModel
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if string(result.Body) != responseBody || result.UpstreamRequestID != "anthropic-unary" {
		t.Fatalf("result/body = %+v/%s", result, result.Body)
	}
	assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 3})
}

func TestOfficialAnthropicMessagesNativeStream(t *testing.T) {
	t.Parallel()

	const rawStream = "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_stream\",\"model\":\"served-claude\",\"usage\":{\"input_tokens\":6,\"cache_read_input_tokens\":4,\"output_tokens\":0}}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"},\"vendor\":{\"precise\":1.2300}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if payload["model"] != "upstream-claude" || payload["stream"] != true {
			t.Errorf("request controls = %#v", payload)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Request-Id", "anthropic-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, rawStream)
		writer.(http.Flusher).Flush()
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL})
	spec := anthropicSpec(true)
	spec.ClientModel = spec.UpstreamModel
	var events []execution.StreamEvent
	result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	assertRawStream(t, result, events, rawStream, usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 3})
}

func TestOfficialGeminiGenerateNativeUnary(t *testing.T) {
	t.Parallel()

	const responseBody = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}], "modelVersion":"served-gemini", "usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":13}, "vendorResponse":{"precise":1.2300}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1beta/models/upstream-gemini:generateContent" {
			t.Errorf("request target = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Goog-Api-Key") != testAPIKey || request.Header.Get("Authorization") != "" {
			t.Errorf("credential headers = goog:%q auth:%q", request.Header.Get("X-Goog-Api-Key"), request.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"provider", "fallbacks", "api_key"} {
			if _, exists := payload[field]; exists {
				t.Errorf("body control field %q reached upstream", field)
			}
		}
		if payload["model"] != "client-gemini" || payload["stream"] != true {
			t.Errorf("native Gemini body fields changed: %#v", payload)
		}
		if !bytes.Contains(body, []byte(`"precise":1.2300`)) {
			t.Errorf("unknown field changed: %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Goog-Request-Id", "gemini-unary")
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	spec := geminiSpec(false)
	spec.ClientModel = spec.UpstreamModel
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if string(result.Body) != responseBody || result.UpstreamRequestID != "gemini-unary" {
		t.Fatalf("result/body = %+v/%s", result, result.Body)
	}
	assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 3})
}

func TestOfficialGeminiGenerateNativeStream(t *testing.T) {
	t.Parallel()

	const rawStream = "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"cachedContentTokenCount\":4,\"candidatesTokenCount\":3,\"totalTokenCount\":13},\"vendor\":{\"precise\":1.2300}}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1beta/models/upstream-gemini:streamGenerateContent" || request.URL.Query().Get("alt") != "sse" {
			t.Errorf("request target = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("X-Goog-Request-Id", "gemini-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, rawStream)
		writer.(http.Flusher).Flush()
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	spec := geminiSpec(true)
	spec.ClientModel = spec.UpstreamModel
	spec.Query.Set("alt", "sse")
	var events []execution.StreamEvent
	result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	assertRawStream(t, result, events, rawStream, usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 3})
}

func anthropicSpec(stream bool) execution.AttemptSpec {
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "anthropic-request", AttemptID: "anthropic-attempt", Sequence: 1,
		ChannelID: string(channel.Anthropic), ClientProtocol: protocol.Anthropic,
		Operation: execution.OperationChatCompletion, ClientModel: "client-claude", UpstreamModel: "upstream-claude",
		Method: http.MethodPost, Path: "/v1/messages", Query: make(map[string][]string),
		Header:       http.Header{"Authorization": {"Bearer client"}, "X-Api-Key": {"client"}, "X-Test-Header": {"forward-me"}},
		Body:         []byte(`{"model":"client-claude","stream":true,"provider":"injected","fallbacks":["other"],"authorization":"body","api_key":"body","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"vendor_extension":{"precise":1.2300}}`),
		TargetConfig: json.RawMessage(`{}`), Credential: execution.NewCredentialSnapshot(8, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	})
}

func geminiSpec(stream bool) execution.AttemptSpec {
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "gemini-request", AttemptID: "gemini-attempt", Sequence: 1,
		ChannelID: string(channel.Gemini), ClientProtocol: protocol.Gemini,
		Operation: execution.OperationChatCompletion, ClientModel: "client-gemini", UpstreamModel: "upstream-gemini",
		Method: http.MethodPost, Path: "/v1beta/models/client-gemini:" + action, Query: make(map[string][]string),
		Header:       http.Header{"Authorization": {"Bearer client"}, "X-Goog-Api-Key": {"client"}, "X-Test-Header": {"forward-me"}},
		Body:         []byte(`{"model":"client-gemini","stream":true,"provider":"injected","fallbacks":["other"],"api_key":"body","contents":[{"role":"user","parts":[{"text":"hello"}]}],"vendor_extension":{"precise":1.2300}}`),
		TargetConfig: json.RawMessage(`{}`), Credential: execution.NewCredentialSnapshot(9, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	})
}

func newProtocolTestRuntime(t *testing.T, options runtimeOptions) *Runtime {
	t.Helper()
	runtime, err := newRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(runtime.Shutdown)
	return runtime
}

func assertRawStream(t *testing.T, result execution.StreamResult, events []execution.StreamEvent, raw string, want usage.Tokens) {
	t.Helper()
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if len(events) < 3 || events[0].Kind != execution.StreamEventReady {
		t.Fatalf("events = %+v", events)
	}
	var data bytes.Buffer
	var sawUsage bool
	for _, event := range events[1:] {
		if event.Kind == execution.StreamEventData {
			data.Write(event.Data)
		}
		if event.Kind == execution.StreamEventUsage {
			sawUsage = true
			assertUsage(t, event.Usage, want)
		}
	}
	if data.String() != raw || !sawUsage {
		t.Fatalf("stream data/usage = %q/%t", data.String(), sawUsage)
	}
	assertUsage(t, result.Usage, want)
}

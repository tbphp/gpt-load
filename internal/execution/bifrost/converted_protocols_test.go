package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
)

func TestConvertedResponsesUnaryUsesCanonicalSDKConverters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		channelID      channel.ID
		clientProtocol protocol.Protocol
		operation      execution.Operation
		clientPath     string
		clientBody     []byte
		upstreamPath   string
		credentialName string
		upstreamBody   string
		upstreamAPI    execution.UpstreamAPI
		modelInPath    bool
		assertClient   func(*testing.T, map[string]any)
		runtime        func(*testing.T, string) *testRuntime
	}{
		{
			name:           "Anthropic client to OpenAI",
			channelID:      channel.OpenAI,
			clientProtocol: protocol.Anthropic,
			operation:      execution.OperationChatCompletion,
			clientPath:     "/v1/messages",
			clientBody:     []byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
			upstreamPath:   "/v1/responses",
			credentialName: "Authorization",
			upstreamBody:   openAIResponsesConvertedFixture,
			upstreamAPI:    execution.UpstreamAPIOpenAIResponses,
			assertClient: func(t *testing.T, body map[string]any) {
				if body["type"] != "message" || body["role"] != "assistant" || body["model"] != "client-model" {
					t.Errorf("Anthropic response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
			},
		},
		{
			name:           "Anthropic client to Gemini",
			channelID:      channel.Gemini,
			clientProtocol: protocol.Anthropic,
			operation:      execution.OperationChatCompletion,
			clientPath:     "/v1/messages",
			clientBody:     []byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
			upstreamPath:   "/v1beta/models/upstream-model:generateContent",
			credentialName: "X-Goog-Api-Key",
			upstreamBody:   geminiResponsesConvertedFixture,
			upstreamAPI:    execution.UpstreamAPIGeminiGenerateContent,
			modelInPath:    true,
			assertClient: func(t *testing.T, body map[string]any) {
				if body["type"] != "message" || body["role"] != "assistant" || body["model"] != "client-model" {
					t.Errorf("Anthropic response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
			},
		},
		{
			name:           "Gemini client to OpenAI",
			channelID:      channel.OpenAI,
			clientProtocol: protocol.Gemini,
			operation:      execution.OperationChatCompletion,
			clientPath:     "/v1beta/models/client-model:generateContent",
			clientBody:     []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
			upstreamPath:   "/v1/responses",
			credentialName: "Authorization",
			upstreamBody:   openAIResponsesConvertedFixture,
			upstreamAPI:    execution.UpstreamAPIOpenAIResponses,
			assertClient: func(t *testing.T, body map[string]any) {
				candidates, _ := body["candidates"].([]any)
				if len(candidates) != 1 || body["modelVersion"] != "client-model" {
					t.Errorf("Gemini response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
			},
		},
		{
			name:           "Gemini client to Anthropic",
			channelID:      channel.Anthropic,
			clientProtocol: protocol.Gemini,
			operation:      execution.OperationChatCompletion,
			clientPath:     "/v1beta/models/client-model:generateContent",
			clientBody:     []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
			upstreamPath:   "/v1/messages",
			credentialName: "X-Api-Key",
			upstreamBody:   anthropicResponsesConvertedFixture,
			upstreamAPI:    execution.UpstreamAPIAnthropicMessages,
			assertClient: func(t *testing.T, body map[string]any) {
				candidates, _ := body["candidates"].([]any)
				if len(candidates) != 1 || body["modelVersion"] != "client-model" {
					t.Errorf("Gemini response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
			},
		},
		{
			name:           "OpenAI Responses client to Anthropic",
			channelID:      channel.Anthropic,
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			clientPath:     "/v1/responses",
			clientBody:     []byte(`{"model":"client-model","input":"hello","max_output_tokens":16}`),
			upstreamPath:   "/v1/messages",
			credentialName: "X-Api-Key",
			upstreamBody:   anthropicResponsesConvertedFixture,
			upstreamAPI:    execution.UpstreamAPIAnthropicMessages,
			assertClient: func(t *testing.T, body map[string]any) {
				if body["object"] != "response" || body["model"] != "client-model" {
					t.Errorf("OpenAI Responses response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
			},
		},
		{
			name:           "OpenAI Responses client to Gemini",
			channelID:      channel.Gemini,
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			clientPath:     "/v1/responses",
			clientBody:     []byte(`{"model":"client-model","input":"hello","max_output_tokens":16}`),
			upstreamPath:   "/v1beta/models/upstream-model:generateContent",
			credentialName: "X-Goog-Api-Key",
			upstreamBody:   geminiResponsesConvertedFixture,
			upstreamAPI:    execution.UpstreamAPIGeminiGenerateContent,
			modelInPath:    true,
			assertClient: func(t *testing.T, body map[string]any) {
				if body["object"] != "response" || body["model"] != "client-model" {
					t.Errorf("OpenAI Responses response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != test.upstreamPath || request.URL.Query().Get("trace") != "kept" {
					t.Errorf("upstream target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
				}
				wantCredential := testAPIKey
				if test.credentialName == "Authorization" {
					wantCredential = "Bearer " + testAPIKey
				}
				if request.Header.Get(test.credentialName) != wantCredential || request.Header.Get("X-Api-Key") == "client-secret" {
					t.Errorf("credential headers = %#v", request.Header)
				}
				requestBody, _ := io.ReadAll(request.Body)
				var upstreamRequest map[string]any
				if err := json.Unmarshal(requestBody, &upstreamRequest); err != nil {
					t.Fatalf("decode upstream request: %v", err)
				}
				if (!test.modelInPath && upstreamRequest["model"] != "upstream-model") || upstreamRequest["provider"] != nil || upstreamRequest["fallbacks"] != nil {
					t.Errorf("upstream request = %#v", upstreamRequest)
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "converted-request")
				_, _ = io.WriteString(writer, test.upstreamBody)
			}))
			defer server.Close()

			runtime := test.runtime(t, server.URL)
			spec := convertedSpec(test.channelID, test.clientProtocol, test.operation, test.clientPath, test.clientBody)
			spec.Query.Set("trace", "kept")
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			// Gemini's bounded-response path in Bifrost v1.7.7 does not expose
			// provider response headers for small responses.
			requestIDMissingOnlyForGemini := test.channelID == channel.Gemini && result.UpstreamRequestID == ""
			if result.Error != nil || result.StatusCode != http.StatusOK ||
				(result.UpstreamRequestID != "converted-request" && !requestIDMissingOnlyForGemini) {
				t.Fatalf("result = %+v, body=%s", result, result.Body)
			}
			if result.UpstreamAPI != test.upstreamAPI {
				t.Fatalf("result upstream API = %q, want %q", result.UpstreamAPI, test.upstreamAPI)
			}
			var clientBody map[string]any
			if err := json.Unmarshal(result.Body, &clientBody); err != nil {
				t.Fatalf("decode client response: %v; body=%s", err, result.Body)
			}
			test.assertClient(t, clientBody)
			if result.Usage == nil {
				t.Fatal("normalized usage is missing")
			}
		})
	}
}

func TestConvertedAttemptReportsAppliedReasoning(t *testing.T) {
	t.Parallel()

	wireBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		wireBody <- body
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, anthropicResponsesConvertedFixture)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{
		allowPrivateNetwork: true,
		anthropicBaseURL:    server.URL,
	})
	longInput := strings.Repeat("conversation context ", (256<<10)/len("conversation context ")+1)
	body, err := json.Marshal(map[string]any{
		"model":             "client-model",
		"input":             longInput,
		"max_output_tokens": 4096,
		"reasoning": map[string]any{
			"mode":   "pro",
			"effort": "high",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= 256<<10 {
		t.Fatalf("request body = %d bytes, want > 256 KiB", len(body))
	}
	spec := convertedSpec(
		channel.Anthropic,
		protocol.OpenAIResponses,
		execution.OperationResponsesCreate,
		"/v1/responses",
		body,
	)
	result := runtime.Execute(context.Background(), spec)
	if result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, error = %+v", result, result.Error)
	}
	want := inspectAnthropicWireReasoning(t, <-wireBody)
	if !reflect.DeepEqual(result.AppliedReasoning, want) {
		t.Fatalf("result applied reasoning = %#v, wire reasoning = %#v", result.AppliedReasoning, want)
	}
	if result.AppliedReasoning == nil || result.AppliedReasoning.Mode == "pro" ||
		result.AppliedReasoning.Effort == "high" {
		t.Fatalf("result retained canonical reasoning instead of wire values: %#v", result.AppliedReasoning)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, fragment := range []string{
		`"applied_reasoning":{`,
		`"mode":"enabled"`,
		`"budget_tokens":`,
	} {
		if !bytes.Contains(raw, []byte(fragment)) {
			t.Fatalf("result JSON = %s, want %s", raw, fragment)
		}
	}
}

func TestConvertedAttemptReportsWireReasoningOnProviderError(t *testing.T) {
	t.Parallel()

	wireBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		wireBody <- body
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, `{"type":"error","error":{"type":"rate_limit_error","message":"retry later"}}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{
		allowPrivateNetwork: true,
		anthropicBaseURL:    server.URL,
	})
	spec := convertedSpec(
		channel.Anthropic,
		protocol.OpenAIResponses,
		execution.OperationResponsesCreate,
		"/v1/responses",
		[]byte(`{"model":"client-model","input":"secret prompt","max_output_tokens":4096,"reasoning":{"mode":"pro","effort":"high"}}`),
	)
	result := runtime.Execute(context.Background(), spec)
	if result.Error == nil || result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("result = %+v, error = %+v", result, result.Error)
	}
	want := inspectAnthropicWireReasoning(t, <-wireBody)
	if !reflect.DeepEqual(result.AppliedReasoning, want) {
		t.Fatalf("result applied reasoning = %#v, wire reasoning = %#v", result.AppliedReasoning, want)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if bytes.Contains(raw, []byte("raw_request")) || bytes.Contains(raw, []byte("secret prompt")) {
		t.Fatalf("result leaked raw request: %s", raw)
	}
}

func TestNativeOpenAIStyleTypedTargetsPreserveSafeQuery(t *testing.T) {
	const rawQuery = "trace=%2F&cursor=next"
	for _, test := range []struct {
		name       string
		provider   channel.ProviderKind
		responses  bool
		stream     bool
		wantTarget string
	}{
		{name: "deepseek chat", provider: channel.ProviderDeepSeek, wantTarget: "/chat/completions?" + rawQuery},
		{name: "deepseek responses stream", provider: channel.ProviderDeepSeek, responses: true, stream: true, wantTarget: "/chat/completions?" + rawQuery},
		{name: "openrouter chat stream", provider: channel.ProviderOpenRouter, stream: true, wantTarget: "/v1/chat/completions?" + rawQuery},
		{name: "openrouter responses", provider: channel.ProviderOpenRouter, responses: true, wantTarget: "/v1/responses?" + rawQuery},
		{name: "groq chat", provider: channel.ProviderGroq, wantTarget: "/v1/chat/completions?" + rawQuery},
		{name: "groq responses", provider: channel.ProviderGroq, responses: true, wantTarget: "/v1/chat/completions?" + rawQuery},
		{name: "xai chat", provider: channel.ProviderXAI, wantTarget: "/v1/chat/completions?" + rawQuery},
		{name: "xai responses stream", provider: channel.ProviderXAI, responses: true, stream: true, wantTarget: "/v1/responses?" + rawQuery},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, err := convertedTypedTarget(test.provider, "", "model", test.responses, test.stream, rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			if target != test.wantTarget {
				t.Fatalf("target = %q, want %q", target, test.wantTarget)
			}
		})
	}

	for _, test := range []struct {
		name      string
		provider  channel.ProviderKind
		responses bool
	}{
		{name: "groq chat stream", provider: channel.ProviderGroq},
		{name: "groq responses stream", provider: channel.ProviderGroq, responses: true},
		{name: "xai chat stream", provider: channel.ProviderXAI},
	} {
		t.Run(test.name+" rejects unsupported query", func(t *testing.T) {
			if _, err := convertedTypedTarget(test.provider, "", "model", test.responses, true, rawQuery); err == nil {
				t.Fatal("convertedTypedTarget() error = nil")
			}
		})
	}
}

func TestNativeOpenAIStyleListModelsTargetsPreserveSafeQuery(t *testing.T) {
	const rawQuery = "cursor=%2F&limit=10"
	for _, test := range []struct {
		provider channel.ProviderKind
		want     string
	}{
		{provider: channel.ProviderDeepSeek, want: "/models?" + rawQuery},
		{provider: channel.ProviderOpenRouter, want: "/v1/models?" + rawQuery},
		{provider: channel.ProviderGroq, want: "/v1/models?" + rawQuery},
		{provider: channel.ProviderXAI, want: "/v1/models?" + rawQuery},
	} {
		t.Run(string(test.provider), func(t *testing.T) {
			target, err := convertedListModelsTarget(test.provider, "", rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			if target != test.want {
				t.Fatalf("target = %q, want %q", target, test.want)
			}
		})
	}
}

func TestConvertedOpenAIChatUnaryUsesSelectedProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		channelID      channel.ID
		upstreamPath   string
		credentialName string
		upstreamBody   string
		modelInPath    bool
		runtime        func(*testing.T, string) *testRuntime
	}{
		{
			name: "Anthropic", channelID: channel.Anthropic, upstreamPath: "/v1/messages",
			credentialName: "X-Api-Key", upstreamBody: anthropicResponsesConvertedFixture,
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
			},
		},
		{
			name: "Gemini", channelID: channel.Gemini, upstreamPath: "/v1beta/models/upstream-model:generateContent",
			credentialName: "X-Goog-Api-Key", upstreamBody: geminiResponsesConvertedFixture, modelInPath: true,
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.upstreamPath || request.URL.Query().Get("trace") != "kept" {
					t.Errorf("target = %s?%s", request.URL.Path, request.URL.RawQuery)
				}
				if request.Header.Get(test.credentialName) != testAPIKey {
					t.Errorf("credential %s = %q", test.credentialName, request.Header.Get(test.credentialName))
				}
				body, _ := io.ReadAll(request.Body)
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if !test.modelInPath && payload["model"] != "upstream-model" {
					t.Errorf("request model = %#v", payload["model"])
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.upstreamBody)
			}))
			defer server.Close()

			runtime := test.runtime(t, server.URL)
			spec := convertedSpec(
				test.channelID,
				protocol.OpenAICompletions,
				execution.OperationChatCompletion,
				"/v1/chat/completions",
				[]byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`),
			)
			spec.Query.Set("trace", "kept")
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			var body map[string]any
			if err := json.Unmarshal(result.Body, &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			choices, _ := body["choices"].([]any)
			if result.Error != nil || len(choices) != 1 || result.Usage == nil || body["model"] != "client-model" {
				t.Fatalf("result/body = %+v/%#v", result, body)
			}
		})
	}
}

func TestConvertedResponsesStreamUsesClientProtocolFraming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		channelID      channel.ID
		clientProtocol protocol.Protocol
		operation      execution.Operation
		clientPath     string
		clientBody     []byte
		upstreamPath   string
		credentialName string
		upstreamStream string
		upstreamModel  string
		upstreamAPI    execution.UpstreamAPI
		streamInBody   bool
		wantReasoning  bool
		wantFragments  []string
		runtime        func(*testing.T, string) *testRuntime
	}{
		{
			name: "Anthropic client from OpenAI stream", channelID: channel.OpenAI,
			clientProtocol: protocol.Anthropic, operation: execution.OperationChatCompletion,
			clientPath: "/v1/messages", clientBody: []byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
			upstreamPath: "/v1/responses", credentialName: "Authorization", upstreamStream: openAIResponsesStreamFixture,
			upstreamModel: "gpt-upstream",
			upstreamAPI:   execution.UpstreamAPIOpenAIResponses,
			streamInBody:  true,
			wantFragments: []string{"event: message_start", `"type":"message_start"`, "hello", "event: message_stop"},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
			},
		},
		{
			name: "Gemini client from OpenAI stream", channelID: channel.OpenAI,
			clientProtocol: protocol.Gemini, operation: execution.OperationChatCompletion,
			clientPath: "/v1beta/models/client-model:streamGenerateContent", clientBody: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
			upstreamPath: "/v1/responses", credentialName: "Authorization", upstreamStream: openAIResponsesStreamFixture,
			upstreamModel: "gpt-upstream",
			upstreamAPI:   execution.UpstreamAPIOpenAIResponses,
			streamInBody:  true,
			wantFragments: []string{"data: ", "hello", "usageMetadata"},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
			},
		},
		{
			name: "OpenAI Responses client from Anthropic stream", channelID: channel.Anthropic,
			clientProtocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate,
			clientPath: "/v1/responses", clientBody: []byte(`{"model":"client-model","input":"hello","max_output_tokens":4096,"reasoning":{"mode":"pro","effort":"high"}}`),
			upstreamPath: "/v1/messages", credentialName: "X-Api-Key", upstreamStream: anthropicResponsesStreamFixture,
			upstreamModel: "claude-upstream",
			upstreamAPI:   execution.UpstreamAPIAnthropicMessages,
			streamInBody:  true,
			wantReasoning: true,
			wantFragments: []string{"event: response.created", "response.output_text.delta", "hello", "event: response.completed"},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
			},
		},
		{
			name: "OpenAI Responses client from Gemini stream", channelID: channel.Gemini,
			clientProtocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate,
			clientPath: "/v1/responses", clientBody: []byte(`{"model":"client-model","input":"hello","max_output_tokens":16}`),
			upstreamPath: "/v1beta/models/upstream-model:streamGenerateContent", credentialName: "X-Goog-Api-Key", upstreamStream: geminiResponsesStreamFixture,
			upstreamModel: "gemini-upstream",
			upstreamAPI:   execution.UpstreamAPIGeminiGenerateContent,
			wantFragments: []string{"event: response.created", "response.output_text.delta", "hello", "event: response.completed"},
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wireBody := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.upstreamPath || request.URL.Query().Get("trace") != "kept" {
					t.Errorf("target = %s?%s", request.URL.Path, request.URL.RawQuery)
				}
				wantCredential := testAPIKey
				if test.credentialName == "Authorization" {
					wantCredential = "Bearer " + testAPIKey
				}
				if request.Header.Get(test.credentialName) != wantCredential {
					t.Errorf("credential = %q", request.Header.Get(test.credentialName))
				}
				body, _ := io.ReadAll(request.Body)
				wireBody <- body
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil || (test.streamInBody && payload["stream"] != true) {
					t.Errorf("stream request = %s err=%v", body, err)
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.Header().Set("X-Request-Id", "converted-stream")
				_, _ = io.WriteString(writer, test.upstreamStream)
			}))
			defer server.Close()

			runtime := test.runtime(t, server.URL)
			spec := convertedSpec(test.channelID, test.clientProtocol, test.operation, test.clientPath, test.clientBody)
			spec.Query.Set("trace", "kept")
			var data bytes.Buffer
			ready := 0
			usageEvents := 0
			result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
				if err := event.Validate(); err != nil {
					t.Fatalf("event validation: %v; event=%+v", err, event)
				}
				switch event.Kind {
				case execution.StreamEventReady:
					ready++
					if data.Len() != 0 || event.UpstreamRequestID != "converted-stream" {
						t.Errorf("ready ordering/id = %d/%q", data.Len(), event.UpstreamRequestID)
					}
				case execution.StreamEventData:
					data.Write(event.Data)
				case execution.StreamEventUsage:
					usageEvents++
				}
				return nil
			})
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v data=%s", err, result, data.String())
			}
			if result.Error != nil || ready != 1 || usageEvents == 0 || result.Usage == nil {
				t.Fatalf("result/events = %+v ready=%d usage=%d data=%s", result, ready, usageEvents, data.String())
			}
			if result.UpstreamAPI != test.upstreamAPI {
				t.Fatalf("result upstream API = %q, want %q", result.UpstreamAPI, test.upstreamAPI)
			}
			upstreamWireBody := <-wireBody
			if test.wantReasoning {
				want := inspectAnthropicWireReasoning(t, upstreamWireBody)
				if !reflect.DeepEqual(result.AppliedReasoning, want) {
					t.Fatalf("result applied reasoning = %#v, wire reasoning = %#v", result.AppliedReasoning, want)
				}
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(data.String(), fragment) {
					t.Errorf("stream missing %q: %s", fragment, data.String())
				}
			}
			if !strings.Contains(data.String(), `"client-model"`) || strings.Contains(data.String(), `"`+test.upstreamModel+`"`) {
				t.Errorf("client model alias was not applied: %s", data.String())
			}
			if strings.Contains(data.String(), "raw_request") {
				t.Errorf("stream leaked raw request: %s", data.String())
			}
		})
	}
}

func inspectAnthropicWireReasoning(t *testing.T, body []byte) *reasoning.Config {
	t.Helper()
	var wire struct {
		Thinking *struct {
			Type         string `json:"type"`
			BudgetTokens *int64 `json:"budget_tokens"`
		} `json:"thinking"`
		OutputConfig *struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode Anthropic wire request: %v; body=%s", err, body)
	}
	if wire.Thinking == nil && wire.OutputConfig == nil {
		return nil
	}
	result := &reasoning.Config{}
	if wire.Thinking != nil {
		result.Mode = wire.Thinking.Type
		result.BudgetTokens = wire.Thinking.BudgetTokens
	}
	if wire.OutputConfig != nil {
		result.Effort = wire.OutputConfig.Effort
	}
	return result
}

func TestConvertedOpenAIChatStreamUsesSelectedProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		channelID      channel.ID
		upstreamPath   string
		credentialName string
		upstreamStream string
		upstreamModel  string
		runtime        func(*testing.T, string) *testRuntime
	}{
		{
			name: "Anthropic", channelID: channel.Anthropic, upstreamPath: "/v1/messages",
			credentialName: "X-Api-Key", upstreamStream: anthropicResponsesStreamFixture, upstreamModel: "claude-upstream",
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
			},
		},
		{
			name: "Gemini", channelID: channel.Gemini, upstreamPath: "/v1beta/models/upstream-model:streamGenerateContent",
			credentialName: "X-Goog-Api-Key", upstreamStream: geminiResponsesStreamFixture, upstreamModel: "gemini-upstream",
			runtime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.upstreamPath || request.URL.Query().Get("trace") != "kept" {
					t.Errorf("target = %s?%s", request.URL.Path, request.URL.RawQuery)
				}
				if request.Header.Get(test.credentialName) != testAPIKey {
					t.Errorf("credential = %q", request.Header.Get(test.credentialName))
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.upstreamStream)
			}))
			defer server.Close()

			runtime := test.runtime(t, server.URL)
			spec := convertedSpec(
				test.channelID,
				protocol.OpenAICompletions,
				execution.OperationChatCompletion,
				"/v1/chat/completions",
				[]byte(`{"model":"client-model","stream":true,"messages":[{"role":"user","content":"hello"}]}`),
			)
			spec.Query.Set("trace", "kept")
			var data bytes.Buffer
			usageEvents := 0
			result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					data.Write(event.Data)
				}
				if event.Kind == execution.StreamEventUsage {
					usageEvents++
				}
				return nil
			})
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v data=%s", err, result, data.String())
			}
			if result.Error != nil || !strings.Contains(data.String(), "hello") || !strings.Contains(data.String(), "data: [DONE]\n\n") ||
				!strings.Contains(data.String(), `"client-model"`) || strings.Contains(data.String(), `"`+test.upstreamModel+`"`) || usageEvents == 0 {
				t.Fatalf("result/data/usage = %+v/%s/%d", result, data.String(), usageEvents)
			}
		})
	}
}

func TestConvertedOpenAIChatStreamWaitsForFirstClientFrame(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(writer, geminiResponsesStreamFixture)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{
		allowPrivateNetwork: true,
		geminiBaseURL:       server.URL + "/v1beta",
	})
	spec := convertedSpec(
		channel.Gemini,
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		"/v1/chat/completions",
		[]byte(`{"model":"client-model","stream":true,"messages":[{"role":"user","content":"hello"}]}`),
	)
	spec.Timeouts.FirstByte = 20 * time.Millisecond
	spec.Timeouts.Request = time.Second
	var events []execution.StreamEvent
	started := time.Now()
	result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})

	if elapsed := time.Since(started); elapsed > 90*time.Millisecond {
		t.Fatalf("first-frame timeout returned after %s", elapsed)
	}
	if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout ||
		result.ResponseStarted || len(events) != 0 {
		t.Fatalf("result/events = %+v/%+v", result, events)
	}
}

func TestConvertedOpenAIChatStreamRejectsMissingProviderTerminal(t *testing.T) {
	t.Parallel()

	const truncatedGeminiStream = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}],\"role\":\"model\"},\"index\":0}],\"modelVersion\":\"gemini-upstream\",\"responseId\":\"resp_1\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, truncatedGeminiStream)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{
		allowPrivateNetwork: true,
		geminiBaseURL:       server.URL + "/v1beta",
	})
	spec := convertedSpec(
		channel.Gemini,
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		"/v1/chat/completions",
		[]byte(`{"model":"client-model","stream":true,"messages":[{"role":"user","content":"hello"}]}`),
	)
	var data bytes.Buffer
	result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventData {
			data.Write(event.Data)
		}
		return nil
	})

	if result.Error == nil || result.Error.Kind != execution.ErrorKindTransport || !result.ResponseStarted {
		t.Fatalf("truncated result = %+v; data=%s", result, data.String())
	}
	if !strings.Contains(data.String(), "hello") || strings.Contains(data.String(), "[DONE]") {
		t.Fatalf("truncated data = %s", data.String())
	}
}

func TestConvertedResponsesStreamHonorsCancellationAndUpstreamError(t *testing.T) {
	t.Parallel()

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "event: response.created\n"+
				"data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":123,\"status\":\"in_progress\",\"model\":\"gpt-upstream\",\"output\":[]}}\n\n")
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			<-request.Context().Done()
		}))
		defer server.Close()

		runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: server.URL})
		spec := convertedSpec(
			channel.OpenAI,
			protocol.Anthropic,
			execution.OperationChatCompletion,
			"/v1/messages",
			[]byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
		)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var data bytes.Buffer
		result := runtime.ExecuteStream(ctx, spec, func(event execution.StreamEvent) error {
			if event.Kind == execution.StreamEventData {
				data.Write(event.Data)
				cancel()
			}
			return nil
		})
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v; result=%+v", err, result)
		}
		if result.Error == nil || result.Error.Kind != execution.ErrorKindCanceled || !result.ResponseStarted {
			t.Fatalf("canceled result = %+v", result)
		}
		if strings.Contains(data.String(), "message_stop") {
			t.Fatalf("terminal event emitted after cancellation: %s", data.String())
		}
	})

	t.Run("upstream HTTP error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("X-Request-Id", "converted-429")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(writer, `{"error":{"type":"rate_limit_error","code":"rate_limited","message":"rejected `+testAPIKey+`"}}`)
		}))
		defer server.Close()

		runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: server.URL})
		spec := convertedSpec(
			channel.OpenAI,
			protocol.Anthropic,
			execution.OperationChatCompletion,
			"/v1/messages",
			[]byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
		)
		var events []execution.StreamEvent
		result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
			events = append(events, event.Clone())
			return nil
		})
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v; result=%+v", err, result)
		}
		if result.Error == nil || result.Error.Kind != execution.ErrorKindHTTP || result.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("HTTP error result = %+v", result)
		}
		if len(events) != 0 {
			t.Fatalf("converted error emitted client data: %+v", events)
		}
		assertNoPrivateLeak(t, result, testAPIKey, "gptload-custom-")
	})
}

func convertedSpec(channelID channel.ID, clientProtocol protocol.Protocol, operation execution.Operation, path string, body []byte) execution.AttemptSpec {
	spec := execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "converted-request", AttemptID: "converted-attempt", Sequence: 1,
		ChannelID: string(channelID), ClientProtocol: clientProtocol, Operation: operation,
		ClientModel: "client-model", UpstreamModel: "upstream-model",
		Method: http.MethodPost, Path: path, Query: make(map[string][]string),
		Header: http.Header{"Authorization": {"Bearer client-secret"}, "X-Api-Key": {"client-secret"}, "X-Test": {"kept"}},
		Body:   body, TargetConfig: json.RawMessage(`{}`),
		Credential: execution.NewCredentialSnapshot(12, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	})
	if _, err := channel.NewRegistry().ResolveExecutionTarget(channelID, spec.TargetConfig); err == nil {
		return freezeTestAttempt(spec)
	}
	return spec
}

const openAIResponsesConvertedFixture = `{"id":"resp_1","object":"response","created_at":123,"status":"completed","model":"gpt-upstream","output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":4,"input_tokens_details":{"cached_tokens":1},"output_tokens":2,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":6}}`

const anthropicResponsesConvertedFixture = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":4,"cache_read_input_tokens":1,"output_tokens":2}}`

const geminiResponsesConvertedFixture = `{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":4,"cachedContentTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":6},"modelVersion":"gemini-upstream","responseId":"resp_1"}`

const openAIResponsesStreamFixture = "event: response.created\n" +
	"data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":123,\"status\":\"in_progress\",\"model\":\"gpt-upstream\",\"output\":[]}}\n\n" +
	"event: response.output_text.delta\n" +
	"data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"output_index\":0,\"content_index\":0,\"item_id\":\"msg_1\",\"delta\":\"hello\"}\n\n" +
	"event: response.completed\n" +
	"data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":" + openAIResponsesConvertedFixture + "}\n\n" +
	"data: [DONE]\n\n"

const anthropicResponsesStreamFixture = "event: message_start\n" +
	"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-upstream\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":4,\"cache_read_input_tokens\":1,\"output_tokens\":0}}}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
	"event: content_block_stop\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":2}}\n\n" +
	"event: message_stop\n" +
	"data: {\"type\":\"message_stop\"}\n\n"

const geminiResponsesStreamFixture = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}],\"role\":\"model\"},\"index\":0}],\"modelVersion\":\"gemini-upstream\",\"responseId\":\"resp_1\"}\n\n" +
	"data: {\"candidates\":[{\"content\":{\"parts\":[],\"role\":\"model\"},\"finishReason\":\"STOP\",\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":4,\"cachedContentTokenCount\":1,\"candidatesTokenCount\":2,\"totalTokenCount\":6},\"modelVersion\":\"gemini-upstream\",\"responseId\":\"resp_1\"}\n\n"

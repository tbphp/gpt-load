package bifrost

import (
	"bytes"
	"context"
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
		modelInPath    bool
		assertClient   func(*testing.T, map[string]any)
		runtime        func(*testing.T, string) *Runtime
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
			assertClient: func(t *testing.T, body map[string]any) {
				if body["type"] != "message" || body["role"] != "assistant" || body["model"] != "client-model" {
					t.Errorf("Anthropic response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
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
			modelInPath:    true,
			assertClient: func(t *testing.T, body map[string]any) {
				if body["type"] != "message" || body["role"] != "assistant" || body["model"] != "client-model" {
					t.Errorf("Anthropic response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
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
			assertClient: func(t *testing.T, body map[string]any) {
				candidates, _ := body["candidates"].([]any)
				if len(candidates) != 1 || body["modelVersion"] != "client-model" {
					t.Errorf("Gemini response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
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
			assertClient: func(t *testing.T, body map[string]any) {
				candidates, _ := body["candidates"].([]any)
				if len(candidates) != 1 || body["modelVersion"] != "client-model" {
					t.Errorf("Gemini response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
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
			assertClient: func(t *testing.T, body map[string]any) {
				if body["object"] != "response" || body["model"] != "client-model" {
					t.Errorf("OpenAI Responses response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
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
			modelInPath:    true,
			assertClient: func(t *testing.T, body map[string]any) {
				if body["object"] != "response" || body["model"] != "client-model" {
					t.Errorf("OpenAI Responses response = %#v", body)
				}
			},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
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
			if result.Error != nil || result.StatusCode != http.StatusOK || result.UpstreamRequestID != "converted-request" {
				t.Fatalf("result = %+v, body=%s", result, result.Body)
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

func TestConvertedOpenAIChatUnaryUsesSelectedProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		channelID      channel.ID
		upstreamPath   string
		credentialName string
		upstreamBody   string
		modelInPath    bool
		runtime        func(*testing.T, string) *Runtime
	}{
		{
			name: "Anthropic", channelID: channel.Anthropic, upstreamPath: "/v1/messages",
			credentialName: "X-Api-Key", upstreamBody: anthropicResponsesConvertedFixture,
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
			},
		},
		{
			name: "Gemini", channelID: channel.Gemini, upstreamPath: "/v1beta/models/upstream-model:generateContent",
			credentialName: "X-Goog-Api-Key", upstreamBody: geminiResponsesConvertedFixture, modelInPath: true,
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
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
		streamInBody   bool
		wantFragments  []string
		runtime        func(*testing.T, string) *Runtime
	}{
		{
			name: "Anthropic client from OpenAI stream", channelID: channel.OpenAI,
			clientProtocol: protocol.Anthropic, operation: execution.OperationChatCompletion,
			clientPath: "/v1/messages", clientBody: []byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
			upstreamPath: "/v1/responses", credentialName: "Authorization", upstreamStream: openAIResponsesStreamFixture,
			upstreamModel: "gpt-upstream",
			streamInBody:  true,
			wantFragments: []string{"event: message_start", `"type":"message_start"`, "hello", "event: message_stop"},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
			},
		},
		{
			name: "Gemini client from OpenAI stream", channelID: channel.OpenAI,
			clientProtocol: protocol.Gemini, operation: execution.OperationChatCompletion,
			clientPath: "/v1beta/models/client-model:streamGenerateContent", clientBody: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
			upstreamPath: "/v1/responses", credentialName: "Authorization", upstreamStream: openAIResponsesStreamFixture,
			upstreamModel: "gpt-upstream",
			streamInBody:  true,
			wantFragments: []string{"data: ", "hello", "usageMetadata"},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
			},
		},
		{
			name: "OpenAI Responses client from Anthropic stream", channelID: channel.Anthropic,
			clientProtocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate,
			clientPath: "/v1/responses", clientBody: []byte(`{"model":"client-model","input":"hello","max_output_tokens":16}`),
			upstreamPath: "/v1/messages", credentialName: "X-Api-Key", upstreamStream: anthropicResponsesStreamFixture,
			upstreamModel: "claude-upstream",
			streamInBody:  true,
			wantFragments: []string{"event: response.created", "response.output_text.delta", "hello", "event: response.completed"},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
			},
		},
		{
			name: "OpenAI Responses client from Gemini stream", channelID: channel.Gemini,
			clientProtocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate,
			clientPath: "/v1/responses", clientBody: []byte(`{"model":"client-model","input":"hello","max_output_tokens":16}`),
			upstreamPath: "/v1beta/models/upstream-model:streamGenerateContent", credentialName: "X-Goog-Api-Key", upstreamStream: geminiResponsesStreamFixture,
			upstreamModel: "gemini-upstream",
			wantFragments: []string{"event: response.created", "response.output_text.delta", "hello", "event: response.completed"},
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
			for _, fragment := range test.wantFragments {
				if !strings.Contains(data.String(), fragment) {
					t.Errorf("stream missing %q: %s", fragment, data.String())
				}
			}
			if !strings.Contains(data.String(), `"client-model"`) || strings.Contains(data.String(), `"`+test.upstreamModel+`"`) {
				t.Errorf("client model alias was not applied: %s", data.String())
			}
		})
	}
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
		runtime        func(*testing.T, string) *Runtime
	}{
		{
			name: "Anthropic", channelID: channel.Anthropic, upstreamPath: "/v1/messages",
			credentialName: "X-Api-Key", upstreamStream: anthropicResponsesStreamFixture, upstreamModel: "claude-upstream",
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, anthropicBaseURL: base})
			},
		},
		{
			name: "Gemini", channelID: channel.Gemini, upstreamPath: "/v1beta/models/upstream-model:streamGenerateContent",
			credentialName: "X-Goog-Api-Key", upstreamStream: geminiResponsesStreamFixture, upstreamModel: "gemini-upstream",
			runtime: func(t *testing.T, base string) *Runtime {
				return newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, geminiBaseURL: base + "/v1beta"})
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

		runtime := newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, openAIBaseURL: server.URL})
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

		runtime := newProtocolTestRuntime(t, runtimeOptions{allowPrivateNetwork: true, openAIBaseURL: server.URL})
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
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "converted-request", AttemptID: "converted-attempt", Sequence: 1,
		ChannelID: string(channelID), ClientProtocol: clientProtocol, Operation: operation,
		ClientModel: "client-model", UpstreamModel: "upstream-model",
		Method: http.MethodPost, Path: path, Query: make(map[string][]string),
		Header: http.Header{"Authorization": {"Bearer client-secret"}, "X-Api-Key": {"client-secret"}, "X-Test": {"kept"}},
		Body:   body, TargetConfig: json.RawMessage(`{}`),
		Credential: execution.NewCredentialSnapshot(12, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	})
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

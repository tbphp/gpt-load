package bifrost

import (
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

func TestOfficialCountTokensNativeUsesProviderEndpoint(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		channelID    channel.ID
		protocol     protocol.Protocol
		clientPath   string
		upstreamPath string
		body         string
		response     string
		credential   string
		runtime      func(*testing.T, string) *testRuntime
	}{
		{
			name: "Anthropic", channelID: channel.Anthropic, protocol: protocol.Anthropic,
			clientPath: "/v1/messages/count_tokens", upstreamPath: "/v1/messages/count_tokens",
			body:     `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"provider":"blocked"}`,
			response: `{"input_tokens":7}`, credential: "X-Api-Key",
			runtime: func(t *testing.T, baseURL string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: baseURL})
			},
		},
		{
			name: "Gemini", channelID: channel.Gemini, protocol: protocol.Gemini,
			clientPath: "/v1beta/models/client-model:countTokens", upstreamPath: "/v1beta/models/upstream-model:countTokens",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"provider":"blocked"}`,
			response: `{"totalTokens":7}`, credential: "X-Goog-Api-Key",
			runtime: func(t *testing.T, baseURL string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: baseURL + "/v1beta"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != test.upstreamPath {
					t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
				}
				if request.Header.Get(test.credential) != testAPIKey {
					t.Errorf("credential header %s = %q", test.credential, request.Header.Get(test.credential))
				}
				body, _ := io.ReadAll(request.Body)
				var object map[string]any
				if err := json.Unmarshal(body, &object); err != nil {
					t.Fatal(err)
				}
				if _, exists := object["provider"]; exists {
					t.Errorf("provider control field reached upstream: %s", body)
				}
				if test.protocol == protocol.Anthropic && object["model"] != "upstream-model" {
					t.Errorf("Anthropic upstream model = %#v", object["model"])
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			runtime := test.runtime(t, server.URL)
			spec := countTokensSpec(test.channelID, test.protocol, test.clientPath, test.body, execution.RouteNative)
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if string(result.Body) != test.response || result.Usage != nil {
				t.Fatalf("body/usage = %s/%+v, want %s/nil", result.Body, result.Usage, test.response)
			}
			if result.UpstreamProtocol != test.protocol {
				t.Fatalf("upstream protocol = %q, want %q", result.UpstreamProtocol, test.protocol)
			}
		})
	}
}

func TestConvertedCountTokensUsesBifrostProviderEndpointAndClientShape(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name             string
		channelID        channel.ID
		clientProtocol   protocol.Protocol
		operation        execution.Operation
		clientPath       string
		clientBody       string
		upstreamPath     string
		upstreamResponse string
		wantField        string
		wantCount        float64
		upstreamBodyKey  string
		credential       string
		upstreamProtocol protocol.Protocol
		runtime          func(*testing.T, string) *testRuntime
	}{
		{
			name: "OpenAI Responses to Anthropic", channelID: channel.Anthropic,
			clientProtocol: protocol.OpenAIResponses, operation: execution.OperationResponsesInputTokens,
			clientPath: "/v1/responses/input_tokens", clientBody: `{"model":"client-model","input":"hello"}`,
			upstreamPath: "/v1/messages/count_tokens", upstreamResponse: `{"input_tokens":7}`,
			wantField: "input_tokens", wantCount: 7, upstreamBodyKey: "messages",
			credential: "X-Api-Key", upstreamProtocol: protocol.Anthropic,
			runtime: func(t *testing.T, baseURL string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: baseURL})
			},
		},
		{
			name: "Anthropic to Gemini", channelID: channel.Gemini,
			clientProtocol: protocol.Anthropic, operation: execution.OperationCountTokens,
			clientPath: "/v1/messages/count_tokens", clientBody: `{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`,
			upstreamPath: "/v1beta/models/upstream-model:countTokens", upstreamResponse: `{"totalTokens":7,"promptTokensDetails":[{"modality":"TEXT","tokenCount":7}]}`,
			wantField: "input_tokens", wantCount: 7, upstreamBodyKey: "generateContentRequest",
			credential: "X-Goog-Api-Key", upstreamProtocol: protocol.Gemini,
			runtime: func(t *testing.T, baseURL string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: baseURL + "/v1beta"})
			},
		},
		{
			name: "Gemini to OpenAI Responses", channelID: channel.OpenAI,
			clientProtocol: protocol.Gemini, operation: execution.OperationCountTokens,
			clientPath: "/v1beta/models/client-model:countTokens", clientBody: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			upstreamPath: "/v1/responses/input_tokens", upstreamResponse: `{"object":"response.input_tokens","input_tokens":7}`,
			wantField: "totalTokens", wantCount: 7, upstreamBodyKey: "input",
			credential: "Authorization", upstreamProtocol: protocol.OpenAIResponses,
			runtime: func(t *testing.T, baseURL string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: baseURL})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != test.upstreamPath {
					t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
				}
				wantCredential := testAPIKey
				if test.credential == "Authorization" {
					wantCredential = "Bearer " + testAPIKey
				}
				if request.Header.Get(test.credential) != wantCredential {
					t.Errorf("credential header %s = %q", test.credential, request.Header.Get(test.credential))
				}
				requestBody, _ := io.ReadAll(request.Body)
				var upstream map[string]any
				if json.Unmarshal(requestBody, &upstream) != nil || upstream[test.upstreamBodyKey] == nil {
					t.Errorf("converted upstream body = %s; want %q", requestBody, test.upstreamBodyKey)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.upstreamResponse)
			}))
			defer server.Close()

			runtime := test.runtime(t, server.URL)
			spec := countTokensSpec(test.channelID, test.clientProtocol, test.clientPath, test.clientBody, execution.RouteConverted)
			spec.Operation = test.operation
			spec = freezeTestAttempt(spec)
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			var body map[string]any
			if err := json.Unmarshal(result.Body, &body); err != nil {
				t.Fatal(err)
			}
			if body[test.wantField] != test.wantCount || result.Usage != nil {
				t.Fatalf("client body/usage = %#v/%+v", body, result.Usage)
			}
			if result.UpstreamProtocol != test.upstreamProtocol {
				t.Fatalf("upstream protocol = %q, want %q", result.UpstreamProtocol, test.upstreamProtocol)
			}
		})
	}
}

func TestCountTokensUnsupportedIsRequestRejected(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		status   int
		response string
		mode     execution.RouteMode
	}{
		{
			name: "native endpoint missing", status: http.StatusNotFound,
			response: `{"type":"error","error":{"type":"not_found_error","message":"missing"}}`,
			mode:     execution.RouteNative,
		},
		{
			name: "converted endpoint not implemented", status: http.StatusNotImplemented,
			response: `{"type":"error","error":{"type":"invalid_request_error","code":"unsupported_operation","message":"unsupported"}}`,
			mode:     execution.RouteConverted,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{
				allowPrivateNetwork: true,
				anthropicBaseURL:    server.URL,
			})
			clientProtocol := protocol.Anthropic
			operation := execution.OperationCountTokens
			path := "/v1/messages/count_tokens"
			body := `{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`
			if test.mode == execution.RouteConverted {
				clientProtocol = protocol.OpenAIResponses
				operation = execution.OperationResponsesInputTokens
				path = "/v1/responses/input_tokens"
				body = `{"model":"client-model","input":"hello"}`
			}
			spec := countTokensSpec(channel.Anthropic, clientProtocol, path, body, test.mode)
			spec.Operation = operation
			spec = freezeTestAttempt(spec)
			result := runtime.Execute(context.Background(), spec)
			if result.Error == nil || result.Error.Hint != execution.FailureHintRequestRejected {
				t.Fatalf("unsupported result = %#v", result)
			}
		})
	}
}

func countTokensSpec(
	channelID channel.ID,
	clientProtocol protocol.Protocol,
	path string,
	body string,
	mode execution.RouteMode,
) execution.AttemptSpec {
	return freezeTestAttempt(execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "count-request", AttemptID: "count-attempt", Sequence: 1,
		ChannelID: string(channelID), ClientProtocol: clientProtocol,
		Operation: execution.OperationCountTokens, RouteMode: mode,
		ClientModel: "client-model", UpstreamModel: "upstream-model",
		Method: http.MethodPost, Path: path, Header: make(http.Header), Body: []byte(body),
		TargetConfig: json.RawMessage(`{}`),
		Credential:   execution.NewCredentialSnapshot(10, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	}))
}

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
	"gpt-load/internal/provideradapter"
)

func TestGPTLoadNativeModelListsUseGatewayRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		clientProtocol   protocol.Protocol
		path             string
		credentialHeader string
		response         string
	}{
		{
			name: "openai", clientProtocol: protocol.OpenAICompletions,
			path: "/v1/models", credentialHeader: "Authorization",
			response: `{"object":"list","data":[{"id":"gpt-load-model"}]}`,
		},
		{
			name: "anthropic", clientProtocol: protocol.Anthropic,
			path: "/v1/models", credentialHeader: "X-Api-Key",
			response: `{"data":[{"id":"gpt-load-model","type":"model"}],"has_more":false}`,
		},
		{
			name: "gemini", clientProtocol: protocol.Gemini,
			path: "/v1beta/models", credentialHeader: "X-Goog-Api-Key",
			response: `{"models":[{"name":"models/gpt-load-model"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != "/gateway"+test.path {
					t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
				}
				wantCredential := testAPIKey
				if test.credentialHeader == "Authorization" {
					wantCredential = "Bearer " + testAPIKey
				}
				if request.Header.Get(test.credentialHeader) != wantCredential {
					t.Errorf("credential %s = %q", test.credentialHeader, request.Header.Get(test.credentialHeader))
				}
				if test.clientProtocol == protocol.Anthropic && request.Header.Get("Anthropic-Version") == "" {
					t.Error("Anthropic-Version is missing")
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			manager, resolved := newGPTLoadManagerForTest(t, server.URL+"/gateway")
			spec := utilitySpec(channel.GPTLoad, test.clientProtocol, execution.OperationListModels, http.MethodGet, test.path, nil)
			spec.TargetConfig = resolved.TargetConfig
			spec = freezeTestAttempt(spec)
			result := manager.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
				result.UpstreamProtocol != test.clientProtocol || !bytes.Equal(result.Body, []byte(test.response)) {
				t.Fatalf("result = %+v, validation=%v, body=%s", result, err, result.Body)
			}
		})
	}
}

func TestGPTLoadResponsesResourcesUseGatewayRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation execution.Operation
		method    string
		path      string
		body      string
		wantModel string
	}{
		{
			name: "retrieve", operation: execution.OperationResponsesRetrieve,
			method: http.MethodGet, path: "/v1/responses/resp_123",
		},
		{
			name: "input tokens", operation: execution.OperationResponsesInputTokens,
			method: http.MethodPost, path: "/v1/responses/input_tokens",
			body:      `{"model":"client-model","input":[],"provider":"client","api_key":"client-body"}`,
			wantModel: "upstream-model",
		},
		{
			name: "vendor passthrough", operation: execution.OperationResponsesPassthrough,
			method: http.MethodPatch, path: "/v1/responses/vendor-extension/nested",
			body: `{"vendor":true,"provider":"client","api_key":"client-body"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			const responseBody = `{"object":"gpt_load.response"}`
			clientModel, upstreamModel := "client-model", "upstream-model"
			if test.operation == execution.OperationResponsesRetrieve || test.operation == execution.OperationResponsesPassthrough {
				clientModel, upstreamModel = "", ""
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != test.method || request.URL.Path != "/gateway"+test.path {
					t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
				}
				if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
					t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
				}
				if test.body != "" {
					body, _ := io.ReadAll(request.Body)
					var payload map[string]any
					if err := json.Unmarshal(body, &payload); err != nil {
						t.Fatalf("decode upstream request: %v", err)
					}
					if payload["provider"] != nil || payload["api_key"] != nil {
						t.Errorf("control fields reached upstream: %#v", payload)
					}
					if test.wantModel != "" && payload["model"] != test.wantModel {
						t.Errorf("upstream model = %#v", payload["model"])
					}
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, responseBody)
			}))
			defer server.Close()

			manager, resolved := newGPTLoadManagerForTest(t, server.URL+"/gateway")
			spec := freezeTestAttempt(execution.AttemptSpec{
				RequestID: "gpt-load-responses", AttemptID: "gpt-load-responses-attempt", Sequence: 1,
				ChannelID: string(channel.GPTLoad), ClientProtocol: protocol.OpenAIResponses,
				Operation: test.operation, RouteRequirement: execution.RouteRequirementNative,
				ClientModel: clientModel, UpstreamModel: upstreamModel,
				Method: test.method, Path: test.path,
				Header:       http.Header{"Authorization": {"Bearer client-auth"}, "Content-Type": {"application/json"}},
				Body:         []byte(test.body),
				TargetConfig: resolved.TargetConfig,
				Credential: execution.NewCredentialSnapshot(
					17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`),
				),
			})
			result := manager.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
				result.UpstreamProtocol != protocol.OpenAIResponses || !bytes.Equal(result.Body, []byte(responseBody)) {
				t.Fatalf("result = %+v, validation=%v, body=%s", result, err, result.Body)
			}
		})
	}
}

func newGPTLoadManagerForTest(t *testing.T, baseURL string) (*RuntimeManager, channel.ResolvedTarget) {
	t.Helper()

	registry := channel.NewRegistry()
	resolved, err := registry.Resolve(
		channel.GPTLoad,
		json.RawMessage(`{"base_url":"`+baseURL+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)
	if err := manager.Reconcile([]provideradapter.RuntimeTarget{{Target: resolved}}); err != nil {
		t.Fatal(err)
	}
	return manager, resolved
}

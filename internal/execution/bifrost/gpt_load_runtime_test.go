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

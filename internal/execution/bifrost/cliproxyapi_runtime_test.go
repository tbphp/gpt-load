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

func TestCLIProxyAPICountTokensUsesNativeGatewayWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		clientProtocol   protocol.Protocol
		path             string
		body             string
		wantPath         string
		credentialHeader string
		response         string
	}{
		{
			name: "anthropic", clientProtocol: protocol.Anthropic,
			path: "/v1/messages/count_tokens",
			body: `{"model":"client-model","messages":[{"role":"user","content":"hello"}],` +
				`"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1/messages/count_tokens", credentialHeader: "X-Api-Key",
			response: `{"input_tokens":7}`,
		},
		{
			name: "gemini", clientProtocol: protocol.Gemini,
			path: "/v1beta/models/client-model:countTokens",
			body: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],` +
				`"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1beta/models/upstream-model:countTokens", credentialHeader: "X-Goog-Api-Key",
			response: `{"totalTokens":7}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != test.wantPath ||
					request.URL.Query().Get("trace") != "kept" || request.URL.Query().Get("key") != "" {
					t.Errorf("upstream target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
				}
				if request.Header.Get(test.credentialHeader) != testAPIKey {
					t.Errorf("credential %s = %q", test.credentialHeader, request.Header.Get(test.credentialHeader))
				}
				for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
					if name != test.credentialHeader && request.Header.Get(name) != "" {
						t.Errorf("unexpected credential header %s = %q", name, request.Header.Get(name))
					}
				}
				body, _ := io.ReadAll(request.Body)
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("decode upstream request: %v", err)
				}
				if payload["provider"] != nil || payload["api_key"] != nil {
					t.Errorf("control fields reached upstream: %#v", payload)
				}
				if test.clientProtocol == protocol.Anthropic && payload["model"] != "upstream-model" {
					t.Errorf("upstream model = %#v", payload["model"])
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			manager, resolved := newCLIProxyAPIManagerForTest(t, server.URL+"/team-a")
			spec := freezeTestAttempt(execution.AttemptSpec{
				RequestID: "cpa-count-tokens", AttemptID: "cpa-count-tokens-attempt", Sequence: 1,
				ChannelID: string(channel.CLIProxyAPI), ClientProtocol: test.clientProtocol,
				Operation:   execution.OperationCountTokens,
				ClientModel: "client-model", UpstreamModel: "upstream-model",
				Method: http.MethodPost, Path: test.path,
				RawQuery: "trace=kept&key=client-query",
				Header: http.Header{
					"Authorization":  {"Bearer client-auth"},
					"X-Api-Key":      {"client-auth"},
					"X-Goog-Api-Key": {"client-auth"},
					"Content-Type":   {"application/json"},
				},
				Body: []byte(test.body), TargetConfig: resolved.TargetConfig,
				Credential: execution.NewCredentialSnapshot(
					17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`),
				),
			})
			result := manager.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
				result.UpstreamProtocol != test.clientProtocol || !bytes.Equal(result.Body, []byte(test.response)) {
				t.Fatalf("result = %+v, validation=%v, body=%s", result, err, result.Body)
			}
		})
	}
}

func TestCLIProxyAPIModelDiscoveryUsesOpenAIEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/team-a/v1/models" {
			t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"cpa-model"}]}`)
	}))
	defer server.Close()

	manager, resolved := newCLIProxyAPIManagerForTest(t, server.URL+"/team-a")
	spec := utilitySpec(channel.CLIProxyAPI, protocol.OpenAICompletions, execution.OperationListModels, http.MethodGet, "/v1/models", nil)
	spec.TargetConfig = resolved.TargetConfig
	spec = freezeTestAttempt(spec)
	result := manager.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
		!bytes.Contains(result.Body, []byte(`"id":"cpa-model"`)) {
		t.Fatalf("result = %+v, validation=%v, body=%s", result, err, result.Body)
	}
}

func newCLIProxyAPIManagerForTest(t *testing.T, baseURL string) (*RuntimeManager, channel.ResolvedTarget) {
	t.Helper()

	registry := channel.NewRegistry()
	resolved, err := registry.Resolve(
		channel.CLIProxyAPI,
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

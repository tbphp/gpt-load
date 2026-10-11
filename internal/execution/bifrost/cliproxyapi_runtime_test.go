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

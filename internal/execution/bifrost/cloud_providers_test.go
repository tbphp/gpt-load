package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestAzureAPIKeyUsesSelectedDirectCredentialAndNormalizesResult(t *testing.T) {
	t.Parallel()

	const azureKey = "azure-secret-do-not-leak"
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/openai/v1/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Api-Key") != azureKey || request.Header.Get("Authorization") != "" {
			t.Errorf("auth headers = %#v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var payload map[string]any
		if json.Unmarshal(body, &payload) != nil || payload["model"] != "upstream-model" {
			t.Errorf("payload = %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Ms-Request-Id", "azure-request")
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-azure","object":"chat.completion","created":1,"model":"served-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}))
	defer server.Close()

	runtime := newTestRuntime(t)
	spec := compatibleSpec("https://unused.example")
	spec.ChannelID = "azure_openai"
	spec.TargetConfig = json.RawMessage(`{"endpoint":"` + server.URL + `"}`)
	spec.Credential = execution.NewCredentialSnapshot(31, 1, 1, []byte(`{"api_key":"`+azureKey+`"}`))
	spec = freezeTestAttempt(spec)
	result := runtime.Execute(context.Background(), spec)

	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if calls.Load() != 1 || result.StatusCode != http.StatusOK || result.UpstreamRequestID != "azure-request" || result.Model != "served-model" {
		t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
	}
	assertNoPrivateLeak(t, result, azureKey)
}

func TestAzureErrorDoesNotExposeStructuredCredentialSecrets(t *testing.T) {
	t.Parallel()

	const azureKey = "azure-error-secret"
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"error":{"type":"invalid_api_key","code":"bad_key","message":"rejected `+azureKey+`"}}`)
	}))
	defer server.Close()

	runtime := newTestRuntime(t)
	spec := compatibleSpec("https://unused.example")
	spec.ChannelID = "azure_openai"
	spec.TargetConfig = json.RawMessage(`{"endpoint":"` + server.URL + `"}`)
	spec.Credential = execution.NewCredentialSnapshot(32, 1, 1, []byte(`{"api_key":"`+azureKey+`"}`))
	spec.Timeouts.Request = 5 * time.Second
	spec = freezeTestAttempt(spec)
	result := runtime.Execute(context.Background(), spec)

	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if calls.Load() != 1 || result.Error == nil || result.Error.Kind != execution.ErrorKindHTTP || result.Error.StatusCode != http.StatusUnauthorized {
		t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
	}
	assertNoPrivateLeak(t, result, azureKey)
}

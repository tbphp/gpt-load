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

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
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

func TestAzureOpenAIEmbeddingsUseRawWireOnAzureEndpoint(t *testing.T) {
	t.Parallel()

	const azureKey = "azure-embeddings-secret"
	const responseBody = `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.12345678901234567]}],"model":"embedding-deployment","usage":{"prompt_tokens":1,"total_tokens":1}}`
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/openai/v1/embeddings" || request.URL.RawQuery != "" {
			t.Errorf("target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		if got := request.Header.Values("Api-Key"); len(got) != 1 || got[0] != azureKey || request.Header.Get("Authorization") != "" {
			t.Errorf("auth headers = %#v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(body, &object); err != nil {
			t.Errorf("request body = %s: %v", body, err)
			return
		}
		if string(object["model"]) != `"embedding-deployment"` ||
			string(object["input"]) != `"hello"` ||
			string(object["dimensions"]) != `8` ||
			string(object["encoding_format"]) != `"base64"` ||
			string(object["vendor_extension"]) != `{"precise":1.2300}` {
			t.Errorf("raw request changed: %s", body)
		}
		for _, field := range []string{"stream", "provider", "fallbacks", "api_key"} {
			if _, exists := object[field]; exists {
				t.Errorf("control field %q reached upstream: %s", field, body)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "azure-embeddings")
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := openAIEmbeddingsSpec(channel.OpenAICompatible, "https://unused.example/v1",
		[]byte(`{"model":"client-model","input":"hello","stream":false,"dimensions":8,"encoding_format":"base64","provider":"client","fallbacks":["other"],"api_key":"client","vendor_extension":{"precise":1.2300}}`))
	spec.ChannelID = string(channel.AzureOpenAI)
	spec.ClientModel = "embedding-deployment"
	spec.UpstreamModel = "embedding-deployment"
	spec.TargetConfig = json.RawMessage(`{"endpoint":"` + server.URL + `"}`)
	spec.Credential = execution.NewCredentialSnapshot(33, 1, 1, []byte(`{"api_key":"`+azureKey+`"}`))
	spec.RouteRequirement = execution.RouteRequirementAny
	spec = freezeTestAttempt(spec)
	if spec.RouteMode != execution.RouteConverted {
		t.Fatalf("Azure Embeddings route mode = %q, want converted", spec.RouteMode)
	}
	result := runtime.Execute(context.Background(), spec)

	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if calls.Load() != 1 || result.Error != nil || result.StatusCode != http.StatusOK ||
		result.UpstreamProtocol != protocol.OpenAIEmbeddings || result.Model != "embedding-deployment" ||
		result.UpstreamRequestID != "azure-embeddings" || string(result.Body) != responseBody {
		t.Fatalf("calls/result = %d/%+v error=%+v body=%s", calls.Load(), result, result.Error, result.Body)
	}
	assertNoPrivateLeak(t, result, azureKey)
}

func TestAzureOpenAIEmbeddingsProbeUsesMinimalWire(t *testing.T) {
	t.Parallel()

	const azureKey = "azure-embeddings-probe-secret"
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/openai/v1/embeddings" {
			t.Errorf("probe target = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Api-Key") != azureKey {
			t.Errorf("auth headers = %#v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read probe body: %v", err)
			return
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil || len(payload) != 2 ||
			string(payload["model"]) != `"probe-upstream"` || string(payload["input"]) != `"ping"` {
			t.Errorf("probe body = %s err=%v", body, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"object":"list","data":[{"index":0,"embedding":[0.1]}]}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := embeddingsProbeSpec(t, channel.OpenAICompatible, "https://unused.example/v1")
	spec.ChannelID = string(channel.AzureOpenAI)
	spec.TargetConfig = json.RawMessage(`{"endpoint":"` + server.URL + `"}`)
	spec.Credential = execution.NewCredentialSnapshot(34, 1, 1, []byte(`{"api_key":"`+azureKey+`"}`))
	spec = freezeTestAttempt(spec)
	result := runtime.Execute(context.Background(), spec)

	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if calls.Load() != 1 || result.Error != nil || result.StatusCode != http.StatusOK ||
		result.UpstreamProtocol != protocol.OpenAIEmbeddings {
		t.Fatalf("calls/result = %d/%+v error=%+v", calls.Load(), result, result.Error)
	}
	assertNoPrivateLeak(t, result, azureKey)
}

func TestAzureOpenAIEmbeddingsRejectProviderQueryBeforeDispatch(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := openAIEmbeddingsSpec(channel.OpenAICompatible, "https://unused.example/v1",
		[]byte(`{"model":"client-model","input":"hello"}`))
	spec.ChannelID = string(channel.AzureOpenAI)
	spec.RouteRequirement = execution.RouteRequirementAny
	spec.Query = map[string][]string{"api-version": {"2024-10-21"}}
	spec.TargetConfig = json.RawMessage(`{"endpoint":"` + server.URL + `"}`)
	spec.Credential = execution.NewCredentialSnapshot(35, 1, 1, []byte(`{"api_key":"azure-query-secret"}`))
	spec = freezeTestAttempt(spec)
	result := runtime.Execute(context.Background(), spec)

	if calls.Load() != 0 || result.Error == nil || result.DispatchState != execution.DispatchNotSent {
		t.Fatalf("calls/result = %d/%+v error=%+v", calls.Load(), result, result.Error)
	}
}

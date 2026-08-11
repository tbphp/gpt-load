package bifrost

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const testAPIKey = "sk-test-do-not-leak"

func TestStreamingSDKContextUsesLaterIdleGuard(t *testing.T) {
	runtime := &Runtime{}
	maximum := time.Duration(math.MaxInt64)
	tests := []struct {
		name       string
		configured time.Duration
		want       time.Duration
		wantSet    bool
	}{
		{name: "adds guard window", configured: 20 * time.Millisecond, want: time.Second + 20*time.Millisecond, wantSet: true},
		{name: "saturates near duration limit", configured: maximum - 500*time.Millisecond, want: maximum, wantSet: true},
		{name: "leaves disabled timeout unset", configured: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := execution.AttemptSpec{Timeouts: execution.AttemptTimeouts{StreamIdle: test.configured}}
			ctx := runtime.newStreamingSDKContext(context.Background(), spec, schemas.Key{})
			got, ok := ctx.Value(schemas.BifrostContextKeyStreamIdleTimeout).(time.Duration)
			if ok != test.wantSet || got != test.want {
				t.Fatalf("SDK stream idle timeout = %s, set=%t; want %s, set=%t", got, ok, test.want, test.wantSet)
			}
		})
	}

	spec := execution.AttemptSpec{Timeouts: execution.AttemptTimeouts{StreamIdle: 20 * time.Millisecond}}
	ctx := runtime.newSDKContext(context.Background(), spec, schemas.Key{})
	if got := ctx.Value(schemas.BifrostContextKeyStreamIdleTimeout); got != 20*time.Millisecond {
		t.Fatalf("non-streaming SDK timeout = %v, want configured timeout", got)
	}
}

func TestRuntimeUnaryUsesSelectedCredentialModelAndEndpoint(t *testing.T) {
	t.Parallel()

	const responseBody = `{"id": "chatcmpl-1", "object":"chat.completion", "created":123, "model":"served-model", "choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}], "usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4}}, "vendor_response":{"precise":1.2300}}`
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			t.Errorf("authorization = %q", got)
		}
		for _, name := range []string{"Proxy-Authorization", "Api-Key", "X-Api-Key", "X-Goog-Api-Key", "Proxy-Connection"} {
			if value := request.Header.Get(name); value != "" {
				t.Errorf("sensitive/transport header %s reached upstream: %q", name, value)
			}
		}
		if value := request.Header.Get("Connection"); value != "close" {
			t.Errorf("SDK-owned Connection header = %q, want close", value)
		}
		if got := request.Header.Get("X-Test-Header"); got != "forward-me" {
			t.Errorf("safe business header = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if payload["model"] != "upstream-model" {
			t.Errorf("model = %#v", payload["model"])
		}
		if _, exists := payload["stream"]; exists {
			t.Errorf("stream = %#v, want omitted", payload["stream"])
		}
		for _, field := range []string{"fallbacks", "provider", "authorization", "api_key", "x-api-key"} {
			if _, exists := payload[field]; exists {
				t.Errorf("client control field %q reached upstream", field)
			}
		}
		vendor, ok := payload["vendor_extension"].(map[string]any)
		if !ok || vendor["precise"] != float64(1.23) {
			t.Errorf("vendor extension was not preserved: %#v", payload["vendor_extension"])
		}
		if !bytes.Contains(body, []byte(`"precise":1.2300`)) {
			t.Errorf("vendor extension raw number was rewritten: %s", body)
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "upstream-request-1")
		writer.Header().Set("X-Upstream-Secret", "must-not-forward")
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer server.Close()

	runtime := newTestRuntime(t)
	spec := compatibleSpec(server.URL)
	// Equal aliases must retain the native byte-for-byte fast path.
	spec.ClientModel = spec.UpstreamModel
	result := runtime.Execute(context.Background(), spec)

	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if result.StatusCode != http.StatusOK || result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted {
		t.Fatalf("unexpected result metadata: %+v", result)
	}
	if result.UpstreamRequestID != "upstream-request-1" {
		t.Fatalf("upstream request ID = %q", result.UpstreamRequestID)
	}
	if result.Header.Get("X-Upstream-Secret") != "" {
		t.Fatal("unsafe response header was forwarded")
	}
	if result.Model != "served-model" {
		t.Fatalf("result model = %q", result.Model)
	}
	if string(result.Body) != responseBody {
		t.Fatalf("native response bytes changed:\n got: %s\nwant: %s", result.Body, responseBody)
	}
	assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 3})
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if runtime.keyPoolCalls() != 0 {
		t.Fatalf("key-pool calls = %d, want 0", runtime.keyPoolCalls())
	}
	assertNoPrivateLeak(t, result, testAPIKey, "gptload-custom-")
}

func TestRuntimeBoundsUnaryResponsesBeforeMaterialization(t *testing.T) {
	const limit = int64(256)

	t.Run("native identity response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(bytes.Repeat([]byte("x"), int(limit+1)))
		}))
		defer server.Close()

		runtime := newProtocolTestRuntime(t, testRuntimeOptions{
			allowPrivateNetwork:       true,
			maxUnaryResponseBodyBytes: limit,
		})
		spec := compatibleSpec(server.URL)
		spec.ClientModel = spec.UpstreamModel
		result := runtime.Execute(context.Background(), spec)
		if result.Error == nil || result.Error.Kind != execution.ErrorKindInternal ||
			result.Error.Summary != "upstream response exceeds size limit" {
			t.Fatalf("oversized native result = %+v", result)
		}
	})

	t.Run("native compressed response fails closed without materialization", func(t *testing.T) {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, _ = writer.Write([]byte(`{"model":"upstream-model","padding":"` + strings.Repeat("x", 4096) + `"}`))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if int64(compressed.Len()) >= limit {
			t.Fatalf("compressed fixture = %d bytes, want below %d", compressed.Len(), limit)
		}
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Content-Encoding", "gzip")
			_, _ = response.Write(compressed.Bytes())
		}))
		defer server.Close()

		runtime := newProtocolTestRuntime(t, testRuntimeOptions{
			allowPrivateNetwork:       true,
			maxUnaryResponseBodyBytes: limit,
		})
		spec := compatibleSpec(server.URL)
		spec.ClientModel = spec.UpstreamModel
		result := runtime.Execute(context.Background(), spec)
		if result.Error == nil || result.Error.Kind != execution.ErrorKindInternal ||
			result.Error.Summary != "encoded upstream response cannot be safely forwarded" ||
			len(result.Body) != 0 {
			t.Fatalf("compressed native result = %+v body=%d", result, len(result.Body))
		}
	})

	t.Run("converted response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"`+strings.Repeat("x", 1024)+`"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		runtime := newProtocolTestRuntime(t, testRuntimeOptions{
			allowPrivateNetwork:       true,
			maxUnaryResponseBodyBytes: limit,
		})
		spec := compatibleSpec(server.URL)
		spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `"}`)
		spec = freezeTestAttempt(spec)
		result := runtime.Execute(context.Background(), spec)
		if result.Error == nil || result.Error.Kind != execution.ErrorKindInternal ||
			result.Error.Summary != "upstream response exceeds size limit" {
			t.Fatalf("oversized converted result = %+v", result)
		}
	})
}

func TestNativePassthroughRewritesModelAliasInUnaryAndStreamResponses(t *testing.T) {
	t.Parallel()

	t.Run("unary JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":"chat_1","object":"chat.completion","created":1,"model":"provider-model","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}))
		defer server.Close()

		runtime := newTestRuntime(t)
		spec := compatibleSpec(server.URL)
		spec.ClientModel = "public-model"
		spec.UpstreamModel = "provider-model"
		result := runtime.Execute(context.Background(), spec)
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v; result=%+v", err, result)
		}
		if result.Model != "provider-model" || !bytes.Contains(result.Body, []byte(`"model":"public-model"`)) || bytes.Contains(result.Body, []byte(`"model":"provider-model"`)) {
			t.Fatalf("model/body = %q/%s", result.Model, result.Body)
		}
	})

	t.Run("SSE split across upstream writes", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			flusher := writer.(http.Flusher)
			_, _ = io.WriteString(writer, `data: {"id":"chat_1","object":"chat.completion.chunk","created":1,"model":"provider-`)
			flusher.Flush()
			time.Sleep(5 * time.Millisecond)
			_, _ = io.WriteString(writer, "model\",\"choices\":[]}\n\ndata: [DONE]\n\n")
			flusher.Flush()
		}))
		defer server.Close()

		runtime := newTestRuntime(t)
		spec := compatibleSpec(server.URL)
		spec.ClientModel = "public-model"
		spec.UpstreamModel = "provider-model"
		var data bytes.Buffer
		result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
			if event.Kind == execution.StreamEventData {
				data.Write(event.Data)
			}
			return nil
		})
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v; result=%+v", err, result)
		}
		if result.Model != "provider-model" || !bytes.Contains(data.Bytes(), []byte(`"model":"public-model"`)) || bytes.Contains(data.Bytes(), []byte(`"model":"provider-model"`)) {
			t.Fatalf("model/data = %q/%s", result.Model, data.String())
		}
	})
}

func TestProductionRuntimeAllowsConfiguredPrivateCompatibleEndpoint(t *testing.T) {
	t.Parallel()

	server := responseServer(t, "private-endpoint")
	defer server.Close()
	runtime, err := NewRuntime(context.Background(), channel.NewRegistry())
	if err != nil {
		t.Fatalf("new production runtime: %v", err)
	}
	defer runtime.Shutdown()

	result := runtime.Execute(context.Background(), compatibleSpec(server.URL))
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if result.UpstreamRequestID != "private-endpoint" {
		t.Fatalf("request ID = %q", result.UpstreamRequestID)
	}
}

func TestRuntimeUnaryHTTPErrorIsSingleLogicalCallAndSanitized(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("X-Request-Id", "request-401")
		writer.Header().Set("Retry-After", "2")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"error":{"type":"invalid_api_key","code":"bad_key","message":"rejected `+testAPIKey+`"}}`)
	}))
	defer server.Close()

	runtime := newTestRuntime(t)
	result := runtime.Execute(context.Background(), compatibleSpec(server.URL))

	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want MaxRetries=0 to produce one call", calls.Load())
	}
	if result.Error == nil || result.Error.Kind != execution.ErrorKindHTTP || result.Error.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected error evidence: %+v", result.Error)
	}
	if result.Error.Type != "invalid_api_key" || result.Error.Code != "bad_key" {
		t.Fatalf("type/code = %q/%q", result.Error.Type, result.Error.Code)
	}
	if result.Error.RetryAfter != 2*time.Second {
		t.Fatalf("retry after = %s", result.Error.RetryAfter)
	}
	if result.UpstreamRequestID != "request-401" {
		t.Fatalf("request ID = %q", result.UpstreamRequestID)
	}
	assertNoPrivateLeak(t, result, testAPIKey, "gptload-custom-")
}

func TestRuntimeRejectsInvalidAttemptBeforeDispatch(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	tests := []struct {
		name   string
		mutate func(*execution.AttemptSpec)
	}{
		{name: "unsupported channel", mutate: func(spec *execution.AttemptSpec) { spec.ChannelID = "other" }},
		{name: "official target has unknown field", mutate: func(spec *execution.AttemptSpec) {
			spec.ChannelID = string(channel.OpenAI)
			spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `/v1","other":"value"}`)
		}},
		{name: "official target missing", mutate: func(spec *execution.AttemptSpec) {
			spec.ChannelID = string(channel.OpenAI)
			spec.TargetConfig = nil
		}},
		{name: "compatible missing base URL", mutate: func(spec *execution.AttemptSpec) { spec.TargetConfig = json.RawMessage(`{}`) }},
		{name: "compatible unknown target field", mutate: func(spec *execution.AttemptSpec) {
			spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `","token":"secret"}`)
		}},
		{name: "credential unknown field", mutate: func(spec *execution.AttemptSpec) {
			spec.Credential = execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":"`+testAPIKey+`","other":"x"}`))
		}},
		{name: "credential empty", mutate: func(spec *execution.AttemptSpec) {
			spec.Credential = execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":""}`))
		}},
		{name: "wrong protocol", mutate: func(spec *execution.AttemptSpec) { spec.ClientProtocol = protocol.Anthropic }},
		{name: "wrong operation", mutate: func(spec *execution.AttemptSpec) { spec.Operation = execution.OperationListModels }},
		{name: "wrong method", mutate: func(spec *execution.AttemptSpec) { spec.Method = http.MethodGet }},
		{name: "wrong path", mutate: func(spec *execution.AttemptSpec) { spec.Path = "/v1/responses" }},
	}

	runtime := newTestRuntime(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := compatibleSpec(server.URL)
			test.mutate(&spec)
			result := runtime.Execute(context.Background(), spec)
			if result.DispatchState != execution.DispatchNotSent || result.Error == nil || result.Error.Kind != execution.ErrorKindInvalidRequest {
				t.Fatalf("unexpected result: %+v", result)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v", err)
			}
			assertNoPrivateLeak(t, result, testAPIKey)
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
}

func TestRuntimeAcceptsCanonicalOfficialTargetWithoutChangingCredential(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	spec := compatibleSpec("https://unused.example")
	spec.ChannelID = string(channel.OpenAI)
	spec.TargetConfig = json.RawMessage(`{}`)
	spec = freezeTestAttempt(spec)
	prepared, failure := runtime.prepare(spec, false)
	if failure != nil {
		t.Fatalf("official preflight failed: %+v", failure)
	}
	if prepared.provider != "openai" || prepared.directKey.Value.GetValue() != testAPIKey {
		t.Fatalf("prepared provider/key mismatch: provider=%q key_matches=%t", prepared.provider, prepared.directKey.Value.GetValue() == testAPIKey)
	}
}

func TestRuntimeUsesExplicitOfficialOpenAIBaseURLOverride(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writeSuccess(writer, "ok")
	}))
	defer server.Close()

	spec := compatibleSpec(server.URL)
	spec.ChannelID = string(channel.OpenAI)
	spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `"}`)
	spec = freezeTestAttempt(spec)
	spec.ClientModel = spec.UpstreamModel
	result := newTestRuntime(t).Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || calls.Load() != 1 {
		t.Fatalf("result/calls = %+v/%d, err=%v", result, calls.Load(), err)
	}
}

func TestRuntimePreparesStructuredCloudCredentialsForSelectedProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		channelID    string
		targetConfig string
		credential   string
		wantProvider string
	}{
		{
			name: "Azure API key", channelID: "azure_openai",
			targetConfig: `{"endpoint":"https://resource.openai.azure.com"}`,
			credential:   `{"api_key":"azure-secret"}`, wantProvider: "azure",
		},
		{
			name: "Azure Entra", channelID: "azure_openai",
			targetConfig: `{"endpoint":"https://resource.services.ai.azure.com"}`,
			credential:   `{"client_id":"client","client_secret":"secret","tenant_id":"tenant"}`, wantProvider: "azure",
		},
		{
			name: "Bedrock API key", channelID: "aws_bedrock",
			targetConfig: `{"region":"us-east-1"}`,
			credential:   `{"api_key":"bedrock-secret"}`, wantProvider: "bedrock",
		},
		{
			name: "Bedrock SigV4 role", channelID: "aws_bedrock",
			targetConfig: `{"region":"eu-west-1"}`,
			credential:   `{"access_key":"access","secret_key":"secret","session_token":"token","role_arn":"arn:aws:iam::123456789012:role/test"}`, wantProvider: "bedrock",
		},
		{
			name: "Vertex service account", channelID: "google_vertex",
			targetConfig: `{"location":"us-central1","project_id":"project-one"}`,
			credential:   `{"service_account_json":"{\"type\":\"service_account\",\"project_id\":\"project-one\",\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"secret\"}"}`, wantProvider: "vertex",
		},
	}
	runtime := newTestRuntime(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := compatibleSpec("https://unused.example")
			spec.ChannelID = test.channelID
			spec.TargetConfig = json.RawMessage(test.targetConfig)
			spec.Credential = execution.NewCredentialSnapshot(23, 5, 7, []byte(test.credential))
			spec = freezeTestAttempt(spec)
			prepared, failure := runtime.prepare(spec, false)
			if failure != nil {
				t.Fatalf("prepare() failure = %+v", failure)
			}
			if string(prepared.provider) != test.wantProvider {
				t.Fatalf("provider = %q, want %q", prepared.provider, test.wantProvider)
			}
			if prepared.directKey.ID != "23:7:5" || prepared.directKey.Weight != 1 || len(prepared.directKey.Models) != 1 || prepared.directKey.Models[0] != "*" {
				t.Fatalf("direct key identity = %#v", prepared.directKey)
			}
			switch test.name {
			case "Azure API key":
				if prepared.directKey.Value.GetValue() != "azure-secret" || prepared.directKey.AzureKeyConfig == nil || prepared.directKey.AzureKeyConfig.Endpoint.GetValue() != "https://resource.openai.azure.com" {
					t.Fatalf("Azure key = %#v", prepared.directKey)
				}
			case "Azure Entra":
				config := prepared.directKey.AzureKeyConfig
				if config == nil || prepared.directKey.Value.GetValue() != "" || config.ClientID == nil || config.ClientID.GetValue() != "client" || config.ClientSecret == nil || config.ClientSecret.GetValue() != "secret" || config.TenantID == nil || config.TenantID.GetValue() != "tenant" {
					t.Fatalf("Azure Entra key = %#v", prepared.directKey)
				}
			case "Bedrock API key":
				if prepared.directKey.Value.GetValue() != "bedrock-secret" || prepared.directKey.BedrockKeyConfig == nil || prepared.directKey.BedrockKeyConfig.Region == nil || prepared.directKey.BedrockKeyConfig.Region.GetValue() != "us-east-1" {
					t.Fatalf("Bedrock API key = %#v", prepared.directKey)
				}
			case "Bedrock SigV4 role":
				config := prepared.directKey.BedrockKeyConfig
				if config == nil || config.AccessKey.GetValue() != "access" || config.SecretKey.GetValue() != "secret" || config.SessionToken == nil || config.SessionToken.GetValue() != "token" || config.RoleARN == nil || config.RoleARN.GetValue() == "" || config.Region == nil || config.Region.GetValue() != "eu-west-1" {
					t.Fatalf("Bedrock SigV4 key = %#v", prepared.directKey)
				}
			case "Vertex service account":
				config := prepared.directKey.VertexKeyConfig
				if config == nil || config.ProjectID.GetValue() != "project-one" || config.Region.GetValue() != "us-central1" || !strings.Contains(config.AuthCredentials.GetValue(), `"private_key":"secret"`) {
					t.Fatalf("Vertex key = %#v", prepared.directKey)
				}
			}
		})
	}
}

func TestRuntimeIsolatesCompatibleEndpointsWithinOneCore(t *testing.T) {
	t.Parallel()

	serverA := responseServer(t, "a")
	defer serverA.Close()
	serverB := responseServer(t, "b")
	defer serverB.Close()

	runtime := newTestRuntime(t)
	resultA := runtime.Execute(context.Background(), compatibleSpec(serverA.URL))
	resultB := runtime.Execute(context.Background(), compatibleSpec(serverB.URL))
	if resultA.UpstreamRequestID != "a" || resultB.UpstreamRequestID != "b" {
		t.Fatalf("endpoint results = %q/%q", resultA.UpstreamRequestID, resultB.UpstreamRequestID)
	}
	assertNoPrivateLeak(t, resultA, "gptload-custom-")
	assertNoPrivateLeak(t, resultB, "gptload-custom-")
}

func TestRuntimeKeepsExactNonV1CompatiblePrefixViaTypedFallback(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/tenant/openai/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		writeSuccess(writer, "compatible-prefix")
	}))
	defer server.Close()

	runtime := newTestRuntime(t)
	spec := compatibleSpec(server.URL)
	spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `/tenant/openai"}`)
	spec = freezeTestAttempt(spec)
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if calls.Load() != 1 || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
	}
}

func TestRuntimeStreamFramesReadyDataUsageAndDone(t *testing.T) {
	t.Parallel()

	const rawStream = "data: {\"id\": \"chatcmpl-s\", \"object\":\"chat.completion.chunk\",\"created\":123,\"model\":\"served-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":null}],\"vendor_chunk\":{\"precise\":1.2300}}\n\n" +
		"data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"created\":123,\"model\":\"served-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"created\":123,\"model\":\"served-model\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2,\"total_tokens\":10,\"prompt_tokens_details\":{\"cached_tokens\":3}}}\n\n" +
		"data: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			t.Errorf("authorization = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if payload["model"] != "upstream-model" || payload["stream"] != true {
			t.Errorf("stream request controls = %#v", payload)
		}
		if _, exists := payload["stream_options"]; exists {
			t.Errorf("adapter injected stream_options without an explicit request option: %#v", payload["stream_options"])
		}
		if _, exists := payload["provider"]; exists {
			t.Error("provider control reached streaming upstream")
		}
		if _, exists := payload["fallbacks"]; exists {
			t.Error("fallback control reached streaming upstream")
		}
		if !bytes.Contains(body, []byte(`"precise":1.2300`)) {
			t.Errorf("stream vendor extension was rewritten: %s", body)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("X-Request-Id", "stream-request")
		writer.WriteHeader(http.StatusOK)
		flusher := writer.(http.Flusher)
		_, _ = io.WriteString(writer, rawStream)
		flusher.Flush()
	}))
	defer server.Close()

	runtime := newTestRuntime(t)
	spec := compatibleSpec(server.URL)
	// Equal aliases must retain the native byte-for-byte fast path.
	spec.ClientModel = spec.UpstreamModel
	var events []execution.StreamEvent
	result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
		if err := event.Validate(); err != nil {
			t.Fatalf("event validation: %v; event=%+v", err, event)
		}
		events = append(events, event.Clone())
		return nil
	})

	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if len(events) < 3 || events[0].Kind != execution.StreamEventReady {
		t.Fatalf("events = %+v", events)
	}
	if events[0].UpstreamRequestID != "stream-request" {
		t.Fatalf("ready request ID = %q", events[0].UpstreamRequestID)
	}
	var sawUsage, sawDone bool
	var rawData bytes.Buffer
	for _, event := range events[1:] {
		if event.Kind == execution.StreamEventUsage {
			sawUsage = true
			assertUsage(t, event.Usage, usage.Tokens{UncachedInput: 5, CacheRead: 3, Output: 2})
		}
		if event.Kind == execution.StreamEventData && bytes.Contains(event.Data, []byte("data: [DONE]\n\n")) {
			sawDone = true
		}
		if event.Kind == execution.StreamEventData {
			rawData.Write(event.Data)
		}
		assertNoPrivateLeak(t, event, "gptload-openai-", testAPIKey)
	}
	if !sawUsage || !sawDone {
		t.Fatalf("usage/done = %t/%t; events=%+v", sawUsage, sawDone, events)
	}
	if rawData.String() != rawStream {
		t.Fatalf("native SSE bytes changed:\n got: %q\nwant: %q", rawData.String(), rawStream)
	}
	assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 5, CacheRead: 3, Output: 2})
}

func TestSanitizeNativeChatBodyPreservesExplicitStreamOptions(t *testing.T) {
	t.Parallel()

	body, err := sanitizeNativeChatBody(
		[]byte(`{"model":"client","messages":[],"stream_options":{"include_usage":false,"vendor_option":"keep"}}`),
		"upstream",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	options, ok := payload["stream_options"].(map[string]any)
	if !ok || options["include_usage"] != false || options["vendor_option"] != "keep" {
		t.Fatalf("stream_options changed: %#v", payload["stream_options"])
	}
}

func TestRuntimePreservesSafeQueryForNativeAndTypedCompatibleTargets(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		basePrefix string
	}{
		{name: "native passthrough", basePrefix: "/v1"},
		{name: "typed fallback", basePrefix: "/tenant/openai"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				wantPath := test.basePrefix + "/chat/completions"
				if request.URL.Path != wantPath {
					t.Errorf("path = %q, want %q", request.URL.Path, wantPath)
				}
				if request.URL.Query().Get("trace") != "keep" {
					t.Errorf("safe query = %q", request.URL.RawQuery)
				}
				for _, key := range []string{"provider", "fallbacks", "api_key"} {
					if request.URL.Query().Has(key) {
						t.Errorf("control query %q reached upstream: %q", key, request.URL.RawQuery)
					}
				}
				writeSuccess(writer, "query")
			}))
			defer server.Close()

			runtime := newTestRuntime(t)
			spec := compatibleSpec(server.URL)
			spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + test.basePrefix + `"}`)
			spec = freezeTestAttempt(spec)
			spec.Query.Set("trace", "keep")
			spec.Query.Set("provider", "injected")
			spec.Query.Set("fallbacks", "injected")
			spec.Query.Set("api_key", "injected")
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if result.Error != nil || calls.Load() != 1 {
				t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
			}
		})
	}
}

func TestSafeRawUpstreamQueryPreservesOpaqueAllowedSegments(t *testing.T) {
	t.Parallel()

	const raw = "first=%2F&provider=injected&broken=%ZZ&X-Api-Key=injected&&first=%2f&empty=&%61pi_key=injected&fallbacks=x"
	const want = "first=%2F&broken=%ZZ&&first=%2f&empty="
	if got := safeRawUpstreamQuery(raw); got != want {
		t.Fatalf("safeRawUpstreamQuery() = %q, want %q", got, want)
	}

	runtime := newTestRuntime(t)
	spec := compatibleSpec("https://upstream.example")
	spec.Query = nil
	spec.RawQuery = raw
	prepared, failure := runtime.prepare(spec, false)
	if failure != nil || prepared.passthrough == nil {
		t.Fatalf("prepare() failure = %+v; prepared=%+v", failure, prepared)
	}
	if prepared.passthrough.RawQuery != want {
		t.Fatalf("prepared RawQuery = %q, want %q", prepared.passthrough.RawQuery, want)
	}
}

func TestRuntimePreservesRawQueryBytesForNativeAndTypedCompatibleTargets(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		basePrefix string
	}{
		{name: "native passthrough", basePrefix: "/v1"},
		{name: "typed fallback", basePrefix: "/tenant/openai"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const raw = "first=%2F&provider=injected&second=%2f&&first=again&api%5Fkey=injected"
			const want = "first=%2F&second=%2f&&first=again"
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.RawQuery != want {
					t.Errorf("RawQuery = %q, want %q", request.URL.RawQuery, want)
				}
				writeSuccess(writer, "raw-query")
			}))
			defer server.Close()

			runtime := newTestRuntime(t)
			spec := compatibleSpec(server.URL)
			spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + test.basePrefix + `"}`)
			spec = freezeTestAttempt(spec)
			spec.Query = nil
			spec.RawQuery = raw
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if result.Error != nil {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestRuntimeStreamStopsWithoutDoneOnSinkFailure(t *testing.T) {
	t.Parallel()

	server := slowStreamServer(t, 200*time.Millisecond)
	defer server.Close()
	runtime := newTestRuntime(t)
	sinkErr := errors.New("sink closed")
	var events []execution.StreamEvent
	result := runtime.ExecuteStream(context.Background(), compatibleSpec(server.URL), func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		if event.Kind == execution.StreamEventData {
			return sinkErr
		}
		return nil
	})
	if result.Error == nil || result.Error.Kind != execution.ErrorKindCanceled {
		t.Fatalf("unexpected result: %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
	for _, event := range events {
		if string(event.Data) == "data: [DONE]\n\n" {
			t.Fatal("DONE emitted after sink failure")
		}
	}
}

func TestRuntimeStreamCancellationAndHTTPErrorDoNotEmitDone(t *testing.T) {
	t.Parallel()

	t.Run("context cancellation", func(t *testing.T) {
		server := slowStreamServer(t, 150*time.Millisecond)
		defer server.Close()
		runtime := newTestRuntime(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var events []execution.StreamEvent
		result := runtime.ExecuteStream(ctx, compatibleSpec(server.URL), func(event execution.StreamEvent) error {
			events = append(events, event.Clone())
			if event.Kind == execution.StreamEventData {
				cancel()
			}
			return nil
		})
		if result.Error == nil || result.Error.Kind != execution.ErrorKindCanceled {
			t.Fatalf("unexpected result: %+v", result)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v", err)
		}
		assertNoDone(t, events)
	})

	t.Run("upstream HTTP error", func(t *testing.T) {
		var calls atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writer.Header().Set("X-Request-Id", "stream-401")
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(writer, `{"error":{"type":"invalid_api_key","message":"`+testAPIKey+`"}}`)
		}))
		defer server.Close()
		runtime := newTestRuntime(t)
		var events []execution.StreamEvent
		result := runtime.ExecuteStream(context.Background(), compatibleSpec(server.URL), func(event execution.StreamEvent) error {
			events = append(events, event.Clone())
			return nil
		})
		if calls.Load() != 1 || result.Error == nil || result.Error.Kind != execution.ErrorKindHTTP || result.StatusCode != http.StatusUnauthorized {
			t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v", err)
		}
		if len(events) != 2 || events[0].Kind != execution.StreamEventReady || events[0].StatusCode != http.StatusUnauthorized || events[1].Kind != execution.StreamEventData {
			t.Fatalf("rejected stream events = %+v", events)
		}
		if bytes.Contains(events[1].Data, []byte(testAPIKey)) || !bytes.Contains(events[1].Data, []byte("[REDACTED]")) {
			t.Fatalf("rejected stream body was not safely redacted: %s", events[1].Data)
		}
		assertNoPrivateLeak(t, result, testAPIKey, "gptload-custom-")
	})
}

func TestRuntimeFirstByteAndStreamIdleTimeouts(t *testing.T) {
	t.Parallel()

	t.Run("first byte", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			time.Sleep(150 * time.Millisecond)
			writeSuccess(writer, "late")
		}))
		defer server.Close()
		runtime := newTestRuntime(t)
		spec := compatibleSpec(server.URL)
		spec.Timeouts.FirstByte = 20 * time.Millisecond
		spec.Timeouts.Request = time.Second
		started := time.Now()
		result := runtime.Execute(context.Background(), spec)
		if elapsed := time.Since(started); elapsed > 120*time.Millisecond {
			t.Fatalf("first-byte gate returned after %s", elapsed)
		}
		if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout || result.ResponseStarted {
			t.Fatalf("unexpected first-byte result: %+v", result)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v", err)
		}
	})

	t.Run("converted unary uses the total request budget", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/tenant/chat/completions" {
				t.Errorf("path = %q", request.URL.Path)
			}
			time.Sleep(80 * time.Millisecond)
			writeSuccess(writer, "converted")
		}))
		defer server.Close()

		runtime := newTestRuntime(t)
		spec := compatibleSpec(server.URL)
		spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `/tenant"}`)
		spec.Timeouts.FirstByte = 20 * time.Millisecond
		spec.Timeouts.Request = time.Second
		spec = freezeTestAttempt(spec)
		result := runtime.Execute(context.Background(), spec)
		if result.Error != nil || result.StatusCode != http.StatusOK {
			t.Fatalf("converted unary result = %+v", result)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v", err)
		}
	})

	t.Run("stream first byte requires a complete data event", func(t *testing.T) {
		finished := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "data: partial\n")
			writer.(http.Flusher).Flush()
			// Core v1.7.7 returns cancellation to its caller but does not
			// guarantee that fasthttp closes an already-open body stream. Keep
			// the fake finite while the assertion below verifies our logical
			// first-event timeout boundary.
			select {
			case <-request.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
			close(finished)
		}))
		defer server.Close()

		runtime := newTestRuntime(t)
		spec := compatibleSpec(server.URL)
		spec.Timeouts.FirstByte = 20 * time.Millisecond
		spec.Timeouts.Request = time.Second
		started := time.Now()
		result := runtime.ExecuteStream(context.Background(), spec, func(execution.StreamEvent) error { return nil })
		if elapsed := time.Since(started); elapsed > 120*time.Millisecond {
			t.Fatalf("first-event gate returned after %s", elapsed)
		}
		if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout || result.ResponseStarted {
			t.Fatalf("unexpected first-event result: %+v", result)
		}
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for finite upstream fixture")
		}
	})

	t.Run("stream idle", func(t *testing.T) {
		server := slowStreamServer(t, 150*time.Millisecond)
		defer server.Close()
		runtime := newTestRuntime(t)
		spec := compatibleSpec(server.URL)
		spec.Timeouts.StreamIdle = 20 * time.Millisecond
		spec.Timeouts.Request = time.Second
		var events []execution.StreamEvent
		result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
			events = append(events, event.Clone())
			return nil
		})
		if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout || !result.ResponseStarted {
			t.Fatalf("unexpected idle result: %+v", result)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v", err)
		}
		if len(events) < 2 || events[0].Kind != execution.StreamEventReady {
			t.Fatalf("events = %+v", events)
		}
		for _, event := range events {
			if string(event.Data) == "data: [DONE]\n\n" {
				t.Fatal("DONE emitted after idle timeout")
			}
		}
	})
}

func TestConvertedStreamRequiresFirstClientFrame(t *testing.T) {
	t.Parallel()

	t.Run("empty Responses stream fails before start", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		runtime := newTestRuntime(t)
		spec := openAIResponsesSpec(execution.OperationResponsesCreate, http.MethodPost, "/v1/responses")
		spec.ChannelID = string(channel.OpenAICompatible)
		spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `/v1"}`)
		spec = freezeTestAttempt(spec)
		var events []execution.StreamEvent
		result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
			events = append(events, event.Clone())
			return nil
		})

		if result.Error == nil || result.ResponseStarted || len(events) != 0 {
			t.Fatalf("empty converted stream = %+v events=%+v", result, events)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v", err)
		}
	})

	t.Run("first-byte timeout waits for a complete converted frame", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/tenant/chat/completions" {
				t.Errorf("path = %q", request.URL.Path)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
			_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-late\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"served\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late\"},\"finish_reason\":null}]}\n\n")
			writer.(http.Flusher).Flush()
		}))
		defer server.Close()

		runtime := newTestRuntime(t)
		spec := compatibleSpec(server.URL)
		spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `/tenant"}`)
		spec.Timeouts.FirstByte = 20 * time.Millisecond
		spec.Timeouts.Request = time.Second
		spec = freezeTestAttempt(spec)
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
			t.Fatalf("converted first-frame result = %+v events=%+v", result, events)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("result validation: %v", err)
		}
	})
}

func TestRuntimeShutdownIsIdempotentAndClosesExecution(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	runtime.Shutdown()
	runtime.Shutdown()
	result := runtime.Execute(context.Background(), compatibleSpec("https://example.com"))
	if result.DispatchState != execution.DispatchNotSent || result.Error == nil || result.Error.Kind != execution.ErrorKindInternal {
		t.Fatalf("unexpected result after shutdown: %+v", result)
	}
}

func TestManagedRuntimeRequiresExplicitStart(t *testing.T) {
	runtime, err := newRuntimeManager(
		runtimeOptions{allowPrivateNetwork: true},
		channel.NewRegistry(),
	)
	if err != nil {
		t.Fatalf("newRuntimeManager() error = %v", err)
	}
	beforeStart := runtime.Execute(context.Background(), execution.AttemptSpec{})
	if beforeStart.DispatchState != execution.DispatchNotSent ||
		beforeStart.Error == nil ||
		beforeStart.Error.Kind != execution.ErrorKindInternal {
		t.Fatalf("Execute() before Start = %+v", beforeStart)
	}

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	runtime.pool.mu.Lock()
	entryCount := len(runtime.pool.entries)
	runtime.pool.mu.Unlock()
	if entryCount != 0 {
		t.Fatalf("Start() created %d provider runtimes, want lazy initialization", entryCount)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("second Start() error = %v, want idempotent success", err)
	}

	runtime.Shutdown()
	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("Start() succeeded after Shutdown")
	}
}

func TestRuntimeCapabilities(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	required, err := execution.NewFeatureSet(execution.FeatureStreaming, execution.FeatureTools)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.Capabilities().Supports(execution.OperationChatCompletion, required) {
		t.Fatal("chat streaming/tools capability missing")
	}
	if !runtime.Capabilities().Supports(execution.OperationResponsesCreate, required) {
		t.Fatal("Responses streaming/tools capability missing")
	}
	resourceFeatures, err := execution.NewFeatureSet(execution.FeatureNativeResourceSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.Capabilities().Supports(execution.OperationResponsesRetrieve, resourceFeatures) {
		t.Fatal("Responses resource capability missing")
	}
	if !runtime.Capabilities().Supports(execution.OperationResponsesCompact, mustTestFeatures(t, execution.FeatureTools)) {
		t.Fatal("Responses compact tools capability missing")
	}
	if runtime.Capabilities().Supports(execution.OperationResponsesCompact, mustTestFeatures(t, execution.FeatureStreaming)) ||
		runtime.Capabilities().Supports(execution.OperationResponsesInputTokens, resourceFeatures) {
		t.Fatal("non-streaming Responses utility capability was overstated")
	}
}

func mustTestFeatures(t *testing.T, features ...execution.Feature) execution.FeatureSet {
	t.Helper()
	set, err := execution.NewFeatureSet(features...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func newTestRuntime(t *testing.T) *testRuntime {
	t.Helper()
	return newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
}

func compatibleSpec(baseURL string) execution.AttemptSpec {
	baseURL = strings.TrimRight(baseURL, "/") + "/v1"
	return freezeTestAttempt(execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID:      "request-1",
		AttemptID:      "attempt-1",
		Sequence:       1,
		ChannelID:      string(channel.OpenAICompatible),
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ClientModel:    "client-model",
		UpstreamModel:  "upstream-model",
		Method:         http.MethodPost,
		Path:           "/v1/chat/completions",
		Query:          make(map[string][]string),
		Header: http.Header{
			"Authorization":       []string{"Bearer client-injected"},
			"Proxy-Authorization": []string{"Basic client-injected"},
			"Api-Key":             []string{"client-injected"},
			"X-Api-Key":           []string{"client-injected"},
			"X-Goog-Api-Key":      []string{"client-injected"},
			"Connection":          []string{"keep-alive"},
			"Proxy-Connection":    []string{"keep-alive"},
			"X-Test-Header":       []string{"forward-me"},
		},
		Body:         []byte(`{"model":"injected-model","provider":"injected-provider","fallbacks":["other/model"],"authorization":"body-injected","api_key":"body-injected","x-api-key":"body-injected","messages":[{"role":"user","content":"hello"}],"vendor_extension":{"precise":1.2300,"nested":{"keep":"yes"}}}`),
		TargetConfig: json.RawMessage(`{"base_url":"` + baseURL + `"}`),
		Timeouts: execution.AttemptTimeouts{
			FirstByte:  time.Second,
			Request:    2 * time.Second,
			StreamIdle: time.Second,
		},
		Credential: execution.NewCredentialSnapshot(7, 3, 2, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	}))
}

func freezeTestAttempt(spec execution.AttemptSpec) execution.AttemptSpec {
	resolved, err := channel.NewRegistry().ResolveExecutionTarget(channel.ID(spec.ChannelID), spec.TargetConfig)
	if err != nil {
		panic("resolve test execution target: " + err.Error())
	}
	mode, ok := resolved.Mode(spec.ClientProtocol, spec.Operation)
	if !ok {
		mode = channel.RouteNative
	}
	spec.TargetKind = string(resolved.ProviderKind)
	spec.RouteMode = execution.RouteMode(mode)
	return execution.NewAttemptSpec(spec)
}

func responseServer(t *testing.T, requestID string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Request-Id", requestID)
		writeSuccess(writer, requestID)
	}))
}

func writeSuccess(writer http.ResponseWriter, content string) {
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, `{"id":"chatcmpl","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"`+content+`"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
}

func slowStreamServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flusher := writer.(http.Flusher)
		_, _ = io.WriteString(writer, "data: {\"id\":\"chatcmpl-slow\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"served\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		time.Sleep(delay)
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

func assertUsage(t *testing.T, evidence *execution.UsageEvidence, want usage.Tokens) {
	t.Helper()
	if evidence == nil {
		t.Fatal("usage evidence is nil")
	}
	if evidence.Normalized.Tokens != want || evidence.Normalized.State != usage.StateComplete {
		t.Fatalf("usage = %+v, want tokens=%+v complete", evidence.Normalized, want)
	}
	if len(evidence.Raw) == 0 || !json.Valid(evidence.Raw) {
		t.Fatalf("raw usage is not JSON: %q", evidence.Raw)
	}
}

func assertNoPrivateLeak(t *testing.T, value any, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	for _, secret := range forbidden {
		if secret != "" && strings.Contains(string(encoded), secret) {
			t.Fatalf("serialized value leaked %q: %s", secret, encoded)
		}
	}
}

func assertNoDone(t *testing.T, events []execution.StreamEvent) {
	t.Helper()
	for _, event := range events {
		if string(event.Data) == "data: [DONE]\n\n" {
			t.Fatal("DONE emitted after stream failure")
		}
	}
}

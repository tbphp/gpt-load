package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const geminiEmbeddingsResponse = `{"embeddings":[{"values":[0.1234567, -0.9876543, 0.42]}],"usageMetadata":{"promptTokenCount":5,"totalTokenCount":5}}`

func geminiEmbeddingsSpec() execution.AttemptSpec {
	spec := geminiSpec(false)
	spec.ClientProtocol, spec.Operation = protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate
	spec.RouteMode, spec.RouteRequirement = execution.RouteConverted, execution.RouteRequirementAny
	spec.ClientModel, spec.UpstreamModel = "public-embedding", "text-embedding-004"
	spec.Path = "/v1/embeddings"
	spec.Header.Set("Content-Type", "application/json")
	spec.Body = []byte(`{"model":"public-embedding","input":"hello world","dimensions":3}`)
	return execution.NewAttemptSpec(spec)
}

func TestGeminiEmbeddingsRouteAndCapability(t *testing.T) {
	target, err := channel.NewRegistry().Resolve(channel.Gemini, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if mode, ok := target.Mode(protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate); !ok || mode != execution.RouteConverted {
		t.Fatalf("Gemini Embeddings route = %s, %t", mode, ok)
	}
	manager := &RuntimeManager{}
	route := channel.RouteDescriptor{
		ClientProtocol: protocol.OpenAIEmbeddings,
		Operation:      execution.OperationEmbeddingsCreate,
		RouteMode:      execution.RouteConverted,
	}
	if err := manager.ValidateRouteCapability(channel.ProviderGemini, route); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []channel.ProviderKind{channel.ProviderOpenAI, channel.ProviderAnthropic, channel.ProviderGoogleVertex, channel.ProviderOpenAICompatible} {
		if err := manager.ValidateRouteCapability(provider, route); err == nil {
			t.Errorf("unexpected converted Embeddings capability for %s", provider)
		}
	}
}

func TestGeminiEmbeddingsConvertsThroughExistingPassthrough(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/v1beta/models/text-embedding-004:batchEmbedContents" || request.URL.RawQuery != "vendor=one" {
			t.Errorf("upstream target = %s %s", request.Method, request.URL)
		}
		if request.Header.Get("X-Goog-Api-Key") != testAPIKey || request.Header.Get("Authorization") != "" {
			t.Error("request did not use the selected Gemini API key")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var got, want any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Error(err)
		}
		expectedReqJSON := `{"requests":[{"content":{"parts":[{"text":"hello world"}]},"model":"models/text-embedding-004","outputDimensionality":3}]}`
		if err := json.Unmarshal([]byte(expectedReqJSON), &want); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Gemini request = %s, want %s", body, expectedReqJSON)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "gemini-emb-request")
		writer.Header().Set("ETag", "gemini-emb-etag")
		if _, err := io.WriteString(writer, geminiEmbeddingsResponse); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})

	for _, tc := range []struct {
		name       string
		body       string
		wantSubstr string
	}{
		{
			name:       "float literal fidelity",
			body:       `{"model":"public-embedding","input":"hello world","dimensions":3}`,
			wantSubstr: `[0.1234567,-0.9876543,0.42]`,
		},
		{
			name:       "base64 format",
			body:       `{"model":"public-embedding","input":"hello world","dimensions":3,"encoding_format":"base64"}`,
			wantSubstr: `"embedding":"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := geminiEmbeddingsSpec()
			spec.RawQuery = "vendor=one&api_key=client-secret&alt=sse"
			spec.Body = []byte(tc.body)
			result := runtime.Execute(t.Context(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("Embeddings result = %+v, error = %+v, validation = %v", result, result.Error, err)
			}
			if result.UpstreamProtocol != protocol.Gemini || result.Model != spec.UpstreamModel ||
				result.UpstreamRequestID != "gemini-emb-request" || result.Header.Get("ETag") != "" {
				t.Fatalf("Embeddings result = %+v", result)
			}
			if !bytes.Contains(result.Body, []byte(tc.wantSubstr)) {
				t.Errorf("result body did not contain %q: %s", tc.wantSubstr, result.Body)
			}
			if !bytes.Contains(result.Body, []byte(`"model":"public-embedding"`)) {
				t.Errorf("result body did not alias model: %s", result.Body)
			}
			assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 5})
		})
	}
	if calls.Load() != 2 || runtime.keyPoolCalls() != 0 {
		t.Fatalf("calls = %d, keyPoolCalls = %d", calls.Load(), runtime.keyPoolCalls())
	}
}

func TestGeminiEmbeddingsRejectsUnsupportedInputsWithoutDispatch(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(500)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})

	var texts101 []string
	for i := 0; i < 101; i++ {
		texts101 = append(texts101, fmt.Sprintf("sample %d", i))
	}
	body101, _ := json.Marshal(map[string]any{"model": "public-embedding", "input": texts101})

	for _, test := range []struct {
		name       string
		body       string
		invalidReq bool
	}{
		{name: "batch over 100", body: string(body101), invalidReq: false},
		{name: "token array input", body: `{"model":"public-embedding","input":[1, 2, 3]}`, invalidReq: false},
		{name: "invalid dimensions", body: `{"model":"public-embedding","input":"test","dimensions":-1}`, invalidReq: true},
		{name: "invalid encoding_format", body: `{"model":"public-embedding","input":"test","encoding_format":"int"}`, invalidReq: true},
		{name: "missing input", body: `{"model":"public-embedding"}`, invalidReq: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := geminiEmbeddingsSpec()
			spec.Body = []byte(test.body)
			result := runtime.Execute(t.Context(), spec)
			if result.DispatchState != execution.DispatchNotSent || result.Error == nil || calls.Load() != 0 {
				t.Fatalf("unsupported input = %+v, upstream calls = %d", result, calls.Load())
			}
			wantKind := execution.ErrorKindConversionUnsupported
			if test.invalidReq {
				wantKind = execution.ErrorKindInvalidRequest
			}
			if result.Error.Kind != wantKind {
				t.Fatalf("error kind = %s, want %s", result.Error.Kind, wantKind)
			}
			if !test.invalidReq && result.Error.Code != execution.ErrorCodeTargetConversionNotSupported {
				t.Fatalf("error code = %s, want %s", result.Error.Code, execution.ErrorCodeTargetConversionNotSupported)
			}
		})
	}
}

func TestGeminiEmbeddingsPreservesErrorsAndStrictContractValidation(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		status    int
		wantError bool
	}{
		{
			name:      "rate limit 429",
			body:      `{"error":{"code":429,"message":"rate limited","status":"RESOURCE_EXHAUSTED"}}`,
			status:    429,
			wantError: true,
		},
		{
			name:      "google 400 invalid api key",
			body:      `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`,
			status:    400,
			wantError: true,
		},
		{
			name:      "dimension mismatch against contract",
			body:      `{"embeddings":[{"values":[0.1, 0.2]}]}`, // contract requested 3
			status:    200,
			wantError: true,
		},
		{
			name:      "null inside float vector",
			body:      `{"embeddings":[{"values":[0.1, null, 0.3]}]}`,
			status:    200,
			wantError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				if _, err := io.WriteString(writer, test.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
			result := runtime.Execute(t.Context(), geminiEmbeddingsSpec())
			if err := result.Validate(); err != nil || calls.Load() != 1 || (result.Error != nil) != test.wantError {
				t.Fatalf("result = %+v, validation = %v, calls = %d", result, err, calls.Load())
			}
			if test.wantError {
				wantStatus := test.status
				if wantStatus == 200 {
					wantStatus = http.StatusBadGateway
				}
				if result.StatusCode != wantStatus || result.DispatchState != execution.DispatchMaybeSent || result.Error.ReplaySafety != execution.ReplaySafetyUnknown {
					t.Fatalf("failure = %+v", result)
				}
				decision := health.JudgeExecution(health.ExecutionAttempt{
					DispatchState: result.DispatchState,
					StatusCode:    result.StatusCode,
					Header:        result.Header,
					Evidence:      result.Error,
				}, health.DecisionContext{})
				if test.status == 200 && decision.Category == health.FailureCategoryClientError {
					t.Fatalf("invalid upstream vector must not be classified as client error: %+v", decision)
				}
				if test.status == 429 {
					if decision.Category != health.FailureCategoryRateLimited || decision.Effect != health.EffectCooldownCredential {
						t.Fatalf("expected 429 to trigger RateLimited with credential cooldown, got: %+v", decision)
					}
				}
			}
		})
	}
}

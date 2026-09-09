package bifrost

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const geminiImagesResponse = `{"modelVersion":"gemini-3.1-flash-image","candidates":[{"content":{"parts":[{"text":"private text"},{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":5,"totalTokenCount":125}}`

func geminiImagesSpec() execution.AttemptSpec {
	spec := geminiSpec(false)
	spec.ClientProtocol, spec.Operation = protocol.OpenAIImages, execution.OperationImagesGenerate
	spec.RouteMode, spec.RouteRequirement = execution.RouteConverted, execution.RouteRequirementAny
	spec.ClientModel, spec.UpstreamModel = "public-image", "gemini-3.1-flash-image"
	spec.Path = "/v1/images/generations"
	spec.Header.Set("Content-Type", "application/json")
	spec.Body = []byte(`{"model":"public-image","prompt":"draw","size":"auto","n":1,"provider":"injected","api_key":"body-secret"}`)
	return execution.NewAttemptSpec(spec)
}

func TestGeminiImagesRouteAndCapability(t *testing.T) {
	target, err := channel.NewRegistry().Resolve(channel.Gemini, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if mode, ok := target.Mode(protocol.OpenAIImages, execution.OperationImagesGenerate); !ok || mode != execution.RouteConverted {
		t.Fatalf("Gemini Images route = %s, %t", mode, ok)
	}
	if _, ok := target.Mode(protocol.OpenAIImages, execution.OperationImagesEdit); ok {
		t.Fatal("Gemini unexpectedly declares image edits")
	}
	manager := &RuntimeManager{}
	route := channel.RouteDescriptor{ClientProtocol: protocol.OpenAIImages, Operation: execution.OperationImagesGenerate, RouteMode: execution.RouteConverted}
	if err := manager.ValidateRouteCapability(channel.ProviderGemini, route); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []channel.ProviderKind{channel.ProviderOpenAI, channel.ProviderAnthropic, channel.ProviderGoogleVertex, channel.ProviderOpenAICompatible} {
		if err := manager.ValidateRouteCapability(provider, route); err == nil {
			t.Errorf("unexpected converted Images capability for %s", provider)
		}
	}
}

func TestGeminiImagesConvertsThroughExistingPassthrough(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/v1beta/models/gemini-3.1-flash-image:generateContent" || request.URL.RawQuery != "vendor=one" {
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
		if err := json.Unmarshal([]byte(`{"contents":[{"role":"user","parts":[{"text":"draw"}]}],"generationConfig":{"responseModalities":["TEXT","IMAGE"]}}`), &want); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Gemini request = %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "gemini-image-request")
		writer.Header().Set("ETag", "old-gemini-representation")
		if _, err := io.WriteString(writer, geminiImagesResponse); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	spec := geminiImagesSpec()
	spec.RawQuery = "vendor=one&api_key=client-secret&alt=sse"
	result := runtime.Execute(t.Context(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("Images result = %+v, validation = %v", result, err)
	}
	if calls.Load() != 1 || result.UpstreamProtocol != protocol.Gemini || result.Model != spec.UpstreamModel ||
		result.UpstreamRequestID != "gemini-image-request" || result.Header.Get("ETag") != "" ||
		!bytes.Contains(result.Body, []byte(`"b64_json":"aW1hZ2U="`)) || !bytes.Contains(result.Body, []byte(`"model":"public-image"`)) ||
		bytes.Contains(result.Body, []byte("private text")) {
		t.Fatalf("Images result = %+v, calls = %d", result, calls.Load())
	}
	assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 60, CacheRead: 40, Output: 25})
	if runtime.keyPoolCalls() != 0 {
		t.Fatal("Bifrost selected a credential instead of using DirectKey")
	}
}

func TestGeminiImagesRejectsUnsupportedInputsWithoutDispatch(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { calls.Add(1); writer.WriteHeader(500) }))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	for _, test := range []struct {
		name, body string
		stream     bool
		invalid    bool
	}{
		{name: "multiple", body: `{"model":"public-image","prompt":"draw","n":2}`},
		{name: "size", body: `{"model":"public-image","prompt":"draw","size":"1024x1024"}`},
		{name: "stream", body: `{"model":"public-image","prompt":"draw","stream":true}`, stream: true},
		{name: "stream without flag", body: `{"model":"public-image","prompt":"draw"}`, stream: true},
		{name: "invalid count", body: `{"model":"public-image","prompt":"draw","n":"2"}`, invalid: true},
		{name: "missing prompt", body: `{"model":"public-image","n":2}`, invalid: true},
		{name: "invalid stream", body: `{"model":"public-image","prompt":"draw","stream":"true"}`, stream: true, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := geminiImagesSpec()
			spec.Body = []byte(test.body)
			var result execution.AttemptResult
			if test.stream {
				stream := runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { t.Error("unexpected streaming event"); return nil })
				result.DispatchState, result.Error = stream.DispatchState, stream.Error
			} else {
				result = runtime.Execute(t.Context(), spec)
			}
			if result.DispatchState != execution.DispatchNotSent || result.Error == nil || calls.Load() != 0 {
				t.Fatalf("unsupported input = %+v, upstream calls = %d", result, calls.Load())
			}
			wantKind := execution.ErrorKindConversionUnsupported
			if test.invalid {
				wantKind = execution.ErrorKindInvalidRequest
			}
			if result.Error.Kind != wantKind || !test.invalid && result.Error.Code != execution.ErrorCodeTargetConversionNotSupported {
				t.Fatalf("input error = %+v, want %s", result.Error, wantKind)
			}
		})
	}
}

func TestGeminiImagesPreservesErrorsAndUsageDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{name: "rate limit", body: `{"error":{"code":429,"message":"rate limited","status":"RESOURCE_EXHAUSTED"}}`, status: 429, wantError: true},
		{name: "no image", body: `{"candidates":[]}`, status: 200, wantError: true},
		{name: "invalid usage", body: strings.Replace(geminiImagesResponse, `"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":5,"totalTokenCount":125`, `"promptTokenCount":"invalid","candidatesTokenCount":"invalid"`, 1), status: 200},
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
			result := runtime.Execute(t.Context(), geminiImagesSpec())
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
			} else {
				want, err := dialect.NewGemini().ExtractUsage([]byte(test.body))
				if err != nil {
					t.Fatal(err)
				}
				if result.Usage == nil || !reflect.DeepEqual(result.Usage.Normalized, want) {
					t.Fatalf("usage = %+v, want %+v", result.Usage, want)
				}
			}
		})
	}
}

func TestGeminiImagesKeepsPassthroughResponseBounds(t *testing.T) {
	for _, test := range []struct {
		name    string
		limit   int64
		summary string
	}{
		{name: "compressed", summary: "encoded upstream response cannot be safely forwarded"},
		{name: "oversized", limit: 128, summary: "upstream response exceeds size limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("Content-Encoding", "gzip")
				compressed := gzip.NewWriter(writer)
				if _, err := io.WriteString(compressed, geminiImagesResponse); err != nil {
					t.Error(err)
				}
				if err := compressed.Close(); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta", maxUnaryResponseBodyBytes: test.limit})
			result := runtime.Execute(t.Context(), geminiImagesSpec())
			if result.Error == nil || result.Error.Summary != test.summary || result.Error.ReplaySafety != execution.ReplaySafetyUnknown || len(result.Body) != 0 {
				t.Fatalf("unsafe response = %+v, evidence = %+v", result, result.Error)
			}
		})
	}
}

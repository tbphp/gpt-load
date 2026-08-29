package bifrost

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"unsafe"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenAIEmbeddingsRuntimeUsesOneTypedRawWireForSupportedChannels(t *testing.T) {
	t.Parallel()

	const responseBody = `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.12345678901234567,-0.00000000000000001234]}],"model":"provider-model","usage":{"prompt_tokens":1,"total_tokens":1},"vendor":{"precise":1.2300}}`
	for _, test := range []struct {
		name       string
		channelID  channel.ID
		baseSuffix string
		wantPath   string
	}{
		{name: "OpenAI", channelID: channel.OpenAI, wantPath: "/v1/embeddings"},
		{name: "OpenRouter", channelID: channel.OpenRouter, wantPath: "/v1/embeddings"},
		{name: "OpenAI Compatible complete prefix", channelID: channel.OpenAICompatible, baseSuffix: "/tenant/api/v4", wantPath: "/tenant/api/v4/embeddings"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.wantPath {
					t.Errorf("path = %q, want %q", request.URL.Path, test.wantPath)
				}
				if got := request.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
					t.Errorf("Authorization = %q", got)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
					return
				}
				var object map[string]json.RawMessage
				if err := json.Unmarshal(body, &object); err != nil {
					t.Errorf("request body = %s: %v", body, err)
					return
				}
				if string(object["model"]) != `"provider-model"` ||
					string(object["input"]) != `"hello"` ||
					string(object["dimensions"]) != `8` ||
					string(object["user"]) != `"tenant"` ||
					string(object["vendor_extension"]) != `{"precise":1.2300}` {
					t.Errorf("typed/raw request changed: %s", body)
				}
				for _, field := range []string{"stream", "provider", "fallbacks", "api_key"} {
					if _, exists := object[field]; exists {
						t.Errorf("control field %q reached upstream: %s", field, body)
					}
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "embedding-request")
				_, _ = io.WriteString(writer, responseBody)
			}))
			defer server.Close()

			options := testRuntimeOptions{allowPrivateNetwork: true}
			if test.channelID == channel.OpenAI {
				options.openAIBaseURL = server.URL
			}
			runtime := newProtocolTestRuntime(t, options)
			spec := openAIEmbeddingsSpec(test.channelID, server.URL+test.baseSuffix,
				[]byte(`{"model":"provider-model","input":"hello","stream":false,"dimensions":8,"user":"tenant","provider":"client","fallbacks":["other"],"api_key":"client","vendor_extension":{"precise":1.2300}}`))
			result := runtime.Execute(context.Background(), spec)

			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if result.Error != nil || result.StatusCode != http.StatusOK ||
				result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted ||
				result.Model != "provider-model" || result.UpstreamProtocol != protocol.OpenAIEmbeddings ||
				result.UpstreamRequestID != "embedding-request" || !bytes.Equal(result.Body, []byte(responseBody)) {
				t.Fatalf("result = %+v error=%+v body=%s", result, result.Error, result.Body)
			}
			if runtime.keyPoolCalls() != 0 {
				t.Fatalf("key-pool calls = %d, want DirectKey only", runtime.keyPoolCalls())
			}
		})
	}
}

func TestOpenAIEmbeddingsRequestShapeAndCapabilityAreExact(t *testing.T) {
	t.Parallel()

	manager := &RuntimeManager{}
	for _, providerKind := range []channel.ProviderKind{
		channel.ProviderOpenAI,
		channel.ProviderOpenRouter,
		channel.ProviderOpenAICompatible,
	} {
		for _, operation := range []execution.Operation{
			execution.OperationEmbeddingsCreate,
			execution.OperationProbe,
		} {
			if err := manager.ValidateRouteCapability(providerKind, channel.RouteDescriptor{
				ClientProtocol: protocol.OpenAIEmbeddings,
				Operation:      operation,
				RouteMode:      execution.RouteNative,
			}); err != nil {
				t.Errorf("ValidateRouteCapability(%q, %q) error = %v", providerKind, operation, err)
			}
		}
	}
	for _, providerKind := range []channel.ProviderKind{
		channel.ProviderAnthropic,
		channel.ProviderGemini,
		channel.ProviderXAI,
	} {
		if err := manager.ValidateRouteCapability(providerKind, channel.RouteDescriptor{
			ClientProtocol: protocol.OpenAIEmbeddings,
			Operation:      execution.OperationEmbeddingsCreate,
			RouteMode:      execution.RouteNative,
		}); err == nil {
			t.Errorf("provider %q unexpectedly supports Embeddings", providerKind)
		}
	}

	valid := openAIEmbeddingsSpec(channel.OpenAICompatible, "https://example.com/v1",
		[]byte(`{"model":"provider-model","input":"hello"}`))
	if !supportedRequestShape(valid, false) || supportedRequestShape(valid, true) {
		t.Fatal("Embeddings shape must be unary only")
	}
	for _, mutate := range []func(*execution.AttemptSpec){
		func(spec *execution.AttemptSpec) { spec.Method = http.MethodGet },
		func(spec *execution.AttemptSpec) { spec.Path = "/v1/embedding" },
		func(spec *execution.AttemptSpec) { spec.Operation = execution.OperationChatCompletion },
		func(spec *execution.AttemptSpec) { spec.RouteMode = execution.RouteConverted },
	} {
		invalid := valid.Clone()
		mutate(&invalid)
		if supportedRequestShape(invalid, false) {
			t.Errorf("invalid Embeddings shape was accepted: %+v", invalid)
		}
	}

	probe := valid.Clone()
	probe.Operation = execution.OperationProbe
	probe.Method = ""
	probe.Path = ""
	probe.Body = nil
	if !supportedRequestShape(probe, false) || supportedRequestShape(probe, true) {
		t.Fatal("Embeddings probe shape must be semantic and unary only")
	}
}

func TestTakeRawEmbeddingResponseMovesOwnershipAndClearsRaw(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"object":"list","data":[{"embedding":[1]}]}`)
	response := &schemas.BifrostEmbeddingResponse{}
	response.ExtraFields.RawResponse = raw
	body, ok := takeRawEmbeddingResponse(response)
	if !ok || !bytes.Equal(body, raw) {
		t.Fatalf("takeRawEmbeddingResponse() = %s, %t", body, ok)
	}
	if response.ExtraFields.RawResponse != nil {
		t.Fatalf("RawResponse remains = %#v", response.ExtraFields.RawResponse)
	}
	if len(body) == 0 || unsafe.SliceData(body) != unsafe.SliceData([]byte(raw)) {
		t.Fatal("takeRawEmbeddingResponse copied the owned response body")
	}
}

func TestMaterializedEmbeddingResponseKeepsVectorsOpaque(t *testing.T) {
	t.Parallel()

	for _, body := range [][]byte{
		[]byte(`{"object":"list","data":[{"index":0,"embedding":[0.12345678901234567,1]}]}`),
		[]byte(`{"object":"list","data":[{"index":0,"embedding":"AAAA"}]}`),
	} {
		response, err := materializedEmbeddingResponse(body)
		if err != nil {
			t.Fatalf("materializedEmbeddingResponse(%s) error = %v", body, err)
		}
		if response.Data != nil {
			t.Fatalf("materializedEmbeddingResponse(%s) decoded vector data", body)
		}
		raw, ok := takeRawEmbeddingResponse(response)
		if !ok || !bytes.Equal(raw, body) || unsafe.SliceData(raw) != unsafe.SliceData(body) {
			t.Fatalf("materializedEmbeddingResponse(%s) did not retain raw body ownership", body)
		}
	}

	if _, err := materializedEmbeddingResponse([]byte(`{"data":[}`)); err == nil {
		t.Fatal("materializedEmbeddingResponse accepted invalid JSON")
	}
}

func TestEmbeddingWireFidelityFlagsAreRequestLocal(t *testing.T) {
	t.Parallel()

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	enableEmbeddingWireFidelity(ctx)
	for _, key := range []schemas.BifrostContextKey{
		schemas.BifrostContextKeyAllowPerRequestRawOverride,
		schemas.BifrostContextKeyUseRawRequestBody,
		schemas.BifrostContextKeySendBackRawResponse,
		schemas.BifrostContextKeyPassthroughExtraParams,
	} {
		if value, ok := ctx.Value(key).(bool); !ok || !value {
			t.Errorf("context flag %q = %#v", key, ctx.Value(key))
		}
	}
	if got, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseThreshold).(int64); got != embeddingDecodedResponseThresholdBytes {
		t.Fatalf("decoded response threshold = %d", got)
	}
	if got, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadPrefetchSize).(int); got != embeddingDecodedResponsePrefetchBytes {
		t.Fatalf("decoded response prefetch = %d", got)
	}
}

func TestOpenAIEmbeddingsProviderRejectionRemainsReplayUnknown(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "embedding-rejected")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":{"type":"invalid_request_error","code":"unsupported_model","message":"unsupported"}}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	result := runtime.Execute(context.Background(), openAIEmbeddingsSpec(
		channel.OpenAICompatible,
		server.URL+"/v1",
		[]byte(`{"model":"provider-model","input":"hello"}`),
	))
	if result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted ||
		result.StatusCode != http.StatusBadRequest || result.Error == nil ||
		result.Error.ReplaySafety != execution.ReplaySafetyUnknown ||
		result.UpstreamRequestID != "embedding-rejected" {
		t.Fatalf("result = %+v error=%+v", result, result.Error)
	}
}

func TestOpenAIEmbeddingsRuntimeEnforcesConfiguredResponseLimit(t *testing.T) {
	const limit = int64(4 << 10)
	for _, test := range []struct {
		name      string
		bodyBytes int
		wantError bool
	}{
		{name: "below limit", bodyBytes: int(limit) - 1},
		{name: "above limit", bodyBytes: int(limit) + 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			prefix := []byte(`{"object":"list","data":[{"index":0,"embedding":"`)
			suffix := []byte(`"}]}`)
			payloadBytes := test.bodyBytes - len(prefix) - len(suffix)
			body := make([]byte, 0, test.bodyBytes)
			body = append(body, prefix...)
			body = append(body, bytes.Repeat([]byte{'A'}, payloadBytes)...)
			body = append(body, suffix...)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write(body)
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{
				allowPrivateNetwork:       true,
				maxUnaryResponseBodyBytes: limit,
			})
			result := runtime.Execute(context.Background(), openAIEmbeddingsSpec(
				channel.OpenAICompatible,
				server.URL+"/v1",
				[]byte(`{"model":"provider-model","input":"hello","encoding_format":"base64"}`),
			))
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if test.wantError {
				if result.Error == nil || result.DispatchState != execution.DispatchMaybeSent ||
					!result.ResponseStarted || len(result.Body) != 0 ||
					result.Error.ReplaySafety != execution.ReplaySafetyUnknown {
					t.Fatalf("over-limit result = %+v", result)
				}
				return
			}
			if result.Error != nil || !bytes.Equal(result.Body, body) {
				t.Fatalf("below-limit result = %+v body=%d/%d", result, len(result.Body), len(body))
			}
		})
	}
}

func TestOpenAIEmbeddingsRuntimeBoundsDecodedGzipResponse(t *testing.T) {
	const limit = int64(4 << 10)
	for _, test := range []struct {
		name      string
		bodyBytes int
		wantError bool
	}{
		{name: "decoded body below limit", bodyBytes: int(limit) - 1},
		{name: "decoded body above limit", bodyBytes: int(limit) + 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plain := embeddingResponseBodyOfSize(t, test.bodyBytes)
			var encoded bytes.Buffer
			writer := gzip.NewWriter(&encoded)
			if _, err := writer.Write(plain); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if encoded.Len() >= test.bodyBytes {
				t.Fatalf("gzip fixture did not compress: %d >= %d", encoded.Len(), test.bodyBytes)
			}
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				response.Header().Set("Content-Encoding", "gzip")
				response.Header().Set("ETag", `"compressed-wire"`)
				_, _ = response.Write(encoded.Bytes())
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{
				allowPrivateNetwork:       true,
				maxUnaryResponseBodyBytes: limit,
			})
			result := runtime.Execute(context.Background(), openAIEmbeddingsSpec(
				channel.OpenAICompatible,
				server.URL+"/v1",
				[]byte(`{"model":"provider-model","input":"hello","encoding_format":"base64"}`),
			))
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if test.wantError {
				if result.Error == nil || len(result.Body) != 0 ||
					result.Error.ReplaySafety != execution.ReplaySafetyUnknown {
					t.Fatalf("over-limit gzip result = %+v", result)
				}
				return
			}
			if result.Error != nil || !bytes.Equal(result.Body, plain) ||
				result.Header.Get("Content-Encoding") != "" ||
				result.Header.Get("ETag") != "" {
				t.Fatalf("below-limit gzip result = %+v body=%d/%d", result, len(result.Body), len(plain))
			}
		})
	}
}

func TestOpenAIEmbeddingsOverLimitResponseDoesNotDrainStalledStream(t *testing.T) {
	const limit = int64(32 << 10)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Content-Length", "131072")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(bytes.Repeat([]byte{'A'}, 64<<10))
		response.(http.Flusher).Flush()
		<-release
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{
		allowPrivateNetwork:       true,
		maxUnaryResponseBodyBytes: limit,
	})
	spec := openAIEmbeddingsSpec(
		channel.OpenAICompatible,
		server.URL+"/v1",
		[]byte(`{"model":"provider-model","input":"hello"}`),
	)
	spec.Timeouts.Request = 50 * time.Millisecond
	resultChannel := make(chan execution.AttemptResult, 1)
	go func() {
		resultChannel <- runtime.Execute(context.Background(), spec)
	}()

	var result execution.AttemptResult
	returnedPromptly := true
	select {
	case result = <-resultChannel:
	case <-time.After(250 * time.Millisecond):
		returnedPromptly = false
	}
	close(release)
	if !returnedPromptly {
		select {
		case <-resultChannel:
		case <-time.After(time.Second):
		}
		t.Fatal("over-limit response blocked while draining a stalled upstream stream")
	}
	if err := result.Validate(); err != nil || result.Error == nil ||
		result.Error.ReplaySafety != execution.ReplaySafetyUnknown {
		t.Fatalf("result = %+v validation=%v", result, err)
	}
}

func embeddingResponseBodyOfSize(t *testing.T, size int) []byte {
	t.Helper()
	prefix := []byte(`{"object":"list","data":[{"index":0,"embedding":"`)
	suffix := []byte(`"}]}`)
	payloadBytes := size - len(prefix) - len(suffix)
	if payloadBytes < 0 {
		t.Fatalf("response fixture size %d is too small", size)
	}
	body := make([]byte, 0, size)
	body = append(body, prefix...)
	body = append(body, bytes.Repeat([]byte{'A'}, payloadBytes)...)
	body = append(body, suffix...)
	return body
}

func openAIEmbeddingsSpec(channelID channel.ID, baseURL string, body []byte) execution.AttemptSpec {
	return freezeTestAttempt(execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "embeddings-request", AttemptID: "embeddings-attempt", Sequence: 1,
		ChannelID: string(channelID), RouteMode: execution.RouteNative,
		ClientProtocol: protocol.OpenAIEmbeddings, Operation: execution.OperationEmbeddingsCreate,
		RouteRequirement: execution.RouteRequirementNative,
		ClientModel:      "provider-model", UpstreamModel: "provider-model",
		Method: http.MethodPost, Path: "/v1/embeddings", Query: make(map[string][]string),
		Header: http.Header{
			"Content-Type":        {"application/json"},
			"Authorization":       {"Bearer client"},
			"Proxy-Authorization": {"Basic client"},
			"Api-Key":             {"client"},
			"X-Api-Key":           {"client"},
		},
		Body: body, TargetConfig: json.RawMessage(`{"base_url":"` + baseURL + `"}`),
		Timeouts:   execution.AttemptTimeouts{FirstByte: time.Second, Request: 2 * time.Second},
		Credential: execution.NewCredentialSnapshot(23, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	}))
}

package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	core "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestBifrostEmbeddingWirePreservesRawOpenAIContract(t *testing.T) {
	tests := []struct {
		name     string
		input    *schemas.EmbeddingInput
		body     string
		response string
	}{
		{
			name:  "string input with optional and unknown fields",
			input: &schemas.EmbeddingInput{Text: schemas.Ptr("hello")},
			body:  `{"model":"provider-model","input":"hello","dimensions":3,"encoding_format":"float","user":"user-1","vendor_extension":{"precise":1.2300}}`,
			response: `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.12345678901234567,-0.00000000000000001234]}],` +
				`"model":"provider-model","usage":{"prompt_tokens":1,"total_tokens":1},"vendor":{"precise":1.2300}}`,
		},
		{
			name:     "string array input and base64 response",
			input:    &schemas.EmbeddingInput{Texts: []string{"hello", "world"}},
			body:     `{"model":"provider-model","input":["hello","world"],"encoding_format":"base64"}`,
			response: `{"object":"list","data":[{"object":"embedding","index":0,"embedding":"AQIDBA=="}],"model":"provider-model","usage":{"prompt_tokens":2,"total_tokens":2}}`,
		},
		{
			name:     "token array input",
			input:    &schemas.EmbeddingInput{Embedding: []int{10, 20, 30}},
			body:     `{"model":"provider-model","input":[10,20,30],"encoding_format":"float"}`,
			response: `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.25,-0.5]}],"model":"provider-model","usage":{"prompt_tokens":3,"total_tokens":3}}`,
		},
		{
			name:     "token matrix input",
			input:    &schemas.EmbeddingInput{Embeddings: [][]int{{10, 20}, {30, 40}}},
			body:     `{"model":"provider-model","input":[[10,20],[30,40]],"encoding_format":"float"}`,
			response: `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.25,-0.5]},{"object":"embedding","index":1,"embedding":[0.75,-1]}],"model":"provider-model","usage":{"prompt_tokens":4,"total_tokens":4}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != "/v1/embeddings" {
					t.Errorf("path = %q, want /v1/embeddings", request.URL.Path)
				}
				if got := request.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
					t.Errorf("Authorization = %q", got)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
					return
				}
				if !bytes.Equal(body, []byte(test.body)) {
					t.Errorf("request body changed:\n got: %s\nwant: %s", body, test.body)
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "embedding-request-1")
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			wire := newEmbeddingWireTestRuntime(t, schemas.OpenAI, server.URL+"/v1", true)
			response, bifrostErr := wire.request(test.input, []byte(test.body), nil)
			if bifrostErr != nil || response == nil {
				t.Fatalf("EmbeddingRequest() response=%#v error=%v", response, bifrostErr)
			}
			if got := rawEmbeddingResponseBytes(t, response.ExtraFields.RawResponse); !bytes.Equal(got, []byte(test.response)) {
				t.Fatalf("raw response changed:\n got: %s\nwant: %s", got, test.response)
			}
			if calls.Load() != 1 {
				t.Fatalf("upstream calls = %d, want 1", calls.Load())
			}
			if wire.account.keyPoolCalls.Load() != 0 {
				t.Fatalf("key-pool calls = %d, want DirectKey only", wire.account.keyPoolCalls.Load())
			}
		})
	}
}

func TestBifrostEmbeddingWireUsesCompleteCompatiblePrefix(t *testing.T) {
	for _, test := range []struct {
		name       string
		baseSuffix string
		wantPath   string
	}{
		{name: "standard v1 prefix", baseSuffix: "/v1", wantPath: "/v1/embeddings"},
		{name: "nonstandard complete prefix", baseSuffix: "/gateway/openai", wantPath: "/gateway/openai/embeddings"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != test.wantPath {
					t.Errorf("path = %q, want %q", request.URL.Path, test.wantPath)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1]}],"model":"provider-model","usage":{"prompt_tokens":1,"total_tokens":1}}`)
			}))
			defer server.Close()

			wire := newEmbeddingWireTestRuntime(t, schemas.OpenAI, server.URL+test.baseSuffix, true)
			response, bifrostErr := wire.request(
				&schemas.EmbeddingInput{Text: schemas.Ptr("hello")},
				[]byte(`{"model":"provider-model","input":"hello"}`),
				nil,
			)
			if bifrostErr != nil || response == nil {
				t.Fatalf("EmbeddingRequest() response=%#v error=%v", response, bifrostErr)
			}
			if calls.Load() != 1 || wire.account.keyPoolCalls.Load() != 0 {
				t.Fatalf("upstream/key-pool calls = %d/%d, want 1/0", calls.Load(), wire.account.keyPoolCalls.Load())
			}
		})
	}
}

func TestBifrostEmbeddingWireSupportsOpenRouterTypedRequest(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q, want /v1/embeddings", request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		if payload["model"] != "provider-model" || payload["input"] != "hello" || payload["dimensions"] != float64(8) || payload["user"] != "user-1" {
			t.Errorf("typed request body = %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1]}],"model":"provider-model","usage":{"prompt_tokens":1,"total_tokens":1}}`)
	}))
	defer server.Close()

	wire := newEmbeddingWireTestRuntime(t, schemas.OpenRouter, server.URL, false)
	dimensions := 8
	response, bifrostErr := wire.request(
		&schemas.EmbeddingInput{Text: schemas.Ptr("hello")},
		nil,
		&schemas.EmbeddingParameters{
			Dimensions: &dimensions,
			ExtraParams: map[string]any{
				"user": "user-1",
			},
		},
	)
	if bifrostErr != nil || response == nil {
		t.Fatalf("EmbeddingRequest() response=%#v error=%v", response, bifrostErr)
	}
	if calls.Load() != 1 || wire.account.keyPoolCalls.Load() != 0 {
		t.Fatalf("upstream/key-pool calls = %d/%d, want 1/0", calls.Load(), wire.account.keyPoolCalls.Load())
	}
}

func TestBifrostEmbeddingWirePreservesNon2xxEvidence(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "embedding-429")
		writer.Header().Set("Retry-After", "3")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, `{"error":{"type":"rate_limit_error","code":"rate_limit","message":"slow down"}}`)
	}))
	defer server.Close()

	wire := newEmbeddingWireTestRuntime(t, schemas.OpenAI, server.URL+"/v1", true)
	response, bifrostErr := wire.request(
		&schemas.EmbeddingInput{Text: schemas.Ptr("hello")},
		[]byte(`{"model":"provider-model","input":"hello"}`),
		nil,
	)
	if response != nil || bifrostErr == nil {
		t.Fatalf("EmbeddingRequest() response=%#v error=%v", response, bifrostErr)
	}
	if bifrostErr.StatusCode == nil || *bifrostErr.StatusCode != http.StatusTooManyRequests ||
		bifrostErr.Error == nil || bifrostErr.Error.Type == nil || *bifrostErr.Error.Type != "rate_limit_error" ||
		bifrostErr.Error.Code == nil || *bifrostErr.Error.Code != "rate_limit" {
		t.Fatalf("error evidence = %v", bifrostErr)
	}
	headers := wire.responseHeaders()
	if headers.Get("X-Request-Id") != "embedding-429" || headers.Get("Retry-After") != "3" {
		t.Fatalf("response headers = %#v", headers)
	}
	rawError, err := json.Marshal(bifrostErr.ExtraFields.RawResponse)
	if err != nil || !bytes.Contains(rawError, []byte(`"rate_limit_error"`)) {
		t.Fatalf("raw error response = %s, err=%v", rawError, err)
	}
	if calls.Load() != 1 || wire.account.keyPoolCalls.Load() != 0 {
		t.Fatalf("upstream/key-pool calls = %d/%d, want 1/0", calls.Load(), wire.account.keyPoolCalls.Load())
	}
}

type embeddingWireTestRuntime struct {
	core     *core.Bifrost
	account  *directAccount
	provider schemas.ModelProvider
	context  *schemas.BifrostContext
}

func newEmbeddingWireTestRuntime(
	t *testing.T,
	baseProvider schemas.ModelProvider,
	baseURL string,
	custom bool,
) *embeddingWireTestRuntime {
	t.Helper()
	provider := baseProvider
	if custom {
		provider = customProviderKey(baseProvider, baseURL)
	}
	config := providerConfig(baseURL, custom, baseProvider, true)
	account := newDirectAccount()
	account.setConfig(provider, config)
	bifrostCore, err := core.Init(context.Background(), schemas.BifrostConfig{
		Account:         account,
		LLMPlugins:      []schemas.LLMPlugin{},
		MCPPlugins:      []schemas.MCPPlugin{},
		Logger:          core.NewNoOpLogger(),
		InitialPoolSize: 1,
	})
	if err != nil {
		t.Fatalf("initialize Bifrost embedding wire: %v", err)
	}
	t.Cleanup(bifrostCore.Shutdown)
	return &embeddingWireTestRuntime{core: bifrostCore, account: account, provider: provider}
}

func (runtime *embeddingWireTestRuntime) request(
	input *schemas.EmbeddingInput,
	rawBody []byte,
	params *schemas.EmbeddingParameters,
) (*schemas.BifrostEmbeddingResponse, *schemas.BifrostError) {
	bifrostContext := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bifrostContext.SetValue(schemas.BifrostContextKeyRequestID, "embedding-wire-gate")
	bifrostContext.SetValue(schemas.BifrostContextKeyDirectKey, schemas.Key{
		ID:     "embedding-key",
		Name:   "selected credential",
		Value:  plainSecret(testAPIKey),
		Models: schemas.WhiteList{"*"},
		Weight: 1,
	})
	bifrostContext.SetValue(schemas.BifrostContextKeyAllowPerRequestRawOverride, true)
	bifrostContext.SetValue(schemas.BifrostContextKeySendBackRawResponse, true)
	bifrostContext.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
	if len(rawBody) > 0 {
		bifrostContext.SetValue(schemas.BifrostContextKeyUseRawRequestBody, true)
	}
	runtime.context = bifrostContext
	return runtime.core.EmbeddingRequest(bifrostContext, &schemas.BifrostEmbeddingRequest{
		Provider:       runtime.provider,
		Model:          "provider-model",
		Input:          input,
		Params:         params,
		RawRequestBody: bytes.Clone(rawBody),
	})
}

func (runtime *embeddingWireTestRuntime) responseHeaders() http.Header {
	return responseHeaders(nil, runtime.context, false)
}

func rawEmbeddingResponseBytes(t *testing.T, raw any) []byte {
	t.Helper()
	switch value := raw.(type) {
	case json.RawMessage:
		return bytes.Clone(value)
	case []byte:
		return bytes.Clone(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal raw response: %v", err)
		}
		return encoded
	}
}

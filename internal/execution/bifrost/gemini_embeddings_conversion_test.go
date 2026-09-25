package bifrost

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func convertedGeminiEmbeddingsSpec(body string) execution.AttemptSpec {
	return freezeTestAttempt(execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "converted-embeddings-request", AttemptID: "converted-embeddings-attempt", Sequence: 1,
		ChannelID: string(channel.Gemini), ClientProtocol: protocol.OpenAIEmbeddings,
		Operation: execution.OperationEmbeddingsCreate, RouteMode: execution.RouteConverted,
		RouteRequirement: execution.RouteRequirementAny,
		ClientModel:      "public-embedding", UpstreamModel: "gemini-embedding-001",
		Method: http.MethodPost, Path: "/v1/embeddings",
		Header: http.Header{"Authorization": {"Bearer client"}, "Content-Type": {"application/json"}},
		Body:   []byte(body), TargetConfig: json.RawMessage(`{}`),
		Credential: execution.NewCredentialSnapshot(9, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	}))
}

func TestGeminiConvertsOpenAIEmbeddingsToBatchEmbedContents(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/v1beta/models/gemini-embedding-001:batchEmbedContents" {
			t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Goog-Api-Key") != testAPIKey || request.Header.Get("Authorization") != "" {
			t.Error("request did not use the selected Gemini API key")
		}
		body, _ := io.ReadAll(request.Body)
		want := `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"a"}]},"outputDimensionality":3},{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"b"}]},"outputDimensionality":3}]}`
		if !bytes.Equal(bytes.TrimSpace(body), []byte(want)) {
			t.Errorf("upstream body = %s, want %s", body, want)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"embeddings":[{"values":[0.1,0.2,0.3]},{"values":[0.4,0.5,0.6]}],"usageMetadata":{"promptTokenCount":4,"totalTokenCount":4}}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	result := runtime.Execute(t.Context(), convertedGeminiEmbeddingsSpec(
		`{"model":"public-embedding","input":["a","b"],"dimensions":3,"provider":"client","api_key":"body"}`,
	))
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, error = %+v, validation = %v", result, result.Error, err)
	}
	if result.UpstreamProtocol != protocol.GeminiEmbeddings || result.Model != "gemini-embedding-001" ||
		result.Header.Get("Content-Type") != "application/json" || calls.Load() != 1 {
		t.Fatalf("result = %+v", result)
	}
	var response struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Data   []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(result.Body, &response); err != nil || response.Object != "list" ||
		response.Model != "public-embedding" || len(response.Data) != 2 || response.Data[1].Index != 1 ||
		response.Data[1].Embedding[2] != 0.6 || response.Usage.PromptTokens != 4 {
		t.Fatalf("converted response = %s, err = %v", result.Body, err)
	}
	assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 4})
}

func TestGeminiEmbeddingsConversionRejectsTokenIDsWithoutDispatch(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	for _, test := range []struct {
		body string
		kind execution.ErrorKind
	}{
		{body: `{"model":"public-embedding","input":[1,2,3]}`, kind: execution.ErrorKindConversionUnsupported},
		{body: `{"model":"public-embedding","input":[]}`, kind: execution.ErrorKindInvalidRequest},
	} {
		result := runtime.Execute(t.Context(), convertedGeminiEmbeddingsSpec(test.body))
		if result.DispatchState != execution.DispatchNotSent || result.Error == nil || result.Error.Kind != test.kind {
			t.Fatalf("%s result = %+v, error = %+v", test.body, result, result.Error)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d", calls.Load())
	}
}

func TestGeminiEmbeddingsConversionRejectsInvalidUpstreamVectors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"embeddings":[{"values":[0.1]}]}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	result := runtime.Execute(t.Context(), convertedGeminiEmbeddingsSpec(`{"model":"public-embedding","input":["a","b"]}`))
	if err := result.Validate(); err != nil || result.Error == nil ||
		result.StatusCode != http.StatusBadGateway || result.Error.Code != "invalid_embedding_response" {
		t.Fatalf("result = %+v, error = %+v, validation = %v", result, result.Error, err)
	}
}

func TestGeminiEmbeddingsConversionRouteCapability(t *testing.T) {
	t.Parallel()

	manager := &RuntimeManager{}
	route := channel.RouteDescriptor{
		ClientProtocol: protocol.OpenAIEmbeddings,
		Operation:      execution.OperationEmbeddingsCreate,
		RouteMode:      execution.RouteConverted,
	}
	if err := manager.ValidateRouteCapability(channel.ProviderGemini, route); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []channel.ProviderKind{channel.ProviderOpenAI, channel.ProviderMultiProtocolGateway, channel.ProviderGoogleVertex} {
		if err := manager.ValidateRouteCapability(provider, route); err == nil {
			t.Errorf("unexpected converted Embeddings capability for %s", provider)
		}
	}
}

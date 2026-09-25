package bifrost

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/provideradapter"
	"gpt-load/internal/usage"
)

const geminiNativeEmbeddingsResponse = `{"embeddings":[{"values":[0.1234567,-0.9876543,1e-7]}],"usageMetadata":{"promptTokenCount":5,"totalTokenCount":5}}`

func geminiNativeEmbeddingsSpec(channelID channel.ID, action, body string, targetConfig json.RawMessage) execution.AttemptSpec {
	return freezeTestAttempt(execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "gemini-embeddings-request", AttemptID: "gemini-embeddings-attempt", Sequence: 1,
		ChannelID: string(channelID), ClientProtocol: protocol.GeminiEmbeddings,
		Operation: execution.OperationEmbeddingsCreate, RouteMode: execution.RouteNative,
		RouteRequirement: execution.RouteRequirementNative,
		ClientModel:      "public-embedding", UpstreamModel: "upstream-embedding",
		Method: http.MethodPost, Path: "/v1beta/models/public-embedding:" + action,
		RawQuery: "trace=kept&key=client-query&api_key=client-query",
		Header: http.Header{
			"Authorization":  {"Bearer client"},
			"X-Goog-Api-Key": {"client"},
			"Content-Type":   {"application/json"},
		},
		Body:         []byte(body),
		TargetConfig: targetConfig,
		Credential:   execution.NewCredentialSnapshot(9, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	}))
}

func geminiNativeEmbeddingsCases() []struct {
	name     string
	action   string
	body     string
	wantBody string
} {
	return []struct {
		name     string
		action   string
		body     string
		wantBody string
	}{
		{
			name:     "embed content",
			action:   "embedContent",
			body:     `{"model":"models/public-embedding","content":{"parts":[{"text":"hello"}]},"outputDimensionality":3,"provider":"client","api_key":"body"}`,
			wantBody: `{"model":"models/upstream-embedding","content":{"parts":[{"text":"hello"}]},"outputDimensionality":3}`,
		},
		{
			name:     "batch embed contents",
			action:   "batchEmbedContents",
			body:     `{"requests":[{"model":"models/public-embedding","content":{"parts":[{"text":"a"}]}},{"content":{"parts":[{"text":"b"}]}}],"fallbacks":["x"]}`,
			wantBody: `{"requests":[{"model":"models/upstream-embedding","content":{"parts":[{"text":"a"}]}},{"model":"models/upstream-embedding","content":{"parts":[{"text":"b"}]}}]}`,
		},
	}
}

func assertGeminiNativeEmbeddingsUpstream(
	t *testing.T,
	request *http.Request,
	wantPath string,
	wantBody string,
) {
	t.Helper()
	if request.Method != http.MethodPost || request.URL.Path != wantPath ||
		request.URL.Query().Get("trace") != "kept" || request.URL.Query().Get("key") != "" ||
		request.URL.Query().Get("api_key") != "" {
		t.Errorf("upstream target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
	}
	if request.Header.Get("X-Goog-Api-Key") != testAPIKey || request.Header.Get("Authorization") != "" {
		t.Errorf("credential headers = %q / %q", request.Header.Get("X-Goog-Api-Key"), request.Header.Get("Authorization"))
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Errorf("read upstream body: %v", err)
		return
	}
	var got, want any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Errorf("decode upstream body %s: %v", body, err)
		return
	}
	if err := json.Unmarshal([]byte(wantBody), &want); err != nil {
		t.Fatalf("decode expected body: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upstream body = %s, want %s", body, wantBody)
	}
}

func assertGeminiNativeEmbeddingsResult(t *testing.T, result execution.AttemptResult) {
	t.Helper()
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, error = %+v, validation = %v", result, result.Error, err)
	}
	if result.UpstreamProtocol != protocol.GeminiEmbeddings || result.Model != "upstream-embedding" {
		t.Fatalf("result protocol/model = %q/%q", result.UpstreamProtocol, result.Model)
	}
	if string(result.Body) != geminiNativeEmbeddingsResponse {
		t.Fatalf("result body = %s, want upstream bytes unchanged", result.Body)
	}
	assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 5})
}

func TestGeminiNativeEmbeddingsPassthroughOnGeminiChannel(t *testing.T) {
	t.Parallel()

	for _, test := range geminiNativeEmbeddingsCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				assertGeminiNativeEmbeddingsUpstream(t, request, "/v1beta/models/upstream-embedding:"+test.action, test.wantBody)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, geminiNativeEmbeddingsResponse)
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
			result := runtime.Execute(t.Context(), geminiNativeEmbeddingsSpec(channel.Gemini, test.action, test.body, json.RawMessage(`{}`)))
			assertGeminiNativeEmbeddingsResult(t, result)
			if calls.Load() != 1 {
				t.Fatalf("upstream calls = %d", calls.Load())
			}
		})
	}
}

func TestGeminiNativeEmbeddingsPassthroughOnMultiProtocolGateways(t *testing.T) {
	t.Parallel()

	for _, id := range []channel.ID{channel.NewAPI, channel.GPTLoad} {
		for _, test := range geminiNativeEmbeddingsCases() {
			t.Run(string(id)+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					assertGeminiNativeEmbeddingsUpstream(t, request, "/team-a/v1beta/models/upstream-embedding:"+test.action, test.wantBody)
					writer.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(writer, geminiNativeEmbeddingsResponse)
				}))
				defer server.Close()

				registry := channel.NewRegistry()
				resolved, err := registry.Resolve(id, json.RawMessage(`{"base_url":"`+server.URL+`/team-a"}`))
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
				defer manager.Shutdown()
				if err := manager.Reconcile([]provideradapter.RuntimeTarget{{Target: resolved}}); err != nil {
					t.Fatal(err)
				}
				result := manager.Execute(t.Context(), geminiNativeEmbeddingsSpec(id, test.action, test.body, resolved.TargetConfig))
				assertGeminiNativeEmbeddingsResult(t, result)
			})
		}
	}
}

func TestGeminiNativeEmbeddingsProbeUsesEmbedContent(t *testing.T) {
	t.Parallel()

	assertProbeRequest := func(t *testing.T, request *http.Request, wantPath string) {
		t.Helper()
		if request.Method != http.MethodPost || request.URL.Path != wantPath || request.URL.RawQuery != "" {
			t.Errorf("probe target = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("X-Goog-Api-Key") != testAPIKey || request.Header.Get("Authorization") != "" {
			t.Error("probe did not use the selected Gemini API key")
		}
		var payload struct {
			Content struct {
				Parts []map[string]string `json:"parts"`
			} `json:"content"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil ||
			len(payload.Content.Parts) != 1 || payload.Content.Parts[0]["text"] != "ping" {
			t.Errorf("probe payload = %+v, err = %v", payload, err)
		}
	}
	const probeResponse = `{"embedding":{"values":[0.1,0.2]}}`

	t.Run("gemini", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			assertProbeRequest(t, request, "/v1beta/models/probe-upstream:embedContent")
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, probeResponse)
		}))
		defer server.Close()
		runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
		spec := utilitySpec(channel.Gemini, protocol.GeminiEmbeddings, execution.OperationProbe, "", "", nil)
		spec.ClientModel, spec.UpstreamModel = "probe-client", "probe-upstream"
		result := runtime.Execute(t.Context(), freezeTestAttempt(spec))
		if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
			result.UpstreamProtocol != protocol.GeminiEmbeddings {
			t.Fatalf("probe result = %+v, error = %+v, validation = %v", result, result.Error, err)
		}
	})

	for _, id := range []channel.ID{channel.NewAPI, channel.GPTLoad} {
		t.Run(string(id), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				assertProbeRequest(t, request, "/team-a/v1beta/models/probe-upstream:embedContent")
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, probeResponse)
			}))
			defer server.Close()
			manager, spec := gatewayProbeForTest(t, id, protocol.GeminiEmbeddings, server.URL+"/team-a")
			result := manager.Execute(t.Context(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
				result.UpstreamProtocol != protocol.GeminiEmbeddings {
				t.Fatalf("probe result = %+v, error = %+v, validation = %v", result, result.Error, err)
			}
		})
	}
}

var invalidGeminiEmbeddingsProbeBodies = []string{
	`{}`, `null`, `[]`, `{"embedding":{}}`, `{"embedding":{"values":[]}}`,
	`{"embedding":{"values":["x"]}}`, `{"embedding":{"values":["0.1"]}}`,
	`{"embedding":{"values":[null]}}`, `{"embedding":{"values":[0.1,null]}}`,
	`{"error":{"message":"rejected"}}`,
}

func TestGeminiNativeEmbeddingsDirectProbeRejectsInvalidSuccessResponses(t *testing.T) {
	t.Parallel()

	for _, body := range invalidGeminiEmbeddingsProbeBodies {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, body)
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
			spec := utilitySpec(channel.Gemini, protocol.GeminiEmbeddings, execution.OperationProbe, "", "", nil)
			spec.ClientModel, spec.UpstreamModel = "probe-client", "probe-upstream"
			result := runtime.Execute(t.Context(), freezeTestAttempt(spec))
			if err := result.Validate(); err != nil || result.Error == nil {
				t.Fatalf("invalid probe response %s succeeded: %+v; validation = %v", body, result, err)
			}
		})
	}
}

func TestGeminiEmbeddingsProbeResponseValidation(t *testing.T) {
	t.Parallel()

	if !validGatewayProtocolProbeResponse(protocol.GeminiEmbeddings, []byte(`{"embedding":{"values":[0.1,-2,1e-7]}}`)) {
		t.Fatal("valid embedding rejected")
	}
	for _, body := range invalidGeminiEmbeddingsProbeBodies {
		if validGatewayProtocolProbeResponse(protocol.GeminiEmbeddings, []byte(body)) {
			t.Errorf("invalid embedding %s accepted", body)
		}
	}
}

func TestGeminiNativeEmbeddingsGatewayProbeRejectsInvalidSuccessResponses(t *testing.T) {
	t.Parallel()

	for _, body := range invalidGeminiEmbeddingsProbeBodies {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, body)
			}))
			defer server.Close()
			manager, spec := gatewayProbeForTest(t, channel.NewAPI, protocol.GeminiEmbeddings, server.URL)
			result := manager.Execute(t.Context(), spec)
			if err := result.Validate(); err != nil || result.Error == nil {
				t.Fatalf("invalid probe response %s succeeded: %+v; validation = %v", body, result, err)
			}
		})
	}
}

func TestGeminiNativeEmbeddingsRouteCapability(t *testing.T) {
	t.Parallel()

	manager := &RuntimeManager{}
	for _, operation := range []execution.Operation{execution.OperationEmbeddingsCreate, execution.OperationProbe} {
		route := channel.RouteDescriptor{
			ClientProtocol: protocol.GeminiEmbeddings,
			Operation:      operation,
			RouteMode:      execution.RouteNative,
		}
		for _, provider := range []channel.ProviderKind{channel.ProviderGemini, channel.ProviderMultiProtocolGateway} {
			if err := manager.ValidateRouteCapability(provider, route); err != nil {
				t.Errorf("%s %s: %v", provider, operation, err)
			}
		}
		for _, provider := range []channel.ProviderKind{
			channel.ProviderOpenAI, channel.ProviderOpenAICompatible, channel.ProviderGoogleVertex, channel.ProviderOpenRouter,
		} {
			if err := manager.ValidateRouteCapability(provider, route); err == nil {
				t.Errorf("unexpected gemini-embeddings %s capability for %s", operation, provider)
			}
		}
	}
}

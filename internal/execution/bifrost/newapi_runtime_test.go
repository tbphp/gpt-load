package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/protocol"
	"gpt-load/internal/provideradapter"
)

func TestNewAPINativeUnaryProtocolsUseCanonicalRootPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		clientProtocol   protocol.Protocol
		operation        execution.Operation
		path             string
		body             string
		wantPath         string
		credentialHeader string
		response         string
	}{
		{
			name: "completions", clientProtocol: protocol.OpenAICompletions,
			operation: execution.OperationChatCompletion, path: "/v1/chat/completions",
			body:     `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1/chat/completions", credentialHeader: "Authorization",
			response: `{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "responses", clientProtocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesCreate, path: "/v1/responses",
			body:     `{"model":"client-model","input":"hello","store":false,"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1/responses", credentialHeader: "Authorization",
			response: `{"id":"resp_1","object":"response","status":"completed","model":"upstream-model","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "responses compact", clientProtocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesCompact, path: "/v1/responses/compact",
			body:     `{"model":"client-model","input":[{"role":"user","content":"hello"}],"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1/responses/compact", credentialHeader: "Authorization",
			response: `{"id":"cmp_1","object":"response.compaction","model":"upstream-model","output":[]}`,
		},
		{
			name: "images", clientProtocol: protocol.OpenAIImages,
			operation: execution.OperationImagesGenerate, path: "/v1/images/generations",
			body:     `{"model":"client-model","prompt":"hello","provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1/images/generations", credentialHeader: "Authorization",
			response: `{"created":1,"data":[{"url":"https://images.example/result.png"}]}`,
		},
		{
			name: "embeddings", clientProtocol: protocol.OpenAIEmbeddings,
			operation: execution.OperationEmbeddingsCreate, path: "/v1/embeddings",
			body:     `{"model":"client-model","input":"hello","provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1/embeddings", credentialHeader: "Authorization",
			response: `{"object":"list","model":"upstream-model","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`,
		},
		{
			name: "anthropic", clientProtocol: protocol.Anthropic,
			operation: execution.OperationChatCompletion, path: "/v1/messages",
			body:     `{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1/messages", credentialHeader: "X-Api-Key",
			response: `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			name: "gemini", clientProtocol: protocol.Gemini,
			operation: execution.OperationChatCompletion, path: "/v1beta/models/client-model:generateContent",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1beta/models/upstream-model:generateContent", credentialHeader: "X-Goog-Api-Key",
			response: `{"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]} ,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"upstream-model"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != test.wantPath ||
					request.URL.Query().Get("trace") != "kept" || request.URL.Query().Get("key") != "" ||
					request.URL.Query().Get("api_key") != "" {
					t.Errorf("upstream target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
				}
				wantCredential := testAPIKey
				if test.credentialHeader == "Authorization" {
					wantCredential = "Bearer " + testAPIKey
				}
				if request.Header.Get(test.credentialHeader) != wantCredential {
					t.Errorf("credential %s = %q", test.credentialHeader, request.Header.Get(test.credentialHeader))
				}
				for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
					if name != test.credentialHeader && request.Header.Get(name) != "" {
						t.Errorf("unexpected credential header %s = %q", name, request.Header.Get(name))
					}
				}
				if test.clientProtocol == protocol.Anthropic && request.Header.Get("Anthropic-Version") == "" {
					t.Error("Anthropic-Version is missing")
				}
				body, _ := io.ReadAll(request.Body)
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("decode upstream request: %v", err)
				}
				if payload["provider"] != nil || payload["api_key"] != nil {
					t.Errorf("control fields reached upstream: %#v", payload)
				}
				if test.clientProtocol != protocol.Gemini && payload["model"] != "upstream-model" {
					t.Errorf("upstream model = %#v", payload["model"])
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "newapi-"+test.name)
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			registry := channel.NewRegistry()
			resolved, err := registry.Resolve(
				channel.NewAPI,
				json.RawMessage(`{"base_url":"`+server.URL+`/team-a"}`),
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
			defer manager.Shutdown()
			if err := manager.Reconcile([]provideradapter.RuntimeTarget{{Target: resolved}}); err != nil {
				t.Fatal(err)
			}

			spec := freezeTestAttempt(execution.AttemptSpec{
				RequestID: "newapi-request", AttemptID: "newapi-attempt", Sequence: 1,
				ChannelID: string(channel.NewAPI), ClientProtocol: test.clientProtocol,
				Operation: test.operation, ClientModel: "client-model", UpstreamModel: "upstream-model",
				Method: http.MethodPost, Path: test.path,
				RawQuery: "trace=kept&key=client-query&api_key=client-query",
				Header: http.Header{
					"Authorization":  {"Bearer client-auth"},
					"X-Api-Key":      {"client-auth"},
					"X-Goog-Api-Key": {"client-auth"},
					"Content-Type":   {"application/json"},
				},
				Body: []byte(test.body), TargetConfig: resolved.TargetConfig,
				Credential: execution.NewCredentialSnapshot(
					17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`),
				),
			})
			if err := spec.Validate(); err != nil {
				t.Fatal(err)
			}
			result := manager.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if result.Error != nil || result.StatusCode != http.StatusOK ||
				result.UpstreamProtocol != test.clientProtocol || result.UpstreamRequestID != "newapi-"+test.name {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestNewAPIImageEditUsesGatewayRoot(t *testing.T) {
	t.Parallel()

	imageBytes := []byte{0x00, 0xff, 0x10, 0x20, '\r', '\n'}
	body, contentType := imagesMultipartBody(t, imageBytes)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/team-a/v1/images/edits" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
			t.Errorf("Content-Type = %q: %v", request.Header.Get("Content-Type"), err)
			return
		}
		reader := multipart.NewReader(request.Body, parameters["boundary"])
		seen := make(map[string]bool)
		for {
			part, nextErr := reader.NextRawPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				t.Errorf("NextRawPart() error = %v", nextErr)
				return
			}
			_, disposition, parseErr := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if parseErr != nil {
				t.Errorf("Content-Disposition = %q: %v", part.Header.Get("Content-Disposition"), parseErr)
				return
			}
			name := disposition["name"]
			seen[name] = true
			data, readErr := io.ReadAll(part)
			if readErr != nil {
				t.Errorf("ReadAll(%q) error = %v", name, readErr)
				return
			}
			switch name {
			case "model":
				if string(data) != "provider-image" {
					t.Errorf("model = %q", data)
				}
			case "image[]":
				if !bytes.Equal(data, imageBytes) {
					t.Errorf("image data = %x", data)
				}
			case "api_key", "provider", "fallbacks":
				t.Errorf("control part %q reached upstream", name)
			}
		}
		for _, name := range []string{"prompt", "model", "stream", "image[]", "future"} {
			if !seen[name] {
				t.Errorf("part %q is missing", name)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"created":1,"data":[{"b64_json":"b2s="}]}`)
	}))
	defer server.Close()

	manager, _ := newNewAPIManagerForTest(t, server.URL+"/team-a")
	spec := openAIImagesSpec(
		channel.NewAPI,
		server.URL+"/team-a",
		execution.OperationImagesEdit,
		"/v1/images/edits",
		contentType,
		body,
	)
	result := manager.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, validation=%v", result, err)
	}
}

func TestNewAPIGeminiAcceptsOpenAIStyleErrorEnvelope(t *testing.T) {
	t.Parallel()

	const responseBody = `{"error":{"type":"rate_limit_error","message":"retry later"}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/team-a/v1beta/models/upstream-model:generateContent" {
			t.Errorf("path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer server.Close()

	manager, resolved := newNewAPIManagerForTest(t, server.URL+"/team-a")
	spec := freezeTestAttempt(execution.AttemptSpec{
		RequestID: "newapi-gemini-error", AttemptID: "newapi-gemini-error-attempt", Sequence: 1,
		ChannelID: string(channel.NewAPI), ClientProtocol: protocol.Gemini,
		Operation:   execution.OperationChatCompletion,
		ClientModel: "client-model", UpstreamModel: "upstream-model",
		Method: http.MethodPost, Path: "/v1beta/models/client-model:generateContent",
		Header:       http.Header{"Content-Type": {"application/json"}},
		Body:         []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
		TargetConfig: resolved.TargetConfig,
		Credential: execution.NewCredentialSnapshot(
			17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`),
		),
	})
	result := manager.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error == nil ||
		result.Error.Kind != execution.ErrorKindHTTP || result.StatusCode != http.StatusTooManyRequests ||
		result.UpstreamProtocol != protocol.Gemini || !bytes.Equal(result.Body, []byte(responseBody)) {
		t.Fatalf("result = %+v, validation=%v, body=%s", result, err, result.Body)
	}
}

func TestResolveMultiProtocolGatewayTargetURLPreservesEncodedDeploymentPrefix(t *testing.T) {
	t.Parallel()

	got, err := resolveMultiProtocolGatewayTargetURL(
		"https://relay.example/team%2Fone/",
		"/v1/models",
		"cursor=next%2Fpage",
	)
	if err != nil {
		t.Fatal(err)
	}
	const want = "https://relay.example/team%2Fone/v1/models?cursor=next%2Fpage"
	if got != want {
		t.Fatalf("target URL = %q, want %q", got, want)
	}
}

func TestNewAPIUtilityOperationsUseGatewayRoot(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/team-a/v1/models":
			_, _ = io.WriteString(writer, `{"success":true,"object":"list","data":[{"id":"model-one"}]}`)
		case "/team-a/v1/chat/completions":
			_, _ = io.WriteString(writer, `{"id":"chat_1","object":"chat.completion","created":1,"model":"probe-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`)
		case "/team-a/v1/embeddings":
			_, _ = io.WriteString(writer, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}],"model":"probe-upstream","usage":{"prompt_tokens":1,"total_tokens":1}}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	manager, resolved := newNewAPIManagerForTest(t, server.URL+"/team-a")
	models := utilitySpec(channel.NewAPI, protocol.OpenAICompletions, execution.OperationListModels, http.MethodGet, "/v1/models", nil)
	models.TargetConfig = resolved.TargetConfig
	models = freezeTestAttempt(models)
	modelsResult := manager.Execute(context.Background(), models)
	if err := modelsResult.Validate(); err != nil || modelsResult.Error != nil ||
		!bytes.Contains(modelsResult.Body, []byte(`"id":"model-one"`)) {
		t.Fatalf("models result = %+v err=%v body=%s", modelsResult, err, modelsResult.Body)
	}

	chatProbe := utilitySpec(channel.NewAPI, protocol.OpenAICompletions, execution.OperationProbe, "", "", nil)
	chatProbe.TargetConfig = resolved.TargetConfig
	chatProbe.ClientModel, chatProbe.UpstreamModel = "probe-client", "probe-upstream"
	chatProbe = freezeTestAttempt(chatProbe)
	chatResult := manager.Execute(context.Background(), chatProbe)
	if err := chatResult.Validate(); err != nil || chatResult.Error != nil {
		t.Fatalf("chat probe = %+v err=%v", chatResult, err)
	}

	embeddingProbe := utilitySpec(channel.NewAPI, protocol.OpenAIEmbeddings, execution.OperationProbe, "", "", nil)
	embeddingProbe.TargetConfig = resolved.TargetConfig
	embeddingProbe.ClientModel, embeddingProbe.UpstreamModel = "probe-client", "probe-upstream"
	embeddingProbe = freezeTestAttempt(embeddingProbe)
	embeddingResult := manager.Execute(context.Background(), embeddingProbe)
	if err := embeddingResult.Validate(); err != nil || embeddingResult.Error != nil {
		t.Fatalf("embedding probe = %+v err=%v", embeddingResult, err)
	}

	wantPaths := []string{
		"/team-a/v1/models",
		"/team-a/v1/chat/completions",
		"/team-a/v1/embeddings",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("utility paths = %#v, want %#v", paths, wantPaths)
	}
}

func TestNewAPIResponsesResourceSemanticsFailBeforeDispatch(t *testing.T) {
	t.Parallel()

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	manager, resolved := newNewAPIManagerForTest(t, server.URL+"/team-a")
	spec := freezeTestAttempt(execution.AttemptSpec{
		RequestID: "responses-resource", AttemptID: "responses-resource-attempt", Sequence: 1,
		ChannelID: string(channel.NewAPI), ClientProtocol: protocol.OpenAIResponses,
		Operation: execution.OperationResponsesCreate, RouteRequirement: execution.RouteRequirementNative,
		ClientModel: "client-model", UpstreamModel: "upstream-model",
		Method: http.MethodPost, Path: "/v1/responses",
		Header:       http.Header{"Content-Type": {"application/json"}},
		Body:         []byte(`{"model":"client-model","input":"hello"}`),
		TargetConfig: resolved.TargetConfig,
		Credential: execution.NewCredentialSnapshot(
			17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`),
		),
	})
	result := manager.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.DispatchState != execution.DispatchNotSent || result.Error == nil {
		t.Fatalf("resource result = %+v err=%v", result, err)
	}
	if calls != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls)
	}
}

func TestNewAPINativeStreamsKeepProtocolWireAndUsage(t *testing.T) {
	t.Parallel()

	const openAIStream = "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"stream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"stream-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"
	tests := []struct {
		name             string
		clientProtocol   protocol.Protocol
		path             string
		body             string
		wantPath         string
		credentialHeader string
		stream           string
	}{
		{
			name: "completions", clientProtocol: protocol.OpenAICompletions,
			path: "/v1/chat/completions", wantPath: "/team-a/v1/chat/completions",
			body:             `{"model":"stream-model","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			credentialHeader: "Authorization", stream: openAIStream,
		},
		{
			name: "anthropic", clientProtocol: protocol.Anthropic,
			path: "/v1/messages", wantPath: "/team-a/v1/messages",
			body:             `{"model":"stream-model","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`,
			credentialHeader: "X-Api-Key", stream: anthropicResponsesStreamFixture,
		},
		{
			name: "gemini", clientProtocol: protocol.Gemini,
			path:             "/v1beta/models/stream-model:streamGenerateContent",
			wantPath:         "/team-a/v1beta/models/stream-model:streamGenerateContent",
			body:             `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			credentialHeader: "X-Goog-Api-Key", stream: geminiResponsesStreamFixture,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.wantPath || request.URL.Query().Get("trace") != "kept" ||
					request.URL.Query().Get("key") != "" {
					t.Errorf("stream target = %s?%s", request.URL.Path, request.URL.RawQuery)
				}
				if test.clientProtocol == protocol.Gemini && request.URL.Query().Get("alt") != "sse" {
					t.Errorf("Gemini alt = %q", request.URL.Query().Get("alt"))
				}
				wantCredential := testAPIKey
				if test.credentialHeader == "Authorization" {
					wantCredential = "Bearer " + testAPIKey
				}
				if request.Header.Get(test.credentialHeader) != wantCredential {
					t.Errorf("credential %s = %q", test.credentialHeader, request.Header.Get(test.credentialHeader))
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, test.stream)
				writer.(http.Flusher).Flush()
			}))
			defer server.Close()

			manager, resolved := newNewAPIManagerForTest(t, server.URL+"/team-a")
			spec := freezeTestAttempt(execution.AttemptSpec{
				RequestID: "newapi-stream", AttemptID: "newapi-stream-attempt", Sequence: 1,
				ChannelID: string(channel.NewAPI), ClientProtocol: test.clientProtocol,
				Operation:   execution.OperationChatCompletion,
				ClientModel: "stream-model", UpstreamModel: "stream-model",
				Method: http.MethodPost, Path: test.path,
				RawQuery: "trace=kept&key=client-query&alt=client",
				Header: http.Header{
					"Authorization":  {"Bearer client-auth"},
					"X-Api-Key":      {"client-auth"},
					"X-Goog-Api-Key": {"client-auth"},
					"Content-Type":   {"application/json"},
				},
				Body: []byte(test.body), TargetConfig: resolved.TargetConfig,
				Credential: execution.NewCredentialSnapshot(
					17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`),
				),
			})
			var data bytes.Buffer
			result := manager.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					data.Write(event.Data)
				}
				return nil
			})
			if err := result.Validate(); err != nil || result.Error != nil || result.Usage == nil ||
				result.UpstreamProtocol != test.clientProtocol || data.String() != test.stream {
				t.Fatalf("stream result = %+v err=%v data=%q", result, err, data.String())
			}
		})
	}
}

func newNewAPIManagerForTest(t *testing.T, baseURL string) (*RuntimeManager, channel.ResolvedTarget) {
	t.Helper()

	registry := channel.NewRegistry()
	resolved, err := registry.Resolve(
		channel.NewAPI,
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

func TestNewAPIRuntimeProfilesUseOneGatewayRoot(t *testing.T) {
	t.Parallel()

	registry := channel.NewRegistry()
	resolved, err := registry.Resolve(
		channel.NewAPI,
		json.RawMessage(`{"base_url":"https://relay.example/team-a"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		protocol     protocol.Protocol
		operation    execution.Operation
		wantProvider schemas.ModelProvider
	}{
		{name: "completions", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion, wantProvider: schemas.OpenAI},
		{name: "responses", protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate, wantProvider: schemas.OpenAI},
		{name: "images", protocol: protocol.OpenAIImages, operation: execution.OperationImagesGenerate, wantProvider: schemas.OpenAI},
		{name: "embeddings", protocol: protocol.OpenAIEmbeddings, operation: execution.OperationEmbeddingsCreate, wantProvider: schemas.OpenAI},
		{name: "anthropic", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion, wantProvider: schemas.Anthropic},
		{name: "gemini", protocol: protocol.Gemini, operation: execution.OperationChatCompletion, wantProvider: schemas.Gemini},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := buildEffectiveProviderConfigForAttempt(
				resolved,
				execution.AttemptSpec{
					ClientProtocol: test.protocol,
					Operation:      test.operation,
					RouteMode:      execution.RouteNative,
				},
				true,
			)
			if err != nil {
				t.Fatalf("buildEffectiveProviderConfigForAttempt() error = %v", err)
			}
			if config.provider != test.wantProvider || config.custom ||
				config.targetBaseURL != "https://relay.example/team-a" ||
				config.providerConfig.NetworkConfig.BaseURL != "https://relay.example/team-a" {
				t.Fatalf("New API config = provider %q custom %t target %q network %q", config.provider, config.custom, config.targetBaseURL, config.providerConfig.NetworkConfig.BaseURL)
			}
		})
	}
}

func TestNewAPIReconcileKeepsEveryRuntimeProfileActive(t *testing.T) {
	t.Parallel()

	registry := channel.NewRegistry()
	resolved, err := registry.Resolve(
		channel.NewAPI,
		json.RawMessage(`{"base_url":"https://relay.example/team-a"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile([]provideradapter.RuntimeTarget{{Target: resolved}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	expected := make([]effectiveProviderConfig, 0, 3)
	for _, clientProtocol := range []protocol.Protocol{
		protocol.OpenAICompletions,
		protocol.Anthropic,
		protocol.Gemini,
	} {
		config, err := buildEffectiveProviderConfigForAttempt(
			resolved,
			execution.AttemptSpec{ClientProtocol: clientProtocol, RouteMode: execution.RouteNative},
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		expected = append(expected, config)
	}

	assertActive := func(want []effectiveProviderConfig) {
		t.Helper()
		manager.pool.mu.Lock()
		defer manager.pool.mu.Unlock()
		if len(manager.pool.active) != len(want) {
			t.Fatalf("active runtime configs = %d, want %d", len(manager.pool.active), len(want))
		}
		for _, config := range want {
			active, ok := manager.pool.active[config.fingerprint]
			if !ok || !bytes.Equal(active, config.canonical) {
				t.Errorf("profile %q is not active", config.provider)
			}
		}
	}
	assertActive(expected)

	effectiveProxy := outboundproxy.Effective{
		Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "http://proxy.example.com:8080"},
		Source: outboundproxy.SourceGroup,
	}
	if err := manager.Reconcile([]provideradapter.RuntimeTarget{{
		Target: resolved,
		Proxy:  effectiveProxy,
	}}); err != nil {
		t.Fatalf("Reconcile(proxy) error = %v", err)
	}
	expectedWithProxy := append([]effectiveProviderConfig(nil), expected...)
	for _, clientProtocol := range []protocol.Protocol{
		protocol.OpenAICompletions,
		protocol.Anthropic,
		protocol.Gemini,
	} {
		config, err := buildEffectiveProviderConfigForAttempt(
			resolved,
			execution.AttemptSpec{
				ClientProtocol: clientProtocol,
				RouteMode:      execution.RouteNative,
				Proxy:          effectiveProxy,
			},
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		expectedWithProxy = append(expectedWithProxy, config)
	}
	assertActive(expectedWithProxy)
}

func TestNewAPIRouteCapabilityMatchesDeclaredOperations(t *testing.T) {
	t.Parallel()

	registry := channel.NewRegistry()
	manager, err := newRuntimeManager(runtimeOptions{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := registry.Get(channel.NewAPI)
	if !ok {
		t.Fatal("New API channel is missing")
	}
	for _, route := range descriptor.Routes {
		if err := manager.ValidateRouteCapability(channel.ProviderMultiProtocolGateway, route); err != nil {
			t.Errorf("route %s/%s/%s rejected: %v", route.ClientProtocol, route.Operation, route.RouteMode, err)
		}
	}
	for _, unsupported := range []channel.RouteDescriptor{
		{ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationProbe, RouteMode: execution.RouteNative},
	} {
		if err := manager.ValidateRouteCapability(channel.ProviderMultiProtocolGateway, unsupported); err == nil {
			t.Errorf("unsupported route accepted: %#v", unsupported)
		}
	}
}

package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestAnthropicAndGeminiCustomBaseURLUseNativeUnaryStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		channelID   channel.ID
		prefix      string
		unaryPath   string
		streamPath  string
		unaryBody   string
		streamBody  string
		unarySpec   func() execution.AttemptSpec
		streamSpec  func() execution.AttemptSpec
		credential  string
		streamQuery string
	}{
		{
			name: "Anthropic", channelID: channel.Anthropic, prefix: "/tenant",
			unaryPath: "/tenant/v1/messages", streamPath: "/tenant/v1/messages", credential: "X-Api-Key",
			unaryBody: anthropicResponsesConvertedFixture, streamBody: anthropicResponsesStreamFixture,
			unarySpec:  func() execution.AttemptSpec { return anthropicSpec(false) },
			streamSpec: func() execution.AttemptSpec { return anthropicSpec(true) },
		},
		{
			name: "Gemini", channelID: channel.Gemini, prefix: "/tenant/v1beta",
			unaryPath:  "/tenant/v1beta/models/upstream-gemini:generateContent",
			streamPath: "/tenant/v1beta/models/upstream-gemini:streamGenerateContent", credential: "X-Goog-Api-Key",
			unaryBody: geminiResponsesConvertedFixture, streamBody: geminiResponsesStreamFixture, streamQuery: "sse",
			unarySpec:  func() execution.AttemptSpec { return geminiSpec(false) },
			streamSpec: func() execution.AttemptSpec { return geminiSpec(true) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get(test.credential) != testAPIKey || request.Header.Get("Authorization") != "" {
					t.Errorf("credential headers = %#v", request.Header)
				}
				body, _ := io.ReadAll(request.Body)
				var payload map[string]any
				_ = json.Unmarshal(body, &payload)
				isStream := request.URL.Path == test.streamPath && (test.streamPath != test.unaryPath || payload["stream"] == true)
				if isStream {
					if test.streamQuery != "" && request.URL.Query().Get("alt") != test.streamQuery {
						t.Errorf("stream query = %q", request.URL.RawQuery)
					}
					writer.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(writer, test.streamBody)
					return
				}
				if request.URL.Path != test.unaryPath {
					t.Errorf("unary path = %q", request.URL.Path)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.unaryBody)
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			target, _ := json.Marshal(map[string]string{"base_url": server.URL + test.prefix})
			unarySpec := test.unarySpec()
			unarySpec.ChannelID = string(test.channelID)
			unarySpec.TargetConfig = target
			unarySpec = freezeTestAttempt(unarySpec)
			unarySpec.ClientModel = unarySpec.UpstreamModel
			unary := runtime.Execute(context.Background(), unarySpec)
			if err := unary.Validate(); err != nil || unary.Error != nil || string(unary.Body) != test.unaryBody || unary.Usage == nil {
				t.Fatalf("unary = %+v err=%v body=%s", unary, err, unary.Body)
			}

			streamSpec := test.streamSpec()
			streamSpec.ChannelID = string(test.channelID)
			streamSpec.TargetConfig = target
			streamSpec = freezeTestAttempt(streamSpec)
			streamSpec.ClientModel = streamSpec.UpstreamModel
			if test.streamQuery != "" {
				streamSpec.Query.Set("alt", test.streamQuery)
			}
			var data bytes.Buffer
			stream := runtime.ExecuteStream(context.Background(), streamSpec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					data.Write(event.Data)
				}
				return nil
			})
			if err := stream.Validate(); err != nil || stream.Error != nil || data.String() != test.streamBody || stream.Usage == nil {
				t.Fatalf("stream = %+v err=%v data=%s", stream, err, data.String())
			}
		})
	}
}

func TestCompatibleOpenAIResponsesCreateConvertsToChatAndRejectsLifecycle(t *testing.T) {
	t.Parallel()

	const unaryResponse = `{"id":"chat_1","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
	const streamResponse = "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"served\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"served\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/tenant/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("request = %s %#v", request.URL.Path, request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if payload["stream"] == true {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, streamResponse)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, unaryResponse)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	target, _ := json.Marshal(map[string]string{"base_url": server.URL + "/tenant/v1"})
	create := openAIResponsesSpec(execution.OperationResponsesCreate, http.MethodPost, "/v1/responses")
	create.ChannelID = string(channel.OpenAICompatible)
	create.TargetConfig = target
	create = freezeTestAttempt(create)
	create.ClientModel = create.UpstreamModel
	unary := runtime.Execute(context.Background(), create)
	if err := unary.Validate(); err != nil || unary.Error != nil || !bytes.Contains(unary.Body, []byte(`"object":"response"`)) {
		t.Fatalf("unary = %+v err=%v body=%s", unary, err, unary.Body)
	}
	var data bytes.Buffer
	stream := runtime.ExecuteStream(context.Background(), create, func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventData {
			data.Write(event.Data)
		}
		return nil
	})
	if err := stream.Validate(); err != nil || stream.Error != nil || !strings.Contains(data.String(), "response.completed") {
		t.Fatalf("stream = %+v err=%v data=%s", stream, err, data.String())
	}
	retrieve := openAIResponsesSpec(execution.OperationResponsesRetrieve, http.MethodGet, "/v1/responses/resp_1")
	retrieve.ChannelID = string(channel.OpenAICompatible)
	retrieve.TargetConfig = target
	retrieve.ClientModel, retrieve.UpstreamModel, retrieve.Body = "", "", nil
	lifecycle := runtime.Execute(context.Background(), retrieve)
	if err := lifecycle.Validate(); err != nil || lifecycle.DispatchState != execution.DispatchNotSent || lifecycle.Error == nil {
		t.Fatalf("lifecycle = %+v err=%v", lifecycle, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want unary and stream only", calls.Load())
	}
}

func TestCompatibleListModelsAndProbeUseConfiguredPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		channelID  channel.ID
		protocol   protocol.Protocol
		baseSuffix string
		modelsPath string
		probePath  string
		probeBody  []byte
	}{
		{name: "OpenAI", channelID: channel.OpenAICompatible, protocol: protocol.OpenAICompletions, baseSuffix: "/custom/v1", modelsPath: "/custom/v1/models", probePath: "/custom/v1/chat/completions", probeBody: []byte(`{"model":"client","messages":[{"role":"user","content":"ping"}]}`)},
		{name: "Anthropic", channelID: channel.Anthropic, protocol: protocol.Anthropic, baseSuffix: "/custom", modelsPath: "/custom/v1/models", probePath: "/custom/v1/messages", probeBody: []byte(`{"model":"client","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)},
		{name: "Gemini", channelID: channel.Gemini, protocol: protocol.Gemini, baseSuffix: "/custom/v1beta", modelsPath: "/custom/v1beta/models", probePath: "/custom/v1beta/models/probe-upstream:generateContent", probeBody: []byte(`{"contents":[{"parts":[{"text":"ping"}]}]}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case test.modelsPath:
					if request.Method != http.MethodGet || request.URL.Query().Get("page") != "next" {
						t.Errorf("models target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
					}
					writer.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(writer, `{"data":[]}`)
				case test.probePath:
					if request.Method != http.MethodPost {
						t.Errorf("probe method = %s", request.Method)
					}
					writer.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(writer, `{"ok":true}`)
				default:
					t.Errorf("unexpected path = %s", request.URL.Path)
					writer.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			target, _ := json.Marshal(map[string]string{"base_url": server.URL + test.baseSuffix})
			modelsPath := "/v1/models"
			if test.protocol == protocol.Gemini {
				modelsPath = "/v1beta/models"
			}
			models := utilitySpec(test.channelID, test.protocol, execution.OperationListModels, http.MethodGet, modelsPath, nil)
			models.TargetConfig = target
			models = freezeTestAttempt(models)
			models.Query.Set("page", "next")
			modelsResult := runtime.Execute(context.Background(), models)
			if err := modelsResult.Validate(); err != nil || modelsResult.Error != nil {
				t.Fatalf("models = %+v err=%v", modelsResult, err)
			}

			probeClientPath := "/v1/chat/completions"
			if test.protocol == protocol.Anthropic {
				probeClientPath = "/v1/messages"
			}
			if test.protocol == protocol.Gemini {
				probeClientPath = "/v1beta/models/client:generateContent"
			}
			probe := utilitySpec(test.channelID, test.protocol, execution.OperationProbe, http.MethodPost, probeClientPath, test.probeBody)
			probe.TargetConfig = target
			probe = freezeTestAttempt(probe)
			probe.ClientModel, probe.UpstreamModel = "probe-client", "probe-upstream"
			probeResult := runtime.Execute(context.Background(), probe)
			if err := probeResult.Validate(); err != nil || probeResult.Error != nil {
				t.Fatalf("probe = %+v err=%v", probeResult, err)
			}
		})
	}
}

func TestOpenAICompatibleNonV1PrefixKeepsListModelsAndProbeFunctional(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/vendor/api/v4/models":
			_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"model-1","object":"model","created":1,"owned_by":"vendor"}]}`)
		case "/vendor/api/v4/chat/completions":
			_, _ = io.WriteString(writer, `{"id":"chat_1","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		default:
			t.Errorf("path = %s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	target, _ := json.Marshal(map[string]string{"base_url": server.URL + "/vendor/api/v4"})
	models := utilitySpec(channel.OpenAICompatible, protocol.OpenAICompletions, execution.OperationListModels, http.MethodGet, "/v1/models", nil)
	models.TargetConfig = target
	models = freezeTestAttempt(models)
	modelsResult := runtime.Execute(context.Background(), models)
	if err := modelsResult.Validate(); err != nil || modelsResult.Error != nil || !bytes.Contains(modelsResult.Body, []byte(`"model-1"`)) {
		t.Fatalf("models = %+v err=%v body=%s", modelsResult, err, modelsResult.Body)
	}

	probe := utilitySpec(channel.OpenAICompatible, protocol.OpenAICompletions, execution.OperationProbe, http.MethodPost, "/v1/chat/completions", []byte(`{"model":"client","messages":[{"role":"user","content":"ping"}]}`))
	probe.TargetConfig = target
	probe = freezeTestAttempt(probe)
	probe.ClientModel, probe.UpstreamModel = "probe-client", "probe-upstream"
	probeResult := runtime.Execute(context.Background(), probe)
	if err := probeResult.Validate(); err != nil || probeResult.Error != nil || !bytes.Contains(probeResult.Body, []byte(`"chat_1"`)) {
		t.Fatalf("probe = %+v err=%v body=%s", probeResult, err, probeResult.Body)
	}
}

func TestOpenAICompatibleConvertedResponsesUsesChatCompletions(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/vendor/api/v4/chat/completions" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chat_1","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	target, _ := json.Marshal(map[string]string{"base_url": server.URL + "/vendor/api/v4"})
	spec := openAIResponsesSpec(execution.OperationResponsesCreate, http.MethodPost, "/v1/responses")
	spec.ChannelID = string(channel.OpenAICompatible)
	spec.TargetConfig = target
	spec = freezeTestAttempt(spec)
	spec.ClientModel = "client-model"
	spec.UpstreamModel = "upstream-model"
	spec.Body = []byte(`{"model":"client-model","input":"hello","store":false}`)
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil {
		t.Fatalf("result = %+v err=%v body=%s", result, err, result.Body)
	}
	if !bytes.Contains(result.Body, []byte(`"object":"response"`)) {
		t.Fatalf("converted Responses body = %s", result.Body)
	}
}

func TestListModelsConvertsProviderResultToClientProtocol(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" {
			t.Errorf("path = %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"model-1","object":"model","created":1,"owned_by":"vendor"}]}`)
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: server.URL})
	spec := utilitySpec(channel.OpenAI, protocol.Anthropic, execution.OperationListModels, http.MethodGet, "/v1/models", nil)
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	var body map[string]any
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["has_more"]; !exists {
		t.Fatalf("Anthropic model list shape missing: %s", result.Body)
	}
	if strings.Contains(string(result.Body), "gptload-custom-") || strings.Contains(string(result.Body), "openai/model-1") {
		t.Fatalf("internal provider leaked: %s", result.Body)
	}
}

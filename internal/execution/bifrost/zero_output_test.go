package bifrost

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestRuntimeRejectsAnthropicZeroOutputBeforeTypedConversion(t *testing.T) {
	t.Parallel()

	for _, channelID := range []channel.ID{channel.Gemini, channel.OpenAI, channel.OpenAICompatible, channel.DeepSeek} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", channelID, stream), func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					calls.Add(1)
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(writer, `{"error":{"message":"unexpected generation request"}}`)
				}))
				defer server.Close()
				runtime := newProtocolTestRuntime(t, testRuntimeOptions{
					allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta", openAIBaseURL: server.URL,
				})
				runtime.baseURLs[channel.OpenAICompatible] = server.URL
				runtime.baseURLs[channel.DeepSeek] = server.URL
				spec := convertedSpec(channelID, protocol.Anthropic, execution.OperationChatCompletion,
					"/v1/messages", []byte(`{"model":"client-model","max_tokens":0,"messages":[{"role":"user","content":"hello"}]}`))
				var evidence *execution.ErrorEvidence
				if stream {
					events := 0
					result := runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error {
						events++
						return nil
					})
					if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted || events != 0 {
						t.Errorf("stream result = %+v, events = %d", result, events)
					}
					evidence = result.Error
				} else {
					result := runtime.Execute(t.Context(), spec)
					if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted {
						t.Errorf("result = %+v", result)
					}
					evidence = result.Error
				}
				if evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported ||
					evidence.Code != execution.ErrorCodeCriticalSemanticLoss ||
					evidence.ScopeHint != execution.ErrorScopeGroup || evidence.OriginHint != execution.ErrorOriginInternal {
					t.Errorf("error = %+v", evidence)
				}
				if calls.Load() != 0 {
					t.Errorf("upstream calls = %d, want 0", calls.Load())
				}
			})
		}
	}
}

func TestNativeAnthropicZeroOutputPreservesLimitAndResponse(t *testing.T) {
	t.Parallel()

	const responseBody = `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[],"stop_reason":"max_tokens","stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":0}}`
	for _, channelID := range []channel.ID{channel.Anthropic, channel.NewAPI} {
		t.Run(string(channelID), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				var body map[string]json.RawMessage
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(body["max_tokens"]) != "0" || request.URL.Path != "/v1/messages" {
					t.Errorf("native path/limit = %s/%s", request.URL.Path, body["max_tokens"])
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, responseBody)
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL})
			runtime.baseURLs[channel.NewAPI] = server.URL
			spec := convertedSpec(channelID, protocol.Anthropic, execution.OperationChatCompletion,
				"/v1/messages", []byte(`{"model":"client-model","max_tokens":0,"messages":[{"role":"user","content":"hello"}]}`))
			spec.ClientModel = spec.UpstreamModel
			result := runtime.Execute(t.Context(), spec)
			if result.Error != nil || result.StatusCode != http.StatusOK || calls.Load() != 1 || string(result.Body) != responseBody {
				t.Fatalf("result = %+v, calls = %d, body = %s", result, calls.Load(), result.Body)
			}
		})
	}
}

func TestCountTokensDoesNotApplyZeroOutputGuard(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1beta/models/upstream-model:countTokens" {
			t.Errorf("path = %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"totalTokens":7}`)
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
	spec := convertedSpec(channel.Gemini, protocol.Anthropic, execution.OperationCountTokens,
		"/v1/messages/count_tokens", []byte(`{"model":"client-model","max_tokens":0,"messages":[{"role":"user","content":"hello"}]}`))
	result := runtime.Execute(t.Context(), spec)
	if result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("count tokens result = %+v", result)
	}
}

package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenAIResponsesUnknownNamespaceUsesExplicitNativePassthrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		channelID  channel.ID
		method     string
		path       string
		body       []byte
		status     int
		response   string
		target     func(string) json.RawMessage
		newRuntime func(*testing.T, string) *testRuntime
	}{
		{
			name: "official vendor extension", channelID: channel.OpenAI,
			method: http.MethodPatch, path: "/v1/responses/vendor_extension",
			body:     []byte(`{"model":"client-model","provider":"injected","api_key":"injected","vendor":{"precise":1.2300}}`),
			status:   http.StatusMultiStatus,
			response: `{"object":"vendor.result","vendor":{"precise":1.2300}}`,
			target:   func(string) json.RawMessage { return json.RawMessage(`{}`) },
			newRuntime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
			},
		},
		{
			name: "model optional head resource", channelID: channel.OpenAI,
			method: http.MethodHead, path: "/v1/responses/resp_123/vendor_state",
			status: http.StatusNoContent,
			target: func(string) json.RawMessage { return json.RawMessage(`{}`) },
			newRuntime: func(t *testing.T, base string) *testRuntime {
				return newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, openAIBaseURL: base})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.Method != test.method || request.URL.Path != test.path || request.URL.Query().Get("vendor") != "kept" {
					t.Errorf("target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
				}
				if request.Header.Get("Authorization") != "Bearer "+testAPIKey || request.Header.Get("Api-Key") != "" {
					t.Errorf("credential headers = %#v", request.Header)
				}
				body, _ := io.ReadAll(request.Body)
				if len(test.body) == 0 {
					if len(body) != 0 {
						t.Errorf("unexpected body = %s", body)
					}
				} else {
					var payload map[string]any
					if err := json.Unmarshal(body, &payload); err != nil {
						t.Fatalf("decode body: %v", err)
					}
					if payload["model"] != "upstream-model" || payload["provider"] != nil || payload["api_key"] != nil || payload["vendor"] == nil {
						t.Errorf("sanitized body = %#v", payload)
					}
				}
				writer.Header().Set("X-Request-Id", "vendor-request")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			runtime := test.newRuntime(t, server.URL)
			spec := convertedSpec(test.channelID, protocol.OpenAIResponses, execution.OperationResponsesPassthrough, test.path, test.body)
			spec.Method = test.method
			spec.TargetConfig = test.target(server.URL)
			spec = freezeTestAttempt(spec)
			spec.Query.Set("vendor", "kept")
			if len(test.body) == 0 {
				spec.ClientModel = ""
				spec.UpstreamModel = ""
			}
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if calls.Load() != 1 || result.Error != nil || result.StatusCode != test.status || string(result.Body) != test.response {
				t.Fatalf("calls/result = %d/%+v body=%s", calls.Load(), result, result.Body)
			}
		})
	}
}

func TestResponsesPassthroughFailsClosedForNonOpenAIProvider(t *testing.T) {
	t.Parallel()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := convertedSpec(channel.Anthropic, protocol.OpenAIResponses, execution.OperationResponsesPassthrough, "/v1/responses/vendor_extension", []byte(`{"vendor":true}`))
	spec.ClientModel = ""
	spec.UpstreamModel = ""
	result := runtime.Execute(context.Background(), spec)
	if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted || result.Error == nil ||
		result.Error.Kind != execution.ErrorKindConversionUnsupported ||
		result.Error.Code != execution.ErrorCodeTargetConversionNotSupported {
		t.Fatalf("result = %+v", result)
	}
}

func TestGenericOpenAICompatibleDoesNotAdvertiseUnknownResponsesPassthrough(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	spec := convertedSpec(channel.OpenAICompatible, protocol.OpenAIResponses, execution.OperationResponsesPassthrough, "/v1/responses/vendor_extension", []byte(`{"vendor":true}`))
	spec.TargetConfig, _ = json.Marshal(map[string]string{"base_url": server.URL + "/v1"})
	spec.TargetKind = string(channel.ProviderOpenAICompatible)
	spec.RouteMode = execution.RouteNative
	result := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true}).Execute(context.Background(), spec)
	if calls.Load() != 0 || result.DispatchState != execution.DispatchNotSent || result.Error == nil ||
		result.Error.Kind != execution.ErrorKindConversionUnsupported ||
		result.Error.Code != execution.ErrorCodeTargetConversionNotSupported {
		t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
	}
}

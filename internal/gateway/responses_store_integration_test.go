package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/bifrost"
	"gpt-load/internal/protocol"
)

func TestExecutionForwarderAppliesStatelessStoreToBifrostNativeAndConvertedRoutes(t *testing.T) {
	tests := []struct {
		name              string
		channelID         channel.ID
		wantPath          string
		wantUpstreamStore bool
		response          string
	}{
		{
			name: "OpenRouter native", channelID: channel.OpenRouter,
			wantPath: "/v1/responses", wantUpstreamStore: true,
			response: `{"id":"resp","object":"response","status":"completed","model":"upstream-model","store":true,"output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "DeepSeek native", channelID: channel.DeepSeek,
			wantPath: "/responses", wantUpstreamStore: true,
			response: `{"id":"resp","object":"response","status":"completed","model":"upstream-model","store":false,"output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "Anthropic converted", channelID: channel.Anthropic,
			wantPath: "/v1/messages",
			response: `{"id":"msg","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != test.wantPath {
					t.Errorf("upstream target = %s %s, want POST %s", request.Method, request.URL.Path, test.wantPath)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(body, []byte(`"store":true`)) {
					t.Errorf("store:true reached upstream: %s", body)
				}
				if test.wantUpstreamStore && !bytes.Contains(body, []byte(`"store":false`)) {
					t.Errorf("store:false missing upstream: %s", body)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			channels := channel.NewRegistry()
			target, err := channels.Resolve(
				test.channelID,
				json.RawMessage(`{"base_url":"`+server.URL+`"}`),
			)
			if err != nil {
				t.Fatal(err)
			}
			mode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate)
			if !ok {
				t.Fatal("Responses create route is missing")
			}
			runtime, err := bifrost.NewRuntime(t.Context(), channels)
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Shutdown()

			input := ForwardInput{
				Dialect:                  dialect.NewOpenAIResponses(),
				RequestID:                "request-1",
				AttemptID:                "attempt-1",
				AttemptSequence:          1,
				ClientProtocol:           protocol.OpenAIResponses,
				Operation:                execution.OperationResponsesCreate,
				RouteRequirement:         execution.RouteRequirementAny,
				ResponsesStoreDowngraded: true,
				ChannelID:                string(test.channelID),
				RouteMode:                execution.RouteMode(mode),
				TargetConfig:             target.TargetConfig,
				ExternalModel:            "client-model",
				UpstreamModelID:          "upstream-model",
				Credential:               execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":"upstream-key"}`)),
				Request: &dialect.ParsedRequest{
					Method: http.MethodPost,
					Path:   "/v1/responses",
					Header: http.Header{"Content-Type": {"application/json"}},
					Body:   []byte(`{"model":"client-model","input":"hello","store":true}`),
				},
			}
			result := NewExecutionForwarder(runtime).Forward(context.Background(), input)
			if result.Err != nil || !bytes.Contains(result.Body, []byte(`"store":false`)) ||
				bytes.Contains(result.Body, []byte(`"store":true`)) {
				t.Fatalf("Forward() = %#v, body=%s", result, result.Body)
			}
		})
	}
}

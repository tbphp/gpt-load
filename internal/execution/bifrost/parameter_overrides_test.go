package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/providers/openai"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestConvertedOverridesReachStreamingUpstream(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicResponsesStreamFixture)
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL})
	spec := convertedSpec(channel.Anthropic, protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses",
		[]byte(`{"model":"client-model","input":[{"role":"developer","content":[{"type":"input_text","text":"stable","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":"hi"}],"stream":true,"max_output_tokens":16,"cache_control":{"type":"ephemeral"}}`))
	spec.ConfiguredParameters = []string{"cache_control", "max_output_tokens"}
	result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error { return event.Validate() })
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	select {
	case body := <-bodies:
		if body["cache_control"] == nil || body["stream"] != true || body["max_tokens"] != float64(16) {
			t.Fatalf("upstream body = %#v", body)
		}
		system, ok := body["system"].([]any)
		if !ok || len(system) == 0 || system[0].(map[string]any)["cache_control"] == nil {
			t.Fatalf("block cache marker lost: %#v", body)
		}
	default:
		t.Fatal("upstream was not called")
	}
}

func TestConvertedOverridesOnlyUseConfiguredFinalValues(t *testing.T) {
	spec := execution.AttemptSpec{
		RouteMode: execution.RouteConverted, ClientProtocol: protocol.OpenAIResponses,
		ConfiguredParameters: []string{"cache_control", "removed", "max_output_tokens", "provider", "api_key", "stream", "model", "store"},
		Body:                 []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"},"client_only":true,"max_output_tokens":16,"provider":"other","api_key":"synthetic","model":"other","stream":true,"store":true}`),
	}
	got, err := convertedParameterOverrides(spec)
	want := map[string]any{"cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("extensions = %#v, error = %v", got, err)
	}
	spec.ConfiguredParameters = nil
	if got, err := convertedParameterOverrides(spec); err != nil || len(got) != 0 {
		t.Fatalf("unconfigured client fields became extensions: %#v, %v", got, err)
	}
}

func TestConvertedOverridesPreserveSDKTargetFiltering(t *testing.T) {
	spec := execution.AttemptSpec{
		RouteMode: execution.RouteConverted, ClientProtocol: protocol.Anthropic,
		Operation: execution.OperationChatCompletion, UpstreamModel: "gpt-upstream",
		ConfiguredParameters: []string{"custom_option"},
		Body:                 []byte(`{"model":"claude-test","max_tokens":16,"top_k":7,"messages":[{"role":"user","content":"hi"}],"custom_option":true}`),
	}
	request, err := buildConvertedResponsesRequest(spec, schemas.OpenAI)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{}
	ctx := runtime.newSDKContext(context.Background(), spec, schemas.Key{})
	defer ctx.Cancel()
	body, sdkErr := providerUtils.CheckContextAndGetRequestBody(ctx, request, func() (providerUtils.RequestBodyWithExtraParams, error) {
		return openai.ToOpenAIResponsesRequest(ctx, request), nil
	})
	if sdkErr != nil {
		t.Fatal(sdkErr)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["top_k"] != nil || fields["custom_option"] != nil || fields["max_output_tokens"] != float64(16) {
		t.Fatalf("SDK filtering changed: %s", body)
	}
}

func TestConvertedOverridesSupportGeminiTargetExtensions(t *testing.T) {
	spec := execution.AttemptSpec{
		RouteMode: execution.RouteConverted, ClientProtocol: protocol.Anthropic,
		Operation: execution.OperationChatCompletion, UpstreamModel: "gemini-test",
		ConfiguredParameters: []string{"cached_content"},
		Body:                 []byte(`{"model":"claude-test","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"cached_content":"cachedContents/synthetic"}`),
	}
	request, err := buildConvertedResponsesRequest(spec, schemas.Gemini)
	if err != nil {
		t.Fatal(err)
	}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	defer ctx.Cancel()
	wire, err := gemini.ToGeminiResponsesRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if wire.CachedContent != "cachedContents/synthetic" {
		t.Fatalf("cached content = %q", wire.CachedContent)
	}
}

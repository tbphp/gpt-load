package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestConvertedResponsesUnaryReasoningIncludesSummary(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chat_1","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning":"think"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	target, err := json.Marshal(map[string]string{"base_url": server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	spec := convertedSpec(channel.OpenAICompatible, protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses", []byte(`{"model":"client-model","input":"hi"}`))
	spec.TargetConfig = target
	spec = freezeTestAttempt(spec)
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil {
		t.Fatalf("result = %+v err=%v body=%s", result, err, result.Body)
	}

	var payload struct {
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(result.Body, &payload); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, result.Body)
	}
	for _, item := range payload.Output {
		if item["type"] != "reasoning" {
			continue
		}
		summary, ok := item["summary"].([]any)
		if !ok || len(summary) != 1 {
			t.Fatalf("summary = %#v, body=%s", item["summary"], result.Body)
		}
		part, ok := summary[0].(map[string]any)
		if !ok || part["type"] != "summary_text" || part["text"] != "think" {
			t.Fatalf("summary part = %#v", summary[0])
		}
		return
	}
	t.Fatalf("no reasoning item in output: %s", result.Body)
}

func TestEnsureResponsesReasoningSummaryMatchesCLIProxyAPIShape(t *testing.T) {
	t.Parallel()

	reasoningType := schemas.ResponsesMessageTypeReasoning
	text := "think"
	item := &schemas.ResponsesMessage{
		Type: &reasoningType,
		Content: &schemas.ResponsesMessageContent{
			ContentBlocks: []schemas.ResponsesMessageContentBlock{{
				Type: schemas.ResponsesOutputMessageContentTypeReasoning,
				Text: &text,
			}},
		},
	}
	response := &schemas.BifrostResponsesStreamResponse{
		Type: schemas.ResponsesStreamResponseTypeOutputItemDone,
		Item: item,
		Response: &schemas.BifrostResponsesResponse{
			Output: []schemas.ResponsesMessage{*item},
		},
	}

	ensureResponsesReasoningSummary(response)

	for _, raw := range [][]byte{mustMarshal(t, response.Item), mustMarshal(t, response.Response.Output[0])} {
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		summary, ok := payload["summary"].([]any)
		if !ok || len(summary) != 1 {
			t.Fatalf("summary = %#v, body=%s", payload["summary"], raw)
		}
		part, ok := summary[0].(map[string]any)
		if !ok || part["type"] != "summary_text" || part["text"] != "think" {
			t.Fatalf("summary part = %#v", summary[0])
		}
	}

	empty := &schemas.ResponsesMessage{Type: &reasoningType}
	ensureResponsesReasoningSummary(&schemas.BifrostResponsesStreamResponse{Item: empty})
	raw := mustMarshal(t, empty)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	summary, ok := payload["summary"].([]any)
	if !ok || len(summary) != 0 {
		t.Fatalf("empty summary = %#v, body=%s", payload["summary"], raw)
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

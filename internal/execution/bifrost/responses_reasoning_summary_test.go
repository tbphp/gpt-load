package bifrost

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

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

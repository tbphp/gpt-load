package responsealias

import (
	"bytes"
	"encoding/json"
	"testing"

	"gpt-load/internal/protocol"
)

func decodeChoicePart(t *testing.T, payload []byte, key string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("payload is not JSON: %v (%s)", err, payload)
	}
	choices, ok := doc["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("payload has no choices: %s", payload)
	}
	choice := choices[0].(map[string]any)
	part, ok := choice[key].(map[string]any)
	if !ok {
		t.Fatalf("payload has no %s: %s", key, payload)
	}
	return part
}

func TestNormalizeOpenAIReasoningCopiesEitherSpelling(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		partKey string
		want    string
	}{
		{
			name:    "message reasoning copied to content",
			payload: `{"choices":[{"message":{"role":"assistant","content":"x","reasoning":"think"}}]}`,
			partKey: "message",
			want:    "think",
		},
		{
			name:    "message content copied to reasoning",
			payload: `{"choices":[{"message":{"role":"assistant","reasoning_content":"think"}}]}`,
			partKey: "message",
			want:    "think",
		},
		{
			name:    "delta reasoning copied to content",
			payload: `{"choices":[{"index":0,"delta":{"reasoning":"tok"}}]}`,
			partKey: "delta",
			want:    "tok",
		},
		{
			name:    "empty destination is filled",
			payload: `{"choices":[{"message":{"reasoning":"think","reasoning_content":""}}]}`,
			partKey: "message",
			want:    "think",
		},
		{
			name:    "empty reasoning is backfilled from content",
			payload: `{"choices":[{"message":{"reasoning":"","reasoning_content":"think"}}]}`,
			partKey: "message",
			want:    "think",
		},
		{
			name:    "null reasoning is backfilled from content",
			payload: `{"choices":[{"message":{"reasoning":null,"reasoning_content":"think"}}]}`,
			partKey: "message",
			want:    "think",
		},
		{
			name:    "null content is backfilled from reasoning",
			payload: `{"choices":[{"message":{"reasoning":"think","reasoning_content":null}}]}`,
			partKey: "message",
			want:    "think",
		},
		{
			name:    "delta empty reasoning is backfilled from content",
			payload: `{"choices":[{"index":0,"delta":{"reasoning":"","reasoning_content":"tok"}}]}`,
			partKey: "delta",
			want:    "tok",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out := normalizeOpenAIReasoning([]byte(test.payload))
			part := decodeChoicePart(t, out, test.partKey)
			if part[reasoningField] != test.want || part[contentField] != test.want {
				t.Fatalf("spellings not both %q: %s", test.want, out)
			}
		})
	}
}

func TestNormalizeOpenAIReasoningLeavesPayloadsUntouched(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{
			name:    "both spellings non-empty",
			payload: `{"choices":[{"message":{"reasoning":"a","reasoning_content":"b"}}]}`,
		},
		{
			name:    "both spellings empty strings",
			payload: `{"choices":[{"message":{"reasoning":"","reasoning_content":""}}]}`,
		},
		{
			name:    "non-string spellings ignored",
			payload: `{"choices":[{"delta":{"reasoning":42}}]}`,
		},
		{
			name:    "no reasoning fields at all",
			payload: `{"choices":[{"delta":{"content":"plain"}}]}`,
		},
		{
			name:    "missing choices",
			payload: `{"id":"chatcmpl-1","reasoning_content":"top level"}`,
		},
		{
			name:    "malformed json",
			payload: `{"choices":[{"delta":{"reasoning":`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out := normalizeOpenAIReasoning([]byte(test.payload))
			if !bytes.Equal(out, []byte(test.payload)) {
				t.Fatalf("payload rewritten: %s", out)
			}
		})
	}
}

func TestRewriteJSONReasoningScopesToOpenAICompletions(t *testing.T) {
	payload := []byte(`{"choices":[{"message":{"role":"assistant","reasoning":"think"}}]}`)
	out, err := RewriteJSONReasoning(protocol.OpenAICompletions, payload, "", ReasoningModeDuplicate)
	if err != nil {
		t.Fatalf("rewrite error = %v", err)
	}
	part := decodeChoicePart(t, out, "message")
	if part[contentField] != "think" || part[reasoningField] != "think" {
		t.Fatalf("duplicate missing: %s", out)
	}
	out, err = RewriteJSONReasoning(protocol.OpenAICompletions, payload, "", ReasoningModeOff)
	if err != nil {
		t.Fatalf("off rewrite error = %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("off mode rewrote payload: %s", out)
	}
	out, err = RewriteJSONReasoning(protocol.Anthropic, payload, "", ReasoningModeDuplicate)
	if err != nil {
		t.Fatalf("anthropic rewrite error = %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("anthropic payload was rewritten: %s", out)
	}
}

func TestRewriteSSEReasoningCopiesBothSpellingsPerEvent(t *testing.T) {
	data := []byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"a\"}}]}\n\n" +
		"data: [DONE]\n\n")
	out, err := RewriteSSEReasoning(protocol.OpenAICompletions, data, "", ReasoningModeDuplicate)
	if err != nil {
		t.Fatalf("rewrite sse error = %v", err)
	}
	events := 0
	for _, line := range bytes.Split(out, []byte("\n\n")) {
		if !bytes.HasPrefix(line, []byte("data: {")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))
		part := decodeChoicePart(t, payload, "delta")
		if part[contentField] != "a" || part[reasoningField] != "a" {
			t.Fatalf("delta duplicate missing: %s", payload)
		}
		events++
	}
	if events != 1 {
		t.Fatalf("rewritten events = %d, want 1: %s", events, out)
	}
	if !bytes.Contains(out, []byte("[DONE]")) {
		t.Fatalf("DONE marker lost: %s", out)
	}
}

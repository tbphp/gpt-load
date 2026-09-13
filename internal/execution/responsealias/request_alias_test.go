package responsealias

import (
	"bytes"
	"encoding/json"
	"testing"
)

func decodeRequestMessage(t *testing.T, body []byte, index int) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, body)
	}
	messages, ok := doc["messages"].([]any)
	if !ok || index >= len(messages) {
		t.Fatalf("body has no message %d: %s", index, body)
	}
	return messages[index].(map[string]any)
}

func TestRewriteRequestMessagesRenamesBothDirections(t *testing.T) {
	cases := []struct {
		name    string
		mode    ReasoningMode
		body    string
		source  string
		target  string
		wantVal string
	}{
		{
			name:    "reasoning to content",
			mode:    ReasoningModeReasoningToContent,
			body:    `{"model":"m","messages":[{"role":"assistant","content":"x","reasoning":"old think"},{"role":"user","content":"next"}]}`,
			source:  reasoningField,
			target:  contentField,
			wantVal: "old think",
		},
		{
			name:    "content to reasoning",
			mode:    ReasoningModeContentToReasoning,
			body:    `{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_content":"old think"}]}`,
			source:  contentField,
			target:  reasoningField,
			wantVal: "old think",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out := RewriteRequestMessages([]byte(test.body), test.mode)
			message := decodeRequestMessage(t, out, 0)
			if got := message[test.target]; got != test.wantVal {
				t.Fatalf("target %s = %v, want %q (%s)", test.target, got, test.wantVal, out)
			}
			if _, exists := message[test.source]; exists {
				t.Fatalf("source %s survived the rename: %s", test.source, out)
			}
		})
	}
}

func TestRewriteRequestMessagesNonEmptyTargetWins(t *testing.T) {
	out := RewriteRequestMessages(
		[]byte(`{"messages":[{"role":"assistant","reasoning":"new","reasoning_content":"keep"}]}`),
		ReasoningModeReasoningToContent,
	)
	message := decodeRequestMessage(t, out, 0)
	if got := message[contentField]; got != "keep" {
		t.Fatalf("target %s = %v, want %q (%s)", contentField, got, "keep", out)
	}
	if _, exists := message[reasoningField]; exists {
		t.Fatalf("source %s survived the rename: %s", reasoningField, out)
	}
}

func TestRewriteRequestMessagesLeavesBodiesUntouched(t *testing.T) {
	cases := []struct {
		name string
		mode ReasoningMode
		body string
	}{
		{
			name: "mode off",
			mode: ReasoningModeOff,
			body: `{"messages":[{"role":"assistant","reasoning":"think"}]}`,
		},
		{
			name: "response-only duplicate selects off",
			mode: ReasoningModeDuplicate,
			body: `{"messages":[{"role":"assistant","reasoning":"think"}]}`,
		},
		{
			name: "no messages",
			mode: ReasoningModeReasoningToContent,
			body: `{"model":"m","reasoning":"top level"}`,
		},
		{
			name: "malformed json",
			mode: ReasoningModeContentToReasoning,
			body: `{"messages":[{"reasoning_content":"`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out := RewriteRequestMessages([]byte(test.body), test.mode)
			if !bytes.Equal(out, []byte(test.body)) {
				t.Fatalf("body rewritten: %s", out)
			}
		})
	}
}

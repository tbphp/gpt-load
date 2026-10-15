package codexrecovery

import (
	"encoding/json"
	"testing"
)

func TestSanitizeStripsEncryptedReasoningAndKeepsRest(t *testing.T) {
	body := []byte(`{
		"model": "gpt-6.1-sol",
		"stream": true,
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]},
			{"type": "reasoning", "id": "rs_1", "encrypted_content": "AAAA", "summary": []},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "hello"}]}
		]
	}`)

	sanitized, removals, ok := Sanitize(body)
	if !ok {
		t.Fatal("expected the body to be sanitized")
	}
	if removals.ReasoningEncryptedContent != 1 || removals.ReasoningItem != 1 {
		t.Fatalf("unexpected removals: %+v", removals)
	}

	var decoded map[string]any
	if err := json.Unmarshal(sanitized, &decoded); err != nil {
		t.Fatalf("sanitized body is not valid JSON: %v", err)
	}
	items, ok := decoded["input"].([]any)
	if !ok {
		t.Fatalf("input is not an array: %T", decoded["input"])
	}
	if len(items) != 2 {
		t.Fatalf("expected the empty reasoning item to be dropped, got %d items", len(items))
	}
	for _, item := range items {
		record, isObject := item.(map[string]any)
		if !isObject {
			continue
		}
		if _, exists := record["encrypted_content"]; exists {
			t.Fatalf("encrypted_content survived sanitize: %+v", record)
		}
	}
	// The rest of the request is untouched.
	if decoded["model"] != "gpt-6.1-sol" || decoded["stream"] != true {
		t.Fatalf("unrelated fields were modified: %+v", decoded)
	}
}

func TestSanitizeStripsToolOutputAndCompaction(t *testing.T) {
	body := []byte(`{
		"input": [
			{"type": "function_call_output", "call_id": "c1", "output": [
				{"type": "output_text", "text": "result"},
				{"type": "encrypted_content", "encrypted_content": "BBBB"}
			]},
			{"type": "compaction", "encrypted_content": "CCCC"}
		]
	}`)

	sanitized, removals, ok := Sanitize(body)
	if !ok {
		t.Fatal("expected the body to be sanitized")
	}
	if removals.FunctionOutputEncryptedContent != 1 {
		t.Fatalf("function output removals = %d, want 1", removals.FunctionOutputEncryptedContent)
	}
	if removals.CompactionItem != 1 {
		t.Fatalf("compaction item removals = %d, want 1", removals.CompactionItem)
	}

	var decoded map[string]any
	if err := json.Unmarshal(sanitized, &decoded); err != nil {
		t.Fatalf("sanitized body is not valid JSON: %v", err)
	}
	items := decoded["input"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected the compaction item to be dropped, got %d items", len(items))
	}
	output := items[0].(map[string]any)["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("expected the encrypted tool output to be dropped, got %d entries", len(output))
	}
}

func TestSanitizeLeavesBodiesWithoutEncryptedContentUntouched(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","role":"user","content":"plain"}]}`)
	if sanitized, removals, ok := Sanitize(body); ok {
		t.Fatalf("expected no change, got body=%s removals=%+v", sanitized, removals)
	}
}

func TestSanitizeHandlesSingleObjectInput(t *testing.T) {
	body := []byte(`{"input":{"type":"reasoning","encrypted_content":"AAAA"}}`)
	sanitized, removals, ok := Sanitize(body)
	if !ok {
		t.Fatal("expected the body to be sanitized")
	}
	if removals.ReasoningItem != 1 {
		t.Fatalf("reasoning item removals = %d, want 1", removals.ReasoningItem)
	}
	var decoded map[string]any
	if err := json.Unmarshal(sanitized, &decoded); err != nil {
		t.Fatalf("sanitized body is not valid JSON: %v", err)
	}
	// A single-object input collapses back to the object form; an emptied
	// single item becomes an empty array.
	if items, isArray := decoded["input"].([]any); !isArray || len(items) != 0 {
		t.Fatalf("expected an empty array input, got %#v", decoded["input"])
	}
}

func TestSanitizeRejectsNonObjectBody(t *testing.T) {
	if _, _, ok := Sanitize([]byte(`not json`)); ok {
		t.Fatal("expected non-JSON body to be rejected")
	}
}

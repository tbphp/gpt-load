package codexrecovery

import (
	"bytes"
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

func TestSanitizeRebuildsInlineHistoryWithoutChangingSemanticFields(t *testing.T) {
	body := []byte(`{
		"model":"model-a","store":false,"vendor_number":9007199254740993,
		"input":[
			{"type":"message","id":"msg_old","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"checking"}]},
			{"type":"function_call","id":"fc_old","call_id":"call_keep","name":"lookup","arguments":"{\"id\":9007199254740993}"},
			{"type":"function_call_output","id":"fco_old","call_id":"call_keep","output":"result"},
			{"type":"reasoning","id":"rs_old","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"keep summary"}]},
			{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file_keep"}]},
			{"type":"vendor_item","id":"vendor_keep","container_id":"container_keep"}
		]
	}`)
	cleaned, _, ok := Sanitize(body)
	if !ok {
		t.Fatal("expected inline history recovery")
	}
	for _, old := range []string{"msg_old", "fc_old", "fco_old", "rs_old", "opaque"} {
		if bytes.Contains(cleaned, []byte(old)) {
			t.Fatalf("rejected state %q survived: %s", old, cleaned)
		}
	}
	for _, kept := range []string{"call_keep", "lookup", "result", "keep summary", "commentary", "file_keep", "vendor_keep", "container_keep", "9007199254740993"} {
		if !bytes.Contains(cleaned, []byte(kept)) {
			t.Fatalf("semantic field %q was lost: %s", kept, cleaned)
		}
	}
	if _, _, changed := Sanitize(cleaned); changed {
		t.Fatal("recovery should be idempotent")
	}
}

func TestSanitizeRecoversHistoryIDsWithoutEncryptedContent(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","id":"msg_old","role":"assistant","content":[{"type":"output_text","text":"history"}]}]}`)
	cleaned, _, ok := Sanitize(body)
	if !ok || bytes.Contains(cleaned, []byte("msg_old")) || !bytes.Contains(cleaned, []byte("history")) {
		t.Fatalf("inline ID recovery = %t %s", ok, cleaned)
	}
}

func TestSanitizePreservesServerSideContextReferences(t *testing.T) {
	body := []byte(`{"previous_response_id":"resp_keep","conversation":"conv_keep","input":[{"type":"item_reference","id":"msg_keep"}]}`)
	if cleaned, _, changed := Sanitize(body); changed {
		t.Fatalf("reference-only history cannot be rebuilt: %s", cleaned)
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

func TestStripEncryptedContentDigestsRemovesOnlyRejectedValues(t *testing.T) {
	body := []byte(`{
		"input": [
			{"type":"reasoning","id":"rs_bad","encrypted_content":"opaque-bad","summary":[{"type":"summary_text","text":"bad"}]},
			{"type":"reasoning","id":"rs_good","encrypted_content":"opaque-good","summary":[{"type":"summary_text","text":"good"}]},
			{"type":"message","role":"user","content":"continue"}
		]
	}`)
	digests := map[string]struct{}{EncryptedContentDigest("opaque-bad"): {}}
	stripped, removals, ok := StripEncryptedContentDigests(body, digests)
	if !ok || removals.Total() != 1 {
		t.Fatalf("strip result = %t %+v", ok, removals)
	}
	text := string(stripped)
	if bytes.Contains(stripped, []byte("opaque-bad")) || !bytes.Contains(stripped, []byte("opaque-good")) {
		t.Fatalf("digest-specific strip was not preserved: %s", text)
	}
	if !bytes.Contains(stripped, []byte(`"id":"rs_good"`)) {
		t.Fatalf("valid item ID was not preserved: %s", text)
	}
}

func TestStripHistoryItemIDsPreservesReadableItemsAndDropsBareReferences(t *testing.T) {
	body := []byte(`{
		"input": [
			{"type":"message","id":"msg_bad","role":"assistant","content":[{"type":"output_text","text":"keep"}]},
			{"type":"function_call","id":"fc_bad","call_id":"call_keep","name":"lookup","arguments":"{}"},
			{"type":"item_reference","id":"rs_server_only"},
			{"type":"reasoning","id":"rs_good","summary":[{"type":"summary_text","text":"keep good"}]}
		]
	}`)
	rejected := map[string]struct{}{
		"msg_bad":        {},
		"fc_bad":         {},
		"rs_server_only": {},
	}
	stripped, removals, ok := StripHistoryItemIDs(body, rejected)
	if !ok || removals.HistoryItemID != 3 {
		t.Fatalf("strip result = %t %+v", ok, removals)
	}
	text := string(stripped)
	for _, removed := range []string{"msg_bad", "fc_bad", "rs_server_only"} {
		if bytes.Contains(stripped, []byte(removed)) {
			t.Fatalf("rejected item ID %q survived: %s", removed, text)
		}
	}
	for _, preserved := range []string{"keep", "call_keep", "lookup", "rs_good", "keep good"} {
		if !bytes.Contains(stripped, []byte(preserved)) {
			t.Fatalf("readable item state %q was lost: %s", preserved, text)
		}
	}
}

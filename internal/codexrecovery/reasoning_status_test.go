package codexrecovery

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestStripReasoningStatusOnlyAndIdempotent(t *testing.T) {
	for _, input := range []string{
		`[{"type":"reasoning","id":"item_9d700c62-046b-426a-8431-27497dcd0a0a_0","status":"completed","encrypted_content":"","summary":[{"type":"summary_text","text":"keep"}]},{"type":"reasoning","id":"rs_native","status":null,"encrypted_content":"native opaque bytes","summary":[]},{"type":"message","status":"completed","content":"keep"},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}","status":"completed"},{"type":"function_call_output","call_id":"call_1","output":"keep"}]`,
		`{"type":"reasoning","status":"completed","id":"keep","summary":[{"type":"summary_text","text":"keep"}]}`,
	} {
		body := []byte(`{"store":false,"input":` + input + `,"metadata":{"n":9007199254740993}}`)
		original := bytes.Clone(body)
		want, _ := decodeJSONObject(body)
		items, _ := responseInputItems(want["input"])
		for _, item := range items {
			record := item.(map[string]any)
			if record["type"] == "reasoning" {
				delete(record, "status")
			}
		}
		for range 2 {
			got, changed := StripReasoningStatus(body)
			if !changed {
				t.Fatal("same history must be cleaned on every turn")
			}
			decoded, ok := decodeJSONObject(got)
			if !ok || !reflect.DeepEqual(decoded, want) {
				t.Fatalf("changed fields other than reasoning.status: %s", got)
			}
			if !bytes.Equal(body, original) {
				t.Fatal("source body mutated")
			}
			if _, changed := StripReasoningStatus(got); changed {
				t.Fatal("cleanup is not idempotent")
			}
		}
	}
}

func TestStripReasoningStatusLeavesOtherBodiesUntouched(t *testing.T) {
	for _, body := range []string{
		`not json`, `[]`, `null`, `{} {}`, `{"input":"text"}`,
		`{"input":[{"type":"reasoning","encrypted_content":"keep"},{"type":"message","status":"completed"},null,"text"]}`,
		`{"input":[{"type":"item_reference","id":"rs_keep"}]}`,
	} {
		if got, changed := StripReasoningStatus([]byte(body)); changed || got != nil {
			t.Fatalf("unexpected cleanup for %s: %s", body, got)
		}
	}
}

func TestStripReasoningStatusPreservesInlineReferences(t *testing.T) {
	body := []byte(`{"previous_response_id":"resp_keep","input":[{"type":"item_reference","id":"rs_keep"},{"type":"reasoning","status":"completed","summary":[]}]}`)
	got, changed := StripReasoningStatus(body)
	var object map[string]any
	if !changed || json.Unmarshal(got, &object) != nil || object["previous_response_id"] != "resp_keep" {
		t.Fatalf("schema cleanup changed server-side references: %s", got)
	}
}

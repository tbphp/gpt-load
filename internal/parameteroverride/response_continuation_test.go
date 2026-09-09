package parameteroverride

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestResponsesContinuationOverrideLoadsButFailsOnlyMatchingRequests(t *testing.T) {
	for _, action := range []map[string]any{
		{"set": map[string]any{"previous_response_id": "replacement"}},
		{"remove": []any{"/previous_response_id"}},
		{"remove": []any{"/previous_response_id/value"}},
	} {
		action["match"] = map[string]any{"model": "matched"}
		rules, err := Compile([]any{action})
		if err != nil {
			t.Fatalf("legacy rules must still compile: %v", err)
		}
		body := []byte(`{"model":"matched","previous_response_id":"original"}`)
		if _, _, err := rules.Apply(protocol.OpenAIResponses, execution.OperationResponsesCreate, "matched", body); err == nil {
			t.Fatal("matching override changed the continuation ID")
		}
		got, applied, err := rules.Apply(protocol.OpenAIResponses, execution.OperationResponsesCreate, "unmatched", body)
		if err != nil || applied || string(got) != string(body) {
			t.Fatalf("unmatched rule affected request: %s %t %v", got, applied, err)
		}
		if _, _, err := rules.Apply(protocol.OpenAICompletions, execution.OperationChatCompletion, "matched", body); err != nil {
			t.Fatalf("Responses restriction changed another protocol: %v", err)
		}
	}
}

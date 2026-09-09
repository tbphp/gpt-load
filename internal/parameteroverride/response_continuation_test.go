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

func TestResponsesContinuationOverrideAllowsOnlyMissingRemovals(t *testing.T) {
	for _, action := range []struct {
		name        string
		config      map[string]any
		allowAbsent bool
	}{
		{"remove root", map[string]any{"remove": []any{"/previous_response_id"}}, true},
		{"remove child", map[string]any{"remove": []any{"/previous_response_id/value"}}, true},
		{"set", map[string]any{"set": map[string]any{"previous_response_id": "injected"}}, false},
	} {
		t.Run(action.name, func(t *testing.T) {
			rules, err := Compile([]any{action.config})
			if err != nil {
				t.Fatal(err)
			}
			if err := rules.ValidateResponsesContinuation(); err == nil {
				t.Fatal("management validation accepted a protected field override")
			}
			for _, value := range []string{"", "null", `""`, `"original"`} {
				t.Run("value="+value, func(t *testing.T) {
					body := `{"model":"gpt-5","input":"hello"`
					if value != "" {
						body += `,"previous_response_id":` + value
					}
					body += "}"
					got, applied, err := rules.Apply(protocol.OpenAIResponses, execution.OperationResponsesCreate, "gpt-5", []byte(body))
					wantError := value != "" || !action.allowAbsent
					if (err != nil) != wantError {
						t.Fatalf("Apply error = %v, want error %t", err, wantError)
					}
					if err == nil && (!applied || string(got) != body) {
						t.Fatalf("no-op removal changed the request: %s, applied %t", got, applied)
					}
				})
			}
		})
	}
}

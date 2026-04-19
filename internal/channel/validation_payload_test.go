package channel

import "testing"

func TestBuildValidationPayload(t *testing.T) {
	t.Run("responses simple", func(t *testing.T) {
		payload, err := BuildValidationPayload("openai-response", "responses_simple", "gpt-5.4-mini")
		if err != nil {
			t.Fatalf("BuildValidationPayload returned error: %v", err)
		}

		if got := payload["model"]; got != "gpt-5.4-mini" {
			t.Fatalf("expected model gpt-5.4-mini, got %#v", got)
		}
		if got := payload["input"]; got != "hi" {
			t.Fatalf("expected input hi, got %#v", got)
		}
	})

	t.Run("responses messages", func(t *testing.T) {
		payload, err := BuildValidationPayload("openai-response", "responses_messages", "gpt-5.4-mini")
		if err != nil {
			t.Fatalf("BuildValidationPayload returned error: %v", err)
		}

		input, ok := payload["input"].([]map[string]any)
		if !ok {
			t.Fatalf("expected responses message array, got %#v", payload["input"])
		}
		if len(input) != 1 {
			t.Fatalf("expected one input message, got %d", len(input))
		}
		if input[0]["role"] != "user" {
			t.Fatalf("expected user role, got %#v", input[0]["role"])
		}
	})

	t.Run("chat payload", func(t *testing.T) {
		payload, err := BuildValidationPayload("openai", "chat", "gpt-4.1-nano")
		if err != nil {
			t.Fatalf("BuildValidationPayload returned error: %v", err)
		}

		messages, ok := payload["messages"].([]map[string]any)
		if !ok {
			t.Fatalf("expected chat messages, got %#v", payload["messages"])
		}
		if len(messages) != 1 {
			t.Fatalf("expected one chat message, got %d", len(messages))
		}
		if messages[0]["content"] != "hi" {
			t.Fatalf("expected hi content, got %#v", messages[0]["content"])
		}
	})
}

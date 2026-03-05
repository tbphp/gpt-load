package proxy

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/models"
)

func TestApplyAnthropicSystemPromptCount_MergeAndPreserveBlocks(t *testing.T) {
	group := &models.Group{
		ChannelType:                "anthropic",
		AnthropicSystemPromptCount: 2,
	}
	input := []byte(`{
		"model":"claude-opus-4-6",
		"system":[
			{"type":"text","text":"first"},
			{"type":"custom_block","value":"keep-me"},
			{"type":"text","text":"second","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"third"}
		]
	}`)

	out := applyAnthropicSystemPromptCount(input, group)

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	system, ok := payload["system"].([]any)
	if !ok {
		t.Fatalf("system is not array: %#v", payload["system"])
	}
	if len(system) != 3 {
		t.Fatalf("system block count = %d, want 3", len(system))
	}

	firstText := system[0].(map[string]any)["text"].(string)
	if firstText != "first" {
		t.Fatalf("first text = %q, want %q", firstText, "first")
	}
	customType := system[1].(map[string]any)["type"].(string)
	if customType != "custom_block" {
		t.Fatalf("custom block type = %q, want %q", customType, "custom_block")
	}
	secondTextBlock := system[2].(map[string]any)
	if secondText := secondTextBlock["text"].(string); secondText != "second\n\nthird" {
		t.Fatalf("merged text = %q, want %q", secondText, "second\\n\\nthird")
	}
	if _, ok := secondTextBlock["cache_control"]; !ok {
		t.Fatalf("cache_control should be preserved on merged text block")
	}
}

func TestApplyAnthropicSystemPromptCount_PadEmptyBlocks(t *testing.T) {
	group := &models.Group{
		ChannelType:                "anthropic",
		AnthropicSystemPromptCount: 2,
	}
	input := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`)

	out := applyAnthropicSystemPromptCount(input, group)

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	system, ok := payload["system"].([]any)
	if !ok {
		t.Fatalf("system is not array: %#v", payload["system"])
	}
	if len(system) != 2 {
		t.Fatalf("system block count = %d, want 2", len(system))
	}
	first := system[0].(map[string]any)["text"].(string)
	second := system[1].(map[string]any)["text"].(string)
	if first != "" || second != "" {
		t.Fatalf("padded texts = (%q, %q), want both empty", first, second)
	}
}

func TestApplyAnthropicSystemPromptCount_NonAnthropicNoChange(t *testing.T) {
	group := &models.Group{
		ChannelType:                "openai",
		AnthropicSystemPromptCount: 2,
	}
	input := []byte(`{"system":"keep","messages":[]}`)

	out := applyAnthropicSystemPromptCount(input, group)
	if string(out) != string(input) {
		t.Fatalf("non-anthropic payload should be unchanged")
	}
}

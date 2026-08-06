package dialect

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"gpt-load/internal/reasoning"
)

const maxReasoningValueBytes = 64

func inspectOpenAICompletionsReasoning(body []byte) reasoning.Config {
	root, ok := reasoningObject(body)
	if !ok {
		return reasoning.Config{}
	}
	return reasoning.Config{Effort: reasoningString(root, "reasoning_effort")}
}

func inspectOpenAIResponsesReasoning(body []byte) reasoning.Config {
	root, ok := reasoningObject(body)
	if !ok {
		return reasoning.Config{}
	}
	nested, ok := reasoningNestedObject(root, "reasoning")
	if !ok {
		return reasoning.Config{}
	}
	return reasoning.Config{
		Mode:   reasoningString(nested, "mode"),
		Effort: reasoningString(nested, "effort"),
	}
}

func inspectAnthropicReasoning(body []byte) reasoning.Config {
	root, ok := reasoningObject(body)
	if !ok {
		return reasoning.Config{}
	}
	result := reasoning.Config{}
	if thinking, exists := reasoningNestedObject(root, "thinking"); exists {
		result.Mode = reasoningString(thinking, "type")
		result.BudgetTokens = reasoningInteger(thinking, "budget_tokens")
	}
	if outputConfig, exists := reasoningNestedObject(root, "output_config"); exists {
		result.Effort = reasoningString(outputConfig, "effort")
	}
	return result
}

func inspectGeminiReasoning(body []byte) reasoning.Config {
	root, ok := reasoningObject(body)
	if !ok {
		return reasoning.Config{}
	}
	generationConfig, ok := reasoningNestedObject(root, "generationConfig")
	if !ok {
		return reasoning.Config{}
	}
	thinkingConfig, ok := reasoningNestedObject(generationConfig, "thinkingConfig")
	if !ok {
		return reasoning.Config{}
	}
	return reasoning.Config{
		Effort:       reasoningString(thinkingConfig, "thinkingLevel"),
		BudgetTokens: reasoningInteger(thinkingConfig, "thinkingBudget"),
	}
}

func reasoningObject(body []byte) (map[string]json.RawMessage, bool) {
	if len(body) == 0 {
		return nil, false
	}
	object, err := decodeJSONObject(body)
	return object, err == nil
}

func reasoningNestedObject(
	object map[string]json.RawMessage,
	field string,
) (map[string]json.RawMessage, bool) {
	raw, exists := object[field]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false
	}
	nested, err := decodeJSONObject(raw)
	return nested, err == nil
}

func reasoningString(object map[string]json.RawMessage, field string) string {
	value, ok := jsonString(object, field)
	if !ok || len(value) == 0 || len(value) > maxReasoningValueBytes {
		return ""
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return ""
	}
	return strings.ToLower(value)
}

func reasoningInteger(object map[string]json.RawMessage, field string) *int64 {
	raw, exists := object[field]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	value, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

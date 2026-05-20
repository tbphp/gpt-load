package proxy

import (
	"encoding/json"
	"strings"
)

// TokenUsage holds token counts parsed from an API response.
type TokenUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// openaiUsage maps the "usage" object in OpenAI /v1/chat/completions responses.
type openaiUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// anthropicUsage maps the "usage" object in Anthropic /v1/messages responses.
type anthropicUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// geminiUsageMetadata maps the "usageMetadata" object in Gemini responses.
type geminiUsageMetadata struct {
	PromptTokenCount     int64 `json:"promptTokenCount"`
	CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	TotalTokenCount      int64 `json:"totalTokenCount"`
}

// extractTokenUsage attempts to parse token usage from a provider response body.
// Returns nil when no usage information can be extracted (e.g. streaming, errors).
func extractTokenUsage(body []byte) *TokenUsage {
	if len(body) == 0 {
		return nil
	}

	// Try a top-level "usage" object (OpenAI, Anthropic, OpenRouter).
	var topLevel struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(body, &topLevel); err == nil && len(topLevel.Usage) > 0 {
		if u := tryOpenAIUsage(topLevel.Usage); u != nil {
			return u
		}
	}

	// Try "usageMetadata" (Gemini).
	var gemini struct {
		UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &gemini); err == nil && gemini.UsageMetadata != nil {
		return &TokenUsage{
			PromptTokens:     gemini.UsageMetadata.PromptTokenCount,
			CompletionTokens: gemini.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gemini.UsageMetadata.TotalTokenCount,
		}
	}

	return nil
}

func tryOpenAIUsage(raw json.RawMessage) *TokenUsage {
	// Standard OpenAI format: {"prompt_tokens":N,"completion_tokens":N,"total_tokens":N}
	var oai openaiUsage
	if err := json.Unmarshal(raw, &oai); err == nil && oai.TotalTokens > 0 {
		return &TokenUsage{
			PromptTokens:     oai.PromptTokens,
			CompletionTokens: oai.CompletionTokens,
			TotalTokens:      oai.TotalTokens,
		}
	}

	// Anthropic format: {"input_tokens":N,"output_tokens":N}
	// (Anthropic also nests under "usage")
	var anthro anthropicUsage
	if err := json.Unmarshal(raw, &anthro); err == nil && anthro.InputTokens > 0 {
		return &TokenUsage{
			PromptTokens:     anthro.InputTokens,
			CompletionTokens: anthro.OutputTokens,
			TotalTokens:      anthro.InputTokens + anthro.OutputTokens,
		}
	}

	return nil
}

// isChatCompletionPath returns true when the request path looks like a chat
// or text-generation endpoint where usage information is expected.
func isChatCompletionPath(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, "/chat/completions") ||
		strings.Contains(p, "/messages") ||
		strings.Contains(p, "/completions") ||
		strings.Contains(p, "/generate")
}

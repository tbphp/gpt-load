package embedded

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// extractReasoningDetails 只读取当前请求中的字段，不用源协议参数补齐上游配置。
func extractReasoningDetails(body []byte, provider string) (string, *int64) {
	var modePath, budgetPath string
	switch provider {
	case ProviderClaude:
		modePath, budgetPath = "thinking.type", "thinking.budget_tokens"
	case ProviderAntigravity:
		budgetPath = "request.generationConfig.thinkingConfig.thinkingBudget"
	case "gemini":
		budgetPath = "generationConfig.thinkingConfig.thinkingBudget"
	case ProviderCodex, ProviderGrok, "openai-response":
		modePath = "reasoning.mode"
	}
	mode := ""
	if modePath != "" {
		value := gjson.GetBytes(body, modePath)
		if value.Type == gjson.String && len(value.Str) <= 64 {
			mode = strings.ToLower(strings.TrimSpace(value.String()))
		}
	}
	if budgetPath != "" {
		value := gjson.GetBytes(body, budgetPath)
		if !value.Exists() && strings.HasSuffix(budgetPath, ".thinkingBudget") {
			value = gjson.GetBytes(body, strings.TrimSuffix(budgetPath, "thinkingBudget")+"thinking_budget")
		}
		if value.Type == gjson.Number {
			if budget, err := strconv.ParseInt(value.Raw, 10, 64); err == nil {
				return mode, &budget
			}
		}
	}
	return mode, nil
}

func (o *executionObservation) reasoningMode() string {
	if o == nil {
		return ""
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.mode
}

func (o *executionObservation) reasoningBudgetTokens() *int64 {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.budgetTokens == nil {
		return nil
	}
	budget := *o.budgetTokens
	return &budget
}

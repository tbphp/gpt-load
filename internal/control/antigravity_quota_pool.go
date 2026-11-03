package control

import "strings"

const antigravityWeeklyWindowSeconds = int64(7 * 24 * 60 * 60)

type antigravityQuotaPool uint8

const (
	antigravityPoolUnknown antigravityQuotaPool = iota
	antigravityPoolGemini
	antigravityPoolClaudeGPT
	antigravityPoolArgon
)

func antigravityWeeklyModelWindow(window ObservationQuotaWindow) bool {
	return window.Scope == "model" &&
		window.WindowSeconds != nil &&
		*window.WindowSeconds == antigravityWeeklyWindowSeconds
}

func antigravityModelBelongsToWindow(model string, window ObservationQuotaWindow) bool {
	pool := antigravityWindowPool(window)
	if pool == antigravityPoolUnknown {
		return false
	}
	return antigravityClassifyModel(model) == pool
}

func antigravityWindowPool(window ObservationQuotaWindow) antigravityQuotaPool {
	return antigravityClassifyPoolText(window.ID + " " + window.Label)
}

func antigravityClassifyPoolText(value string) antigravityQuotaPool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return antigravityPoolUnknown
	}
	switch {
	case strings.Contains(normalized, "argon"):
		return antigravityPoolArgon
	case strings.Contains(normalized, "claude"),
		strings.Contains(normalized, "gpt"),
		strings.Contains(normalized, "3p-"):
		return antigravityPoolClaudeGPT
	case strings.Contains(normalized, "gemini"):
		return antigravityPoolGemini
	default:
		return antigravityPoolUnknown
	}
}

func antigravityClassifyModel(model string) antigravityQuotaPool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return antigravityPoolUnknown
	}
	if strings.Contains(normalized, "argon") {
		return antigravityPoolArgon
	}
	if antigravityClaudeOrGPTModel(normalized) {
		return antigravityPoolClaudeGPT
	}
	if strings.Contains(normalized, "gemini") {
		return antigravityPoolGemini
	}
	return antigravityPoolUnknown
}

func antigravityClaudeOrGPTModel(model string) bool {
	if strings.Contains(model, "claude") {
		return true
	}
	if model == "gpt" || strings.HasPrefix(model, "gpt-") || strings.Contains(model, "-gpt-") {
		return true
	}
	return false
}

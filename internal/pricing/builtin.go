package pricing

import "time"

const (
	builtinOpenAIURL    = "https://developers.openai.com/api/docs/pricing"
	builtinAnthropicURL = "https://platform.claude.com/docs/en/about-claude/pricing"
	builtinGeminiURL    = "https://ai.google.dev/gemini-api/docs/pricing"
)

var builtinUpdatedAt = time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

var builtinRules = []Rule{
	{Pattern: "gpt-5.6", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, Output: Price{Value: 30, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.6-sol", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, Output: Price{Value: 30, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.6-terra", Prices: Prices{UncachedInput: Price{Value: 2.5, Set: true}, CacheRead: Price{Value: 0.25, Set: true}, Output: Price{Value: 15, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.6-luna", Prices: Prices{UncachedInput: Price{Value: 1, Set: true}, CacheRead: Price{Value: 0.1, Set: true}, Output: Price{Value: 6, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.5", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, Output: Price{Value: 30, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.5-pro", Prices: Prices{UncachedInput: Price{Value: 30, Set: true}, Output: Price{Value: 180, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-5.4", Prices: Prices{UncachedInput: Price{Value: 2.5, Set: true}, CacheRead: Price{Value: 0.25, Set: true}, Output: Price{Value: 15, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.4-mini", Prices: Prices{UncachedInput: Price{Value: 0.75, Set: true}, CacheRead: Price{Value: 0.075, Set: true}, Output: Price{Value: 4.5, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-5.4-nano", Prices: Prices{UncachedInput: Price{Value: 0.2, Set: true}, CacheRead: Price{Value: 0.02, Set: true}, Output: Price{Value: 1.25, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-5.4-pro", Prices: Prices{UncachedInput: Price{Value: 30, Set: true}, Output: Price{Value: 180, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-4.1", Prices: Prices{UncachedInput: Price{Value: 2, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, Output: Price{Value: 8, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-4.1-mini", Prices: Prices{UncachedInput: Price{Value: 0.4, Set: true}, CacheRead: Price{Value: 0.1, Set: true}, Output: Price{Value: 1.6, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-4.1-nano", Prices: Prices{UncachedInput: Price{Value: 0.1, Set: true}, CacheRead: Price{Value: 0.025, Set: true}, Output: Price{Value: 0.4, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-4o", Prices: Prices{UncachedInput: Price{Value: 2.5, Set: true}, CacheRead: Price{Value: 1.25, Set: true}, Output: Price{Value: 10, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-4o-mini", Prices: Prices{UncachedInput: Price{Value: 0.15, Set: true}, CacheRead: Price{Value: 0.075, Set: true}, Output: Price{Value: 0.6, Set: true}}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-fable-5", Prices: Prices{UncachedInput: Price{Value: 10, Set: true}, CacheRead: Price{Value: 1, Set: true}, CacheWrite5M: Price{Value: 12.5, Set: true}, CacheWrite1H: Price{Value: 20, Set: true}, Output: Price{Value: 50, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-5", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, CacheWrite5M: Price{Value: 6.25, Set: true}, CacheWrite1H: Price{Value: 10, Set: true}, Output: Price{Value: 25, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-8", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, CacheWrite5M: Price{Value: 6.25, Set: true}, CacheWrite1H: Price{Value: 10, Set: true}, Output: Price{Value: 25, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-7", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, CacheWrite5M: Price{Value: 6.25, Set: true}, CacheWrite1H: Price{Value: 10, Set: true}, Output: Price{Value: 25, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-6", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, CacheWrite5M: Price{Value: 6.25, Set: true}, CacheWrite1H: Price{Value: 10, Set: true}, Output: Price{Value: 25, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-5", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, CacheWrite5M: Price{Value: 6.25, Set: true}, CacheWrite1H: Price{Value: 10, Set: true}, Output: Price{Value: 25, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-5-20251101", Prices: Prices{UncachedInput: Price{Value: 5, Set: true}, CacheRead: Price{Value: 0.5, Set: true}, CacheWrite5M: Price{Value: 6.25, Set: true}, CacheWrite1H: Price{Value: 10, Set: true}, Output: Price{Value: 25, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-sonnet-5", Prices: Prices{UncachedInput: Price{Value: 2, Set: true}, CacheRead: Price{Value: 0.2, Set: true}, CacheWrite5M: Price{Value: 2.5, Set: true}, CacheWrite1H: Price{Value: 4, Set: true}, Output: Price{Value: 10, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-sonnet-4-6", Prices: Prices{UncachedInput: Price{Value: 3, Set: true}, CacheRead: Price{Value: 0.3, Set: true}, CacheWrite5M: Price{Value: 3.75, Set: true}, CacheWrite1H: Price{Value: 6, Set: true}, Output: Price{Value: 15, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-sonnet-4-5", Prices: Prices{UncachedInput: Price{Value: 3, Set: true}, CacheRead: Price{Value: 0.3, Set: true}, CacheWrite5M: Price{Value: 3.75, Set: true}, CacheWrite1H: Price{Value: 6, Set: true}, Output: Price{Value: 15, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-sonnet-4-5-20250929", Prices: Prices{UncachedInput: Price{Value: 3, Set: true}, CacheRead: Price{Value: 0.3, Set: true}, CacheWrite5M: Price{Value: 3.75, Set: true}, CacheWrite1H: Price{Value: 6, Set: true}, Output: Price{Value: 15, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-haiku-4-5", Prices: Prices{UncachedInput: Price{Value: 1, Set: true}, CacheRead: Price{Value: 0.1, Set: true}, CacheWrite5M: Price{Value: 1.25, Set: true}, CacheWrite1H: Price{Value: 2, Set: true}, Output: Price{Value: 5, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-haiku-4-5-20251001", Prices: Prices{UncachedInput: Price{Value: 1, Set: true}, CacheRead: Price{Value: 0.1, Set: true}, CacheWrite5M: Price{Value: 1.25, Set: true}, CacheWrite1H: Price{Value: 2, Set: true}, Output: Price{Value: 5, Set: true}}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-3.6-flash", Prices: Prices{UncachedInput: Price{Value: 1.5, Set: true}, CacheRead: Price{Value: 0.15, Set: true}, Output: Price{Value: 7.5, Set: true}}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-3.5-flash", Prices: Prices{UncachedInput: Price{Value: 1.5, Set: true}, CacheRead: Price{Value: 0.15, Set: true}, Output: Price{Value: 9, Set: true}}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-3.5-flash-lite", Prices: Prices{UncachedInput: Price{Value: 0.3, Set: true}, CacheRead: Price{Value: 0.03, Set: true}, Output: Price{Value: 2.5, Set: true}}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-3.1-flash-lite", Prices: Prices{UncachedInput: Price{Value: 0.25, Set: true}, CacheRead: Price{Value: 0.025, Set: true}, Output: Price{Value: 1.5, Set: true}}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-2.5-pro", Prices: Prices{UncachedInput: Price{Value: 1.25, Set: true}, CacheRead: Price{Value: 0.125, Set: true}, Output: Price{Value: 10, Set: true}}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-2.5-flash", Prices: Prices{UncachedInput: Price{Value: 0.3, Set: true}, CacheRead: Price{Value: 0.03, Set: true}, Output: Price{Value: 2.5, Set: true}}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-2.5-flash-lite", Prices: Prices{UncachedInput: Price{Value: 0.1, Set: true}, CacheRead: Price{Value: 0.01, Set: true}, Output: Price{Value: 0.4, Set: true}}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
}

// BuiltinRules returns a caller-owned copy of the built-in pricing rules.
func BuiltinRules() []Rule {
	result := make([]Rule, len(builtinRules))
	for index, rule := range builtinRules {
		result[index] = cloneRule(rule)
	}
	return result
}

func newBuiltinLongContextPolicy() *LongContextPolicy {
	return &LongContextPolicy{
		InputThresholdTokens: 272_000,
		InputMultiplier:      2,
		OutputMultiplier:     1.5,
	}
}

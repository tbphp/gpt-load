package pricing

import "time"

const (
	builtinOpenAIURL    = "https://developers.openai.com/api/docs/pricing"
	builtinAnthropicURL = "https://platform.claude.com/docs/en/about-claude/pricing"
	builtinGeminiURL    = "https://ai.google.dev/gemini-api/docs/pricing"
)

var builtinUpdatedAt = time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

var builtinRules = []Rule{
	{Pattern: "gpt-5.6", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), Output: mustPrice("30")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.6-sol", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), Output: mustPrice("30")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.6-terra", Prices: Prices{UncachedInput: mustPrice("2.5"), CacheRead: mustPrice("0.25"), Output: mustPrice("15")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.6-luna", Prices: Prices{UncachedInput: mustPrice("1"), CacheRead: mustPrice("0.1"), Output: mustPrice("6")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.5", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), Output: mustPrice("30")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.5-pro", Prices: Prices{UncachedInput: mustPrice("30"), Output: mustPrice("180")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-5.4", Prices: Prices{UncachedInput: mustPrice("2.5"), CacheRead: mustPrice("0.25"), Output: mustPrice("15")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-5.4-mini", Prices: Prices{UncachedInput: mustPrice("0.75"), CacheRead: mustPrice("0.075"), Output: mustPrice("4.5")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-5.4-nano", Prices: Prices{UncachedInput: mustPrice("0.2"), CacheRead: mustPrice("0.02"), Output: mustPrice("1.25")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-5.4-pro", Prices: Prices{UncachedInput: mustPrice("30"), Output: mustPrice("180")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt, LongContextPolicy: newBuiltinLongContextPolicy()},
	{Pattern: "gpt-4.1", Prices: Prices{UncachedInput: mustPrice("2"), CacheRead: mustPrice("0.5"), Output: mustPrice("8")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-4.1-mini", Prices: Prices{UncachedInput: mustPrice("0.4"), CacheRead: mustPrice("0.1"), Output: mustPrice("1.6")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-4.1-nano", Prices: Prices{UncachedInput: mustPrice("0.1"), CacheRead: mustPrice("0.025"), Output: mustPrice("0.4")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-4o", Prices: Prices{UncachedInput: mustPrice("2.5"), CacheRead: mustPrice("1.25"), Output: mustPrice("10")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gpt-4o-mini", Prices: Prices{UncachedInput: mustPrice("0.15"), CacheRead: mustPrice("0.075"), Output: mustPrice("0.6")}, Source: SourceBuiltin, SourceURL: builtinOpenAIURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-fable-5", Prices: Prices{UncachedInput: mustPrice("10"), CacheRead: mustPrice("1"), CacheWrite5M: mustPrice("12.5"), CacheWrite1H: mustPrice("20"), Output: mustPrice("50")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-5", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), CacheWrite5M: mustPrice("6.25"), CacheWrite1H: mustPrice("10"), Output: mustPrice("25")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-8", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), CacheWrite5M: mustPrice("6.25"), CacheWrite1H: mustPrice("10"), Output: mustPrice("25")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-7", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), CacheWrite5M: mustPrice("6.25"), CacheWrite1H: mustPrice("10"), Output: mustPrice("25")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-6", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), CacheWrite5M: mustPrice("6.25"), CacheWrite1H: mustPrice("10"), Output: mustPrice("25")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-5", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), CacheWrite5M: mustPrice("6.25"), CacheWrite1H: mustPrice("10"), Output: mustPrice("25")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-opus-4-5-20251101", Prices: Prices{UncachedInput: mustPrice("5"), CacheRead: mustPrice("0.5"), CacheWrite5M: mustPrice("6.25"), CacheWrite1H: mustPrice("10"), Output: mustPrice("25")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-sonnet-5", Prices: Prices{UncachedInput: mustPrice("2"), CacheRead: mustPrice("0.2"), CacheWrite5M: mustPrice("2.5"), CacheWrite1H: mustPrice("4"), Output: mustPrice("10")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-sonnet-4-6", Prices: Prices{UncachedInput: mustPrice("3"), CacheRead: mustPrice("0.3"), CacheWrite5M: mustPrice("3.75"), CacheWrite1H: mustPrice("6"), Output: mustPrice("15")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-sonnet-4-5", Prices: Prices{UncachedInput: mustPrice("3"), CacheRead: mustPrice("0.3"), CacheWrite5M: mustPrice("3.75"), CacheWrite1H: mustPrice("6"), Output: mustPrice("15")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-sonnet-4-5-20250929", Prices: Prices{UncachedInput: mustPrice("3"), CacheRead: mustPrice("0.3"), CacheWrite5M: mustPrice("3.75"), CacheWrite1H: mustPrice("6"), Output: mustPrice("15")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-haiku-4-5", Prices: Prices{UncachedInput: mustPrice("1"), CacheRead: mustPrice("0.1"), CacheWrite5M: mustPrice("1.25"), CacheWrite1H: mustPrice("2"), Output: mustPrice("5")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "claude-haiku-4-5-20251001", Prices: Prices{UncachedInput: mustPrice("1"), CacheRead: mustPrice("0.1"), CacheWrite5M: mustPrice("1.25"), CacheWrite1H: mustPrice("2"), Output: mustPrice("5")}, Source: SourceBuiltin, SourceURL: builtinAnthropicURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-3.6-flash", Prices: Prices{UncachedInput: mustPrice("1.5"), CacheRead: mustPrice("0.15"), Output: mustPrice("7.5")}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-3.5-flash", Prices: Prices{UncachedInput: mustPrice("1.5"), CacheRead: mustPrice("0.15"), Output: mustPrice("9")}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-3.5-flash-lite", Prices: Prices{UncachedInput: mustPrice("0.3"), CacheRead: mustPrice("0.03"), Output: mustPrice("2.5")}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-3.1-flash-lite", Prices: Prices{UncachedInput: mustPrice("0.25"), CacheRead: mustPrice("0.025"), Output: mustPrice("1.5")}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-2.5-pro", Prices: Prices{UncachedInput: mustPrice("1.25"), CacheRead: mustPrice("0.125"), Output: mustPrice("10")}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-2.5-flash", Prices: Prices{UncachedInput: mustPrice("0.3"), CacheRead: mustPrice("0.03"), Output: mustPrice("2.5")}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
	{Pattern: "gemini-2.5-flash-lite", Prices: Prices{UncachedInput: mustPrice("0.1"), CacheRead: mustPrice("0.01"), Output: mustPrice("0.4")}, Source: SourceBuiltin, SourceURL: builtinGeminiURL, UpdatedAt: builtinUpdatedAt},
}

// BuiltinRules returns a caller-owned copy of the built-in pricing rules.
func BuiltinRules() []Rule {
	result := make([]Rule, len(builtinRules))
	for index, rule := range builtinRules {
		result[index] = cloneRule(rule)
	}
	return result
}

func mustPrice(value string) Price {
	price, err := ParseUSD(value)
	if err != nil {
		panic("invalid builtin price " + value + ": " + err.Error())
	}
	return Price{NanoUSDPerMillion: price, Set: true}
}

func newBuiltinLongContextPolicy() *LongContextPolicy {
	return &LongContextPolicy{
		InputThresholdTokens: 272_000,
		InputMultiplier:      Multiplier{Numerator: 2, Denominator: 1},
		OutputMultiplier:     Multiplier{Numerator: 3, Denominator: 2},
	}
}

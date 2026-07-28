package pricing

import (
	"reflect"
	"testing"
	"time"
)

func TestBuiltinRulesMatchGoldenTable(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	const openAIURL = "https://developers.openai.com/api/docs/pricing"
	const anthropicURL = "https://platform.claude.com/docs/en/about-claude/pricing"
	const geminiURL = "https://ai.google.dev/gemini-api/docs/pricing"
	want := []Rule{
		{Pattern: "gpt-5.6", Prices: builtinPrices(5, 0.5, unsetPrice(), unsetPrice(), 30), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.6-sol", Prices: builtinPrices(5, 0.5, unsetPrice(), unsetPrice(), 30), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.6-terra", Prices: builtinPrices(2.5, 0.25, unsetPrice(), unsetPrice(), 15), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.6-luna", Prices: builtinPrices(1, 0.1, unsetPrice(), unsetPrice(), 6), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.5", Prices: builtinPrices(5, 0.5, unsetPrice(), unsetPrice(), 30), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.5-pro", Prices: builtinPrices(30, unsetPrice(), unsetPrice(), unsetPrice(), 180), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-5.4", Prices: builtinPrices(2.5, 0.25, unsetPrice(), unsetPrice(), 15), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.4-mini", Prices: builtinPrices(0.75, 0.075, unsetPrice(), unsetPrice(), 4.5), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-5.4-nano", Prices: builtinPrices(0.2, 0.02, unsetPrice(), unsetPrice(), 1.25), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-5.4-pro", Prices: builtinPrices(30, unsetPrice(), unsetPrice(), unsetPrice(), 180), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-4.1", Prices: builtinPrices(2, 0.5, unsetPrice(), unsetPrice(), 8), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-4.1-mini", Prices: builtinPrices(0.4, 0.1, unsetPrice(), unsetPrice(), 1.6), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-4.1-nano", Prices: builtinPrices(0.1, 0.025, unsetPrice(), unsetPrice(), 0.4), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-4o", Prices: builtinPrices(2.5, 1.25, unsetPrice(), unsetPrice(), 10), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-4o-mini", Prices: builtinPrices(0.15, 0.075, unsetPrice(), unsetPrice(), 0.6), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "claude-fable-5", Prices: builtinPrices(10, 1, 12.5, 20, 50), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-5", Prices: builtinPrices(5, 0.5, 6.25, 10, 25), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-8", Prices: builtinPrices(5, 0.5, 6.25, 10, 25), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-7", Prices: builtinPrices(5, 0.5, 6.25, 10, 25), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-6", Prices: builtinPrices(5, 0.5, 6.25, 10, 25), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-5", Prices: builtinPrices(5, 0.5, 6.25, 10, 25), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-5-20251101", Prices: builtinPrices(5, 0.5, 6.25, 10, 25), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-sonnet-5", Prices: builtinPrices(2, 0.2, 2.5, 4, 10), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-sonnet-4-6", Prices: builtinPrices(3, 0.3, 3.75, 6, 15), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-sonnet-4-5", Prices: builtinPrices(3, 0.3, 3.75, 6, 15), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-sonnet-4-5-20250929", Prices: builtinPrices(3, 0.3, 3.75, 6, 15), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-haiku-4-5", Prices: builtinPrices(1, 0.1, 1.25, 2, 5), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-haiku-4-5-20251001", Prices: builtinPrices(1, 0.1, 1.25, 2, 5), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-3.6-flash", Prices: builtinPrices(1.5, 0.15, unsetPrice(), unsetPrice(), 7.5), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-3.5-flash", Prices: builtinPrices(1.5, 0.15, unsetPrice(), unsetPrice(), 9), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-3.5-flash-lite", Prices: builtinPrices(0.3, 0.03, unsetPrice(), unsetPrice(), 2.5), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-3.1-flash-lite", Prices: builtinPrices(0.25, 0.025, unsetPrice(), unsetPrice(), 1.5), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-2.5-pro", Prices: builtinPrices(1.25, 0.125, unsetPrice(), unsetPrice(), 10), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-2.5-flash", Prices: builtinPrices(0.3, 0.03, unsetPrice(), unsetPrice(), 2.5), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-2.5-flash-lite", Prices: builtinPrices(0.1, 0.01, unsetPrice(), unsetPrice(), 0.4), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
	}

	got := BuiltinRules()
	if len(got) != len(want) {
		t.Fatalf("BuiltinRules() length = %d, want %d", len(got), len(want))
	}
	for index, rule := range want {
		if !reflect.DeepEqual(got[index], rule) {
			t.Errorf("BuiltinRules()[%d] = %+v, want %+v", index, got[index], rule)
		}
	}
	if len(got) == 0 {
		t.Fatal("BuiltinRules() unexpectedly empty")
	}
	got[0].Pattern = "mutated"
	if got[0].LongContextPolicy != nil {
		got[0].LongContextPolicy.InputMultiplier = 99
	}
	again := BuiltinRules()
	if again[0].Pattern != "gpt-5.6" || again[0].LongContextPolicy == nil ||
		again[0].LongContextPolicy.InputMultiplier != 2 {
		t.Fatalf("BuiltinRules() returned mutable backing data: %+v", again[0])
	}
}

func TestBuiltinRulesDoNotMatchUnsupportedModelFamilies(t *testing.T) {
	t.Parallel()

	table, err := Compile(BuiltinRules())
	if err != nil {
		t.Fatalf("Compile(BuiltinRules()) error = %v", err)
	}
	for _, upstreamModel := range []string{
		"gpt-realtime",
		"gpt-4o-realtime-preview",
		"gpt-audio",
		"gpt-4o-audio-preview",
		"gpt-4o-transcribe",
		"gpt-5.6-future",
		"gemini-2.5-flash-image",
		"gemini-2.5-flash-preview-native-audio-dialog",
		"gemini-2.5-flash-preview-tts",
		"gemini-3.5-pro-preview",
		"anthropic.claude-opus-4-5-v1:0",
		"claude-opus-4-5@20251101",
		"claude-sonnet-4-5@20250929",
		"claude-haiku-4-5@20251001",
	} {
		if rule, ok := table.Match(upstreamModel); ok {
			t.Errorf("Match(%q) = %+v, true; want no match", upstreamModel, rule)
		}
	}
}

func builtinPrices(uncachedInput, cacheRead, cacheWrite5M, cacheWrite1H, output any) Prices {
	return Prices{
		UncachedInput: builtinPrice(uncachedInput),
		CacheRead:     builtinPrice(cacheRead),
		CacheWrite5M:  builtinPrice(cacheWrite5M),
		CacheWrite1H:  builtinPrice(cacheWrite1H),
		Output:        builtinPrice(output),
	}
}

func unsetPrice() Price {
	return Price{}
}

func builtinPrice(value any) Price {
	switch value := value.(type) {
	case float64:
		return Price{Value: value, Set: true}
	case int:
		return Price{Value: float64(value), Set: true}
	case Price:
		return value
	default:
		panic("unsupported builtin test price")
	}
}

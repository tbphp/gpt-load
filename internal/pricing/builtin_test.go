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
		{Pattern: "gpt-5.6", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), unsetPrice(), unsetPrice(), nano(30_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.6-sol", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), unsetPrice(), unsetPrice(), nano(30_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.6-terra", Prices: builtinPrices(nano(2_500_000_000), nano(250_000_000), unsetPrice(), unsetPrice(), nano(15_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.6-luna", Prices: builtinPrices(nano(1_000_000_000), nano(100_000_000), unsetPrice(), unsetPrice(), nano(6_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.5", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), unsetPrice(), unsetPrice(), nano(30_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.5-pro", Prices: builtinPrices(nano(30_000_000_000), unsetPrice(), unsetPrice(), unsetPrice(), nano(180_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-5.4", Prices: builtinPrices(nano(2_500_000_000), nano(250_000_000), unsetPrice(), unsetPrice(), nano(15_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-5.4-mini", Prices: builtinPrices(nano(750_000_000), nano(75_000_000), unsetPrice(), unsetPrice(), nano(4_500_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-5.4-nano", Prices: builtinPrices(nano(200_000_000), nano(20_000_000), unsetPrice(), unsetPrice(), nano(1_250_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-5.4-pro", Prices: builtinPrices(nano(30_000_000_000), unsetPrice(), unsetPrice(), unsetPrice(), nano(180_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt, LongContextPolicy: testLongContextPolicy()},
		{Pattern: "gpt-4.1", Prices: builtinPrices(nano(2_000_000_000), nano(500_000_000), unsetPrice(), unsetPrice(), nano(8_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-4.1-mini", Prices: builtinPrices(nano(400_000_000), nano(100_000_000), unsetPrice(), unsetPrice(), nano(1_600_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-4.1-nano", Prices: builtinPrices(nano(100_000_000), nano(25_000_000), unsetPrice(), unsetPrice(), nano(400_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-4o", Prices: builtinPrices(nano(2_500_000_000), nano(1_250_000_000), unsetPrice(), unsetPrice(), nano(10_000_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "gpt-4o-mini", Prices: builtinPrices(nano(150_000_000), nano(75_000_000), unsetPrice(), unsetPrice(), nano(600_000_000)), Source: SourceBuiltin, SourceURL: openAIURL, UpdatedAt: updatedAt},
		{Pattern: "claude-fable-5", Prices: builtinPrices(nano(10_000_000_000), nano(1_000_000_000), nano(12_500_000_000), nano(20_000_000_000), nano(50_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-5", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), nano(6_250_000_000), nano(10_000_000_000), nano(25_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-8", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), nano(6_250_000_000), nano(10_000_000_000), nano(25_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-7", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), nano(6_250_000_000), nano(10_000_000_000), nano(25_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-6", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), nano(6_250_000_000), nano(10_000_000_000), nano(25_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-5", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), nano(6_250_000_000), nano(10_000_000_000), nano(25_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-opus-4-5-20251101", Prices: builtinPrices(nano(5_000_000_000), nano(500_000_000), nano(6_250_000_000), nano(10_000_000_000), nano(25_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-sonnet-5", Prices: builtinPrices(nano(2_000_000_000), nano(200_000_000), nano(2_500_000_000), nano(4_000_000_000), nano(10_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-sonnet-4-6", Prices: builtinPrices(nano(3_000_000_000), nano(300_000_000), nano(3_750_000_000), nano(6_000_000_000), nano(15_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-sonnet-4-5", Prices: builtinPrices(nano(3_000_000_000), nano(300_000_000), nano(3_750_000_000), nano(6_000_000_000), nano(15_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-sonnet-4-5-20250929", Prices: builtinPrices(nano(3_000_000_000), nano(300_000_000), nano(3_750_000_000), nano(6_000_000_000), nano(15_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-haiku-4-5", Prices: builtinPrices(nano(1_000_000_000), nano(100_000_000), nano(1_250_000_000), nano(2_000_000_000), nano(5_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "claude-haiku-4-5-20251001", Prices: builtinPrices(nano(1_000_000_000), nano(100_000_000), nano(1_250_000_000), nano(2_000_000_000), nano(5_000_000_000)), Source: SourceBuiltin, SourceURL: anthropicURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-3.6-flash", Prices: builtinPrices(nano(1_500_000_000), nano(150_000_000), unsetPrice(), unsetPrice(), nano(7_500_000_000)), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-3.5-flash", Prices: builtinPrices(nano(1_500_000_000), nano(150_000_000), unsetPrice(), unsetPrice(), nano(9_000_000_000)), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-3.5-flash-lite", Prices: builtinPrices(nano(300_000_000), nano(30_000_000), unsetPrice(), unsetPrice(), nano(2_500_000_000)), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-3.1-flash-lite", Prices: builtinPrices(nano(250_000_000), nano(25_000_000), unsetPrice(), unsetPrice(), nano(1_500_000_000)), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-2.5-pro", Prices: builtinPrices(nano(1_250_000_000), nano(125_000_000), unsetPrice(), unsetPrice(), nano(10_000_000_000)), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-2.5-flash", Prices: builtinPrices(nano(300_000_000), nano(30_000_000), unsetPrice(), unsetPrice(), nano(2_500_000_000)), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
		{Pattern: "gemini-2.5-flash-lite", Prices: builtinPrices(nano(100_000_000), nano(10_000_000), unsetPrice(), unsetPrice(), nano(400_000_000)), Source: SourceBuiltin, SourceURL: geminiURL, UpdatedAt: updatedAt},
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
		got[0].LongContextPolicy.InputMultiplier = Multiplier{Numerator: 99, Denominator: 1}
	}
	again := BuiltinRules()
	if again[0].Pattern != "gpt-5.6" || again[0].LongContextPolicy == nil ||
		again[0].LongContextPolicy.InputMultiplier != (Multiplier{Numerator: 2, Denominator: 1}) {
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

func builtinPrices(uncachedInput, cacheRead, cacheWrite5M, cacheWrite1H, output Price) Prices {
	return Prices{
		UncachedInput: uncachedInput,
		CacheRead:     cacheRead,
		CacheWrite5M:  cacheWrite5M,
		CacheWrite1H:  cacheWrite1H,
		Output:        output,
	}
}

func unsetPrice() Price {
	return Price{}
}

func nano(value NanoUSD) Price {
	return fixedPrice(value)
}

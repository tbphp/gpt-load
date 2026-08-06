package catalog

import (
	"slices"
	"testing"

	"gpt-load/internal/pricing"
)

func TestAutomaticPriceProviderPriority(t *testing.T) {
	want := []string{
		"openai",
		"anthropic",
		"google",
		"deepseek",
		"moonshotai",
		"moonshotai-cn",
		"zhipuai",
		"zai",
		"alibaba",
		"alibaba-cn",
		"xai",
		"minimax",
		"minimax-cn",
		"mistral",
		"cohere",
		"stepfun-ai",
		"stepfun",
		"perplexity",
		"upstage",
		"xiaomi",
		"meta",
		"llama",
		"groq",
		"cerebras",
		"fireworks-ai",
		"togetherai",
		"deepinfra",
		"siliconflow",
		"siliconflow-cn",
		"nebius",
		"baseten",
		"openrouter",
	}

	if !slices.Equal(automaticPriceProviderPriority, want) {
		t.Fatalf("automatic price provider priority = %q, want %q", automaticPriceProviderPriority, want)
	}
	if automaticPriceProviderPriority[0] != "openai" ||
		automaticPriceProviderPriority[1] != "anthropic" ||
		automaticPriceProviderPriority[2] != "google" {
		t.Fatalf("first automatic price providers = %q, want [openai anthropic google]", automaticPriceProviderPriority[:3])
	}

	seen := make(map[string]struct{}, len(automaticPriceProviderPriority))
	for _, providerID := range automaticPriceProviderPriority {
		if _, duplicate := seen[providerID]; duplicate {
			t.Errorf("automatic price provider %q is duplicated", providerID)
		}
		seen[providerID] = struct{}{}
		if _, err := pricing.ProviderScopeKey(providerID); err != nil {
			t.Errorf("automatic price provider %q has an invalid provider scope: %v", providerID, err)
		}
	}
}

package catalog

import (
	"strings"

	"gpt-load/internal/protocol"
)

// curatedProvider is the maintainer-curated provider directory: services we
// choose to surface in the import picker beyond the three official presets
// and whatever models.dev happens to index. Entries here rank above
// models.dev catalog matches (see ListProviderSuggestions) and remain
// browsable with an empty search query, unlike the models.dev catalog.
//
// An ID may intentionally coincide with a models.dev provider ID (e.g.
// "deepseek"): the curated entry only overrides display metadata and
// priority, while pricing continues to key off the same provider scope and
// can still inherit models.dev cost data.
type curatedProvider struct {
	ID        string
	Name      string
	APIURL    string
	Mark      string
	Protocols []protocol.Protocol
}

// curatedProviders is the static seed table. IDs must be lowercase slugs
// accepted by pricing.ProviderScopeKey, APIURL must already be normalized
// per normalizeSuggestionURL, and Protocols must be non-empty data-plane
// protocols; TestCuratedProvidersSelfCheck enforces this at test time.
var curatedProviders = []curatedProvider{
	{
		ID:        "deepseek",
		Name:      "DeepSeek",
		APIURL:    "https://api.deepseek.com/v1",
		Mark:      "DS",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic},
	},
	{
		ID:        "moonshotai",
		Name:      "Moonshot AI",
		APIURL:    "https://api.moonshot.cn/v1",
		Mark:      "MS",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic},
	},
	{
		ID:        "siliconflow",
		Name:      "SiliconFlow",
		APIURL:    "https://api.siliconflow.cn/v1",
		Mark:      "SF",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions},
	},
	{
		ID:        "zhipuai",
		Name:      "Zhipu AI",
		APIURL:    "https://open.bigmodel.cn/api/paas/v4",
		Mark:      "ZP",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic},
	},
	{
		ID:        "alibaba",
		Name:      "Alibaba Cloud Bailian",
		APIURL:    "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Mark:      "BL",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions},
	},
	{
		ID:        "volcengine",
		Name:      "Volcengine Ark",
		APIURL:    "https://ark.cn-beijing.volces.com/api/v3",
		Mark:      "VE",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions},
	},
	{
		ID:        "openrouter",
		Name:      "OpenRouter",
		APIURL:    "https://openrouter.ai/api/v1",
		Mark:      "OR",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions},
	},
	{
		ID:        "groq",
		Name:      "Groq",
		APIURL:    "https://api.groq.com/openai/v1",
		Mark:      "GQ",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions},
	},
	{
		ID:        "xai",
		Name:      "xAI",
		APIURL:    "https://api.x.ai/v1",
		Mark:      "XA",
		Protocols: []protocol.Protocol{protocol.OpenAICompletions},
	},
}

var curatedProviderIDSet = buildCuratedProviderIDSet()

func buildCuratedProviderIDSet() map[string]struct{} {
	set := make(map[string]struct{}, len(curatedProviders))
	for _, provider := range curatedProviders {
		set[provider.ID] = struct{}{}
	}
	return set
}

// IsCuratedProviderID reports whether providerID is one of the maintainer
// curated presets, distinct from the three official provider IDs.
func IsCuratedProviderID(providerID string) bool {
	_, ok := curatedProviderIDSet[providerID]
	return ok
}

// CuratedProviders returns cloned curated provider entries in fixed
// declaration order. Each call returns independent slice storage so callers
// may freely mutate the result.
func CuratedProviders() []Provider {
	providers := make([]Provider, len(curatedProviders))
	for index, provider := range curatedProviders {
		providers[index] = Provider{
			ID:        provider.ID,
			Name:      provider.Name,
			APIURL:    provider.APIURL,
			Mark:      provider.Mark,
			Protocols: append([]protocol.Protocol(nil), provider.Protocols...),
		}
	}
	return providers
}

// SearchCurated returns curated providers matching query by case-insensitive
// substring match against ID or Name, in fixed declaration order. An empty
// query returns the full table so the curated directory stays browsable
// without typing, unlike the unbounded models.dev catalog.
func SearchCurated(query string) []Provider {
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	all := CuratedProviders()
	if normalizedQuery == "" {
		return all
	}
	matched := make([]Provider, 0, len(all))
	for _, provider := range all {
		if strings.Contains(strings.ToLower(provider.ID), normalizedQuery) ||
			strings.Contains(strings.ToLower(provider.Name), normalizedQuery) {
			matched = append(matched, provider)
		}
	}
	return matched
}

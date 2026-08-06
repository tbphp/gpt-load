package catalog

import (
	"slices"
	"strings"
	"testing"

	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
)

func TestCuratedProvidersSelfCheck(t *testing.T) {
	providers := CuratedProviders()
	if len(providers) == 0 {
		t.Fatal("CuratedProviders() is empty, want at least one curated entry")
	}

	seenIDs := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		if _, err := pricing.ProviderScopeKey(provider.ID); err != nil {
			t.Errorf("curated provider %q: ID is not a valid price scope key: %v", provider.ID, err)
		}
		if IsOfficialProviderID(provider.ID) {
			t.Errorf("curated provider %q collides with an official provider ID", provider.ID)
		}
		if _, duplicate := seenIDs[provider.ID]; duplicate {
			t.Errorf("curated provider ID %q is duplicated", provider.ID)
		}
		seenIDs[provider.ID] = struct{}{}

		if strings.TrimSpace(provider.Name) == "" {
			t.Errorf("curated provider %q: Name must not be blank", provider.ID)
		}
		if strings.TrimSpace(provider.Mark) == "" {
			t.Errorf("curated provider %q: Mark must not be blank", provider.ID)
		}

		if normalized, ok := normalizeSuggestionURL(provider.APIURL); !ok {
			t.Errorf("curated provider %q: APIURL %q is not a valid suggestion URL", provider.ID, provider.APIURL)
		} else if normalized != provider.APIURL {
			t.Errorf("curated provider %q: APIURL %q is not already normalized (want %q)", provider.ID, provider.APIURL, normalized)
		}

		if len(provider.Protocols) == 0 {
			t.Errorf("curated provider %q: Protocols must not be empty", provider.ID)
		}
		seenProtocols := make(map[string]struct{}, len(provider.Protocols))
		for _, proto := range provider.Protocols {
			if !proto.DataPlaneEnabled() {
				t.Errorf("curated provider %q: protocol %q is not data-plane enabled", provider.ID, proto)
			}
			if _, duplicate := seenProtocols[string(proto)]; duplicate {
				t.Errorf("curated provider %q: protocol %q is duplicated", provider.ID, proto)
			}
			seenProtocols[string(proto)] = struct{}{}
		}
	}
}

func TestCuratedProvidersDeclaration(t *testing.T) {
	want := []Provider{
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

	got := CuratedProviders()
	if len(got) != len(want) {
		t.Fatalf("CuratedProviders() returned %d providers, want %d", len(got), len(want))
	}
	for index, wantProvider := range want {
		gotProvider := got[index]
		if gotProvider.ID != wantProvider.ID {
			t.Errorf("provider[%d].ID = %q, want %q", index, gotProvider.ID, wantProvider.ID)
		}
		if gotProvider.Name != wantProvider.Name {
			t.Errorf("provider[%d].Name = %q, want %q", index, gotProvider.Name, wantProvider.Name)
		}
		if gotProvider.APIURL != wantProvider.APIURL {
			t.Errorf("provider[%d].APIURL = %q, want %q", index, gotProvider.APIURL, wantProvider.APIURL)
		}
		if gotProvider.Mark != wantProvider.Mark {
			t.Errorf("provider[%d].Mark = %q, want %q", index, gotProvider.Mark, wantProvider.Mark)
		}
		if !slices.Equal(gotProvider.Protocols, wantProvider.Protocols) {
			t.Errorf("provider[%d].Protocols = %q, want %q", index, gotProvider.Protocols, wantProvider.Protocols)
		}
	}
}

func TestCuratedProvidersUseModelsDevProviderIDs(t *testing.T) {
	wantIDs := map[string]string{
		"Moonshot AI":           "moonshotai",
		"Zhipu AI":              "zhipuai",
		"Alibaba Cloud Bailian": "alibaba",
	}
	found := make(map[string]struct{}, len(wantIDs))
	for _, provider := range CuratedProviders() {
		wantID, tracked := wantIDs[provider.Name]
		if !tracked {
			continue
		}
		found[provider.Name] = struct{}{}
		if provider.ID != wantID {
			t.Errorf("curated provider %q ID = %q, want %q", provider.Name, provider.ID, wantID)
		}
		if _, err := pricing.ProviderScopeKey(wantID); err != nil {
			t.Errorf("models.dev provider ID %q has an invalid price scope: %v", wantID, err)
		}
	}
	for name := range wantIDs {
		if _, ok := found[name]; !ok {
			t.Errorf("curated provider %q is missing", name)
		}
	}
}

func TestVolcengineRemainsCuratedOutsideAutomaticPricePriority(t *testing.T) {
	if !IsCuratedProviderID("volcengine") {
		t.Fatal("IsCuratedProviderID(\"volcengine\") = false, want true")
	}
	if slices.Contains(AutomaticPriceProviderPriority(), "volcengine") {
		t.Error("AutomaticPriceProviderPriority() contains volcengine, want false")
	}
}

func TestCuratedProvidersReturnsIndependentClones(t *testing.T) {
	first := CuratedProviders()
	if len(first) == 0 {
		t.Fatal("CuratedProviders() is empty")
	}
	first[0].Protocols[0] = "mutated"
	second := CuratedProviders()
	if second[0].Protocols[0] == "mutated" {
		t.Fatal("CuratedProviders() shares mutable protocol storage across calls")
	}
}

func TestSearchCuratedEmptyQueryReturnsFullTable(t *testing.T) {
	all := CuratedProviders()
	got := SearchCurated("")
	if len(got) != len(all) {
		t.Fatalf("SearchCurated(\"\") returned %d providers, want full table of %d", len(got), len(all))
	}
}

func TestSearchCuratedMatchesIDOrNameCaseInsensitively(t *testing.T) {
	all := CuratedProviders()
	if len(all) == 0 {
		t.Fatal("CuratedProviders() is empty")
	}
	target := all[0]

	byID := SearchCurated(strings.ToUpper(target.ID))
	if !containsProviderID(byID, target.ID) {
		t.Fatalf("SearchCurated(%q) = %#v, want it to contain %q", strings.ToUpper(target.ID), byID, target.ID)
	}

	byName := SearchCurated(strings.ToUpper(target.Name))
	if !containsProviderID(byName, target.ID) {
		t.Fatalf("SearchCurated(%q) = %#v, want it to contain %q", strings.ToUpper(target.Name), byName, target.ID)
	}

	none := SearchCurated("no-such-curated-provider-xyz")
	if len(none) != 0 {
		t.Fatalf("SearchCurated(no-match) = %#v, want empty", none)
	}
}

func TestIsCuratedProviderID(t *testing.T) {
	for _, provider := range CuratedProviders() {
		if !IsCuratedProviderID(provider.ID) {
			t.Errorf("IsCuratedProviderID(%q) = false, want true", provider.ID)
		}
	}
	if IsCuratedProviderID("openai") {
		t.Error("IsCuratedProviderID(\"openai\") = true, want false (official, not curated)")
	}
	if IsCuratedProviderID("no-such-id") {
		t.Error("IsCuratedProviderID(\"no-such-id\") = true, want false")
	}
}

func containsProviderID(providers []Provider, id string) bool {
	for _, provider := range providers {
		if provider.ID == id {
			return true
		}
	}
	return false
}

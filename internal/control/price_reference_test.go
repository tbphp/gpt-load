package control

import (
	"reflect"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
)

func TestPriceIdentityForChannelModelRequiresRegisteredChannel(t *testing.T) {
	identity, err := PriceIdentityForChannelModel(string(channel.OpenAICompatible), "shared")
	if err != nil || identity != (pricing.Identity{
		ChannelID: string(channel.OpenAICompatible), ModelID: "shared",
	}) {
		t.Fatalf("PriceIdentityForChannelModel() = %#v, %v", identity, err)
	}
	if _, err := PriceIdentityForChannelModel("unknown", "shared"); err == nil {
		t.Fatal("PriceIdentityForChannelModel() accepted an unregistered channel")
	}
}

func TestResolveAutomaticPriceForIdentityUsesOfficialChannelCatalogProvider(t *testing.T) {
	openAICost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	anthropicCost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(2)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"shared": {ID: "shared", Cost: openAICost},
		}},
		"anthropic": {ID: "anthropic", Models: map[string]catalog.Model{
			"shared": {ID: "shared", Cost: anthropicCost},
		}},
	}}

	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.Anthropic), ModelID: "shared",
	})
	if !ok || match.cost != anthropicCost || match.providerID != "anthropic" ||
		match.source != ModelPriceMatchSourceChannelCatalogProvider {
		t.Fatalf("official channel match = %#v, %t", match, ok)
	}
}

func TestResolveAutomaticPriceForIdentityUsesVolcengineOfficialSupplement(t *testing.T) {
	snapshot, err := catalog.MergeOfficial(&catalog.Snapshot{})
	if err != nil {
		t.Fatalf("MergeOfficial() error = %v", err)
	}
	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.Volcengine), ModelID: "doubao-seed-2-1-pro-260628",
	})
	if !ok || match.providerID != "volcengine" ||
		match.source != ModelPriceMatchSourceChannelCatalogProvider || match.cost == nil ||
		!match.cost.Prices.Input.Set ||
		match.cost.Prices.Input.NanoUSDPerMillion != 889_902_497 {
		t.Fatalf("Volcengine official price match = %#v, %t", match, ok)
	}
}

func TestResolveAutomaticPriceForIdentityFallsBackWhenChannelCatalogProviderMisses(t *testing.T) {
	openAICost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"custom-model": {ID: "custom-model", Cost: openAICost},
		}},
	}}

	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.Anthropic), ModelID: "custom-model",
	})
	if !ok || match.cost != openAICost || match.providerID != "openai" ||
		match.source != ModelPriceMatchSourceProviderPriorityFallback {
		t.Fatalf("official channel fallback = %#v, %t", match, ok)
	}
}

func TestResolveAutomaticPriceForIdentityFallsBackForCompatibleChannel(t *testing.T) {
	openAICost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"shared": {ID: "shared", Cost: openAICost},
		}},
	}}

	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.OpenAICompatible), ModelID: "shared",
	})
	if !ok || match.cost != openAICost || match.providerID != "openai" ||
		match.source != ModelPriceMatchSourceProviderPriorityFallback {
		t.Fatalf("compatible channel match = %#v, %t", match, ok)
	}
}

func TestResolveAutomaticPriceForIdentityTreatsCodexOpenAIPriceAsReference(t *testing.T) {
	openAICost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"gpt-codex": {ID: "gpt-codex", Cost: openAICost},
		}},
	}}

	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.Codex), ModelID: "gpt-codex",
	})
	if !ok || match.cost != openAICost || match.providerID != "openai" ||
		match.source != ModelPriceMatchSourceProviderPriorityFallback {
		t.Fatalf("Codex price reference = %#v, %t", match, ok)
	}
}

func TestResolveAutomaticPriceForIdentityTreatsClaudeAnthropicPriceAsReference(t *testing.T) {
	anthropicCost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"anthropic": {ID: "anthropic", Models: map[string]catalog.Model{
			"claude-subscription": {ID: "claude-subscription", Cost: anthropicCost},
		}},
	}}

	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.Claude), ModelID: "claude-subscription",
	})
	if !ok || match.cost != anthropicCost || match.providerID != "anthropic" ||
		match.source != ModelPriceMatchSourceProviderPriorityFallback {
		t.Fatalf("Claude price reference = %#v, %t", match, ok)
	}
}

func TestResolveAutomaticPriceForIdentityTreatsAntigravityGooglePriceAsReference(t *testing.T) {
	googleCost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"google": {ID: "google", Models: map[string]catalog.Model{
			"gemini-antigravity": {ID: "gemini-antigravity", Cost: googleCost},
		}},
	}}

	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.Antigravity), ModelID: "gemini-antigravity",
	})
	if !ok || match.cost != googleCost || match.providerID != "google" ||
		match.source != ModelPriceMatchSourceProviderPriorityFallback {
		t.Fatalf("Antigravity price reference = %#v, %t", match, ok)
	}
}

func TestResolveAutomaticPriceForIdentityTreatsGrokXAIPriceAsReference(t *testing.T) {
	xaiCost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"xai": {ID: "xai", Models: map[string]catalog.Model{
			"grok-4.3": {ID: "grok-4.3", Cost: xaiCost},
		}},
	}}

	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.Grok), ModelID: "grok-4.3",
	})
	if !ok || match.cost != xaiCost || match.providerID != "xai" ||
		match.source != ModelPriceMatchSourceProviderPriorityFallback {
		t.Fatalf("Grok price reference = %#v, %t", match, ok)
	}
}

func TestCompatibleAutomaticPriceUsesGlobalPriority(t *testing.T) {
	openAICost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	alphaCost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(2)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"alpha":  {ID: "alpha", Models: map[string]catalog.Model{"shared": {ID: "shared", Cost: alphaCost}}},
		"openai": {ID: "openai", Models: map[string]catalog.Model{"shared": {ID: "shared", Cost: openAICost}}},
	}}
	want := *snapshot
	want.Providers = make(map[string]catalog.Provider, len(snapshot.Providers))
	for id, provider := range snapshot.Providers {
		want.Providers[id] = provider
	}
	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.OpenAICompatible), ModelID: "shared",
	})
	if !ok || match.cost != openAICost || match.providerID != "openai" ||
		match.source != ModelPriceMatchSourceProviderPriorityFallback {
		t.Fatalf("compatible price = (%p, %q, %q, %t), want openai fallback", match.cost, match.providerID, match.source, ok)
	}
	if !reflect.DeepEqual(snapshot, &want) {
		t.Fatal("global price resolution mutated catalog snapshot")
	}
}

func TestCompatibleAutomaticPriceFallsBackToStableProviderID(t *testing.T) {
	alphaCost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"zeta":  {ID: "zeta", Models: map[string]catalog.Model{"shared": {ID: "shared", Cost: &catalog.ModelCost{}}}},
		"alpha": {ID: "alpha", Models: map[string]catalog.Model{"shared": {ID: "shared", Cost: alphaCost}}},
	}}
	match, ok := resolveAutomaticPriceForIdentity(snapshot, pricing.Identity{
		ChannelID: string(channel.OpenAICompatible), ModelID: "shared",
	})
	if !ok || match.cost != alphaCost || match.providerID != "alpha" ||
		match.source != ModelPriceMatchSourceProviderPriorityFallback {
		t.Fatalf("compatible fallback = (%p, %q, %q, %t)", match.cost, match.providerID, match.source, ok)
	}
}

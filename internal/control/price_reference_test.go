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

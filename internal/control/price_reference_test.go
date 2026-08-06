package control

import (
	"reflect"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/pricing"
)

func TestResolveAutomaticPricePrefersScopeProvider(t *testing.T) {
	scopeCost := automaticPriceTestCost(2)
	snapshot := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
		"openai":     automaticPriceTestCost(1),
		"z-provider": scopeCost,
	})

	cost, providerID, ok := resolveAutomaticPrice(snapshot, "z-provider", "shared-model")
	if !ok || providerID != "z-provider" || cost != scopeCost {
		t.Fatalf("resolveAutomaticPrice() = (%p, %q, %t), want (%p, %q, true)", cost, providerID, ok, scopeCost, "z-provider")
	}
}

func TestResolveAutomaticPriceUsesFixedProviderPriority(t *testing.T) {
	openAICost := automaticPriceTestCost(1)
	snapshot := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
		"anthropic": automaticPriceTestCost(2),
		"openai":    openAICost,
	})

	cost, providerID, ok := resolveAutomaticPrice(snapshot, "", "shared-model")
	if !ok || providerID != "openai" || cost != openAICost {
		t.Fatalf("resolveAutomaticPrice() = (%p, %q, %t), want (%p, %q, true)", cost, providerID, ok, openAICost, "openai")
	}
}

func TestResolveAutomaticPriceUsesAlphabeticalFallback(t *testing.T) {
	alphaCost := automaticPriceTestCost(1)
	snapshot := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
		"zulu":  automaticPriceTestCost(3),
		"alpha": alphaCost,
		"bravo": automaticPriceTestCost(2),
	})

	for range 100 {
		cost, providerID, ok := resolveAutomaticPrice(snapshot, "", "shared-model")
		if !ok || providerID != "alpha" || cost != alphaCost {
			t.Fatalf("resolveAutomaticPrice() = (%p, %q, %t), want (%p, %q, true)", cost, providerID, ok, alphaCost, "alpha")
		}
	}
}

func TestResolveAutomaticPriceSkipsProviderWithoutCost(t *testing.T) {
	anthropicCost := automaticPriceTestCost(2)
	snapshot := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
		"openai":    nil,
		"anthropic": anthropicCost,
	})

	cost, providerID, ok := resolveAutomaticPrice(snapshot, "", "shared-model")
	if !ok || providerID != "anthropic" || cost != anthropicCost {
		t.Fatalf("resolveAutomaticPrice() = (%p, %q, %t), want (%p, %q, true)", cost, providerID, ok, anthropicCost, "anthropic")
	}
}

func TestResolveAutomaticPriceContinuesAfterUnpricedScopeProvider(t *testing.T) {
	openAICost := automaticPriceTestCost(1)
	snapshot := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
		"z-provider": nil,
		"openai":     openAICost,
	})

	cost, providerID, ok := resolveAutomaticPrice(snapshot, "z-provider", "shared-model")
	if !ok || providerID != "openai" || cost != openAICost {
		t.Fatalf("resolveAutomaticPrice() = (%p, %q, %t), want (%p, %q, true)", cost, providerID, ok, openAICost, "openai")
	}
}

func TestResolveAutomaticPriceReturnsNoMatch(t *testing.T) {
	snapshot := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
		"openai": nil,
		"other":  nil,
	})

	cost, providerID, ok := resolveAutomaticPrice(snapshot, "other", "missing-model")
	if cost != nil || providerID != "" || ok {
		t.Fatalf("resolveAutomaticPrice() = (%p, %q, %t), want (nil, \"\", false)", cost, providerID, ok)
	}
}

func TestResolveAutomaticPriceHandlesNilOrEmptySnapshot(t *testing.T) {
	for _, snapshot := range []*catalog.Snapshot{
		nil,
		{},
		{Providers: map[string]catalog.Provider{}},
	} {
		cost, providerID, ok := resolveAutomaticPrice(snapshot, "openai", "shared-model")
		if cost != nil || providerID != "" || ok {
			t.Fatalf("resolveAutomaticPrice(%#v) = (%p, %q, %t), want (nil, \"\", false)", snapshot, cost, providerID, ok)
		}
	}
}

func TestResolveAutomaticPriceMatchesModelIDExactly(t *testing.T) {
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai",
			Models: map[string]catalog.Model{
				"Shared-Model": {ID: "Shared-Model", Cost: automaticPriceTestCost(1)},
			},
		},
	}}

	cost, providerID, ok := resolveAutomaticPrice(snapshot, "openai", "shared-model")
	if cost != nil || providerID != "" || ok {
		t.Fatalf("resolveAutomaticPrice() = (%p, %q, %t), want no case-folded match", cost, providerID, ok)
	}
}

func TestResolveAutomaticPriceDoesNotMutateSnapshot(t *testing.T) {
	cost := automaticPriceTestCost(1)
	snapshot := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{"z-provider": cost})
	want := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{"z-provider": automaticPriceTestCost(1)})

	resolveAutomaticPrice(snapshot, "z-provider", "shared-model")
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("resolveAutomaticPrice() mutated snapshot:\n got %#v\nwant %#v", snapshot, want)
	}
}

func automaticPriceTestSnapshot(costs map[string]*catalog.ModelCost) *catalog.Snapshot {
	providers := make(map[string]catalog.Provider, len(costs))
	for providerID, cost := range costs {
		providers[providerID] = catalog.Provider{
			ID: providerID,
			Models: map[string]catalog.Model{
				"shared-model": {ID: "shared-model", Cost: cost},
			},
		}
	}
	return &catalog.Snapshot{Providers: providers}
}

func automaticPriceTestCost(value pricing.NanoUSD) *catalog.ModelCost {
	return &catalog.ModelCost{Prices: pricing.Prices{
		Input: pricing.Price{NanoUSDPerMillion: value, Set: true},
	}}
}

func TestResolveAutomaticPriceRejectsEmptyModelID(t *testing.T) {
	snapshot := automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
		"openai": automaticPriceTestCost(1),
	})

	cost, providerID, ok := resolveAutomaticPrice(snapshot, "openai", "")
	if cost != nil || providerID != "" || ok {
		t.Fatalf("resolveAutomaticPrice() = (%p, %q, %t), want (nil, \"\", false)", cost, providerID, ok)
	}
}

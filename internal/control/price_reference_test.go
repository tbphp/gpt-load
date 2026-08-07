package control

import (
	"reflect"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/pricing"
)

func TestResolveAutomaticPriceUsesGlobalPriorityNotRouteProvider(t *testing.T) {
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
	cost, providerID, ok := resolveAutomaticPrice(snapshot, "shared")
	if !ok || cost != openAICost || providerID != "openai" {
		t.Fatalf("global price = (%p, %q, %t), want openai", cost, providerID, ok)
	}
	if !reflect.DeepEqual(snapshot, &want) {
		t.Fatal("global price resolution mutated catalog snapshot")
	}
}

func TestResolveAutomaticPriceFallsBackToStableProviderID(t *testing.T) {
	alphaCost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"zeta":  {ID: "zeta", Models: map[string]catalog.Model{"shared": {ID: "shared", Cost: &catalog.ModelCost{}}}},
		"alpha": {ID: "alpha", Models: map[string]catalog.Model{"shared": {ID: "shared", Cost: alphaCost}}},
	}}
	cost, providerID, ok := resolveAutomaticPrice(snapshot, "shared")
	if !ok || cost != alphaCost || providerID != "alpha" {
		t.Fatalf("global fallback = (%p, %q, %t)", cost, providerID, ok)
	}
}

package catalog

import (
	"testing"
)

func TestOfficialSnapshotProvidesRepresentableVolcengineArkPrices(t *testing.T) {
	snapshot, err := OfficialSnapshot()
	if err != nil {
		t.Fatalf("OfficialSnapshot() error = %v", err)
	}
	provider, ok := snapshot.Providers["volcengine"]
	if !ok || provider.ID != "volcengine" || provider.Name != "Volcengine Ark" {
		t.Fatalf("Volcengine provider = %#v", provider)
	}
	if len(provider.Models) != 17 {
		t.Fatalf("Volcengine model count = %d, want 17", len(provider.Models))
	}

	pro := provider.Models["doubao-seed-2-0-pro-260215"]
	if pro.Cost == nil {
		t.Fatal("Doubao Seed 2.0 Pro cost = nil")
	}
	assertPrice(t, "Doubao base input", pro.Cost.Prices.Input, 474_614_665, true)
	assertPrice(t, "Doubao base output", pro.Cost.Prices.Output, 2_373_073_326, true)
	assertPrice(t, "Doubao base cache read", pro.Cost.Prices.CacheRead, 94_922_933, true)
	if len(pro.Cost.ContextTiers) != 2 {
		t.Fatalf("Doubao tiers = %#v", pro.Cost.ContextTiers)
	}
	assertPrice(t, "Doubao 32k tier input", pro.Cost.ContextTiers[0].Prices.Input, 711_921_998, true)
	assertPrice(t, "Doubao 128k tier output", pro.Cost.ContextTiers[1].Prices.Output, 7_119_219_977, true)

	flash := provider.Models["deepseek-v4-flash-ga-260731"]
	if flash.Cost == nil {
		t.Fatal("DeepSeek V4 Flash GA cost = nil")
	}
	assertPrice(t, "DeepSeek Flash adjusted input", flash.Cost.Prices.Input, 444_951_249, true)
	assertPrice(t, "DeepSeek Flash adjusted output", flash.Cost.Prices.Output, 1_334_853_746, true)
	assertPrice(t, "DeepSeek Flash adjusted cache read", flash.Cost.Prices.CacheRead, 14_831_708, true)

	// These official schedules depend on output length as well as input length,
	// which GPT-Load's current Models.dev-compatible tier contract cannot encode.
	for _, modelID := range []string{"doubao-seed-1-8-251228", "glm-4-7-251222"} {
		if _, exists := provider.Models[modelID]; exists {
			t.Fatalf("non-representable model %q was included", modelID)
		}
	}
}

func TestOfficialSnapshotReturnsIndependentCopies(t *testing.T) {
	first, err := OfficialSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := OfficialSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	provider := first.Providers["volcengine"]
	delete(provider.Models, "doubao-seed-evolving")
	first.Providers["volcengine"] = provider
	if _, exists := second.Providers["volcengine"].Models["doubao-seed-evolving"]; !exists {
		t.Fatal("OfficialSnapshot() returned shared model maps")
	}
}

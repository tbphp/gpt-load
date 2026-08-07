package control

import (
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestReconcileReferencedPricesUsesOneGlobalIdentity(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider", ProviderID: &providerID, UpstreamURL: "https://provider.example/v1",
		Protocols: models.JSON(`["openai-completions"]`), Models: models.JSON(`[{"id":"shared","alias":"a"}]`), Config: models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "custom", UpstreamURL: "https://custom.example/v1",
		Protocols: models.JSON(`["openai-completions"]`), Models: models.JSON(`[{"id":"shared","alias":"b"}]`), Config: models.JSON(`{}`), Enabled: true,
	})
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"shared": {ID: "shared", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(2_000_000_000)}}},
		}},
	}}
	if err := fixture.service.withControlTransaction(t.Context(), func(tx *gorm.DB) error {
		return reconcileReferencedPrices(tx, snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	var rows []models.ModelPrice
	if err := fixture.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ModelID != "shared" || rows[0].InputPriceNanoUSDPerMillionTokens == nil || *rows[0].InputPriceNanoUSDPerMillionTokens != 2_000_000_000 {
		t.Fatalf("reconciled rows = %#v", rows)
	}
	if _, ok := mustLoadPriceTable(t, fixture.db).Lookup(pricing.Identity{ModelID: "shared"}); !ok {
		t.Fatal("global runtime table did not contain shared model")
	}
}

func createPriceTestGroup(t *testing.T, db *gorm.DB, group models.Group) models.Group {
	t.Helper()
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create price test group: %v", err)
	}
	return group
}

func priceTestValue(value pricing.NanoUSD) pricing.Price {
	return pricing.Price{NanoUSDPerMillion: value, Set: true}
}

func priceTestRowHasValue(row models.ModelPrice) bool {
	return row.InputPriceNanoUSDPerMillionTokens != nil || row.OutputPriceNanoUSDPerMillionTokens != nil || row.CacheReadPriceNanoUSDPerMillionTokens != nil || row.CacheWritePriceNanoUSDPerMillionTokens != nil || len(row.ContextPriceTiers) != 0
}

func mustLoadPriceTable(t *testing.T, db *gorm.DB) *pricing.Table {
	t.Helper()
	table, err := loadPriceTable(t.Context(), db)
	if err != nil {
		t.Fatalf("loadPriceTable() error = %v", err)
	}
	return table
}

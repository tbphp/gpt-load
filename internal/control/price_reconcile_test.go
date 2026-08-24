package control

import (
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

func TestReconcileReferencedPricesUsesChannelModelIdentity(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "openai", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"shared","alias":"a"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "anthropic", ChannelID: string(channel.Anthropic), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"shared","alias":"b"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"shared": {ID: "shared", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(2_000_000_000)}}},
		}},
		"anthropic": {ID: "anthropic", Models: map[string]catalog.Model{
			"shared": {ID: "shared", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(3_000_000_000)}}},
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
	if len(rows) != 2 {
		t.Fatalf("reconciled rows = %#v", rows)
	}
	byChannel := make(map[string]models.ModelPrice, len(rows))
	for _, row := range rows {
		byChannel[row.ChannelID] = row
	}
	if row := byChannel[string(channel.OpenAI)]; row.InputPriceNanoUSDPerMillionTokens == nil ||
		*row.InputPriceNanoUSDPerMillionTokens != 2_000_000_000 {
		t.Fatalf("OpenAI price = %#v", row)
	}
	if row := byChannel[string(channel.Anthropic)]; row.InputPriceNanoUSDPerMillionTokens == nil ||
		*row.InputPriceNanoUSDPerMillionTokens != 3_000_000_000 {
		t.Fatalf("Anthropic price = %#v", row)
	}
	table := mustLoadPriceTable(t, fixture.db)
	for _, identity := range []pricing.Identity{
		{ChannelID: string(channel.OpenAI), ModelID: "shared"},
		{ChannelID: string(channel.Anthropic), ModelID: "shared"},
	} {
		if _, ok := table.Lookup(identity); !ok {
			t.Fatalf("runtime table does not contain %#v", identity)
		}
	}
}

func TestLoadPriceTableDoesNotMergeModelsDevModePricesIntoManualRule(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	standard := int64(2)
	if err := fixture.db.Create(&models.ModelPrice{
		ChannelID: string(channel.OpenAI), ModelID: "gpt-fast",
		InputPriceNanoUSDPerMillionTokens: &standard,
		IsManual:                          true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"gpt-fast": {ID: "gpt-fast", Cost: &catalog.ModelCost{
				ModePrices: map[pricing.Mode]pricing.Prices{
					pricing.ModeFast: {Input: priceTestValue(7)},
				},
			}},
		}},
	}}
	fixture.catalogRuntime.Publish(snapshot)

	table, err := loadPriceTable(t.Context(), fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	identity := pricing.Identity{ChannelID: string(channel.OpenAI), ModelID: "gpt-fast"}
	quote := table.QuoteForMode(identity, usage.Result{
		Tokens: usage.Tokens{UncachedInput: 1_000_000},
		State:  usage.StateComplete,
	}, pricing.ModeFast)
	if quote.EstimatedCostNanoUSD != 2 {
		t.Fatalf("fast quote = %#v, want persisted manual standard fallback", quote)
	}
}

func createPriceTestGroup(t *testing.T, db *gorm.DB, group models.Group) models.Group {
	t.Helper()
	if group.ChannelID == "" {
		group.ChannelID = string(channel.OpenAICompatible)
	}
	if len(group.Params) == 0 {
		group.Params = models.JSON(`{"base_url":"https://` + group.Name + `.example/v1"}`)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create price test group: %v", err)
	}
	return group
}

func priceTestValue(value pricing.NanoUSD) pricing.Price {
	return pricing.Price{NanoUSDPerMillion: value, Set: true}
}

func priceTestRowHasValue(row models.ModelPrice) bool {
	return row.InputPriceNanoUSDPerMillionTokens != nil || row.OutputPriceNanoUSDPerMillionTokens != nil || row.CacheReadPriceNanoUSDPerMillionTokens != nil || row.CacheWritePriceNanoUSDPerMillionTokens != nil || len(row.ContextPriceTiers) != 0 || len(row.ModePriceSchedules) != 0
}

func mustLoadPriceTable(t *testing.T, db *gorm.DB) *pricing.Table {
	t.Helper()
	table, err := loadPriceTable(t.Context(), db)
	if err != nil {
		t.Fatalf("loadPriceTable() error = %v", err)
	}
	return table
}

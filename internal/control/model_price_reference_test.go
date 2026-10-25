package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func referencedCatalogFixture(t *testing.T, fixture serviceFixture, row models.ModelPrice) {
	t.Helper()
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "reference-group", ChannelID: row.ChannelID,
		Params:    models.JSON(`{"base_url":"https://price.example/v1"}`),
		Models:    models.JSON(fmt.Sprintf(`[{"id":%q}]`, row.ModelID)),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	if err := fixture.db.Model(&row).Updates(map[string]any{
		"reference_provider_id": "google", "reference_model_id": "source-model", "updated_at_ms": 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func referenceCatalog(input int64) *catalog.Snapshot {
	return &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"google": {ID: "google", Name: "Google", Models: map[string]catalog.Model{
			"source-model": {ID: "source-model", Name: "Reference model", Cost: &catalog.ModelCost{
				Prices:       pricing.Prices{Input: priceTestValue(pricing.NanoUSD(input))},
				ContextTiers: []pricing.ContextTier{{InputThresholdTokens: 1000, Prices: pricing.Prices{Input: priceTestValue(pricing.NanoUSD(input * 2))}}},
				ModePrices: map[pricing.Mode]pricing.Prices{
					pricing.ModeFast:      {Input: priceTestValue(pricing.NanoUSD(input * 3))},
					pricing.ModeUltrafast: {Input: priceTestValue(pricing.NanoUSD(input * 4))},
				},
			}},
		}},
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"gpt-wire": {ID: "gpt-wire", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(9_000_000_000)}}},
		}},
	}}
}

func TestCatalogPriceReferenceSyncAndOfflineStartup(t *testing.T) {
	t.Parallel()
	fixture, _, row := newModelPriceHTTPFixture(t, true)
	referencedCatalogFixture(t, fixture, row)
	identity := pricing.Identity{ChannelID: row.ChannelID, ModelID: row.ModelID}
	oldTable, err := loadPriceTable(t.Context(), fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []int64{3_000_000_000, 5_000_000_000} {
		if err := fixture.service.applyCatalogSnapshot(t.Context(), referenceCatalog(input)); err != nil {
			t.Fatal(err)
		}
		var saved models.ModelPrice
		if err := fixture.db.First(&saved, row.ID).Error; err != nil {
			t.Fatal(err)
		}
		if saved.InputPriceNanoUSDPerMillionTokens == nil || *saved.InputPriceNanoUSDPerMillionTokens != input || saved.IsManual || saved.UpdatedAtMS <= 1 {
			t.Fatalf("directory reference did not follow its exact source: %#v", saved)
		}
		rule, ok := fixture.priceRuntime.Load().Lookup(identity)
		if !ok || int64(rule.Prices.Input.NanoUSDPerMillion) != input || len(rule.ContextTiers) != 1 || len(rule.ModeSchedules) != 2 {
			t.Fatalf("incomplete referenced runtime rule: %#v", rule)
		}
		// 缺少目录的启动不能抹掉已经保存的关联价。
		fixture.catalogRuntime.Publish(&catalog.Snapshot{})
		if err := fixture.service.EnsureInitialState(t.Context()); err != nil {
			t.Fatal(err)
		}
		rule, _ = fixture.priceRuntime.Load().Lookup(identity)
		if int64(rule.Prices.Input.NanoUSDPerMillion) != input {
			t.Fatalf("offline startup lost referenced price: %#v", rule)
		}
		listed, err := fixture.service.ListModelPrices(t.Context(), ModelPriceListQuery{Usage: ModelPriceUsageInUse, Status: ModelPriceStatusAll, Page: 1, PageSize: 20})
		if err != nil || len(listed.Items) != 1 {
			t.Fatalf("list referenced price: %#v, %v", listed, err)
		}
		encoded, _ := json.Marshal(listed.Items[0])
		var wire map[string]any
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		ref, ok := wire["catalog_reference"].(map[string]any)
		if wire["method"] != "catalog_reference" || wire["pricing_status"] != "configured" || !ok || ref["available"] != false || wire["can_reset"] != true {
			t.Fatalf("offline reference presentation = %s", encoded)
		}
	}
	oldRule, _ := oldTable.Lookup(identity)
	if oldRule.Prices.Input.NanoUSDPerMillion != 2_500_000_000 {
		t.Fatal("published updates changed an existing request snapshot")
	}
}

func TestCatalogPriceReferenceClearedByManualSaveAndReset(t *testing.T) {
	t.Parallel()
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprintf("reset=%t", reset), func(t *testing.T) {
			fixture, engine, row := newModelPriceHTTPFixture(t, true)
			referencedCatalogFixture(t, fixture, row)
			fixture.catalogRuntime.Publish(referenceCatalog(3_000_000_000))
			method, path, body := http.MethodPut, fmt.Sprintf("/api/model-prices/%d", row.ID), modelPriceHTTPUpdateBody(`"4"`, "false")
			if reset {
				method, path, body = http.MethodPost, path+"/reset", `{}`
			}
			response := serveModelPriceHTTPRequest(engine, method, path, body, authTestKey)
			if response.Code != http.StatusOK {
				t.Fatalf("detach status = %d: %s", response.Code, response.Body.String())
			}
			var saved models.ModelPrice
			if err := fixture.db.First(&saved, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if saved.ReferenceProviderID != "" || saved.ReferenceModelID != "" || saved.IsManual == reset || saved.ChannelID != string(channel.OpenAICompatible) || saved.ModelID != "gpt-wire" {
				t.Fatalf("detach failed: %#v", saved)
			}
		})
	}
}

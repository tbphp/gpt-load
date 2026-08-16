package control

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestChannelModelPriceIsSharedAcrossGroupsAndAliases(t *testing.T) {
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "first", ChannelID: string(channel.OpenAICompatible),
		Params:    models.JSON(`{"base_url":"https://first.example/v1"}`),
		Models:    models.JSON(`[{"id":"shared-upstream","alias":"client-a"},{"id":"shared-upstream","alias":"client-b"}]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "second", ChannelID: string(channel.OpenAICompatible),
		Params:    models.JSON(`{"base_url":"https://second.example/v1"}`),
		Models:    models.JSON(`[{"id":"shared-upstream","alias":"client-a"}]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})
	mustEnsureInitialPrices(t, fixture)

	var rows []models.ModelPrice
	if err := fixture.db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ChannelID != string(channel.OpenAICompatible) || rows[0].ModelID != "shared-upstream" {
		t.Fatalf("persisted prices = %#v, want one shared upstream price", rows)
	}

	listed, err := fixture.service.ListModelPrices(t.Context(), ModelPriceListQuery{
		Usage: ModelPriceUsageInUse, Status: ModelPriceStatusAll, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ReferenceCount != 3 || listed.Items[0].ReferenceGroupCount != 2 {
		t.Fatalf("listed prices = %#v", listed)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	item := wire["items"].([]any)[0].(map[string]any)
	if item["channel_id"] != string(channel.OpenAICompatible) {
		t.Fatalf("price response channel_id = %#v", item["channel_id"])
	}
	if _, exists := item["scope"]; exists {
		t.Fatalf("global price response leaked scope: %s", encoded)
	}
}

func TestProjectModelPriceRowDoesNotExposeSlotCompleteness(t *testing.T) {
	value := int64(1)
	record, err := projectModelPriceRow(models.ModelPrice{
		ID: 1, ChannelID: string(channel.OpenAICompatible), ModelID: "tiered", InputPriceNanoUSDPerMillionTokens: &value,
		ContextPriceTiers: models.JSON(`[{"threshold_tokens":1000,"input_price_nano_usd_per_million_tokens":1,"output_price_nano_usd_per_million_tokens":null,"cache_read_price_nano_usd_per_million_tokens":null,"cache_write_price_nano_usd_per_million_tokens":null}]`),
	}, priceReferenceSnapshot{references: map[pricing.Identity]referencedPrice{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record.dto)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["partial"]; exists {
		t.Fatalf("model price response exposed slot completeness: %s", encoded)
	}
}

func TestProjectModelPriceRowExposesModelsDevFastPrices(t *testing.T) {
	standardInput := int64(3)
	record, err := projectModelPriceRow(models.ModelPrice{
		ID: 1, ChannelID: string(channel.OpenAI), ModelID: "gpt-fast",
		InputPriceNanoUSDPerMillionTokens: &standardInput,
	}, priceReferenceSnapshot{references: map[pricing.Identity]referencedPrice{}}, &catalog.Snapshot{
		Providers: map[string]catalog.Provider{
			"openai": {
				ID: "openai",
				Models: map[string]catalog.Model{
					"gpt-fast": {
						ID: "gpt-fast",
						Cost: &catalog.ModelCost{ModePrices: map[pricing.Mode]pricing.Prices{
							pricing.ModeFast: {
								Input:  priceTestValue(7),
								Output: priceTestValue(11),
							},
						}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record.dto)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	modes, ok := wire["mode_prices"].(map[string]any)
	if !ok {
		t.Fatalf("model price response mode_prices = %#v", wire["mode_prices"])
	}
	fast, ok := modes[string(pricing.ModeFast)].(map[string]any)
	if !ok || fast["input"] != "0.000000007" || fast["output"] != "0.000000011" ||
		fast["cache_read"] != nil || fast["cache_write"] != nil {
		t.Fatalf("model price response fast prices = %#v", modes[string(pricing.ModeFast)])
	}
}

func TestResetModelPriceChangesOnlySelectedChannelIdentity(t *testing.T) {
	fixture := newServiceFixture(t)
	manual := int64(99)
	rows := []models.ModelPrice{
		{
			ChannelID: string(channel.OpenAI), ModelID: "shared",
			InputPriceNanoUSDPerMillionTokens: &manual, IsManual: true,
		},
		{
			ChannelID: string(channel.Anthropic), ModelID: "shared",
			InputPriceNanoUSDPerMillionTokens: &manual, IsManual: true,
		},
	}
	for index := range rows {
		if err := fixture.db.Create(&rows[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"shared": {ID: "shared", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(10)}}},
		}},
		"anthropic": {ID: "anthropic", Models: map[string]catalog.Model{
			"shared": {ID: "shared", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(20)}}},
		}},
	}})

	reset, err := fixture.service.ResetModelPrice(t.Context(), rows[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.ChannelID != string(channel.Anthropic) || reset.Prices.Input == nil ||
		*reset.Prices.Input != "0.00000002" || reset.MatchSource == nil ||
		*reset.MatchSource != ModelPriceMatchSourceChannelCatalogProvider ||
		reset.MatchedProviderID == nil || *reset.MatchedProviderID != "anthropic" {
		t.Fatalf("reset Anthropic price = %#v", reset)
	}
	var openAI models.ModelPrice
	if err := fixture.db.First(&openAI, rows[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if !openAI.IsManual || openAI.InputPriceNanoUSDPerMillionTokens == nil ||
		*openAI.InputPriceNanoUSDPerMillionTokens != 99 {
		t.Fatalf("OpenAI price changed with Anthropic reset: %#v", openAI)
	}
	table := fixture.priceRuntime.Load()
	for _, expected := range []struct {
		identity pricing.Identity
		value    pricing.NanoUSD
	}{
		{identity: pricing.Identity{ChannelID: string(channel.OpenAI), ModelID: "shared"}, value: 99},
		{identity: pricing.Identity{ChannelID: string(channel.Anthropic), ModelID: "shared"}, value: 20},
	} {
		rule, ok := table.Lookup(expected.identity)
		if !ok || !rule.Prices.Input.Set || rule.Prices.Input.NanoUSDPerMillion != expected.value {
			t.Fatalf("runtime rule %#v = %#v, %t", expected.identity, rule, ok)
		}
	}
}

func TestDeleteModelPriceIgnoresSameModelReferenceFromDifferentChannel(t *testing.T) {
	fixture := newServiceFixture(t)
	manual := int64(1)
	row := models.ModelPrice{
		ChannelID: string(channel.OpenAI), ModelID: "shared",
		InputPriceNanoUSDPerMillionTokens: &manual, IsManual: true,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "anthropic", ChannelID: string(channel.Anthropic), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"shared"}]`), Enabled: true,
	})

	if err := fixture.service.DeleteModelPrice(t.Context(), row.ID); err != nil {
		t.Fatalf("DeleteModelPrice() error = %v", err)
	}
	var count int64
	if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", row.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deleted OpenAI price count = %d", count)
	}
}

func mustModelPriceUpdateRequest(t *testing.T, body string) ModelPriceUpdateRequest {
	t.Helper()
	var request ModelPriceUpdateRequest
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatalf("decode model price request: %v", err)
	}
	if err := request.validate(); err != nil {
		t.Fatalf("validate model price request: %v", err)
	}
	return request
}

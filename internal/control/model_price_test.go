package control

import (
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestGlobalModelPriceIsSharedAcrossGroupsAndAliases(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "first", ProviderID: &providerID, UpstreamURL: "https://first.example/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models:    models.JSON(`[{"id":"shared-upstream","alias":"client-a"},{"id":"shared-upstream","alias":"client-b"}]`),
		Config:    models.JSON(`{}`), Enabled: true,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "second", ProviderID: &providerID, UpstreamURL: "https://second.example/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models:    models.JSON(`[{"id":"shared-upstream","alias":"client-a"}]`),
		Config:    models.JSON(`{}`), Enabled: true,
	})
	mustEnsureInitialPrices(t, fixture)

	var rows []models.ModelPrice
	if err := fixture.db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ModelID != "shared-upstream" {
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
	if _, exists := item["scope"]; exists {
		t.Fatalf("global price response leaked scope: %s", encoded)
	}
}

func TestModelPriceVersionIsMonotonicWithinOneMillisecond(t *testing.T) {
	fixture := newServiceFixture(t)
	value := int64(1_000_000_000)
	row := models.ModelPrice{ModelID: "versioned", InputPriceNanoUSDPerMillionTokens: &value, UpdatedAtMS: 100}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.now = func() time.Time { return time.UnixMilli(100) }
	request := mustModelPriceUpdateRequest(t, `{"input":"2","output":null,"cache_read":null,"cache_write":null,"context_tiers":[],"confirm_unpriced":false}`)
	updated, err := fixture.service.UpdateModelPriceIfCurrent(t.Context(), row.ID, request, &row.UpdatedAtMS)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UpdatedAtMS != 101 {
		t.Fatalf("updated version = %d, want 101", updated.UpdatedAtMS)
	}
	if _, err := fixture.service.UpdateModelPriceIfCurrent(t.Context(), row.ID, request, &row.UpdatedAtMS); err == nil {
		t.Fatal("stale same-millisecond update succeeded")
	}
}

func TestProjectModelPriceRowMarksIncompleteTierPartial(t *testing.T) {
	value := int64(1)
	record, err := projectModelPriceRow(models.ModelPrice{
		ID: 1, ModelID: "tiered", InputPriceNanoUSDPerMillionTokens: &value,
		ContextPriceTiers: models.JSON(`[{"threshold_tokens":1000,"input_price_nano_usd_per_million_tokens":1,"output_price_nano_usd_per_million_tokens":null,"cache_read_price_nano_usd_per_million_tokens":null,"cache_write_price_nano_usd_per_million_tokens":null}]`),
	}, priceReferenceSnapshot{references: map[pricing.Identity]referencedPrice{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !record.dto.Partial {
		t.Fatal("incomplete tier was not marked partial")
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

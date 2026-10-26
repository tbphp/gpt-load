package control

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func priceReferenceFixture(t *testing.T, fixture serviceFixture, count int) ([]models.ModelPrice, []models.Group) {
	t.Helper()
	rows := make([]models.ModelPrice, 0, count)
	groups := make([]models.Group, 0, count)
	prefix := fmt.Sprintf("price-follow-%d", time.Now().UnixNano())
	for index := range count {
		input := int64(2_000_000_000)
		row := models.ModelPrice{ChannelID: string(channel.OpenAICompatible), ModelID: fmt.Sprintf("%s-%d", prefix, index), IsManual: true, InputPriceNanoUSDPerMillionTokens: &input}
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		group := createPriceTestGroup(t, fixture.db, models.Group{
			Name: row.ModelID, ChannelID: row.ChannelID, Enabled: true, Overrides: models.JSON(`{}`),
			Models: models.JSON(fmt.Sprintf(`[{"id":%q}]`, row.ModelID)),
		})
		rows = append(rows, row)
		groups = append(groups, group)
	}
	fixture.priceRuntime.Publish(mustLoadPriceTable(t, fixture.db))
	return rows, groups
}

func followPrice(t *testing.T, fixture serviceFixture, target, source uint) {
	t.Helper()
	if _, err := fixture.service.BatchModelPrices(t.Context(), ModelPriceBatchRequest{
		IDs: []uint{target}, Mode: "link", Reference: &ModelPriceReferenceRequest{PriceID: source},
	}); err != nil {
		t.Fatal(err)
	}
}

func assertFollowedPrice(t *testing.T, fixture serviceFixture, row models.ModelPrice, value *int64) models.ModelPrice {
	t.Helper()
	var saved models.ModelPrice
	if err := fixture.db.First(&saved, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !pricePointerEqual(saved.InputPriceNanoUSDPerMillionTokens, value) {
		t.Fatalf("persisted downstream price = %#v, want %v", saved.InputPriceNanoUSDPerMillionTokens, value)
	}
	rule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{ChannelID: row.ChannelID, ModelID: row.ModelID})
	if !exists || rule.Prices.Input.Set != (value != nil) || value != nil && int64(rule.Prices.Input.NanoUSDPerMillion) != *value {
		t.Fatalf("published downstream price differs: %#v", rule)
	}
	return saved
}

func testModelPriceFollowChain(t *testing.T, fixture serviceFixture) {
	t.Helper()
	rows, _ := priceReferenceFixture(t, fixture, 3)
	followPrice(t, fixture, rows[1].ID, rows[0].ID)
	followPrice(t, fixture, rows[2].ID, rows[1].ID)
	request := mustModelPriceUpdateRequest(t, `{"input":"5","output":"0","cache_read":"0.5","cache_write":"6","context_tiers":[{"threshold_tokens":1000,"input":"10","output":null,"cache_read":null,"cache_write":null}],"mode_schedules":{"fast":{"prices":{"input":"15","output":null,"cache_read":null,"cache_write":null},"context_tiers":[]},"ultrafast":{"prices":{"input":"20","output":null,"cache_read":null,"cache_write":null},"context_tiers":[]}}}`)
	if _, err := fixture.service.UpdateModelPrice(t.Context(), rows[0].ID, request); err != nil {
		t.Fatal(err)
	}
	input := int64(5_000_000_000)
	for _, row := range rows {
		saved := assertFollowedPrice(t, fixture, row, &input)
		if len(saved.ModePriceSchedules) == 0 || len(saved.ContextPriceTiers) == 0 {
			t.Fatal("follow lost mode schedules or tiers")
		}
	}
	clear := mustModelPriceUpdateRequest(t, `{"input":null,"output":null,"cache_read":null,"cache_write":null,"context_tiers":[],"mode_schedules":{},"confirm_unpriced":true}`)
	if _, err := fixture.service.UpdateModelPrice(t.Context(), rows[0].ID, clear); err != nil {
		t.Fatal(err)
	}
	assertFollowedPrice(t, fixture, rows[2], nil)
	if _, err := fixture.service.UpdateModelPrice(t.Context(), rows[1].ID, request); err != nil {
		t.Fatal(err)
	}
	middle := assertFollowedPrice(t, fixture, rows[1], &input)
	last := assertFollowedPrice(t, fixture, rows[2], &input)
	if middle.SourcePriceID != nil || last.SourcePriceID == nil || *last.SourcePriceID != rows[1].ID {
		t.Fatal("manual edit did not detach only the selected model")
	}
}

func TestModelPriceFollowChain(t *testing.T) {
	t.Parallel()
	testModelPriceFollowChain(t, newServiceFixture(t))
}

func TestExternalModelPriceFollowChain(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testModelPriceFollowChain(t, newServiceFixtureWithDSN(t, dsn))
}

func TestModelPriceReferenceRejectsCyclesAtomically(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	rows, _ := priceReferenceFixture(t, fixture, 3)
	followPrice(t, fixture, rows[1].ID, rows[0].ID)
	before := fixture.priceRuntime.Load()
	for _, ids := range [][]uint{{rows[1].ID}, {rows[0].ID, rows[2].ID}} {
		_, err := fixture.service.BatchModelPrices(t.Context(), ModelPriceBatchRequest{IDs: ids, Mode: "link", Reference: &ModelPriceReferenceRequest{PriceID: rows[1].ID}})
		if !errors.Is(err, app_errors.ErrModelPriceReferenceCycle) {
			t.Fatalf("cycle error = %v", err)
		}
	}
	var untouched models.ModelPrice
	if err := fixture.db.First(&untouched, rows[2].ID).Error; err != nil || !untouched.IsManual || untouched.SourcePriceID != nil {
		t.Fatalf("cycle partially changed the batch: %#v, %v", untouched, err)
	}
	if fixture.priceRuntime.Load() != before {
		t.Fatal("cycle published an invalid table")
	}
}

func TestModelPriceReferenceProtectsLastGroup(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	rows, groups := priceReferenceFixture(t, fixture, 2)
	followPrice(t, fixture, rows[1].ID, rows[0].ID)
	var apiErr *app_errors.APIError
	if err := fixture.service.DeleteGroup(t.Context(), groups[0].ID); !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrModelPriceSourceReferenced.Code {
		t.Fatalf("deleting the last source group: %v", err)
	}
	other := createPriceTestGroup(t, fixture.db, models.Group{Name: rows[0].ModelID + "-other", ChannelID: rows[0].ChannelID, Enabled: true, Overrides: models.JSON(`{}`), Models: groups[0].Models})
	if err := fixture.service.DeleteGroup(t.Context(), groups[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.DeleteGroup(t.Context(), other.ID); !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrModelPriceSourceReferenced.Code {
		t.Fatalf("last remaining group was not protected: %v", err)
	}
	if err := fixture.service.DeleteGroup(t.Context(), groups[1].ID); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", rows[1].ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("removed target left a hidden link: count=%d, err=%v", count, err)
	}
	if err := fixture.service.DeleteGroup(t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
}

func TestModelPriceReferenceFollowsCatalogAndOfflineStartup(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	rows, _ := priceReferenceFixture(t, fixture, 2)
	if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", rows[0].ID).Update("is_manual", false).Error; err != nil {
		t.Fatal(err)
	}
	followPrice(t, fixture, rows[1].ID, rows[0].ID)
	input := int64(7_000_000_000)
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{"openai": {ID: "openai", Name: "OpenAI", Models: map[string]catalog.Model{
		rows[0].ModelID: {ID: rows[0].ModelID, Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(pricing.NanoUSD(input))}}},
	}}}}
	if err := fixture.service.applyCatalogSnapshot(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	assertFollowedPrice(t, fixture, rows[1], &input)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{})
	if err := fixture.service.EnsureInitialState(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertFollowedPrice(t, fixture, rows[0], &input)
	assertFollowedPrice(t, fixture, rows[1], &input)
}

func TestModelPriceSourcesOnlyGroupedModelsWithoutTruncation(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	rows, _ := priceReferenceFixture(t, fixture, 55)
	unused := models.ModelPrice{ChannelID: rows[0].ChannelID, ModelID: "ungrouped", IsManual: true}
	if err := fixture.db.Create(&unused).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.ModelPriceSources(t.Context())
	if err != nil || len(result.Items) != len(rows) {
		t.Fatalf("grouped sources = %d, want %d, err=%v", len(result.Items), len(rows), err)
	}
	for _, item := range result.Items {
		if len(item.Groups) == 0 || item.Price.ID == unused.ID {
			t.Fatalf("source is not a grouped model: %#v", item)
		}
	}
	_, err = fixture.service.BatchModelPrices(t.Context(), ModelPriceBatchRequest{IDs: []uint{rows[0].ID}, Mode: "link", Reference: &ModelPriceReferenceRequest{PriceID: unused.ID}})
	if !errors.Is(err, app_errors.ErrModelPriceSourceUnavailable) {
		t.Fatalf("ungrouped source accepted: %v", err)
	}
}

func TestModelPriceBatchManualCopyDetachesAndPreservesZero(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	rows, _ := priceReferenceFixture(t, fixture, 3)
	followPrice(t, fixture, rows[1].ID, rows[0].ID)
	followPrice(t, fixture, rows[2].ID, rows[1].ID)
	request := mustModelPriceUpdateRequest(t, `{"input":"0","output":"1","cache_read":"0","cache_write":null,"context_tiers":[{"threshold_tokens":1000,"input":"2","output":null,"cache_read":null,"cache_write":null}],"mode_schedules":{"fast":{"prices":{"input":"3","output":null,"cache_read":null,"cache_write":null},"context_tiers":[]},"ultrafast":{"prices":{"input":"4","output":null,"cache_read":null,"cache_write":null},"context_tiers":[]}}}`)
	if _, err := fixture.service.BatchModelPrices(t.Context(), ModelPriceBatchRequest{IDs: []uint{rows[1].ID, rows[2].ID}, Mode: "copy", Price: &request}); err != nil {
		t.Fatal(err)
	}
	updated := mustModelPriceUpdateRequest(t, modelPriceHTTPUpdateBody(`"9"`, "false"))
	if _, err := fixture.service.UpdateModelPrice(t.Context(), rows[0].ID, updated); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	for _, row := range rows[1:] {
		saved := assertFollowedPrice(t, fixture, row, &zero)
		if !saved.IsManual || saved.SourcePriceID != nil || len(saved.ContextPriceTiers) == 0 || len(saved.ModePriceSchedules) == 0 {
			t.Fatalf("manual batch lost independent prices: %#v", saved)
		}
	}
}

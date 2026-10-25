package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestModelPriceSourcesAndBatchRequireManagementAuth(t *testing.T) {
	t.Parallel()
	fixture, engine, _ := newModelPriceHTTPFixture(t, true)
	accessKey, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "price-read-only"})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/model-prices/sources?mode=copy", ""},
		{http.MethodPost, "/api/model-prices/batch", `{}`},
	} {
		response := serveModelPriceHTTPRequest(engine, request.method, request.path, request.body, "")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s auth status = %d", request.path, response.Code)
		}
		response = serveModelPriceHTTPRequest(engine, request.method, request.path, request.body, accessKey.Key)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s access key status = %d", request.path, response.Code)
		}
	}
}

func TestModelPriceBatchDatabaseFailureRollsBackEveryTarget(t *testing.T) {
	t.Parallel()
	fixture, _, first := newModelPriceHTTPFixture(t, true)
	second := models.ModelPrice{ChannelID: first.ChannelID, ModelID: "second-copy"}
	if err := fixture.db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	before := mustLoadPriceTable(t, fixture.db)
	fixture.priceRuntime.Publish(before)
	updates := 0
	const callback = "test:batch-price-failure"
	if err := fixture.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "model_prices" {
			updates++
			if updates == 2 {
				tx.AddError(errors.New("injected price write failure"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.db.Callback().Update().Remove(callback) })
	var price ModelPriceUpdateRequest
	if err := json.Unmarshal([]byte(modelPriceHTTPUpdateBody(`"7"`, "false")), &price); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.BatchModelPrices(t.Context(), ModelPriceBatchRequest{IDs: []uint{first.ID, second.ID}, Mode: "copy", Price: &price}); err == nil {
		t.Fatal("batch ignored a database write failure")
	}
	if updates != 2 || fixture.priceRuntime.Load() != before {
		t.Fatal("failed batch published partial prices")
	}
	var saved models.ModelPrice
	if err := fixture.db.First(&saved, first.ID).Error; err != nil || saved.IsManual || *saved.InputPriceNanoUSDPerMillionTokens != 2_500_000_000 {
		t.Fatalf("failed batch did not roll back the first write: %#v, %v", saved, err)
	}
}

func TestExternalModelPriceDirectoryReuse(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	fixture := newServiceFixtureWithDSN(t, dsn)
	suffix := time.Now().UnixNano()
	modelID := fmt.Sprintf("referenced-price-%d", suffix)
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: fmt.Sprintf("price-reuse-%d", suffix), ChannelID: string(channel.OpenAICompatible),
		Models: models.JSON(fmt.Sprintf(`[{"id":%q}]`, modelID)), Overrides: models.JSON(`{}`), Enabled: true,
	})
	row := models.ModelPrice{ChannelID: string(channel.OpenAICompatible), ModelID: modelID}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	fixture.catalogRuntime.Publish(referenceCatalog(3_000_000_000))
	linked, err := fixture.service.BatchModelPrices(t.Context(), ModelPriceBatchRequest{
		IDs: []uint{row.ID}, Mode: "link", Reference: &ModelPriceCatalogReferenceRequest{ProviderID: "google", ModelID: "source-model"},
	})
	if err != nil || len(linked.Items) != 1 || *linked.Items[0].Prices.Input != "3" {
		t.Fatalf("external link failed: %#v, %v", linked, err)
	}
	if err := fixture.service.applyCatalogSnapshot(t.Context(), referenceCatalog(5_000_000_000)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.DeleteGroup(t.Context(), group.ID); err != nil {
		t.Fatal(err)
	}
	fixture.catalogRuntime.Publish(&catalog.Snapshot{})
	if err := fixture.service.EnsureInitialState(t.Context()); err != nil {
		t.Fatal(err)
	}
	var saved models.ModelPrice
	if err := fixture.db.First(&saved, row.ID).Error; err != nil || saved.ReferenceProviderID != "google" || *saved.InputPriceNanoUSDPerMillionTokens != 5_000_000_000 {
		t.Fatalf("external offline price lost: %#v, %v", saved, err)
	}
	var price ModelPriceUpdateRequest
	if err := json.Unmarshal([]byte(modelPriceHTTPUpdateBody(`"4"`, "false")), &price); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.BatchModelPrices(t.Context(), ModelPriceBatchRequest{IDs: []uint{row.ID}, Mode: "copy", Price: &price}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.First(&saved, row.ID).Error; err != nil || saved.ReferenceProviderID != "" || !saved.IsManual || *saved.InputPriceNanoUSDPerMillionTokens != 4_000_000_000 {
		t.Fatalf("external copy did not detach: %#v, %v", saved, err)
	}
}

func TestModelPriceSourcesIncludeUnconfiguredCatalogAndLocalCopy(t *testing.T) {
	t.Parallel()
	fixture, engine, _ := newModelPriceHTTPFixture(t, true)
	fixture.catalogRuntime.Publish(referenceCatalog(3_000_000_000))
	for _, mode := range []string{"copy", "link"} {
		response := serveModelPriceHTTPRequest(engine, http.MethodGet, "/api/model-prices/sources?mode="+mode, "", authTestKey)
		if response.Code != http.StatusOK {
			t.Fatalf("sources status = %d: %s", response.Code, response.Body.String())
		}
		data := decodeModelPriceHTTPData(t, response)
		items := data["items"].([]any)
		catalogFound, localFound := false, false
		for _, item := range items {
			source := item.(map[string]any)
			localFound = localFound || source["kind"] == "local"
			if source["kind"] == "catalog" && source["model_id"] == "source-model" {
				catalogFound = true
				if source["prices"].(map[string]any)["input"] != "3" || len(source["context_tiers"].([]any)) != 1 || len(source["mode_schedules"].(map[string]any)) != 2 {
					t.Fatalf("source omitted price configuration: %#v", source)
				}
			}
		}
		if !catalogFound || localFound != (mode == "copy") {
			t.Fatalf("wrong %s sources: %#v", mode, items)
		}
	}
}

func TestModelPriceBatchCopiesAndLinksAtomically(t *testing.T) {
	t.Parallel()
	fixture, engine, first := newModelPriceHTTPFixture(t, true)
	second := models.ModelPrice{ChannelID: first.ChannelID, ModelID: "another-model"}
	if err := fixture.db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	fixture.catalogRuntime.Publish(referenceCatalog(3_000_000_000))
	link := fmt.Sprintf(`{"ids":[%d,%d,%d],"mode":"link","reference":{"provider_id":"google","model_id":"source-model"}}`, first.ID, second.ID, first.ID)
	response := serveModelPriceHTTPRequest(engine, http.MethodPost, "/api/model-prices/batch", link, authTestKey)
	if response.Code != http.StatusOK {
		t.Fatalf("batch link status = %d: %s", response.Code, response.Body.String())
	}
	data := decodeModelPriceHTTPData(t, response)
	if len(data["items"].([]any)) != 2 {
		t.Fatal("batch did not deduplicate target prices")
	}
	for _, id := range []uint{first.ID, second.ID} {
		var row models.ModelPrice
		if err := fixture.db.First(&row, id).Error; err != nil {
			t.Fatal(err)
		}
		if row.IsManual || row.ReferenceProviderID != "google" || row.ReferenceModelID != "source-model" || row.InputPriceNanoUSDPerMillionTokens == nil || *row.InputPriceNanoUSDPerMillionTokens != 3_000_000_000 || len(row.ModePriceSchedules) == 0 {
			t.Fatalf("incomplete batch link: %#v", row)
		}
	}
	before := fixture.priceRuntime.Load()
	invalid := fmt.Sprintf(`{"ids":[%d,999999],"mode":"copy","price":%s}`, first.ID, modelPriceHTTPUpdateBody(`"4"`, "false"))
	response = serveModelPriceHTTPRequest(engine, http.MethodPost, "/api/model-prices/batch", invalid, authTestKey)
	if response.Code != http.StatusNotFound || fixture.priceRuntime.Load() != before {
		t.Fatalf("invalid target batch changed runtime: %d %s", response.Code, response.Body.String())
	}
	var unchanged models.ModelPrice
	if err := fixture.db.First(&unchanged, first.ID).Error; err != nil || unchanged.ReferenceProviderID != "google" || *unchanged.InputPriceNanoUSDPerMillionTokens != 3_000_000_000 {
		t.Fatalf("rejected batch changed existing target: %#v, %v", unchanged, err)
	}
	copyBody := fmt.Sprintf(`{"ids":[%d,%d],"mode":"copy","price":{"input":"0","output":"5","cache_read":"0.5","cache_write":null,"context_tiers":[{"threshold_tokens":1000,"input":"6","output":null,"cache_read":null,"cache_write":null}],"mode_schedules":{"fast":{"prices":{"input":"7","output":null,"cache_read":null,"cache_write":null},"context_tiers":[]},"ultrafast":{"prices":{"input":"9","output":null,"cache_read":null,"cache_write":null},"context_tiers":[]}},"confirm_unpriced":false}}`, first.ID, second.ID)
	response = serveModelPriceHTTPRequest(engine, http.MethodPost, "/api/model-prices/batch", copyBody, authTestKey)
	if response.Code != http.StatusOK {
		t.Fatalf("batch copy status = %d: %s", response.Code, response.Body.String())
	}
	for _, id := range []uint{first.ID, second.ID} {
		var row models.ModelPrice
		if err := fixture.db.First(&row, id).Error; err != nil {
			t.Fatal(err)
		}
		var schedules map[string]models.ModePriceSchedule
		if err := json.Unmarshal(row.ModePriceSchedules, &schedules); err != nil {
			t.Fatal(err)
		}
		if !row.IsManual || row.ReferenceProviderID != "" || row.ReferenceModelID != "" || row.InputPriceNanoUSDPerMillionTokens == nil || *row.InputPriceNanoUSDPerMillionTokens != 0 || len(schedules) != 2 || len(row.ContextPriceTiers) == 0 {
			t.Fatalf("incomplete independent copy: %#v", row)
		}
	}
}

func TestModelPriceBatchRejectsInvalidReferenceAndMixedPayload(t *testing.T) {
	t.Parallel()
	fixture, engine, row := newModelPriceHTTPFixture(t, true)
	fixture.catalogRuntime.Publish(referenceCatalog(3_000_000_000))
	for _, body := range []string{
		fmt.Sprintf(`{"ids":[%d],"mode":"link","reference":{"provider_id":"missing","model_id":"source-model"}}`, row.ID),
		fmt.Sprintf(`{"ids":[%d],"mode":"link","reference":{"price_id":%d}}`, row.ID, row.ID),
		fmt.Sprintf(`{"ids":[%d],"mode":"link","reference":{"provider_id":"google","model_id":"source-model"},"price":%s}`, row.ID, modelPriceHTTPUpdateBody(`"4"`, "false")),
		fmt.Sprintf(`{"ids":[%d],"mode":"copy"}`, row.ID),
	} {
		response := serveModelPriceHTTPRequest(engine, http.MethodPost, "/api/model-prices/batch", body, authTestKey)
		if response.Code < 400 || response.Code >= 500 {
			t.Fatalf("invalid batch status = %d: %s", response.Code, response.Body.String())
		}
	}
}

func TestModelPriceSourcesPrioritizeExactModelsAndCatalogProviders(t *testing.T) {
	t.Parallel()
	fixture, _, _ := newModelPriceHTTPFixture(t, false)
	modelID := "gpt-example"
	cost := &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1_000_000_000)}}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{}}
	for _, providerID := range []string{"openai", "anthropic"} {
		snapshot.Providers[providerID] = catalog.Provider{ID: providerID, Models: map[string]catalog.Model{
			modelID: {ID: modelID, Cost: cost},
		}}
	}
	for i := range 60 {
		providerID := fmt.Sprintf("aaa-provider-%02d", i)
		snapshot.Providers[providerID] = catalog.Provider{ID: providerID, Models: map[string]catalog.Model{
			modelID: {ID: modelID, Cost: cost},
		}}
		variant := fmt.Sprintf("%s-%02d", modelID, i)
		snapshot.Providers["openai"].Models[variant] = catalog.Model{ID: variant, Cost: cost}
		input := int64(1_000_000_000)
		if err := fixture.db.Create(&models.ModelPrice{ChannelID: string(channel.OpenAICompatible), ModelID: variant, IsManual: true, InputPriceNanoUSDPerMillionTokens: &input}).Error; err != nil {
			t.Fatal(err)
		}
	}
	fixture.catalogRuntime.Publish(snapshot)
	for _, mode := range []string{"copy", "link"} {
		result, err := fixture.service.ModelPriceSources(t.Context(), mode, "GPT-EXAMPLE")
		if err != nil {
			t.Fatal(err)
		}
		if !result.HasMore || len(result.Items) != 50 {
			t.Fatalf("%s search returned %d sources, has_more=%t", mode, len(result.Items), result.HasMore)
		}
		if result.Items[0].ProviderID != "openai" || result.Items[1].ProviderID != "anthropic" {
			t.Fatalf("%s search buried preferred sources behind %q and %q", mode, result.Items[0].Key, result.Items[1].Key)
		}
		for _, source := range result.Items {
			if source.ModelID != modelID {
				t.Fatalf("%s search ranked a partial match above exact matches: %#v", mode, source)
			}
		}
	}
}

func TestModelPriceBatchReportsUnavailableCatalogSource(t *testing.T) {
	t.Parallel()
	fixture, engine, row := newModelPriceHTTPFixture(t, true)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{})
	body := fmt.Sprintf(`{"ids":[%d],"mode":"link","reference":{"provider_id":"google","model_id":"source-model"}}`, row.ID)
	response := serveModelPriceHTTPRequestWithLanguage(engine, http.MethodPost, "/api/model-prices/batch", body, authTestKey, "zh-CN")
	assertModelPriceHTTPError(t, response, http.StatusConflict, "MODEL_PRICE_SOURCE_UNAVAILABLE", nil)
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Message != "参考价格来源已不可用，请重新选择" {
		t.Fatalf("unhelpful source error: %q", envelope.Message)
	}
}

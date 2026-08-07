package control

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestListModelPricesProjectsFinalFactsWithoutLeakingScopeKey(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "disabled-provider", ProviderID: &providerID,
		UpstreamURL: "https://disabled.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models: models.JSON(`[` +
			`{"id":"shared","alias":"public-one"},` +
			`{"id":"shared","alias":"public-two"},` +
			`{"id":"zero","alias":"free"}` +
			`]`),
		Config: models.JSON(`{}`), Enabled: false,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "enabled-provider", ProviderID: &providerID,
		UpstreamURL: "https://enabled.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"shared","alias":"another-public"}]`),
		Config:      models.JSON(`{}`), Enabled: true,
	})
	custom := createPriceTestGroup(t, fixture.db, models.Group{
		Name:        "Custom Group",
		UpstreamURL: "https://custom.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"custom-model","alias":"client-name"}]`),
		Config:      models.JSON(`{}`), Enabled: true,
	})
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Name: "OpenAI Catalog"},
	}})

	providerScope, _ := pricing.ProviderScopeKey("openai")
	missingProviderScope, _ := pricing.ProviderScopeKey("missing")
	customScope, _ := pricing.GroupScopeKey(custom.ID)
	orphanScope, _ := pricing.GroupScopeKey(999999)
	zero := int64(0)
	maximum := int64(math.MaxInt64)
	rows := []models.ModelPrice{
		{PriceScopeKey: providerScope, ModelID: "shared"},
		{
			PriceScopeKey: providerScope, ModelID: "zero",
			InputPriceNanoUSDPerMillionTokens: &zero,
			ContextPriceTiers: models.JSON(`[{` +
				`"threshold_tokens":1000,` +
				`"input_price_nano_usd_per_million_tokens":0,` +
				`"output_price_nano_usd_per_million_tokens":null,` +
				`"cache_read_price_nano_usd_per_million_tokens":null,` +
				`"cache_write_price_nano_usd_per_million_tokens":null` +
				`}]`),
		},
		{PriceScopeKey: customScope, ModelID: "custom-model"},
		{PriceScopeKey: missingProviderScope, ModelID: "manual-null", IsManual: true},
		{
			PriceScopeKey: orphanScope, ModelID: "orphan-expensive", IsManual: true,
			InputPriceNanoUSDPerMillionTokens: &maximum,
		},
	}
	if err := fixture.db.Create(&rows).Error; err != nil {
		t.Fatalf("create model price fixtures: %v", err)
	}

	result, err := fixture.service.ListModelPrices(t.Context(), ModelPriceListQuery{
		Usage: ModelPriceUsageAll, Status: ModelPriceStatusAll, Page: 1, PageSize: 100,
	})
	if err != nil {
		t.Fatalf("ListModelPrices() error = %v", err)
	}
	wantOrder := []string{
		"group/" + modelPriceGroupID(custom.ID) + "/custom-model",
		"provider/openai/shared",
		"provider/openai/zero",
		"group/999999/orphan-expensive",
		"provider/missing/manual-null",
	}
	gotOrder := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		gotOrder = append(gotOrder, item.Scope.Kind+"/"+item.Scope.ID+"/"+item.ModelID)
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("list order = %v, want %v", gotOrder, wantOrder)
	}
	if result.Pagination != (ModelPricePaginationDTO{
		Page: 1, PageSize: 100, TotalItems: 5, TotalPages: 1,
	}) {
		t.Fatalf("pagination = %#v", result.Pagination)
	}

	customItem := result.Items[0]
	if customItem.Scope.Label != "Custom Group" || customItem.PricingStatus != PricingStatusPending ||
		customItem.Method != nil || !customItem.Referenced || customItem.ReferenceCount != 1 ||
		customItem.ReferenceGroupCount != 1 || customItem.Partial || customItem.CanReset || customItem.CanDelete {
		t.Fatalf("custom pending item = %#v", customItem)
	}
	sharedItem := result.Items[1]
	if sharedItem.Scope.Label != "OpenAI Catalog" || sharedItem.Method != nil ||
		sharedItem.ReferenceCount != 3 || sharedItem.ReferenceGroupCount != 2 || !sharedItem.Referenced {
		t.Fatalf("shared provider item = %#v", sharedItem)
	}
	zeroItem := result.Items[2]
	if zeroItem.Prices.Input == nil || *zeroItem.Prices.Input != "0" ||
		zeroItem.Prices.Output != nil || zeroItem.Method != nil ||
		!zeroItem.Partial || len(zeroItem.ContextTiers) != 1 ||
		zeroItem.ContextTiers[0].ThresholdTokens != 1000 ||
		zeroItem.ContextTiers[0].Prices.Input == nil || *zeroItem.ContextTiers[0].Prices.Input != "0" ||
		zeroItem.ReferenceCount != 1 || zeroItem.ReferenceGroupCount != 1 {
		t.Fatalf("explicit zero item = %#v", zeroItem)
	}
	orphanItem := result.Items[3]
	if orphanItem.Scope.Label != "#999999" || orphanItem.Method == nil || *orphanItem.Method != "user_set" ||
		orphanItem.Prices.Input == nil || *orphanItem.Prices.Input != "9223372036.854775807" ||
		!orphanItem.CanReset || !orphanItem.CanDelete || orphanItem.Referenced {
		t.Fatalf("orphan item = %#v", orphanItem)
	}
	manualNullItem := result.Items[4]
	if manualNullItem.Scope.Label != "missing" || manualNullItem.Method == nil ||
		*manualNullItem.Method != "user_marked_unpriced" ||
		manualNullItem.PricingStatus != PricingStatusConfigured || manualNullItem.Partial ||
		!manualNullItem.CanReset || !manualNullItem.CanDelete {
		t.Fatalf("manual all-null item = %#v", manualNullItem)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if containsJSONField(encoded, "price_scope_key") {
		t.Fatalf("response leaked price_scope_key: %s", encoded)
	}
}

func TestProjectModelPriceRowDerivesAutomaticMatchedProviderFromCurrentCatalog(t *testing.T) {
	providerScope, err := pricing.ProviderScopeKey("z-provider")
	if err != nil {
		t.Fatal(err)
	}
	selfScope, err := pricing.ProviderScopeKey("self-provider")
	if err != nil {
		t.Fatal(err)
	}
	groupScope, err := pricing.GroupScopeKey(1)
	if err != nil {
		t.Fatal(err)
	}
	persistedValue := int64(999)
	cases := []struct {
		name          string
		row           models.ModelPrice
		snapshot      *catalog.Snapshot
		wantMethod    any
		wantMatchedID any
	}{
		{
			name: "provider scoped self match remains auto sync",
			row: models.ModelPrice{
				ID: 1, PriceScopeKey: selfScope, ModelID: "shared-model",
				InputPriceNanoUSDPerMillionTokens: &persistedValue,
			},
			snapshot: automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
				"self-provider": automaticPriceTestCost(1),
			}),
			wantMethod: "auto_sync", wantMatchedID: "self-provider",
		},
		{
			name: "group scope follows priority provider",
			row: models.ModelPrice{
				ID: 2, PriceScopeKey: groupScope, ModelID: "shared-model",
				InputPriceNanoUSDPerMillionTokens: &persistedValue,
			},
			snapshot: automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
				"openai": automaticPriceTestCost(1),
			}),
			wantMethod: "auto_matched", wantMatchedID: "openai",
		},
		{
			name: "group scope tier only price follows priority provider",
			row: models.ModelPrice{
				ID: 7, PriceScopeKey: groupScope, ModelID: "shared-model",
				ContextPriceTiers: models.JSON(`[{"threshold_tokens":1000,"input_price_nano_usd_per_million_tokens":1,"output_price_nano_usd_per_million_tokens":null,"cache_read_price_nano_usd_per_million_tokens":null,"cache_write_price_nano_usd_per_million_tokens":null}]`),
			},
			snapshot: automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
				"openai": automaticPriceTestCost(1),
			}),
			wantMethod: "auto_matched", wantMatchedID: "openai",
		},
		{
			name: "provider scope falls back to another provider",
			row: models.ModelPrice{
				ID: 3, PriceScopeKey: providerScope, ModelID: "shared-model",
				InputPriceNanoUSDPerMillionTokens: &persistedValue,
			},
			snapshot: automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
				"z-provider": nil,
				"openai":     automaticPriceTestCost(1),
			}),
			wantMethod: "auto_matched", wantMatchedID: "openai",
		},
		{
			name: "automatic row with no current source is pending",
			row: models.ModelPrice{
				ID: 4, PriceScopeKey: providerScope, ModelID: "shared-model",
				InputPriceNanoUSDPerMillionTokens: &persistedValue,
			},
			snapshot: automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
				"z-provider": nil,
			}),
			wantMethod: nil, wantMatchedID: nil,
		},
		{
			name: "manual override never exposes catalog source",
			row: models.ModelPrice{
				ID: 5, PriceScopeKey: selfScope, ModelID: "shared-model", IsManual: true,
				InputPriceNanoUSDPerMillionTokens: &persistedValue,
			},
			snapshot: automaticPriceTestSnapshot(map[string]*catalog.ModelCost{
				"self-provider": automaticPriceTestCost(1),
			}),
			wantMethod: "user_override", wantMatchedID: nil,
		},
		{
			name:          "manual unpriced row never exposes catalog source",
			row:           models.ModelPrice{ID: 6, PriceScopeKey: groupScope, ModelID: "shared-model", IsManual: true},
			snapshot:      automaticPriceTestSnapshot(map[string]*catalog.ModelCost{"openai": automaticPriceTestCost(1)}),
			wantMethod:    "user_marked_unpriced",
			wantMatchedID: nil,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			record, err := projectModelPriceRow(test.row, priceReferenceSnapshot{}, test.snapshot)
			if err != nil {
				t.Fatalf("projectModelPriceRow() error = %v", err)
			}
			encoded, err := json.Marshal(record.dto)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["method"] != test.wantMethod {
				t.Fatalf("method = %#v, want %#v", payload["method"], test.wantMethod)
			}
			matchedID, exists := payload["matched_provider_id"]
			if !exists || matchedID != test.wantMatchedID {
				t.Fatalf("matched_provider_id = %#v (exists %t), want %#v", matchedID, exists, test.wantMatchedID)
			}
		})
	}
}

func TestListModelPricesFiltersBeforeStablePagination(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "disabled", ProviderID: &providerID,
		UpstreamURL: "https://provider.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"pending","alias":"ignored-alias"},{"id":"configured","alias":""}]`),
		Config:      models.JSON(`{}`), Enabled: false,
	})
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Name: "Provider Display"},
	}})
	scope, _ := pricing.ProviderScopeKey("openai")
	value := int64(1_000_000_000)
	rows := []models.ModelPrice{
		{PriceScopeKey: scope, ModelID: "pending"},
		{PriceScopeKey: scope, ModelID: "configured", InputPriceNanoUSDPerMillionTokens: &value},
		{PriceScopeKey: scope, ModelID: "unused-a", IsManual: true},
		{PriceScopeKey: scope, ModelID: "unused-b", IsManual: true},
	}
	if err := fixture.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	defaults, err := fixture.service.ListModelPrices(t.Context(), ModelPriceListQuery{
		Usage: ModelPriceUsageInUse, Status: ModelPriceStatusAll, Page: 1, PageSize: 20,
	})
	if err != nil || len(defaults.Items) != 2 || defaults.Pagination.TotalItems != 2 {
		t.Fatalf("default in-use result = %#v, %v", defaults, err)
	}

	pageTwo, err := fixture.service.ListModelPrices(t.Context(), ModelPriceListQuery{
		Usage: ModelPriceUsageAll, Status: ModelPriceStatusConfigured,
		Search: "provider display", Page: 2, PageSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pageTwo.Pagination != (ModelPricePaginationDTO{
		Page: 2, PageSize: 2, TotalItems: 3, TotalPages: 2,
	}) || len(pageTwo.Items) != 1 || pageTwo.Items[0].ModelID != "unused-b" {
		t.Fatalf("filtered page two = %#v", pageTwo)
	}

	empty, err := fixture.service.ListModelPrices(t.Context(), ModelPriceListQuery{
		Usage: ModelPriceUsageUnreferenced, Status: ModelPriceStatusPending,
		Search: "ignored-alias", Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Items == nil || len(empty.Items) != 0 || empty.Pagination.TotalPages != 0 {
		t.Fatalf("alias search unexpectedly matched or nil items: %#v", empty)
	}
}

func TestUpdateModelPriceUsesFullManualOwnershipAndIsIdempotent(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider-one", ProviderID: &providerID,
		UpstreamURL: "https://one.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"shared","alias":"first"},{"id":"shared","alias":"second"}]`),
		Config:      models.JSON(`{}`), Enabled: false,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider-two", ProviderID: &providerID,
		UpstreamURL: "https://two.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"shared","alias":"third"}]`),
		Config:      models.JSON(`{}`), Enabled: true,
	})
	scope, _ := pricing.ProviderScopeKey(providerID)
	oldInput := int64(1_000_000_000)
	row := models.ModelPrice{
		PriceScopeKey: scope, ModelID: "shared",
		InputPriceNanoUSDPerMillionTokens: &oldInput,
		ContextPriceTiers: models.JSON(`[{` +
			`"threshold_tokens":1000,` +
			`"input_price_nano_usd_per_million_tokens":2000000000,` +
			`"output_price_nano_usd_per_million_tokens":null,` +
			`"cache_read_price_nano_usd_per_million_tokens":null,` +
			`"cache_write_price_nano_usd_per_million_tokens":null` +
			`}]`),
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	fixture.service.now = func() time.Time { return time.UnixMilli(50_000) }
	request := mustModelPriceUpdateRequest(t,
		`{"input":"2.0","output":null,"cache_read":"0","cache_write":null,`+
			`"context_tiers":[{"threshold_tokens":1000,"input":"2.5","output":null,"cache_read":null,"cache_write":null}]}`,
	)

	result, err := fixture.service.UpdateModelPrice(t.Context(), row.ID, request)
	if err != nil {
		t.Fatalf("UpdateModelPrice() error = %v", err)
	}
	if result.Scope.ID != providerID || result.ModelID != "shared" || result.Method == nil ||
		*result.Method != "user_override" || result.ReferenceCount != 3 || result.ReferenceGroupCount != 2 ||
		len(result.ContextTiers) != 1 || result.ContextTiers[0].ThresholdTokens != 1000 ||
		result.ContextTiers[0].Prices.Input == nil || *result.ContextTiers[0].Prices.Input != "2.5" ||
		result.Prices.Input == nil || *result.Prices.Input != "2" ||
		result.Prices.CacheRead == nil || *result.Prices.CacheRead != "0" {
		t.Fatalf("updated DTO = %#v", result)
	}
	var stored models.ModelPrice
	if err := fixture.db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.IsManual || len(stored.ContextPriceTiers) == 0 || stored.UpdatedAtMS != 50_000 ||
		stored.PriceScopeKey != scope || stored.ModelID != "shared" ||
		stored.InputPriceNanoUSDPerMillionTokens == nil || *stored.InputPriceNanoUSDPerMillionTokens != 2_000_000_000 ||
		stored.CacheReadPriceNanoUSDPerMillionTokens == nil || *stored.CacheReadPriceNanoUSDPerMillionTokens != 0 {
		t.Fatalf("stored manual row = %#v", stored)
	}
	rule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{ScopeKey: scope, ModelID: "shared"})
	if !exists || !rule.IsManual || len(rule.ContextTiers) != 1 ||
		!rule.Prices.Input.Set || rule.Prices.Input.NanoUSDPerMillion != 2_000_000_000 ||
		!rule.Prices.CacheRead.Set || rule.Prices.CacheRead.NanoUSDPerMillion != 0 {
		t.Fatalf("published manual rule = %#v, %t", rule, exists)
	}

	fixture.service.now = func() time.Time { return time.UnixMilli(60_000) }
	equivalent := mustModelPriceUpdateRequest(t,
		`{"input":"2.000000000","output":null,"cache_read":"0.0","cache_write":null,`+
			`"context_tiers":[{"threshold_tokens":1000,"input":"2.500000000","output":null,"cache_read":null,"cache_write":null}]}`,
	)
	if _, err := fixture.service.UpdateModelPrice(t.Context(), row.ID, equivalent); err != nil {
		t.Fatalf("idempotent UpdateModelPrice() error = %v", err)
	}
	if err := fixture.db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.UpdatedAtMS != 50_000 {
		t.Fatalf("idempotent update timestamp = %d, want 50000", stored.UpdatedAtMS)
	}

	fixture.service.now = func() time.Time { return time.UnixMilli(70_000) }
	cleared := mustModelPriceUpdateRequest(t,
		`{"input":"2.0","output":null,"cache_read":"0","cache_write":null,"context_tiers":[]}`,
	)
	clearedResult, err := fixture.service.UpdateModelPrice(t.Context(), row.ID, cleared)
	if err != nil {
		t.Fatalf("UpdateModelPrice() clearing tiers error = %v", err)
	}
	if len(clearedResult.ContextTiers) != 0 {
		t.Fatalf("cleared DTO still has tiers = %#v", clearedResult)
	}
	// A freshly declared destination is used here (rather than reusing `stored`)
	// because GORM's scanner field-setter leaves a reused struct field
	// untouched when a SQL NULL follows a prior non-NULL scan into the same
	// variable — an unrelated stdlib/GORM interaction, not a persistence bug.
	var clearedRow models.ModelPrice
	if err := fixture.db.First(&clearedRow, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(clearedRow.ContextPriceTiers) != 0 || clearedRow.UpdatedAtMS != 70_000 {
		t.Fatalf("cleared stored row = %#v", clearedRow)
	}
}

func TestUpdateModelPriceConfirmedAllNullBlocksCatalogSync(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider", ProviderID: &providerID,
		UpstreamURL: "https://provider.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"priced","alias":""}]`),
		Config:      models.JSON(`{}`), Enabled: true,
	})
	scope, _ := pricing.ProviderScopeKey(providerID)
	oldValue := int64(1_000_000_000)
	row := models.ModelPrice{
		PriceScopeKey: scope, ModelID: "priced",
		InputPriceNanoUSDPerMillionTokens: &oldValue,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	newPrice := pricing.Price{NanoUSDPerMillion: 9_000_000_000, Set: true}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		providerID: {
			ID: providerID,
			Models: map[string]catalog.Model{
				"priced": {ID: "priced", Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: newPrice}}},
			},
		},
	}}
	fixture.catalogRuntime.Publish(snapshot)
	confirmed := mustModelPriceUpdateRequest(t,
		`{"input":null,"output":null,"cache_read":null,"cache_write":null,"confirm_unpriced":true,"context_tiers":[]}`,
	)

	result, err := fixture.service.UpdateModelPrice(t.Context(), row.ID, confirmed)
	if err != nil {
		t.Fatalf("confirmed UpdateModelPrice() error = %v", err)
	}
	if result.PricingStatus != PricingStatusConfigured || result.Method == nil ||
		*result.Method != "user_marked_unpriced" || result.Prices != (PriceSlotsDTO{}) {
		t.Fatalf("confirmed all-null DTO = %#v", result)
	}
	if err := fixture.service.applyCatalogSnapshot(t.Context(), snapshot); err != nil {
		t.Fatalf("applyCatalogSnapshot() error = %v", err)
	}
	var stored models.ModelPrice
	if err := fixture.db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.IsManual || modelPriceConfiguredSlotCount(stored) != 0 {
		t.Fatalf("catalog sync overwrote manual all-null row: %#v", stored)
	}
	rule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{ScopeKey: scope, ModelID: "priced"})
	if !exists || !rule.IsManual || priceRuleHasConfiguredValue(rule) {
		t.Fatalf("catalog sync published over manual all-null rule = %#v, %t", rule, exists)
	}
}

func TestUpdateModelPriceSerializesAfterInFlightCatalogSyncAndWins(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider", ProviderID: &providerID,
		UpstreamURL: "https://provider.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"serialized","alias":""}]`),
		Config:      models.JSON(`{}`), Enabled: true,
	})
	scope, _ := pricing.ProviderScopeKey(providerID)
	oldValue := int64(1_000_000_000)
	row := models.ModelPrice{
		PriceScopeKey: scope, ModelID: "serialized",
		InputPriceNanoUSDPerMillionTokens: &oldValue,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	catalogValue := pricing.Price{NanoUSDPerMillion: 9_000_000_000, Set: true}
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		providerID: {
			ID: providerID,
			Models: map[string]catalog.Model{
				"serialized": {
					ID:   "serialized",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: catalogValue}},
				},
			},
		},
	}}

	syncAtUpdate := make(chan struct{})
	releaseSync := make(chan struct{})
	var blockOnce sync.Once
	callbackName := "test:block_catalog_price_update"
	if err := fixture.db.Callback().Update().Before("gorm:update").Register(callbackName, func(*gorm.DB) {
		block := false
		blockOnce.Do(func() { block = true })
		if block {
			close(syncAtUpdate)
			<-releaseSync
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.db.Callback().Update().Remove(callbackName) })

	syncDone := make(chan error, 1)
	go func() { syncDone <- fixture.service.applyCatalogSnapshot(t.Context(), snapshot) }()
	<-syncAtUpdate
	if fixture.service.writeMu.TryRLock() {
		fixture.service.writeMu.RUnlock()
		t.Fatal("catalog sync did not hold writeMu while updating prices")
	}

	userStarted := make(chan struct{})
	userDone := make(chan error, 1)
	request := mustModelPriceUpdateRequest(t,
		`{"input":"7","output":null,"cache_read":null,"cache_write":null,"context_tiers":[]}`,
	)
	go func() {
		close(userStarted)
		_, err := fixture.service.UpdateModelPrice(t.Context(), row.ID, request)
		userDone <- err
	}()
	<-userStarted
	close(releaseSync)
	if err := <-syncDone; err != nil {
		t.Fatalf("applyCatalogSnapshot() error = %v", err)
	}
	if err := <-userDone; err != nil {
		t.Fatalf("UpdateModelPrice() error = %v", err)
	}

	if err := fixture.service.applyCatalogSnapshot(t.Context(), snapshot); err != nil {
		t.Fatalf("second applyCatalogSnapshot() error = %v", err)
	}
	var stored models.ModelPrice
	if err := fixture.db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.IsManual || stored.InputPriceNanoUSDPerMillionTokens == nil ||
		*stored.InputPriceNanoUSDPerMillionTokens != 7_000_000_000 {
		t.Fatalf("catalog sync won over user edit: %#v", stored)
	}
	rule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{ScopeKey: scope, ModelID: "serialized"})
	if !exists || !rule.IsManual || !rule.Prices.Input.Set ||
		rule.Prices.Input.NanoUSDPerMillion != 7_000_000_000 {
		t.Fatalf("final published rule = %#v, %t", rule, exists)
	}
}

func TestUpdateModelPriceRequiresAllNullConfirmationAndRollsBackCompileFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	scope, _ := pricing.ProviderScopeKey("openai")
	value := int64(1_000_000_000)
	row := models.ModelPrice{
		PriceScopeKey: scope, ModelID: "target",
		InputPriceNanoUSDPerMillionTokens: &value,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	beforeRuntime := fixture.priceRuntime.Load()

	unconfirmed := mustModelPriceUpdateRequest(t,
		`{"input":null,"output":null,"cache_read":null,"cache_write":null,"context_tiers":[]}`,
	)
	_, err := fixture.service.UpdateModelPrice(t.Context(), row.ID, unconfirmed)
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED" {
		t.Fatalf("unconfirmed all-null error = %#v", err)
	}
	data, ok := apiErr.Data.(ModelPriceIDData)
	if !ok || data.ID != row.ID {
		t.Fatalf("unconfirmed all-null data = %#v, want id %d", apiErr.Data, row.ID)
	}
	if fixture.priceRuntime.Load() != beforeRuntime {
		t.Fatal("rejected all-null edit published PriceRuntime")
	}

	if err := fixture.db.Exec(
		`INSERT INTO model_prices (`+
			`price_scope_key, model_id, is_manual, created_at_ms, updated_at_ms`+
			`) VALUES (?, ?, ?, ?, ?)`,
		"invalid:scope", "corrupt", false, 1, 1,
	).Error; err != nil {
		t.Fatal(err)
	}
	confirmed := mustModelPriceUpdateRequest(t,
		`{"input":null,"output":null,"cache_read":null,"cache_write":null,"confirm_unpriced":true,"context_tiers":[]}`,
	)
	_, err = fixture.service.UpdateModelPrice(t.Context(), row.ID, confirmed)
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("compile failure error = %v, want internal", err)
	}
	if fixture.priceRuntime.Load() != beforeRuntime {
		t.Fatal("failed transaction replaced PriceRuntime")
	}
	var stored models.ModelPrice
	if err := fixture.db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.IsManual || stored.InputPriceNanoUSDPerMillionTokens == nil ||
		*stored.InputPriceNanoUSDPerMillionTokens != value {
		t.Fatalf("failed transaction did not roll back target: %#v", stored)
	}
}

func TestResetModelPriceRestoresExactProviderCatalogCostAndPublishes(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider-one", ProviderID: &providerID,
		UpstreamURL: "https://one.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"restore","alias":"one"},{"id":"restore","alias":"two"}]`),
		Config:      models.JSON(`{}`), Enabled: false,
	})
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider-two", ProviderID: &providerID,
		UpstreamURL: "https://two.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"restore","alias":"three"}]`),
		Config:      models.JSON(`{}`), Enabled: true,
	})
	scope, _ := pricing.ProviderScopeKey(providerID)
	manualValue := int64(99_000_000_000)
	row := models.ModelPrice{
		PriceScopeKey: scope, ModelID: "restore", IsManual: true,
		InputPriceNanoUSDPerMillionTokens: &manualValue,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	fixture.service.now = func() time.Time { return time.UnixMilli(70_000) }
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		providerID: {
			ID: providerID, Name: "OpenAI Catalog",
			Models: map[string]catalog.Model{
				"restore": {
					ID: "restore",
					Cost: &catalog.ModelCost{
						Prices: pricing.Prices{
							Input:      priceTestValue(2_000_000_000),
							Output:     priceTestValue(4_000_000_000),
							CacheRead:  priceTestValue(0),
							CacheWrite: priceTestValue(1_000_000_000),
						},
						ContextTiers: []pricing.ContextTier{{
							InputThresholdTokens: 200_000,
							Prices: pricing.Prices{
								Input:  priceTestValue(3_000_000_000),
								Output: priceTestValue(5_000_000_000),
							},
						}},
					},
				},
			},
		},
	}}
	fixture.catalogRuntime.Publish(snapshot)

	result, err := fixture.service.ResetModelPrice(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("ResetModelPrice() error = %v", err)
	}
	if result.Scope.ID != providerID || result.Scope.Label != "OpenAI Catalog" ||
		result.ModelID != "restore" || result.PricingStatus != PricingStatusConfigured ||
		result.Method == nil || *result.Method != "auto_sync" || result.CanReset || result.CanDelete ||
		result.ReferenceCount != 3 || result.ReferenceGroupCount != 2 || len(result.ContextTiers) != 1 ||
		result.ContextTiers[0].ThresholdTokens != 200_000 ||
		result.ContextTiers[0].Prices.Input == nil || *result.ContextTiers[0].Prices.Input != "3" ||
		result.Prices.Input == nil || *result.Prices.Input != "2" ||
		result.Prices.Output == nil || *result.Prices.Output != "4" ||
		result.Prices.CacheRead == nil || *result.Prices.CacheRead != "0" ||
		result.Prices.CacheWrite == nil || *result.Prices.CacheWrite != "1" {
		t.Fatalf("reset DTO = %#v", result)
	}
	var stored models.ModelPrice
	if err := fixture.db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.IsManual || stored.PriceScopeKey != scope || stored.ModelID != "restore" ||
		stored.UpdatedAtMS != 70_000 || len(stored.ContextPriceTiers) == 0 {
		t.Fatalf("reset stored row = %#v", stored)
	}
	rule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{ScopeKey: scope, ModelID: "restore"})
	if !exists || rule.IsManual || len(rule.ContextTiers) != 1 ||
		!rule.Prices.Input.Set || rule.Prices.Input.NanoUSDPerMillion != 2_000_000_000 ||
		!rule.Prices.CacheRead.Set || rule.Prices.CacheRead.NanoUSDPerMillion != 0 {
		t.Fatalf("reset published rule = %#v, %t", rule, exists)
	}
}

func TestResetModelPriceMissingCatalogCandidateBecomesAutomaticPending(t *testing.T) {
	tests := []struct {
		name     string
		snapshot *catalog.Snapshot
	}{
		{name: "provider missing", snapshot: &catalog.Snapshot{Providers: map[string]catalog.Provider{}}},
		{name: "model missing", snapshot: &catalog.Snapshot{Providers: map[string]catalog.Provider{
			"openai": {ID: "openai", Models: map[string]catalog.Model{}},
		}}},
		{name: "cost missing", snapshot: &catalog.Snapshot{Providers: map[string]catalog.Provider{
			"openai": {ID: "openai", Models: map[string]catalog.Model{
				"missing": {ID: "missing"},
			}},
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			scope, _ := pricing.ProviderScopeKey("openai")
			value := int64(8_000_000_000)
			row := models.ModelPrice{
				PriceScopeKey: scope, ModelID: "missing", IsManual: true,
				InputPriceNanoUSDPerMillionTokens: &value,
			}
			if err := fixture.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			publishPersistedPriceTableForTest(t, fixture)
			fixture.catalogRuntime.Publish(test.snapshot)
			fixture.service.now = func() time.Time { return time.UnixMilli(80_000) }

			result, err := fixture.service.ResetModelPrice(t.Context(), row.ID)
			if err != nil {
				t.Fatalf("ResetModelPrice() error = %v", err)
			}
			if result.PricingStatus != PricingStatusPending || result.Method != nil ||
				result.Prices != (PriceSlotsDTO{}) || result.CanReset || len(result.ContextTiers) != 0 {
				t.Fatalf("pending reset DTO = %#v", result)
			}
			var stored models.ModelPrice
			if err := fixture.db.First(&stored, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.IsManual || modelPriceConfiguredSlotCount(stored) != 0 ||
				len(stored.ContextPriceTiers) != 0 || stored.UpdatedAtMS != 80_000 {
				t.Fatalf("pending reset row = %#v", stored)
			}
			rule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{ScopeKey: scope, ModelID: "missing"})
			if !exists || rule.IsManual || priceRuleHasConfiguredValue(rule) {
				t.Fatalf("pending reset rule = %#v, %t", rule, exists)
			}
		})
	}
}

func TestResetModelPriceGroupScopeRestoresPriorityCatalogCost(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name:        "custom",
		UpstreamURL: "https://custom.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"custom-model","alias":"client"}]`),
		Config:      models.JSON(`{}`),
		Enabled:     false,
	})
	scope, _ := pricing.GroupScopeKey(group.ID)
	value := int64(6_000_000_000)
	row := models.ModelPrice{
		PriceScopeKey: scope, ModelID: "custom-model", IsManual: true,
		InputPriceNanoUSDPerMillionTokens: &value,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"custom-model": {
				ID:   "custom-model",
				Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(99_000_000_000)}},
			},
		}},
	}})

	result, err := fixture.service.ResetModelPrice(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("ResetModelPrice() error = %v", err)
	}
	if result.Scope.Kind != priceScopeKindGroup || result.Scope.ID != modelPriceGroupID(group.ID) ||
		result.PricingStatus != PricingStatusConfigured || result.Method == nil || *result.Method != "auto_matched" ||
		result.Prices.Input == nil || *result.Prices.Input != "99" ||
		!result.Referenced || result.ReferenceCount != 1 || result.ReferenceGroupCount != 1 {
		t.Fatalf("Group reset DTO = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["matched_provider_id"] != "openai" {
		t.Fatalf("Group reset matched_provider_id = %#v, want openai", payload["matched_provider_id"])
	}
	var stored models.ModelPrice
	if err := fixture.db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.IsManual || stored.InputPriceNanoUSDPerMillionTokens == nil ||
		*stored.InputPriceNanoUSDPerMillionTokens != 99_000_000_000 || len(stored.ContextPriceTiers) != 0 {
		t.Fatalf("Group reset did not restore priority catalog values: %#v", stored)
	}
}

func TestResetModelPriceAutomaticIdenticalDoesNotChurnTimestamp(t *testing.T) {
	fixture := newServiceFixture(t)
	scope, _ := pricing.ProviderScopeKey("openai")
	input := int64(2_000_000_000)
	row := models.ModelPrice{
		PriceScopeKey: scope, ModelID: "same",
		InputPriceNanoUSDPerMillionTokens: &input,
		UpdatedAtMS:                       12_345,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	var before models.ModelPrice
	if err := fixture.db.First(&before, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"same": {
				ID:   "same",
				Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(2_000_000_000)}},
			},
		}},
	}})
	fixture.service.now = func() time.Time { return time.UnixMilli(90_000) }

	if _, err := fixture.service.ResetModelPrice(t.Context(), row.ID); err != nil {
		t.Fatalf("ResetModelPrice() error = %v", err)
	}
	var after models.ModelPrice
	if err := fixture.db.First(&after, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.UpdatedAtMS != before.UpdatedAtMS {
		t.Fatalf("identical reset timestamp = %d, want %d", after.UpdatedAtMS, before.UpdatedAtMS)
	}
	if fixture.priceRuntime.Load() == nil {
		t.Fatal("identical reset did not publish PriceRuntime")
	}
}

func TestResetModelPriceNotFoundAndCompileFailureDoNotPublish(t *testing.T) {
	fixture := newServiceFixture(t)
	if _, err := fixture.service.ResetModelPrice(t.Context(), 0); !errors.Is(err, app_errors.ErrBadRequest) {
		t.Fatalf("ResetModelPrice(0) error = %v, want BAD_REQUEST", err)
	}
	if _, err := fixture.service.ResetModelPrice(t.Context(), 999); !errors.Is(err, app_errors.ErrResourceNotFound) {
		t.Fatalf("ResetModelPrice(missing) error = %v, want NOT_FOUND", err)
	}

	scope, _ := pricing.ProviderScopeKey("openai")
	manual := int64(7_000_000_000)
	row := models.ModelPrice{
		PriceScopeKey: scope, ModelID: "target", IsManual: true,
		InputPriceNanoUSDPerMillionTokens: &manual,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	beforeRuntime := fixture.priceRuntime.Load()
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"target": {
				ID:   "target",
				Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1_000_000_000)}},
			},
		}},
	}})
	if err := fixture.db.Exec(
		`INSERT INTO model_prices (`+
			`price_scope_key, model_id, is_manual, created_at_ms, updated_at_ms`+
			`) VALUES (?, ?, ?, ?, ?)`,
		"invalid:scope", "corrupt", false, 1, 1,
	).Error; err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.ResetModelPrice(t.Context(), row.ID)
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("compile failure error = %v, want internal", err)
	}
	if fixture.priceRuntime.Load() != beforeRuntime {
		t.Fatal("failed reset replaced PriceRuntime")
	}
	var stored models.ModelPrice
	if err := fixture.db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.IsManual || stored.InputPriceNanoUSDPerMillionTokens == nil ||
		*stored.InputPriceNanoUSDPerMillionTokens != manual {
		t.Fatalf("failed reset did not roll back target: %#v", stored)
	}
}

func TestDeleteModelPriceReferencedConflictTakesPriorityWithCurrentCounts(t *testing.T) {
	for _, isManual := range []bool{false, true} {
		t.Run("manual="+strconv.FormatBool(isManual), func(t *testing.T) {
			fixture := newServiceFixture(t)
			providerID := "openai"
			createPriceTestGroup(t, fixture.db, models.Group{
				Name: "one", ProviderID: &providerID,
				UpstreamURL: "https://one.example/v1",
				Protocols:   models.JSON(`["openai-completions"]`),
				Models:      models.JSON(`[{"id":"used","alias":"one"},{"id":"used","alias":"two"}]`),
				Config:      models.JSON(`{}`), Enabled: false,
			})
			createPriceTestGroup(t, fixture.db, models.Group{
				Name: "two", ProviderID: &providerID,
				UpstreamURL: "https://two.example/v1",
				Protocols:   models.JSON(`["openai-completions"]`),
				Models:      models.JSON(`[{"id":"used","alias":"three"}]`),
				Config:      models.JSON(`{}`), Enabled: true,
			})
			scope, _ := pricing.ProviderScopeKey(providerID)
			row := models.ModelPrice{PriceScopeKey: scope, ModelID: "used", IsManual: isManual}
			if err := fixture.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			publishPersistedPriceTableForTest(t, fixture)
			beforeRuntime := fixture.priceRuntime.Load()

			err := fixture.service.DeleteModelPrice(t.Context(), row.ID)
			var apiErr *app_errors.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "MODEL_PRICE_REFERENCED" {
				t.Fatalf("DeleteModelPrice() error = %#v", err)
			}
			data, ok := apiErr.Data.(ModelPriceReferenceData)
			if !ok || data.ID != row.ID || data.ReferenceCount != 3 || data.ReferenceGroupCount != 2 {
				t.Fatalf("referenced conflict data = %#v", apiErr.Data)
			}
			if fixture.priceRuntime.Load() != beforeRuntime {
				t.Fatal("referenced rejection published PriceRuntime")
			}
			var count int64
			if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", row.ID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("referenced row count = %d, %v", count, err)
			}
		})
	}
}

func TestDeleteModelPriceRejectsUnreferencedAutomatic(t *testing.T) {
	fixture := newServiceFixture(t)
	scope, _ := pricing.ProviderScopeKey("openai")
	row := models.ModelPrice{PriceScopeKey: scope, ModelID: "automatic"}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	beforeRuntime := fixture.priceRuntime.Load()

	err := fixture.service.DeleteModelPrice(t.Context(), row.ID)
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "MODEL_PRICE_AUTOMATIC_DELETE_FORBIDDEN" {
		t.Fatalf("DeleteModelPrice() error = %#v", err)
	}
	data, ok := apiErr.Data.(ModelPriceIDData)
	if !ok || data.ID != row.ID {
		t.Fatalf("automatic conflict data = %#v", apiErr.Data)
	}
	if fixture.priceRuntime.Load() != beforeRuntime {
		t.Fatal("automatic rejection published PriceRuntime")
	}
}

func TestDeleteModelPriceRemovesUnreferencedManualProviderAndOrphanGroup(t *testing.T) {
	for _, scopeKey := range []string{"provider:openai", "group:999999"} {
		t.Run(scopeKey, func(t *testing.T) {
			fixture := newServiceFixture(t)
			value := int64(1_000_000_000)
			row := models.ModelPrice{
				PriceScopeKey: scopeKey, ModelID: "manual", IsManual: true,
				InputPriceNanoUSDPerMillionTokens: &value,
			}
			if err := fixture.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			publishPersistedPriceTableForTest(t, fixture)

			if err := fixture.service.DeleteModelPrice(t.Context(), row.ID); err != nil {
				t.Fatalf("DeleteModelPrice() error = %v", err)
			}
			var count int64
			if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", row.ID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("deleted row count = %d, %v", count, err)
			}
			if _, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{ScopeKey: scopeKey, ModelID: "manual"}); exists {
				t.Fatal("deleted rule remained published")
			}
		})
	}
}

func TestDeleteModelPriceInvalidNotFoundAndCompileFailureDoNotPublish(t *testing.T) {
	fixture := newServiceFixture(t)
	if err := fixture.service.DeleteModelPrice(t.Context(), 0); !errors.Is(err, app_errors.ErrBadRequest) {
		t.Fatalf("DeleteModelPrice(0) error = %v", err)
	}
	if err := fixture.service.DeleteModelPrice(t.Context(), 999); !errors.Is(err, app_errors.ErrResourceNotFound) {
		t.Fatalf("DeleteModelPrice(missing) error = %v", err)
	}
	scope, _ := pricing.ProviderScopeKey("openai")
	row := models.ModelPrice{PriceScopeKey: scope, ModelID: "target", IsManual: true}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publishPersistedPriceTableForTest(t, fixture)
	beforeRuntime := fixture.priceRuntime.Load()
	if err := fixture.db.Exec(
		`INSERT INTO model_prices (`+
			`price_scope_key, model_id, is_manual, created_at_ms, updated_at_ms`+
			`) VALUES (?, ?, ?, ?, ?)`,
		"invalid:scope", "corrupt", false, 1, 1,
	).Error; err != nil {
		t.Fatal(err)
	}

	err := fixture.service.DeleteModelPrice(t.Context(), row.ID)
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("compile failure error = %v, want internal", err)
	}
	if fixture.priceRuntime.Load() != beforeRuntime {
		t.Fatal("failed delete replaced PriceRuntime")
	}
	var count int64
	if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", row.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("rolled-back row count = %d, %v", count, err)
	}
}

func TestDeleteModelPriceWaitsForGroupWriteAndRechecksStaleListState(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider", ProviderID: &providerID,
		UpstreamURL: "https://provider.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[]`),
		Config:      models.JSON(`{}`), Enabled: true,
	})
	scope, _ := pricing.ProviderScopeKey(providerID)
	row := models.ModelPrice{PriceScopeKey: scope, ModelID: "new-reference", IsManual: true}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	stale, err := fixture.service.ListModelPrices(t.Context(), ModelPriceListQuery{
		Usage: ModelPriceUsageAll, Status: ModelPriceStatusAll, Page: 1, PageSize: 20,
	})
	if err != nil || len(stale.Items) != 1 || !stale.Items[0].CanDelete {
		t.Fatalf("stale list state = %#v, %v", stale, err)
	}

	groupAtUpdate := make(chan struct{})
	releaseGroup := make(chan struct{})
	var blockOnce sync.Once
	callbackName := "test:block_group_model_update_before_delete"
	if err := fixture.db.Callback().Update().Before("gorm:update").Register(callbackName, func(*gorm.DB) {
		block := false
		blockOnce.Do(func() { block = true })
		if block {
			close(groupAtUpdate)
			<-releaseGroup
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.db.Callback().Update().Remove(callbackName) })

	groupDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.UpdateGroupModels(t.Context(), group.ID, GroupModelsUpdateRequest{
			Models: optionalGroupModels{Set: true, Values: []GroupModel{{ID: "new-reference"}}},
		})
		groupDone <- err
	}()
	<-groupAtUpdate
	if fixture.service.writeMu.TryRLock() {
		fixture.service.writeMu.RUnlock()
		t.Fatal("Group write did not hold writeMu")
	}
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- fixture.service.DeleteModelPrice(t.Context(), row.ID) }()
	close(releaseGroup)
	if err := <-groupDone; err != nil {
		t.Fatalf("UpdateGroupModels() error = %v", err)
	}
	err = <-deleteDone
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "MODEL_PRICE_REFERENCED" {
		t.Fatalf("DeleteModelPrice() stale decision error = %#v", err)
	}
	data, ok := apiErr.Data.(ModelPriceReferenceData)
	if !ok || data.ReferenceCount != 1 || data.ReferenceGroupCount != 1 {
		t.Fatalf("latest reference data = %#v", apiErr.Data)
	}
	var count int64
	if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", row.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("referenced row count = %d, %v", count, err)
	}
}

func mustModelPriceUpdateRequest(t *testing.T, body string) ModelPriceUpdateRequest {
	t.Helper()
	request, apiErr := decodeModelPriceUpdateRequestForTest(body)
	if apiErr != nil {
		t.Fatalf("decode model price update: %v", apiErr)
	}
	return request
}

func publishPersistedPriceTableForTest(t *testing.T, fixture serviceFixture) {
	t.Helper()
	table, err := loadPriceTable(t.Context(), fixture.db)
	if err != nil {
		t.Fatalf("load initial PriceTable: %v", err)
	}
	fixture.priceRuntime.Publish(table)
}

func modelPriceGroupID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func containsJSONField(encoded []byte, field string) bool {
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return false
	}
	return walkJSONField(value, field)
}

func walkJSONField(value any, field string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if _, exists := typed[field]; exists {
			return true
		}
		for _, child := range typed {
			if walkJSONField(child, field) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if walkJSONField(child, field) {
				return true
			}
		}
	}
	return false
}

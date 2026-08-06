package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/catalog"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestModelsAPIProjectsClientModelTreeAndProtectsScopeKey(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	enabled := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "Enabled OpenAI", ProviderID: &providerID, Enabled: true,
		UpstreamURL: "https://enabled.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"gpt-upstream","alias":"client-gpt"}]`),
		Config:      models.JSON(`{}`),
	})
	disabled := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "Disabled OpenAI", ProviderID: &providerID, Enabled: false,
		UpstreamURL: "https://disabled.example/v1",
		Protocols:   models.JSON(`["openai-responses"]`),
		Models:      models.JSON(`[{"id":"gpt-upstream","alias":"hidden-client"}]`),
		Config:      models.JSON(`{}`),
	})
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", disabled.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	scopeKey, err := pricing.ProviderScopeKey(providerID)
	if err != nil {
		t.Fatal(err)
	}
	price := int64(1_000_000_000)
	if err := fixture.db.Create(&models.ModelPrice{
		PriceScopeKey: scopeKey, ModelID: "gpt-upstream",
		InputPriceNanoUSDPerMillionTokens: &price,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.ModelPrice{
		PriceScopeKey: scopeKey, ModelID: "unreferenced-model",
	}).Error; err != nil {
		t.Fatal(err)
	}
	contextLimit := int64(128000)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Name: "OpenAI", Models: map[string]catalog.Model{
			"gpt-upstream": {ID: "gpt-upstream", Name: "GPT Upstream Friendly", Metadata: catalog.ModelMetadata{
				Description: "Catalog description", Family: "gpt", Limits: catalog.ModelLimits{Context: &contextLimit},
				Modalities: catalog.ModelModalities{Input: []string{"text"}, Output: []string{"text"}},
				Status:     "active",
			}},
		}},
	}})
	coordinator := newCatalogSyncCoordinator(
		fixture.service,
		nil,
		"",
		catalog.Metadata{CheckedAtMillis: 111, SuccessfulFetchAtMillis: 222},
		true,
	)
	coordinator.last = CatalogSyncStatus{
		CheckedAtMS:         333,
		SuccessfulFetchAtMS: 222,
		ErrorCode:           "catalog_sync_failed",
	}

	initControlI18n(t)
	server := NewServer(&config.Config{AuthKey: authTestKey}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	request.Header.Set("Authorization", "Bearer "+authTestKey)
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/models status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	var payload struct {
		Data struct {
			Summary struct {
				ClientModelCount       int `json:"client_model_count"`
				UpstreamModelCount     int `json:"upstream_model_count"`
				PriceCount             int `json:"price_count"`
				PendingPriceCount      int `json:"pending_price_count"`
				UnreferencedPriceCount int `json:"unreferenced_price_count"`
			} `json:"summary"`
			Catalog struct {
				Available           bool   `json:"available"`
				CheckedAtMS         int64  `json:"checked_at_ms"`
				SuccessfulFetchAtMS int64  `json:"successful_fetch_at_ms"`
				ErrorCode           string `json:"error_code"`
			} `json:"catalog"`
			Items []struct {
				ClientModel    string   `json:"client_model"`
				Protocols      []string `json:"protocols"`
				UpstreamModels []struct {
					ModelID        string `json:"model_id"`
					AliasApplied   bool   `json:"alias_applied"`
					CatalogSummary *struct {
						Source       string `json:"source"`
						ProviderID   string `json:"provider_id"`
						ProviderName string `json:"provider_name"`
					} `json:"catalog_summary"`
					Prices []struct {
						RouteGroups []struct {
							ID      uint `json:"id"`
							Enabled bool `json:"enabled"`
						} `json:"route_groups"`
						AffectedGroups []struct {
							ID      uint `json:"id"`
							Enabled bool `json:"enabled"`
						} `json:"affected_groups"`
						CatalogReference *struct {
							Source       string `json:"source"`
							ProviderID   string `json:"provider_id"`
							ProviderName string `json:"provider_name"`
							Model        struct {
								Name        string `json:"name"`
								Description string `json:"description"`
								Family      string `json:"family"`
								Limits      struct {
									Context *int64 `json:"context"`
								} `json:"limits"`
							} `json:"model"`
						} `json:"catalog_reference"`
					} `json:"prices"`
				} `json:"upstream_models"`
			} `json:"items"`
			Pagination struct {
				Page       int64 `json:"page"`
				PageSize   int64 `json:"page_size"`
				TotalItems int64 `json:"total_items"`
				TotalPages int64 `json:"total_pages"`
			} `json:"pagination"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Items) != 1 || payload.Data.Items[0].ClientModel != "client-gpt" ||
		len(payload.Data.Items[0].Protocols) != 1 || payload.Data.Items[0].Protocols[0] != "openai-completions" {
		t.Fatalf("model tree = %#v", payload.Data.Items)
	}
	upstream := payload.Data.Items[0].UpstreamModels
	if len(upstream) != 1 || upstream[0].ModelID != "gpt-upstream" || !upstream[0].AliasApplied || len(upstream[0].Prices) != 1 {
		t.Fatalf("upstream models = %#v", upstream)
	}
	scope := upstream[0].Prices[0]
	if len(scope.RouteGroups) != 1 || scope.RouteGroups[0].ID != enabled.ID || !scope.RouteGroups[0].Enabled ||
		len(scope.AffectedGroups) != 2 || scope.CatalogReference == nil ||
		scope.CatalogReference.Source != "actual_provider" || scope.CatalogReference.ProviderID != "openai" ||
		scope.CatalogReference.ProviderName != "OpenAI" || scope.CatalogReference.Model.Name != "GPT Upstream Friendly" ||
		scope.CatalogReference.Model.Description != "Catalog description" || scope.CatalogReference.Model.Family != "gpt" ||
		scope.CatalogReference.Model.Limits.Context == nil || *scope.CatalogReference.Model.Limits.Context != contextLimit {
		t.Fatalf("price branch = %#v", scope)
	}
	affectedIDs := map[uint]bool{}
	for _, group := range scope.AffectedGroups {
		affectedIDs[group.ID] = group.Enabled
	}
	if len(affectedIDs) != 2 || !affectedIDs[enabled.ID] || affectedIDs[disabled.ID] {
		t.Fatalf("affected groups = %#v", scope.AffectedGroups)
	}
	if upstream[0].CatalogSummary == nil || upstream[0].CatalogSummary.ProviderID != "openai" {
		t.Fatalf("catalog summary = %#v", upstream[0].CatalogSummary)
	}
	if payload.Data.Summary.ClientModelCount != 1 || payload.Data.Summary.UpstreamModelCount != 1 ||
		payload.Data.Summary.PriceCount != 1 || payload.Data.Summary.PendingPriceCount != 0 ||
		payload.Data.Summary.UnreferencedPriceCount != 1 {
		t.Fatalf("summary = %#v", payload.Data.Summary)
	}
	if !payload.Data.Catalog.Available || payload.Data.Catalog.CheckedAtMS != 333 ||
		payload.Data.Catalog.SuccessfulFetchAtMS != 222 || payload.Data.Catalog.ErrorCode != "catalog_sync_failed" {
		t.Fatalf("catalog status = %#v", payload.Data.Catalog)
	}
	if strings.Contains(recorder.Body.String(), "price_scope_key") {
		t.Fatalf("response leaked price_scope_key: %s", recorder.Body.String())
	}
	if payload.Data.Pagination.Page != 1 || payload.Data.Pagination.PageSize != 20 ||
		payload.Data.Pagination.TotalItems != 1 || payload.Data.Pagination.TotalPages != 1 {
		t.Fatalf("pagination = %#v", payload.Data.Pagination)
	}

	searchRecorder := httptest.NewRecorder()
	searchRequest := httptest.NewRequest(http.MethodGet, "/api/models?q=friendly", nil)
	searchRequest.Header.Set("Authorization", "Bearer "+authTestKey)
	engine.ServeHTTP(searchRecorder, searchRequest)
	if searchRecorder.Code != http.StatusOK || !strings.Contains(searchRecorder.Body.String(), `"client_model":"client-gpt"`) {
		t.Fatalf("catalog name search = %d: %s", searchRecorder.Code, searchRecorder.Body.String())
	}

	filteredRecorder := httptest.NewRecorder()
	filteredRequest := httptest.NewRequest(http.MethodGet, "/api/models?q=missing&pricing_status=pending", nil)
	filteredRequest.Header.Set("Authorization", "Bearer "+authTestKey)
	engine.ServeHTTP(filteredRecorder, filteredRequest)
	if filteredRecorder.Code != http.StatusOK {
		t.Fatalf("filtered model collection = %d: %s", filteredRecorder.Code, filteredRecorder.Body.String())
	}
	var filtered struct {
		Data struct {
			Summary struct {
				ClientModelCount       int `json:"client_model_count"`
				PriceCount             int `json:"price_count"`
				UnreferencedPriceCount int `json:"unreferenced_price_count"`
			} `json:"summary"`
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(filteredRecorder.Body.Bytes(), &filtered); err != nil {
		t.Fatal(err)
	}
	if filtered.Data.Summary.ClientModelCount != 1 || filtered.Data.Summary.PriceCount != 1 ||
		filtered.Data.Summary.UnreferencedPriceCount != 1 || len(filtered.Data.Items) != 0 {
		t.Fatalf("filtered summary = %#v", filtered.Data)
	}
}

func TestModelsAPICatalogReferenceUsesMatchedProviderThenMetadataFallback(t *testing.T) {
	fixture := newServiceFixture(t)
	missingProviderID := "missing-provider"
	automaticGroup := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "Automatic route", ProviderID: &missingProviderID, Enabled: true,
		UpstreamURL: "https://automatic.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"shared-model","alias":"auto-client"}]`),
		Config:      models.JSON(`{}`),
	})
	manualGroup := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "Manual route", Enabled: true,
		UpstreamURL: "https://manual.example/v1",
		Protocols:   models.JSON(`["anthropic"]`),
		Models:      models.JSON(`[{"id":"manual-model","alias":"manual-client"}]`),
		Config:      models.JSON(`{}`),
	})
	automaticScope, err := pricing.ProviderScopeKey(missingProviderID)
	if err != nil {
		t.Fatal(err)
	}
	manualScope, err := pricing.GroupScopeKey(manualGroup.ID)
	if err != nil {
		t.Fatal(err)
	}
	automaticPrice := int64(7)
	manualPrice := int64(9)
	if err := fixture.db.Create(&models.ModelPrice{
		PriceScopeKey: automaticScope, ModelID: "shared-model",
		InputPriceNanoUSDPerMillionTokens: &automaticPrice,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.ModelPrice{
		PriceScopeKey: manualScope, ModelID: "manual-model", IsManual: true,
		InputPriceNanoUSDPerMillionTokens: &manualPrice,
	}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai", Name: "OpenAI", Models: map[string]catalog.Model{
				"shared-model": {ID: "shared-model", Name: "Wrong metadata candidate"},
				"manual-model": {ID: "manual-model", Name: "Readable Manual Model"},
			},
		},
		"anthropic": {
			ID: "anthropic", Name: "Anthropic", Models: map[string]catalog.Model{
				"shared-model": {
					ID: "shared-model", Name: "Matched Provider Model",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{
						Input: pricing.Price{NanoUSDPerMillion: pricing.NanoUSD(automaticPrice), Set: true},
					}},
				},
			},
		},
	}})

	initControlI18n(t)
	server := NewServer(&config.Config{AuthKey: authTestKey}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	request.Header.Set("Authorization", "Bearer "+authTestKey)
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/models status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Items []struct {
				ClientModel    string `json:"client_model"`
				UpstreamModels []struct {
					Prices []struct {
						Price struct {
							MatchedProviderID *string `json:"matched_provider_id"`
						} `json:"price"`
						CatalogReference *struct {
							Source     string `json:"source"`
							ProviderID string `json:"provider_id"`
							Model      struct {
								Name string `json:"name"`
							} `json:"model"`
						} `json:"catalog_reference"`
					} `json:"prices"`
				} `json:"upstream_models"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	byClient := make(map[string]struct {
		MatchedProviderID *string
		Reference         string
		Source            string
		Name              string
	})
	for _, item := range payload.Data.Items {
		if len(item.UpstreamModels) != 1 || len(item.UpstreamModels[0].Prices) != 1 ||
			item.UpstreamModels[0].Prices[0].CatalogReference == nil {
			t.Fatalf("model item = %#v", item)
		}
		branch := item.UpstreamModels[0].Prices[0]
		byClient[item.ClientModel] = struct {
			MatchedProviderID *string
			Reference         string
			Source            string
			Name              string
		}{
			MatchedProviderID: branch.Price.MatchedProviderID,
			Reference:         branch.CatalogReference.ProviderID,
			Source:            branch.CatalogReference.Source,
			Name:              branch.CatalogReference.Model.Name,
		}
	}
	automatic := byClient["auto-client"]
	if automatic.MatchedProviderID == nil || *automatic.MatchedProviderID != "anthropic" ||
		automatic.Reference != "anthropic" || automatic.Source != "reference_provider" ||
		automatic.Name != "Matched Provider Model" {
		t.Fatalf("automatic reference = %#v", automatic)
	}
	manual := byClient["manual-client"]
	if manual.MatchedProviderID != nil || manual.Reference != "openai" ||
		manual.Source != "reference_provider" || manual.Name != "Readable Manual Model" {
		t.Fatalf("manual reference = %#v", manual)
	}
	if automaticGroup.ID == 0 {
		t.Fatal("automatic group was not persisted")
	}

	searchRecorder := httptest.NewRecorder()
	searchRequest := httptest.NewRequest(http.MethodGet, "/api/models?q=readable", nil)
	searchRequest.Header.Set("Authorization", "Bearer "+authTestKey)
	engine.ServeHTTP(searchRecorder, searchRequest)
	if searchRecorder.Code != http.StatusOK ||
		!strings.Contains(searchRecorder.Body.String(), `"client_model":"manual-client"`) ||
		strings.Contains(searchRecorder.Body.String(), `"client_model":"auto-client"`) {
		t.Fatalf("catalog fallback search = %d: %s", searchRecorder.Code, searchRecorder.Body.String())
	}
}

func TestModelsAPIValidatesFiltersAndPaginatesClientModelRoots(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "Anthropic group", Enabled: true, UpstreamURL: "https://anthropic.example/v1",
		Protocols: models.JSON(`["anthropic"]`),
		Models:    models.JSON(`[{"id":"first","alias":"a-root"},{"id":"second","alias":"b-root"}]`),
		Config:    models.JSON(`{}`),
	})
	scope, err := pricing.GroupScopeKey(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, modelID := range []string{"first", "second"} {
		if err := fixture.db.Create(&models.ModelPrice{PriceScopeKey: scope, ModelID: modelID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	initControlI18n(t)
	server := NewServer(&config.Config{AuthKey: authTestKey}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)

	for _, path := range []string{
		"/api/models?group_status=wrong", "/api/models?pricing_status=wrong",
		"/api/models?group_status=disabled", "/api/models?protocol=anthropic",
		"/api/models?page=0", "/api/models?page_size=101",
		"/api/models?q=a&q=b",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+authTestKey)
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400: %s", path, recorder.Code, recorder.Body.String())
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/models?page=2&page_size=1", nil)
	request.Header.Set("Authorization", "Bearer "+authTestKey)
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("paginated models status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Items []struct {
				ClientModel string `json:"client_model"`
			} `json:"items"`
			Pagination struct {
				TotalItems int64 `json:"total_items"`
				TotalPages int64 `json:"total_pages"`
			} `json:"pagination"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Items) != 1 || payload.Data.Items[0].ClientModel != "b-root" ||
		payload.Data.Pagination.TotalItems != 2 || payload.Data.Pagination.TotalPages != 2 {
		t.Fatalf("root pagination = %#v", payload.Data)
	}
}

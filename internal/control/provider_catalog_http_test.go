package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/catalog"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

func TestProviderSuggestionsUseOfficialFallbacksMergeCatalogAndStayBounded(t *testing.T) {
	fixture := newServiceFixture(t)
	withoutCatalog, err := fixture.service.ListProviderSuggestions(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	wantWithoutCatalogCount := 3 + len(catalog.CuratedProviders())
	if len(withoutCatalog.Items) != wantWithoutCatalogCount || withoutCatalog.Items[0].ProviderID != "openai" ||
		withoutCatalog.Items[1].ProviderID != "anthropic" || withoutCatalog.Items[2].ProviderID != "google" {
		t.Fatalf("fallback suggestions = %#v, want %d items (3 official + full curated table)",
			withoutCatalog.Items, wantWithoutCatalogCount)
	}

	providers := map[string]catalog.Provider{
		"openai": {
			ID: "openai", Name: "OpenAI Catalog", APIURL: "https://catalog.openai.example/v1",
			Models: map[string]catalog.Model{},
		},
	}
	for index := 0; index < providerSuggestionCatalogLimit+5; index++ {
		id := "provider-" + strings.Repeat("a", index/26) + string(rune('a'+index%26))
		providers[id] = catalog.Provider{
			ID: id, Name: "Provider " + id, APIURL: "https://" + id + ".example/v1",
			NPM: "@ai-sdk/openai-compatible", Models: map[string]catalog.Model{},
		}
	}
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: providers})
	empty, err := fixture.service.ListProviderSuggestions(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	wantEmptyCount := 3 + len(catalog.CuratedProviders())
	if len(empty.Items) != wantEmptyCount {
		t.Fatalf("empty search suggestions = %d, want three official plus full curated table (%d)",
			len(empty.Items), wantEmptyCount)
	}
	for _, item := range empty.Items[3:] {
		if item.Source != ProviderSuggestionSourceCurated {
			t.Fatalf("empty search suggestion %#v, want source=curated (models.dev catalog stays hidden until searched)", item)
		}
	}
	got, err := fixture.service.ListProviderSuggestions(t.Context(), "provider")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 3+providerSuggestionCatalogLimit || got.Items[0].Name != "OpenAI Catalog" ||
		got.Items[0].APIURL != "https://catalog.openai.example/v1" ||
		got.Items[0].Source != ProviderSuggestionSourceOfficial ||
		len(got.Items[0].Protocols) != 2 || got.Items[0].Protocols[0] != protocol.OpenAICompletions {
		t.Fatalf("merged suggestions = %#v", got.Items)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"svg", `"models"`, `"npm"`} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("suggestions expose %q: %s", forbidden, encoded)
		}
	}
}

func TestProviderSuggestionsCuratedOutranksCatalogForSharedID(t *testing.T) {
	fixture := newServiceFixture(t)
	curated := catalog.CuratedProviders()
	if len(curated) == 0 {
		t.Fatal("catalog.CuratedProviders() is empty, cannot exercise dedup priority")
	}
	sharedID := curated[0].ID

	// A models.dev snapshot entry sharing a curated provider's ID must lose
	// the display metadata race: the curated entry's Name/APIURL/Mark/Source
	// wins, and the catalog duplicate never appears as a second item.
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		sharedID: {
			ID:     sharedID,
			Name:   "Should Not Win",
			APIURL: "https://should-not-win.example/v1",
			Models: map[string]catalog.Model{},
		},
	}})

	got, err := fixture.service.ListProviderSuggestions(t.Context(), sharedID)
	if err != nil {
		t.Fatal(err)
	}
	matches := 0
	for _, item := range got.Items {
		if item.ProviderID != sharedID {
			continue
		}
		matches++
		if item.Source != ProviderSuggestionSourceCurated || item.Name == "Should Not Win" {
			t.Fatalf("shared-ID suggestion = %#v, want curated metadata to win", item)
		}
	}
	if matches != 1 {
		t.Fatalf("shared-ID provider %q appeared %d times, want exactly 1 (deduplicated)", sharedID, matches)
	}
}

func TestValidateSelectableProviderIDAllowsCuratedProviderIDs(t *testing.T) {
	fixture := newServiceFixture(t)
	for _, provider := range catalog.CuratedProviders() {
		id := provider.ID
		if err := fixture.service.validateSelectableProviderID(&id); err != nil {
			t.Fatalf("validateSelectableProviderID(%q) = %v, want nil for curated provider", id, err)
		}
	}
	unknown := "no-such-provider-anywhere"
	if err := fixture.service.validateSelectableProviderID(&unknown); err == nil {
		t.Fatal("validateSelectableProviderID(unknown) = nil, want an error")
	}
}

func TestProviderSuggestionsEncodeUnknownProtocolsAsEmptyArray(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"custom-provider": {
			ID:     "custom-provider",
			Name:   "Custom Provider",
			APIURL: "https://custom.example/v1",
			NPM:    "@vendor/unknown-sdk",
			Models: map[string]catalog.Model{},
		},
	}})

	got, err := fixture.service.ListProviderSuggestions(t.Context(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"protocols":null`) ||
		!strings.Contains(string(encoded), `"provider_id":"custom-provider"`) ||
		!strings.Contains(string(encoded), `"protocols":[]`) {
		t.Fatalf("unknown provider protocols = %s, want an empty JSON array", encoded)
	}
}

func TestProviderModelsAreStrictBoundedAndUsePersistedOrCatalogCandidatePricingStatus(t *testing.T) {
	fixture := newServiceFixture(t)
	providerModels := make(map[string]catalog.Model)
	for index := 0; index < providerModelResultLimit+5; index++ {
		id := "model-" + strings.Repeat("a", index/26) + string(rune('a'+index%26))
		providerModels[id] = catalog.Model{ID: id, Name: "Model " + id}
	}
	providerModels["configured"] = catalog.Model{ID: "configured", Name: "Configured"}
	providerModels["catalog-priced"] = catalog.Model{
		ID: "catalog-priced", Name: "Catalog Priced",
		Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}},
	}
	providerModels["pending"] = catalog.Model{ID: "pending", Name: "Pending"}
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Name: "OpenAI", Models: providerModels},
	}})
	configuredValue := int64(0)
	for _, row := range []models.ModelPrice{
		{PriceScopeKey: "provider:openai", ModelID: "configured", InputPriceNanoUSDPerMillionTokens: &configuredValue},
		{PriceScopeKey: "provider:openai", ModelID: "pending"},
	} {
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	got, err := fixture.service.ListProviderModels(t.Context(), "openai", ProviderModelQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != providerModelResultLimit || got.Total != len(providerModels) {
		t.Fatalf("provider model bounds = %d/%d, want %d/%d", len(got.Items), got.Total, providerModelResultLimit, len(providerModels))
	}
	configured, err := fixture.service.ListProviderModels(t.Context(), "openai", ProviderModelQuery{
		Query: "configured", Status: PricingStatusConfigured,
	})
	if err != nil || len(configured.Items) != 1 || configured.Items[0].PricingStatus != PricingStatusConfigured {
		t.Fatalf("configured filter = %#v, %v", configured, err)
	}
	catalogConfigured, err := fixture.service.ListProviderModels(t.Context(), "openai", ProviderModelQuery{
		Query: "catalog-priced", Status: PricingStatusConfigured,
	})
	if err != nil || len(catalogConfigured.Items) != 1 ||
		catalogConfigured.Items[0].PricingStatus != PricingStatusConfigured {
		t.Fatalf("catalog configured filter = %#v, %v", catalogConfigured, err)
	}
	pending, err := fixture.service.ListProviderModels(t.Context(), "openai", ProviderModelQuery{
		Query: "pending", Status: PricingStatusPending,
	})
	if err != nil || len(pending.Items) != 1 || pending.Items[0].PricingStatus != PricingStatusPending {
		t.Fatalf("pending filter = %#v, %v", pending, err)
	}
	for _, providerID := range []string{"OpenAI", "openai:alias", "missing"} {
		if _, err := fixture.service.ListProviderModels(t.Context(), providerID, ProviderModelQuery{}); err == nil {
			t.Fatalf("ListProviderModels(%q) error = nil", providerID)
		}
	}
}

func TestProviderCatalogRoutesRequireAuthRejectInvalidQueriesAndSanitizeSyncFailure(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Name: "OpenAI", Models: map[string]catalog.Model{}},
	}})
	const rawFailure = "secret upstream response body"
	client := catalogSyncClientFunc(func(context.Context, catalog.Metadata) (catalog.SyncResult, error) {
		return catalog.SyncResult{}, errors.New(rawFailure)
	})
	coordinator := newCatalogSyncCoordinator(fixture.service, client, "unused", catalog.Metadata{
		CheckedAtMillis: 100, SuccessfulFetchAtMillis: 90,
	}, true)
	coordinator.now = func() time.Time { return time.UnixMilli(250) }
	var serviceLogs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&serviceLogs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)

	unauthorized := serveProviderCatalogRequest(engine, http.MethodGet, "/api/provider-suggestions", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	for _, path := range []string{
		"/api/provider-suggestions?unknown=1",
		"/api/provider-suggestions?q=a&q=b",
		"/api/providers/OpenAI/models",
		"/api/providers/openai/models?status=priced",
	} {
		response := serveProviderCatalogRequest(engine, http.MethodGet, path, authTestKey)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d %s, want 400", path, response.Code, response.Body.String())
		}
	}

	syncResponse := serveProviderCatalogRequest(engine, http.MethodPost, "/api/model-prices/sync", authTestKey)
	if syncResponse.Code != http.StatusBadGateway ||
		strings.Contains(syncResponse.Body.String(), rawFailure) {
		t.Fatalf("sync failure = %d %s", syncResponse.Code, syncResponse.Body.String())
	}
	var envelope struct {
		Code string            `json:"code"`
		Data CatalogSyncStatus `json:"data"`
	}
	if err := json.Unmarshal(syncResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != "BAD_GATEWAY" || envelope.Data.Trigger != CatalogSyncManual ||
		envelope.Data.CheckedAtMS != 250 || envelope.Data.SuccessfulFetchAtMS != 90 ||
		envelope.Data.ErrorCode != "catalog_sync_failed" || envelope.Data.NotModified ||
		envelope.Data.Skipped {
		t.Fatalf("sync failure envelope = %#v", envelope)
	}
	assertControlLogExcludes(t, serviceLogs.String(), rawFailure)
}

func serveProviderCatalogRequest(engine *gin.Engine, method, path, authKey string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	request.Header.Set("Accept-Language", "en-US")
	engine.ServeHTTP(recorder, request)
	return recorder
}

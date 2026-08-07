package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/catalog"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestModelPriceFinalRoutesRequireManagementAuthentication(t *testing.T) {
	fixture, engine, row := newModelPriceHTTPFixture(t, true)
	_ = fixture
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/model-prices?usage=all"},
		{
			method: http.MethodPut,
			path:   fmt.Sprintf("/api/model-prices/%d", row.ID),
			body:   modelPriceHTTPUpdateBody(`"1"`, "false"),
		},
		{method: http.MethodPost, path: fmt.Sprintf("/api/model-prices/%d/reset", row.ID), body: `{}`},
		{method: http.MethodDelete, path: fmt.Sprintf("/api/model-prices/%d", row.ID)},
	} {
		recorder := serveModelPriceHTTPRequest(engine, request.method, request.path, request.body, "")
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401: %s", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestModelPriceHTTPListAndUpdateUseFinalWireContract(t *testing.T) {
	fixture, engine, row := newModelPriceHTTPFixture(t, true)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {ID: "openai", Models: map[string]catalog.Model{
			"gpt-wire": {ID: "gpt-wire", Cost: &catalog.ModelCost{
				Prices: pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 3_000_000_000, Set: true}},
			}},
		}},
	}})

	listResponse := serveModelPriceHTTPRequest(
		engine,
		http.MethodGet,
		"/api/model-prices?usage=all&status=all&page=1&page_size=20",
		"",
		authTestKey,
	)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("GET /api/model-prices status = %d: %s", listResponse.Code, listResponse.Body.String())
	}
	listData := decodeModelPriceHTTPData(t, listResponse)
	items, ok := listData["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("list items = %#v", listData["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["id"] != float64(row.ID) || item["model_id"] != "gpt-wire" {
		t.Fatalf("list item identity = %#v", items[0])
	}
	prices, ok := item["prices"].(map[string]any)
	if !ok || prices["input"] != "2.5" || prices["output"] != nil {
		t.Fatalf("list price wire = %#v", item["prices"])
	}
	if matchedProviderID, exists := item["matched_provider_id"]; !exists || matchedProviderID != "openai" {
		t.Fatalf("list matched provider wire = %#v", item)
	}
	if _, leaked := item["price_scope_key"]; leaked {
		t.Fatalf("list leaked internal scope: %#v", item)
	}
	if _, leaked := item["has_context_tiers"]; leaked {
		t.Fatalf("list leaked removed has_context_tiers field: %#v", item)
	}
	contextTiers, ok := item["context_tiers"].([]any)
	if !ok || len(contextTiers) != 0 {
		t.Fatalf("list context_tiers wire = %#v", item["context_tiers"])
	}

	updateResponse := serveModelPriceHTTPRequest(
		engine,
		http.MethodPut,
		fmt.Sprintf("/api/model-prices/%d", row.ID),
		modelPriceHTTPUpdateBody(`"0"`, "false"),
		authTestKey,
	)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("PUT /api/model-prices/:id status = %d: %s", updateResponse.Code, updateResponse.Body.String())
	}
	updated := decodeModelPriceHTTPData(t, updateResponse)
	updatedPrices, ok := updated["prices"].(map[string]any)
	if !ok || updatedPrices["input"] != "0" || updatedPrices["output"] != nil {
		t.Fatalf("updated price wire = %#v", updated["prices"])
	}
	if updated["method"] != "user_override" || updated["id"] != float64(row.ID) {
		t.Fatalf("updated ownership wire = %#v", updated)
	}
	if matchedProviderID, exists := updated["matched_provider_id"]; !exists || matchedProviderID != nil {
		t.Fatalf("updated matched provider wire = %#v", updated)
	}

	conflictResponse := serveModelPriceHTTPRequest(
		engine,
		http.MethodPut,
		fmt.Sprintf("/api/model-prices/%d", row.ID),
		modelPriceHTTPUpdateBody("null", "false"),
		authTestKey,
	)
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("unpriced PUT status = %d, want 409: %s", conflictResponse.Code, conflictResponse.Body.String())
	}
	var conflict struct {
		Code string `json:"code"`
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(conflictResponse.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Code != "MODEL_PRICE_UNPRICED_CONFIRMATION_REQUIRED" || conflict.Data.ID != row.ID {
		t.Fatalf("unpriced conflict = %#v", conflict)
	}
}

func TestModelPriceHTTPUpdateAcceptsAndPersistsContextTiers(t *testing.T) {
	_, engine, row := newModelPriceHTTPFixture(t, true)

	tieredBody := `{"input":"1","output":null,"cache_read":null,"cache_write":null,"confirm_unpriced":false,` +
		`"context_tiers":[` +
		`{"threshold_tokens":1000,"input":"2","output":null,"cache_read":null,"cache_write":null},` +
		`{"threshold_tokens":272000,"input":"3","output":null,"cache_read":null,"cache_write":null}` +
		`]}`
	updateResponse := serveModelPriceHTTPRequest(
		engine, http.MethodPut, fmt.Sprintf("/api/model-prices/%d", row.ID), tieredBody, authTestKey,
	)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("PUT with context_tiers status = %d: %s", updateResponse.Code, updateResponse.Body.String())
	}
	updated := decodeModelPriceHTTPData(t, updateResponse)
	tiers, ok := updated["context_tiers"].([]any)
	if !ok || len(tiers) != 2 {
		t.Fatalf("updated context_tiers = %#v", updated["context_tiers"])
	}
	firstTier, ok := tiers[0].(map[string]any)
	if !ok || firstTier["threshold_tokens"] != float64(1000) {
		t.Fatalf("first tier = %#v", tiers[0])
	}
	firstTierPrices, ok := firstTier["prices"].(map[string]any)
	if !ok || firstTierPrices["input"] != "2" || firstTierPrices["output"] != nil {
		t.Fatalf("first tier prices = %#v", firstTier["prices"])
	}

	listResponse := serveModelPriceHTTPRequest(
		engine, http.MethodGet, "/api/model-prices?usage=all&status=all&page=1&page_size=20", "", authTestKey,
	)
	listData := decodeModelPriceHTTPData(t, listResponse)
	items, ok := listData["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("list items = %#v", listData["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("list item = %#v", items[0])
	}
	persistedTiers, ok := item["context_tiers"].([]any)
	if !ok || len(persistedTiers) != 2 {
		t.Fatalf("persisted context_tiers = %#v", item["context_tiers"])
	}

	for _, malformed := range []struct {
		name string
		body string
	}{
		{
			name: "non increasing thresholds",
			body: `{"input":"1","output":null,"cache_read":null,"cache_write":null,"confirm_unpriced":false,` +
				`"context_tiers":[` +
				`{"threshold_tokens":272000,"input":"3","output":null,"cache_read":null,"cache_write":null},` +
				`{"threshold_tokens":1000,"input":"2","output":null,"cache_read":null,"cache_write":null}` +
				`]}`,
		},
		{
			name: "tier without any price",
			body: `{"input":"1","output":null,"cache_read":null,"cache_write":null,"confirm_unpriced":false,` +
				`"context_tiers":[{"threshold_tokens":1000,"input":null,"output":null,"cache_read":null,"cache_write":null}]}`,
		},
	} {
		t.Run(malformed.name, func(t *testing.T) {
			response := serveModelPriceHTTPRequest(
				engine, http.MethodPut, fmt.Sprintf("/api/model-prices/%d", row.ID), malformed.body, authTestKey,
			)
			assertModelPriceHTTPError(t, response, http.StatusBadRequest, "VALIDATION_FAILED", nil)

			unchanged := serveModelPriceHTTPRequest(
				engine, http.MethodGet, "/api/model-prices?usage=all&status=all&page=1&page_size=20", "", authTestKey,
			)
			unchangedData := decodeModelPriceHTTPData(t, unchanged)
			unchangedItems, ok := unchangedData["items"].([]any)
			if !ok || len(unchangedItems) != 1 {
				t.Fatalf("unchanged items = %#v", unchangedData["items"])
			}
			unchangedItem, ok := unchangedItems[0].(map[string]any)
			if !ok {
				t.Fatalf("unchanged item = %#v", unchangedItems[0])
			}
			unchangedTiers, ok := unchangedItem["context_tiers"].([]any)
			if !ok || len(unchangedTiers) != 2 {
				t.Fatalf("rejected update mutated tiers = %#v", unchangedItem["context_tiers"])
			}
		})
	}
}

func TestModelPriceHTTPResetDeleteAndStableErrorData(t *testing.T) {
	t.Run("reset returns the complete pending row", func(t *testing.T) {
		_, engine, row := newModelPriceHTTPFixture(t, true)
		response := serveModelPriceHTTPRequest(
			engine,
			http.MethodPost,
			fmt.Sprintf("/api/model-prices/%d/reset", row.ID),
			`{}`,
			authTestKey,
		)
		if response.Code != http.StatusOK {
			t.Fatalf("reset status = %d: %s", response.Code, response.Body.String())
		}
		data := decodeModelPriceHTTPData(t, response)
		prices, ok := data["prices"].(map[string]any)
		if !ok || prices["input"] != nil || data["method"] != nil || data["pricing_status"] != "pending" {
			t.Fatalf("reset wire = %#v", data)
		}
		if matchedProviderID, exists := data["matched_provider_id"]; !exists || matchedProviderID != nil {
			t.Fatalf("reset matched provider wire = %#v", data)
		}

	})

	t.Run("automatic delete conflict is structured", func(t *testing.T) {
		_, engine, row := newModelPriceHTTPFixture(t, true)
		response := serveModelPriceHTTPRequest(
			engine,
			http.MethodDelete,
			fmt.Sprintf("/api/model-prices/%d", row.ID),
			"",
			authTestKey,
		)
		assertModelPriceHTTPError(t, response, http.StatusConflict,
			"MODEL_PRICE_AUTOMATIC_DELETE_FORBIDDEN", map[string]any{"id": float64(row.ID)})
	})

	t.Run("referenced delete conflict includes both counts", func(t *testing.T) {
		fixture, engine, row := newModelPriceHTTPFixture(t, true)
		providerID := "openai"
		createPriceTestGroup(t, fixture.db, models.Group{
			Name: "reference-one", ProviderID: &providerID,
			UpstreamURL: "https://one.example/v1",
			Protocols:   models.JSON(`["openai-completions"]`),
			Models:      models.JSON(`[{"id":"gpt-wire","alias":"one"},{"id":"gpt-wire","alias":"two"}]`),
			Config:      models.JSON(`{}`), Enabled: true,
		})
		createPriceTestGroup(t, fixture.db, models.Group{
			Name: "reference-two", ProviderID: &providerID,
			UpstreamURL: "https://two.example/v1",
			Protocols:   models.JSON(`["openai-completions"]`),
			Models:      models.JSON(`[{"id":"gpt-wire","alias":"three"}]`),
			Config:      models.JSON(`{}`), Enabled: false,
		})
		response := serveModelPriceHTTPRequest(
			engine,
			http.MethodDelete,
			fmt.Sprintf("/api/model-prices/%d", row.ID),
			"",
			authTestKey,
		)
		assertModelPriceHTTPError(t, response, http.StatusConflict,
			"MODEL_PRICE_REFERENCED", map[string]any{
				"id": float64(row.ID), "reference_count": float64(3), "reference_group_count": float64(2),
			})
	})

	t.Run("manual unreferenced row is deleted", func(t *testing.T) {
		fixture, engine, row := newModelPriceHTTPFixture(t, true)
		if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", row.ID).
			Update("is_manual", true).Error; err != nil {
			t.Fatal(err)
		}
		response := serveModelPriceHTTPRequest(
			engine,
			http.MethodDelete,
			fmt.Sprintf("/api/model-prices/%d", row.ID),
			"",
			authTestKey,
		)
		if response.Code != http.StatusOK {
			t.Fatalf("delete status = %d: %s", response.Code, response.Body.String())
		}
		var count int64
		if err := fixture.db.Model(&models.ModelPrice{}).Where("id = ?", row.ID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("deleted row count = %d, err = %v", count, err)
		}
	})

	t.Run("missing item uses stable not found", func(t *testing.T) {
		_, engine, _ := newModelPriceHTTPFixture(t, false)
		response := serveModelPriceHTTPRequest(
			engine,
			http.MethodPost,
			"/api/model-prices/999/reset",
			"",
			authTestKey,
		)
		assertModelPriceHTTPError(t, response, http.StatusNotFound, "NOT_FOUND", nil)
	})
}

func TestModelPriceHTTPRejectsLegacyAndAmbiguousContracts(t *testing.T) {
	_, engine, row := newModelPriceHTTPFixture(t, true)
	for _, request := range []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode string
	}{
		{name: "legacy list query", method: http.MethodGet, path: "/api/model-prices?pattern=gpt", wantCode: "BAD_REQUEST"},
		{name: "forced empty query", method: http.MethodGet, path: "/api/model-prices?", wantCode: "BAD_REQUEST"},
		{name: "non canonical id", method: http.MethodPut, path: "/api/model-prices/01", body: modelPriceHTTPUpdateBody(`"1"`, "false"), wantCode: "BAD_REQUEST"},
		{name: "mutation query", method: http.MethodPut, path: fmt.Sprintf("/api/model-prices/%d?pattern=legacy", row.ID), body: modelPriceHTTPUpdateBody(`"1"`, "false"), wantCode: "BAD_REQUEST"},
		{name: "number price", method: http.MethodPut, path: fmt.Sprintf("/api/model-prices/%d", row.ID), body: modelPriceHTTPUpdateBody("1", "false"), wantCode: "INVALID_JSON"},
		{name: "invalid decimal", method: http.MethodPut, path: fmt.Sprintf("/api/model-prices/%d", row.ID), body: modelPriceHTTPUpdateBody(`"1e3"`, "false"), wantCode: "VALIDATION_FAILED"},
		{name: "missing full replacement slot", method: http.MethodPut, path: fmt.Sprintf("/api/model-prices/%d", row.ID), body: `{"input":"1","output":null,"cache_read":null}`, wantCode: "VALIDATION_FAILED"},
		{name: "identity field", method: http.MethodPut, path: fmt.Sprintf("/api/model-prices/%d", row.ID), body: `{"input":"1","output":null,"cache_read":null,"cache_write":null,"model_id":"secret"}`, wantCode: "INVALID_JSON"},
		{name: "reset query", method: http.MethodPost, path: fmt.Sprintf("/api/model-prices/%d/reset?force=true", row.ID), body: `{}`, wantCode: "BAD_REQUEST"},
		{name: "reset nonempty body", method: http.MethodPost, path: fmt.Sprintf("/api/model-prices/%d/reset", row.ID), body: `{"force":true}`, wantCode: "INVALID_JSON"},
		{name: "delete query", method: http.MethodDelete, path: fmt.Sprintf("/api/model-prices/%d?force=true", row.ID), wantCode: "BAD_REQUEST"},
	} {
		t.Run(request.name, func(t *testing.T) {
			response := serveModelPriceHTTPRequest(engine, request.method, request.path, request.body, authTestKey)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
			}
			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Code != request.wantCode {
				t.Fatalf("code = %q, want %q: %s", envelope.Code, request.wantCode, response.Body.String())
			}
		})
	}

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		response := serveModelPriceHTTPRequest(engine, method, "/api/model-prices", "", "")
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
			t.Fatalf("legacy %s collection = %d Allow %q: %s", method, response.Code, response.Header().Get("Allow"), response.Body.String())
		}
	}
}

func TestModelPriceHTTPErrorsUseThreeLocaleMessageIDs(t *testing.T) {
	for _, test := range []struct {
		language string
		message  string
	}{
		{language: "zh-CN", message: "将模型价格标记为未定价需要明确确认"},
		{language: "en-US", message: "Marking a model price as unpriced requires explicit confirmation"},
		{language: "ja-JP", message: "モデル価格を未設定としてマークするには明示的な確認が必要です"},
	} {
		t.Run(test.language, func(t *testing.T) {
			_, engine, row := newModelPriceHTTPFixture(t, true)
			response := serveModelPriceHTTPRequestWithLanguage(
				engine,
				http.MethodPut,
				fmt.Sprintf("/api/model-prices/%d", row.ID),
				modelPriceHTTPUpdateBody("null", "false"),
				authTestKey,
				test.language,
			)
			var envelope struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusConflict || envelope.Message != test.message {
				t.Fatalf("localized response = %d %s, want %q", response.Code, response.Body.String(), test.message)
			}
		})
	}
}

func newModelPriceHTTPFixture(
	t *testing.T,
	seed bool,
) (serviceFixture, *gin.Engine, models.ModelPrice) {
	t.Helper()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	var row models.ModelPrice
	if seed {
		scope, err := pricing.ProviderScopeKey("openai")
		if err != nil {
			t.Fatal(err)
		}
		input := int64(2_500_000_000)
		row = models.ModelPrice{
			PriceScopeKey:                     scope,
			ModelID:                           "gpt-wire",
			InputPriceNanoUSDPerMillionTokens: &input,
		}
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	return fixture, engine, row
}

func serveModelPriceHTTPRequest(
	engine *gin.Engine,
	method string,
	path string,
	body string,
	authKey string,
) *httptest.ResponseRecorder {
	return serveModelPriceHTTPRequestWithLanguage(engine, method, path, body, authKey, "")
}

func serveModelPriceHTTPRequestWithLanguage(
	engine *gin.Engine,
	method string,
	path string,
	body string,
	authKey string,
	language string,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	if language != "" {
		request.Header.Set("Accept-Language", language)
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertModelPriceHTTPError(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
	wantData map[string]any,
) {
	t.Helper()
	var envelope struct {
		Code string         `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v: %s", err, recorder.Body.String())
	}
	if recorder.Code != wantStatus || envelope.Code != wantCode {
		t.Fatalf("error response = %d %#v, want %d %s", recorder.Code, envelope, wantStatus, wantCode)
	}
	if len(wantData) != len(envelope.Data) {
		t.Fatalf("error data = %#v, want %#v", envelope.Data, wantData)
	}
	for key, want := range wantData {
		if envelope.Data[key] != want {
			t.Fatalf("error data[%q] = %#v, want %#v", key, envelope.Data[key], want)
		}
	}
}

func decodeModelPriceHTTPData(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) map[string]any {
	t.Helper()
	var envelope struct {
		Code any            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v: %s", err, recorder.Body.String())
	}
	if numeric, ok := envelope.Code.(float64); !ok || numeric != 0 || envelope.Data == nil {
		t.Fatalf("success envelope = %#v", envelope)
	}
	return envelope.Data
}

func modelPriceHTTPUpdateBody(input string, confirm string) string {
	return `{"input":` + input + `,"output":null,"cache_read":null,"cache_write":null,` +
		`"context_tiers":[],"confirm_unpriced":` + confirm + `}`
}

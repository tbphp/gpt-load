package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestModelPriceAPIListsStableBuiltinAndUserRules(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	fixture.service.now = func() time.Time {
		return time.Date(2026, time.July, 27, 12, 30, 0, 0, time.UTC)
	}
	zero := pricing.NanoUSD(0)
	output := pricing.NanoUSD(7_500_000_000)
	if err := fixture.service.UpsertModelPrice(t.Context(), ModelPriceInput{
		Pattern: "z-user", UncachedInput: &zero, Output: &output,
	}); err != nil {
		t.Fatalf("seed z-user price: %v", err)
	}
	if err := fixture.service.UpsertModelPrice(t.Context(), ModelPriceInput{
		Pattern: "a-user", Output: &output,
	}); err != nil {
		t.Fatalf("seed a-user price: %v", err)
	}

	recorder := serveModelPriceRequest(newModelPriceTestEngine(t, fixture), http.MethodGet, "/api/model-prices", "", true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET model prices = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			PriceUnit string                       `json:"price_unit"`
			Builtin   []modelPriceListTestResponse `json:"builtin"`
			Overrides []modelPriceListTestResponse `json:"overrides"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.PriceUnit != "usd_per_million_tokens" ||
		len(envelope.Data.Builtin) < 1 || len(envelope.Data.Overrides) != 2 {
		t.Fatalf("GET envelope = %#v", envelope)
	}
	assertModelPriceResponseSorted(t, envelope.Data.Builtin)
	assertModelPriceResponseSorted(t, envelope.Data.Overrides)

	var builtin, userA, userZ *modelPriceListTestResponse
	for index := range envelope.Data.Builtin {
		item := &envelope.Data.Builtin[index]
		switch item.Pattern {
		case "gpt-5.6":
			builtin = item
		default:
			if item.Source != string(pricing.SourceBuiltin) {
				t.Fatalf("builtin partition contains source %q", item.Source)
			}
		}
	}
	for index := range envelope.Data.Overrides {
		item := &envelope.Data.Overrides[index]
		switch item.Pattern {
		case "a-user":
			userA = item
		case "z-user":
			userZ = item
		}
		if item.Source != string(pricing.SourceUser) {
			t.Fatalf("override partition contains source %q", item.Source)
		}
	}
	if builtin == nil || builtin.Source != string(pricing.SourceBuiltin) || builtin.SourceURL == nil ||
		*builtin.SourceURL != "https://developers.openai.com/api/docs/pricing" ||
		builtin.UpdatedAtMS != time.Date(2026, time.July, 26, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("builtin response = %#v", builtin)
	}
	if len(builtin.Prices) != 5 ||
		builtin.Prices["cache_write_5m_price_usd_per_million_tokens"] != nil ||
		builtin.Prices["cache_write_1h_price_usd_per_million_tokens"] != nil {
		t.Fatalf("builtin prices = %#v, want five values including null cache writes", builtin.Prices)
	}
	if userA == nil || userA.Source != string(pricing.SourceUser) || userA.SourceURL != nil ||
		userA.UpdatedAtMS != time.Date(2026, time.July, 27, 12, 30, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("a-user response = %#v", userA)
	}
	if userZ == nil || len(userZ.Prices) != 5 ||
		userZ.Prices["input_price_usd_per_million_tokens"] == nil ||
		*userZ.Prices["input_price_usd_per_million_tokens"] != "0" ||
		userZ.Prices["cache_read_price_usd_per_million_tokens"] != nil ||
		userZ.Prices["output_price_usd_per_million_tokens"] == nil ||
		*userZ.Prices["output_price_usd_per_million_tokens"] != "7.5" {
		t.Fatalf("z-user prices = %#v", userZ)
	}
}

func TestModelPriceAPIListsReadOnlyPricingPolicy(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	output := pricing.NanoUSD(7_500_000_000)
	if err := fixture.service.UpsertModelPrice(t.Context(), ModelPriceInput{
		Pattern: "user-model", Output: &output,
	}); err != nil {
		t.Fatalf("seed user model price: %v", err)
	}

	recorder := serveModelPriceRequest(
		newModelPriceTestEngine(t, fixture),
		http.MethodGet,
		"/api/model-prices",
		"",
		true,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET model prices = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Builtin []struct {
				Pattern       string          `json:"pattern"`
				PricingPolicy json.RawMessage `json:"pricing_policy"`
			} `json:"builtin"`
			Overrides []struct {
				Pattern       string          `json:"pattern"`
				PricingPolicy json.RawMessage `json:"pricing_policy"`
			} `json:"overrides"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}

	var policyModel, noPolicyModel, userModel json.RawMessage
	for _, rule := range envelope.Data.Builtin {
		switch rule.Pattern {
		case "gpt-5.6":
			policyModel = rule.PricingPolicy
		case "gpt-5.5-pro":
			noPolicyModel = rule.PricingPolicy
		}
	}
	for _, rule := range envelope.Data.Overrides {
		if rule.Pattern == "user-model" {
			userModel = rule.PricingPolicy
		}
	}
	var policy modelPricePolicyResponse
	if len(policyModel) == 0 || string(policyModel) == "null" {
		t.Fatalf("gpt-5.6 pricing_policy = %s, want object", policyModel)
	}
	if err := json.Unmarshal(policyModel, &policy); err != nil {
		t.Fatalf("decode gpt-5.6 pricing_policy: %v", err)
	}
	wantPolicy := modelPricePolicyResponse{
		InputThresholdTokens: 272_000,
		InputMultiplier:      2,
		OutputMultiplier:     1.5,
	}
	if policy != wantPolicy {
		t.Fatalf("gpt-5.6 pricing_policy = %+v, want %+v", policy, wantPolicy)
	}
	if string(noPolicyModel) != "null" {
		t.Fatalf("gpt-5.5-pro pricing_policy = %s, want null", noPolicyModel)
	}
	if string(userModel) != "null" {
		t.Fatalf("user pricing_policy = %s, want null", userModel)
	}
}

func TestModelPriceAPIPutStrictlyReplacesFivePrices(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	engine := newModelPriceTestEngine(t, fixture)

	valid := `{"pattern":"replace-model","prices":{"input_price_usd_per_million_tokens":"1","output_price_usd_per_million_tokens":"5","cache_read_price_usd_per_million_tokens":"2","cache_write_5m_price_usd_per_million_tokens":"3","cache_write_1h_price_usd_per_million_tokens":"4"}}`
	first := serveModelPriceRequest(engine, http.MethodPut, "/api/model-prices", valid, true)
	if first.Code != http.StatusOK {
		t.Fatalf("first PUT = %d %s", first.Code, first.Body.String())
	}
	replacement := `{"pattern":"replace-model","prices":{"input_price_usd_per_million_tokens":null,"output_price_usd_per_million_tokens":"9","cache_read_price_usd_per_million_tokens":"0","cache_write_5m_price_usd_per_million_tokens":null,"cache_write_1h_price_usd_per_million_tokens":null}}`
	second := serveModelPriceRequest(engine, http.MethodPut, "/api/model-prices", replacement, true)
	if second.Code != http.StatusOK {
		t.Fatalf("replacement PUT = %d %s", second.Code, second.Body.String())
	}
	var row models.ModelPrice
	if err := fixture.db.Where("pattern = ?", "replace-model").Take(&row).Error; err != nil {
		t.Fatalf("read replacement row: %v", err)
	}
	if row.InputPriceNanoUSDPerMillionTokens != nil ||
		row.CacheReadPriceNanoUSDPerMillionTokens == nil ||
		*row.CacheReadPriceNanoUSDPerMillionTokens != 0 ||
		row.CacheWrite5MPriceNanoUSDPerMillionTokens != nil ||
		row.CacheWrite1HPriceNanoUSDPerMillionTokens != nil ||
		row.OutputPriceNanoUSDPerMillionTokens == nil ||
		*row.OutputPriceNanoUSDPerMillionTokens != 9_000_000_000 {
		t.Fatalf("replacement row = %#v", row)
	}

	for _, body := range []string{
		strings.TrimSuffix(valid, "}") + `,"unknown":true}`,
		strings.TrimSuffix(valid, "}") + `,"pricing_policy":null}`,
		strings.Replace(valid, `"pattern":"replace-model"`, `"pattern":"strict-model","pattern":"other"`, 1),
		strings.Replace(valid, `"cache_read_price_usd_per_million_tokens":"2"`, `"cache_read_price_usd_per_million_tokens":"2","cache_read_price_usd_per_million_tokens":"3"`, 1),
		strings.Replace(valid, `,"cache_write_1h_price_usd_per_million_tokens":"4"`, "", 1),
		strings.Replace(valid, `"1"`, "1", 1),
		strings.Replace(valid, `"1"`, `"1e0"`, 1),
		strings.Replace(valid, `"1"`, `" 1"`, 1),
		valid + ` {}`,
	} {
		recorder := serveModelPriceRequest(engine, http.MethodPut, "/api/model-prices", body, true)
		assertModelPriceAPIError(t, recorder, http.StatusBadRequest, "INVALID_JSON")
	}
}

func TestModelPriceAPIDeleteValidatesSinglePatternAndIsIdempotent(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	value := pricing.NanoUSD(99_000_000_000)
	if err := fixture.service.UpsertModelPrice(t.Context(), ModelPriceInput{Pattern: "gpt-4o", Output: &value}); err != nil {
		t.Fatalf("seed override: %v", err)
	}
	engine := newModelPriceTestEngine(t, fixture)

	for _, target := range []string{
		"/api/model-prices",
		"/api/model-prices?pattern=gpt-4o&pattern=other",
		"/api/model-prices?unknown=value",
	} {
		recorder := serveModelPriceRequest(engine, http.MethodDelete, target, "", true)
		assertModelPriceAPIError(t, recorder, http.StatusBadRequest, "BAD_REQUEST")
	}
	invalid := serveModelPriceRequest(engine, http.MethodDelete, "/api/model-prices?pattern=invalid%3Fpattern", "", true)
	assertModelPriceAPIError(t, invalid, http.StatusBadRequest, "VALIDATION_FAILED")

	for attempt := 0; attempt < 2; attempt++ {
		recorder := serveModelPriceRequest(engine, http.MethodDelete, "/api/model-prices?pattern=gpt-4o", "", true)
		if recorder.Code != http.StatusOK {
			t.Fatalf("DELETE attempt %d = %d %s", attempt+1, recorder.Code, recorder.Body.String())
		}
	}
	rule, ok := fixture.priceRuntime.Load().Match("gpt-4o")
	if !ok || rule.Source != pricing.SourceBuiltin ||
		rule.Prices.Output.NanoUSDPerMillion != 10_000_000_000 {
		t.Fatalf("runtime after idempotent DELETE = %#v, %t", rule, ok)
	}
}

func TestModelPriceAPIRequiresAuthAndGetWaitsForPriceWrite(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	engine := newModelPriceTestEngine(t, fixture)

	unauthorized := serveModelPriceRequest(engine, http.MethodGet, "/api/model-prices", "", false)
	assertModelPriceAPIError(t, unauthorized, http.StatusUnauthorized, "UNAUTHORIZED")

	fixture.service.writeMu.Lock()
	locked := true
	defer func() {
		if locked {
			fixture.service.writeMu.Unlock()
		}
	}()
	value := int64(88_000_000_000)
	if err := fixture.db.Create(&models.ModelPrice{
		Pattern:                            "locked-write-model",
		OutputPriceNanoUSDPerMillionTokens: &value,
		Source:                             string(pricing.SourceUser),
	}).Error; err != nil {
		t.Fatalf("persist locked write row: %v", err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- serveModelPriceRequest(engine, http.MethodGet, "/api/model-prices", "", true)
	}()
	select {
	case recorder := <-done:
		t.Fatalf("GET completed while price write lock held: %d %s", recorder.Code, recorder.Body.String())
	case <-time.After(50 * time.Millisecond):
	}
	if rule, ok := fixture.priceRuntime.Load().Match("locked-write-model"); ok || rule.Source == pricing.SourceUser {
		t.Fatalf("runtime exposed locked DB write before publish: %#v, %t", rule, ok)
	}
	table, err := loadPriceTable(t.Context(), fixture.db)
	if err != nil {
		t.Fatalf("compile locked write table: %v", err)
	}
	fixture.priceRuntime.Publish(table)
	fixture.service.writeMu.Unlock()
	locked = false
	select {
	case recorder := <-done:
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET after write unlock = %d %s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"pattern":"locked-write-model"`) {
			t.Fatalf("GET after write unlock missed published row: %s", recorder.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GET did not complete after price write unlock")
	}
}

func newModelPriceTestEngine(t *testing.T, fixture serviceFixture) *gin.Engine {
	t.Helper()
	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	return engine
}

func serveModelPriceRequest(engine *gin.Engine, method, target, body string, authorized bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if authorized {
		request.Header.Set("Authorization", "Bearer test-auth-key")
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertModelPriceAPIError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), wantStatus)
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Code != wantCode {
		t.Fatalf("error code = %q, want %q", envelope.Code, wantCode)
	}
}

type modelPriceListTestResponse struct {
	Pattern     string             `json:"pattern"`
	Source      string             `json:"source"`
	Prices      map[string]*string `json:"prices"`
	SourceURL   *string            `json:"source_url"`
	UpdatedAtMS int64              `json:"updated_at_ms"`
}

func assertModelPriceResponseSorted(t *testing.T, rules []modelPriceListTestResponse) {
	t.Helper()
	patterns := make([]string, 0, len(rules))
	for _, rule := range rules {
		patterns = append(patterns, rule.Pattern)
	}
	if !sort.StringsAreSorted(patterns) {
		t.Fatalf("patterns are not sorted: %#v", patterns)
	}
}

package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestSettingsHTTPRouteStrategyRejectsInvalidValuesWithoutMutation(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	updated := serveLocalizedSettingsRequest(t, engine, http.MethodPut, "test-auth-key", "en-US",
		`{"settings":{"route_strategy":"weighted_mix"}}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("PUT weighted_mix = %d %s", updated.Code, updated.Body.String())
	}
	var envelope struct {
		Data SettingsResponse `json:"data"`
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Values.RouteStrategy != state.RouteStrategyWeightedMix {
		t.Fatalf("response route strategy = %q", envelope.Data.Values.RouteStrategy)
	}
	before := fixture.manager.Current()
	for _, raw := range []string{`""`, `"unknown"`, `"Native_First"`, `" weighted_mix "`, "true", "1", "[]", "{}"} {
		rejected := serveLocalizedSettingsRequest(t, engine, http.MethodPut, "test-auth-key", "en-US",
			`{"settings":{"route_strategy":`+raw+`,"retry_count":9}}`)
		if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), `"code":"VALIDATION_FAILED"`) {
			t.Fatalf("PUT route_strategy=%s = %d %s", raw, rejected.Code, rejected.Body.String())
		}
	}
	if fixture.manager.Current() != before {
		t.Fatal("invalid route strategy published a new Snapshot")
	}
	var rows []models.SystemSetting
	if err := fixture.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Key != state.SettingRouteStrategy || rows[0].Value != `"weighted_mix"` {
		t.Fatalf("invalid updates changed persisted settings: %#v", rows)
	}
}

func TestSettingsHTTPLastWriteWinsWithoutPrecondition(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, body := range []string{
		`{"settings":{"request_timeout":700}}`,
		`{"settings":{"retry_count":4}}`,
		`{"settings":{"request_timeout":800}}`,
	} {
		updated := serveLocalizedSettingsRequest(
			t, engine, http.MethodPut, "test-auth-key", "en-US", body,
		)
		if updated.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", body, updated.Code, updated.Body.String())
		}
		assertSettingsResponseHeaders(t, updated, "en-US")
	}

	current := serveLocalizedSettingsRequest(
		t, engine, http.MethodGet, "test-auth-key", "en-US", "",
	)
	if current.Code != http.StatusOK {
		t.Fatalf("GET settings = %d %s", current.Code, current.Body.String())
	}
	assertSettingsResponseHeaders(t, current, "en-US")
	var envelope struct {
		Data SettingsResponse `json:"data"`
	}
	if err := json.Unmarshal(current.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Values.RequestTimeout != 800 || envelope.Data.Values.RetryCount != 4 {
		t.Fatalf(
			"request_timeout/retry_count = %d/%d, want 800/4",
			envelope.Data.Values.RequestTimeout,
			envelope.Data.Values.RetryCount,
		)
	}
}

func TestSettingsHTTPUsesLocalizedNoStoreResponse(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, test := range []struct {
		language string
		message  string
	}{
		{language: "en-US", message: "Success"},
		{language: "zh-CN", message: "操作成功"},
	} {
		response := serveLocalizedSettingsRequest(
			t, engine, http.MethodGet, "test-auth-key", test.language, "",
		)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", test.language, response.Code, response.Body.String())
		}
		assertSettingsResponseHeaders(t, response, test.language)
		if !strings.Contains(response.Body.String(), fmt.Sprintf(`"message":%q`, test.message)) {
			t.Fatalf("GET %s body = %s", test.language, response.Body.String())
		}
		if strings.Contains(response.Body.String(), `"revision"`) {
			t.Fatalf("GET %s exposes process revision: %s", test.language, response.Body.String())
		}
	}
}

func TestSettingsHTTPPublicationFailureReloadsCommittedDatabaseTruth(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	before := fixture.manager.Current()
	fixture.service.publishSnapshot = func(state.CompileInput) (*state.ConfigSnapshot, error) {
		return nil, errors.New("forced settings snapshot publication failure")
	}

	failed := serveLocalizedSettingsRequest(
		t,
		engine,
		http.MethodPut,
		"test-auth-key",
		"en-US",
		`{"settings":{"retry_count":4}}`,
	)
	if failed.Code != http.StatusInternalServerError ||
		!strings.Contains(failed.Body.String(), `"code":"INTERNAL_SERVER_ERROR"`) {
		t.Fatalf("failed update = %d %s, want internal error", failed.Code, failed.Body.String())
	}

	after := fixture.manager.Current()
	if after.Revision != before.Revision+1 || after.Settings.RetryCount != 4 {
		t.Fatalf("recovered snapshot = %#v, want committed retry_count 4", after)
	}
}

func assertSettingsResponseHeaders(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	language string,
) {
	t.Helper()
	if tag := recorder.Header().Get("ETag"); tag != "" {
		t.Fatalf("ETag = %q, want omitted", tag)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Content-Language"); got != language {
		t.Fatalf("Content-Language = %q, want %q", got, language)
	}
	if got := recorder.Header().Get("Vary"); got != "Accept-Language" {
		t.Fatalf("Vary = %q", got)
	}
}

func serveLocalizedSettingsRequest(
	t *testing.T,
	engine *gin.Engine,
	method string,
	authKey string,
	language string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/api/settings", strings.NewReader(body))
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	if language != "" {
		request.Header.Set("Accept-Language", language)
	}
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	return recorder
}

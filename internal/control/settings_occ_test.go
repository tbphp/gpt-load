package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

func TestSettingsHTTPUsesCanonicalLocalizedStrongRepresentation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	english := serveSettingsOCCRequest(
		t, engine, http.MethodGet, "test-auth-key", "en-US", "", "",
	)
	if english.Code != http.StatusOK {
		t.Fatalf("English GET = %d %s", english.Code, english.Body.String())
	}
	assertSettingsRepresentationHeaders(t, english, "en-US")
	const wantEnglish = `{"code":0,"data":{"overrides":[],"values":{"affinity_capacity":10000,"affinity_enabled":true,"affinity_ttl":3600,"first_byte_timeout":120,"header_rules":{"remove":[],"set":{}},"inject_usage_options":true,"models_dev_auto_sync_enabled":true,"request_log_retention_days":7,"request_timeout":600,"stream_idle_timeout":300,"validation_interval":600}},"message":"Success"}`
	if english.Body.String() != wantEnglish {
		t.Fatalf("English body = %s, want %s", english.Body.String(), wantEnglish)
	}

	chinese := serveSettingsOCCRequest(
		t, engine, http.MethodGet, "test-auth-key", "zh-CN", "", "",
	)
	if chinese.Code != http.StatusOK {
		t.Fatalf("Chinese GET = %d %s", chinese.Code, chinese.Body.String())
	}
	assertSettingsRepresentationHeaders(t, chinese, "zh-CN")
	if chinese.Header().Get("ETag") == english.Header().Get("ETag") {
		t.Fatalf("localized representations share ETag %q", english.Header().Get("ETag"))
	}
	if !strings.Contains(chinese.Body.String(), `"message":"操作成功"`) {
		t.Fatalf("Chinese body = %s", chinese.Body.String())
	}
}

func TestSettingsHTTPRequiresStrongIfMatchAndReturnsLatestConflict(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	before := fixture.manager.Current()
	missing := serveSettingsOCCRequest(
		t,
		engine,
		http.MethodPut,
		"test-auth-key",
		"en-US",
		"",
		`{"settings":{"request_timeout":`,
	)
	if missing.Code != http.StatusPreconditionRequired ||
		!strings.Contains(missing.Body.String(), `"code":"SETTINGS_PRECONDITION_REQUIRED"`) {
		t.Fatalf("missing If-Match = %d %s", missing.Code, missing.Body.String())
	}
	if fixture.manager.Current() != before {
		t.Fatal("missing If-Match published a snapshot")
	}

	malformed := serveSettingsOCCRequest(
		t,
		engine,
		http.MethodPut,
		"test-auth-key",
		"en-US",
		`W/"sha256-`+strings.Repeat("0", 64)+`"`,
		`{"settings":{"request_timeout":900}}`,
	)
	if malformed.Code != http.StatusBadRequest ||
		!strings.Contains(malformed.Body.String(), `"code":"BAD_REQUEST"`) {
		t.Fatalf("weak If-Match = %d %s", malformed.Code, malformed.Body.String())
	}

	latest := serveSettingsOCCRequest(
		t, engine, http.MethodGet, "test-auth-key", "en-US", "", "",
	)
	conflict := serveSettingsOCCRequest(
		t,
		engine,
		http.MethodPut,
		"test-auth-key",
		"en-US",
		`"sha256-`+strings.Repeat("0", 64)+`"`,
		`{"settings":{"request_timeout":900}}`,
	)
	if conflict.Code != http.StatusPreconditionFailed {
		t.Fatalf("conflict = %d %s", conflict.Code, conflict.Body.String())
	}
	var envelope struct {
		Code string `json:"code"`
		Data struct {
			Settings     SettingsDTO `json:"settings"`
			SettingsETag string      `json:"settings_etag"`
		} `json:"data"`
	}
	if err := json.Unmarshal(conflict.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != "SETTINGS_VERSION_CONFLICT" ||
		envelope.Data.SettingsETag != strings.Trim(latest.Header().Get("ETag"), `"`) ||
		envelope.Data.Settings.Values.RequestTimeout != 600 ||
		len(envelope.Data.Settings.Overrides) != 0 {
		t.Fatalf("conflict body = %s", conflict.Body.String())
	}
	if fixture.manager.Current() != before {
		t.Fatal("version conflict published a snapshot")
	}
}

func TestSettingsHTTPMatchingWriteReturnsCurrentGETAndAllowsABA(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	initial := serveSettingsOCCRequest(
		t, engine, http.MethodGet, "test-auth-key", "en-US", "", "",
	)
	updated := serveSettingsOCCRequest(
		t,
		engine,
		http.MethodPut,
		"test-auth-key",
		"en-US",
		initial.Header().Get("ETag"),
		`{"settings":{"request_timeout":900}}`,
	)
	if updated.Code != http.StatusOK {
		t.Fatalf("update = %d %s", updated.Code, updated.Body.String())
	}
	assertSettingsRepresentationHeaders(t, updated, "en-US")
	if updated.Header().Get("ETag") == initial.Header().Get("ETag") {
		t.Fatalf("changed settings retained ETag %q", updated.Header().Get("ETag"))
	}
	current := serveSettingsOCCRequest(
		t, engine, http.MethodGet, "test-auth-key", "en-US", "", "",
	)
	if current.Header().Get("ETag") != updated.Header().Get("ETag") ||
		!bytes.Equal(current.Body.Bytes(), updated.Body.Bytes()) {
		t.Fatalf("GET/PUT representation differs:\nGET %q %s\nPUT %q %s",
			current.Header().Get("ETag"), current.Body.String(),
			updated.Header().Get("ETag"), updated.Body.String())
	}

	reset := serveSettingsOCCRequest(
		t,
		engine,
		http.MethodPut,
		"test-auth-key",
		"en-US",
		updated.Header().Get("ETag"),
		`{"settings":{"request_timeout":null}}`,
	)
	if reset.Code != http.StatusOK ||
		reset.Header().Get("ETag") != initial.Header().Get("ETag") ||
		!bytes.Equal(reset.Body.Bytes(), initial.Body.Bytes()) {
		t.Fatalf("A-B-A response = %d %q %s, want %q %s",
			reset.Code, reset.Header().Get("ETag"), reset.Body.String(),
			initial.Header().Get("ETag"), initial.Body.String())
	}
}

func TestSettingsETagIgnoresOtherResourcesAndSurvivesRuntimeReload(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	initial := serveSettingsOCCRequest(
		t, engine, http.MethodGet, "test-auth-key", "en-US", "", "",
	)

	groupName := "ETag unrelated group"
	if _, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: &groupName, ChannelID: channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://example.com/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "sk-unrelated-etag-key",
	}); err != nil {
		t.Fatal(err)
	}
	afterGroup := serveSettingsOCCRequest(
		t, engine, http.MethodGet, "test-auth-key", "en-US", "", "",
	)
	if afterGroup.Header().Get("ETag") != initial.Header().Get("ETag") ||
		!bytes.Equal(afterGroup.Body.Bytes(), initial.Body.Bytes()) {
		t.Fatalf("unrelated mutation changed Settings representation")
	}

	reloadedManager := state.NewManager()
	reloadedRegistry := state.NewCredentialRegistry()
	if err := stateloader.New(fixture.db, reloadedManager, reloadedRegistry).Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	reloadedService := NewService(
		fixture.db,
		reloadedManager,
		reloadedRegistry,
		fixture.priceRuntime,
		fixture.catalogRuntime,
		nil,
		fixture.encryption,
		fixture.service.executor,
		nil,
		fixture.service.requestLogs,
		fixture.service.usageStats,
		fixture.service.homeStatistics,
		fixture.stats,
		fixture.mutations,
		fixture.requestLogStats,
	)
	reloadedEngine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, reloadedService).RegisterRoutes(reloadedEngine)
	afterReload := serveSettingsOCCRequest(
		t, reloadedEngine, http.MethodGet, "test-auth-key", "en-US", "", "",
	)
	if afterReload.Header().Get("ETag") != initial.Header().Get("ETag") ||
		!bytes.Equal(afterReload.Body.Bytes(), initial.Body.Bytes()) {
		t.Fatalf("reload changed Settings representation:\ninitial %q %s\nreload %q %s",
			initial.Header().Get("ETag"), initial.Body.String(),
			afterReload.Header().Get("ETag"), afterReload.Body.String())
	}
}

func assertSettingsRepresentationHeaders(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	language string,
) {
	t.Helper()
	tag := recorder.Header().Get("ETag")
	if len(tag) != len(`"sha256-`)+64+len(`"`) ||
		!strings.HasPrefix(tag, `"sha256-`) ||
		!strings.HasSuffix(tag, `"`) {
		t.Fatalf("ETag = %q", tag)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want header omitted", got)
	}
	if got := recorder.Header().Get("Content-Language"); got != language {
		t.Fatalf("Content-Language = %q, want %q", got, language)
	}
	if got := recorder.Header().Get("Vary"); got != "Accept-Language" {
		t.Fatalf("Vary = %q", got)
	}
}

func serveSettingsOCCRequest(
	t *testing.T,
	engine *gin.Engine,
	method string,
	authKey string,
	language string,
	ifMatch string,
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
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	return recorder
}

package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestClientCatalogDirectorySavePreservesCommittedMetadataAfterPublishFailure(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "catalog-recovery", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"gpt-6"},{"id":"gpt-5"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	mustPublishClientModelSnapshot(t, fixture)
	before := fixture.manager.Current()
	publish := fixture.service.publishSnapshot
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.service.publishSnapshot = func(state.CompileInput) (*state.ConfigSnapshot, error) {
		cancel()
		return nil, errors.New("forced publication failure after commit")
	}
	description := "Committed model description"
	metadata := catalog.ClientModelOverrides{Description: &description}
	_, err := fixture.service.UpdateClientCatalog(ctx, ClientCatalogRequest{
		Profiles: []ClientModelProfileUpdateRequest{{ClientModel: "gpt-6", Overrides: &metadata}},
	})
	if !errors.Is(err, context.Canceled) || fixture.manager.Current() != before {
		t.Fatalf("failed publication did not leave the expected stale snapshot: %v", err)
	}
	persisted := mustBuildCompileInput(t, fixture.db).ClientModelOverrides["gpt-6"]
	if persisted.Description == nil || *persisted.Description != description {
		t.Fatal("metadata was not committed before publication failed")
	}
	fixture.service.publishSnapshot = publish
	result, err := fixture.service.UpdateClientCatalog(t.Context(), ClientCatalogRequest{
		Models: []string{"gpt-5", "gpt-6"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Selected, []string{"gpt-5", "gpt-6"}) {
		t.Fatalf("saved order = %v", result.Selected)
	}
	persisted = mustBuildCompileInput(t, fixture.db).ClientModelOverrides["gpt-6"]
	effective := fixture.manager.Current().ClientModelOverrides["gpt-6"]
	if persisted.Description == nil || *persisted.Description != description ||
		effective.Description == nil || *effective.Description != description {
		t.Fatal("directory save overwrote committed metadata from a stale snapshot")
	}
}

func TestClientCatalogAtomicSavePreviewAndIndependentResets(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "catalog", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"gpt-6"},{"id":"gpt-5"},{"id":"other"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	mustPublishClientModelSnapshot(t, fixture)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	read := func(method, path, body string) map[string]json.RawMessage {
		t.Helper()
		recorder := serveClientModelRequest(engine, method, path, body, authTestKey)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %s", method, path, recorder.Code, recorder.Body.String())
		}
		var response struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Data
	}
	initial := read(http.MethodGet, "/api/models/client-catalog", "")
	if string(initial["selected"]) != `["gpt-6","gpt-5"]` {
		t.Fatalf("default selection = %s", initial["selected"])
	}
	draft := `{"known_models":["gpt-6","gpt-5","other"],"models":["other","gpt-5"],"profiles":[{"client_model":"gpt-5","overrides":{"context_window":64000,"display_name":"中文 <model>"}}]}`
	preview := read(http.MethodPost, "/api/models/client-catalog/preview", draft)
	if string(preview["selected"]) != `["other","gpt-5"]` {
		t.Fatalf("preview selection = %s", preview["selected"])
	}
	if len(fixture.manager.Current().ClientModelOverrides) != 0 {
		t.Fatal("preview mutated configuration")
	}
	saved := read(http.MethodPut, "/api/models/client-catalog", draft)
	if string(saved["budget"]) != string(preview["budget"]) {
		t.Fatalf("save and preview differ: %s / %s", saved["budget"], preview["budget"])
	}
	mustPublishClientModelSnapshot(t, fixture)
	if got := read(http.MethodGet, "/api/models/client-catalog", ""); string(got["selected"]) != string(saved["selected"]) {
		t.Fatalf("saved selection lost: %s", got["selected"])
	}
	// 单模型资料重置不能重新启用被移出的模型，也不能改变顺序。
	readClientModelProfile(t, engine, http.MethodPut, "/api/models/profile", `{"client_model":"gpt-5","overrides":{}}`, authTestKey)
	if got := read(http.MethodGet, "/api/models/client-catalog", ""); string(got["selected"]) != `["other","gpt-5"]` {
		t.Fatalf("profile reset changed directory: %s", got["selected"])
	}
	readClientModelProfile(t, engine, http.MethodPut, "/api/models/profile", `{"client_model":"gpt-5","overrides":{"context_window":123000}}`, authTestKey)
	if _, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		return tx.Model(&models.Group{}).Where("id = ?", group.ID).
			Update("models", models.JSON(`[{"id":"gpt-6"},{"id":"gpt-5"},{"id":"other"},{"id":"gpt-99"}]`)).Error
	}, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(http.MethodGet, "/api/models/client-catalog", ""); string(got["selected"]) != `["other","gpt-5","gpt-99"]` {
		t.Fatalf("new model disrupted manual order or restored removed model: %s", got["selected"])
	}
	if stale := serveClientModelRequest(engine, http.MethodPut, "/api/models/client-catalog", draft, authTestKey); stale.Code != http.StatusBadRequest {
		t.Fatalf("stale model collection accepted: %d", stale.Code)
	}
	reset := read(http.MethodPut, "/api/models/client-catalog", `{"reset_directory":true}`)
	if string(reset["selected"]) != `["gpt-99","gpt-6","gpt-5"]` {
		t.Fatalf("default reset = %s", reset["selected"])
	}
	profile := readClientModelProfile(t, engine, http.MethodGet, "/api/models/profile?model=gpt-5", "", authTestKey)
	if profile.Effective.ContextWindow == nil || *profile.Effective.ContextWindow != 123000 {
		t.Fatal("directory reset erased metadata")
	}
	before := fixture.manager.Current()
	bad := serveClientModelRequest(engine, http.MethodPut, "/api/models/client-catalog", `{"models":["gpt-5"],"profiles":[{"client_model":"gpt-5","overrides":{"context_window":1}},{"client_model":"missing","overrides":{}}]}`, authTestKey)
	if bad.Code != http.StatusBadRequest || fixture.manager.Current() != before {
		t.Fatalf("invalid batch was not rejected atomically: %d", bad.Code)
	}
}

func TestClientCatalogMetadataOnlySaveKeepsAutomaticVersionOrdering(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "automatic", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"gpt-6"},{"id":"gpt-5"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	mustPublishClientModelSnapshot(t, fixture)
	contextWindow := int64(512000)
	metadata := catalog.ClientModelOverrides{ContextWindow: &contextWindow}
	if _, err := fixture.service.UpdateClientCatalog(t.Context(), ClientCatalogRequest{
		Profiles: []ClientModelProfileUpdateRequest{{ClientModel: "gpt-6", Overrides: &metadata}},
	}); err != nil {
		t.Fatalf("metadata-only batch rejected: %v", err)
	}
	if _, err := fixture.service.writeConfig(t.Context(), func(tx *gorm.DB) error {
		return tx.Model(&models.Group{}).Where("id = ?", group.ID).
			Update("models", models.JSON(`[{"id":"gpt-6"},{"id":"gpt-5"},{"id":"gpt-7"}]`)).Error
	}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.GetClientCatalog(t.Context())
	if err != nil || !reflect.DeepEqual(result.Selected, []string{"gpt-7", "gpt-6", "gpt-5"}) {
		t.Fatalf("metadata froze automatic order: %v, %v", result.Selected, err)
	}
	if got := fixture.manager.Current().ClientModelOverrides["gpt-6"]; got.CatalogOrder != nil || got.CatalogEnabled != nil || got.ContextWindow == nil || *got.ContextWindow != contextWindow {
		t.Fatalf("metadata batch changed directory configuration: %#v", got)
	}
}

func TestClientCatalogRejectsAccessKeysInvalidDraftsAndRollsBackBatch(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "catalog", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"gpt-6"},{"id":"gpt-5"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	mustPublishClientModelSnapshot(t, fixture)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	access, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/models/client-catalog", ""},
		{http.MethodPost, "/api/models/client-catalog/preview", `{"models":[]}`},
		{http.MethodPut, "/api/models/client-catalog", `{"models":[]}`},
	} {
		if result := serveClientModelRequest(engine, request.method, request.path, request.body, access.Key); result.Code != http.StatusForbidden {
			t.Fatalf("access key reached %s: %d", request.path, result.Code)
		}
	}
	for _, body := range []string{
		`{}`, `{"models":["gpt-5","gpt-5"]}`, `{"models":["missing"]}`, `{"models":[],"unknown":true}`,
		`{"reset_directory":true,"models":["gpt-5"]}`, `{"models":[],"profiles":[{"client_model":"gpt-5"}]}`,
		`{"models":[],"profiles":[{"client_model":"gpt-5","overrides":{"catalog_enabled":true}}]}`,
		`{"models":[],"profiles":[{"client_model":"gpt-5","overrides":{"context_window":0}}]}`,
	} {
		for _, path := range []string{"/api/models/client-catalog", "/api/models/client-catalog/preview"} {
			method := http.MethodPut
			if path == "/api/models/client-catalog/preview" {
				method = http.MethodPost
			}
			if result := serveClientModelRequest(engine, method, path, body, authTestKey); result.Code != http.StatusBadRequest {
				t.Fatalf("invalid draft %s: %d", body, result.Code)
			}
		}
	}
	before := fixture.manager.Current().ClientModelOverrides
	if err := fixture.db.Callback().Create().Before("gorm:create").Register("catalog_force_failure", func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*models.ClientModelOverride); ok && row.ClientModel == "gpt-6" {
			tx.AddError(errors.New("forced second catalog write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fixture.db.Callback().Create().Remove("catalog_force_failure"); err != nil {
			t.Error(err)
		}
	})
	result := serveClientModelRequest(engine, http.MethodPut, "/api/models/client-catalog", `{"models":["gpt-5","gpt-6"]}`, authTestKey)
	if result.Code != http.StatusInternalServerError {
		t.Fatalf("forced write failure = %d", result.Code)
	}
	var count int64
	if err := fixture.db.Model(&models.ClientModelOverride{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || !reflect.DeepEqual(before, fixture.manager.Current().ClientModelOverrides) {
		t.Fatal("failed batch left partial database or runtime changes")
	}
}

func TestClientCatalogPersistsMetadataAndValidatesDefaultReasoning(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "metadata", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"gpt-6.1-sol"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	mustPublishClientModelSnapshot(t, fixture)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	metadata := `{"description":"中文说明","auto_compact_token_limit":48000,"supported_reasoning_levels":["low","high"],"default_reasoning_level":"high","service_tiers":["ultrafast"]}`
	body := `{"profiles":[{"client_model":"gpt-6.1-sol","overrides":` + metadata + `}]}`
	preview := serveClientModelRequest(engine, http.MethodPost, "/api/models/client-catalog/preview", body, authTestKey)
	if preview.Code != http.StatusOK {
		t.Fatalf("metadata preview = %d %s", preview.Code, preview.Body.String())
	}
	var count int64
	if err := fixture.db.Model(&models.ClientModelOverride{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("preview persisted metadata: count=%d, err=%v", count, err)
	}
	saved := serveClientModelRequest(engine, http.MethodPut, "/api/models/client-catalog", body, authTestKey)
	if saved.Code != http.StatusOK {
		t.Fatalf("metadata save = %d %s", saved.Code, saved.Body.String())
	}
	mustPublishClientModelSnapshot(t, fixture)
	profile := readClientModelProfile(t, engine, http.MethodGet, "/api/models/profile?model=gpt-6.1-sol", "", authTestKey)
	if profile.Effective.Description != "中文说明" || profile.Effective.AutoCompactTokenLimit == nil || *profile.Effective.AutoCompactTokenLimit != 48000 ||
		profile.Effective.DefaultReasoningLevel != "high" || !reflect.DeepEqual(profile.Effective.ServiceTiers, []string{"ultrafast"}) {
		t.Fatalf("reloaded metadata = %#v", profile.Effective)
	}
	for _, invalid := range []string{
		`{"default_reasoning_level":"none"}`,
		`{"default_reasoning_level":"high","supported_reasoning_levels":["low"]}`,
		`{"default_service_tier":"priority"}`,
	} {
		before := fixture.manager.Current()
		for _, path := range []string{"/api/models/client-catalog", "/api/models/client-catalog/preview", "/api/models/profile"} {
			method := http.MethodPut
			payload := `{"profiles":[{"client_model":"gpt-6.1-sol","overrides":` + invalid + `}]}`
			if path == "/api/models/client-catalog/preview" {
				method = http.MethodPost
			} else if path == "/api/models/profile" {
				payload = `{"client_model":"gpt-6.1-sol","overrides":` + invalid + `}`
			}
			result := serveClientModelRequest(engine, method, path, payload, authTestKey)
			if result.Code != http.StatusBadRequest || fixture.manager.Current() != before {
				t.Fatalf("invalid default accepted by %s: %d", path, result.Code)
			}
		}
	}
	reset := readClientModelProfile(t, engine, http.MethodPut, "/api/models/profile", `{"client_model":"gpt-6.1-sol","overrides":{}}`, authTestKey)
	if reset.HasOverrides || !reflect.DeepEqual(reset.Effective, reset.Automatic) {
		t.Fatal("metadata reset did not restore the full preset")
	}
}

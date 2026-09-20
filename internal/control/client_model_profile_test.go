package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestClientModelProfileHTTPPersistsAndResetsOverrides(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "enabled", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"upstream","alias":"client"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	disabledGroup := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "disabled", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"disabled-upstream","alias":"disabled-only"}]`), Overrides: models.JSON(`{}`), Enabled: false,
	})
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", disabledGroup.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	mustEnsureInitialPrices(t, fixture)
	mustPublishClientModelSnapshot(t, fixture)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)

	initial := readClientModelProfile(t, engine, http.MethodGet, "/api/models/profile?model=client", "", authTestKey)
	if initial.HasOverrides || initial.Automatic.ContextWindow == nil || *initial.Automatic.ContextWindow != 272000 ||
		initial.Effective.DisplayName != "client" {
		t.Fatalf("initial profile = %#v", initial)
	}

	updated := readClientModelProfile(t, engine, http.MethodPut, "/api/models/profile", `{
		"client_model":"client",
		"overrides":{"display_name":"Client name","context_window":64000,"supported_reasoning_levels":["low","high"],"input_modalities":["text"]}
	}`, authTestKey)
	if !updated.HasOverrides || updated.Overrides.DisplayName == nil || *updated.Overrides.DisplayName != "Client name" ||
		updated.Effective.DisplayName != "Client name" || updated.Effective.ContextWindow == nil || *updated.Effective.ContextWindow != 64000 ||
		len(updated.Effective.SupportedReasoningLevels) != 2 || updated.Effective.SupportedReasoningLevels[1] != "high" ||
		len(updated.Effective.InputModalities) != 1 || updated.Effective.InputModalities[0] != "text" ||
		updated.Automatic.ContextWindow == nil || *updated.Automatic.ContextWindow != 272000 {
		t.Fatalf("updated profile = %#v", updated)
	}
	var row models.ClientModelOverride
	if err := fixture.db.Where("model_hash = ?", models.ClientModelHash("client")).Take(&row).Error; err != nil {
		t.Fatalf("load persisted override: %v", err)
	}
	if row.ClientModel != "client" || string(row.Overrides) != `{"context_window":64000,"display_name":"Client name","input_modalities":["text"],"supported_reasoning_levels":["low","high"]}` {
		t.Fatalf("persisted override = %#v", row)
	}
	modelsResponse := readProjectModelList(t, engine, authTestKey)
	if len(modelsResponse.Items) != 2 || !modelsResponse.Items[0].HasOverrides || modelsResponse.Items[1].HasOverrides {
		t.Fatalf("project models override indicators = %#v", modelsResponse.Items)
	}

	reset := readClientModelProfile(t, engine, http.MethodPut, "/api/models/profile", `{"client_model":"client","overrides":{}}`, authTestKey)
	if reset.HasOverrides || !reset.Overrides.IsEmpty() || reset.Effective.DisplayName != "client" ||
		reset.Effective.ContextWindow == nil || *reset.Effective.ContextWindow != 272000 {
		t.Fatalf("reset profile = %#v", reset)
	}
	if err := fixture.db.Where("model_hash = ?", models.ClientModelHash("client")).Take(&row).Error; err == nil {
		t.Fatal("reset retained persisted override")
	}

	disabled := readClientModelProfile(t, engine, http.MethodPut, "/api/models/profile", `{"client_model":"disabled-only","overrides":{"display_name":"Disabled model"}}`, authTestKey)
	if !disabled.HasOverrides || disabled.Effective.DisplayName != "Disabled model" {
		t.Fatalf("disabled-only profile = %#v", disabled)
	}
}

func TestClientModelProfileHTTPRejectsInvalidAndAccessKeyMutations(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "configured", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"upstream","alias":"client"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	mustEnsureInitialPrices(t, fixture)
	mustPublishClientModelSnapshot(t, fixture)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)

	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/models/profile", ""},
		{http.MethodGet, "/api/models/profile?model=client&model=other", ""},
		{http.MethodPut, "/api/models/profile", `{"client_model":"client"}`},
		{http.MethodPut, "/api/models/profile", `{"client_model":"client","overrides":{"unknown":true}}`},
		{http.MethodPut, "/api/models/profile", `{"client_model":"missing","overrides":{"display_name":"x"}}`},
	} {
		recorder := serveClientModelRequest(engine, request.method, request.path, request.body, authTestKey)
		if recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d %s", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/models/profile?model=client", ""},
		{http.MethodPut, "/api/models/profile", `{"client_model":"client","overrides":{"display_name":"Denied"}}`},
	} {
		recorder := serveClientModelRequest(engine, request.method, request.path, request.body, created.Key)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("access key %s = %d %s, want 403", request.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestClientModelProfilePublishFailureRecoversCommittedOverride(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "recovery", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"upstream","alias":"client"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	})
	mustPublishClientModelSnapshot(t, fixture)
	fixture.service.publishSnapshot = func(state.CompileInput) (*state.ConfigSnapshot, error) {
		return nil, errors.New("forced profile snapshot publication failure")
	}
	displayName := "Recovered"
	_, err := fixture.service.UpdateClientModelProfile(t.Context(), ClientModelProfileUpdateRequest{
		ClientModel: "client", Overrides: &catalog.ClientModelOverrides{DisplayName: &displayName},
	})
	if err == nil {
		t.Fatal("UpdateClientModelProfile() error = nil")
	}
	var operationErr *controlOperationError
	if !errors.As(err, &operationErr) || operationErr.stage != stagePublishCommittedSnapshot {
		t.Fatalf("UpdateClientModelProfile() error = %#v", err)
	}
	var row models.ClientModelOverride
	if err := fixture.db.Where("model_hash = ?", models.ClientModelHash("client")).Take(&row).Error; err != nil {
		t.Fatalf("committed override is missing: %v", err)
	}
	overrides := fixture.manager.Current().ClientModelOverrides["client"]
	if overrides.DisplayName == nil || *overrides.DisplayName != displayName {
		t.Fatalf("recovered runtime override = %#v", overrides)
	}
}

func readClientModelProfile(t *testing.T, engine *gin.Engine, method, path, body, token string) ClientModelProfileDTO {
	t.Helper()
	recorder := serveClientModelRequest(engine, method, path, body, token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %s", method, path, recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data ClientModelProfileDTO `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func serveClientModelRequest(engine *gin.Engine, method, path, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func mustPublishClientModelSnapshot(t *testing.T, fixture serviceFixture) {
	t.Helper()
	if _, err := fixture.service.writeConfig(t.Context(), func(*gorm.DB) error { return nil }, nil); err != nil {
		t.Fatalf("publish fixture snapshot: %v", err)
	}
}

func readProjectModelList(t *testing.T, engine *gin.Engine, token string) ProjectModelListResponse {
	t.Helper()
	recorder := serveAuthRequest(engine, "/api/models?group_status=all&page_size=100", "192.0.2.10:1234", "Bearer "+token, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list models = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data ProjectModelListResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

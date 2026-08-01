package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestGetGroupSettingsReturnsPersistedDraftOverridesAndEffectiveConfig(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("settings-read")
	group.Config = models.JSON(`{
		"connect_timeout":20,
		"first_byte_timeout":180,
		"request_timeout":480,
		"stream_idle_timeout":270,
		"header_rules":{"set":{"X-Group":"value"},"remove":["X-Removed"]},
		"inject_usage_options":false
	}`)
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Publish(mustBuildCompileInput(t, fixture.db)); err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.GetGroupSettings(t.Context(), group.ID)
	if err != nil {
		t.Fatalf("GetGroupSettings() error = %v", err)
	}
	if got.Name != group.Name || got.UpstreamURL != group.UpstreamURL ||
		!reflect.DeepEqual(got.Protocols, []protocol.Protocol{protocol.OpenAICompletions}) ||
		!got.Enabled || got.WeightManual != nil {
		t.Fatalf("persisted settings = %#v", got)
	}
	for _, key := range []string{
		state.SettingConnectTimeout,
		state.SettingFirstByteTimeout,
		state.SettingRequestTimeout,
		state.SettingStreamIdleTimeout,
		state.SettingHeaderRules,
		state.SettingInjectUsageOptions,
	} {
		if got.Overrides[key] == nil {
			t.Fatalf("overrides missing %q: %#v", key, got.Overrides)
		}
	}
	if got.Effective.ConnectTimeout != 20 || got.Effective.FirstByteTimeout != 180 ||
		got.Effective.RequestTimeout != 480 || got.Effective.StreamIdleTimeout != 270 ||
		got.Effective.InjectUsageOptions ||
		!reflect.DeepEqual(got.Effective.HeaderRules.Set, map[string]string{"X-Group": "value"}) ||
		!reflect.DeepEqual(got.Effective.HeaderRules.Remove, []string{"X-Removed"}) {
		t.Fatalf("effective = %#v", got.Effective)
	}
}

func TestUpdateGroupSettingsPublishesOnceAndReturnsNewSettings(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-settings-update")
	beforeRevision := fixture.manager.Current().Revision
	weight := 75
	result, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Name:                  optionalField[string]{Set: true, Value: " updated settings "},
		UpstreamURL:           optionalField[string]{Set: true, Value: " HTTPS://SETTINGS-UPDATED.EXAMPLE.COM/v1/ "},
		Protocols:             optionalField[[]protocol.Protocol]{Set: true, Value: []protocol.Protocol{protocol.OpenAICompletions}},
		ValidationModel:       optionalField[string]{Set: true, Value: " gpt-4.1 "},
		Enabled:               optionalField[bool]{Set: true, Value: false},
		WeightManual:          optionalField[int]{Set: true, Value: weight},
		Overrides:             optionalField[config.Settings]{Set: true, Value: config.Settings{"request_timeout": json.Number("720")}},
		ConfirmUpstreamChange: true,
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings() error = %v", err)
	}
	if result.Name != "updated settings" || result.UpstreamURL != "https://settings-updated.example.com/v1" ||
		result.ValidationModel == nil || *result.ValidationModel != "gpt-4.1" || result.Enabled ||
		result.WeightManual == nil || *result.WeightManual != weight || result.Effective.RequestTimeout != 720 {
		t.Fatalf("UpdateGroupSettings() = %#v", result)
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision+1 {
		t.Fatalf("snapshot revision = %d, want %d", got, beforeRevision+1)
	}
	stored, err := fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil {
		t.Fatalf("GetGroupSettings() after update error = %v", err)
	}
	if !reflect.DeepEqual(stored, result) {
		t.Fatalf("stored settings = %#v, want %#v", stored, result)
	}
}

func TestUpdateGroupSettingsValidatesWeightInjectUsageAndUpstreamConfirmation(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-settings-validation")
	beforeRevision := fixture.manager.Current().Revision

	for _, weight := range []optionalField[int]{
		{Set: true, Value: 0},
		{Set: true, Value: -1},
		{Set: true, Value: 101},
	} {
		if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{WeightManual: weight}); !errors.Is(err, app_errors.ErrValidation) {
			t.Fatalf("weight %#v error = %v, want validation", weight, err)
		}
	}
	for _, test := range []struct {
		name  string
		field optionalField[int]
		want  *int
	}{
		{name: "null", field: optionalField[int]{Set: true, Null: true}},
		{name: "minimum", field: optionalField[int]{Set: true, Value: 1}, want: settingsWeightPointer(1)},
		{name: "maximum", field: optionalField[int]{Set: true, Value: 100}, want: settingsWeightPointer(100)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := fixture.service.UpdateGroupSettings(
				t.Context(),
				groupID,
				GroupSettingsUpdateRequest{WeightManual: test.field},
			)
			if err != nil || !reflect.DeepEqual(got.WeightManual, test.want) {
				t.Fatalf("weight update = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		UpstreamURL: optionalField[string]{Set: true, Value: "https://confirm-required.example.com/v1"},
	}); !errors.Is(err, errGroupSettingsUpstreamChangeConfirmationRequired) {
		t.Fatalf("unconfirmed URL change error = %v", err)
	}

	anthropic := validControlGroup("settings-anthropic")
	anthropic.Protocols = models.JSON(`["anthropic"]`)
	if err := fixture.db.Create(anthropic).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Publish(mustBuildCompileInput(t, fixture.db)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), anthropic.ID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{state.SettingInjectUsageOptions: true}},
	}); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("inject_usage_options without OpenAI completions error = %v", err)
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision+4 {
		t.Fatalf("invalid settings mutation revision = %d, want %d", got, beforeRevision+4)
	}
}

func TestUpdateGroupSettingsRejectsProtocolChangeThatLeavesInjectUsageOptionsOnNonOpenAIGroup(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://settings-protocol-constraint.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Config: config.Settings{
			state.SettingInjectUsageOptions: true,
		},
		Keys: "sk-settings-protocol-constraint",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	var before models.Group
	if err := fixture.db.First(&before, created.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	beforeRevision := fixture.manager.Current().Revision

	_, err = fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		Protocols: optionalField[[]protocol.Protocol]{
			Set: true, Value: []protocol.Protocol{protocol.Anthropic},
		},
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateGroupSettings() error = %v, want validation", err)
	}
	var after models.Group
	if err := fixture.db.First(&after, created.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("protocol constraint changed persisted group: got=%#v want=%#v", after, before)
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("protocol constraint published revision %d, want %d", got, beforeRevision)
	}
}

func TestGroupSettingsHTTPRejectsStrictJSONAndUnauthorizedWithoutMutation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-settings-http")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	path := "/api/groups/" + stringGroupID(groupID) + "/settings"
	beforeRevision := fixture.manager.Current().Revision

	for _, body := range []string{
		`{"unknown":true}`,
		`{"name":"one","name":"two"}`,
		`{"weight_manual":0}`,
	} {
		recorder := serveGroupSettingsRequest(t, engine, http.MethodPut, path, "test-auth-key", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("PUT settings body %s = %d %s, want 400", body, recorder.Code, recorder.Body.String())
		}
	}
	unauthorized := serveGroupSettingsRequest(t, engine, http.MethodPut, path, "", `{"name":"changed"}`)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized PUT settings = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("rejected HTTP settings updates revision = %d, want %d", got, beforeRevision)
	}
}

func TestGroupSettingsHTTPNotFoundAndDatabaseFailureDoNotMutate(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-settings-errors")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	beforeRevision := fixture.manager.Current().Revision

	missing := serveGroupSettingsRequest(t, engine, http.MethodPut, "/api/groups/999/settings", "test-auth-key", `{"name":"missing"}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing group PUT settings = %d %s, want 404", missing.Code, missing.Body.String())
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("missing group published revision %d, want %d", got, beforeRevision)
	}

	closeMutationAuditDB(t, fixture)
	database := serveGroupSettingsRequest(t, engine, http.MethodPut, "/api/groups/"+stringGroupID(groupID)+"/settings", "test-auth-key", `{"name":"database"}`)
	if database.Code != http.StatusInternalServerError || !strings.Contains(database.Body.String(), app_errors.ErrDatabase.Code) {
		t.Fatalf("database PUT settings = %d %s, want database error", database.Code, database.Body.String())
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision {
		t.Fatalf("database failure published revision %d, want %d", got, beforeRevision)
	}
}

func serveGroupSettingsRequest(t *testing.T, engine *gin.Engine, method, path, authKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func stringGroupID(groupID uint) string {
	return strconv.FormatUint(uint64(groupID), 10)
}

func settingsWeightPointer(value int) *int {
	return &value
}

package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestGetGroupSettingsReturnsPersistedDraftOverridesAndEffectiveConfig(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("settings-read")
	group.Overrides = models.JSON(`{
		"first_byte_timeout":180,
		"request_timeout":480,
		"stream_idle_timeout":270,
		"header_rules":{"set":{"X-Group":"value"},"remove":["X-Removed"]},
		"inject_usage_options":false,
		"affinity_enabled":false
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
	if got.Name != group.Name || got.ChannelID != channel.OpenAICompatible ||
		string(got.Params) != `{"base_url":"https://settings-read.example/v1"}` ||
		!got.Enabled || got.WeightManual != nil {
		t.Fatalf("persisted settings = %#v", got)
	}
	for _, key := range []string{
		state.SettingFirstByteTimeout,
		state.SettingRequestTimeout,
		state.SettingStreamIdleTimeout,
		state.SettingHeaderRules,
		state.SettingInjectUsageOptions,
		state.SettingAffinityEnabled,
	} {
		if got.Overrides[key] == nil {
			t.Fatalf("overrides missing %q: %#v", key, got.Overrides)
		}
	}
	if got.Effective.FirstByteTimeout != 180 ||
		got.Effective.RequestTimeout != 480 || got.Effective.StreamIdleTimeout != 270 ||
		got.Effective.InjectUsageOptions || got.Effective.AffinityEnabled ||
		!reflect.DeepEqual(got.Effective.HeaderRules.Set, map[string]string{"X-Group": "value"}) ||
		!reflect.DeepEqual(got.Effective.HeaderRules.Remove, []string{"X-Removed"}) {
		t.Fatalf("effective = %#v", got.Effective)
	}
}

func TestUpdateGroupSettingsPublishesOnceAndReturnsNewSettings(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":"https://settings-before.example.com/v1"}`),
		Models:    optionalGroupModels{Set: true}, Credentials: "sk-settings-update",
	})
	if err != nil {
		t.Fatal(err)
	}
	groupID := created.GroupID
	beforeRevision := fixture.manager.Current().Revision
	weight := 75
	result, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Name: optionalField[string]{Set: true, Value: " updated settings "},
		Params: optionalField[json.RawMessage]{Set: true,
			Value: json.RawMessage(`{"base_url":" HTTPS://SETTINGS-UPDATED.EXAMPLE.COM/v1/ "}`)},
		ValidationModel: optionalField[string]{Set: true, Value: " gpt-4.1 "},
		Enabled:         optionalField[bool]{Set: true, Value: false},
		WeightManual:    optionalField[int]{Set: true, Value: weight},
		Overrides:       optionalField[config.Settings]{Set: true, Value: config.Settings{"request_timeout": json.Number("720")}},
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings() error = %v", err)
	}
	if result.Name != "updated settings" || result.ChannelID != channel.OpenAICompatible ||
		string(result.Params) != `{"base_url":"https://settings-updated.example.com/v1"}` ||
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

func TestUpdateGroupSettingsOverridesAffinityParticipation(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForCredentialImport(t, fixture, "sk-settings-affinity")

	for _, test := range []struct {
		name     string
		override config.Settings
		want     bool
	}{
		{name: "disable", override: config.Settings{state.SettingAffinityEnabled: false}, want: false},
		{name: "enable", override: config.Settings{state.SettingAffinityEnabled: true}, want: true},
		{name: "inherit", override: config.Settings{}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
				Overrides: optionalField[config.Settings]{Set: true, Value: test.override},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Effective.AffinityEnabled != test.want {
				t.Fatalf("effective affinity = %t, want %t", got.Effective.AffinityEnabled, test.want)
			}
			if view := fixture.manager.Current().Groups[groupID]; view.AffinityEnabled != test.want {
				t.Fatalf("snapshot affinity = %t, want %t", view.AffinityEnabled, test.want)
			}
		})
	}
}

func TestUpdateGroupSettingsAllowsDuplicateUpstreamURLWithoutConfirmation(t *testing.T) {
	fixture := newServiceFixture(t)
	first, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://settings-first.example.com/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "sk-settings-first",
	})
	if err != nil {
		t.Fatalf("first CreateGroup() error = %v", err)
	}
	sharedURL := "https://settings-shared.example.com/v1"
	_, err = fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://settings-shared.example.com/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "sk-settings-second",
	})
	if err != nil {
		t.Fatalf("second CreateGroup() error = %v", err)
	}
	beforeRevision := fixture.manager.Current().Revision

	result, err := fixture.service.UpdateGroupSettings(t.Context(), first.GroupID, GroupSettingsUpdateRequest{
		Params: optionalField[json.RawMessage]{
			Set:   true,
			Value: json.RawMessage(`{"base_url":" HTTPS://SETTINGS-SHARED.EXAMPLE.COM/v1/ "}`),
		},
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings() error = %v", err)
	}
	if string(result.Params) != `{"base_url":"`+sharedURL+`"}` {
		t.Fatalf("updated params = %s, want shared target", result.Params)
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision+1 {
		t.Fatalf("snapshot revision = %d, want %d", got, beforeRevision+1)
	}
}

func TestUpdateGroupTargetResetsCredentialHealthIdentity(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":"https://health-old.example/v1"}`),
		Models:    optionalGroupModels{Set: true}, Credentials: "sk-target-health",
	})
	if err != nil {
		t.Fatal(err)
	}
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	before, exists := findRuntimeCredential(fixture.registry.Snapshot(), credential.ID)
	if !exists || !fixture.registry.SetBlacklisted(credential.ID) {
		t.Fatal("credential runtime state is unavailable")
	}
	now := time.Now().UTC()
	fixture.stats.RecordFailure(
		credential.ID,
		health.FailureCategoryUpstreamHostError,
		http.StatusBadGateway,
		now,
	)

	if _, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		Params: optionalField[json.RawMessage]{
			Set: true, Value: json.RawMessage(`{"base_url":"https://health-new.example/v1"}`),
		},
	}); err != nil {
		t.Fatalf("UpdateGroupSettings() error = %v", err)
	}
	after, exists := findRuntimeCredential(fixture.registry.Snapshot(), credential.ID)
	if !exists {
		t.Fatal("credential disappeared after target update")
	}
	if after.IdentityGeneration == before.IdentityGeneration || after.Blacklisted || after.FailureCount != 0 {
		t.Fatalf("credential health identity before=%#v after=%#v", before, after)
	}
	if got := fixture.stats.Snapshot(credential.ID, now); got != (health.CredentialStats{}) {
		t.Fatalf("credential stats after target update = %#v, want zero", got)
	}
}

func TestUpdateGroupTargetSerializesWithCredentialSecretMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":"https://serialized-old.example/v1"}`),
		Models:    optionalGroupModels{Set: true}, Credentials: "sk-target-serialized",
	})
	if err != nil {
		t.Fatal(err)
	}
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	go fixture.mutations.Do(credential.ID, func() {
		close(entered)
		<-release
	})
	<-entered
	done := make(chan error, 1)
	go func() {
		_, callErr := fixture.service.UpdateGroupSettings(context.Background(), created.GroupID, GroupSettingsUpdateRequest{
			Params: optionalField[json.RawMessage]{Set: true, Value: json.RawMessage(`{"base_url":"https://serialized-new.example/v1"}`)},
		})
		done <- callErr
	}()
	select {
	case callErr := <-done:
		close(release)
		t.Fatalf("group target update completed inside credential mutation: %v", callErr)
	case <-time.After(100 * time.Millisecond):
	}
	var blocked models.Group
	if err := fixture.db.Take(&blocked, created.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	if string(blocked.Params) != `{"base_url":"https://serialized-old.example/v1"}` {
		close(release)
		t.Fatalf("group target changed before credential mutation released: %s", blocked.Params)
	}
	close(release)
	select {
	case callErr := <-done:
		if callErr != nil {
			t.Fatalf("UpdateGroupSettings() error = %v", callErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("group target update did not finish")
	}
}

func TestUpdateGroupSettingsValidatesWeightAndAllowsUsageObservationAcrossChannels(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForCredentialImport(t, fixture, "sk-settings-validation")
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
	anthropic, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("settings-anthropic"), ChannelID: channel.Anthropic,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true}, Credentials: "sk-anthropic",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.service.UpdateGroupSettings(t.Context(), anthropic.GroupID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{state.SettingInjectUsageOptions: true}},
	})
	if err != nil || !updated.Effective.InjectUsageOptions {
		t.Fatalf("cross-channel usage observation update = %#v, %v", updated, err)
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision+5 {
		t.Fatalf("settings mutation revision = %d, want %d", got, beforeRevision+5)
	}
}

func TestGroupSettingsHTTPRejectsStrictJSONAndUnauthorizedWithoutMutation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupForCredentialImport(t, fixture, "sk-settings-http")
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
	groupID := createGroupForCredentialImport(t, fixture, "sk-settings-errors")
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

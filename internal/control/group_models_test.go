package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestGetGroupModelsReturnsClientNamesAndPricingStatus(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai",
			Models: map[string]catalog.Model{
				"gpt-4o": {
					ID: "gpt-4o",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{
						Input: pricing.Price{Set: true, NanoUSDPerMillion: 1},
					}},
				},
			},
		},
	}})
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID: channel.OpenAI,
		Params:    json.RawMessage(`{}`),
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: "gpt-4o", Alias: "default", AliasEnabled: true},
			{ID: "missing-price", Alias: ""},
		}},
		Credentials: "sk-model-read",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.GetGroupModels(t.Context(), created.GroupID)
	if err != nil {
		t.Fatalf("GetGroupModels() error = %v", err)
	}
	want := GroupModelsResponse{
		Items: []GroupModelResponse{
			{ID: "gpt-4o", Alias: "default", AliasEnabled: true, ClientModel: "default", PricingStatus: PricingStatusConfigured},
			{ID: "missing-price", Alias: "", AliasEnabled: false, ClientModel: "missing-price", PricingStatus: PricingStatusPending},
		},
		Total:   2,
		Pending: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetGroupModels() = %#v, want %#v", got, want)
	}
}

func TestMapGroupModelsResponseTreatsContextTierOnlyPriceAsConfigured(t *testing.T) {
	result, err := mapGroupModelsResponse(
		string(channel.OpenAI),
		[]GroupModel{{ID: "tiered-model"}},
		modelPriceRows{
			{ChannelID: string(channel.OpenAI), ModelID: "tiered-model"}: {
				ChannelID:         string(channel.OpenAI),
				ModelID:           "tiered-model",
				ContextPriceTiers: models.JSON(`[{"threshold_tokens":1000,"input_price_nano_usd_per_million_tokens":1,"output_price_nano_usd_per_million_tokens":null,"cache_read_price_nano_usd_per_million_tokens":null,"cache_write_price_nano_usd_per_million_tokens":null}]`),
			},
		},
	)
	if err != nil {
		t.Fatalf("mapGroupModelsResponse() error = %v", err)
	}
	want := GroupModelsResponse{
		Items: []GroupModelResponse{{
			ID: "tiered-model", ClientModel: "tiered-model", PricingStatus: PricingStatusConfigured,
		}},
		Total: 1,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("mapGroupModelsResponse() = %#v, want %#v", result, want)
	}
}

func TestNormalizeGroupModelsAppliesAliasSwitchAndReportsStableConflicts(t *testing.T) {
	tests := []struct {
		name          string
		values        []GroupModel
		want          []GroupModel
		wantConflicts []ModelNameConflict
		wantError     error
	}{
		{
			name: "disabled alias uses upstream ID and conflicts with enabled alias",
			values: []GroupModel{
				{ID: "a", Alias: ""},
				{ID: "b", Alias: "a", AliasEnabled: true},
			},
			wantConflicts: []ModelNameConflict{{ClientModel: "a", Indexes: []int{0, 1}}},
			wantError:     app_errors.ErrModelNameConflict,
		},
		{
			name: "enabled aliases conflict",
			values: []GroupModel{
				{ID: "a", Alias: "x", AliasEnabled: true},
				{ID: "b", Alias: "x", AliasEnabled: true},
			},
			wantConflicts: []ModelNameConflict{{ClientModel: "x", Indexes: []int{0, 1}}},
			wantError:     app_errors.ErrModelNameConflict,
		},
		{
			name: "model names remain case sensitive",
			values: []GroupModel{
				{ID: "a", Alias: "X", AliasEnabled: true},
				{ID: "b", Alias: "x", AliasEnabled: true},
			},
			want: []GroupModel{
				{ID: "a", Alias: "X"},
				{ID: "b", Alias: "x"},
			},
		},
		{
			name: "trimmed IDs and aliases conflict",
			values: []GroupModel{
				{ID: " a ", Alias: ""},
				{ID: "b", Alias: " a ", AliasEnabled: true},
			},
			wantConflicts: []ModelNameConflict{{ClientModel: "a", Indexes: []int{0, 1}}},
			wantError:     app_errors.ErrModelNameConflict,
		},
		{
			name:      "enabled alias cannot be blank after trimming",
			values:    []GroupModel{{ID: "a", Alias: " ", AliasEnabled: true}},
			wantError: app_errors.ErrValidation,
		},
		{
			name: "multiple conflicts use first occurrence order",
			values: []GroupModel{
				{ID: "a"},
				{ID: "b", Alias: "a", AliasEnabled: true},
				{ID: "c"},
				{ID: "d", Alias: "c", AliasEnabled: true},
			},
			wantConflicts: []ModelNameConflict{
				{ClientModel: "a", Indexes: []int{0, 1}},
				{ClientModel: "c", Indexes: []int{2, 3}},
			},
			wantError: app_errors.ErrModelNameConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeGroupModels(test.values)
			if test.wantError == nil {
				if err != nil {
					t.Fatalf("normalizeGroupModels() error = %v", err)
				}
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("normalizeGroupModels() = %#v, want %#v", got, test.want)
				}
				return
			}

			var apiErr *app_errors.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != test.wantError.(*app_errors.APIError).Code {
				t.Fatalf("normalizeGroupModels() error = %#v, want %q", err, test.wantError)
			}
			if test.wantConflicts == nil {
				return
			}
			data, ok := apiErr.Data.(ModelNameConflictData)
			if !ok || !reflect.DeepEqual(data.Conflicts, test.wantConflicts) {
				t.Fatalf("conflict data = %#v, want %#v", apiErr.Data, test.wantConflicts)
			}
		})
	}
}

func TestNormalizeGroupModelsRejectsDuplicateExternalNames(t *testing.T) {
	for _, values := range [][]GroupModel{
		{{ID: "provider-a", Alias: "public", AliasEnabled: true}, {ID: "provider-b", Alias: "public", AliasEnabled: true}},
		{{ID: "public"}, {ID: "provider-b", Alias: "public", AliasEnabled: true}},
	} {
		var apiErr *app_errors.APIError
		if _, err := normalizeGroupModels(values); !errors.As(err, &apiErr) ||
			apiErr.Code != app_errors.ErrModelNameConflict.Code {
			t.Fatalf("normalizeGroupModels(%#v) error = %#v", values, err)
		}
	}
}

func TestUpdateGroupModelsRequiresNonNullModelsField(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForCredentialImport(t, fixture, "sk-required-models")
	for _, request := range []GroupModelsUpdateRequest{
		{},
		{Models: optionalGroupModels{Set: false}},
	} {
		if _, err := fixture.service.UpdateGroupModels(t.Context(), groupID, request); !errors.Is(err, app_errors.ErrValidation) {
			t.Fatalf("request %#v error = %v", request, err)
		}
	}

	var request GroupModelsUpdateRequest
	err := json.Unmarshal([]byte(`{"models":null}`), &request)
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("null models error = %v, want ErrValidation", err)
	}
}

func TestUpdateGroupModelsReplacesAuthoritativeListAndPublishesOnce(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":"https://model-save.example.com/v1"}`),
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "provider-old", Alias: "old-public", AliasEnabled: true}},
		},
		Credentials: "sk-model-save-a\nsk-model-save-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	validation := "validation-model-must-stay"
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		ValidationModel: optionalField[string]{Set: true, Value: validation},
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Group{}).
		Where("id = ?", created.GroupID).
		Update("overrides", models.JSON(`{
			"stream_idle_timeout":45,
			"inject_usage_options":false,
			"header_rules":{"remove":["X-Trace"]}
		}`)).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.SystemSetting{
		Key: state.SettingRequestTimeout, Value: "701",
	}).Error; err != nil {
		t.Fatal(err)
	}
	beforeRevision := fixture.manager.Current().Revision
	beforeRegistry := fixture.registry.Snapshot()
	var beforeCredentials []models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&beforeCredentials).Error; err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.UpdateGroupModels(t.Context(), created.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{
			Set: true,
			Values: []GroupModel{
				{ID: "provider-b", Alias: "public-b", AliasEnabled: true},
				{ID: "provider-a", Alias: "public-a", AliasEnabled: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateGroupModels() error = %v", err)
	}
	wantModels := []GroupModel{
		{ID: "provider-b", Alias: "public-b"},
		{ID: "provider-a", Alias: "public-a"},
	}
	want := GroupModelsResponse{
		Items: []GroupModelResponse{
			{ID: "provider-b", Alias: "public-b", AliasEnabled: true, ClientModel: "public-b", PricingStatus: PricingStatusPending},
			{ID: "provider-a", Alias: "public-a", AliasEnabled: true, ClientModel: "public-a", PricingStatus: PricingStatusPending},
		},
		Total:   2,
		Pending: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models response = %#v, want %#v", got, want)
	}
	settings, err := fixture.service.GetGroupSettings(t.Context(), created.GroupID)
	if err != nil {
		t.Fatalf("GetGroupSettings() error = %v", err)
	}
	summary, err := fixture.service.GetGroupSummary(t.Context(), created.GroupID)
	if err != nil {
		t.Fatalf("GetGroupSummary() error = %v", err)
	}
	if settings.ValidationModel == nil || *settings.ValidationModel != validation || summary.CredentialCount != 2 {
		t.Fatalf("settings/summary = %#v/%#v", settings, summary)
	}
	streamIdle, ok := settings.Overrides[state.SettingStreamIdleTimeout].(json.Number)
	if len(settings.Overrides) != 3 || !ok || streamIdle.String() != "45" ||
		settings.Overrides[state.SettingHeaderRules] == nil ||
		settings.Overrides[state.SettingInjectUsageOptions] != false {
		t.Fatalf("preserved sparse config = %#v", settings.Overrides)
	}
	if settings.Effective.FirstByteTimeout != 120 ||
		settings.Effective.RequestTimeout != 701 ||
		settings.Effective.StreamIdleTimeout != 45 ||
		settings.Effective.InjectUsageOptions ||
		len(settings.Effective.HeaderRules.Set) != 0 ||
		!reflect.DeepEqual(settings.Effective.HeaderRules.Remove, []string{"X-Trace"}) {
		t.Fatalf("post-write effective config = %#v", settings.Effective)
	}
	if settings.Effective.HeaderRules.Set == nil || settings.Effective.HeaderRules.Remove == nil {
		t.Fatalf("effective header collections = %#v", settings.Effective.HeaderRules)
	}
	if stored := loadCreatedGroupModels(t, fixture, created.GroupID); !reflect.DeepEqual(stored, wantModels) {
		t.Fatalf("stored models = %#v, want %#v", stored, wantModels)
	}
	if fixture.manager.Current().Revision != beforeRevision+1 {
		t.Fatalf("revision = %d, want %d", fixture.manager.Current().Revision, beforeRevision+1)
	}
	if !reflect.DeepEqual(fixture.registry.Snapshot(), beforeRegistry) {
		t.Fatal("models save changed Registry")
	}
	var afterCredentials []models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&afterCredentials).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterCredentials, beforeCredentials) {
		t.Fatalf("credentials changed: got=%#v want=%#v", afterCredentials, beforeCredentials)
	}
	snapshot := fixture.manager.Current()
	view := snapshot.Groups[created.GroupID]
	if settings.Effective.RequestTimeout != int64(view.Timeouts.Request/time.Second) ||
		settings.Effective.StreamIdleTimeout != int64(view.Timeouts.StreamIdle/time.Second) ||
		settings.Effective.InjectUsageOptions != view.InjectUsageOptions ||
		!reflect.DeepEqual(settings.Effective.HeaderRules.Remove, view.HeaderRules.Remove) {
		t.Fatalf("effective/snapshot = %#v/%#v", settings.Effective, view)
	}
	targets := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]
	if len(targets) != 2 ||
		targets["public-a"][0].UpstreamModelID != "provider-a" ||
		targets["public-b"][0].UpstreamModelID != "provider-b" {
		t.Fatalf("candidate mapping = %#v", targets)
	}
	if _, exists := targets["old-public"]; exists {
		t.Fatalf("authoritative replacement retained old model: %#v", targets)
	}
	routes := snapshot.ExecutionRouteCatalog[protocol.OpenAICompletions][execution.OperationChatCompletion]
	if len(routes) != 2 ||
		routes["public-a"][0].UpstreamModelID != "provider-a" ||
		routes["public-b"][0].UpstreamModelID != "provider-b" {
		t.Fatalf("route catalog = %#v", routes)
	}
}

func TestUpdateGroupModelsAllowsEmptyList(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":"https://empty-models.example.com/v1"}`),
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "provider-old", Alias: "old-public", AliasEnabled: true}},
		},
		Credentials: "sk-empty-models",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.manager.Current().Revision
	got, err := fixture.service.UpdateGroupModels(t.Context(), created.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{Set: true, Values: []GroupModel{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := GroupModelsResponse{Items: []GroupModelResponse{}, Total: 0, Pending: 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models response = %#v, want %#v", got, want)
	}
	if fixture.manager.Current().Revision != before+1 {
		t.Fatalf("revision = %d, want %d", fixture.manager.Current().Revision, before+1)
	}
	if len(fixture.manager.Current().ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]) != 0 ||
		len(fixture.manager.Current().ExecutionRouteCatalog[protocol.OpenAICompletions][execution.OperationChatCompletion]) != 0 {
		t.Fatalf("model indexes = candidates:%#v routes:%#v",
			fixture.manager.Current().ExecutionCandidates, fixture.manager.Current().ExecutionRouteCatalog)
	}
}

func TestUpdateGroupModelsNeverCallsDiscoveryOrChangesAccessKeyFilters(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	groupID := createGroupForCredentialImport(t, fixture, "sk-no-discovery")
	access, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "filtered",
		Filters: &AccessKeyFilters{
			Groups: []uint{groupID},
			Models: []string{"old-public"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var beforeAccess models.AccessKey
	if err := fixture.db.First(&beforeAccess, access.ID).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			t.Fatal("UpdateGroupModels must not call model discovery")
			return nil, nil
		},
	})

	_, err = fixture.service.UpdateGroupModels(t.Context(), groupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "provider-new", Alias: "new-public", AliasEnabled: true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var afterAccess models.AccessKey
	if err := fixture.db.First(&afterAccess, access.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterAccess, beforeAccess) {
		t.Fatalf("persisted AccessKey changed: got=%#v want=%#v", afterAccess, beforeAccess)
	}
	filters, err := decodeStoredAccessKeyFilters(afterAccess.Filters)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(filters.Models, []string{"old-public"}) {
		t.Fatalf("filters = %#v", filters)
	}
}

func TestUpdateGroupModelsFailuresDoNotPublish(t *testing.T) {
	t.Run("external collision", func(t *testing.T) {
		fixture := newServiceFixture(t)
		groupID := createGroupForCredentialImport(t, fixture, "sk-invalid-models")
		beforeRevision := fixture.manager.Current().Revision
		beforeRegistry := fixture.registry.Snapshot()
		beforeModels := loadCreatedGroupModels(t, fixture, groupID)

		_, err := fixture.service.UpdateGroupModels(t.Context(), groupID, GroupModelsUpdateRequest{
			Models: optionalGroupModels{
				Set: true,
				Values: []GroupModel{
					{ID: "provider-a", Alias: "public", AliasEnabled: true},
					{ID: "provider-b", Alias: "public", AliasEnabled: true},
				},
			},
		})
		var apiErr *app_errors.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrModelNameConflict.Code {
			t.Fatalf("UpdateGroupModels() error = %#v, want MODEL_NAME_CONFLICT", err)
		}
		assertModelsUpdateStateUnchanged(t, fixture, groupID, beforeRevision, beforeRegistry, beforeModels)
	})

	t.Run("full compile failure", func(t *testing.T) {
		fixture := newServiceFixture(t)
		groupID := createGroupForCredentialImport(t, fixture, "sk-compile-models")
		corrupt := validControlGroup("model-save-corrupt-other")
		if err := fixture.db.Create(corrupt).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Exec("UPDATE groups SET channel_id = ? WHERE id = ?", "unknown", corrupt.ID).Error; err != nil {
			t.Fatal(err)
		}
		beforeRevision := fixture.manager.Current().Revision
		beforeRegistry := fixture.registry.Snapshot()
		beforeModels := loadCreatedGroupModels(t, fixture, groupID)

		_, err := fixture.service.UpdateGroupModels(t.Context(), groupID, GroupModelsUpdateRequest{
			Models: optionalGroupModels{
				Set:    true,
				Values: []GroupModel{{ID: "provider-new", Alias: "new-public", AliasEnabled: true}},
			},
		})
		if err == nil {
			t.Fatal("UpdateGroupModels() error = nil, want full Compile failure")
		}
		assertModelsUpdateStateUnchanged(t, fixture, groupID, beforeRevision, beforeRegistry, beforeModels)
	})

	t.Run("commit failure", func(t *testing.T) {
		fixture, dsn := newFileServiceFixture(t)
		created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
			ChannelID: channel.OpenAICompatible,
			Params:    json.RawMessage(`{"base_url":"https://commit-failure-models.example.com/v1"}`),
			Models: optionalGroupModels{
				Set:    true,
				Values: []GroupModel{{ID: "provider-old", Alias: "old-public", AliasEnabled: true}},
			},
			Credentials: "sk-commit-models",
		})
		if err != nil {
			t.Fatal(err)
		}
		beforeRevision := fixture.manager.Current().Revision
		beforeRegistry := fixture.registry.Snapshot()
		beforeModels := loadCreatedGroupModels(t, fixture, created.GroupID)
		releaseReader := holdRollbackJournalReadLock(t, fixture.db, dsn)

		_, err = fixture.service.UpdateGroupModels(t.Context(), created.GroupID, GroupModelsUpdateRequest{
			Models: optionalGroupModels{
				Set:    true,
				Values: []GroupModel{{ID: "provider-new", Alias: "new-public", AliasEnabled: true}},
			},
		})
		var apiErr *app_errors.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrDatabase.Code {
			t.Fatalf("UpdateGroupModels() error = %#v, want DATABASE_ERROR", err)
		}
		releaseReader()
		assertModelsUpdateStateUnchanged(
			t, fixture, created.GroupID, beforeRevision, beforeRegistry, beforeModels,
		)
	})
}

func assertModelsUpdateStateUnchanged(
	t *testing.T,
	fixture serviceFixture,
	groupID uint,
	wantRevision uint64,
	wantRegistry []state.CredentialRuntimeView,
	wantModels []GroupModel,
) {
	t.Helper()
	if fixture.manager.Current().Revision != wantRevision {
		t.Fatalf("revision = %d, want unchanged %d", fixture.manager.Current().Revision, wantRevision)
	}
	if !reflect.DeepEqual(fixture.registry.Snapshot(), wantRegistry) {
		t.Fatal("Registry changed")
	}
	if got := loadCreatedGroupModels(t, fixture, groupID); !reflect.DeepEqual(got, wantModels) {
		t.Fatalf("persisted models changed: got=%#v want=%#v", got, wantModels)
	}
}

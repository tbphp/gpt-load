package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestGetGroupModelsReturnsClientNamesAndPricingStatus(t *testing.T) {
	fixture := newServiceFixture(t)
	mustEnsureInitialPrices(t, fixture)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://model-read.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: "gpt-4o", Alias: "default", AliasEnabled: true},
			{ID: "missing-price", Alias: ""},
		}},
		Keys: "sk-model-read",
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
			{ID: "gpt-4o", Alias: "default", AliasEnabled: true, ClientModel: "default", PricingStatus: "priced"},
			{ID: "missing-price", Alias: "", AliasEnabled: false, ClientModel: "missing-price", PricingStatus: "unpriced"},
		},
		Total:    2,
		Unpriced: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetGroupModels() = %#v, want %#v", got, want)
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
		{{ID: "provider-a", Alias: "public"}, {ID: "provider-b", Alias: "public"}},
		{{ID: "public"}, {ID: "provider-b", Alias: "public"}},
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
	groupID := createGroupForKeyImport(t, fixture, "sk-required-models")
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
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://model-save.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "provider-old", Alias: "old-public"}},
		},
		Keys: "sk-model-save-a\nsk-model-save-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	validation := "validation-model-must-stay"
	if _, err := fixture.service.UpdateGroup(t.Context(), created.GroupID, GroupUpdateRequest{
		ValidationModel: optionalField[string]{Set: true, Value: validation},
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Group{}).
		Where("id = ?", created.GroupID).
		Update("config", models.JSON(`{
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
	var beforeKeys []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&beforeKeys).Error; err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.UpdateGroupModels(t.Context(), created.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{
			Set: true,
			Values: []GroupModel{
				{ID: "provider-b", Alias: "public-b"},
				{ID: "provider-a", Alias: "public-a"},
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
	if !reflect.DeepEqual(got.Models, wantModels) ||
		got.ValidationModel == nil || *got.ValidationModel != validation ||
		got.KeyCount != 2 {
		t.Fatalf("detail = %#v", got)
	}
	streamIdle, ok := got.Config[state.SettingStreamIdleTimeout].(json.Number)
	if len(got.Config) != 3 || !ok || streamIdle.String() != "45" ||
		got.Config[state.SettingHeaderRules] == nil ||
		got.Config[state.SettingInjectUsageOptions] != false {
		t.Fatalf("preserved sparse config = %#v", got.Config)
	}
	if got.EffectiveConfig.ConnectTimeout != 15 ||
		got.EffectiveConfig.FirstByteTimeout != 120 ||
		got.EffectiveConfig.RequestTimeout != 701 ||
		got.EffectiveConfig.StreamIdleTimeout != 45 ||
		got.EffectiveConfig.InjectUsageOptions ||
		len(got.EffectiveConfig.HeaderRules.Set) != 0 ||
		!reflect.DeepEqual(got.EffectiveConfig.HeaderRules.Remove, []string{"X-Trace"}) {
		t.Fatalf("post-write effective config = %#v", got.EffectiveConfig)
	}
	if got.EffectiveConfig.HeaderRules.Set == nil || got.EffectiveConfig.HeaderRules.Remove == nil {
		t.Fatalf("effective header collections = %#v", got.EffectiveConfig.HeaderRules)
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
	var afterKeys []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&afterKeys).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterKeys, beforeKeys) {
		t.Fatalf("upstream keys changed: got=%#v want=%#v", afterKeys, beforeKeys)
	}
	snapshot := fixture.manager.Current()
	view := snapshot.Groups[created.GroupID]
	if got.EffectiveConfig.RequestTimeout != int64(view.Timeouts.Request/time.Second) ||
		got.EffectiveConfig.StreamIdleTimeout != int64(view.Timeouts.StreamIdle/time.Second) ||
		got.EffectiveConfig.InjectUsageOptions != view.InjectUsageOptions ||
		!reflect.DeepEqual(got.EffectiveConfig.HeaderRules.Remove, view.HeaderRules.Remove) {
		t.Fatalf("effective/snapshot = %#v/%#v", got.EffectiveConfig, view)
	}
	targets := snapshot.Candidates[protocol.OpenAICompletions]
	if len(targets) != 2 ||
		targets["public-a"][0].UpstreamModelID != "provider-a" ||
		targets["public-b"][0].UpstreamModelID != "provider-b" {
		t.Fatalf("candidate mapping = %#v", targets)
	}
	if _, exists := targets["old-public"]; exists {
		t.Fatalf("authoritative replacement retained old model: %#v", targets)
	}
	routes := snapshot.RouteCatalog[protocol.OpenAICompletions]
	if len(routes) != 2 ||
		routes["public-a"][0].UpstreamModelID != "provider-a" ||
		routes["public-b"][0].UpstreamModelID != "provider-b" {
		t.Fatalf("route catalog = %#v", routes)
	}
}

func TestUpdateGroupModelsAllowsEmptyList(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://empty-models.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "provider-old", Alias: "old-public"}},
		},
		Keys: "sk-empty-models",
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
	if got.Models == nil || len(got.Models) != 0 {
		t.Fatalf("models = %#v, want []", got.Models)
	}
	if fixture.manager.Current().Revision != before+1 {
		t.Fatalf("revision = %d, want %d", fixture.manager.Current().Revision, before+1)
	}
	if len(fixture.manager.Current().Candidates[protocol.OpenAICompletions]) != 0 ||
		len(fixture.manager.Current().RouteCatalog[protocol.OpenAICompletions]) != 0 {
		t.Fatalf("model indexes = candidates:%#v routes:%#v",
			fixture.manager.Current().Candidates, fixture.manager.Current().RouteCatalog)
	}
}

func TestUpdateGroupModelsNeverCallsDiscoveryOrChangesAccessKeyFilters(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-no-discovery")
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
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			t.Fatal("UpdateGroupModels must not call model discovery")
			return nil, nil
		},
	})

	_, err = fixture.service.UpdateGroupModels(t.Context(), groupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "provider-new", Alias: "new-public"}},
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
		groupID := createGroupForKeyImport(t, fixture, "sk-invalid-models")
		beforeRevision := fixture.manager.Current().Revision
		beforeRegistry := fixture.registry.Snapshot()
		beforeModels := loadCreatedGroupModels(t, fixture, groupID)

		_, err := fixture.service.UpdateGroupModels(t.Context(), groupID, GroupModelsUpdateRequest{
			Models: optionalGroupModels{
				Set: true,
				Values: []GroupModel{
					{ID: "provider-a", Alias: "public"},
					{ID: "provider-b", Alias: "public"},
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
		groupID := createGroupForKeyImport(t, fixture, "sk-compile-models")
		corrupt := validControlGroup("model-save-corrupt-other")
		if err := fixture.db.Create(corrupt).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Exec("UPDATE groups SET protocols = ? WHERE id = ?", `[]`, corrupt.ID).Error; err != nil {
			t.Fatal(err)
		}
		beforeRevision := fixture.manager.Current().Revision
		beforeRegistry := fixture.registry.Snapshot()
		beforeModels := loadCreatedGroupModels(t, fixture, groupID)

		_, err := fixture.service.UpdateGroupModels(t.Context(), groupID, GroupModelsUpdateRequest{
			Models: optionalGroupModels{
				Set:    true,
				Values: []GroupModel{{ID: "provider-new", Alias: "new-public"}},
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
			UpstreamURL: "https://commit-failure-models.example.com/v1",
			Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
			Models: optionalGroupModels{
				Set:    true,
				Values: []GroupModel{{ID: "provider-old", Alias: "old-public"}},
			},
			Keys: "sk-commit-models",
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
				Values: []GroupModel{{ID: "provider-new", Alias: "new-public"}},
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
	wantRegistry []state.KeyRuntimeView,
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

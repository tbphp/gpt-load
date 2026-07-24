package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"gpt-load/internal/dialect"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestNormalizeGroupModelsRejectsDuplicateExternalNames(t *testing.T) {
	for _, values := range [][]GroupModel{
		{{ID: "provider-a", Alias: "public"}, {ID: "provider-b", Alias: "public"}},
		{{ID: "public"}, {ID: "provider-b", Alias: "public"}},
	} {
		if _, err := normalizeGroupModels(values); !errors.Is(err, app_errors.ErrValidation) {
			t.Fatalf("normalizeGroupModels(%#v) error = %v", values, err)
		}
	}
	got, err := normalizeGroupModels([]GroupModel{
		{ID: "provider", Alias: "a"},
		{ID: "provider", Alias: "b"},
		{ID: " provider ", Alias: " a "},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []GroupModel{{ID: "provider", Alias: "a"}, {ID: "provider", Alias: "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %#v, want %#v", got, want)
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
		Protocols:   []protocol.Protocol{protocol.OpenAI},
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
				{ID: " provider-a ", Alias: " public-a "},
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
	targets := snapshot.Candidates[protocol.OpenAI]
	if len(targets) != 2 ||
		targets["public-a"][0].UpstreamModelID != "provider-a" ||
		targets["public-b"][0].UpstreamModelID != "provider-b" {
		t.Fatalf("candidate mapping = %#v", targets)
	}
	if _, exists := targets["old-public"]; exists {
		t.Fatalf("authoritative replacement retained old model: %#v", targets)
	}
	routes := snapshot.RouteCatalog[protocol.OpenAI]
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
		Protocols:   []protocol.Protocol{protocol.OpenAI},
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
	if len(fixture.manager.Current().Candidates[protocol.OpenAI]) != 0 ||
		len(fixture.manager.Current().RouteCatalog[protocol.OpenAI]) != 0 {
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
		value: protocol.OpenAI,
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
		if !errors.Is(err, app_errors.ErrValidation) {
			t.Fatalf("UpdateGroupModels() error = %v, want ErrValidation", err)
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
			Protocols:   []protocol.Protocol{protocol.OpenAI},
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

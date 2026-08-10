package control

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
)

func TestMapGroupRowToStateCarriesDeepClonedParams(t *testing.T) {
	group := models.Group{
		ID: 1, Name: "channel-group", ChannelID: string(channel.OpenAICompatible),
		Params: models.JSON(`{"base_url":"https://api.example.com/v1"}`),
		Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: false,
	}

	got, err := mapGroupRowToState(group)
	if err != nil {
		t.Fatalf("mapGroupRowToState() error = %v", err)
	}
	group.Params[0] = '['
	if string(got.Params) != `{"base_url":"https://api.example.com/v1"}` {
		t.Fatalf("GroupConfig.Params = %s, want independent canonical params", got.Params)
	}
}

func TestGroupCatalogSyncTriggerOnlyTracksProviderAndModelIDChanges(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID: channel.OpenAI,
		Params:    json.RawMessage(`{}`),
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: "model-a", Alias: "public-a", AliasEnabled: true},
		}},
		Credentials: "sk-catalog-trigger",
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := newCatalogSyncCoordinator(
		fixture.service,
		nil,
		filepath.Join(t.TempDir(), "catalog.json"),
		catalog.Metadata{},
		false,
	)
	if _, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.Anthropic,
		Params:      json.RawMessage(`{}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "created-model"}}},
		Credentials: "sk-catalog-trigger-created",
	}); err != nil {
		t.Fatal(err)
	}
	assertCatalogGroupWake(t, coordinator)

	if _, err := fixture.service.UpdateGroupModels(t.Context(), created.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: "model-a", Alias: "renamed", AliasEnabled: true},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	assertNoCatalogGroupWake(t, coordinator)

	if _, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		Params: optionalField[json.RawMessage]{Set: true, Value: json.RawMessage(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	assertNoCatalogGroupWake(t, coordinator)

	if _, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		Name: optionalField[string]{Set: true, Value: "renamed-group"},
	}); err != nil {
		t.Fatal(err)
	}
	assertNoCatalogGroupWake(t, coordinator)

	if _, err := fixture.service.UpdateGroupModels(t.Context(), created.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{Set: true, Values: []GroupModel{{ID: "model-b"}}},
	}); err != nil {
		t.Fatal(err)
	}
	assertCatalogGroupWake(t, coordinator)

	custom, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://catalog-trigger-custom.example.com/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "custom-a"}}},
		Credentials: "sk-catalog-trigger-custom",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogGroupWake(t, coordinator)
	if _, err := fixture.service.UpdateGroupModels(t.Context(), custom.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{Set: true, Values: []GroupModel{{ID: "custom-b"}}},
	}); err != nil {
		t.Fatal(err)
	}
	assertCatalogGroupWake(t, coordinator)
	if err := fixture.service.DeleteGroup(t.Context(), custom.GroupID); err != nil {
		t.Fatal(err)
	}
	assertCatalogGroupWake(t, coordinator)
	if err := fixture.service.DeleteGroup(t.Context(), created.GroupID); err != nil {
		t.Fatal(err)
	}
	assertCatalogGroupWake(t, coordinator)
}

func assertCatalogGroupWake(t *testing.T, coordinator *CatalogSyncCoordinator) {
	t.Helper()
	select {
	case <-coordinator.groupWake:
	default:
		t.Fatal("catalog group sync was not requested")
	}
}

func assertNoCatalogGroupWake(t *testing.T, coordinator *CatalogSyncCoordinator) {
	t.Helper()
	select {
	case <-coordinator.groupWake:
		t.Fatal("irrelevant group change requested catalog sync")
	default:
	}
}

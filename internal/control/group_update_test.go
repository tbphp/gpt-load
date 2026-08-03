package control

import (
	"path/filepath"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

func TestMapGroupRowToStateCarriesDeepClonedProviderID(t *testing.T) {
	providerID := "openai"
	group := models.Group{
		ID:          1,
		Name:        "provider-group",
		ProviderID:  &providerID,
		UpstreamURL: "https://api.openai.com/v1",
		Protocols:   models.JSON(`["openai-responses"]`),
		Models:      models.JSON(`[]`),
		Config:      models.JSON(`{}`),
		Enabled:     false,
	}

	got, err := mapGroupRowToState(group)
	if err != nil {
		t.Fatalf("mapGroupRowToState() error = %v", err)
	}
	providerID = "mutated-source"
	*group.ProviderID = "mutated-row"
	if got.ProviderID == nil || *got.ProviderID != "openai" {
		t.Fatalf("GroupConfig.ProviderID = %v, want independent openai", got.ProviderID)
	}
}

func TestGroupCatalogSyncTriggerOnlyTracksProviderAndModelIDChanges(t *testing.T) {
	fixture := newServiceFixture(t)
	initialProviderID := "openai"
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ProviderID:  &initialProviderID,
		UpstreamURL: "https://catalog-trigger.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: "model-a", Alias: "public-a", AliasEnabled: true},
		}},
		Keys: "sk-catalog-trigger",
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
	createdProviderID := "anthropic"
	if _, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ProviderID:  &createdProviderID,
		UpstreamURL: "https://catalog-trigger-created.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "created-model"}}},
		Keys:        "sk-catalog-trigger-created",
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
		UpstreamURL: optionalField[string]{Set: true, Value: "https://catalog-trigger-new-url.example.com/v1"},
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

	providerID := "anthropic"
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		ProviderID: optionalField[string]{Set: true, Value: providerID},
	}); err != nil {
		t.Fatal(err)
	}
	assertCatalogGroupWake(t, coordinator)

	custom, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://catalog-trigger-custom.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "custom-a"}}},
		Keys:        "sk-catalog-trigger-custom",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNoCatalogGroupWake(t, coordinator)
	if _, err := fixture.service.UpdateGroupModels(t.Context(), custom.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{Set: true, Values: []GroupModel{{ID: "custom-b"}}},
	}); err != nil {
		t.Fatal(err)
	}
	assertNoCatalogGroupWake(t, coordinator)
	if err := fixture.service.DeleteGroup(t.Context(), custom.GroupID); err != nil {
		t.Fatal(err)
	}
	assertNoCatalogGroupWake(t, coordinator)
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

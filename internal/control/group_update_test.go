package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestOptionalFieldDistinguishesMissingNullAndZero(t *testing.T) {
	var request GroupUpdateRequest
	if err := json.Unmarshal([]byte(`{
		"enabled":false,
		"validation_model":null,
		"weight_manual":0,
		"config":{}
	}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Name.Set {
		t.Fatal("missing name marked set")
	}
	if !request.Enabled.Set || request.Enabled.Null || request.Enabled.Value {
		t.Fatalf("enabled = %#v", request.Enabled)
	}
	if !request.ValidationModel.Set || !request.ValidationModel.Null {
		t.Fatalf("validation_model = %#v", request.ValidationModel)
	}
	if !request.WeightManual.Set || request.WeightManual.Null || request.WeightManual.Value != 0 {
		t.Fatalf("weight_manual = %#v", request.WeightManual)
	}
	if !request.Config.Set || request.Config.Null || request.Config.Value == nil {
		t.Fatalf("config = %#v", request.Config)
	}
}

func TestGroupUpdateRejectsNullForNonNullableFields(t *testing.T) {
	for _, body := range []string{
		`{"name":null}`,
		`{"enabled":null}`,
		`{"upstream_url":null}`,
		`{"protocols":null}`,
		`{"config":null}`,
		`{"confirm_upstream_url_change":null}`,
	} {
		t.Run(body, func(t *testing.T) {
			var request GroupUpdateRequest
			if err := json.Unmarshal([]byte(body), &request); err != nil {
				t.Fatal(err)
			}
			_, err := normalizeGroupUpdate(request)
			if !errors.Is(err, app_errors.ErrValidation) {
				t.Fatalf("normalizeGroupUpdate(%s) error = %v", body, err)
			}
		})
	}
}

func TestOptionalFieldResetsReuseStateBeforeDecoding(t *testing.T) {
	t.Run("null then value clears null", func(t *testing.T) {
		var field optionalField[string]
		if err := json.Unmarshal([]byte(`null`), &field); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(`"updated"`), &field); err != nil {
			t.Fatal(err)
		}
		if !field.Set || field.Null || field.Value != "updated" {
			t.Fatalf("field = %#v", field)
		}
	})

	t.Run("map and slice replace prior values", func(t *testing.T) {
		settings := optionalField[config.Settings]{
			Set: true,
			Value: config.Settings{
				"first_byte_timeout": json.Number("180"),
				"request_timeout":    json.Number("300"),
			},
		}
		if err := json.Unmarshal([]byte(`{"first_byte_timeout":120}`), &settings); err != nil {
			t.Fatal(err)
		}
		wantSettings := config.Settings{"first_byte_timeout": json.Number("120")}
		if !reflect.DeepEqual(settings.Value, wantSettings) {
			t.Fatalf("settings = %#v, want %#v", settings.Value, wantSettings)
		}

		protocols := optionalField[[]protocol.Protocol]{
			Set:   true,
			Value: []protocol.Protocol{protocol.OpenAI, protocol.Anthropic},
		}
		if err := json.Unmarshal([]byte(`["gemini"]`), &protocols); err != nil {
			t.Fatal(err)
		}
		wantProtocols := []protocol.Protocol{protocol.Gemini}
		if !reflect.DeepEqual(protocols.Value, wantProtocols) {
			t.Fatalf("protocols = %#v, want %#v", protocols.Value, wantProtocols)
		}
	})
}

func TestUpdateGroupReplacesOnlySuppliedFieldsAndPublishesOnce(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-update-preserved")
	beforeRevision := fixture.manager.Current().Revision
	name := optionalField[string]{Set: true, Value: "  renamed  "}
	enabled := optionalField[bool]{Set: true, Value: false}
	validation := optionalField[string]{Set: true, Null: true}
	weight := optionalField[int]{Set: true, Value: 0}
	settings := optionalField[config.Settings]{
		Set:   true,
		Value: config.Settings{"first_byte_timeout": json.Number("180")},
	}

	result, err := fixture.service.UpdateGroup(t.Context(), groupID, GroupUpdateRequest{
		Name: name, Enabled: enabled, ValidationModel: validation,
		WeightManual: weight, Config: settings,
	})
	if err != nil {
		t.Fatalf("UpdateGroup() error = %v", err)
	}
	if result.Group.Name != "renamed" || result.Group.Enabled ||
		result.Group.ValidationModel != nil ||
		result.Group.WeightManual == nil || *result.Group.WeightManual != 0 ||
		result.ModelRediscoveryRecommended {
		t.Fatalf("result = %#v", result)
	}
	if got := fixture.manager.Current().Revision; got != beforeRevision+1 {
		t.Fatalf("revision = %d, want %d", got, beforeRevision+1)
	}
	var keyRows []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", groupID).Find(&keyRows).Error; err != nil {
		t.Fatal(err)
	}
	if len(keyRows) != 1 {
		t.Fatalf("keys changed = %#v", keyRows)
	}
	if _, ok := fixture.registry.EncryptedValue(keyRows[0].ID); !ok {
		t.Fatal("Registry key was replaced or removed")
	}
}

func TestUpdateGroupURLStateMachine(t *testing.T) {
	fixture := newServiceFixture(t)
	firstID := createGroupForKeyImport(t, fixture, "sk-url-first")
	second := validControlGroup("url-conflict")
	second.UpstreamURL = "https://conflict.example.com/v1"
	if err := fixture.db.Create(second).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Publish(mustBuildCompileInput(t, fixture.db)); err != nil {
		t.Fatal(err)
	}
	beforeRevision := fixture.manager.Current().Revision

	_, err := fixture.service.UpdateGroup(t.Context(), firstID, GroupUpdateRequest{
		UpstreamURL: optionalField[string]{Set: true, Value: "https://unique.example.com/v1"},
	})
	if !errors.Is(err, app_errors.ErrUpstreamURLChangeConfirmationRequired) {
		t.Fatalf("unconfirmed error = %v", err)
	}
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatal("unconfirmed URL change published")
	}

	_, err = fixture.service.UpdateGroup(t.Context(), firstID, GroupUpdateRequest{
		UpstreamURL:              optionalField[string]{Set: true, Value: "https://conflict.example.com/v1/"},
		ConfirmUpstreamURLChange: optionalField[bool]{Set: true, Value: true},
	})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != app_errors.ErrUpstreamURLConflict.Code {
		t.Fatalf("conflict error = %#v", err)
	}
	conflicts := apiErr.Data.(UpstreamURLConflictData)
	if len(conflicts.Groups) != 1 || conflicts.Groups[0].ID != second.ID {
		t.Fatalf("conflicts = %#v", conflicts)
	}

	result, err := fixture.service.UpdateGroup(t.Context(), firstID, GroupUpdateRequest{
		UpstreamURL:              optionalField[string]{Set: true, Value: " HTTPS://UNIQUE.example.com/v1/ "},
		ConfirmUpstreamURLChange: optionalField[bool]{Set: true, Value: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ModelRediscoveryRecommended ||
		result.Group.UpstreamURL != "https://unique.example.com/v1" {
		t.Fatalf("confirmed result = %#v", result)
	}
	if fixture.manager.Current().Revision != beforeRevision+1 {
		t.Fatalf("revision = %d, want %d", fixture.manager.Current().Revision, beforeRevision+1)
	}
}

func TestUpdateGroupSameNormalizedURLNeedsNoConfirmationButStillPublishes(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-same-url")
	before := fixture.manager.Current().Revision
	result, err := fixture.service.UpdateGroup(t.Context(), groupID, GroupUpdateRequest{
		UpstreamURL: optionalField[string]{Set: true, Value: " HTTPS://KEY-IMPORT.EXAMPLE.COM/v1/ "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelRediscoveryRecommended {
		t.Fatal("same normalized URL recommends rediscovery")
	}
	if fixture.manager.Current().Revision != before+1 {
		t.Fatalf("revision = %d", fixture.manager.Current().Revision)
	}
}

func TestUpdateGroupURLChangePreservesModelsKeysAndRegistryWithoutDiscovery(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://update-preserve.example.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Models: optionalGroupModels{
			Set:    true,
			Values: []GroupModel{{ID: "configured-model", Alias: "primary"}},
		},
		Keys: "sk-update-preserve-a\nsk-update-preserve-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.OpenAI,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			t.Fatal("UpdateGroup must not discover models")
			return nil, nil
		},
	})

	var beforeGroup models.Group
	if err := fixture.db.First(&beforeGroup, created.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	var beforeKeys []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&beforeKeys).Error; err != nil {
		t.Fatal(err)
	}
	beforeRegistry := fixture.registry.CollectCandidates([]uint{created.GroupID}, nil, time.Time{})
	beforeEncrypted := make(map[uint]string, len(beforeKeys))
	for _, key := range beforeKeys {
		value, ok := fixture.registry.EncryptedValue(key.ID)
		if !ok {
			t.Fatalf("Registry key %d missing before update", key.ID)
		}
		beforeEncrypted[key.ID] = value
	}

	result, err := fixture.service.UpdateGroup(t.Context(), created.GroupID, GroupUpdateRequest{
		UpstreamURL:              optionalField[string]{Set: true, Value: "https://updated-preserve.example.com/v1"},
		ConfirmUpstreamURLChange: optionalField[bool]{Set: true, Value: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ModelRediscoveryRecommended || !reflect.DeepEqual(result.Group.Models, beforeGroupModels(t, beforeGroup)) {
		t.Fatalf("result = %#v", result)
	}

	var afterGroup models.Group
	if err := fixture.db.First(&afterGroup, created.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterGroup.Models, beforeGroup.Models) {
		t.Fatalf("models changed: got=%s want=%s", afterGroup.Models, beforeGroup.Models)
	}
	var afterKeys []models.UpstreamKey
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&afterKeys).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterKeys, beforeKeys) {
		t.Fatalf("persisted keys changed: got=%#v want=%#v", afterKeys, beforeKeys)
	}
	if got := fixture.registry.CollectCandidates([]uint{created.GroupID}, nil, time.Time{}); !reflect.DeepEqual(got, beforeRegistry) {
		t.Fatalf("Registry candidates changed: got=%#v want=%#v", got, beforeRegistry)
	}
	for keyID, want := range beforeEncrypted {
		if got, ok := fixture.registry.EncryptedValue(keyID); !ok || got != want {
			t.Fatalf("Registry key %d = %q, %t, want %q", keyID, got, ok, want)
		}
	}
}

func TestUpdateGroupEmptyConfigClearsPersistedAndReturnedSettings(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "sk-empty-config")
	_, err := fixture.service.UpdateGroup(t.Context(), groupID, GroupUpdateRequest{
		Config: optionalField[config.Settings]{
			Set: true,
			Value: config.Settings{
				"first_byte_timeout": json.Number("180"),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var beforeClear models.Group
	if err := fixture.db.First(&beforeClear, groupID).Error; err != nil {
		t.Fatal(err)
	}
	if string(beforeClear.Config) != `{"first_byte_timeout":180}` {
		t.Fatalf("persisted config before clear = %s, want non-empty seeded config", beforeClear.Config)
	}
	beforeClearDTO, err := fixture.service.GetGroup(t.Context(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	timeout, ok := beforeClearDTO.Config["first_byte_timeout"].(json.Number)
	if !ok || timeout.String() != "180" || len(beforeClearDTO.Config) != 1 {
		t.Fatalf("returned config before clear = %#v, want first_byte_timeout=180 only", beforeClearDTO.Config)
	}

	result, err := fixture.service.UpdateGroup(t.Context(), groupID, GroupUpdateRequest{
		Config: optionalField[config.Settings]{Set: true, Value: config.Settings{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Group.Config == nil || len(result.Group.Config) != 0 {
		t.Fatalf("returned config = %#v, want empty object", result.Group.Config)
	}
	var stored models.Group
	if err := fixture.db.First(&stored, groupID).Error; err != nil {
		t.Fatal(err)
	}
	if string(stored.Config) != "{}" {
		t.Fatalf("persisted config = %s, want {}", stored.Config)
	}
}

func beforeGroupModels(t *testing.T, group models.Group) []GroupModel {
	t.Helper()
	var result []GroupModel
	if err := json.Unmarshal(group.Models, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func mustBuildCompileInput(t *testing.T, db *gorm.DB) state.CompileInput {
	t.Helper()
	input, err := stateloader.BuildCompileInput(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

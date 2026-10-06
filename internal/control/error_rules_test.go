package control

import (
	"encoding/json"
	"errors"
	"testing"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
)

func TestCustomErrorSettingsPersistPublishAndRespectEmptyOverride(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "test-custom-error-key")
	const ruleJSON = `[{"status_codes":[403],"keywords":["欠费"],"retry":"next_candidate","effect":"record_credential_failure"}]`
	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingErrorRules: json.RawMessage(ruleJSON),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Values.ErrorRules) != 1 || len(fixture.manager.Current().Groups[groupID].ErrorRules.Rules()) != 1 {
		t.Fatal("system rules were not returned and inherited by the group")
	}
	read, err := fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Effective.ErrorRules) != 1 {
		t.Fatal("effective group response omitted inherited rules")
	}
	group, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{state.SettingErrorRules: []any{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := group.Overrides[state.SettingErrorRules]; !exists || len(group.Effective.ErrorRules) != 0 || len(group.DefaultErrorRules) != 1 || len(fixture.manager.Current().Groups[groupID].ErrorRules.Rules()) != 0 {
		t.Fatal("empty override did not suppress global rules")
	}
	before := fixture.manager.Current()
	_, err = fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingErrorRules: json.RawMessage(`[{"status_codes":[403],"retry":"refresh_credential","effect":"none"}]`),
	}})
	if !errors.Is(err, app_errors.ErrValidation) || fixture.manager.Current() != before {
		t.Fatalf("invalid system rules changed the snapshot: %v", err)
	}
	_, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{state.SettingErrorRules: []any{map[string]any{
			"retry": "none", "effect": "none",
		}}}},
	})
	if !errors.Is(err, app_errors.ErrValidation) || fixture.manager.Current() != before {
		t.Fatalf("invalid group rules changed the snapshot: %v", err)
	}
	group, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{}},
	})
	if err != nil || len(group.Effective.ErrorRules) != 1 {
		t.Fatalf("restoring inheritance failed: %v", err)
	}
	reset, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingErrorRules: json.RawMessage("null"),
	}})
	if err != nil || len(reset.Values.ErrorRules) != 0 {
		t.Fatalf("resetting system rules failed: %v", err)
	}
}

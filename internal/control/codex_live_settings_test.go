package control

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

func TestCodexLiveModePersistsInSystemAndGroupSettings(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "voice-settings")
	for _, mode := range []state.CodexLiveMode{state.CodexLiveOff, state.CodexLiveDirect, state.CodexLiveRelay} {
		raw, _ := json.Marshal(mode)
		got, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{state.SettingCodexLiveMode: raw}})
		if err != nil || got.Values.CodexLiveMode != mode {
			t.Fatalf("system mode = %v, %v", got.Values.CodexLiveMode, err)
		}
		group, err := fixture.service.GetGroupSettings(t.Context(), groupID)
		if err != nil || group.Effective.CodexLiveMode != mode {
			t.Fatalf("inherited mode = %v, %v", group.Effective.CodexLiveMode, err)
		}
	}
	group, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{state.SettingCodexLiveMode: "off"}}})
	if err != nil || group.Effective.CodexLiveMode != state.CodexLiveOff {
		t.Fatalf("override = %v, %v", group.Effective.CodexLiveMode, err)
	}
	reloaded := state.NewManager()
	if err := stateloader.New(fixture.db, reloaded, state.NewCredentialRegistry()).Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reloaded.Current().Settings.CodexLiveMode != state.CodexLiveRelay || reloaded.Current().Groups[groupID].CodexLiveMode != state.CodexLiveOff {
		t.Fatal("modes not preserved after reload")
	}
	group, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{}}})
	if err != nil || group.Effective.CodexLiveMode != state.CodexLiveRelay {
		t.Fatal("group did not restore inherited mode")
	}
	got, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{state.SettingCodexLiveMode: json.RawMessage(`null`)}})
	if err != nil || got.Values.CodexLiveMode != state.CodexLiveDirect {
		t.Fatal("system did not restore default direct mode")
	}
}

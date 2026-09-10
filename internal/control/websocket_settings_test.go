package control

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/platform/config"
)

func TestResponsesWebsocketSettingsInheritAndPersist(t *testing.T) {
	const key = "responses_websocket_enabled"
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-websocket-settings")
	assertValue := func(value any, want bool) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if got, exists := fields[key]; !exists || got != want {
			t.Fatalf("effective switch=%v exists=%t want=%t", got, exists, want)
		}
	}
	settings, err := fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertValue(settings.Values, true)
	for _, test := range []struct {
		global bool
		group  config.Settings
		want   bool
	}{
		{false, config.Settings{}, false},
		{false, config.Settings{key: true}, true},
		{true, config.Settings{key: false}, false},
		{true, config.Settings{}, true},
	} {
		global, _ := json.Marshal(test.global)
		if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{key: global}}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{Overrides: optionalField[config.Settings]{Set: true, Value: test.group}}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.manager.Publish(mustBuildCompileInput(t, fixture.db)); err != nil {
			t.Fatal(err)
		}
		group, err := fixture.service.GetGroupSettings(t.Context(), groupID)
		if err != nil {
			t.Fatal(err)
		}
		assertValue(group.Effective, test.want)
		if _, exists := group.Overrides[key]; exists != (len(test.group) > 0) {
			t.Fatalf("inheritance was not persisted: %#v", group.Overrides)
		}
	}
	for _, invalid := range []json.RawMessage{json.RawMessage(`"false"`), json.RawMessage(`0`)} {
		if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{key: invalid}}); err == nil {
			t.Fatalf("accepted non-boolean setting %s", invalid)
		}
	}
	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{key: json.RawMessage(`null`)}}); err != nil {
		t.Fatal(err)
	}
	settings, err = fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertValue(settings.Values, true)
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{key: "false"}}}); err == nil {
		t.Fatal("accepted a non-boolean group override")
	}
}

package control

import (
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/requestaudit"
)

func TestSettingsExposeGuardrailPresetAlongsideSavedRules(t *testing.T) {
	fixture := newServiceFixture(t)
	response, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		"request_audit": json.RawMessage(`{"enabled":false,"access_key_ids":[],"rules":[]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Values.RequestAudit.Rules) != 0 {
		t.Fatal("preset replaced saved rules")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Preset requestaudit.Config `json:"request_audit_preset"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Preset.Rules) != 3 || payload.Preset.Enabled || len(payload.Preset.AccessKeyIDs) != 0 {
		t.Fatalf("missing reusable guardrail preset: %+v", payload.Preset)
	}
}

func TestExperimentalSettingsPersistSharedJevAndGuardrails(t *testing.T) {
	fixture := newServiceFixture(t)
	response, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		"jev":           json.RawMessage(`{"model":"","group_id":0,"timeout_seconds":3}`),
		"request_audit": json.RawMessage(`{"enabled":false,"access_key_ids":[],"rules":[]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(response)
	if !strings.Contains(string(encoded), `"request_audit":{"enabled":false`) || !strings.Contains(string(encoded), `"jev":{"model":""`) {
		t.Fatalf("missing experimental settings: %s", encoded)
	}
}

func TestGuardrailsRequireExplicitJevRoute(t *testing.T) {
	fixture := newServiceFixture(t)
	_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		"request_audit": json.RawMessage(`{"enabled":true}`),
	}})
	if err == nil {
		t.Fatal("semantic audit enabled without an explicit route")
	}
}

func TestSharedJevResetDoesNotRestoreTheLegacyAutoModelConnection(t *testing.T) {
	fixture := newServiceFixture(t)
	for _, settings := range []map[string]json.RawMessage{
		{"auto_model": json.RawMessage(`{"enabled":false,"model":"legacy-jev","timeout_seconds":4,"models":[]}`)},
		{"jev": json.RawMessage(`{"model":"shared-jev","group_id":0,"timeout_seconds":3}`)},
	} {
		if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: settings}); err != nil {
			t.Fatal(err)
		}
	}
	response, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{"jev": json.RawMessage(`null`)}})
	if err != nil || response.Values.Jev.Model != "" || response.Values.Jev.TimeoutSeconds != 2 {
		t.Fatalf("reset revived nested connection: %#v %v", response.Values.Jev, err)
	}
}

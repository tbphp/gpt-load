package control

import (
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/automodel"
	"gpt-load/internal/storage/models"
)

func TestAutoModelSettingsPersistOnlyChannelBackedConfig(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	const raw = `{"enabled":false,"model":"jev-router","timeout_seconds":4,"models":[]}`
	response, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{"auto_model": json.RawMessage(raw)},
	})
	if err != nil {
		t.Fatalf("save automatic model settings: %v", err)
	}
	encoded, _ := json.Marshal(response)
	for _, removed := range []string{"api_key", "api_key_configured", "provider", "input_price", "output_price"} {
		if strings.Contains(string(encoded), `"`+removed+`"`) {
			t.Fatalf("removed field %q returned: %s", removed, encoded)
		}
	}

	var row models.SystemSetting
	if err := fixture.db.Where("key = ?", automodel.SettingKey).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.encryption.Decrypt(row.Value)
	if err != nil || plaintext != raw {
		t.Fatalf("stored config = %q, error = %v", plaintext, err)
	}
}

func TestAutoModelSettingsRejectLegacyDirectCredentials(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	legacy := json.RawMessage(`{"enabled":false,"provider":"openrouter","model":"jev-router","api_key":"secret","timeout_seconds":2,"input_price":"0.042","output_price":"0","models":[]}`)
	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{"auto_model": legacy},
	}); err == nil {
		t.Fatal("legacy direct credential fields were accepted")
	}
}

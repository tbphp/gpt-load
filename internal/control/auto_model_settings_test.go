package control

import (
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/storage/models"
)

func TestAutoModelSettingsEncryptAndPreserveSecret(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	const raw = `{"enabled":false,"provider":"openrouter","model":"~typesafe/jev-latest","api_key":"decision-test-secret","timeout_seconds":2,"input_price":"0.042","output_price":"0","models":[]}`
	response, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{"auto_model": json.RawMessage(raw)},
	})
	if err != nil {
		t.Fatalf("save automatic model settings: %v", err)
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), "decision-test-secret") || !strings.Contains(string(encoded), `"api_key_configured":true`) {
		t.Fatalf("secret must be masked in response: %s", encoded)
	}
	var row models.SystemSetting
	if err := fixture.db.Where("key = ?", "auto_model").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.Value, "decision-test-secret") {
		t.Fatal("decision secret stored as plaintext")
	}
	withoutKey := strings.Replace(raw, `"decision-test-secret"`, `""`, 1)
	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{"auto_model": json.RawMessage(withoutKey)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Where("key = ?", "auto_model").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.encryption.Decrypt(row.Value)
	if err != nil || !strings.Contains(plaintext, "decision-test-secret") {
		t.Fatal("saving with empty key must preserve the existing same-provider secret")
	}
}

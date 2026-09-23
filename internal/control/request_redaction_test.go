package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestRequestRedactionSettingsPersistEncryptedAndRejectInvalidRules(t *testing.T) {
	f := newServiceFixture(t)
	raw := json.RawMessage(`[{"pattern":"private-value","replacement":"[SECRET]"}]`)
	got, err := f.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{"request_redaction": raw}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(encoded), `"request_redaction":[{"pattern":"private-value"`) {
		t.Fatalf("missing rules: %s %v", encoded, err)
	}
	var stored models.SystemSetting
	if err := f.db.Where("key = ?", "request_redaction").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Value, "private-value") {
		t.Fatal("literal secret persisted in plaintext")
	}
	before := f.manager.Current()
	_, err = f.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{"request_redaction": json.RawMessage(`[{"pattern":"[","replacement":""}]`)}})
	if err == nil || f.manager.Current() != before {
		t.Fatal("invalid expression published configuration")
	}
	if _, err := f.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{"request_redaction": json.RawMessage(`[{"pattern":".*","replacement":""}]`)}}); err != nil {
		t.Fatal("broad rule cannot be saved", err)
	}
	if _, err := f.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{"request_redaction": json.RawMessage(`null`)}}); err != nil {
		t.Fatal(err)
	}
}

func TestRequestRedactionValidationUsesBackendRegexWithoutChangingSettings(t *testing.T) {
	initControlI18n(t)
	f := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, f.service).RegisterRoutes(engine)
	before := f.manager.Current()
	request := httptest.NewRequest(http.MethodPost, "/api/settings/request-redaction/validate", strings.NewReader(`{"rules":[{"pattern":"[","replacement":""},{"pattern":".*","replacement":"[ALL]"}]}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	engine.ServeHTTP(res, request)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"error":`) || !strings.Contains(res.Body.String(), `"warning":"broad_pattern"`) {
		t.Fatalf("validation = %d %s", res.Code, res.Body)
	}
	if f.manager.Current() != before {
		t.Fatal("validation mutated configuration")
	}
}

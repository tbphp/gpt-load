package config

import (
	"strings"
	"testing"

	"gpt-load/internal/types"
)

func TestSanitizeRuntimeSystemSettings_ClearsSystemFailoverStatusCodes(t *testing.T) {
	settings := types.SystemSettings{
		ProxyKeys:           "test-key",
		FailoverStatusCodes: "404,250-260",
	}

	sanitized := sanitizeRuntimeSystemSettings(settings)

	if sanitized.FailoverStatusCodes != "" {
		t.Fatalf("expected system failover status codes to be cleared, got %q", sanitized.FailoverStatusCodes)
	}
	if sanitized.ProxyKeys != settings.ProxyKeys {
		t.Fatalf("expected unrelated fields to remain unchanged")
	}
}

func TestValidateSystemSettingsPayload_RejectsGroupScopedSetting(t *testing.T) {
	err := ValidateSystemSettingsPayload(map[string]any{
		GroupScopedSettingKeyFailoverStatusCodes: "404",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), GroupScopedSettingKeyFailoverStatusCodes) {
		t.Fatalf("expected error to mention rejected key, got %v", err)
	}
}

func TestValidateGroupConfigOverrides_FailoverStatusCodesErrorIncludesContext(t *testing.T) {
	sm := NewSystemSettingsManager()

	err := sm.ValidateGroupConfigOverrides(map[string]any{
		GroupScopedSettingKeyFailoverStatusCodes: "404,abc",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}

	errText := err.Error()
	if !strings.Contains(errText, GroupScopedSettingKeyFailoverStatusCodes) {
		t.Fatalf("expected error to include setting key, got %q", errText)
	}
	if !strings.Contains(errText, "404,abc") {
		t.Fatalf("expected error to include original value, got %q", errText)
	}
}

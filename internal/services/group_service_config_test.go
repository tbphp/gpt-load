package services

import (
	"testing"

	"gpt-load/internal/config"
)

func TestValidateAndCleanConfig_AllowsPriorityAndValidationModes(t *testing.T) {
	service := &GroupService{
		settingsManager: config.NewSystemSettingsManager(),
	}

	configMap := map[string]any{
		"blacklist_threshold":      3.0,
		"key_selection_mode":       "priority",
		"sub_group_selection_mode": "priority",
		"validation_payload_mode":  "responses_messages",
	}

	cleaned, err := service.validateAndCleanConfig(configMap)
	if err != nil {
		t.Fatalf("validateAndCleanConfig returned error: %v", err)
	}

	if got := cleaned["key_selection_mode"]; got != "priority" {
		t.Fatalf("expected key_selection_mode priority, got %#v", got)
	}
	if got := cleaned["sub_group_selection_mode"]; got != "priority" {
		t.Fatalf("expected sub_group_selection_mode priority, got %#v", got)
	}
	if got := cleaned["validation_payload_mode"]; got != "responses_messages" {
		t.Fatalf("expected validation_payload_mode responses_messages, got %#v", got)
	}
}

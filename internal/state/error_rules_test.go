package state

import (
	"reflect"
	"testing"
)

func TestResolveRuntimeSettingsAcceptsCustomErrorRules(t *testing.T) {
	_, err := ResolveRuntimeSettings(map[string]any{
		"error_rules": []any{map[string]any{
			"status_codes": []int{400, 403},
			"keywords":     []string{"余额不足", "欠费"},
			"retry":        "next_candidate",
			"effect":       "record_credential_failure",
		}},
	})
	if err != nil {
		t.Fatalf("custom error rules should be accepted: %v", err)
	}
	if !IsRuntimeSettingKey("error_rules") {
		t.Fatal("custom error rules should be available in system settings")
	}
}

func TestErrorRulesInheritReplaceAndAllowEmptyGroupOverride(t *testing.T) {
	global := []any{map[string]any{"status_codes": []int{403}, "retry": "next_candidate", "effect": "record_credential_failure"}}
	system, err := ResolveRuntimeSettings(map[string]any{"error_rules": global})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := ResolveGroupRuntimeSettings(system, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inherited.ErrorRules.Rules(), system.ErrorRules.Rules()) {
		t.Fatal("group did not inherit global rules")
	}
	override, err := ResolveGroupRuntimeSettings(system, map[string]any{"error_rules": []any{map[string]any{"keywords": []string{"欠费"}, "retry": "none", "effect": "none"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(override.ErrorRules.Rules()) != 1 || override.ErrorRules.Rules()[0].Keywords[0] != "欠费" {
		t.Fatal("group rules did not replace the global list")
	}
	empty, err := ResolveGroupRuntimeSettings(system, map[string]any{"error_rules": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.ErrorRules.Rules()) != 0 || len(system.ErrorRules.Rules()) != 1 {
		t.Fatal("empty override inherited or changed the global rules")
	}
}

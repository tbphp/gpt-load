package state

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
)

func TestCompilePublishesDefaultRuntimeSettingsWithoutGroups(t *testing.T) {
	snapshot, err := Compile(CompileInput{})
	if err != nil {
		t.Fatal(err)
	}
	want := RuntimeSettings{
		ConnectTimeout:          15 * time.Second,
		FirstByteTimeout:        120 * time.Second,
		RequestTimeout:          600 * time.Second,
		StreamIdleTimeout:       300 * time.Second,
		HeaderRules:             HeaderRules{Set: map[string]string{}},
		RequestLogRetentionDays: 7,
	}
	if !reflect.DeepEqual(snapshot.Settings, want) {
		t.Fatalf("Settings = %#v, want %#v", snapshot.Settings, want)
	}
}

func TestCompileRejectsInvalidGlobalSettingsWithoutGroups(t *testing.T) {
	for _, input := range []config.Settings{
		{"request_log_retention_days": json.Number("0")},
		{"request_log_retention_days": json.Number("366")},
		{"request_timeout": json.Number("1.5")},
		{"unknown_public_setting": true},
	} {
		if _, err := Compile(CompileInput{SystemSettings: input}); err == nil {
			t.Fatalf("Compile(%#v) error = nil", input)
		}
	}
}

func TestCompileValidatesDisabledGroupSettings(t *testing.T) {
	_, err := Compile(CompileInput{Groups: []GroupConfig{{
		ID: 1, Protocols: []protocol.Protocol{protocol.OpenAI},
		Settings: config.Settings{"request_log_retention_days": 30},
		Enabled:  false,
	}}})
	if err == nil || !strings.Contains(err.Error(), "unknown group setting") {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestResolveRuntimeSettingsAppliesSystemOverrides(t *testing.T) {
	got, err := ResolveRuntimeSettings(config.Settings{
		SettingConnectTimeout:    json.Number("20"),
		SettingFirstByteTimeout:  json.Number("180"),
		SettingRequestTimeout:    json.Number("900"),
		SettingStreamIdleTimeout: json.Number("45"),
		SettingHeaderRules: map[string]any{
			"set":    map[string]any{"x-test": "value"},
			"remove": []any{"x-old"},
		},
		SettingRequestLogRetentionDays: json.Number("30"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ConnectTimeout != 20*time.Second ||
		got.FirstByteTimeout != 180*time.Second ||
		got.RequestTimeout != 900*time.Second ||
		got.StreamIdleTimeout != 45*time.Second ||
		got.RequestLogRetentionDays != 30 {
		t.Fatalf("settings = %#v", got)
	}
	wantRules := HeaderRules{Set: map[string]string{"X-Test": "value"}, Remove: []string{"X-Old"}}
	if !reflect.DeepEqual(got.HeaderRules, wantRules) {
		t.Fatalf("HeaderRules = %#v, want %#v", got.HeaderRules, wantRules)
	}
}

func TestResolveGroupRuntimeSettingsUsesGroupPrecedence(t *testing.T) {
	system, err := ResolveRuntimeSettings(config.Settings{
		SettingRequestTimeout: json.Number("700"),
		SettingHeaderRules:    map[string]any{"set": map[string]any{"X-System": "system"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	timeouts, rules, err := ResolveGroupRuntimeSettings(system, config.Settings{
		SettingFirstByteTimeout: json.Number("180"),
		SettingHeaderRules:      map[string]any{"set": map[string]any{"X-Group": "group"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if timeouts.Request != 700*time.Second || timeouts.FirstByte != 180*time.Second {
		t.Fatalf("timeouts = %#v", timeouts)
	}
	wantRules := HeaderRules{Set: map[string]string{"X-Group": "group"}}
	if !reflect.DeepEqual(rules, wantRules) {
		t.Fatalf("rules = %#v, want replacement %#v", rules, wantRules)
	}
}

func TestValidateRuntimeSettingBoundaries(t *testing.T) {
	for _, value := range []any{json.Number("1"), json.Number("365")} {
		if err := ValidateRuntimeSetting(SettingRequestLogRetentionDays, value); err != nil {
			t.Fatalf("valid retention %v: %v", value, err)
		}
	}
	for _, value := range []any{json.Number("0"), json.Number("366"), json.Number("1.5"), "7"} {
		if err := ValidateRuntimeSetting(SettingRequestLogRetentionDays, value); err == nil {
			t.Fatalf("invalid retention %v accepted", value)
		}
	}
}

func TestValidateRuntimeSettingAcceptsSupportedWholeNumberTypes(t *testing.T) {
	for _, value := range []any{1, int64(365), float64(30), json.Number("3e1")} {
		if err := ValidateRuntimeSetting(SettingRequestLogRetentionDays, value); err != nil {
			t.Fatalf("valid retention %#v: %v", value, err)
		}
	}
}

func TestIsRuntimeSettingKeyRecognizesOnlyPublicRuntimeKeys(t *testing.T) {
	for _, key := range []string{
		SettingConnectTimeout,
		SettingFirstByteTimeout,
		SettingRequestTimeout,
		SettingStreamIdleTimeout,
		SettingHeaderRules,
		SettingRequestLogRetentionDays,
	} {
		if !IsRuntimeSettingKey(key) {
			t.Errorf("IsRuntimeSettingKey(%q) = false", key)
		}
	}
	for _, key := range []string{"", "retry_count", "_internal.bootstrap"} {
		if IsRuntimeSettingKey(key) {
			t.Errorf("IsRuntimeSettingKey(%q) = true", key)
		}
	}
}

func TestRuntimeSettingsOwnHeaderRuleCopies(t *testing.T) {
	set := map[string]any{"X-Test": "original"}
	remove := []any{"X-Old"}
	got, err := ResolveRuntimeSettings(config.Settings{
		SettingHeaderRules: map[string]any{"set": set, "remove": remove},
	})
	if err != nil {
		t.Fatal(err)
	}
	set["X-Test"] = "mutated"
	remove[0] = "X-Mutated"
	if got.HeaderRules.Set["X-Test"] != "original" || got.HeaderRules.Remove[0] != "X-Old" {
		t.Fatalf("HeaderRules changed with input: %#v", got.HeaderRules)
	}
}

func TestResolveGroupRuntimeSettingsOwnsSystemHeaderRuleCopy(t *testing.T) {
	system, err := ResolveRuntimeSettings(config.Settings{
		SettingHeaderRules: map[string]any{
			"set":    map[string]any{"X-System": "system"},
			"remove": []any{"X-Old"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, rules, err := ResolveGroupRuntimeSettings(system, nil)
	if err != nil {
		t.Fatal(err)
	}
	system.HeaderRules.Set["X-System"] = "mutated"
	system.HeaderRules.Remove[0] = "X-Mutated"
	if rules.Set["X-System"] != "system" || rules.Remove[0] != "X-Old" {
		t.Fatalf("group rules changed with system settings: %#v", rules)
	}
}

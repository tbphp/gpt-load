package state

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
)

func TestCompilePublishesDefaultRuntimeSettingsWithoutGroups(t *testing.T) {
	snapshot, err := Compile(CompileInput{})
	if err != nil {
		t.Fatal(err)
	}
	want := RuntimeSettings{
		ConnectTimeout:           15 * time.Second,
		FirstByteTimeout:         120 * time.Second,
		RequestTimeout:           600 * time.Second,
		StreamIdleTimeout:        300 * time.Second,
		HeaderRules:              HeaderRules{Set: map[string]string{}},
		InjectUsageOptions:       true,
		RequestLogRetentionDays:  7,
		ModelsDevAutoSyncEnabled: true,
	}
	if !reflect.DeepEqual(snapshot.Settings, want) {
		t.Fatalf("Settings = %#v, want %#v", snapshot.Settings, want)
	}
}

func TestModelsDevAutoSyncSettingDefaultsTrueAndIsSystemOnly(t *testing.T) {
	defaults, err := ResolveRuntimeSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !defaults.ModelsDevAutoSyncEnabled {
		t.Fatal("ModelsDevAutoSyncEnabled = false, want default true")
	}

	disabled, err := ResolveRuntimeSettings(config.Settings{
		SettingModelsDevAutoSyncEnabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.ModelsDevAutoSyncEnabled {
		t.Fatal("ModelsDevAutoSyncEnabled = true, want persisted false")
	}
	if !IsRuntimeSettingKey(SettingModelsDevAutoSyncEnabled) {
		t.Fatal("models_dev_auto_sync_enabled is not a public runtime setting")
	}
	if _, err := ResolveGroupRuntimeSettings(
		defaults,
		config.Settings{SettingModelsDevAutoSyncEnabled: false},
	); err == nil {
		t.Fatal("Group override accepted system-only models_dev_auto_sync_enabled")
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
	_, err := Compile(CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{
		ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
		Settings: config.Settings{"request_log_retention_days": 30},
		Enabled:  false,
	}}})
	if err == nil || !strings.Contains(err.Error(), "unknown group setting") {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestResolveRuntimeSettingsRejectsPresentNullHeaderRules(t *testing.T) {
	_, err := ResolveRuntimeSettings(config.Settings{SettingHeaderRules: nil})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() accepted present null header_rules")
	}
}

func TestParseHeaderRulesRejectsUnsafeNames(t *testing.T) {
	names := []string{
		"Connection",
		"proxy-connection",
		"KEEP-ALIVE",
		"te",
		"Trailer",
		"transfer-encoding",
		"Upgrade",
		"cookie",
		"Cookie2",
		"Proxy-Authorization",
		"pRoXy-Custom",
	}
	for _, name := range names {
		for _, section := range []string{"set", "remove"} {
			values := []string{"ordinary"}
			if section == "set" {
				values = append(values, "Bearer ${API_KEY}")
			}
			for _, value := range values {
				setting := map[string]any{}
				if section == "set" {
					setting[section] = map[string]any{name: value}
				} else {
					setting[section] = []any{name}
				}
				if err := ValidateRuntimeSetting(SettingHeaderRules, setting); err == nil {
					t.Errorf("parseHeaderRules accepted unsafe %s %q", section, name)
				}
			}
		}
	}
}

func TestParseHeaderRulesRejectsReservedContentCodingNames(t *testing.T) {
	for _, section := range []string{"set", "remove"} {
		for _, name := range []string{
			"Accept-Encoding",
			"aCcEpT-eNcOdInG",
			"Content-Encoding",
			"cOnTeNt-EnCoDiNg",
		} {
			t.Run(section+"/"+name, func(t *testing.T) {
				value := map[string]any{}
				if section == "set" {
					value[section] = map[string]any{name: "gzip"}
				} else {
					value[section] = []any{name}
				}
				if err := ValidateRuntimeSetting(SettingHeaderRules, value); err == nil {
					t.Fatalf("parseHeaderRules accepted header_rules.%s reserved name %q", section, name)
				}
			})
		}
	}
}

func TestValidateRuntimeSettingRejectsDuplicateHeaderRuleIdentities(t *testing.T) {
	tests := []struct {
		name  string
		value map[string]any
	}{
		{
			name: "duplicate remove names ignore ASCII case",
			value: map[string]any{
				"remove": []any{"X-Trace-ID", "x-trace-id"},
			},
		},
		{
			name: "set and remove names share one identity",
			value: map[string]any{
				"set":    map[string]any{"X-Trace-ID": "trace"},
				"remove": []any{"x-trace-id"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRuntimeSetting(SettingHeaderRules, test.value); err == nil {
				t.Fatal("ValidateRuntimeSetting() accepted duplicate Header Rule identity")
			}
		})
	}
}

func TestParseHeaderRulesRequiresAPIKeyTemplateForCredentials(t *testing.T) {
	for _, name := range []string{
		"Authorization",
		"Api-Key",
		"x-api-key",
		"X-Goog-Api-Key",
	} {
		err := ValidateRuntimeSetting(SettingHeaderRules, map[string]any{
			"set": map[string]any{name: "secret-canary"},
		})
		if err == nil {
			t.Errorf("parseHeaderRules accepted literal credential %q", name)
		}

		err = ValidateRuntimeSetting(SettingHeaderRules, map[string]any{
			"set": map[string]any{name: "Bearer ${API_KEY}"},
		})
		if err != nil {
			t.Errorf("parseHeaderRules rejected template credential %q: %v", name, err)
		}
	}

	err := ValidateRuntimeSetting(SettingHeaderRules, map[string]any{
		"remove": []any{
			"Authorization",
			"Api-Key",
			"X-Api-Key",
			"X-Goog-Api-Key",
		},
	})
	if err != nil {
		t.Errorf("parseHeaderRules rejected credential removals: %v", err)
	}

	err = ValidateRuntimeSetting(SettingHeaderRules, map[string]any{
		"set": map[string]any{
			"Accept":   "application/json",
			"X-Custom": "ordinary",
		},
	})
	if err != nil {
		t.Errorf("parseHeaderRules rejected ordinary values: %v", err)
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
	resolved, err := ResolveGroupRuntimeSettings(system, config.Settings{
		SettingFirstByteTimeout: json.Number("180"),
		SettingHeaderRules:      map[string]any{"set": map[string]any{"X-Group": "group"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Timeouts.Request != 700*time.Second || resolved.Timeouts.FirstByte != 180*time.Second {
		t.Fatalf("timeouts = %#v", resolved.Timeouts)
	}
	wantRules := HeaderRules{Set: map[string]string{"X-Group": "group"}}
	if !reflect.DeepEqual(resolved.HeaderRules, wantRules) {
		t.Fatalf("rules = %#v, want replacement %#v", resolved.HeaderRules, wantRules)
	}
}

func TestResolveGroupRuntimeSettingsRejectsPresentNullHeaderRules(t *testing.T) {
	_, err := ResolveGroupRuntimeSettings(
		DefaultRuntimeSettings(),
		config.Settings{SettingHeaderRules: nil},
	)
	if err == nil {
		t.Fatal("ResolveGroupRuntimeSettings() accepted present null header_rules")
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
		SettingInjectUsageOptions,
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
	resolved, err := ResolveGroupRuntimeSettings(system, nil)
	if err != nil {
		t.Fatal(err)
	}
	system.HeaderRules.Set["X-System"] = "mutated"
	system.HeaderRules.Remove[0] = "X-Mutated"
	if resolved.HeaderRules.Set["X-System"] != "system" || resolved.HeaderRules.Remove[0] != "X-Old" {
		t.Fatalf("group rules changed with system settings: %#v", resolved.HeaderRules)
	}
}

func TestDefaultRuntimeSettingsInjectUsageOptions(t *testing.T) {
	if !DefaultRuntimeSettings().InjectUsageOptions {
		t.Fatal("default inject_usage_options = false, want true")
	}
}

func TestResolveRuntimeSettingsInjectUsageOptionsRequiresBoolean(t *testing.T) {
	for _, value := range []any{true, false} {
		got, err := ResolveRuntimeSettings(config.Settings{SettingInjectUsageOptions: value})
		if err != nil || got.InjectUsageOptions != value {
			t.Fatalf("ResolveRuntimeSettings(%#v) = %#v, %v", value, got, err)
		}
	}
	for _, value := range []any{0, 1, "true", nil, []any{}, map[string]any{}} {
		if _, err := ResolveRuntimeSettings(config.Settings{SettingInjectUsageOptions: value}); err == nil {
			t.Fatalf("ResolveRuntimeSettings(%#v) accepted non-boolean", value)
		}
	}
}

func TestResolveGroupRuntimeSettingsInjectUsagePrecedence(t *testing.T) {
	tests := []struct {
		name   string
		system config.Settings
		group  config.Settings
		want   bool
	}{
		{name: "default", want: true},
		{name: "system false", system: config.Settings{SettingInjectUsageOptions: false}, want: false},
		{name: "group true", system: config.Settings{SettingInjectUsageOptions: false}, group: config.Settings{SettingInjectUsageOptions: true}, want: true},
		{name: "group false", system: config.Settings{SettingInjectUsageOptions: true}, group: config.Settings{SettingInjectUsageOptions: false}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, err := ResolveRuntimeSettings(test.system)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := ResolveGroupRuntimeSettings(base, test.group)
			if err != nil || resolved.InjectUsageOptions != test.want {
				t.Fatalf("ResolveGroupRuntimeSettings() = %#v, %v; want %t", resolved, err, test.want)
			}
		})
	}
	for _, value := range []any{nil, 0, 1, "true", []any{}, map[string]any{}} {
		if _, err := ResolveGroupRuntimeSettings(DefaultRuntimeSettings(), config.Settings{SettingInjectUsageOptions: value}); err == nil {
			t.Fatalf("ResolveGroupRuntimeSettings(%#v) accepted non-boolean", value)
		}
	}
}

func TestResolvedGroupSettingsOwnsHeaderRuleCopies(t *testing.T) {
	base, err := ResolveRuntimeSettings(config.Settings{
		SettingHeaderRules: map[string]any{"set": map[string]any{"X-System": "system"}, "remove": []any{"X-Old"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ResolveGroupRuntimeSettings(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	first.HeaderRules.Set["X-System"] = "changed"
	first.HeaderRules.Remove[0] = "X-Changed"
	second, err := ResolveGroupRuntimeSettings(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if base.HeaderRules.Set["X-System"] != "system" || base.HeaderRules.Remove[0] != "X-Old" ||
		second.HeaderRules.Set["X-System"] != "system" || second.HeaderRules.Remove[0] != "X-Old" {
		t.Fatalf("header rules aliased: base=%#v second=%#v", base.HeaderRules, second.HeaderRules)
	}
}

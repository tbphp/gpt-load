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
		FirstByteTimeout:  120 * time.Second,
		RequestTimeout:    600 * time.Second,
		StreamIdleTimeout: 300 * time.Second,
		HeaderRules:       HeaderRules{Set: map[string]string{}},
		CORS: CORSConfig{
			AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			AllowedHeaders: []string{"*"},
			MaxAgeSeconds:  600,
		},
		ResponseHeaderRules:      HeaderRules{Set: map[string]string{}},
		InjectUsageOptions:       true,
		RetryCount:               2,
		BlacklistThreshold:       3,
		AffinityEnabled:          true,
		AffinityTTL:              time.Hour,
		AffinityCapacity:         10_000,
		ValidationInterval:       10 * time.Minute,
		RequestLogRetentionDays:  7,
		ModelsDevAutoSyncEnabled: true,
	}
	if !reflect.DeepEqual(snapshot.Settings, want) {
		t.Fatalf("Settings = %#v, want %#v", snapshot.Settings, want)
	}
}

func TestCORSAndResponseHeaderRulesAreSystemOnlyRuntimeSettings(t *testing.T) {
	for key, value := range map[string]any{
		SettingCORS: map[string]any{
			"enabled":           true,
			"allowed_origins":   []any{"app://obsidian.md", "https://notes.example"},
			"allowed_methods":   []any{"post", "GET"},
			"allowed_headers":   []any{"authorization", "content-type"},
			"exposed_headers":   []any{"x-request-id"},
			"allow_credentials": true,
			"max_age":           json.Number("900"),
		},
		SettingResponseHeaderRules: map[string]any{
			"set":    map[string]any{"x-browser-client": "enabled"},
			"remove": []any{"x-upstream-marker"},
		},
	} {
		if !IsRuntimeSettingKey(key) {
			t.Errorf("IsRuntimeSettingKey(%q) = false", key)
		}
		if err := ValidateRuntimeSetting(key, value); err != nil {
			t.Errorf("ValidateRuntimeSetting(%q) error = %v", key, err)
		}
	}

	resolved, err := ResolveRuntimeSettings(config.Settings{
		SettingCORS: map[string]any{
			"enabled":           true,
			"allowed_origins":   []any{"app://obsidian.md", "https://notes.example"},
			"allowed_methods":   []any{"post", "GET"},
			"allowed_headers":   []any{"authorization", "content-type"},
			"exposed_headers":   []any{"x-request-id"},
			"allow_credentials": true,
			"max_age":           json.Number("900"),
		},
		SettingResponseHeaderRules: map[string]any{
			"set":    map[string]any{"x-browser-client": "enabled"},
			"remove": []any{"x-upstream-marker"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCORS := CORSConfig{
		Enabled:          true,
		AllowedOrigins:   []string{"app://obsidian.md", "https://notes.example"},
		AllowedMethods:   []string{"POST", "GET"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAgeSeconds:    900,
	}
	if !reflect.DeepEqual(resolved.CORS, wantCORS) {
		t.Fatalf("CORS = %#v, want %#v", resolved.CORS, wantCORS)
	}
	wantRules := HeaderRules{
		Set:    map[string]string{"X-Browser-Client": "enabled"},
		Remove: []string{"X-Upstream-Marker"},
	}
	if !reflect.DeepEqual(resolved.ResponseHeaderRules, wantRules) {
		t.Fatalf("ResponseHeaderRules = %#v, want %#v", resolved.ResponseHeaderRules, wantRules)
	}

	for _, key := range []string{SettingCORS, SettingResponseHeaderRules} {
		if _, err := ResolveGroupRuntimeSettings(
			resolved,
			config.Settings{key: map[string]any{}},
		); err == nil {
			t.Fatalf("ResolveGroupRuntimeSettings accepted system-only %q", key)
		}
	}
}

func TestCORSValidationRejectsUnsafeOrAmbiguousConfiguration(t *testing.T) {
	validBase := map[string]any{
		"enabled":         true,
		"allowed_origins": []any{"https://notes.example"},
	}
	tests := []struct {
		name  string
		value map[string]any
	}{
		{name: "enabled without origins", value: map[string]any{"enabled": true, "allowed_origins": []any{}}},
		{name: "credentialed wildcard", value: map[string]any{"enabled": true, "allowed_origins": []any{"*"}, "allow_credentials": true}},
		{name: "wildcard mixed with origin", value: map[string]any{"enabled": true, "allowed_origins": []any{"*", "https://notes.example"}}},
		{name: "origin with comma", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://one.example, https://two.example"}}},
		{name: "origin with path", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://notes.example/path"}}},
		{name: "origin without scheme", value: map[string]any{"enabled": true, "allowed_origins": []any{"notes.example"}}},
		{name: "duplicate origin", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://notes.example", "https://notes.example"}}},
		{name: "empty methods", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://notes.example"}, "allowed_methods": []any{}}},
		{name: "wildcard method", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://notes.example"}, "allowed_methods": []any{"*"}}},
		{name: "invalid method", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://notes.example"}, "allowed_methods": []any{"POST\nTRACE"}}},
		{name: "invalid allowed header", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://notes.example"}, "allowed_headers": []any{"Bad Header"}}},
		{name: "negative max age", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://notes.example"}, "max_age": json.Number("-1")}},
		{name: "credentialed wildcard exposed headers", value: map[string]any{"enabled": true, "allowed_origins": []any{"https://notes.example"}, "allow_credentials": true, "exposed_headers": []any{"*"}}},
		{name: "unknown field", value: map[string]any{"enabled": false, "surprise": true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRuntimeSetting(SettingCORS, test.value); err == nil {
				t.Fatalf("ValidateRuntimeSetting accepted %#v", test.value)
			}
		})
	}
	if err := ValidateRuntimeSetting(SettingCORS, validBase); err != nil {
		t.Fatalf("ValidateRuntimeSetting rejected valid CORS config: %v", err)
	}
}

func TestCORSOriginsAreCanonicalizedBeforeDuplicateDetection(t *testing.T) {
	origins, err := parseCORSOrigins([]any{
		"HTTPS://EXAMPLE.COM",
		"http://EXAMPLE.COM:80",
		"https://EXAMPLE.COM:444",
		"https://bücher.example",
		"APP://OBSIDIAN.MD",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://example.com",
		"http://example.com",
		"https://example.com:444",
		"https://xn--bcher-kva.example",
		"app://obsidian.md",
	}
	if !reflect.DeepEqual(origins, want) {
		t.Fatalf("origins = %#v, want %#v", origins, want)
	}

	if _, err := parseCORSOrigins([]any{
		"HTTPS://EXAMPLE.COM",
		"https://example.com:443",
	}); err == nil || !strings.Contains(err.Error(), "duplicate origin") {
		t.Fatalf("normalized duplicate error = %v, want duplicate origin", err)
	}
}

func TestResponseHeaderRulesRejectTransportAndCORSOwnedHeaders(t *testing.T) {
	for _, name := range []string{
		"Connection",
		"Content-Length",
		"Content-Type",
		"Set-Cookie",
		"Transfer-Encoding",
		"Vary",
		"Access-Control-Allow-Origin",
		"X-GPTLoad-Attempts",
	} {
		for _, section := range []string{"set", "remove"} {
			value := map[string]any{}
			if section == "set" {
				value[section] = map[string]any{name: "value"}
			} else {
				value[section] = []any{name}
			}
			if err := ValidateRuntimeSetting(SettingResponseHeaderRules, value); err == nil {
				t.Errorf("response_header_rules accepted %s %q", section, name)
			}
		}
	}
}

func TestRetryAndBlacklistCountsArePublicAndResolveByGroupPrecedence(t *testing.T) {
	for key, value := range map[string]any{
		SettingRetryCount:         json.Number("9007199254740991"),
		SettingBlacklistThreshold: json.Number("0"),
	} {
		if !IsRuntimeSettingKey(key) {
			t.Errorf("IsRuntimeSettingKey(%q) = false", key)
		}
		if err := ValidateRuntimeSetting(key, value); err != nil {
			t.Errorf("ValidateRuntimeSetting(%q) error = %v", key, err)
		}
	}

	system, err := ResolveRuntimeSettings(config.Settings{
		SettingRetryCount:         json.Number("0"),
		SettingBlacklistThreshold: json.Number("0"),
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	if system.RetryCount != 0 || system.BlacklistThreshold != 0 {
		t.Fatalf("system policies = %#v", system)
	}
	resolved, err := ResolveGroupRuntimeSettings(system, config.Settings{
		SettingRetryCount:         json.Number("4"),
		SettingBlacklistThreshold: json.Number("5"),
	})
	if err != nil {
		t.Fatalf("ResolveGroupRuntimeSettings() error = %v", err)
	}
	if resolved.RetryCount != 4 || resolved.BlacklistThreshold != 5 {
		t.Fatalf("group policies = %#v", resolved)
	}
}

func TestRetryAndBlacklistCountsRejectNegativeOrNonIntegralValues(t *testing.T) {
	for _, key := range []string{SettingRetryCount, SettingBlacklistThreshold} {
		for _, value := range []any{
			nil,
			json.Number("-1"),
			json.Number("1.5"),
			json.Number("9007199254740992"),
			"3",
		} {
			if err := ValidateRuntimeSetting(key, value); err == nil {
				t.Errorf("ValidateRuntimeSetting(%q, %#v) accepted invalid value", key, value)
			}
		}
	}
}

func TestAffinitySettingsArePublicAndGroupEnableIsOverridable(t *testing.T) {
	for key, value := range map[string]any{
		"affinity_enabled":  false,
		"affinity_ttl":      json.Number("7200"),
		"affinity_capacity": json.Number("20000"),
	} {
		if !IsRuntimeSettingKey(key) {
			t.Errorf("IsRuntimeSettingKey(%q) = false", key)
		}
		if err := ValidateRuntimeSetting(key, value); err != nil {
			t.Errorf("ValidateRuntimeSetting(%q) error = %v", key, err)
		}
	}
	global, err := ResolveRuntimeSettings(config.Settings{
		"affinity_enabled":  false,
		"affinity_ttl":      json.Number("7200"),
		"affinity_capacity": json.Number("20000"),
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	if global.AffinityEnabled || global.AffinityTTL != 2*time.Hour || global.AffinityCapacity != 20_000 {
		t.Fatalf("global affinity settings = %#v", global)
	}
	group, err := ResolveGroupRuntimeSettings(
		global,
		config.Settings{"affinity_enabled": false},
	)
	if err != nil {
		t.Fatalf("ResolveGroupRuntimeSettings() error = %v", err)
	}
	if group.AffinityEnabled {
		t.Fatal("group affinity = true, want explicit false")
	}
	overridden, err := ResolveGroupRuntimeSettings(global, config.Settings{"affinity_enabled": true})
	if err != nil || !overridden.AffinityEnabled {
		t.Fatalf("group affinity override = %#v, %v; want true", overridden, err)
	}
	for _, key := range []string{"affinity_ttl", "affinity_capacity"} {
		if _, err := ResolveGroupRuntimeSettings(global, config.Settings{key: 1}); err == nil {
			t.Fatalf("group accepted system-only %s", key)
		}
	}
}

func TestAffinitySettingsRejectInvalidValues(t *testing.T) {
	for key, values := range map[string][]any{
		SettingAffinityEnabled:  {nil, 0, "true"},
		SettingAffinityTTL:      {json.Number("0"), json.Number("1.5"), "3600"},
		SettingAffinityCapacity: {json.Number("0"), json.Number("1000001"), json.Number("1.5")},
	} {
		for _, value := range values {
			if err := ValidateRuntimeSetting(key, value); err == nil {
				t.Errorf("ValidateRuntimeSetting(%q, %#v) accepted invalid value", key, value)
			}
		}
	}
	for _, value := range []any{nil, 0, "true"} {
		if _, err := ResolveGroupRuntimeSettings(
			DefaultRuntimeSettings(),
			config.Settings{SettingAffinityEnabled: value},
		); err == nil {
			t.Errorf("ResolveGroupRuntimeSettings(%#v) accepted invalid affinity switch", value)
		}
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

func TestValidationIntervalDefaultsToTenMinutesAndIsSystemOnly(t *testing.T) {
	defaults, err := ResolveRuntimeSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.ValidationInterval != 10*time.Minute {
		t.Fatalf("ValidationInterval = %v, want 10m", defaults.ValidationInterval)
	}

	overridden, err := ResolveRuntimeSettings(config.Settings{
		SettingValidationInterval: json.Number("900"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.ValidationInterval != 15*time.Minute {
		t.Fatalf("ValidationInterval = %v, want 15m", overridden.ValidationInterval)
	}
	if !IsRuntimeSettingKey(SettingValidationInterval) {
		t.Fatal("validation_interval is not a public runtime setting")
	}
	if _, err := ResolveGroupRuntimeSettings(
		defaults,
		config.Settings{SettingValidationInterval: json.Number("900")},
	); err == nil {
		t.Fatal("Group override accepted system-only validation_interval")
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

func TestConnectTimeoutIsNotAPublicRuntimeSetting(t *testing.T) {
	if IsRuntimeSettingKey("connect_timeout") {
		t.Fatal("connect_timeout remains a public runtime setting")
	}
	if err := ValidateRuntimeSetting("connect_timeout", json.Number("15")); err == nil {
		t.Fatal("connect_timeout was accepted even though the SDK cannot enforce it")
	}
}

func TestCompileValidatesDisabledGroupSettings(t *testing.T) {
	_, err := Compile(CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
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

func TestParseHeaderRulesRejectsSDKOwnedCredentialHeaders(t *testing.T) {
	for _, name := range []string{
		"Authorization",
		"Api-Key",
		"x-api-key",
		"X-Goog-Api-Key",
	} {
		for _, value := range []string{"secret-canary", "Bearer ${API_KEY}"} {
			err := ValidateRuntimeSetting(SettingHeaderRules, map[string]any{
				"set": map[string]any{name: value},
			})
			if err == nil {
				t.Errorf("parseHeaderRules accepted SDK-owned credential header %q", name)
			}
		}
		if err := ValidateRuntimeSetting(SettingHeaderRules, map[string]any{
			"remove": []any{name},
		}); err == nil {
			t.Errorf("parseHeaderRules accepted removal of SDK-owned credential header %q", name)
		}
	}

	err := ValidateRuntimeSetting(SettingHeaderRules, map[string]any{
		"set": map[string]any{
			"Accept":        "application/json",
			"X-Custom":      "ordinary",
			"X-Custom-Auth": "Token ${API_KEY}",
		},
	})
	if err != nil {
		t.Errorf("parseHeaderRules rejected ordinary values: %v", err)
	}
}

func TestResolveRuntimeSettingsAppliesSystemOverrides(t *testing.T) {
	got, err := ResolveRuntimeSettings(config.Settings{
		SettingFirstByteTimeout:   json.Number("180"),
		SettingRequestTimeout:     json.Number("900"),
		SettingStreamIdleTimeout:  json.Number("45"),
		SettingValidationInterval: json.Number("900"),
		SettingHeaderRules: map[string]any{
			"set":    map[string]any{"x-test": "value"},
			"remove": []any{"x-old"},
		},
		SettingRequestLogRetentionDays: json.Number("30"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.FirstByteTimeout != 180*time.Second ||
		got.RequestTimeout != 900*time.Second ||
		got.StreamIdleTimeout != 45*time.Second ||
		got.ValidationInterval != 15*time.Minute ||
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
		SettingFirstByteTimeout,
		SettingRequestTimeout,
		SettingStreamIdleTimeout,
		SettingHeaderRules,
		SettingCORS,
		SettingResponseHeaderRules,
		SettingInjectUsageOptions,
		SettingRetryCount,
		SettingBlacklistThreshold,
		SettingAffinityEnabled,
		SettingAffinityTTL,
		SettingAffinityCapacity,
		SettingValidationInterval,
		SettingRequestLogRetentionDays,
	} {
		if !IsRuntimeSettingKey(key) {
			t.Errorf("IsRuntimeSettingKey(%q) = false", key)
		}
	}
	for _, key := range []string{"", "retry_enabled", "blacklist_enabled", "_internal.bootstrap"} {
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

package state

import (
	"slices"
	"testing"

	"gpt-load/internal/platform/config"
)

func TestConfiguredHeaderNamesExcludeDefaults(t *testing.T) {
	rules := HeaderRules{Default: map[string]string{"User-Agent": "fallback"}}
	if got := rules.ConfiguredNames(); got != nil {
		t.Fatalf("default-only rules returned configured names: %v", got)
	}
	rules.Set = map[string]string{"X-Set": "value"}
	rules.Remove = []string{"X-Remove"}
	if got := rules.ConfiguredNames(); !slices.Equal(got, []string{"X-Set", "X-Remove"}) {
		t.Fatalf("configured names = %v; want only explicitly set and removed headers", got)
	}
}

func TestHeaderDefaultsValidationAndInheritance(t *testing.T) {
	settings, err := ResolveRuntimeSettings(config.Settings{SettingHeaderRules: map[string]any{
		"default": map[string]any{"user-agent": "Go-http-client/1.1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if settings.HeaderRules.Default["User-Agent"] != "Go-http-client/1.1" {
		t.Fatal(settings.HeaderRules)
	}
	inherited, err := ResolveGroupRuntimeSettings(settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	inherited.HeaderRules.Default["User-Agent"] = "changed"
	if settings.HeaderRules.Default["User-Agent"] != "Go-http-client/1.1" {
		t.Fatal("shared default map")
	}
	overridden, err := ResolveGroupRuntimeSettings(settings, config.Settings{SettingHeaderRules: map[string]any{"set": map[string]any{"X-Group": "own"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(overridden.HeaderRules.Default) != 0 {
		t.Fatal("group override merged system defaults")
	}
	for _, rules := range []map[string]any{
		{"default": "invalid"},
		{"default": map[string]any{"Authorization": "secret"}},
		{"default": map[string]any{"Connection": "close"}},
		{"default": map[string]any{"X-Test": "bad\r\nvalue"}},
		{"default": map[string]any{"X-Test": 1}},
		{"default": map[string]any{"X-Test": "one", "x-test": "two"}},
		{"default": map[string]any{"X-Test": "one"}, "set": map[string]any{"x-test": "two"}},
		{"default": map[string]any{"X-Test": "one"}, "remove": []any{"x-test"}},
	} {
		if err := ValidateRuntimeSetting(SettingHeaderRules, rules); err == nil {
			t.Fatalf("accepted invalid rules: %#v", rules)
		}
	}
	if err := ValidateRuntimeSetting(SettingResponseHeaderRules, map[string]any{"default": map[string]any{"X-Test": "one"}}); err == nil {
		t.Fatal("response defaults are out of scope")
	}
}

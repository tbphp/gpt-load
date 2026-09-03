package state

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestResolveGroupRuntimeSettingsCompilesParameterOverrides(t *testing.T) {
	resolved, err := ResolveGroupRuntimeSettings(DefaultRuntimeSettings(), map[string]any{
		SettingParameterOverrides: []any{
			map[string]any{
				"match": map[string]any{"model": "public-*"},
				"set":   map[string]any{"temperature": json.Number("0.25")},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, applied, err := resolved.ParameterOverrides.Apply(
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		"public-model",
		[]byte(`{"model":"public-model"}`),
	)
	if err != nil || !applied || string(body) != `{"model":"public-model","temperature":0.25}` {
		t.Fatalf("Apply() = %s, %t, %v", body, applied, err)
	}
}

func TestResolveRuntimeSettingsRejectsGroupOnlyParameterOverrides(t *testing.T) {
	_, err := ResolveRuntimeSettings(map[string]any{SettingParameterOverrides: []any{}})
	if err == nil {
		t.Fatal("ResolveRuntimeSettings() accepted group-only parameter overrides")
	}
	if IsRuntimeSettingKey(SettingParameterOverrides) {
		t.Fatal("IsRuntimeSettingKey() exposed group-only parameter overrides")
	}
}

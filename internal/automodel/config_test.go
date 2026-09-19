package automodel

import (
	"encoding/json"
	"testing"
)

func TestConfigUsesSingleEnableSwitchAndRejectsMissingTargets(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.APIKey = "secret"
	entry := Template()
	entry.Enabled = false
	config.Models = []Entry{entry}
	names := map[string]struct{}{"gpt-5.6-luna": {}, "gpt-5.6-terra": {}, "gpt-5.6-sol": {}, "gpt-6-astra": {}}
	compiled, err := Compile(config, names)
	if err != nil {
		t.Fatal(err)
	}
	if selected, exists := compiled.Lookup("auto"); !exists || !selected.Enabled {
		t.Fatal("automatic entry did not follow the system enable switch")
	}
	names["auto"] = struct{}{}
	if _, err := Compile(config, names); err == nil {
		t.Fatal("ordinary auto model was hijacked")
	}
	delete(names, "auto")
	delete(names, "gpt-5.6-sol")
	if _, err := Compile(config, names); err == nil {
		t.Fatal("missing enabled target accepted")
	}
	config.Enabled = false
	if _, err := Compile(config, names); err != nil {
		t.Fatalf("disabled draft cannot be edited: %v", err)
	}
}

func TestConfigRejectsProtectedContinuationOverrides(t *testing.T) {
	config := DefaultConfig()
	config.Models = []Entry{Template()}
	config.Models[0].Presets[0].ParameterOverrides = json.RawMessage(`[{"set":{"previous_response_id":"injected"}}]`)
	if _, err := Compile(config, nil); err == nil {
		t.Fatal("preset can rewrite continuation identity")
	}
}

package catalog

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestCodexModelCatalogSnapshotDigest(t *testing.T) {
	if digest := fmt.Sprintf("%x", sha256.Sum256(codexClientModelsJSON)); digest != codexModelCatalogSHA256 {
		t.Fatalf("Codex model catalog digest = %q, want %q", digest, codexModelCatalogSHA256)
	}
}

func TestCodexClientModelUsesGPT6Templates(t *testing.T) {
	for _, test := range []struct {
		id     string
		name   string
		levels []string
	}{
		{"gpt-6-sol", "GPT-6-Sol", []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
		{"gpt-6-luna", "GPT-6-Luna", []string{"low", "medium", "high", "xhigh", "max"}},
	} {
		t.Run(test.id, func(t *testing.T) {
			model, automatic, _, err := BuildCodexClientModel(test.id, 0, ClientModelOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if automatic.DisplayName != test.name ||
				!reflect.DeepEqual(automatic.SupportedReasoningLevels, test.levels) ||
				model["minimal_client_version"] != "0.155.0" ||
				model["tool_mode"] != "code_mode_only" || model["use_responses_lite"] != true {
				t.Fatalf("model %q did not use its native template: profile=%#v, minimum=%v, tool_mode=%v, responses_lite=%v",
					test.id, automatic, model["minimal_client_version"], model["tool_mode"], model["use_responses_lite"])
			}
		})
	}
}

func TestCodexClientModelUsesExactTemplateAndFourOverrides(t *testing.T) {
	contextWindow := int64(64000)
	reasoning := []string{"low", "high"}
	modalities := []string{"text"}
	overrides := ClientModelOverrides{
		DisplayName:              ptr("Custom Sol"),
		ContextWindow:            &contextWindow,
		SupportedReasoningLevels: &reasoning,
		InputModalities:          &modalities,
	}
	model, automatic, effective, err := BuildCodexClientModel("gpt-5.6-sol", 7, overrides)
	if err != nil {
		t.Fatal(err)
	}
	if automatic.DisplayName != "GPT-5.6-Sol" || automatic.ContextWindow == nil || *automatic.ContextWindow != 272000 ||
		!reflect.DeepEqual(automatic.SupportedReasoningLevels, []string{"low", "medium", "high", "xhigh", "max", "ultra"}) ||
		!reflect.DeepEqual(automatic.InputModalities, []string{"text", "image"}) {
		t.Fatalf("automatic profile = %#v", automatic)
	}
	if effective.DisplayName != "Custom Sol" || effective.ContextWindow == nil || *effective.ContextWindow != contextWindow ||
		!reflect.DeepEqual(effective.SupportedReasoningLevels, reasoning) || !reflect.DeepEqual(effective.InputModalities, modalities) {
		t.Fatalf("effective profile = %#v", effective)
	}
	if model["slug"] != "gpt-5.6-sol" || model["display_name"] != "Custom Sol" || model["priority"] != 7 ||
		model["context_window"] != int64(64000) || model["max_context_window"] != int64(64000) ||
		model["tool_mode"] != "code_mode_only" || model["use_responses_lite"] != true {
		t.Fatalf("resolved model = %#v", model)
	}
	levels := model["supported_reasoning_levels"].([]any)
	if len(levels) != 2 || levels[0].(map[string]any)["effort"] != "low" || levels[1].(map[string]any)["effort"] != "high" {
		t.Fatalf("reasoning levels = %#v", levels)
	}
	assertCodexInstructionFields(t, model)
}

func TestCodexClientModelFallsBackToGPT55(t *testing.T) {
	model, automatic, effective, err := BuildCodexClientModel("vendor/new-model", 3, ClientModelOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if automatic.DisplayName != "vendor/new-model" || effective.DisplayName != "vendor/new-model" ||
		automatic.ContextWindow == nil || *automatic.ContextWindow != 272000 {
		t.Fatalf("fallback profile = %#v / %#v", automatic, effective)
	}
	if model["slug"] != "vendor/new-model" || model["display_name"] != "vendor/new-model" ||
		model["description"] != "vendor/new-model" || model["use_responses_lite"] != false || model["tool_mode"] != nil {
		t.Fatalf("fallback model = %#v", model)
	}
	assertCodexInstructionFields(t, model)
}

func TestCodexClientModelMakesConfiguredHiddenTemplateSelectable(t *testing.T) {
	model, _, _, err := BuildCodexClientModel("gpt-reserve", 0, ClientModelOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if model["visibility"] != "list" {
		t.Fatalf("visibility = %#v, want list", model["visibility"])
	}
}

func TestClientModelOverridesValidation(t *testing.T) {
	for _, raw := range []string{
		`{"context_window":0}`, `{"context_window":9007199254740992}`,
		`{"display_name":" "}`, `{"supported_reasoning_levels":[]}`,
		`{"supported_reasoning_levels":["future"]}`, `{"supported_reasoning_levels":["high","high"]}`,
		`{"input_modalities":[]}`, `{"input_modalities":["text","video"]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var overrides ClientModelOverrides
			if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
				t.Fatal(err)
			}
			if err := overrides.Validate(); err == nil {
				t.Fatal("invalid overrides accepted")
			}
		})
	}
	var overrides ClientModelOverrides
	if err := json.Unmarshal([]byte(`{"display_name":null,"supported_reasoning_levels":null}`), &overrides); err != nil {
		t.Fatal(err)
	}
	if !overrides.IsEmpty() || overrides.Validate() != nil {
		t.Fatalf("null must restore automatic values: %#v", overrides)
	}
}

func assertCodexInstructionFields(t *testing.T, model map[string]any) {
	t.Helper()
	base, ok := model["base_instructions"].(string)
	if !ok || base == "" {
		t.Fatal("base_instructions is missing")
	}
	messages, ok := model["model_messages"].(map[string]any)
	if !ok {
		t.Fatal("model_messages is missing")
	}
	template, ok := messages["instructions_template"].(string)
	if !ok || template == "" {
		t.Fatal("instructions_template is missing")
	}
	variables, _ := messages["instructions_variables"].(map[string]any)
	defaultPersonality, _ := variables["personality_default"].(string)
	if base != replacePersonality(template, defaultPersonality) {
		t.Fatal("base_instructions is not the default rendered instructions_template")
	}
}

func ptr(value string) *string { return &value }

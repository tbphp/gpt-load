package catalog

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestClientModelProfileIntersectionAndOverrides(t *testing.T) {
	large, small := int64(200000), int64(64000)
	profiles := []ClientModelProfile{
		{ContextWindow: &large, SupportedReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high",
			InputModalities: []string{"text", "image"}, SupportsReasoningSummary: true, SupportVerbosity: true},
		{ContextWindow: &small, SupportedReasoningLevels: []string{"high"}, DefaultReasoningLevel: "high",
			InputModalities: []string{"text"}, SupportsReasoningSummary: true},
	}
	automatic := IntersectClientModelProfiles("client", profiles)
	if automatic.DisplayName != "client" || *automatic.ContextWindow != small || automatic.DefaultReasoningLevel != "high" ||
		!reflect.DeepEqual(automatic.SupportedReasoningLevels, []string{"high"}) ||
		!reflect.DeepEqual(automatic.InputModalities, []string{"text"}) || !automatic.SupportsReasoningSummary || automatic.SupportVerbosity {
		t.Fatalf("intersection = %#v", automatic)
	}
	var overrides ClientModelOverrides
	if err := json.Unmarshal([]byte(`{"context_window":32000,"supported_reasoning_levels":[],"supports_reasoning_summary":false}`), &overrides); err != nil {
		t.Fatal(err)
	}
	if err := overrides.Validate(); err != nil {
		t.Fatal(err)
	}
	effective := automatic.Apply(overrides)
	if *effective.ContextWindow != 32000 || len(effective.SupportedReasoningLevels) != 0 || effective.DefaultReasoningLevel != "" || effective.SupportsReasoningSummary {
		t.Fatalf("explicit zero values lost: %#v", effective)
	}
	if *automatic.ContextWindow != small || *profiles[0].ContextWindow != large || len(automatic.SupportedReasoningLevels) != 1 {
		t.Fatal("profile calculation mutated source")
	}
	unknown := IntersectClientModelProfiles("client", append(profiles, DefaultClientModelProfile("unknown")))
	if unknown.ContextWindow != nil || len(unknown.SupportedReasoningLevels) != 0 || unknown.SupportsReasoningSummary {
		t.Fatalf("unknown source was omitted: %#v", unknown)
	}
}

func TestClientModelOverridesValidation(t *testing.T) {
	for _, raw := range []string{
		`{"context_window":0}`, `{"context_window":9007199254740992}`,
		`{"display_name":" "}`, `{"supported_reasoning_levels":["future"]}`,
		`{"supported_reasoning_levels":["high","high"]}`, `{"default_reasoning_level":"high"}`,
		`{"supported_reasoning_levels":["low"],"default_reasoning_level":"high"}`,
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

func TestClientModelCatalogProfilesResolveAndFreezeGeneration(t *testing.T) {
	contextLimit := int64(64000)
	runtime := &Runtime{}
	runtime.Publish(&Snapshot{Providers: map[string]Provider{
		"openai": {Models: map[string]Model{"shared": {Metadata: ModelMetadata{Limits: ModelLimits{Context: &contextLimit}, Modalities: ModelModalities{Input: []string{"text", "image", "video"}}}}}},
		"other":  {Models: map[string]Model{"shared": {Metadata: ModelMetadata{Modalities: ModelModalities{Input: []string{"text"}}}}}},
	}})
	generation := runtime.ClientProfiles()
	profile, found := generation.Resolve("other", "shared")
	if !found || profile.ContextWindow != nil || !reflect.DeepEqual(profile.InputModalities, []string{"text"}) {
		t.Fatalf("exact provider = %#v, %v", profile, found)
	}
	profile, found = generation.Resolve("unknown", "shared")
	if !found || profile.ContextWindow == nil || *profile.ContextWindow != contextLimit ||
		!reflect.DeepEqual(profile.InputModalities, []string{"text", "image"}) {
		t.Fatalf("priority fallback = %#v, %v", profile, found)
	}
	*profile.ContextWindow = 1
	profile.InputModalities[0] = "mutated"
	runtime.Publish(nil)
	profile, found = generation.Resolve("openai", "shared")
	if !found || *profile.ContextWindow != contextLimit || profile.InputModalities[0] != "text" {
		t.Fatal("captured generation was mutated")
	}
	if _, found = runtime.ClientProfiles().Resolve("openai", "shared"); found {
		t.Fatal("cleared runtime still exposes a profile")
	}
}

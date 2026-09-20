package catalog

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

type ClientModelProfile struct {
	DisplayName              string   `json:"display_name"`
	Description              string   `json:"description"`
	ContextWindow            *int64   `json:"context_window"`
	SupportedReasoningLevels []string `json:"supported_reasoning_levels"`
	DefaultReasoningLevel    string   `json:"default_reasoning_level"`
	InputModalities          []string `json:"input_modalities"`
	SupportsReasoningSummary bool     `json:"supports_reasoning_summary"`
	SupportVerbosity         bool     `json:"support_verbosity"`
}

type ClientModelOverrides struct {
	DisplayName              *string   `json:"display_name,omitempty"`
	Description              *string   `json:"description,omitempty"`
	ContextWindow            *int64    `json:"context_window,omitempty"`
	SupportedReasoningLevels *[]string `json:"supported_reasoning_levels,omitempty"`
	DefaultReasoningLevel    *string   `json:"default_reasoning_level,omitempty"`
	InputModalities          *[]string `json:"input_modalities,omitempty"`
	SupportsReasoningSummary *bool     `json:"supports_reasoning_summary,omitempty"`
	SupportVerbosity         *bool     `json:"support_verbosity,omitempty"`
}

func (overrides ClientModelOverrides) Validate() error {
	if overrides.DisplayName != nil && (!utf8.ValidString(*overrides.DisplayName) ||
		strings.TrimSpace(*overrides.DisplayName) == "" || len(*overrides.DisplayName) > 512) {
		return fmt.Errorf("display_name must be nonblank UTF-8 text up to 512 bytes")
	}
	if overrides.Description != nil && (!utf8.ValidString(*overrides.Description) || len(*overrides.Description) > 4096) {
		return fmt.Errorf("description must be UTF-8 text up to 4096 bytes")
	}
	if overrides.ContextWindow != nil && (*overrides.ContextWindow < 1 || *overrides.ContextWindow > 9_007_199_254_740_991) {
		return fmt.Errorf("context_window must be a positive safe integer")
	}
	if overrides.SupportedReasoningLevels != nil {
		if err := validateProfileChoices(*overrides.SupportedReasoningLevels,
			[]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"}); err != nil {
			return fmt.Errorf("supported_reasoning_levels: %w", err)
		}
	}
	if overrides.DefaultReasoningLevel != nil && *overrides.DefaultReasoningLevel != "" {
		if overrides.SupportedReasoningLevels == nil || !slices.Contains(*overrides.SupportedReasoningLevels, *overrides.DefaultReasoningLevel) {
			return fmt.Errorf("default_reasoning_level must be in supported_reasoning_levels")
		}
	}
	if overrides.InputModalities != nil {
		if err := validateProfileChoices(*overrides.InputModalities, []string{"text", "image", "audio"}); err != nil {
			return fmt.Errorf("input_modalities: %w", err)
		}
		if !slices.Contains(*overrides.InputModalities, "text") {
			return fmt.Errorf("input_modalities must contain text")
		}
	}
	return nil
}

func validateProfileChoices(values, allowed []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return fmt.Errorf("unsupported value %q", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("duplicate value %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (overrides ClientModelOverrides) IsEmpty() bool {
	return overrides.DisplayName == nil && overrides.Description == nil && overrides.ContextWindow == nil &&
		overrides.SupportedReasoningLevels == nil && overrides.DefaultReasoningLevel == nil &&
		overrides.InputModalities == nil && overrides.SupportsReasoningSummary == nil && overrides.SupportVerbosity == nil
}

func (overrides ClientModelOverrides) Clone() ClientModelOverrides {
	overrides.DisplayName = cloneProfileValue(overrides.DisplayName)
	overrides.Description = cloneProfileValue(overrides.Description)
	overrides.ContextWindow = cloneProfileValue(overrides.ContextWindow)
	overrides.DefaultReasoningLevel = cloneProfileValue(overrides.DefaultReasoningLevel)
	overrides.SupportsReasoningSummary = cloneProfileValue(overrides.SupportsReasoningSummary)
	overrides.SupportVerbosity = cloneProfileValue(overrides.SupportVerbosity)
	if overrides.SupportedReasoningLevels != nil {
		values := append([]string{}, (*overrides.SupportedReasoningLevels)...)
		overrides.SupportedReasoningLevels = &values
	}
	if overrides.InputModalities != nil {
		values := append([]string{}, (*overrides.InputModalities)...)
		overrides.InputModalities = &values
	}
	return overrides
}

func cloneProfileValue[Value any](value *Value) *Value {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func DefaultClientModelProfile(model string) ClientModelProfile {
	return ClientModelProfile{
		DisplayName: model, SupportedReasoningLevels: []string{}, InputModalities: []string{"text"},
	}
}

func (profile ClientModelProfile) Clone() ClientModelProfile {
	profile.ContextWindow = cloneProfileValue(profile.ContextWindow)
	profile.SupportedReasoningLevels = append([]string{}, profile.SupportedReasoningLevels...)
	profile.InputModalities = append([]string{}, profile.InputModalities...)
	return profile
}

func (profile ClientModelProfile) Apply(overrides ClientModelOverrides) ClientModelProfile {
	result := profile.Clone()
	if overrides.DisplayName != nil {
		result.DisplayName = *overrides.DisplayName
	}
	if overrides.Description != nil {
		result.Description = *overrides.Description
	}
	if overrides.ContextWindow != nil {
		result.ContextWindow = cloneProfileValue(overrides.ContextWindow)
	}
	if overrides.SupportedReasoningLevels != nil {
		result.SupportedReasoningLevels = append([]string{}, (*overrides.SupportedReasoningLevels)...)
	}
	if overrides.DefaultReasoningLevel != nil {
		result.DefaultReasoningLevel = *overrides.DefaultReasoningLevel
	}
	if !slices.Contains(result.SupportedReasoningLevels, result.DefaultReasoningLevel) {
		result.DefaultReasoningLevel = ""
	}
	if overrides.InputModalities != nil {
		result.InputModalities = append([]string{}, (*overrides.InputModalities)...)
	}
	if overrides.SupportsReasoningSummary != nil {
		result.SupportsReasoningSummary = *overrides.SupportsReasoningSummary
	}
	if overrides.SupportVerbosity != nil {
		result.SupportVerbosity = *overrides.SupportVerbosity
	}
	return result
}

func IntersectClientModelProfiles(model string, profiles []ClientModelProfile) ClientModelProfile {
	if len(profiles) == 0 {
		return DefaultClientModelProfile(model)
	}
	result := profiles[0].Clone()
	result.DisplayName = model
	for _, profile := range profiles[1:] {
		if result.Description != profile.Description {
			result.Description = ""
		}
		if result.ContextWindow == nil || profile.ContextWindow == nil {
			result.ContextWindow = nil
		} else if *profile.ContextWindow < *result.ContextWindow {
			result.ContextWindow = cloneProfileValue(profile.ContextWindow)
		}
		result.SupportedReasoningLevels = intersectProfileChoices(result.SupportedReasoningLevels, profile.SupportedReasoningLevels)
		result.InputModalities = intersectProfileChoices(result.InputModalities, profile.InputModalities)
		if result.DefaultReasoningLevel != profile.DefaultReasoningLevel {
			result.DefaultReasoningLevel = ""
		}
		result.SupportsReasoningSummary = result.SupportsReasoningSummary && profile.SupportsReasoningSummary
		result.SupportVerbosity = result.SupportVerbosity && profile.SupportVerbosity
	}
	if !slices.Contains(result.SupportedReasoningLevels, result.DefaultReasoningLevel) {
		result.DefaultReasoningLevel = ""
	}
	return result
}

func intersectProfileChoices(left, right []string) []string {
	result := make([]string, 0, len(left))
	for _, value := range left {
		if slices.Contains(right, value) {
			result = append(result, value)
		}
	}
	return result
}

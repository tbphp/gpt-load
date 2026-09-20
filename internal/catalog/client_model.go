package catalog

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

type ClientModelProfile struct {
	DisplayName              string   `json:"display_name"`
	ContextWindow            *int64   `json:"context_window"`
	SupportedReasoningLevels []string `json:"supported_reasoning_levels"`
	InputModalities          []string `json:"input_modalities"`
}

type ClientModelOverrides struct {
	DisplayName              *string   `json:"display_name,omitempty"`
	ContextWindow            *int64    `json:"context_window,omitempty"`
	SupportedReasoningLevels *[]string `json:"supported_reasoning_levels,omitempty"`
	InputModalities          *[]string `json:"input_modalities,omitempty"`
}

func (overrides ClientModelOverrides) Validate() error {
	if overrides.DisplayName != nil && (!utf8.ValidString(*overrides.DisplayName) ||
		strings.TrimSpace(*overrides.DisplayName) == "" || len(*overrides.DisplayName) > 512) {
		return fmt.Errorf("display_name must be nonblank UTF-8 text up to 512 bytes")
	}
	if overrides.ContextWindow != nil && (*overrides.ContextWindow < 1 || *overrides.ContextWindow > 9_007_199_254_740_991) {
		return fmt.Errorf("context_window must be a positive safe integer")
	}
	if overrides.SupportedReasoningLevels != nil {
		if len(*overrides.SupportedReasoningLevels) == 0 {
			return fmt.Errorf("supported_reasoning_levels must not be empty")
		}
		if err := validateProfileChoices(*overrides.SupportedReasoningLevels,
			[]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"}); err != nil {
			return fmt.Errorf("supported_reasoning_levels: %w", err)
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
	return overrides.DisplayName == nil && overrides.ContextWindow == nil &&
		overrides.SupportedReasoningLevels == nil && overrides.InputModalities == nil
}

func (overrides ClientModelOverrides) Clone() ClientModelOverrides {
	overrides.DisplayName = cloneProfileValue(overrides.DisplayName)
	overrides.ContextWindow = cloneProfileValue(overrides.ContextWindow)
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
	if overrides.ContextWindow != nil {
		result.ContextWindow = cloneProfileValue(overrides.ContextWindow)
	}
	if overrides.SupportedReasoningLevels != nil {
		result.SupportedReasoningLevels = append([]string{}, (*overrides.SupportedReasoningLevels)...)
	}
	if overrides.InputModalities != nil {
		result.InputModalities = append([]string{}, (*overrides.InputModalities)...)
	}
	return result
}

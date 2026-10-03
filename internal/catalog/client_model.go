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
	AutoCompactTokenLimit    *int64   `json:"auto_compact_token_limit"`
	SupportedReasoningLevels []string `json:"supported_reasoning_levels"`
	DefaultReasoningLevel    string   `json:"default_reasoning_level"`
	InputModalities          []string `json:"input_modalities"`
	ServiceTiers             []string `json:"service_tiers"`
}

type ClientModelOverrides struct {
	CatalogEnabled           *bool     `json:"catalog_enabled,omitempty"`
	CatalogOrder             *int64    `json:"catalog_order,omitempty"`
	DisplayName              *string   `json:"display_name,omitempty"`
	Description              *string   `json:"description,omitempty"`
	ContextWindow            *int64    `json:"context_window,omitempty"`
	AutoCompactTokenLimit    *int64    `json:"auto_compact_token_limit,omitempty"`
	SupportedReasoningLevels *[]string `json:"supported_reasoning_levels,omitempty"`
	DefaultReasoningLevel    *string   `json:"default_reasoning_level,omitempty"`
	InputModalities          *[]string `json:"input_modalities,omitempty"`
	ServiceTiers             *[]string `json:"service_tiers,omitempty"`
}

func (overrides ClientModelOverrides) Validate() error {
	if overrides.CatalogOrder != nil && (*overrides.CatalogOrder < 0 || *overrides.CatalogOrder > 9_007_199_254_740_991) {
		return fmt.Errorf("catalog_order must be a nonnegative safe integer")
	}
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
	if overrides.AutoCompactTokenLimit != nil && (*overrides.AutoCompactTokenLimit < 1 || *overrides.AutoCompactTokenLimit > 9_007_199_254_740_991) {
		return fmt.Errorf("auto_compact_token_limit must be a positive safe integer")
	}
	reasoningLevels := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"}
	if overrides.SupportedReasoningLevels != nil {
		if len(*overrides.SupportedReasoningLevels) == 0 {
			return fmt.Errorf("supported_reasoning_levels must not be empty")
		}
		if err := validateProfileChoices(*overrides.SupportedReasoningLevels, reasoningLevels); err != nil {
			return fmt.Errorf("supported_reasoning_levels: %w", err)
		}
	}
	if overrides.DefaultReasoningLevel != nil {
		if !slices.Contains(reasoningLevels, *overrides.DefaultReasoningLevel) {
			return fmt.Errorf("default_reasoning_level is invalid")
		}
		if overrides.SupportedReasoningLevels != nil &&
			!slices.Contains(*overrides.SupportedReasoningLevels, *overrides.DefaultReasoningLevel) {
			return fmt.Errorf("default_reasoning_level must be included in supported_reasoning_levels")
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
	if overrides.ServiceTiers != nil {
		if err := validateProfileChoices(*overrides.ServiceTiers, []string{"priority", "ultrafast"}); err != nil {
			return fmt.Errorf("service_tiers: %w", err)
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
	return overrides.CatalogEnabled == nil && overrides.CatalogOrder == nil &&
		overrides.DisplayName == nil && overrides.Description == nil &&
		overrides.ContextWindow == nil && overrides.AutoCompactTokenLimit == nil &&
		overrides.SupportedReasoningLevels == nil && overrides.DefaultReasoningLevel == nil &&
		overrides.InputModalities == nil && overrides.ServiceTiers == nil
}

func (overrides ClientModelOverrides) Clone() ClientModelOverrides {
	overrides.CatalogEnabled = cloneProfileValue(overrides.CatalogEnabled)
	overrides.CatalogOrder = cloneProfileValue(overrides.CatalogOrder)
	overrides.DisplayName = cloneProfileValue(overrides.DisplayName)
	overrides.Description = cloneProfileValue(overrides.Description)
	overrides.ContextWindow = cloneProfileValue(overrides.ContextWindow)
	overrides.AutoCompactTokenLimit = cloneProfileValue(overrides.AutoCompactTokenLimit)
	overrides.DefaultReasoningLevel = cloneProfileValue(overrides.DefaultReasoningLevel)
	if overrides.SupportedReasoningLevels != nil {
		values := append([]string{}, (*overrides.SupportedReasoningLevels)...)
		overrides.SupportedReasoningLevels = &values
	}
	if overrides.InputModalities != nil {
		values := append([]string{}, (*overrides.InputModalities)...)
		overrides.InputModalities = &values
	}
	if overrides.ServiceTiers != nil {
		values := append([]string{}, (*overrides.ServiceTiers)...)
		overrides.ServiceTiers = &values
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
	profile.AutoCompactTokenLimit = cloneProfileValue(profile.AutoCompactTokenLimit)
	profile.SupportedReasoningLevels = append([]string{}, profile.SupportedReasoningLevels...)
	profile.InputModalities = append([]string{}, profile.InputModalities...)
	profile.ServiceTiers = append([]string{}, profile.ServiceTiers...)
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
	if overrides.AutoCompactTokenLimit != nil {
		result.AutoCompactTokenLimit = cloneProfileValue(overrides.AutoCompactTokenLimit)
	}
	if overrides.SupportedReasoningLevels != nil {
		result.SupportedReasoningLevels = append([]string{}, (*overrides.SupportedReasoningLevels)...)
	}
	if overrides.DefaultReasoningLevel != nil {
		result.DefaultReasoningLevel = *overrides.DefaultReasoningLevel
	}
	result.DefaultReasoningLevel = resolveDefaultReasoningLevel(result.SupportedReasoningLevels, result.DefaultReasoningLevel)
	if overrides.InputModalities != nil {
		result.InputModalities = append([]string{}, (*overrides.InputModalities)...)
	}
	if overrides.ServiceTiers != nil {
		result.ServiceTiers = append([]string{}, (*overrides.ServiceTiers)...)
	}
	return result
}

func resolveDefaultReasoningLevel(levels []string, preferred string) string {
	if slices.Contains(levels, preferred) {
		return preferred
	}
	if slices.Contains(levels, "medium") {
		return "medium"
	}
	if len(levels) != 0 {
		return levels[0]
	}
	return ""
}

// Metadata 只保留模型属性，移除目录选择和排序覆盖。
func (overrides ClientModelOverrides) Metadata() ClientModelOverrides {
	overrides = overrides.Clone()
	overrides.CatalogEnabled, overrides.CatalogOrder = nil, nil
	return overrides
}

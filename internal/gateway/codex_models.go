package gateway

import (
	"encoding/json"
	"math"

	"gpt-load/internal/catalog"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type codexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

type codexModel struct {
	Slug                              string                `json:"slug"`
	DisplayName                       string                `json:"display_name"`
	Description                       string                `json:"description"`
	DefaultReasoningLevel             *string               `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels          []codexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                         string                `json:"shell_type"`
	Visibility                        string                `json:"visibility"`
	SupportedInAPI                    bool                  `json:"supported_in_api"`
	Priority                          int                   `json:"priority"`
	AdditionalSpeedTiers              []string              `json:"additional_speed_tiers"`
	ServiceTiers                      []string              `json:"service_tiers"`
	AvailabilityNUX                   *string               `json:"availability_nux"`
	Upgrade                           *string               `json:"upgrade"`
	SupportsReasoningSummaryParameter bool                  `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary           string                `json:"default_reasoning_summary"`
	SupportVerbosity                  bool                  `json:"support_verbosity"`
	DefaultVerbosity                  *string               `json:"default_verbosity"`
	ApplyPatchToolType                *string               `json:"apply_patch_tool_type"`
	TruncationPolicy                  codexTruncationPolicy `json:"truncation_policy"`
	ContextWindow                     *int64                `json:"context_window,omitempty"`
	MaxContextWindow                  *int64                `json:"max_context_window,omitempty"`
	EffectiveContextWindowPercent     int                   `json:"effective_context_window_percent"`
	ExperimentalSupportedTools        []string              `json:"experimental_supported_tools"`
	InputModalities                   []string              `json:"input_modalities"`
}

func buildCodexModelList(snapshot *state.ConfigSnapshot, runtime *catalog.Runtime, accessKey state.AccessKeyView, limit int64) ([]byte, error) {
	body := []byte(`{"models":[`)
	if int64(len(body)+2) > limit {
		return nil, errModelListTooLarge
	}
	ids, err := collectVisibleModelIDs(snapshot, accessKey, protocol.OpenAICompletions, math.MaxInt64)
	if err != nil {
		return nil, err
	}
	profiles := state.ResolveClientModelProfiles(snapshot, runtime, ids, accessKey.Filters, modelListProtocols(protocol.OpenAICompletions))
	for index, id := range ids {
		item, err := json.Marshal(newCodexModel(id, index, profiles[id].Effective))
		if err != nil {
			return nil, err
		}
		required := int64(len(item))
		if index > 0 {
			required++
		}
		if required > limit-int64(len(body))-2 {
			return nil, errModelListTooLarge
		}
		if index > 0 {
			body = append(body, ',')
		}
		body = append(body, item...)
	}
	return append(body, ']', '}'), nil
}

func newCodexModel(id string, priority int, profile catalog.ClientModelProfile) codexModel {
	levels := make([]codexReasoningLevel, 0, len(profile.SupportedReasoningLevels))
	for _, level := range profile.SupportedReasoningLevels {
		levels = append(levels, codexReasoningLevel{Effort: level, Description: level})
	}
	var defaultLevel *string
	if profile.DefaultReasoningLevel != "" {
		defaultLevel = &profile.DefaultReasoningLevel
	}
	return codexModel{
		Slug: id, DisplayName: profile.DisplayName, Description: profile.Description,
		DefaultReasoningLevel: defaultLevel, SupportedReasoningLevels: levels,
		ShellType: "unified_exec", Visibility: "list", SupportedInAPI: true, Priority: priority,
		AdditionalSpeedTiers: []string{}, ServiceTiers: []string{},
		SupportsReasoningSummaryParameter: profile.SupportsReasoningSummary, DefaultReasoningSummary: "none",
		SupportVerbosity: profile.SupportVerbosity, TruncationPolicy: codexTruncationPolicy{Mode: "tokens", Limit: 10000},
		ContextWindow: profile.ContextWindow, MaxContextWindow: profile.ContextWindow, EffectiveContextWindowPercent: 95,
		ExperimentalSupportedTools: []string{}, InputModalities: profile.InputModalities,
	}
}

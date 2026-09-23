package catalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

const (
	// CodexModelCatalogVersion identifies the Codex client contract represented by the embedded snapshot.
	CodexModelCatalogVersion = "0.155.0"
	// CodexModelCatalogCPASDKVersion identifies the CPA SDK release tested with this snapshot.
	CodexModelCatalogCPASDKVersion = "v7.3.15"
	codexModelCatalogSHA256        = "7b15fec55ed279c2a0f4b6dfd1f7d2617d7c9f22e94d385242a8cd9534411d38"
	codexFallbackModel             = "gpt-5.5"
)

const personalityPlaceholder = "{{ personality }}"

// Snapshot source: router-for-me/CLIProxyAPI v7.3.15, internal/registry/models/codex_client_models.json.
// CPA fetches this upstream model catalog using Codex client version 0.155.0.
//
//go:embed codex_client_models_0.155.0.json
var codexClientModelsJSON []byte

type codexModelCatalog struct {
	models   map[string]map[string]any
	fallback map[string]any
}

var (
	codexModelCatalogOnce sync.Once
	codexModelCatalogData codexModelCatalog
	codexModelCatalogErr  error
)

// BuildCodexClientModel resolves one client model from the pinned template catalog and applies manual overrides.
func BuildCodexClientModel(
	model string,
	priority int,
	overrides ClientModelOverrides,
) (map[string]any, ClientModelProfile, ClientModelProfile, error) {
	return buildCodexClientModel(model, priority, overrides, false)
}

// BuildCodexFallbackClientModel resolves a virtual client model from the pinned fallback template.
func BuildCodexFallbackClientModel(model string, priority int) (map[string]any, error) {
	resolved, _, _, err := buildCodexClientModel(model, priority, ClientModelOverrides{}, true)
	return resolved, err
}

func buildCodexClientModel(
	model string,
	priority int,
	overrides ClientModelOverrides,
	forceFallback bool,
) (map[string]any, ClientModelProfile, ClientModelProfile, error) {
	catalog, err := loadCodexModelCatalog()
	if err != nil {
		return nil, ClientModelProfile{}, ClientModelProfile{}, err
	}
	template, matched := catalog.models[model]
	if !matched || forceFallback {
		template = catalog.fallback
		matched = false
	}
	resolved := cloneCodexModelMap(template)
	resolved["slug"] = model
	resolved["priority"] = priority
	resolved["visibility"] = "list"
	if !matched {
		resolved["display_name"] = model
		resolved["description"] = model
		delete(resolved, "upgrade")
		delete(resolved, "availability_nux")
	}

	automatic, err := clientModelProfileFromTemplate(resolved)
	if err != nil {
		return nil, ClientModelProfile{}, ClientModelProfile{}, fmt.Errorf("resolve Codex model %q: %w", model, err)
	}
	effective := automatic.Apply(overrides)
	applyClientModelProfile(resolved, effective, overrides)
	if err := applyLegacyBaseInstructions(resolved); err != nil {
		return nil, ClientModelProfile{}, ClientModelProfile{}, fmt.Errorf("resolve Codex model %q: %w", model, err)
	}
	return resolved, automatic, effective, nil
}

// ResolveClientModelProfile returns the automatic and manually overridden values exposed by the control plane.
func ResolveClientModelProfile(
	model string,
	overrides ClientModelOverrides,
) (ClientModelProfile, ClientModelProfile, error) {
	_, automatic, effective, err := BuildCodexClientModel(model, 0, overrides)
	return automatic, effective, err
}

func loadCodexModelCatalog() (codexModelCatalog, error) {
	codexModelCatalogOnce.Do(func() {
		decoder := json.NewDecoder(bytes.NewReader(codexClientModelsJSON))
		decoder.UseNumber()
		var payload struct {
			Models []map[string]any `json:"models"`
		}
		if err := decoder.Decode(&payload); err != nil {
			codexModelCatalogErr = fmt.Errorf("decode Codex %s model catalog: %w", CodexModelCatalogVersion, err)
			return
		}
		models := make(map[string]map[string]any, len(payload.Models))
		for _, model := range payload.Models {
			slug, _ := model["slug"].(string)
			if strings.TrimSpace(slug) == "" {
				codexModelCatalogErr = fmt.Errorf("Codex %s model catalog contains an empty slug", CodexModelCatalogVersion)
				return
			}
			if _, duplicate := models[slug]; duplicate {
				codexModelCatalogErr = fmt.Errorf("Codex %s model catalog contains duplicate slug %q", CodexModelCatalogVersion, slug)
				return
			}
			models[slug] = model
		}
		fallback := models[codexFallbackModel]
		if fallback == nil {
			codexModelCatalogErr = fmt.Errorf("Codex %s model catalog is missing fallback %q", CodexModelCatalogVersion, codexFallbackModel)
			return
		}
		codexModelCatalogData = codexModelCatalog{models: models, fallback: fallback}
	})
	return codexModelCatalogData, codexModelCatalogErr
}

func clientModelProfileFromTemplate(model map[string]any) (ClientModelProfile, error) {
	displayName, _ := model["display_name"].(string)
	if strings.TrimSpace(displayName) == "" {
		return ClientModelProfile{}, fmt.Errorf("display_name is missing")
	}
	contextWindow, err := codexInteger(model["context_window"])
	if err != nil || contextWindow < 1 {
		return ClientModelProfile{}, fmt.Errorf("context_window is invalid")
	}
	reasoningLevels, err := codexReasoningEfforts(model["supported_reasoning_levels"])
	if err != nil || len(reasoningLevels) == 0 {
		return ClientModelProfile{}, fmt.Errorf("supported_reasoning_levels is invalid")
	}
	inputModalities, err := codexStrings(model["input_modalities"])
	if err != nil || len(inputModalities) == 0 {
		return ClientModelProfile{}, fmt.Errorf("input_modalities is invalid")
	}
	return ClientModelProfile{
		DisplayName:              displayName,
		ContextWindow:            &contextWindow,
		SupportedReasoningLevels: reasoningLevels,
		InputModalities:          inputModalities,
	}, nil
}

func applyClientModelProfile(
	model map[string]any,
	effective ClientModelProfile,
	overrides ClientModelOverrides,
) {
	model["display_name"] = effective.DisplayName
	if overrides.ContextWindow != nil {
		model["context_window"] = *effective.ContextWindow
		model["max_context_window"] = *effective.ContextWindow
	}
	if overrides.SupportedReasoningLevels != nil {
		model["supported_reasoning_levels"] = codexReasoningLevels(
			model["supported_reasoning_levels"],
			effective.SupportedReasoningLevels,
		)
		defaultLevel, _ := model["default_reasoning_level"].(string)
		if !slices.Contains(effective.SupportedReasoningLevels, defaultLevel) {
			if slices.Contains(effective.SupportedReasoningLevels, "medium") {
				model["default_reasoning_level"] = "medium"
			} else {
				model["default_reasoning_level"] = effective.SupportedReasoningLevels[0]
			}
		}
	}
	if overrides.InputModalities != nil {
		model["input_modalities"] = append([]string{}, effective.InputModalities...)
	}
}

func applyLegacyBaseInstructions(model map[string]any) error {
	messages, ok := model["model_messages"].(map[string]any)
	if !ok {
		return fmt.Errorf("model_messages is missing")
	}
	template, _ := messages["instructions_template"].(string)
	if template == "" {
		return fmt.Errorf("model_messages.instructions_template is missing")
	}
	var defaultPersonality string
	if variables, ok := messages["instructions_variables"].(map[string]any); ok {
		defaultPersonality, _ = variables["personality_default"].(string)
	}
	model["base_instructions"] = replacePersonality(template, defaultPersonality)
	return nil
}

func replacePersonality(template, personality string) string {
	return strings.ReplaceAll(template, personalityPlaceholder, personality)
}

func codexReasoningEfforts(value any) ([]string, error) {
	levels, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array")
	}
	result := make([]string, 0, len(levels))
	for _, value := range levels {
		level, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("entry must be an object")
		}
		effort, _ := level["effort"].(string)
		if effort == "" {
			return nil, fmt.Errorf("entry effort is missing")
		}
		result = append(result, effort)
	}
	return result, nil
}

func codexReasoningLevels(existing any, efforts []string) []any {
	descriptions := make(map[string]string)
	if levels, ok := existing.([]any); ok {
		for _, value := range levels {
			level, _ := value.(map[string]any)
			effort, _ := level["effort"].(string)
			description, _ := level["description"].(string)
			if effort != "" {
				descriptions[effort] = description
			}
		}
	}
	result := make([]any, 0, len(efforts))
	for _, effort := range efforts {
		description := descriptions[effort]
		if description == "" {
			description = effort
		}
		result = append(result, map[string]any{"effort": effort, "description": description})
	}
	return result
}

func codexStrings(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		item, ok := value.(string)
		if !ok || item == "" {
			return nil, fmt.Errorf("entry must be a non-empty string")
		}
		result = append(result, item)
	}
	return result, nil
}

func codexInteger(value any) (int64, error) {
	switch value := value.(type) {
	case json.Number:
		return value.Int64()
	case int64:
		return value, nil
	case int:
		return int64(value), nil
	default:
		return 0, fmt.Errorf("must be an integer")
	}
}

func cloneCodexModelMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneCodexModelValue(value)
	}
	return result
}

func cloneCodexModelValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneCodexModelMap(value)
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = cloneCodexModelValue(item)
		}
		return result
	default:
		return value
	}
}

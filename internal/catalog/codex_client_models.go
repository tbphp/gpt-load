package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/modelcatalog"
)

const (
	// CodexModelCatalogVersion identifies the Codex client contract represented by the embedded snapshot.
	CodexModelCatalogVersion = modelcatalog.ClientVersion
	// CodexModelCatalogCPASDKVersion identifies the CPA SDK release tested with this snapshot.
	CodexModelCatalogCPASDKVersion = "v8.0.23"
	codexModelCatalogSHA256        = modelcatalog.ClientSHA256
	codexFallbackModel             = "gpt-5.5"
)

const personalityPlaceholder = "{{ personality }}"

// Client templates and CPA reasoning levels share the same pinned source.
var codexClientModelsJSON = modelcatalog.ClientJSON()

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
	return BuildCodexCatalogModel(model, priority, overrides, false)
}

// BuildCodexCatalogModel 用同一资料契约生成普通模型或虚拟模型的目录条目。
func BuildCodexCatalogModel(
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
	resolved["supports_reasoning_effort_updates"] = !forceFallback && modelcatalog.SupportsReasoningUpdates(model)
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
	description, _ := model["description"].(string)
	var autoCompactTokenLimit *int64
	if model["auto_compact_token_limit"] != nil {
		limit, err := codexInteger(model["auto_compact_token_limit"])
		if err != nil || limit < 1 || limit > 9_007_199_254_740_991 {
			return ClientModelProfile{}, fmt.Errorf("auto_compact_token_limit is invalid")
		}
		autoCompactTokenLimit = &limit
	}
	reasoningLevels, err := codexReasoningEfforts(model["supported_reasoning_levels"])
	if err != nil || len(reasoningLevels) == 0 {
		return ClientModelProfile{}, fmt.Errorf("supported_reasoning_levels is invalid")
	}
	inputModalities, err := codexStrings(model["input_modalities"])
	if err != nil || len(inputModalities) == 0 {
		return ClientModelProfile{}, fmt.Errorf("input_modalities is invalid")
	}
	serviceTiers, err := codexServiceTierIDs(model["service_tiers"])
	if err != nil {
		return ClientModelProfile{}, fmt.Errorf("service_tiers: %w", err)
	}
	defaultReasoningLevel, _ := model["default_reasoning_level"].(string)
	return ClientModelProfile{
		DisplayName:              displayName,
		Description:              description,
		ContextWindow:            &contextWindow,
		AutoCompactTokenLimit:    autoCompactTokenLimit,
		SupportedReasoningLevels: reasoningLevels,
		DefaultReasoningLevel:    resolveDefaultReasoningLevel(reasoningLevels, defaultReasoningLevel),
		InputModalities:          inputModalities,
		ServiceTiers:             serviceTiers,
	}, nil
}

func applyClientModelProfile(
	model map[string]any,
	effective ClientModelProfile,
	overrides ClientModelOverrides,
) {
	model["display_name"] = effective.DisplayName
	model["description"] = effective.Description
	if overrides.ContextWindow != nil {
		model["context_window"] = *effective.ContextWindow
		model["max_context_window"] = *effective.ContextWindow
	}
	if overrides.AutoCompactTokenLimit != nil {
		model["auto_compact_token_limit"] = *effective.AutoCompactTokenLimit
	}
	if overrides.SupportedReasoningLevels != nil {
		model["supported_reasoning_levels"] = codexReasoningLevels(
			model["supported_reasoning_levels"],
			effective.SupportedReasoningLevels,
		)
	}
	model["default_reasoning_level"] = effective.DefaultReasoningLevel
	if overrides.InputModalities != nil {
		model["input_modalities"] = append([]string{}, effective.InputModalities...)
	}
	if overrides.ServiceTiers != nil {
		model["service_tiers"] = codexServiceTiers(model["service_tiers"], effective.ServiceTiers)
		// 旧字段仍被部分客户端读取，隐藏 Fast 时一并清除，避免继续显示。
		legacy := []string{}
		if slices.Contains(effective.ServiceTiers, "priority") {
			legacy = append(legacy, "fast")
		}
		model["additional_speed_tiers"] = legacy
	}
	// 目录默认使用标准档位，加速档位由客户端显式选择。
	model["default_service_tier"] = nil
}

func codexServiceTierIDs(value any) ([]string, error) {
	result := []string{}
	if value == nil {
		return result, nil
	}
	tiers, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array")
	}
	for _, value := range tiers {
		tier, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("entry must be an object")
		}
		id, _ := tier["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("entry id is missing")
		}
		// 编辑器只管理网关已有的 Fast / Ultrafast 处理模式。
		if slices.Contains([]string{"priority", "ultrafast"}, id) && !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	return result, nil
}

func codexServiceTiers(existing any, ids []string) []any {
	presets := map[string]map[string]any{}
	if tiers, ok := existing.([]any); ok {
		for _, value := range tiers {
			if tier, ok := value.(map[string]any); ok {
				id, _ := tier["id"].(string)
				presets[id] = tier
			}
		}
	}
	result := make([]any, 0, len(ids))
	for _, id := range ids {
		if preset := presets[id]; preset != nil {
			result = append(result, preset)
			continue
		}
		name := "Fast"
		if id == "ultrafast" {
			name = "Ultrafast"
		}
		result = append(result, map[string]any{
			"id": id, "name": name, "description": name + " processing",
		})
	}
	return result
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

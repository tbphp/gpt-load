// Package automodel classifies task data into administrator-defined presets.
package automodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"gpt-load/internal/parameteroverride"
)

const SettingKey = "auto_model"

var ErrInvalidConfig = errors.New("invalid automatic model configuration")

const PromptVersion = "jev-presets-v4"
const MaxStateBytes = 16 << 10
const MaxRequestBytes = 24 << 10

type Config struct {
	Enabled        bool    `json:"enabled"`
	Model          string  `json:"model"`
	TimeoutSeconds int     `json:"timeout_seconds"`
	Models         []Entry `json:"models"`
}

type Entry struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	Fallback string   `json:"fallback"`
	Presets  []Preset `json:"presets"`
}

type Preset struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	Model              string          `json:"model"`
	ParameterOverrides json.RawMessage `json:"parameter_overrides"`
}

type CompiledPreset struct {
	Preset
	Rules parameteroverride.Rules
}

type CompiledEntry struct {
	ID       string
	Name     string
	Enabled  bool
	Fallback string
	Presets  []CompiledPreset
}

type Compiled struct {
	config  Config
	entries map[string]CompiledEntry
}

func DefaultConfig() Config {
	return Config{TimeoutSeconds: 2, Models: []Entry{}}
}

func Decode(raw []byte) (Config, error) {
	config, legacy, err := DecodeStored(raw)
	if err != nil {
		return Config{}, err
	}
	if legacy {
		return Config{}, fmt.Errorf("legacy direct Jev configuration is not accepted")
	}
	return config, nil
}

// DecodeStored accepts the previously persisted experimental shape only so
// startup recovery can replace it with the current channel-backed shape.
func DecodeStored(raw []byte) (Config, bool, error) {
	if len(raw) > 1<<20 {
		return Config{}, false, fmt.Errorf("automatic model configuration is too large")
	}
	type storedConfig struct {
		Enabled        bool    `json:"enabled"`
		Model          string  `json:"model"`
		TimeoutSeconds int     `json:"timeout_seconds"`
		Models         []Entry `json:"models"`
		Provider       *string `json:"provider,omitempty"`
		APIKey         *string `json:"api_key,omitempty"`
		InputPrice     *string `json:"input_price,omitempty"`
		OutputPrice    *string `json:"output_price,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	stored := storedConfig{TimeoutSeconds: DefaultConfig().TimeoutSeconds, Models: []Entry{}}
	if err := decoder.Decode(&stored); err != nil {
		return Config{}, false, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, false, fmt.Errorf("invalid configuration JSON")
	}
	legacy := stored.Provider != nil || stored.APIKey != nil || stored.InputPrice != nil || stored.OutputPrice != nil
	config := Config{
		Enabled: stored.Enabled, Model: stored.Model,
		TimeoutSeconds: stored.TimeoutSeconds, Models: stored.Models,
	}
	if legacy {
		config.Enabled = false
		config.Model = ""
	}
	return config, legacy, nil
}

func Compile(config Config, ordinaryModels, decisionModels map[string]struct{}) (result *Compiled, compileErr error) {
	defer func() {
		if compileErr != nil {
			compileErr = fmt.Errorf("%w: %v", ErrInvalidConfig, compileErr)
		}
	}()
	// 深拷贝自由 JSON 字段，保证请求使用不可变的配置版本。
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	config, err = Decode(raw)
	if err != nil {
		return nil, err
	}
	if config.Models == nil {
		return nil, fmt.Errorf("automatic models must be an array")
	}
	if config.TimeoutSeconds < 1 || config.TimeoutSeconds > 60 || config.Model != "" && !validName(config.Model) {
		return nil, fmt.Errorf("invalid Jev model or timeout")
	}
	if config.Enabled {
		if !validName(config.Model) {
			return nil, fmt.Errorf("Jev decision model is required")
		}
		if _, exists := decisionModels[config.Model]; !exists {
			return nil, fmt.Errorf("Jev decision model does not have an enabled Decisions route")
		}
	}
	// 自动模型只有系统级总开关；保留字段用于读取早期实验配置，但入口始终随总开关启用。
	for index := range config.Models {
		config.Models[index].Enabled = true
	}
	compiled := &Compiled{config: config, entries: map[string]CompiledEntry{}}
	ids := map[string]struct{}{}
	for _, entry := range config.Models {
		if !validName(entry.ID) || !validName(entry.Name) {
			return nil, fmt.Errorf("automatic model ID and name are required")
		}
		if _, exists := ids[entry.ID]; exists {
			return nil, fmt.Errorf("duplicate automatic model ID")
		}
		ids[entry.ID] = struct{}{}
		if _, exists := compiled.entries[entry.Name]; exists {
			return nil, fmt.Errorf("duplicate automatic model name")
		}
		if _, exists := ordinaryModels[entry.Name]; exists {
			return nil, fmt.Errorf("automatic model name conflicts with an ordinary model or alias")
		}
		if len(entry.Presets) < 1 || len(entry.Presets) > 254 {
			return nil, fmt.Errorf("automatic model requires 1 through 254 presets")
		}
		value := CompiledEntry{ID: entry.ID, Name: entry.Name, Enabled: entry.Enabled, Fallback: entry.Fallback}
		criteria := map[string]string{}
		for _, preset := range entry.Presets {
			if !validName(preset.ID) || strings.TrimSpace(preset.Name) == "" ||
				strings.TrimSpace(preset.Description) == "" || !validName(preset.Model) {
				return nil, fmt.Errorf("invalid preset ID, name, description or target model")
			}
			if _, exists := criteria[preset.ID]; exists {
				return nil, fmt.Errorf("duplicate preset ID")
			}
			criteria[preset.ID] = preset.Description
			if config.Enabled {
				if _, exists := ordinaryModels[preset.Model]; !exists {
					return nil, fmt.Errorf("enabled preset target model does not exist")
				}
			}
			if len(preset.ParameterOverrides) == 0 {
				preset.ParameterOverrides = json.RawMessage(`[]`)
			}
			var rules any
			decoder := json.NewDecoder(bytes.NewReader(preset.ParameterOverrides))
			decoder.UseNumber()
			if err := decoder.Decode(&rules); err != nil {
				return nil, fmt.Errorf("invalid preset parameter overrides")
			}
			compiledRules, err := parameteroverride.Compile(rules)
			if err != nil || compiledRules.ValidateResponsesContinuation() != nil {
				return nil, fmt.Errorf("invalid preset parameter overrides")
			}
			value.Presets = append(value.Presets, CompiledPreset{Preset: preset, Rules: compiledRules})
		}
		if _, exists := criteria[entry.Fallback]; !exists {
			return nil, fmt.Errorf("fallback preset does not exist")
		}
		payload, _ := json.Marshal(map[string]any{"model": config.Model, "state": map[string]any{}, "questions": map[string]any{
			"preset": map[string]any{"type": "choice", "instructions": Instructions, "criteria": criteria},
		}})
		if len(payload) > MaxRequestBytes-MaxStateBytes {
			return nil, fmt.Errorf("preset criteria exceed the decision request budget")
		}
		compiled.entries[entry.Name] = value
	}
	for _, entry := range compiled.entries {
		for _, preset := range entry.Presets {
			if _, recursive := compiled.entries[preset.Model]; recursive {
				return nil, fmt.Errorf("preset cannot target another automatic model")
			}
		}
	}
	return compiled, nil
}

func validName(value string) bool {
	return value != "" && len(value) <= 255 && value == strings.TrimSpace(value) && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (compiled *Compiled) Config() Config {
	if compiled == nil {
		return DefaultConfig()
	}
	raw, _ := json.Marshal(compiled.config)
	config, _ := Decode(raw)
	for entryIndex := range config.Models {
		for presetIndex := range config.Models[entryIndex].Presets {
			if len(config.Models[entryIndex].Presets[presetIndex].ParameterOverrides) == 0 {
				config.Models[entryIndex].Presets[presetIndex].ParameterOverrides = json.RawMessage(`[]`)
			}
		}
	}
	return config
}

func (compiled *Compiled) Lookup(name string) (CompiledEntry, bool) {
	if compiled == nil {
		return CompiledEntry{}, false
	}
	entry, exists := compiled.entries[name]
	entry.Presets = append([]CompiledPreset(nil), entry.Presets...)
	return entry, exists
}

func (compiled *Compiled) Enabled() bool { return compiled != nil && compiled.config.Enabled }

func Template() Entry {
	entry := Entry{ID: "auto", Name: "auto", Enabled: true, Fallback: "medium", Presets: []Preset{}}
	for _, value := range []struct{ id, model, description string }{
		{"low", "gpt-5.6-luna", "Fully specified routine work with one clear path and no diagnosis or trade-offs: direct answers, extraction, translation, formatting, or an exact mechanical edit."},
		{"medium", "gpt-5.6-terra", "Bounded work using established methods: explanation, comparison, routine implementation, or debugging from clear evidence. It needs several connected steps but no broad investigation."},
		{"high", "gpt-5.6-sol", "Difficult but bounded work where the goal is known and the path requires investigating uncertain causes, nontrivial reasoning, or meaningful trade-offs across components."},
		{"max", "gpt-6-astra", "The highest capability level for open-ended or high-risk work with many coupled constraints, multi-system architecture or migration decisions, difficult multi-hop diagnosis, or subtle interactions requiring broad investigation and careful validation."},
	} {
		rules, _ := json.Marshal([]any{
			map[string]any{"match": map[string]string{"protocol": "openai-completions"}, "set": map[string]string{"reasoning_effort": value.id}},
			map[string]any{"match": map[string]string{"protocol": "openai-responses"}, "set": map[string]any{"reasoning": map[string]string{"effort": value.id}}},
		})
		entry.Presets = append(entry.Presets, Preset{ID: value.id, Name: value.id, Model: value.model, Description: value.description, ParameterOverrides: rules})
	}
	return entry
}

// Package automodel classifies task data into administrator-defined presets.
package automodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/pricing"
)

const SettingKey = "auto_model"

var ErrInvalidConfig = errors.New("invalid automatic model configuration")

const PromptVersion = "jev-presets-v1"
const Uncertain = "uncertain"
const MaxStateBytes = 16 << 10
const MaxRequestBytes = 24 << 10

type Config struct {
	Enabled        bool    `json:"enabled"`
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	APIKey         string  `json:"api_key"`
	TimeoutSeconds int     `json:"timeout_seconds"`
	MinConfidence  float64 `json:"min_confidence"`
	InputPrice     string  `json:"input_price"`
	OutputPrice    string  `json:"output_price"`
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
	Prices  *pricing.Table
}

func DefaultConfig() Config {
	return Config{Provider: "typesafe", Model: "jev-latest", TimeoutSeconds: 2,
		MinConfidence: 0.5, InputPrice: "0.042", OutputPrice: "0", Models: []Entry{}}
}

func Decode(raw []byte) (Config, error) {
	if len(raw) > 1<<20 {
		return Config{}, fmt.Errorf("automatic model configuration is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	config := DefaultConfig()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, fmt.Errorf("invalid configuration JSON")
	}
	return config, nil
}

func Compile(config Config, ordinaryModels map[string]struct{}) (result *Compiled, compileErr error) {
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
	if config.Provider != "typesafe" && config.Provider != "openrouter" {
		return nil, fmt.Errorf("unsupported Jev provider")
	}
	if config.Models == nil {
		return nil, fmt.Errorf("automatic models must be an array")
	}
	if !validName(config.Model) || config.TimeoutSeconds < 1 || config.TimeoutSeconds > 60 ||
		math.IsNaN(config.MinConfidence) || math.IsInf(config.MinConfidence, 0) || config.MinConfidence < 0 || config.MinConfidence > 1 {
		return nil, fmt.Errorf("invalid Jev model, timeout or confidence threshold")
	}
	if strings.ContainsAny(config.APIKey, "\r\n") || (config.Enabled && strings.TrimSpace(config.APIKey) == "") {
		return nil, fmt.Errorf("Jev API key is required")
	}
	input, err := pricing.ParseUSD(config.InputPrice)
	if err != nil || input < 0 {
		return nil, fmt.Errorf("invalid Jev input price")
	}
	output, err := pricing.ParseUSD(config.OutputPrice)
	if err != nil || output < 0 {
		return nil, fmt.Errorf("invalid Jev output price")
	}
	identity := pricing.Identity{ChannelID: "jev-" + config.Provider, ModelID: config.Model}
	prices, err := pricing.NewTable([]pricing.Rule{{Identity: identity, IsManual: true, Prices: pricing.Prices{
		Input: pricing.Price{Set: true, NanoUSDPerMillion: input}, Output: pricing.Price{Set: true, NanoUSDPerMillion: output},
	}}})
	if err != nil {
		return nil, err
	}
	compiled := &Compiled{config: config, entries: map[string]CompiledEntry{}, Prices: prices}
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
		criteria := map[string]string{Uncertain: UncertainCriteria}
		for _, preset := range entry.Presets {
			if !validName(preset.ID) || preset.ID == Uncertain || strings.TrimSpace(preset.Name) == "" ||
				strings.TrimSpace(preset.Description) == "" || !validName(preset.Model) {
				return nil, fmt.Errorf("invalid preset ID, name, description or target model")
			}
			if _, exists := criteria[preset.ID]; exists {
				return nil, fmt.Errorf("duplicate preset ID")
			}
			criteria[preset.ID] = preset.Description
			if config.Enabled && entry.Enabled {
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
		if _, exists := criteria[entry.Fallback]; !exists || entry.Fallback == Uncertain {
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
	entry := Entry{ID: "auto", Name: "auto", Fallback: "medium", Presets: []Preset{}}
	for _, value := range []struct{ id, model, description string }{
		{"low", "gpt-5.6-luna", "Use for a narrow, well-specified task with an obvious solution: extracting fields, translating straightforward text, reformatting supplied content, answering a basic question, or applying a small mechanical edit. The task does not require substantial investigation or interacting trade-offs."},
		{"medium", "gpt-5.6-terra", "Use for ordinary implementation, explanation, analysis, or debugging that requires several connected steps. The objective is clear, the scope is bounded, and the constraints can be handled without deep investigation or major architectural reasoning."},
		{"high", "gpt-5.6-sol", "Use for difficult root-cause analysis, architecture decisions, complex algorithms, or work that must reconcile many interacting constraints. Substantial investigation, deep reasoning, or careful evaluation of trade-offs is needed to complete the current task reliably."},
	} {
		rules, _ := json.Marshal([]any{
			map[string]any{"match": map[string]string{"protocol": "openai-completions"}, "set": map[string]string{"reasoning_effort": value.id}},
			map[string]any{"match": map[string]string{"protocol": "openai-responses"}, "set": map[string]any{"reasoning": map[string]string{"effort": value.id}}},
		})
		entry.Presets = append(entry.Presets, Preset{ID: value.id, Name: value.id, Model: value.model, Description: value.description, ParameterOverrides: rules})
	}
	return entry
}

// Package loader adapts persisted storage rows into runtime state.
package loader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type Loader struct {
	db       *gorm.DB
	manager  *state.Manager
	registry *state.KeyRegistry
}

type compileRows struct {
	settings   []models.SystemSetting
	groups     []models.Group
	accessKeys []models.AccessKey
}

type modelDTO struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

type filterDTO struct {
	Groups    []uint              `json:"groups"`
	Protocols []protocol.Protocol `json:"protocols"`
	Models    []string            `json:"models"`
}

func (f filterDTO) toState() state.FilterSet {
	filters := state.FilterSet{}
	if len(f.Groups) > 0 {
		filters.Groups = make(map[uint]struct{}, len(f.Groups))
		for _, id := range f.Groups {
			filters.Groups[id] = struct{}{}
		}
	}
	if len(f.Protocols) > 0 {
		filters.Protocols = make(map[protocol.Protocol]struct{}, len(f.Protocols))
		for _, p := range f.Protocols {
			filters.Protocols[p] = struct{}{}
		}
	}
	if len(f.Models) > 0 {
		filters.Models = make(map[string]struct{}, len(f.Models))
		for _, model := range f.Models {
			filters.Models[model] = struct{}{}
		}
	}
	return filters
}

func New(db *gorm.DB, manager *state.Manager, registry *state.KeyRegistry) *Loader {
	return &Loader{db: db, manager: manager, registry: registry}
}

func (l *Loader) Load(ctx context.Context) error {
	input, entries, err := l.read(ctx)
	if err != nil {
		return fmt.Errorf("read runtime state: %w", err)
	}
	if err := state.ValidateKeyEntries(entries); err != nil {
		return fmt.Errorf("validate upstream keys: %w", err)
	}
	if _, err := l.manager.Publish(input); err != nil {
		return fmt.Errorf("publish config snapshot: %w", err)
	}
	if err := l.registry.Replace(entries); err != nil {
		return fmt.Errorf("replace key registry: %w", err)
	}
	return nil
}

func queryCompileRows(ctx context.Context, db *gorm.DB) (compileRows, error) {
	db = db.WithContext(ctx)
	var rows compileRows
	if err := db.Order("key ASC").Find(&rows.settings).Error; err != nil {
		return compileRows{}, fmt.Errorf("query system settings: %w", err)
	}
	if err := db.Order("id ASC").Find(&rows.groups).Error; err != nil {
		return compileRows{}, fmt.Errorf("query groups: %w", err)
	}
	if err := db.Order("id ASC").Find(&rows.accessKeys).Error; err != nil {
		return compileRows{}, fmt.Errorf("query access keys: %w", err)
	}
	return rows, nil
}

func queryUpstreamKeys(ctx context.Context, db *gorm.DB) ([]models.UpstreamKey, error) {
	var rows []models.UpstreamKey
	if err := db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query upstream keys: %w", err)
	}
	return rows, nil
}

// BuildCompileInput maps persisted configuration rows into a runtime compiler input.
func BuildCompileInput(ctx context.Context, db *gorm.DB) (state.CompileInput, error) {
	rows, err := queryCompileRows(ctx, db)
	if err != nil {
		return state.CompileInput{}, err
	}
	input, err := mapSystemAndGroups(rows)
	if err != nil {
		return state.CompileInput{}, err
	}
	input.AccessKeys, err = mapAccessKeys(rows.accessKeys)
	if err != nil {
		return state.CompileInput{}, err
	}
	return input, nil
}

func (l *Loader) read(ctx context.Context) (state.CompileInput, []state.KeyEntry, error) {
	input, err := BuildCompileInput(ctx, l.db)
	if err != nil {
		return state.CompileInput{}, nil, err
	}
	upstreamKeys, err := queryUpstreamKeys(ctx, l.db)
	if err != nil {
		return state.CompileInput{}, nil, err
	}
	return input, mapUpstreamKeys(upstreamKeys), nil
}

func decodeJSON(raw models.JSON, target any) error {
	return decodeJSONDocument(raw, target, false)
}

func decodeFilterJSON(raw models.JSON, target *filterDTO) error {
	return decodeJSONDocument(raw, target, true)
}

func decodeJSONDocument(raw models.JSON, target any, disallowUnknownFields bool) error {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func decodeSettingValue(raw string) (any, error) {
	if !json.Valid([]byte(raw)) {
		return raw, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func isInternalSystemSetting(key string) bool {
	return strings.HasPrefix(key, models.InternalSystemSettingPrefix)
}

// LoadSystemSettings reads only the persisted system settings used to compile a draft Group.
func LoadSystemSettings(ctx context.Context, db *gorm.DB) (config.Settings, error) {
	var rows []models.SystemSetting
	if err := db.WithContext(ctx).Order("key ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query system settings: %w", err)
	}
	settings := make(config.Settings, len(rows))
	for _, row := range rows {
		if isInternalSystemSetting(row.Key) {
			continue
		}
		value, err := decodeSettingValue(row.Value)
		if err != nil {
			return nil, fmt.Errorf("decode system setting %q: %w", row.Key, err)
		}
		settings[row.Key] = value
	}
	return settings, nil
}

func mapSystemAndGroups(rows compileRows) (state.CompileInput, error) {
	input := state.CompileInput{
		SystemSettings: make(config.Settings, len(rows.settings)),
		Groups:         make([]state.GroupConfig, 0, len(rows.groups)),
	}
	for _, row := range rows.settings {
		if isInternalSystemSetting(row.Key) {
			continue
		}
		value, err := decodeSettingValue(row.Value)
		if err != nil {
			return state.CompileInput{}, fmt.Errorf("decode system setting %q: %w", row.Key, err)
		}
		input.SystemSettings[row.Key] = value
	}
	for _, row := range rows.groups {
		var protocols []protocol.Protocol
		if err := decodeJSON(row.Protocols, &protocols); err != nil {
			return state.CompileInput{}, fmt.Errorf("decode group %d protocols: %w", row.ID, err)
		}
		var storedModels []modelDTO
		if err := decodeJSON(row.Models, &storedModels); err != nil {
			return state.CompileInput{}, fmt.Errorf("decode group %d models: %w", row.ID, err)
		}
		settings := make(config.Settings)
		if err := decodeJSON(row.Config, &settings); err != nil {
			return state.CompileInput{}, fmt.Errorf("decode group %d config: %w", row.ID, err)
		}
		validationModel := ""
		if row.ValidationModel != nil {
			validationModel = strings.TrimSpace(*row.ValidationModel)
		}

		runtimeModels := make([]state.ModelConfig, 0, len(storedModels))
		for _, model := range storedModels {
			runtimeModels = append(runtimeModels, state.ModelConfig{ID: model.ID, Alias: model.Alias})
		}
		input.Groups = append(input.Groups, state.GroupConfig{
			ID:              row.ID,
			Name:            row.Name,
			UpstreamURL:     row.UpstreamURL,
			ValidationModel: validationModel,
			Protocols:       append([]protocol.Protocol(nil), protocols...),
			Models:          runtimeModels,
			Settings:        settings,
			WeightManual:    row.WeightManual,
			Enabled:         row.Enabled,
		})
	}
	return input, nil
}

func mapAccessKeys(rows []models.AccessKey) ([]state.AccessKeyConfig, error) {
	result := make([]state.AccessKeyConfig, 0, len(rows))
	for _, row := range rows {
		var filters filterDTO
		if err := decodeFilterJSON(row.Filters, &filters); err != nil {
			return nil, fmt.Errorf("decode access key %d filters: %w", row.ID, err)
		}
		result = append(result, state.AccessKeyConfig{
			ID: row.ID, Name: row.Name, KeyHash: row.KeyHash,
			Status: state.AccessKeyStatus(row.Status), Filters: filters.toState(),
		})
	}
	return result, nil
}

func mapUpstreamKeys(rows []models.UpstreamKey) []state.KeyEntry {
	result := make([]state.KeyEntry, 0, len(rows))
	for _, row := range rows {
		result = append(result, state.KeyEntry{
			ID: row.ID, GroupID: row.GroupID, WeightManual: row.WeightManual,
			WeightAuto: state.DefaultWeight,
			Status:     state.KeyStatus(row.Status), EncryptedValue: row.KeyValue,
		})
	}
	return result
}

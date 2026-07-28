package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type HeaderRulesResponse struct {
	Set    map[string]string `json:"set"`
	Remove []string          `json:"remove"`
}

type SettingsValuesResponse struct {
	ConnectTimeout          int64               `json:"connect_timeout"`
	FirstByteTimeout        int64               `json:"first_byte_timeout"`
	RequestTimeout          int64               `json:"request_timeout"`
	StreamIdleTimeout       int64               `json:"stream_idle_timeout"`
	HeaderRules             HeaderRulesResponse `json:"header_rules"`
	InjectUsageOptions      bool                `json:"inject_usage_options"`
	RequestLogRetentionDays int                 `json:"request_log_retention_days"`
}

type SettingsResponse struct {
	Revision  uint64                 `json:"revision"`
	Values    SettingsValuesResponse `json:"values"`
	Overrides []string               `json:"overrides"`
}

func (response SettingsResponse) DTO() SettingsDTO {
	return canonicalizeSettingsDTO(SettingsDTO{
		Values:    response.Values,
		Overrides: response.Overrides,
	})
}

type SettingsUpdateRequest struct {
	Settings map[string]json.RawMessage `json:"settings"`
}

type persistedSettingUpdate struct {
	key   string
	value *string
}

func (request *SettingsUpdateRequest) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if object == nil || len(object) != 1 {
		return fmt.Errorf("request body must contain only settings")
	}
	rawSettings, exists := object["settings"]
	if !exists {
		return fmt.Errorf("request body must contain settings")
	}
	trimmed := bytes.TrimSpace(rawSettings)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return fmt.Errorf("settings must be a non-null JSON object")
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(rawSettings, &settings); err != nil {
		return err
	}
	if settings == nil {
		return fmt.Errorf("settings must be a non-null JSON object")
	}
	request.Settings = settings
	return nil
}

func (s *Service) GetSettings(ctx context.Context) (SettingsResponse, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	snapshot := s.manager.Current()
	if snapshot == nil {
		return SettingsResponse{}, app_errors.ErrInternalServer
	}
	return s.getSettingsWithSnapshot(ctx, s.db, snapshot)
}

func (s *Service) getSettingsWithSnapshot(
	ctx context.Context,
	db *gorm.DB,
	snapshot *state.ConfigSnapshot,
) (SettingsResponse, error) {
	var rows []models.SystemSetting
	if err := db.WithContext(ctx).Order("key ASC").Find(&rows).Error; err != nil {
		return SettingsResponse{}, app_errors.ParseDBError(err)
	}
	return mapSettingsResponse(snapshot, rows), nil
}

func (s *Service) UpdateSettings(
	ctx context.Context,
	request SettingsUpdateRequest,
) (SettingsResponse, error) {
	updates, err := normalizeSettingUpdates(request)
	if err != nil {
		return SettingsResponse{}, err
	}

	_, err = s.writeConfig(ctx, func(tx *gorm.DB) error {
		return s.applySettingUpdates(tx, updates)
	}, nil)
	if err != nil {
		return SettingsResponse{}, err
	}
	return s.GetSettings(ctx)
}

func (s *Service) UpdateSettingsIfMatch(
	ctx context.Context,
	request SettingsUpdateRequest,
	expectedETag string,
	message string,
) (settingsWireRepresentation, error) {
	updates, err := normalizeSettingUpdates(request)
	if err != nil {
		return settingsWireRepresentation{}, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return settingsWireRepresentation{}, err
	}

	var (
		input  state.CompileInput
		result settingsWireRepresentation
	)
	err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		currentInput, err := stateloader.BuildCompileInput(ctx, tx)
		if err != nil {
			return err
		}
		currentSnapshot, err := state.Compile(currentInput)
		if err != nil {
			return err
		}
		currentSettings, err := s.getSettingsWithSnapshot(ctx, tx, currentSnapshot)
		if err != nil {
			return err
		}
		currentRepresentation, err := newSettingsWireRepresentation(
			message,
			currentSettings.DTO(),
		)
		if err != nil {
			return err
		}
		if currentRepresentation.ETag != expectedETag {
			return app_errors.NewAPIErrorWithData(
				app_errors.ErrSettingsVersionConflict,
				SettingsConflictData{
					Settings:     currentRepresentation.Settings,
					SettingsETag: currentRepresentation.ETag,
				},
			)
		}

		if err := s.applySettingUpdates(tx, updates); err != nil {
			return err
		}
		input, err = stateloader.BuildCompileInput(ctx, tx)
		if err != nil {
			return err
		}
		compiled, err := state.Compile(input)
		if err != nil {
			return err
		}
		updatedSettings, err := s.getSettingsWithSnapshot(ctx, tx, compiled)
		if err != nil {
			return err
		}
		result, err = newSettingsWireRepresentation(message, updatedSettings.DTO())
		return err
	})
	if err != nil {
		return settingsWireRepresentation{}, err
	}
	if _, err := s.manager.Publish(input); err != nil {
		return settingsWireRepresentation{}, newControlOperationError(
			stagePublishCommittedSnapshot,
		)
	}
	return result, nil
}

func (s *Service) applySettingUpdates(
	tx *gorm.DB,
	updates []persistedSettingUpdate,
) error {
	for _, update := range updates {
		if update.value == nil {
			if err := tx.Where("key = ?", update.key).
				Delete(&models.SystemSetting{}).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
			continue
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).Create(&models.SystemSetting{
			Key:       update.key,
			Value:     *update.value,
			UpdatedAt: s.now().UTC(),
		}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
	}
	return nil
}

func normalizeSettingUpdates(request SettingsUpdateRequest) ([]persistedSettingUpdate, error) {
	if len(request.Settings) == 0 {
		return nil, app_errors.ErrBadRequest
	}
	keys := make([]string, 0, len(request.Settings))
	for key := range request.Settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	updates := make([]persistedSettingUpdate, 0, len(keys))
	for _, key := range keys {
		if !state.IsRuntimeSettingKey(key) {
			return nil, app_errors.ErrValidation
		}
		raw := bytes.TrimSpace(request.Settings[key])
		if bytes.Equal(raw, []byte("null")) {
			updates = append(updates, persistedSettingUpdate{key: key})
			continue
		}
		value, err := decodeSettingValue(raw)
		if err != nil {
			return nil, app_errors.ErrValidation
		}
		if err := state.ValidateRuntimeSetting(key, value); err != nil {
			return nil, app_errors.ErrValidation
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, app_errors.ErrValidation
		}
		text := string(encoded)
		updates = append(updates, persistedSettingUpdate{key: key, value: &text})
	}
	return updates, nil
}

func decodeSettingValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func mapSettingsResponse(
	snapshot *state.ConfigSnapshot,
	rows []models.SystemSetting,
) SettingsResponse {
	settings := snapshot.Settings
	set := make(map[string]string, len(settings.HeaderRules.Set))
	for name, value := range settings.HeaderRules.Set {
		set[name] = value
	}
	remove := append([]string{}, settings.HeaderRules.Remove...)
	overrides := make([]string, 0, len(rows))
	for _, row := range rows {
		if state.IsRuntimeSettingKey(row.Key) {
			overrides = append(overrides, row.Key)
		}
	}
	return SettingsResponse{
		Revision: snapshot.Revision,
		Values: SettingsValuesResponse{
			ConnectTimeout:    durationSeconds(settings.ConnectTimeout),
			FirstByteTimeout:  durationSeconds(settings.FirstByteTimeout),
			RequestTimeout:    durationSeconds(settings.RequestTimeout),
			StreamIdleTimeout: durationSeconds(settings.StreamIdleTimeout),
			HeaderRules: HeaderRulesResponse{
				Set:    set,
				Remove: remove,
			},
			InjectUsageOptions:      settings.InjectUsageOptions,
			RequestLogRetentionDays: settings.RequestLogRetentionDays,
		},
		Overrides: overrides,
	}
}

func durationSeconds(value time.Duration) int64 {
	return int64(value / time.Second)
}

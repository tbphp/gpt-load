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
	RequestLogRetentionDays int                 `json:"request_log_retention_days"`
}

type SettingsResponse struct {
	Revision  uint64                 `json:"revision"`
	Values    SettingsValuesResponse `json:"values"`
	Overrides []string               `json:"overrides"`
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

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, isDelimiter := token.(json.Delim)
		if !isDelimiter {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key must be a string")
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate field %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected delimiter %q", delimiter)
		}
	}
	if err := walk(); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func (s *Service) GetSettings(ctx context.Context) (SettingsResponse, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	snapshot := s.manager.Current()
	if snapshot == nil {
		return SettingsResponse{}, app_errors.ErrInternalServer
	}
	var rows []models.SystemSetting
	if err := s.db.WithContext(ctx).Order("key ASC").Find(&rows).Error; err != nil {
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
	}, nil)
	if err != nil {
		return SettingsResponse{}, err
	}
	return s.GetSettings(ctx)
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
			RequestLogRetentionDays: settings.RequestLogRetentionDays,
		},
		Overrides: overrides,
	}
}

func durationSeconds(value time.Duration) int64 {
	return int64(value / time.Second)
}

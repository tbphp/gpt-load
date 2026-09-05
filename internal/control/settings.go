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

	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type HeaderRulesResponse struct {
	Set    map[string]string `json:"set"`
	Remove []string          `json:"remove"`
}

type CORSConfigResponse struct {
	Enabled          bool     `json:"enabled"`
	AllowedOrigins   []string `json:"allowed_origins"`
	AllowedMethods   []string `json:"allowed_methods"`
	AllowedHeaders   []string `json:"allowed_headers"`
	ExposedHeaders   []string `json:"exposed_headers"`
	AllowCredentials bool     `json:"allow_credentials"`
	MaxAge           int      `json:"max_age"`
}

type SettingsValuesResponse struct {
	FirstByteTimeout         int64               `json:"first_byte_timeout"`
	RequestTimeout           int64               `json:"request_timeout"`
	StreamIdleTimeout        int64               `json:"stream_idle_timeout"`
	HeaderRules              HeaderRulesResponse `json:"header_rules"`
	CORS                     CORSConfigResponse  `json:"cors"`
	ResponseHeaderRules      HeaderRulesResponse `json:"response_header_rules"`
	InjectUsageOptions       bool                `json:"inject_usage_options"`
	RetryCount               int                 `json:"retry_count"`
	RouteStrategy            state.RouteStrategy `json:"route_strategy"`
	BlacklistThreshold       int                 `json:"blacklist_threshold"`
	AffinityEnabled          bool                `json:"affinity_enabled"`
	AffinityTTL              int64               `json:"affinity_ttl"`
	AffinityCapacity         int                 `json:"affinity_capacity"`
	ValidationInterval       int64               `json:"validation_interval"`
	RequestLogRetentionDays  int                 `json:"request_log_retention_days"`
	ModelsDevAutoSyncEnabled bool                `json:"models_dev_auto_sync_enabled"`
	ProxyConfig              outboundproxy.View  `json:"proxy_config"`
}

type SettingsResponse struct {
	Revision  uint64                 `json:"-"`
	Values    SettingsValuesResponse `json:"values"`
	Overrides []string               `json:"overrides"`
	ReadOnly  []string               `json:"read_only,omitempty"`
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
	if err := db.WithContext(ctx).
		Order(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "key"}}}}).
		Find(&rows).Error; err != nil {
		return SettingsResponse{}, app_errors.ParseDBError(err)
	}
	return mapSettingsResponse(
		snapshot,
		rows,
		s.modelsDevAutoSyncOverride,
		s.encryption,
		s.environmentProxy,
	)
}

func (s *Service) UpdateSettings(
	ctx context.Context,
	request SettingsUpdateRequest,
) (SettingsResponse, error) {
	updates, err := normalizeSettingUpdates(request, s.encryption)
	if err != nil {
		return SettingsResponse{}, err
	}
	if s.modelsDevAutoSyncOverride != nil && settingRequestContains(request, state.SettingModelsDevAutoSyncEnabled) {
		return SettingsResponse{}, app_errors.ErrValidation
	}

	previousAutoSyncEnabled := false
	snapshot, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		previousAutoSyncEnabled = s.modelsDevAutoSyncEnabled()
		return s.applySettingUpdates(tx, updates)
	}, nil)
	if err != nil {
		return SettingsResponse{}, err
	}
	s.requestCatalogSyncOnEnable(
		settingRequestContains(request, state.SettingModelsDevAutoSyncEnabled),
		previousAutoSyncEnabled,
		snapshot.Settings.ModelsDevAutoSyncEnabled,
	)
	return s.GetSettings(ctx)
}

func (s *Service) requestCatalogSyncOnEnable(requested, before, after bool) {
	if requested && !before && after && s.catalogSync != nil {
		s.catalogSync.RequestImmediateSync()
	}
}

func (s *Service) applySettingUpdates(
	tx *gorm.DB,
	updates []persistedSettingUpdate,
) error {
	for _, update := range updates {
		if update.value == nil {
			if err := tx.Where(&models.SystemSetting{Key: update.key}).
				Delete(&models.SystemSetting{}).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
			continue
		}
		updatedAtMS, err := epochms.FromTime(s.now())
		if err != nil {
			return app_errors.ErrInternalServer
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at_ms"}),
		}).Create(&models.SystemSetting{
			Key:         update.key,
			Value:       *update.value,
			UpdatedAtMS: updatedAtMS,
		}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
	}
	return nil
}

func normalizeSettingUpdates(
	request SettingsUpdateRequest,
	encryptionService encryption.Service,
) ([]persistedSettingUpdate, error) {
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
		if key != outboundproxy.SystemSettingKey && !state.IsRuntimeSettingKey(key) {
			return nil, app_errors.ErrValidation
		}
		raw := bytes.TrimSpace(request.Settings[key])
		if bytes.Equal(raw, []byte("null")) {
			updates = append(updates, persistedSettingUpdate{key: key})
			continue
		}
		if key == outboundproxy.SystemSettingKey {
			config, err := outboundproxy.Decode(string(raw))
			if err != nil || config.Mode == outboundproxy.ModeInherit || encryptionService == nil {
				return nil, app_errors.ErrValidation
			}
			encoded, err := outboundproxy.Encode(config)
			if err != nil {
				return nil, app_errors.ErrValidation
			}
			ciphertext, err := encryptionService.Encrypt(encoded)
			if err != nil {
				return nil, app_errors.ErrInternalServer
			}
			updates = append(updates, persistedSettingUpdate{key: key, value: &ciphertext})
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

func settingRequestContains(request SettingsUpdateRequest, key string) bool {
	_, exists := request.Settings[key]
	return exists
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
	modelsDevAutoSyncOverride *bool,
	encryptionService encryption.Service,
	environmentProxy *outboundproxy.Config,
) (SettingsResponse, error) {
	settings := snapshot.Settings
	set := make(map[string]string, len(settings.HeaderRules.Set))
	for name, value := range settings.HeaderRules.Set {
		set[name] = value
	}
	remove := append([]string{}, settings.HeaderRules.Remove...)
	responseSet := make(map[string]string, len(settings.ResponseHeaderRules.Set))
	for name, value := range settings.ResponseHeaderRules.Set {
		responseSet[name] = value
	}
	responseRemove := append([]string{}, settings.ResponseHeaderRules.Remove...)
	overrides := make([]string, 0, len(rows))
	var configuredProxy *outboundproxy.Config
	for _, row := range rows {
		if state.IsRuntimeSettingKey(row.Key) {
			overrides = append(overrides, row.Key)
		}
		if row.Key == outboundproxy.SystemSettingKey {
			if encryptionService == nil {
				return SettingsResponse{}, app_errors.ErrInternalServer
			}
			plaintext, err := encryptionService.Decrypt(row.Value)
			if err != nil {
				return SettingsResponse{}, app_errors.ErrInternalServer
			}
			config, err := outboundproxy.Decode(plaintext)
			if err != nil || config.Mode == outboundproxy.ModeInherit {
				return SettingsResponse{}, app_errors.ErrInternalServer
			}
			plaintext = ""
			configuredProxy = &config
		}
	}
	effectiveProxy, err := outboundproxy.Resolve(nil, nil, configuredProxy, environmentProxy)
	if err != nil {
		return SettingsResponse{}, app_errors.ErrInternalServer
	}
	proxyView, err := outboundproxy.NewView(configuredProxy, effectiveProxy)
	if err != nil {
		return SettingsResponse{}, app_errors.ErrInternalServer
	}
	readOnly := make([]string, 0)
	modelsDevAutoSyncEnabled := settings.ModelsDevAutoSyncEnabled
	if modelsDevAutoSyncOverride != nil {
		modelsDevAutoSyncEnabled = *modelsDevAutoSyncOverride
		readOnly = append(readOnly, state.SettingModelsDevAutoSyncEnabled)
	}
	return SettingsResponse{
		Revision: snapshot.Revision,
		Values: SettingsValuesResponse{
			FirstByteTimeout:  durationSeconds(settings.FirstByteTimeout),
			RequestTimeout:    durationSeconds(settings.RequestTimeout),
			StreamIdleTimeout: durationSeconds(settings.StreamIdleTimeout),
			HeaderRules: HeaderRulesResponse{
				Set:    set,
				Remove: remove,
			},
			CORS: CORSConfigResponse{
				Enabled:          settings.CORS.Enabled,
				AllowedOrigins:   append([]string{}, settings.CORS.AllowedOrigins...),
				AllowedMethods:   append([]string{}, settings.CORS.AllowedMethods...),
				AllowedHeaders:   append([]string{}, settings.CORS.AllowedHeaders...),
				ExposedHeaders:   append([]string{}, settings.CORS.ExposedHeaders...),
				AllowCredentials: settings.CORS.AllowCredentials,
				MaxAge:           settings.CORS.MaxAgeSeconds,
			},
			ResponseHeaderRules: HeaderRulesResponse{
				Set:    responseSet,
				Remove: responseRemove,
			},
			InjectUsageOptions:       settings.InjectUsageOptions,
			RetryCount:               settings.RetryCount,
			RouteStrategy:            settings.RouteStrategy,
			BlacklistThreshold:       settings.BlacklistThreshold,
			AffinityEnabled:          settings.AffinityEnabled,
			AffinityTTL:              durationSeconds(settings.AffinityTTL),
			AffinityCapacity:         settings.AffinityCapacity,
			ValidationInterval:       durationSeconds(settings.ValidationInterval),
			RequestLogRetentionDays:  settings.RequestLogRetentionDays,
			ModelsDevAutoSyncEnabled: modelsDevAutoSyncEnabled,
			ProxyConfig:              proxyView,
		},
		Overrides: overrides,
		ReadOnly:  readOnly,
	}, nil
}

func durationSeconds(value time.Duration) int64 {
	return int64(value / time.Second)
}

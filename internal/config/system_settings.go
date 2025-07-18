package config

import (
	"context"
	"encoding/json"
	"fmt"
	"gpt-load/internal/db"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"gpt-load/internal/syncer"
	"gpt-load/internal/types"
	"gpt-load/internal/utils"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	"gorm.io/datatypes"
	"gorm.io/gorm/clause"
)

const SettingsUpdateChannel = "system_settings:updated"

// SystemSettingsManager manages system configuration
type SystemSettingsManager struct {
	syncer *syncer.CacheSyncer[types.SystemSettings]
}

// NewSystemSettingsManager creates a new, uninitialized SystemSettingsManager.
func NewSystemSettingsManager() *SystemSettingsManager {
	return &SystemSettingsManager{}
}

type groupManager interface {
	Invalidate() error
}

// Initialize initializes the SystemSettingsManager with database and store dependencies.
func (sm *SystemSettingsManager) Initialize(store store.Store, gm groupManager, isMaster bool) error {
	settingsLoader := func() (types.SystemSettings, error) {
		var dbSettings []models.SystemSetting
		if err := db.DB.Find(&dbSettings).Error; err != nil {
			return types.SystemSettings{}, fmt.Errorf("failed to load system settings from db: %w", err)
		}

		settingsMap := make(map[string]string)
		for _, setting := range dbSettings {
			settingsMap[setting.SettingKey] = setting.SettingValue
		}

		// Start with default settings, then override with values from the database.
		settings := utils.DefaultSystemSettings()
		v := reflect.ValueOf(&settings).Elem()
		t := v.Type()
		jsonToField := make(map[string]string)
		for i := range t.NumField() {
			field := t.Field(i)
			jsonTag := strings.Split(field.Tag.Get("json"), ",")[0]
			if jsonTag != "" {
				jsonToField[jsonTag] = field.Name
			}
		}

		for key, valStr := range settingsMap {
			if fieldName, ok := jsonToField[key]; ok {
				fieldValue := v.FieldByName(fieldName)
				if fieldValue.IsValid() && fieldValue.CanSet() {
					if err := utils.SetFieldFromString(fieldValue, valStr); err != nil {
						logrus.Warnf("Failed to set value from map for field %s: %v", fieldName, err)
					}
				}
			}
		}

		sm.DisplaySystemConfig(settings)

		return settings, nil
	}

	afterLoader := func(newData types.SystemSettings) {
		if !isMaster {
			return
		}
		gm.Invalidate()
	}

	syncer, err := syncer.NewCacheSyncer(
		settingsLoader,
		store,
		SettingsUpdateChannel,
		logrus.WithField("syncer", "system_settings"),
		afterLoader,
	)
	if err != nil {
		return fmt.Errorf("failed to create system settings syncer: %w", err)
	}

	sm.syncer = syncer
	return nil
}

// Stop gracefully stops the SystemSettingsManager's background syncer.
func (sm *SystemSettingsManager) Stop(ctx context.Context) {
	if sm.syncer != nil {
		sm.syncer.Stop()
	}
}

// EnsureSettingsInitialized ensures all system settings records exist in the database.
func (sm *SystemSettingsManager) EnsureSettingsInitialized() error {
	defaultSettings := utils.DefaultSystemSettings()
	metadata := utils.GenerateSettingsMetadata(&defaultSettings)

	for _, meta := range metadata {
		var existing models.SystemSetting
		err := db.DB.Where("setting_key = ?", meta.Key).First(&existing).Error
		if err != nil {
			value := fmt.Sprintf("%v", meta.DefaultValue)
			if meta.Key == "app_url" {
				host := os.Getenv("HOST")
				if host == "" || host == "0.0.0.0" {
					host = "localhost"
				}
				port := os.Getenv("PORT")
				if port == "" {
					port = "3001"
				}
				value = fmt.Sprintf("http://%s:%s", host, port)
			}
			setting := models.SystemSetting{
				SettingKey:   meta.Key,
				SettingValue: value,
				Description:  meta.Description,
			}
			if err := db.DB.Create(&setting).Error; err != nil {
				logrus.Errorf("Failed to initialize setting %s: %v", setting.SettingKey, err)
				return err
			}
			logrus.Infof("Initialized system setting: %s = %s", setting.SettingKey, setting.SettingValue)
		}
	}

	return nil
}

// GetSettings retrieves the current system configuration
func (sm *SystemSettingsManager) GetSettings() types.SystemSettings {
	if sm.syncer == nil {
		logrus.Warn("SystemSettingsManager is not initialized, returning default settings.")
		return utils.DefaultSystemSettings()
	}
	return sm.syncer.Get()
}

// GetAppUrl returns the effective App URL.
func (sm *SystemSettingsManager) GetAppUrl() string {
	settings := sm.GetSettings()
	if settings.AppUrl != "" {
		return settings.AppUrl
	}

	host := os.Getenv("HOST")
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

// UpdateSettings updates the system configuration
func (sm *SystemSettingsManager) UpdateSettings(settingsMap map[string]any) error {
	// Validate configuration items
	if err := sm.ValidateSettings(settingsMap); err != nil {
		return err
	}

	// Update database
	var settingsToUpdate []models.SystemSetting
	for key, value := range settingsMap {
		settingsToUpdate = append(settingsToUpdate, models.SystemSetting{
			SettingKey:   key,
			SettingValue: fmt.Sprintf("%v", value), // Convert any to string
		})
	}

	if len(settingsToUpdate) > 0 {
		if err := db.DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "setting_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"setting_value", "updated_at"}),
		}).Create(&settingsToUpdate).Error; err != nil {
			return fmt.Errorf("failed to update system settings: %w", err)
		}
	}

	// Trigger all instances to reload
	return sm.syncer.Invalidate()
}

// GetEffectiveConfig retrieves effective configuration (system config + group overrides)
func (sm *SystemSettingsManager) GetEffectiveConfig(groupConfigJSON datatypes.JSONMap) types.SystemSettings {
	effectiveConfig := sm.GetSettings()

	if groupConfigJSON == nil {
		return effectiveConfig
	}

	var groupConfig models.GroupConfig
	groupConfigBytes, err := groupConfigJSON.MarshalJSON()
	if err != nil {
		logrus.Warnf("Failed to marshal group config JSON, using system settings only. Error: %v", err)
		return effectiveConfig
	}
	if err := json.Unmarshal(groupConfigBytes, &groupConfig); err != nil {
		logrus.Warnf("Failed to unmarshal group config, using system settings only. Error: %v", err)
		return effectiveConfig
	}

	gcv := reflect.ValueOf(groupConfig)
	ecv := reflect.ValueOf(&effectiveConfig).Elem()

	for i := range gcv.NumField() {
		groupField := gcv.Field(i)
		if groupField.Kind() == reflect.Ptr && !groupField.IsNil() {
			groupFieldValue := groupField.Elem()
			effectiveField := ecv.FieldByName(gcv.Type().Field(i).Name)
			if effectiveField.IsValid() && effectiveField.CanSet() {
				if effectiveField.Type() == groupFieldValue.Type() {
					effectiveField.Set(groupFieldValue)
				}
			}
		}
	}

	return effectiveConfig
}

// ValidateSettings validates the system configuration
func (sm *SystemSettingsManager) ValidateSettings(settingsMap map[string]any) error {
	tempSettings := utils.DefaultSystemSettings()
	v := reflect.ValueOf(&tempSettings).Elem()
	t := v.Type()
	jsonToField := make(map[string]reflect.StructField)
	for i := range t.NumField() {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag != "" {
			jsonToField[jsonTag] = field
		}
	}

	for key, value := range settingsMap {
		field, ok := jsonToField[key]
		if !ok {
			return fmt.Errorf("invalid setting key: %s", key)
		}

		validateTag := field.Tag.Get("validate")

		switch field.Type.Kind() {
		case reflect.Int:
			floatVal, ok := value.(float64)
			if !ok {
				return fmt.Errorf("invalid type for %s: expected a number, got %T", key, value)
			}
			intVal := int(floatVal)
			if floatVal != float64(intVal) {
				return fmt.Errorf("invalid value for %s: must be an integer", key)
			}

			if strings.HasPrefix(validateTag, "min=") {
				minValStr := strings.TrimPrefix(validateTag, "min=")
				minVal, _ := strconv.Atoi(minValStr)
				if intVal < minVal {
					return fmt.Errorf("value for %s (%d) is below minimum value (%d)", key, intVal, minVal)
				}
			}
		case reflect.Bool:
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("invalid type for %s: expected a boolean, got %T", key, value)
			}
		case reflect.String:
			if _, ok := value.(string); !ok {
				return fmt.Errorf("invalid type for %s: expected a string, got %T", key, value)
			}
		default:
			return fmt.Errorf("unsupported type for setting key validation: %s", key)
		}
	}

	return nil
}

// ValidateGroupConfigOverrides validates a map of group-level configuration overrides.
func (sm *SystemSettingsManager) ValidateGroupConfigOverrides(configMap map[string]any) error {
	tempSettings := types.SystemSettings{}
	v := reflect.ValueOf(&tempSettings).Elem()
	t := v.Type()
	jsonToField := make(map[string]reflect.StructField)
	for i := range t.NumField() {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag != "" {
			jsonToField[jsonTag] = field
		}
	}

	for key, value := range configMap {
		if value == nil {
			continue
		}

		field, ok := jsonToField[key]
		if !ok {
			return fmt.Errorf("invalid setting key: %s", key)
		}

		validateTag := field.Tag.Get("validate")

		floatVal, isFloat := value.(float64)
		if !isFloat {
			continue
		}
		intVal := int(floatVal)
		if floatVal != float64(intVal) {
			return fmt.Errorf("invalid value for %s: must be an integer", key)
		}

		if strings.HasPrefix(validateTag, "min=") {
			minValStr := strings.TrimPrefix(validateTag, "min=")
			minVal, _ := strconv.Atoi(minValStr)
			if intVal < minVal {
				return fmt.Errorf("value for %s (%d) is below minimum value (%d)", key, intVal, minVal)
			}
		}
	}

	return nil
}

// DisplaySystemConfig displays the current system settings.
func (sm *SystemSettingsManager) DisplaySystemConfig(settings types.SystemSettings) {
	logrus.Info("")
	logrus.Info("========= System Settings =========")
	logrus.Info("  --- Basic Settings ---")
	logrus.Infof("    App URL: %s", settings.AppUrl)
	logrus.Infof("    Request Log Retention: %d days", settings.RequestLogRetentionDays)
	logrus.Infof("    Request Log Write Interval: %d minutes", settings.RequestLogWriteIntervalMinutes)

	logrus.Info("  --- Request Behavior ---")
	logrus.Infof("    Request Timeout: %d seconds", settings.RequestTimeout)
	logrus.Infof("    Connect Timeout: %d seconds", settings.ConnectTimeout)
	logrus.Infof("    Response Header Timeout: %d seconds", settings.ResponseHeaderTimeout)
	logrus.Infof("    Idle Connection Timeout: %d seconds", settings.IdleConnTimeout)
	logrus.Infof("    Max Idle Connections: %d", settings.MaxIdleConns)
	logrus.Infof("    Max Idle Connections Per Host: %d", settings.MaxIdleConnsPerHost)

	logrus.Info("  --- Key & Group Behavior ---")
	logrus.Infof("    Max Retries: %d", settings.MaxRetries)
	logrus.Infof("    Blacklist Threshold: %d", settings.BlacklistThreshold)
	logrus.Infof("    Key Validation Interval: %d minutes", settings.KeyValidationIntervalMinutes)
	logrus.Info("====================================")
	logrus.Info("")
}

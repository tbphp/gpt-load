package migrations

import (
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

const (
	// ID0008 removes the retired inject_usage_options setting, which is now
	// always enabled and no longer configurable.
	ID0008 = "0008_remove_inject_usage_options"
)

const (
	injectUsageOptionsKey0008 = "inject_usage_options"
	systemSettingTable0008    = "system_settings"
	groupTable0008            = "groups"
)

type systemSetting0008 struct {
	Key string `gorm:"column:key;primaryKey"`
}

func (systemSetting0008) TableName() string { return systemSettingTable0008 }

type group0008 struct {
	ID        uint   `gorm:"column:id;primaryKey"`
	Overrides []byte `gorm:"column:overrides"`
}

func (group0008) TableName() string { return groupTable0008 }

// Up0008 drops the retired global setting row and strips the key from every
// stored Group override object. Without this the resolver rejects the whole
// configuration with "unknown runtime setting".
func Up0008(db *gorm.DB) error {
	if !db.Migrator().HasTable(&systemSetting0008{}) {
		return fmt.Errorf("remove inject usage options: table %q is missing", systemSettingTable0008)
	}
	if err := db.Where(&systemSetting0008{Key: injectUsageOptionsKey0008}).
		Delete(&systemSetting0008{}).Error; err != nil {
		return fmt.Errorf("delete %s system setting: %w", injectUsageOptionsKey0008, err)
	}
	return stripGroupInjectUsageOptions0008(db)
}

func stripGroupInjectUsageOptions0008(db *gorm.DB) error {
	if !db.Migrator().HasTable(&group0008{}) {
		return fmt.Errorf("remove inject usage options: table %q is missing", groupTable0008)
	}
	var groups []group0008
	if err := db.Find(&groups).Error; err != nil {
		return fmt.Errorf("load groups for %s cleanup: %w", injectUsageOptionsKey0008, err)
	}
	for _, group := range groups {
		stripped, changed, err := removeOverrideKey0008(group.Overrides)
		if err != nil {
			return fmt.Errorf("decode overrides for group %d: %w", group.ID, err)
		}
		if !changed {
			continue
		}
		// json 列必须收到文本；写 []byte 时 PostgreSQL 驱动会当成 bytea 而报
		// invalid input syntax for type json。
		if err := db.Model(&group0008{}).
			Where("id = ?", group.ID).
			Update("overrides", string(stripped)).Error; err != nil {
			return fmt.Errorf("update overrides for group %d: %w", group.ID, err)
		}
	}
	return nil
}

func removeOverrideKey0008(raw []byte) ([]byte, bool, error) {
	if len(raw) == 0 {
		return raw, false, nil
	}
	var overrides map[string]json.RawMessage
	if err := json.Unmarshal(raw, &overrides); err != nil {
		// Opaque or malformed payloads are left untouched; the resolver reports
		// them with their own error rather than this migration failing the boot.
		return raw, false, nil
	}
	if _, exists := overrides[injectUsageOptionsKey0008]; !exists {
		return raw, false, nil
	}
	delete(overrides, injectUsageOptionsKey0008)
	encoded, err := json.Marshal(overrides)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

// ValidateRecoverable0008 reports whether the retired key is still persisted.
func ValidateRecoverable0008(db *gorm.DB) error {
	if !db.Migrator().HasTable(&systemSetting0008{}) {
		return fmt.Errorf(
			"validate recoverable inject usage options: table %q is missing",
			systemSettingTable0008,
		)
	}
	var settingCount int64
	if err := db.Model(&systemSetting0008{}).
		Where(&systemSetting0008{Key: injectUsageOptionsKey0008}).
		Count(&settingCount).Error; err != nil {
		return fmt.Errorf("count %s system setting: %w", injectUsageOptionsKey0008, err)
	}
	if settingCount > 0 {
		return fmt.Errorf("system setting %q is still present", injectUsageOptionsKey0008)
	}
	return validateGroupOverrides0008(db)
}

func validateGroupOverrides0008(db *gorm.DB) error {
	if !db.Migrator().HasTable(&group0008{}) {
		return fmt.Errorf(
			"validate recoverable inject usage options: table %q is missing",
			groupTable0008,
		)
	}
	var groups []group0008
	if err := db.Find(&groups).Error; err != nil {
		return fmt.Errorf("load groups for %s validation: %w", injectUsageOptionsKey0008, err)
	}
	for _, group := range groups {
		_, changed, err := removeOverrideKey0008(group.Overrides)
		if err != nil {
			return fmt.Errorf("decode overrides for group %d: %w", group.ID, err)
		}
		if changed {
			return fmt.Errorf("group %d still overrides %q", group.ID, injectUsageOptionsKey0008)
		}
	}
	return nil
}

// Validate0008 verifies the retired setting is gone everywhere it was stored.
func Validate0008(db *gorm.DB) error {
	return ValidateRecoverable0008(db)
}

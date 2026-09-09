package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0012 = "0012_access_key_mask_prefix"

// Up0012 为脱敏前缀保存独立元数据；既有系统生成密钥保留 sk-gl-，列表无需解密。
func Up0012(db *gorm.DB) error {
	if err := ValidateRecoverable0012(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn("access_keys", "key_prefix") {
		if err := db.Exec("ALTER TABLE access_keys ADD COLUMN key_prefix VARCHAR(6) NOT NULL DEFAULT 'sk-gl-' CONSTRAINT chk_access_key_prefix CHECK (length(key_prefix) IN (0,6))").Error; err != nil {
			return fmt.Errorf("add access key mask prefix: %w", err)
		}
	}
	return Validate0012(db)
}

// ValidateRecoverable0012 接受原子加列前后的状态，以支持 MySQL 的 DDL 中断恢复。
func ValidateRecoverable0012(db *gorm.DB) error {
	if !db.Migrator().HasTable("access_keys") {
		return fmt.Errorf("access key mask table is missing")
	}
	if !db.Migrator().HasColumn("access_keys", "key_prefix") {
		return nil
	}
	return validateAccessKeyPrefix0012(db)
}

func Validate0012(db *gorm.DB) error {
	if !db.Migrator().HasColumn("access_keys", "key_prefix") {
		return fmt.Errorf("access key mask prefix is missing")
	}
	return validateAccessKeyPrefix0012(db)
}

func validateAccessKeyPrefix0012(db *gorm.DB) error {
	if !db.Migrator().HasConstraint("access_keys", "chk_access_key_prefix") {
		return fmt.Errorf("access key mask prefix constraint is missing")
	}
	columns, err := db.Migrator().ColumnTypes("access_keys")
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "key_prefix" {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "char") {
			return fmt.Errorf("access key mask prefix must be varchar")
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("access key mask prefix must not be nullable")
		}
		if length, known := column.Length(); known && length != 6 {
			return fmt.Errorf("access key mask prefix length must be six")
		}
		value, known := column.DefaultValue()
		if !known || value != "sk-gl-" && !strings.HasPrefix(value, "'sk-gl-'") {
			return fmt.Errorf("access key mask prefix default is invalid")
		}
		return nil
	}
	return fmt.Errorf("access key mask prefix is missing")
}

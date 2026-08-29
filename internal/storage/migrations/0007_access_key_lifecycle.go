package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0007 = "0007_access_key_lifecycle"

type accessKeyLifecycle0007 struct {
	ExpiresAtMS *int64 `gorm:"column:expires_at_ms"`
}

func (accessKeyLifecycle0007) TableName() string { return "access_keys" }

// Up0007 adds the optional AccessKey expiry without changing existing rows.
func Up0007(db *gorm.DB) error {
	model := &accessKeyLifecycle0007{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("add access key lifecycle: table %q is missing", model.TableName())
	}
	if db.Migrator().HasColumn(model, "expires_at_ms") {
		return nil
	}
	if err := db.Migrator().AddColumn(model, "ExpiresAtMS"); err != nil {
		return fmt.Errorf("add access_keys.expires_at_ms: %w", err)
	}
	return nil
}

// ValidateRecoverable0007 accepts either side of the idempotent column addition.
func ValidateRecoverable0007(db *gorm.DB) error {
	model := &accessKeyLifecycle0007{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("validate recoverable access key lifecycle: table %q is missing", model.TableName())
	}
	if !db.Migrator().HasColumn(model, "expires_at_ms") {
		return nil
	}
	return validateAccessKeyExpiryColumn0007(db)
}

// Validate0007 verifies the nullable AccessKey expiry column.
func Validate0007(db *gorm.DB) error {
	if err := ValidateRecoverable0007(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&accessKeyLifecycle0007{}, "expires_at_ms") {
		return fmt.Errorf("validate access key lifecycle: column %q is missing", "access_keys.expires_at_ms")
	}
	return validateAccessKeyExpiryColumn0007(db)
}

func validateAccessKeyExpiryColumn0007(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes("access_keys")
	if err != nil {
		return fmt.Errorf("inspect access_keys.expires_at_ms: %w", err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), "expires_at_ms") {
			continue
		}
		typeName := strings.ToLower(column.DatabaseTypeName())
		if !strings.Contains(typeName, "int") {
			return fmt.Errorf("validate access key lifecycle: column %q is not integer", "access_keys.expires_at_ms")
		}
		if nullable, known := column.Nullable(); known && !nullable {
			return fmt.Errorf("validate access key lifecycle: column %q is not nullable", "access_keys.expires_at_ms")
		}
		return nil
	}
	return fmt.Errorf("validate access key lifecycle: column %q is missing", "access_keys.expires_at_ms")
}

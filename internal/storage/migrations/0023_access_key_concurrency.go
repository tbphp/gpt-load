package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ID0023 = "0023_access_key_concurrency"

// Up0023 原子增加可空上限；NULL 表示继承默认值。
func Up0023(db *gorm.DB) error {
	if err := ValidateRecoverable0023(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn("access_keys", "concurrency_limit") {
		if err := db.Exec("ALTER TABLE ? ADD COLUMN concurrency_limit BIGINT NULL CONSTRAINT chk_access_key_concurrency CHECK (concurrency_limit >= 0 AND concurrency_limit <= 9007199254740991)", clause.Table{Name: "access_keys"}).Error; err != nil {
			return fmt.Errorf("add access key concurrency: %w", err)
		}
	}
	return Validate0023(db)
}
func ValidateRecoverable0023(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported concurrency migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable("access_keys") {
		return fmt.Errorf("access_keys table is missing")
	}
	if db.Migrator().HasColumn("access_keys", "concurrency_limit") {
		return Validate0023(db)
	}
	return nil
}
func Validate0023(db *gorm.DB) error {
	if !db.Migrator().HasConstraint("access_keys", "chk_access_key_concurrency") {
		return fmt.Errorf("concurrency constraint is missing")
	}
	columns, err := db.Migrator().ColumnTypes("access_keys")
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "concurrency_limit" {
			continue
		}
		kind := strings.ToLower(column.DatabaseTypeName())
		if !strings.Contains(kind, "int") {
			return fmt.Errorf("concurrency limit must be integer")
		}
		if nullable, known := column.Nullable(); !known || !nullable {
			return fmt.Errorf("concurrency limit must be nullable")
		}
		var invalid int64
		if err := db.Table("access_keys").Where("concurrency_limit < 0 OR concurrency_limit > 9007199254740991").Count(&invalid).Error; err != nil {
			return err
		}
		if invalid != 0 {
			return fmt.Errorf("invalid persisted concurrency limits")
		}
		return nil
	}
	return fmt.Errorf("concurrency limit is missing")
}

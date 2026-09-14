package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0014 = "0014_affinity_kind"

// Up0014 原子增加命中来源，旧日志保留空值，避免推测历史亲和类型。
func Up0014(db *gorm.DB) error {
	if err := ValidateRecoverable0014(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn("request_logs", "affinity_kind") {
		if err := db.Exec("ALTER TABLE request_logs ADD COLUMN affinity_kind VARCHAR(32) NOT NULL DEFAULT ''").Error; err != nil {
			return fmt.Errorf("add affinity kind: %w", err)
		}
	}
	return Validate0014(db)
}
func ValidateRecoverable0014(db *gorm.DB) error {
	if !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("request_logs table is missing")
	}
	if db.Migrator().HasColumn("request_logs", "affinity_kind") {
		return Validate0014(db)
	}
	return nil
}
func Validate0014(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes("request_logs")
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "affinity_kind" {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "char") {
			return fmt.Errorf("affinity kind must be varchar")
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("affinity kind must be non-null")
		}
		if length, known := column.Length(); known && length != 32 {
			return fmt.Errorf("affinity kind length must be 32")
		}
		value, known := column.DefaultValue()
		if !known || (value != "" && value != "''" && value != "''::character varying") {
			return fmt.Errorf("affinity kind must default to empty")
		}
		return nil
	}
	return fmt.Errorf("affinity kind is missing")
}

package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0013 = "0013_validation_protocol"

// Up0013 使用原子加列，NULL 保留既有分组的默认协议选择。
func Up0013(db *gorm.DB) error {
	if err := ValidateRecoverable0013(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn("groups", "validation_protocol") {
		table := `"groups"`
		if db.Dialector.Name() == "mysql" {
			table = "`groups`"
		}
		if err := db.Exec("ALTER TABLE " + table + " ADD COLUMN validation_protocol VARCHAR(32) NULL").Error; err != nil {
			return fmt.Errorf("add validation protocol: %w", err)
		}
	}
	return Validate0013(db)
}

func ValidateRecoverable0013(db *gorm.DB) error {
	if !db.Migrator().HasTable("groups") {
		return fmt.Errorf("groups table is missing")
	}
	if db.Migrator().HasColumn("groups", "validation_protocol") {
		return Validate0013(db)
	}
	return nil
}

func Validate0013(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes("groups")
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "validation_protocol" {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "char") {
			return fmt.Errorf("validation protocol must be varchar")
		}
		if nullable, known := column.Nullable(); !known || !nullable {
			return fmt.Errorf("validation protocol must be nullable")
		}
		if length, known := column.Length(); known && length != 32 {
			return fmt.Errorf("validation protocol length must be 32")
		}
		return nil
	}
	return fmt.Errorf("validation protocol is missing")
}

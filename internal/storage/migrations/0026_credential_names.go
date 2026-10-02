package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0026 = "0026_credential_names"

// Up0026 单独保存展示别名，既有凭据默认为空，不改写凭据内容或身份。
func Up0026(db *gorm.DB) error {
	if err := ValidateRecoverable0026(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn("credentials", "name") {
		if err := db.Exec("ALTER TABLE credentials ADD COLUMN name VARCHAR(255) NOT NULL DEFAULT ''").Error; err != nil {
			return fmt.Errorf("add credential name: %w", err)
		}
	}
	return Validate0026(db)
}

func ValidateRecoverable0026(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported credential name migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable("credentials") {
		return fmt.Errorf("credentials table is missing")
	}
	if !db.Migrator().HasColumn("credentials", "name") {
		return nil
	}
	return Validate0026(db)
}

func Validate0026(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes("credentials")
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "name" {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "char") {
			return fmt.Errorf("credential name must be varchar")
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("credential name must not be nullable")
		}
		if length, known := column.Length(); known && length != 255 {
			return fmt.Errorf("credential name length must be 255")
		}
		value, known := column.DefaultValue()
		if !known || value != "" && value != "''" && !strings.HasPrefix(value, "''::") {
			return fmt.Errorf("credential name default must be empty")
		}
		return nil
	}
	return fmt.Errorf("credential name is missing")
}

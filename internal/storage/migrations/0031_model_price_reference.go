package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0031 = "0031_model_price_reference"

type modelPrice0031 struct {
	ReferenceProviderID string `gorm:"type:varchar(246);not null;default:''"`
	ReferenceModelID    string `gorm:"type:varchar(255);not null;default:''"`
}

func (modelPrice0031) TableName() string { return "model_prices" }

// Up0031 为目录关联保存准确来源，既有价格和维护归属保持不变。
func Up0031(db *gorm.DB) error {
	if err := ValidateRecoverable0031(db); err != nil {
		return err
	}
	for _, field := range []string{"ReferenceProviderID", "ReferenceModelID"} {
		if !db.Migrator().HasColumn(&modelPrice0031{}, field) {
			if err := db.Migrator().AddColumn(&modelPrice0031{}, field); err != nil {
				return fmt.Errorf("add model price %s: %w", field, err)
			}
		}
	}
	return Validate0031(db)
}

func ValidateRecoverable0031(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported model price reference migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable(&modelPrice0031{}) {
		return fmt.Errorf("model_prices table is missing")
	}
	return validateReferenceColumns0031(db, false)
}

func Validate0031(db *gorm.DB) error {
	return validateReferenceColumns0031(db, true)
}

func validateReferenceColumns0031(db *gorm.DB, required bool) error {
	columns, err := db.Migrator().ColumnTypes(&modelPrice0031{})
	if err != nil {
		return err
	}
	wanted := map[string]int64{"reference_provider_id": 246, "reference_model_id": 255}
	for _, column := range columns {
		length, exists := wanted[column.Name()]
		if !exists {
			continue
		}
		kind := strings.ToLower(column.DatabaseTypeName())
		if !strings.Contains(kind, "char") && kind != "text" {
			return fmt.Errorf("model price %s must be text", column.Name())
		}
		if actual, known := column.Length(); known && actual < length {
			return fmt.Errorf("model price %s is too short", column.Name())
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("model price %s must not be nullable", column.Name())
		}
		value, known := column.DefaultValue()
		if !known || strings.Trim(strings.SplitN(value, "::", 2)[0], "'\"() ") != "" {
			return fmt.Errorf("model price %s must default to empty", column.Name())
		}
		delete(wanted, column.Name())
	}
	if required && len(wanted) != 0 {
		return fmt.Errorf("model price reference columns are missing")
	}
	return nil
}

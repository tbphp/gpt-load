package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0032 = "0032_credential_priority"

type credential0032 struct {
	Priority int32 `gorm:"type:integer;not null;default:0"`
}

func (credential0032) TableName() string { return "credentials" }

// Up0032 既有凭据统一使用 0，保留原有组内同档调度行为。
func Up0032(db *gorm.DB) error {
	if err := ValidateRecoverable0032(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&credential0032{}, "priority") {
		if err := db.Migrator().AddColumn(&credential0032{}, "Priority"); err != nil {
			return fmt.Errorf("add credential priority: %w", err)
		}
	}
	return Validate0032(db)
}

func ValidateRecoverable0032(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported credential priority migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable(&credential0032{}) {
		return fmt.Errorf("credentials table is missing")
	}
	if db.Migrator().HasColumn(&credential0032{}, "priority") {
		return Validate0032(db)
	}
	return nil
}

func Validate0032(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes(&credential0032{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "priority" {
			continue
		}
		switch strings.ToLower(column.DatabaseTypeName()) {
		case "int", "integer", "int4":
		default:
			return fmt.Errorf("credential priority must be an integer")
		}
		if kind, known := column.ColumnType(); known && strings.Contains(strings.ToLower(kind), "unsigned") {
			return fmt.Errorf("credential priority must be signed")
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("credential priority must not be nullable")
		}
		value, known := column.DefaultValue()
		if !known || strings.Trim(strings.SplitN(value, "::", 2)[0], "'\"() ") != "0" {
			return fmt.Errorf("credential priority default must be zero")
		}
		return nil
	}
	return fmt.Errorf("credential priority is missing")
}

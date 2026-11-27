package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0032 = "0032_credential_quota_reserve"

type credential0032 struct {
	QuotaReservePercent int `gorm:"type:integer;not null;default:0"`
}

func (credential0032) TableName() string { return "credentials" }

func Up0032(db *gorm.DB) error {
	if err := ValidateRecoverable0032(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&credential0032{}, "quota_reserve_percent") {
		if err := db.Migrator().AddColumn(&credential0032{}, "QuotaReservePercent"); err != nil {
			return fmt.Errorf("add credential quota reserve: %w", err)
		}
	}
	return Validate0032(db)
}

func ValidateRecoverable0032(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported quota reserve migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable(&credential0032{}) {
		return fmt.Errorf("credentials table is missing")
	}
	if db.Migrator().HasColumn(&credential0032{}, "quota_reserve_percent") {
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
		if column.Name() != "quota_reserve_percent" {
			continue
		}
		switch strings.ToLower(column.DatabaseTypeName()) {
		case "int", "integer", "int4":
		default:
			return fmt.Errorf("credential quota reserve must be an integer")
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("credential quota reserve must not be nullable")
		}
		value, known := column.DefaultValue()
		if !known || strings.Trim(strings.SplitN(value, "::", 2)[0], "'\"() ") != "0" {
			return fmt.Errorf("credential quota reserve default must be zero")
		}
		return nil
	}
	return fmt.Errorf("credential quota reserve is missing")
}

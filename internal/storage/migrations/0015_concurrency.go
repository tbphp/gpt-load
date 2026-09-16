package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0015 = "0015_concurrency"

type concurrencyPolicy0015 struct {
	Subject        string `gorm:"type:varchar(255);primaryKey;not null"`
	MaxConcurrency int64  `gorm:"type:bigint;not null;check:chk_concurrency_limit,max_concurrency >= 0 AND max_concurrency <= 1000000"`
}

func (concurrencyPolicy0015) TableName() string { return "concurrency_policies" }

func Up0015(db *gorm.DB) error {
	if err := ValidateRecoverable0015(db); err != nil {
		return err
	}
	if !db.Migrator().HasTable(&concurrencyPolicy0015{}) {
		if err := db.Migrator().CreateTable(&concurrencyPolicy0015{}); err != nil {
			return err
		}
	}
	return Validate0015(db)
}

func ValidateRecoverable0015(db *gorm.DB) error {
	if !db.Migrator().HasTable(&concurrencyPolicy0015{}) {
		return nil
	}
	return Validate0015(db)
}

func Validate0015(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes(&concurrencyPolicy0015{})
	if err != nil {
		return err
	}
	found := make(map[string]bool)
	for _, column := range columns {
		name := column.Name()
		if name != "subject" && name != "max_concurrency" {
			continue
		}
		found[name] = true
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("concurrency %s must be non-null", name)
		}
		kind := strings.ToLower(column.DatabaseTypeName())
		if name == "subject" {
			if primary, known := column.PrimaryKey(); known && !primary {
				return fmt.Errorf("concurrency subject must be primary key")
			}
			if !strings.Contains(kind, "char") {
				return fmt.Errorf("concurrency subject must be varchar")
			}
		} else if !strings.Contains(kind, "int") {
			return fmt.Errorf("concurrency limit must be integer")
		}
	}
	if len(found) != 2 || !db.Migrator().HasConstraint(&concurrencyPolicy0015{}, "chk_concurrency_limit") {
		return fmt.Errorf("concurrency policy schema is incomplete")
	}
	return nil
}

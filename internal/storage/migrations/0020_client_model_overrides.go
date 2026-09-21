package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const ID0020 = "0020_client_model_overrides"

type clientModelOverride0020 struct {
	ModelHash   string              `gorm:"type:varchar(64);primaryKey;not null"`
	ClientModel clientModelText0020 `gorm:"not null"`
	Overrides   clientModelText0020 `gorm:"not null"`
}

func (clientModelOverride0020) TableName() string { return "client_model_overrides" }

type clientModelText0020 string

func (clientModelText0020) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return "longtext"
	}
	return "text"
}

func Up0020(db *gorm.DB) error {
	if db.Migrator().HasTable(&clientModelOverride0020{}) {
		return Validate0020(db)
	}
	switch strings.ToLower(db.Dialector.Name()) {
	case "mysql", "postgres", "sqlite":
	default:
		return fmt.Errorf("client model overrides: unsupported database driver %q", db.Dialector.Name())
	}
	if err := db.Migrator().CreateTable(&clientModelOverride0020{}); err != nil {
		return fmt.Errorf("create client model overrides: %w", err)
	}
	return Validate0020(db)
}

func ValidateRecoverable0020(db *gorm.DB) error {
	if !db.Migrator().HasTable(&clientModelOverride0020{}) {
		return nil
	}
	return Validate0020(db)
}

func Validate0020(db *gorm.DB) error {
	model := &clientModelOverride0020{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("client model overrides table is missing")
	}
	columns, err := db.Migrator().ColumnTypes(model)
	if err != nil {
		return err
	}
	if len(columns) != 3 {
		return fmt.Errorf("client model overrides has unexpected columns")
	}
	found := make(map[string]bool, 3)
	for _, column := range columns {
		name := column.Name()
		found[name] = true
		kind := strings.ToLower(column.DatabaseTypeName())
		switch name {
		case "model_hash":
			if !strings.Contains(kind, "char") {
				return fmt.Errorf("client model hash must be varchar")
			}
			if length, known := column.Length(); known && length != 64 {
				return fmt.Errorf("client model hash must have length 64")
			}
			if primary, known := column.PrimaryKey(); known && !primary {
				return fmt.Errorf("client model hash must be primary key")
			}
		case "client_model", "overrides":
			if !strings.Contains(kind, "text") {
				return fmt.Errorf("client model override column %s must be text", name)
			}
			if primary, known := column.PrimaryKey(); known && primary {
				return fmt.Errorf("client model override column %s must not be primary key", name)
			}
		default:
			return fmt.Errorf("client model overrides has unexpected column %q", name)
		}
		if nullable, known := column.Nullable(); known && nullable {
			return fmt.Errorf("client model override column %s must be non-null", name)
		}
	}
	for _, name := range []string{"model_hash", "client_model", "overrides"} {
		if !found[name] {
			return fmt.Errorf("client model override column %s is missing", name)
		}
	}
	return nil
}

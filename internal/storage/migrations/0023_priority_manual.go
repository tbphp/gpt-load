package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0023 = "0023_priority_manual"

type groupPriorityManual0023 struct {
	PriorityManual *int `gorm:"column:priority_manual"`
}

func (groupPriorityManual0023) TableName() string { return "groups" }

type credentialPriorityManual0023 struct {
	PriorityManual *int `gorm:"column:priority_manual"`
}

func (credentialPriorityManual0023) TableName() string { return "credentials" }

// Up0023 adds nullable priority_manual columns without changing existing rows.
func Up0023(db *gorm.DB) error {
	for _, definition := range []struct {
		model any
		name  string
	}{
		{model: &groupPriorityManual0023{}, name: "groups"},
		{model: &credentialPriorityManual0023{}, name: "credentials"},
	} {
		if db.Migrator().HasColumn(definition.model, "priority_manual") {
			continue
		}
		if err := db.Migrator().AddColumn(definition.model, "PriorityManual"); err != nil {
			return fmt.Errorf("add %s.priority_manual: %w", definition.name, err)
		}
	}
	return nil
}

func ValidateRecoverable0023(db *gorm.DB) error {
	for _, definition := range []struct {
		model any
		table string
	}{
		{model: &groupPriorityManual0023{}, table: "groups"},
		{model: &credentialPriorityManual0023{}, table: "credentials"},
	} {
		if !db.Migrator().HasTable(definition.model) {
			return fmt.Errorf("validate recoverable priority manual: table %q is missing", definition.table)
		}
		if db.Migrator().HasColumn(definition.model, "priority_manual") {
			if err := validatePriorityManualColumn0023(db, definition.table); err != nil {
				return err
			}
		}
	}
	return nil
}

func Validate0023(db *gorm.DB) error {
	for _, table := range []string{"groups", "credentials"} {
		if !db.Migrator().HasColumn(table, "priority_manual") {
			return fmt.Errorf("validate priority manual: column %q.priority_manual is missing", table)
		}
		if err := validatePriorityManualColumn0023(db, table); err != nil {
			return err
		}
	}
	return nil
}

func validatePriorityManualColumn0023(db *gorm.DB, table string) error {
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return fmt.Errorf("inspect %s.priority_manual: %w", table, err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), "priority_manual") {
			continue
		}
		typeName := strings.ToLower(column.DatabaseTypeName())
		if !strings.Contains(typeName, "int") {
			return fmt.Errorf("validate priority manual: column %q.priority_manual is not integer", table)
		}
		if nullable, known := column.Nullable(); known && !nullable {
			return fmt.Errorf("validate priority manual: column %q.priority_manual is not nullable", table)
		}
		return nil
	}
	return fmt.Errorf("validate priority manual: column %q.priority_manual is missing", table)
}

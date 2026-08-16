package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0007 = "0007_model_price_mode_schedules"

const (
	modelPricesTable         = "model_prices"
	modePriceSchedulesColumn = "mode_price_schedules"
)

// Up0007 adds the persisted non-standard mode schedules used by the pricing
// runtime. Existing rows intentionally start without a mode schedule.
func Up0007(db *gorm.DB) error {
	state, err := inspectModePriceSchedulesState0007(db, true)
	if err != nil {
		return err
	}
	if state.hasColumn {
		return nil
	}
	if err := db.Exec(fmt.Sprintf(
		"ALTER TABLE %s ADD COLUMN %s JSON NULL",
		quoteIdentifier0007(db, modelPricesTable),
		quoteIdentifier0007(db, modePriceSchedulesColumn),
	)).Error; err != nil {
		return fmt.Errorf("add model price mode schedules: %w", err)
	}
	return validateModePriceSchedulesColumn0007(db)
}

func ValidateRecoverable0007(db *gorm.DB) error {
	_, err := inspectModePriceSchedulesState0007(db, true)
	return err
}

func Validate0007(db *gorm.DB) error {
	state, err := inspectModePriceSchedulesState0007(db, false)
	if err != nil {
		return err
	}
	if !state.hasColumn {
		return fmt.Errorf("validate model price mode schedules: column %q is missing", modePriceSchedulesColumn)
	}
	return validateModePriceSchedulesColumn0007(db)
}

type modePriceSchedulesState0007 struct {
	hasColumn bool
}

func inspectModePriceSchedulesState0007(db *gorm.DB, requireRecoverable bool) (modePriceSchedulesState0007, error) {
	if db == nil || db.Dialector == nil {
		return modePriceSchedulesState0007{}, fmt.Errorf("inspect model price mode schedules: database is unavailable")
	}
	if !db.Migrator().HasTable(modelPricesTable) {
		return modePriceSchedulesState0007{}, fmt.Errorf("inspect model price mode schedules: table %q is missing", modelPricesTable)
	}
	state := modePriceSchedulesState0007{
		hasColumn: db.Migrator().HasColumn(modelPricesTable, modePriceSchedulesColumn),
	}
	if !state.hasColumn {
		return state, nil
	}
	if err := validateModePriceSchedulesColumn0007(db); err != nil {
		return state, err
	}
	if requireRecoverable {
		var count int64
		if err := db.Table(modelPricesTable).
			Where(modePriceSchedulesColumn + " IS NOT NULL").
			Count(&count).Error; err != nil {
			return state, fmt.Errorf("inspect model price mode schedule values: %w", err)
		}
		if count != 0 {
			return state, fmt.Errorf("recover model price mode schedules: %d non-null value(s) exist", count)
		}
	}
	return state, nil
}

func validateModePriceSchedulesColumn0007(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes(modelPricesTable)
	if err != nil {
		return fmt.Errorf("inspect model price mode schedules column: %w", err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), modePriceSchedulesColumn) {
			continue
		}
		databaseType := strings.ToLower(strings.TrimSpace(column.DatabaseTypeName()))
		if !strings.Contains(databaseType, "json") {
			return fmt.Errorf("validate model price mode schedules column: type %q is not JSON", column.DatabaseTypeName())
		}
		nullable, known := column.Nullable()
		if !known || !nullable {
			return fmt.Errorf("validate model price mode schedules column: nullable is %t (known=%t), want true", nullable, known)
		}
		return nil
	}
	return fmt.Errorf("validate model price mode schedules column: column %q is missing", modePriceSchedulesColumn)
}

func quoteIdentifier0007(db *gorm.DB, value string) string {
	if db != nil && db.Dialector != nil && strings.EqualFold(db.Dialector.Name(), "mysql") {
		return "`" + value + "`"
	}
	return `"` + value + `"`
}

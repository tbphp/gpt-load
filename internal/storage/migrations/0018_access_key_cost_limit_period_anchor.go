package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0018 = "0018_access_key_cost_limit_period_anchor"

const (
	accessKeyCostLimitRuleTable0018 = "access_key_cost_limit_rules"
	periodAnchorConstraint0018      = "chk_ak_cost_rule_anchor"
)

func Up0018(db *gorm.DB) error {
	if err := ValidateRecoverable0018(db); err != nil {
		return err
	}
	isSQLite := strings.EqualFold(db.Dialector.Name(), "sqlite")
	if !db.Migrator().HasColumn(accessKeyCostLimitRuleTable0018, "period_anchor") {
		definition := "VARCHAR(16) NOT NULL DEFAULT 'first_request'"
		if isSQLite {
			definition += " CONSTRAINT " + periodAnchorConstraint0018 +
				" CHECK (period_anchor IN ('first_request','calendar_day'))"
		}
		if err := db.Exec("ALTER TABLE " + accessKeyCostLimitRuleTable0018 +
			" ADD COLUMN period_anchor " + definition).Error; err != nil {
			return fmt.Errorf("add access key cost limit period anchor: %w", err)
		}
	}
	if !db.Migrator().HasColumn(accessKeyCostLimitRuleTable0018, "period_timezone") {
		if err := db.Exec("ALTER TABLE " + accessKeyCostLimitRuleTable0018 +
			" ADD COLUMN period_timezone VARCHAR(64) NOT NULL DEFAULT ''").Error; err != nil {
			return fmt.Errorf("add access key cost limit period timezone: %w", err)
		}
	}
	if !isSQLite && !db.Migrator().HasConstraint(accessKeyCostLimitRuleTable0018, periodAnchorConstraint0018) {
		if err := db.Exec("ALTER TABLE " + accessKeyCostLimitRuleTable0018 +
			" ADD CONSTRAINT " + periodAnchorConstraint0018 +
			" CHECK (period_anchor IN ('first_request','calendar_day'))").Error; err != nil {
			return fmt.Errorf("add access key cost limit period anchor constraint: %w", err)
		}
	}
	return Validate0018(db)
}

func ValidateRecoverable0018(db *gorm.DB) error {
	if !db.Migrator().HasTable(accessKeyCostLimitRuleTable0018) {
		return fmt.Errorf("access key cost limit period anchor: rules table is missing")
	}
	if db.Migrator().HasColumn(accessKeyCostLimitRuleTable0018, "period_anchor") {
		if err := validatePeriodAnchorColumn0018(db, "period_anchor", 16, "first_request"); err != nil {
			return err
		}
	}
	if db.Migrator().HasColumn(accessKeyCostLimitRuleTable0018, "period_timezone") {
		if err := validatePeriodAnchorColumn0018(db, "period_timezone", 64, ""); err != nil {
			return err
		}
	}
	return nil
}

func Validate0018(db *gorm.DB) error {
	if err := ValidateRecoverable0018(db); err != nil {
		return err
	}
	for _, column := range []string{"period_anchor", "period_timezone"} {
		if !db.Migrator().HasColumn(accessKeyCostLimitRuleTable0018, column) {
			return fmt.Errorf("access key cost limit period anchor: %s is missing", column)
		}
	}
	if !db.Migrator().HasConstraint(accessKeyCostLimitRuleTable0018, periodAnchorConstraint0018) {
		return fmt.Errorf("access key cost limit period anchor constraint is missing")
	}
	return nil
}

func validatePeriodAnchorColumn0018(db *gorm.DB, name string, expectedLength int64, expectedDefault string) error {
	columns, err := db.Migrator().ColumnTypes(accessKeyCostLimitRuleTable0018)
	if err != nil {
		return err
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), name) {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "char") &&
			!strings.Contains(strings.ToLower(column.DatabaseTypeName()), "text") {
			return fmt.Errorf("access key cost limit period anchor: %s must be text", name)
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("access key cost limit period anchor: %s must be non-null", name)
		}
		if length, known := column.Length(); known && length != expectedLength {
			return fmt.Errorf("access key cost limit period anchor: %s length must be %d", name, expectedLength)
		}
		value, known := column.DefaultValue()
		if !known || normalizePeriodAnchorDefault0018(value) != expectedDefault {
			return fmt.Errorf("access key cost limit period anchor: %s has an invalid default %q", name, value)
		}
		return nil
	}
	return fmt.Errorf("access key cost limit period anchor: %s is missing", name)
}

func normalizePeriodAnchorDefault0018(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.Index(strings.ToUpper(value), " CONSTRAINT "); index >= 0 {
		value = value[:index]
	}
	if index := strings.Index(value, "::"); index >= 0 {
		value = value[:index]
	}
	value = strings.Trim(value, "()'\"")
	return value
}

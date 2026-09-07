package migrations

import (
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

const ID0009 = "0009_price_multipliers"

var priceMultiplierTables0009 = []struct{ table, constraint string }{
	{"groups", "chk_group_price_multiplier"},
	{"access_keys", "chk_access_key_price_multiplier"},
}

// Up0009 用独立且原子的列 DDL 保持 MySQL 中断后的安全恢复。
func Up0009(db *gorm.DB) error {
	if err := ValidateRecoverable0009(db); err != nil {
		return err
	}
	for _, definition := range priceMultiplierTables0009 {
		if db.Migrator().HasColumn(definition.table, "price_multiplier_micros") {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN price_multiplier_micros BIGINT NOT NULL DEFAULT 1000000 CONSTRAINT %s CHECK (price_multiplier_micros >= 0 AND price_multiplier_micros <= 1000000000)", quotePriceMultiplierIdentifier0009(db, definition.table), quotePriceMultiplierIdentifier0009(db, definition.constraint))
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("add %s.price_multiplier_micros: %w", definition.table, err)
		}
	}
	return Validate0009(db)
}

func ValidateRecoverable0009(db *gorm.DB) error {
	for _, definition := range priceMultiplierTables0009 {
		if !db.Migrator().HasTable(definition.table) {
			return fmt.Errorf("price multiplier table %q is missing", definition.table)
		}
		if db.Migrator().HasColumn(definition.table, "price_multiplier_micros") {
			if err := validatePriceMultiplierColumn0009(db, definition.table, definition.constraint); err != nil {
				return err
			}
		}
	}
	return nil
}

func Validate0009(db *gorm.DB) error {
	for _, definition := range priceMultiplierTables0009 {
		if !db.Migrator().HasColumn(definition.table, "price_multiplier_micros") {
			return fmt.Errorf("%s.price_multiplier_micros is missing", definition.table)
		}
		if err := validatePriceMultiplierColumn0009(db, definition.table, definition.constraint); err != nil {
			return err
		}
	}
	return nil
}

func validatePriceMultiplierColumn0009(db *gorm.DB, table, constraint string) error {
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return fmt.Errorf("inspect %s price multiplier: %w", table, err)
	}
	found := false
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), "price_multiplier_micros") {
			continue
		}
		found = true
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("%s price multiplier is not integer", table)
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("%s price multiplier is nullable", table)
		}
		defaultValue, known := column.DefaultValue()
		if strings.EqualFold(db.Dialector.Name(), "sqlite") {
			// SQLite 驱动解析列尾约束时可能把 CHECK 也并入默认值，读取数据库元数据。
			if err := db.Raw("SELECT dflt_value FROM pragma_table_info(?) WHERE name = ?", table, "price_multiplier_micros").Scan(&defaultValue).Error; err != nil {
				return fmt.Errorf("inspect %s price multiplier default: %w", table, err)
			}
			known = defaultValue != ""
		}
		defaultValue = strings.Trim(strings.Split(defaultValue, "::")[0], "()' \"")
		value, parseErr := strconv.ParseInt(defaultValue, 10, 64)
		if !known || parseErr != nil || value != 1_000_000 {
			return fmt.Errorf("%s price multiplier has invalid default %q (known %t)", table, defaultValue, known)
		}
	}
	if !found {
		return fmt.Errorf("%s price multiplier is missing", table)
	}
	if !db.Migrator().HasConstraint(table, constraint) {
		return fmt.Errorf("%s price multiplier constraint is missing", table)
	}
	var definition string
	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite":
		err = db.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&definition).Error
	case "mysql":
		err = db.Raw("SELECT CHECK_CLAUSE FROM information_schema.check_constraints WHERE constraint_schema = DATABASE() AND constraint_name = ?", constraint).Scan(&definition).Error
	case "postgres", "postgresql":
		err = db.Raw("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = ? AND conrelid = ?::regclass", constraint, table).Scan(&definition).Error
	default:
		return fmt.Errorf("unsupported price multiplier migration driver %q", db.Dialector.Name())
	}
	if err != nil {
		return fmt.Errorf("inspect %s price multiplier constraint: %w", table, err)
	}
	normalized := strings.NewReplacer(" ", "", "\n", "", "\t", "", "(", "", ")", "", "`", "", `"`, "", "::bigint", "").Replace(strings.ToLower(definition))
	if !strings.Contains(normalized, "price_multiplier_micros>=0andprice_multiplier_micros<=1000000000") {
		return fmt.Errorf("%s price multiplier constraint has invalid bounds", table)
	}
	var invalid int64
	if err := db.Table(table).Where("price_multiplier_micros IS NULL OR price_multiplier_micros < 0 OR price_multiplier_micros > 1000000000").Count(&invalid).Error; err != nil {
		return fmt.Errorf("validate %s price multiplier values: %w", table, err)
	}
	if invalid != 0 {
		return fmt.Errorf("%s contains invalid price multipliers", table)
	}
	return nil
}

func quotePriceMultiplierIdentifier0009(db *gorm.DB, value string) string {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return "`" + value + "`"
	}
	return `"` + value + `"`
}

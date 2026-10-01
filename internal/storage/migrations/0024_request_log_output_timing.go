package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0024 = "0024_request_log_output_timing"

// Up0024 仅增加可空观测列；历史日志没有有效输出时点，不推测或回填。
func Up0024(db *gorm.DB) error {
	if err := ValidateRecoverable0024(db); err != nil {
		return err
	}
	for _, name := range []string{"first_output", "last_output"} {
		if !db.Migrator().HasColumn("request_logs", name+"_ms") {
			if err := db.Exec("ALTER TABLE request_logs ADD COLUMN " + name + "_ms BIGINT NULL CONSTRAINT chk_request_log_" + name + " CHECK (" + name + "_ms >= 0)").Error; err != nil {
				return fmt.Errorf("add request log %s: %w", name, err)
			}
		}
	}
	return Validate0024(db)
}

func ValidateRecoverable0024(db *gorm.DB) error {
	return validateOutputTiming0024(db, false)
}

func Validate0024(db *gorm.DB) error {
	return validateOutputTiming0024(db, true)
}

func validateOutputTiming0024(db *gorm.DB, complete bool) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported output timing migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("request_logs table is missing")
	}
	columns, err := db.Migrator().ColumnTypes("request_logs")
	if err != nil {
		return err
	}
	for _, name := range []string{"first_output", "last_output"} {
		found := false
		for _, column := range columns {
			if column.Name() != name+"_ms" {
				continue
			}
			found = true
			if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
				return fmt.Errorf("%s_ms must be integer", name)
			}
			if nullable, known := column.Nullable(); !known || !nullable {
				return fmt.Errorf("%s_ms must be nullable", name)
			}
			if !db.Migrator().HasConstraint("request_logs", "chk_request_log_"+name) {
				return fmt.Errorf("%s constraint is missing", name)
			}
		}
		if complete && !found {
			return fmt.Errorf("%s_ms is missing", name)
		}
	}
	return nil
}

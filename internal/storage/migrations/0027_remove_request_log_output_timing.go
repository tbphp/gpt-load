package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const ID0027 = "0027_remove_request_log_output_timing"

// Up0027 原地删列，避免 SQLite 重建父表时级联删除请求尝试等关联记录。
func Up0027(db *gorm.DB) error {
	if err := ValidateRecoverable0027(db); err != nil {
		return err
	}
	for _, name := range []string{"first_output", "last_output"} {
		if !db.Migrator().HasColumn("request_logs", name+"_ms") {
			continue
		}
		statement := "ALTER TABLE request_logs DROP COLUMN " + name + "_ms"
		if db.Dialector.Name() == "mysql" {
			// MySQL 8 的单条 ALTER 原子删除约束及其列，中断只可能发生在两列之间。
			statement = "ALTER TABLE request_logs DROP CHECK chk_request_log_" + name + ", DROP COLUMN " + name + "_ms"
		}
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("remove request log %s: %w", name, err)
		}
	}
	return Validate0027(db)
}

func ValidateRecoverable0027(db *gorm.DB) error {
	if err := ValidateRecoverable0024(db); err != nil {
		return err
	}
	first := db.Migrator().HasColumn("request_logs", "first_output_ms")
	last := db.Migrator().HasColumn("request_logs", "last_output_ms")
	if first && !last {
		return fmt.Errorf("request log output timing columns were removed out of order")
	}
	for _, name := range []string{"first_output", "last_output"} {
		if !db.Migrator().HasColumn("request_logs", name+"_ms") && db.Migrator().HasConstraint("request_logs", "chk_request_log_"+name) {
			return fmt.Errorf("orphaned request log %s constraint", name)
		}
	}
	for _, name := range []string{"duration_ms", "first_response_ms", "output_tokens"} {
		if !db.Migrator().HasColumn("request_logs", name) {
			return fmt.Errorf("request log %s is missing", name)
		}
	}
	return nil
}

func Validate0027(db *gorm.DB) error {
	if err := ValidateRecoverable0027(db); err != nil {
		return err
	}
	for _, name := range []string{"first_output_ms", "last_output_ms"} {
		if db.Migrator().HasColumn("request_logs", name) {
			return fmt.Errorf("obsolete request log %s remains", name)
		}
	}
	return nil
}

// ValidateCurrent0024 仅凭已登记的清理迁移接受缺列；没有升级证据仍执行冻结的原校验。
func ValidateCurrent0024(db *gorm.DB) error {
	var ids []string
	if err := db.Table("schema_migrations").Where("id IN ?", []string{ID0027, ID0027 + "#building"}).Pluck("id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if id == ID0027 {
			return Validate0027(db)
		}
		if id == ID0027+"#building" && db.Dialector.Name() == "mysql" {
			return ValidateRecoverable0027(db)
		}
	}
	return Validate0024(db)
}

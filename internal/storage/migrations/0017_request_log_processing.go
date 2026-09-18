package migrations

import (
	"fmt"
	"strings"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const ID0017 = "0017_request_log_processing"

const requestLogStatusExpression0017 = "status IN ('processing','success','error','incomplete','canceled')"

type requestLogProcessing0017 struct {
	StartedAtMS int64  `gorm:"column:started_at_ms;not null;check:chk_request_log_started_at,started_at_ms >= 0;index:idx_request_logs_started_id,priority:1,sort:desc"`
	ID          string `gorm:"column:id;index:idx_request_logs_started_id,priority:2,sort:desc"`
}

func (requestLogProcessing0017) TableName() string { return "request_logs" }

func Up0017(db *gorm.DB) error {
	if err := ValidateRecoverable0017(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn("request_logs", "started_at_ms") {
		if err := db.Exec("ALTER TABLE request_logs ADD COLUMN started_at_ms BIGINT NOT NULL DEFAULT 0").Error; err != nil {
			return fmt.Errorf("add request log start time: %w", err)
		}
	}
	if err := db.Exec("UPDATE request_logs SET started_at_ms = completed_at_ms WHERE started_at_ms = 0").Error; err != nil {
		return fmt.Errorf("backfill request log start time: %w", err)
	}
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		if err := rebuildSQLiteRequestLogs0017(db); err != nil {
			return err
		}
	} else {
		if db.Migrator().HasConstraint("request_logs", "chk_request_log_status") {
			// MySQL 8.0.16–8.0.18 accepts DROP CHECK but rejects the otherwise
			// portable DROP CONSTRAINT spelling. Replace the check atomically so
			// an interrupted migration cannot leave the old status contract gone.
			drop := "DROP CONSTRAINT"
			if dialector, ok := db.Dialector.(*gormmysql.Dialector); ok &&
				dialector.Config != nil && mysqlRequiresCheckDropSyntax0003(dialector.ServerVersion) {
				drop = "DROP CHECK"
			}
			if err := db.Exec("ALTER TABLE request_logs " + drop +
				" chk_request_log_status, ADD CONSTRAINT chk_request_log_status CHECK (" +
				requestLogStatusExpression0017 + ")").Error; err != nil {
				return fmt.Errorf("replace request log status constraint: %w", err)
			}
		} else {
			if err := db.Exec("ALTER TABLE request_logs ADD CONSTRAINT chk_request_log_status CHECK (" + requestLogStatusExpression0017 + ")").Error; err != nil {
				return fmt.Errorf("add request log status constraint: %w", err)
			}
		}
	}
	if !db.Migrator().HasIndex(&requestLogProcessing0017{}, "idx_request_logs_started_id") {
		if err := db.Migrator().CreateIndex(&requestLogProcessing0017{}, "idx_request_logs_started_id"); err != nil {
			return fmt.Errorf("create request log start index: %w", err)
		}
	}
	return Validate0017(db)
}

func ValidateRecoverable0017(db *gorm.DB) error {
	if !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("request log processing: request_logs table is missing")
	}
	return nil
}

func Validate0017(db *gorm.DB) error {
	if err := ValidateRecoverable0017(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn("request_logs", "started_at_ms") {
		return fmt.Errorf("request log processing: started_at_ms is missing")
	}
	if !db.Migrator().HasConstraint("request_logs", "chk_request_log_status") {
		return fmt.Errorf("request log processing: status constraint is missing")
	}
	if !db.Migrator().HasIndex(&requestLogProcessing0017{}, "idx_request_logs_started_id") {
		return fmt.Errorf("request log processing: start index is missing")
	}
	return nil
}

func rebuildSQLiteRequestLogs0017(db *gorm.DB) error {
	var ddl string
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'request_logs'").Scan(&ddl).Error; err != nil {
		return fmt.Errorf("inspect request log table: %w", err)
	}
	start := strings.Index(ddl, "(")
	if start < 0 {
		return fmt.Errorf("request log table definition is invalid")
	}
	old := "status IN ('success','error','incomplete','canceled')"
	if !strings.Contains(ddl, old) {
		if strings.Contains(ddl, "status IN ('processing','success','error','incomplete','canceled')") {
			return nil
		}
		return fmt.Errorf("request log status constraint definition is invalid")
	}
	newDDL := "CREATE TABLE request_logs__0017 " + strings.Replace(ddl[start:], old, requestLogStatusExpression0017, 1)
	var objects []string
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE tbl_name = 'request_logs' AND type IN ('index','trigger') AND sql IS NOT NULL ORDER BY type, name").Scan(&objects).Error; err != nil {
		return fmt.Errorf("inspect request log indexes: %w", err)
	}
	columns, err := db.Migrator().ColumnTypes("request_logs")
	if err != nil {
		return fmt.Errorf("inspect request log columns: %w", err)
	}
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		if strings.ContainsAny(column.Name(), "\"`\x00") {
			return fmt.Errorf("request log column name is invalid")
		}
		names = append(names, `"`+column.Name()+`"`)
	}
	projection := strings.Join(names, ",")
	statements := []string{
		newDDL,
		"INSERT INTO request_logs__0017 (" + projection + ") SELECT " + projection + " FROM request_logs",
		"DROP TABLE request_logs",
		"ALTER TABLE request_logs__0017 RENAME TO request_logs",
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("rebuild SQLite request logs: %w", err)
		}
	}
	for _, object := range objects {
		if err := db.Exec(object).Error; err != nil {
			return fmt.Errorf("restore request log index: %w", err)
		}
	}
	return nil
}

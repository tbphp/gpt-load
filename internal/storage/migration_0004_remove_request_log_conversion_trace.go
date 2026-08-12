package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const (
	requestLogConversionTraceColumn        = "client_parameters_json"
	requestLogAttemptConversionTraceColumn = "conversion_trace_json"
)

func removeRequestLogConversionTrace(db *gorm.DB) error {
	if db == nil || db.Dialector == nil {
		return fmt.Errorf("remove request log conversion trace: database is nil")
	}
	if !strings.EqualFold(db.Dialector.Name(), "sqlite") {
		return nil
	}
	if db.Migrator().HasColumn("request_log_attempts", requestLogAttemptConversionTraceColumn) {
		if err := db.Exec(
			`ALTER TABLE "request_log_attempts" DROP COLUMN "conversion_trace_json"`,
		).Error; err != nil {
			return fmt.Errorf("remove request log attempt conversion trace: %w", err)
		}
	}
	if db.Migrator().HasColumn("request_logs", requestLogConversionTraceColumn) {
		if err := db.Exec(
			`ALTER TABLE "request_logs" DROP COLUMN "client_parameters_json"`,
		).Error; err != nil {
			return fmt.Errorf("remove request log client parameter projection: %w", err)
		}
	}
	return nil
}

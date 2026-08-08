package storage

import (
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

// addRequestLogModelConsistencyColumns preserves existing logs while enabling
// new requests to persist the upstream-declared response model. Historical
// successful modeled requests become unknown because they were not observed at
// the time; failed and model-less requests remain not applicable.
func addRequestLogModelConsistencyColumns(db *gorm.DB) error {
	const constraint = "chk_request_log_model_consistency"
	for _, field := range []string{"UpstreamReportedModel", "ModelConsistency"} {
		if db.Migrator().HasColumn(&models.RequestLog{}, field) {
			continue
		}
		var err error
		if field == "ModelConsistency" && db.Dialector.Name() == "sqlite" {
			err = db.Exec(`ALTER TABLE request_logs ADD COLUMN model_consistency varchar(32) NOT NULL DEFAULT 'not_applicable' CONSTRAINT "chk_request_log_model_consistency" CHECK (model_consistency IN ('not_applicable','match','unknown','mismatch'))`).Error
		} else {
			err = db.Migrator().AddColumn(&models.RequestLog{}, field)
		}
		if err != nil {
			return fmt.Errorf("add request log model consistency column %s: %w", field, err)
		}
	}

	if err := db.Model(&models.RequestLog{}).
		Where(
			"status = ? AND upstream_model <> ?",
			string(telemetry.RequestStatusSuccess),
			"",
		).
		Update("model_consistency", string(telemetry.ModelConsistencyUnknown)).Error; err != nil {
		return fmt.Errorf("backfill request log model consistency: %w", err)
	}
	if !db.Migrator().HasConstraint(&models.RequestLog{}, constraint) {
		if db.Dialector.Name() == "sqlite" {
			return fmt.Errorf("request log model consistency constraint is missing after column migration")
		}
		if err := db.Migrator().CreateConstraint(&models.RequestLog{}, constraint); err != nil {
			return fmt.Errorf("create request log model consistency constraint: %w", err)
		}
	}
	return nil
}

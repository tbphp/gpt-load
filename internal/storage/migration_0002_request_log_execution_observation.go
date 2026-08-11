package storage

import "gorm.io/gorm"

// Migration-local models freeze the additive request-log observation schema.
type requestLogExecutionObservationMigration struct {
	Operation string `gorm:"type:varchar(64);not null;default:''"`
}

func (requestLogExecutionObservationMigration) TableName() string { return "request_logs" }

type requestLogAttemptExecutionObservationMigration struct {
	UpstreamAPI           string `gorm:"type:varchar(64);not null;default:''"`
	ReasoningMode         string `gorm:"type:varchar(64);not null;default:''"`
	ReasoningEffort       string `gorm:"type:varchar(64);not null;default:''"`
	ReasoningBudgetTokens *int64 `gorm:"column:reasoning_budget_tokens"`
}

func (requestLogAttemptExecutionObservationMigration) TableName() string {
	return "request_log_attempts"
}

func addRequestLogExecutionObservation(db *gorm.DB) error {
	request := &requestLogExecutionObservationMigration{}
	if err := addMigrationColumns(db, request, "Operation"); err != nil {
		return err
	}
	attempt := &requestLogAttemptExecutionObservationMigration{}
	return addMigrationColumns(
		db,
		attempt,
		"UpstreamAPI",
		"ReasoningMode",
		"ReasoningEffort",
		"ReasoningBudgetTokens",
	)
}

func addMigrationColumns(db *gorm.DB, model any, fields ...string) error {
	for _, field := range fields {
		if db.Migrator().HasColumn(model, field) {
			continue
		}
		if err := db.Migrator().AddColumn(model, field); err != nil {
			return err
		}
	}
	return nil
}

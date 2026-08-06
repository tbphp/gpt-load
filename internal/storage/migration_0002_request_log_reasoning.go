package storage

import (
	"fmt"

	"gorm.io/gorm"
)

func addRequestLogReasoningColumns(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE request_logs
			ADD COLUMN reasoning_mode varchar(64) NOT NULL DEFAULT ''`,
		`ALTER TABLE request_logs
			ADD COLUMN reasoning_effort varchar(64) NOT NULL DEFAULT ''`,
		`ALTER TABLE request_logs
			ADD COLUMN reasoning_budget_tokens integer`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("add request log reasoning column: %w", err)
		}
	}
	return nil
}

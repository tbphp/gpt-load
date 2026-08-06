package storage

import (
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

// addRequestLogReasoningColumns upgrades databases that were created by the
// original SQLite-only 0001 migration. Fresh schemas already contain these
// fields because the current model is the single source of schema metadata;
// HasColumn makes this migration idempotent for both paths.
func addRequestLogReasoningColumns(db *gorm.DB) error {
	fields := []string{"ReasoningMode", "ReasoningEffort", "ReasoningBudgetTokens"}
	for _, field := range fields {
		if db.Migrator().HasColumn(&models.RequestLog{}, field) {
			continue
		}
		if err := db.Migrator().AddColumn(&models.RequestLog{}, field); err != nil {
			return fmt.Errorf("add request log reasoning column %s: %w", field, err)
		}
	}
	return nil
}

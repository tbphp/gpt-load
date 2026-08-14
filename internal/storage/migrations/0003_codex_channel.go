package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const ID0003 = "0003_codex_channel"

// Up0003 gives the already implemented Codex subscription path its own
// product channel without changing API-key OpenAI groups or historical logs.
func Up0003(db *gorm.DB) error {
	for _, table := range []string{"groups", "credential_stages"} {
		if err := db.Table(table).
			Where("channel_id = ? AND connection_type = ?", "openai", "subscription").
			Update("channel_id", "codex").Error; err != nil {
			return fmt.Errorf("migrate %s subscription channel: %w", table, err)
		}
	}
	return nil
}

func ValidateRecoverable0003(db *gorm.DB) error {
	for _, table := range []string{"groups", "credential_stages"} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("required subscription table %q is missing", table)
		}
		if !db.Migrator().HasColumn(table, "channel_id") || !db.Migrator().HasColumn(table, "connection_type") {
			return fmt.Errorf("required subscription columns on %q are missing", table)
		}
	}
	return nil
}

func Validate0003(db *gorm.DB) error {
	if err := ValidateRecoverable0003(db); err != nil {
		return err
	}
	for _, table := range []string{"groups", "credential_stages"} {
		var count int64
		if err := db.Table(table).
			Where("channel_id = ? AND connection_type = ?", "openai", "subscription").
			Count(&count).Error; err != nil {
			return fmt.Errorf("validate %s subscription channel: %w", table, err)
		}
		if count != 0 {
			return fmt.Errorf("validate %s subscription channel: %d legacy row(s) remain", table, count)
		}
	}
	return nil
}

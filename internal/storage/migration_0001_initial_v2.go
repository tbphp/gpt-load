package storage

import (
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

// createInitialV2Tables builds the final pre-release v2 baseline from the same
// GORM models used by the application. The baseline intentionally accepts only
// an empty database; future schema changes must append a new migration.
func createInitialV2Tables(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&models.Group{},
		&models.Credential{},
		&models.AccessKey{},
		&models.RequestLog{},
		&models.RequestLogAttempt{},
		&models.UsageAggregationJournal{},
		&models.UsageStat{},
		&models.ModelPrice{},
		&models.SystemSetting{},
		&models.Job{},
		&models.ControlOperation{},
	); err != nil {
		return fmt.Errorf("create initial v2 schema: %w", err)
	}
	if db.Dialector.Name() == "mysql" {
		// MySQL's database default is commonly case-insensitive. Model IDs are
		// wire identities, so preserve exact casing within the composite key.
		if err := db.Exec(
			"ALTER TABLE model_prices MODIFY model_id varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL",
		).Error; err != nil {
			return fmt.Errorf("configure exact MySQL model price identity: %w", err)
		}
	}
	return nil
}

// validateMigrationForeignKeys performs the existing SQLite integrity check.
// MySQL and PostgreSQL enforce the same foreign-key definitions during normal
// writes; their catalogs do not expose SQLite's PRAGMA interface.
func validateMigrationForeignKeys(db *gorm.DB) error {
	if db == nil || db.Dialector == nil || db.Dialector.Name() != "sqlite" {
		return nil
	}
	var violations []struct {
		Table string
		RowID int64 `gorm:"column:rowid"`
	}
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		return fmt.Errorf("validate migration foreign keys: %w", err)
	}
	if len(violations) != 0 {
		return fmt.Errorf("validate migration foreign keys: %d violation(s)", len(violations))
	}
	return nil
}

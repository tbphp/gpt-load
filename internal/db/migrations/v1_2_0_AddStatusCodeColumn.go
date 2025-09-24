package db

import (
	"fmt"
	"gpt-load/internal/models"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// V1_2_0_AddStatusCodeColumn adds status_code column to api_keys table
func V1_2_0_AddStatusCodeColumn(db *gorm.DB) error {
	// Prefer GORM migrator for portability
	if db.Migrator().HasColumn(&models.APIKey{}, "status_code") {
		logrus.Info("status_code column already exists, skipping migration")
		return nil
	}
	// Will honor gorm:"default:0" tag on models.APIKey.StatusCode
	if err := db.Migrator().AddColumn(&models.APIKey{}, "StatusCode"); err != nil {
		return fmt.Errorf("failed to add status_code column: %w", err)
	}
	logrus.Info("Successfully added status_code column to api_keys table")
	return nil
}

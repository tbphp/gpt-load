package db

import (
	"gpt-load/internal/models"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// V1_2_0_AddAllowedModelsColumn adds allowed_models column to api_keys table
func V1_2_0_AddAllowedModelsColumn(db *gorm.DB) error {
	migrator := db.Migrator()
	if migrator.HasColumn(&models.APIKey{}, "allowed_models") {
		logrus.Info("api_keys.allowed_models column already exists, skipping v1.2.0...")
		return nil
	}

	if err := migrator.AddColumn(&models.APIKey{}, "allowed_models"); err != nil {
		return err
	}

	logrus.Info("Migration v1.2.0 completed: added allowed_models column")
	return nil
}
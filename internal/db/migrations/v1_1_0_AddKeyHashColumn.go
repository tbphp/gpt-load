package db

import (
	"fmt"
	"gpt-load/internal/encryption"
	"gpt-load/internal/models"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// V1_1_0_AddKeyHashColumn adds key_hash column to api_keys and request_logs tables
func V1_1_0_AddKeyHashColumn(db *gorm.DB) error {
	// First check if there are any records need migration
	var needMigrateCount int64
	db.Model(&models.APIKey{}).
		Where("key_hash IS NULL OR key_hash = ''").
		Count(&needMigrateCount)

	if needMigrateCount == 0 {
		logrus.Info("No api_keys need migration, skipping v1.1.0...")
		return nil
	}

	logrus.Infof("Found %d api_keys need to populate key_hash", needMigrateCount)

	encSvc, err := encryption.NewService("")
	if err != nil {
		return fmt.Errorf("failed to initialize encryption service: %w", err)
	}

	// Process in batches to avoid memory issues
	const batchSize = 1000

	for {
		var apiKeys []models.APIKey
		// Only query records that need migration
		result := db.Where("key_hash IS NULL OR key_hash = ''").
			Limit(batchSize).
			Find(&apiKeys)

		if result.Error != nil {
			return fmt.Errorf("failed to fetch api_keys: %w", result.Error)
		}

		if len(apiKeys) == 0 {
			break
		}

		// Update each key's hash
		for _, key := range apiKeys {
			// Generate hash
			keyHash := encSvc.Hash(key.KeyValue)

			// Update the record
			if err := db.Model(&models.APIKey{}).
				Where("id = ?", key.ID).
				Update("key_hash", keyHash).Error; err != nil {
				logrus.WithError(err).Errorf("Failed to update key_hash for api_key ID %d", key.ID)
			}
		}
	}

	logrus.Info("Migration v1.1.0 completed successfully")
	return nil
}

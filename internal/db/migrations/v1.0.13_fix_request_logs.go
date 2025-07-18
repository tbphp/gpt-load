package db

import (
	"gpt-load/internal/models"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// TODO: Update migration, remove after multiple versions when all old versions have been upgraded.
func V1_0_13_FixRequestLogs(db *gorm.DB) error {
	// If key_id exists, execute the fix
	if !db.Migrator().HasColumn(&models.RequestLog{}, "key_id") {
		return nil
	}

	logrus.Info("Old schema detected. Starting data migration for request_logs...")

	if !db.Migrator().HasColumn(&models.RequestLog{}, "group_name") {
		logrus.Info("Adding 'group_name' column to request_logs table...")
		if err := db.Migrator().AddColumn(&models.RequestLog{}, "group_name"); err != nil {
			return err // Column addition is critical
		}
	}
	if !db.Migrator().HasColumn(&models.RequestLog{}, "key_value") {
		logrus.Info("Adding 'key_value' column to request_logs table...")
		if err := db.Migrator().AddColumn(&models.RequestLog{}, "key_value"); err != nil {
			return err // Column addition is critical
		}
	}

	type OldRequestLog struct {
		ID      string
		KeyID   uint `gorm:"column:key_id"`
		GroupID uint
	}

	batchSize := 1000
	for i := 0; ; i++ {
		logrus.Infof("Processing batch %d...", i+1)
		var oldLogs []OldRequestLog

		result := db.Model(&models.RequestLog{}).
			Select("id", "key_id", "group_id").
			Where("key_value IS NULL OR group_name IS NULL").
			Limit(batchSize).
			Find(&oldLogs)

		if result.Error != nil {
			logrus.WithError(result.Error).Error("Failed to fetch batch of logs. Skipping to next batch.")
			continue
		}

		if len(oldLogs) == 0 {
			logrus.Info("All batches processed.")
			break
		}

		keyIDMap := make(map[uint]bool)
		groupIDMap := make(map[uint]bool)
		for _, logEntry := range oldLogs {
			if logEntry.KeyID > 0 {
				keyIDMap[logEntry.KeyID] = true
			}
			if logEntry.GroupID > 0 {
				groupIDMap[logEntry.GroupID] = true
			}
		}

		var apiKeys []models.APIKey
		if len(keyIDMap) > 0 {
			var keyIDs []uint
			for id := range keyIDMap {
				keyIDs = append(keyIDs, id)
			}
			if err := db.Model(&models.APIKey{}).Where("id IN ?", keyIDs).Find(&apiKeys).Error; err != nil {
				logrus.WithError(err).Warn("Failed to fetch API keys for the current batch. Some logs may not be updated.")
			}
		}
		keyValueMapping := make(map[uint]string)
		for _, key := range apiKeys {
			keyValueMapping[key.ID] = key.KeyValue
		}

		var groups []models.Group
		if len(groupIDMap) > 0 {
			var groupIDs []uint
			for id := range groupIDMap {
				groupIDs = append(groupIDs, id)
			}
			if err := db.Model(&models.Group{}).Where("id IN ?", groupIDs).Find(&groups).Error; err != nil {
				logrus.WithError(err).Warn("Failed to fetch groups for the current batch. Some logs may not be updated.")
			}
		}
		groupNameMapping := make(map[uint]string)
		for _, group := range groups {
			groupNameMapping[group.ID] = group.Name
		}

		updateGroups := make(map[string]map[string][]string)

		for _, logEntry := range oldLogs {
			groupName, gExists := groupNameMapping[logEntry.GroupID]
			if !gExists {
				logrus.Warnf("Log ID %s: Could not find Group for group_id %d. Setting group_name to empty string.", logEntry.ID, logEntry.GroupID)
			}

			keyValue, kExists := keyValueMapping[logEntry.KeyID]
			if !kExists {
				logrus.Warnf("Log ID %s: Could not find APIKey for key_id %d. Setting key_value to empty string.", logEntry.ID, logEntry.KeyID)
			}

			if _, ok := updateGroups[groupName]; !ok {
				updateGroups[groupName] = make(map[string][]string)
			}
			updateGroups[groupName][keyValue] = append(updateGroups[groupName][keyValue], logEntry.ID)
		}

		for groupName, keyMap := range updateGroups {
			for keyValue, ids := range keyMap {
				updates := map[string]any{
					"group_name": groupName,
					"key_value":  keyValue,
				}
				if err := db.Model(&models.RequestLog{}).Where("id IN ?", ids).UpdateColumns(updates).Error; err != nil {
					logrus.WithError(err).Errorf("Failed to update a batch of log entries. Skipping this batch.")
				}
			}
		}
		logrus.Infof("Finished processing batch %d. Updated %d log entries.", i+1, len(oldLogs))
	}

	logrus.Info("Data migration complete. Dropping 'key_id' column from request_logs table...")
	if err := db.Migrator().DropColumn(&models.RequestLog{}, "key_id"); err != nil {
		logrus.WithError(err).Warn("Failed to drop 'key_id' column. This can be done manually.")
	}

	logrus.Info("Database migration finished!")
	return nil
}

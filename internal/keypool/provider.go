package keypool

import (
	"encoding/json"
	"errors"
	"fmt"
	"gpt-load/internal/config"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type KeyProvider struct {
	db              *gorm.DB
	store           store.Store
	settingsManager *config.SystemSettingsManager
}

func NewProvider(db *gorm.DB, store store.Store, settingsManager *config.SystemSettingsManager) *KeyProvider {
	return &KeyProvider{
		db:              db,
		store:           store,
		settingsManager: settingsManager,
	}
}

// SelectKey atomically selects and rotates an available APIKey for the specified group.
func (p *KeyProvider) SelectKey(groupID uint, upstreamID ...string) (*models.APIKey, error) {
	// Determine the upstream filter to use
	targetUpstreamID := "Default"
	if len(upstreamID) > 0 && upstreamID[0] != "" {
		targetUpstreamID = upstreamID[0]
	}

	// Try to select key from the specified upstream filter list
	specificListKey := fmt.Sprintf("group:%d:upstream:%s:active_keys", groupID, targetUpstreamID)
	keyIDStr, err := p.store.Rotate(specificListKey)

	// If no key in the specified upstream filter list, try the Default list
	if err != nil && errors.Is(err, store.ErrNotFound) && targetUpstreamID != "Default" {
		defaultListKey := fmt.Sprintf("group:%d:upstream:Default:active_keys", groupID)
		keyIDStr, err = p.store.Rotate(defaultListKey)
	}

	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, app_errors.ErrNoActiveKeys
		}
		return nil, fmt.Errorf("failed to rotate key from store: %w", err)
	}

	keyID, err := strconv.ParseUint(keyIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse key ID '%s': %w", keyIDStr, err)
	}

	// Get key details from HASH
	keyHashKey := fmt.Sprintf("key:%d", keyID)
	keyDetails, err := p.store.HGetAll(keyHashKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get key details for key ID %d: %w", keyID, err)
	}

	// Manually unmarshal the map into an APIKey struct
	failureCount, _ := strconv.ParseInt(keyDetails["failure_count"], 10, 64)
	createdAt, _ := strconv.ParseInt(keyDetails["created_at"], 10, 64)
	upstreamFilter := keyDetails["upstream_filter"]
	if upstreamFilter == "" {
		upstreamFilter = "Default"
	}

	apiKey := &models.APIKey{
		ID:             uint(keyID),
		KeyValue:       keyDetails["key_string"],
		Status:         keyDetails["status"],
		UpstreamFilter: upstreamFilter,
		FailureCount:   failureCount,
		GroupID:        groupID,
		CreatedAt:      time.Unix(createdAt, 0),
	}

	return apiKey, nil
}

// UpdateStatus 异步地提交一个 Key 状态更新任务。
func (p *KeyProvider) UpdateStatus(apiKey *models.APIKey, group *models.Group, isSuccess bool) {
	go func() {
		keyHashKey := fmt.Sprintf("key:%d", apiKey.ID)

		upstreamFilter := apiKey.UpstreamFilter
		if upstreamFilter == "" {
			upstreamFilter = "Default"
		}
		activeKeysListKey := fmt.Sprintf("group:%d:upstream:%s:active_keys", group.ID, upstreamFilter)

		if isSuccess {
			if err := p.handleSuccess(apiKey.ID, keyHashKey, activeKeysListKey); err != nil {
				logrus.WithFields(logrus.Fields{"keyID": apiKey.ID, "error": err}).Error("Failed to handle key success")
			}
		} else {
			if err := p.handleFailure(apiKey, group, keyHashKey, activeKeysListKey); err != nil {
				logrus.WithFields(logrus.Fields{"keyID": apiKey.ID, "error": err}).Error("Failed to handle key failure")
			}
		}
	}()
}

func (p *KeyProvider) handleSuccess(keyID uint, keyHashKey, activeKeysListKey string) error {
	keyDetails, err := p.store.HGetAll(keyHashKey)
	if err != nil {
		return fmt.Errorf("failed to get key details from store: %w", err)
	}

	failureCount, _ := strconv.ParseInt(keyDetails["failure_count"], 10, 64)
	isActive := keyDetails["status"] == models.KeyStatusActive

	if failureCount == 0 && isActive {
		return nil
	}

	return p.db.Transaction(func(tx *gorm.DB) error {
		var key models.APIKey
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&key, keyID).Error; err != nil {
			return fmt.Errorf("failed to lock key %d for update: %w", keyID, err)
		}

		updates := map[string]any{"failure_count": 0}
		if !isActive {
			updates["status"] = models.KeyStatusActive
		}

		if err := tx.Model(&key).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update key in DB: %w", err)
		}

		if err := p.store.HSet(keyHashKey, updates); err != nil {
			return fmt.Errorf("failed to update key details in store: %w", err)
		}

		if !isActive {
			logrus.WithField("keyID", keyID).Debug("Key has recovered and is being restored to active pool.")
			if err := p.store.LRem(activeKeysListKey, 0, keyID); err != nil {
				return fmt.Errorf("failed to LRem key before LPush on recovery: %w", err)
			}
			if err := p.store.LPush(activeKeysListKey, keyID); err != nil {
				return fmt.Errorf("failed to LPush key back to active list: %w", err)
			}
		}

		return nil
	})
}

func (p *KeyProvider) handleFailure(apiKey *models.APIKey, group *models.Group, keyHashKey, activeKeysListKey string) error {
	keyDetails, err := p.store.HGetAll(keyHashKey)
	if err != nil {
		return fmt.Errorf("failed to get key details from store: %w", err)
	}

	if keyDetails["status"] == models.KeyStatusInvalid {
		return nil
	}

	failureCount, _ := strconv.ParseInt(keyDetails["failure_count"], 10, 64)

	// 获取该分组的有效配置
	blacklistThreshold := group.EffectiveConfig.BlacklistThreshold

	return p.db.Transaction(func(tx *gorm.DB) error {
		var key models.APIKey
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&key, apiKey.ID).Error; err != nil {
			return fmt.Errorf("failed to lock key %d for update: %w", apiKey.ID, err)
		}

		newFailureCount := failureCount + 1

		updates := map[string]any{"failure_count": newFailureCount}
		shouldBlacklist := blacklistThreshold > 0 && newFailureCount >= int64(blacklistThreshold)
		if shouldBlacklist {
			updates["status"] = models.KeyStatusInvalid
		}

		if err := tx.Model(&key).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update key stats in DB: %w", err)
		}

		if _, err := p.store.HIncrBy(keyHashKey, "failure_count", 1); err != nil {
			return fmt.Errorf("failed to increment failure count in store: %w", err)
		}

		if shouldBlacklist {
			logrus.WithFields(logrus.Fields{"keyID": apiKey.ID, "threshold": blacklistThreshold}).Warn("Key has reached blacklist threshold, disabling.")
			if err := p.store.LRem(activeKeysListKey, 0, apiKey.ID); err != nil {
				return fmt.Errorf("failed to LRem key from active list: %w", err)
			}
			if err := p.store.HSet(keyHashKey, map[string]any{"status": models.KeyStatusInvalid}); err != nil {
				return fmt.Errorf("failed to update key status to invalid in store: %w", err)
			}
		}

		return nil
	})
}

// LoadKeysFromDB 从数据库加载所有分组和密钥，并填充到 Store 中。
func (p *KeyProvider) LoadKeysFromDB() error {
	initFlagKey := "initialization:db_keys_loaded"

	exists, err := p.store.Exists(initFlagKey)
	if err != nil {
		return fmt.Errorf("failed to check initialization flag: %w", err)
	}

	if exists {
		logrus.Debug("Keys have already been loaded into the store. Skipping.")
		return nil
	}

	logrus.Debug("First time startup, loading keys from DB...")

	// Execute data migration (backward compatibility handling)
	if err := p.migrateOldData(); err != nil {
		logrus.WithError(err).Error("Failed to migrate old data, but continuing with loading...")
	}

	allActiveKeyIDs := make(map[uint]map[string][]any)
	batchSize := 1000
	var batchKeys []*models.APIKey

	err = p.db.Model(&models.APIKey{}).FindInBatches(&batchKeys, batchSize, func(tx *gorm.DB, batch int) error {
		logrus.Debugf("Processing batch %d with %d keys...", batch, len(batchKeys))

		var pipeline store.Pipeliner
		if redisStore, ok := p.store.(store.RedisPipeliner); ok {
			pipeline = redisStore.Pipeline()
		}

		for _, key := range batchKeys {
			keyHashKey := fmt.Sprintf("key:%d", key.ID)
			keyDetails := p.apiKeyToMap(key)

			if pipeline != nil {
				pipeline.HSet(keyHashKey, keyDetails)
			} else {
				if err := p.store.HSet(keyHashKey, keyDetails); err != nil {
					logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to HSet key details")
				}
			}

			if key.Status == models.KeyStatusActive {
				upstreamFilter := key.UpstreamFilter
				if upstreamFilter == "" {
					upstreamFilter = "Default"
				}

				if allActiveKeyIDs[key.GroupID] == nil {
					allActiveKeyIDs[key.GroupID] = make(map[string][]any)
				}
				allActiveKeyIDs[key.GroupID][upstreamFilter] = append(allActiveKeyIDs[key.GroupID][upstreamFilter], key.ID)
			}
		}

		if pipeline != nil {
			if err := pipeline.Exec(); err != nil {
				return fmt.Errorf("failed to execute pipeline for batch %d: %w", batch, err)
			}
		}
		return nil
	}).Error

	if err != nil {
		return fmt.Errorf("failed during batch processing of keys: %w", err)
	}

	// 2. 更新所有分组的 active_keys 列表
	logrus.Info("Updating active key lists for all groups...")
	for groupID, upstreamFilters := range allActiveKeyIDs {
		for upstreamFilter, activeIDs := range upstreamFilters {
			if len(activeIDs) > 0 {
				activeKeysListKey := fmt.Sprintf("group:%d:upstream:%s:active_keys", groupID, upstreamFilter)
				p.store.Delete(activeKeysListKey)
				if err := p.store.LPush(activeKeysListKey, activeIDs...); err != nil {
					logrus.WithFields(logrus.Fields{"groupID": groupID, "upstreamFilter": upstreamFilter, "error": err}).Error("Failed to LPush active keys for group upstream filter")
				}
			}
		}
	}

	if err := p.store.Set(initFlagKey, []byte("1"), 0); err != nil {
		logrus.WithField("flagKey", initFlagKey).Error("Failed to set initialization flag after loading keys")
	}

	return nil
}

// migrateOldData handles migration of old data to ensure backward compatibility
func (p *KeyProvider) migrateOldData() error {
	needsMigration, err := p.checkIfMigrationNeeded()
	if err != nil {
		logrus.WithError(err).Warn("Failed to check migration status, proceeding with migration")
	} else if !needsMigration {
		logrus.Debug("Data migration not needed, all data is already migrated")
		return nil
	}

	logrus.Info("Starting data migration for backward compatibility...")

	// 1. Migrate old APIKey data: set default values for keys without UpstreamFilter
	if err := p.migrateOldAPIKeys(); err != nil {
		return fmt.Errorf("failed to migrate old API keys: %w", err)
	}

	// 2. Migrate old Group Upstreams data: assign IDs to upstreams without ID
	if err := p.migrateOldUpstreams(); err != nil {
		return fmt.Errorf("failed to migrate old upstreams: %w", err)
	}

	// 3. Migrate old storage structure: from old active_keys lists to new grouped structure
	if err := p.migrateOldActiveKeysLists(); err != nil {
		return fmt.Errorf("failed to migrate old active keys lists: %w", err)
	}

	logrus.Info("Data migration completed successfully")
	return nil
}

// checkIfMigrationNeeded checks if data migration is needed
func (p *KeyProvider) checkIfMigrationNeeded() (bool, error) {
	// Check 1: Are there APIKeys that need default UpstreamFilter set
	var countKeysNeedMigration int64
	if err := p.db.Model(&models.APIKey{}).Where("upstream_filter = '' OR upstream_filter IS NULL").Count(&countKeysNeedMigration).Error; err != nil {
		return true, fmt.Errorf("failed to count keys needing migration: %w", err)
	}
	if countKeysNeedMigration > 0 {
		logrus.WithField("count", countKeysNeedMigration).Debug("Found API keys needing UpstreamFilter migration")
		return true, nil
	}

	// Check 2: Are there Group Upstreams that need ID assignment
	var groups []models.Group
	if err := p.db.Find(&groups).Error; err != nil {
		return true, fmt.Errorf("failed to load groups for migration check: %w", err)
	}

	for _, group := range groups {
		needsUpstreamMigration, err := p.checkGroupUpstreamMigration(&group)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": group.ID,
				"error":   err,
			}).Warn("Failed to check upstream migration for group, assuming migration needed")
			return true, nil
		}
		if needsUpstreamMigration {
			logrus.WithField("groupID", group.ID).Debug("Found group needing upstream ID migration")
			return true, nil
		}
	}

	// Check 3: Are there old storage structures that need cleanup
	// This check is relatively lightweight, we can execute cleanup every time
	// Because deleting non-existent keys is a safe operation

	logrus.Debug("No migration needed, all data is up to date")
	return false, nil
}

// checkGroupUpstreamMigration checks if a single group needs upstream ID migration
func (p *KeyProvider) checkGroupUpstreamMigration(group *models.Group) (bool, error) {
	type upstreamDef struct {
		URL    string `json:"url"`
		Weight int    `json:"weight"`
		ID     string `json:"id,omitempty"`
	}

	var defs []upstreamDef
	if err := json.Unmarshal(group.Upstreams, &defs); err != nil {
		return true, fmt.Errorf("failed to unmarshal upstreams for group %d: %w", group.ID, err)
	}

	for _, def := range defs {
		if strings.TrimSpace(def.ID) == "" {
			return true, nil // Found upstream that needs ID assignment
		}
	}

	return false, nil // All upstreams have IDs
}

// migrateOldAPIKeys 为旧的 APIKey 设置默认的 UpstreamFilter
func (p *KeyProvider) migrateOldAPIKeys() error {
	var keysToUpdate []models.APIKey
	if err := p.db.Where("upstream_filter = '' OR upstream_filter IS NULL").Find(&keysToUpdate).Error; err != nil {
		return fmt.Errorf("failed to find keys without upstream_filter: %w", err)
	}

	if len(keysToUpdate) == 0 {
		logrus.Debug("No old API keys found to migrate")
		return nil
	}

	logrus.WithField("count", len(keysToUpdate)).Info("Migrating old API keys to set default UpstreamFilter")

	// Batch update
	if err := p.db.Model(&models.APIKey{}).Where("upstream_filter = '' OR upstream_filter IS NULL").Update("upstream_filter", "Default").Error; err != nil {
		return fmt.Errorf("failed to update old API keys with default upstream_filter: %w", err)
	}

	return nil
}

// migrateOldUpstreams assigns IDs to old Group Upstreams
func (p *KeyProvider) migrateOldUpstreams() error {
	var groups []models.Group
	if err := p.db.Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to load groups for upstream migration: %w", err)
	}

	for _, group := range groups {
		if err := p.migrateGroupUpstreams(&group); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": group.ID,
				"error":   err,
			}).Error("Failed to migrate upstreams for group")
			// Continue processing other groups, don't interrupt the entire migration process
		}
	}

	return nil
}

// migrateGroupUpstreams assigns IDs to upstreams for a single group
func (p *KeyProvider) migrateGroupUpstreams(group *models.Group) error {
	type oldUpstreamDef struct {
		URL    string `json:"url"`
		Weight int    `json:"weight"`
		ID     string `json:"id,omitempty"` // May or may not exist
	}

	var defs []oldUpstreamDef
	if err := json.Unmarshal(group.Upstreams, &defs); err != nil {
		return fmt.Errorf("failed to unmarshal upstreams for group %d: %w", group.ID, err)
	}

	needsUpdate := false
	for i := range defs {
		if strings.TrimSpace(defs[i].ID) == "" {
			defs[i].ID = fmt.Sprintf("%d", i+1)
			needsUpdate = true
		}
	}

	if needsUpdate {
		updatedUpstreams, err := json.Marshal(defs)
		if err != nil {
			return fmt.Errorf("failed to marshal updated upstreams for group %d: %w", group.ID, err)
		}

		if err := p.db.Model(group).Update("upstreams", updatedUpstreams).Error; err != nil {
			return fmt.Errorf("failed to update upstreams for group %d: %w", group.ID, err)
		}

		logrus.WithField("groupID", group.ID).Info("Updated upstreams with auto-generated IDs")
	}

	return nil
}

// migrateOldActiveKeysLists migrates old storage structure
func (p *KeyProvider) migrateOldActiveKeysLists() error {
	// This method will rebuild all lists in LoadKeysFromDB, so we only need to clean up old lists
	logrus.Info("Cleaning up old active keys lists structure...")

	// Get all groups
	var groups []models.Group
	if err := p.db.Select("id").Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to load groups for active keys migration: %w", err)
	}

	// Delete old active_keys lists
	for _, group := range groups {
		oldActiveKeysListKey := fmt.Sprintf("group:%d:active_keys", group.ID)
		if err := p.store.Delete(oldActiveKeysListKey); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": group.ID,
				"error":   err,
			}).Debug("Failed to delete old active keys list (may not exist)")
		}
	}

	return nil
}

// AddKeys 批量添加新的 Key 到池和数据库中。
func (p *KeyProvider) AddKeys(groupID uint, keys []models.APIKey) error {
	if len(keys) == 0 {
		return nil
	}

	err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&keys).Error; err != nil {
			return err
		}

		for _, key := range keys {
			if err := p.addKeyToStore(&key); err != nil {
				logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to add key to store after DB creation, rolling back transaction")
				return err
			}
		}
		return nil
	})

	return err
}

// RemoveKeys 批量从池和数据库中移除 Key。
func (p *KeyProvider) RemoveKeys(groupID uint, keyValues []string) (int64, error) {
	if len(keyValues) == 0 {
		return 0, nil
	}

	var keysToDelete []models.APIKey
	var deletedCount int64

	err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ? AND key_value IN ?", groupID, keyValues).Find(&keysToDelete).Error; err != nil {
			return err
		}

		if len(keysToDelete) == 0 {
			return nil
		}

		keyIDsToDelete := pluckIDs(keysToDelete)

		result := tx.Where("id IN ?", keyIDsToDelete).Delete(&models.APIKey{})
		if result.Error != nil {
			return result.Error
		}
		deletedCount = result.RowsAffected

		for _, key := range keysToDelete {
			if err := p.removeKeyFromStore(key.ID, key.GroupID); err != nil {
				logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to remove key from store after DB deletion, rolling back transaction")
				return err
			}
		}

		return nil
	})

	return deletedCount, err
}

// RestoreKeys 恢复组内所有无效的 Key。
func (p *KeyProvider) RestoreKeys(groupID uint) (int64, error) {
	var invalidKeys []models.APIKey
	var restoredCount int64

	err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ? AND status = ?", groupID, models.KeyStatusInvalid).Find(&invalidKeys).Error; err != nil {
			return err
		}

		if len(invalidKeys) == 0 {
			return nil
		}

		updates := map[string]any{
			"status":        models.KeyStatusActive,
			"failure_count": 0,
		}
		result := tx.Model(&models.APIKey{}).Where("group_id = ? AND status = ?", groupID, models.KeyStatusInvalid).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		restoredCount = result.RowsAffected

		for _, key := range invalidKeys {
			key.Status = models.KeyStatusActive
			key.FailureCount = 0
			if err := p.addKeyToStore(&key); err != nil {
				logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to restore key in store after DB update, rolling back transaction")
				return err
			}
		}
		return nil
	})

	return restoredCount, err
}

// RestoreMultipleKeys 恢复指定的 Key。
func (p *KeyProvider) RestoreMultipleKeys(groupID uint, keyValues []string) (int64, error) {
	if len(keyValues) == 0 {
		return 0, nil
	}

	var keysToRestore []models.APIKey
	var restoredCount int64

	err := p.db.Transaction(func(tx *gorm.DB) error {
		// 1. 查找要恢复的密钥
		if err := tx.Where("group_id = ? AND key_value IN ? AND status = ?", groupID, keyValues, models.KeyStatusInvalid).Find(&keysToRestore).Error; err != nil {
			return err
		}

		if len(keysToRestore) == 0 {
			return nil
		}

		keyIDsToRestore := pluckIDs(keysToRestore)

		// 2. 更新数据库中的状态
		updates := map[string]any{
			"status":        models.KeyStatusActive,
			"failure_count": 0,
		}
		result := tx.Model(&models.APIKey{}).Where("id IN ?", keyIDsToRestore).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		restoredCount = result.RowsAffected

		// 3. 将密钥添加回 Redis
		for _, key := range keysToRestore {
			key.Status = models.KeyStatusActive
			key.FailureCount = 0
			if err := p.addKeyToStore(&key); err != nil {
				// 在事务中，单个失败会回滚整个事务，但这里的日志记录仍然有用
				logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to restore key in store after DB update")
				return err // 返回错误以回滚事务
			}
		}

		return nil
	})

	return restoredCount, err
}

// RemoveInvalidKeys 移除组内所有无效的 Key。
func (p *KeyProvider) RemoveInvalidKeys(groupID uint) (int64, error) {
	var invalidKeys []models.APIKey
	var removedCount int64

	err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ? AND status = ?", groupID, models.KeyStatusInvalid).Find(&invalidKeys).Error; err != nil {
			return err
		}

		if len(invalidKeys) == 0 {
			return nil
		}

		result := tx.Where("id IN ?", pluckIDs(invalidKeys)).Delete(&models.APIKey{})
		if result.Error != nil {
			return result.Error
		}
		removedCount = result.RowsAffected

		for _, key := range invalidKeys {
			if err := p.removeKeyFromStore(key.ID, key.GroupID); err != nil {
				logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to remove invalid key from store after DB deletion, rolling back transaction")
				return err
			}
		}
		return nil
	})

	return removedCount, err
}

// RemoveKeysFromStore 直接从内存存储中移除指定的键，不涉及数据库操作
// 这个方法适用于数据库已经删除但需要清理内存存储的场景
func (p *KeyProvider) RemoveKeysFromStore(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}

	// Step 1: Get detailed information of all keys to be deleted to determine their upstream filter
	upstreamFilters := make(map[string]bool)
	for _, keyID := range keyIDs {
		keyHashKey := fmt.Sprintf("key:%d", keyID)
		keyDetails, err := p.store.HGetAll(keyHashKey)
		if err == nil && keyDetails != nil {
			upstreamFilter := keyDetails["upstream_filter"]
			if upstreamFilter == "" {
				upstreamFilter = "Default"
			}
			upstreamFilters[upstreamFilter] = true
		}
	}

	// Step 2: Delete all related upstream filter lists
	for upstreamFilter := range upstreamFilters {
		activeKeysListKey := fmt.Sprintf("group:%d:upstream:%s:active_keys", groupID, upstreamFilter)
		if err := p.store.Delete(activeKeysListKey); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID":        groupID,
				"upstreamFilter": upstreamFilter,
				"error":          err,
			}).Error("Failed to delete active keys list for upstream filter")
		}
	}

	// Step 3: Batch delete all related key hashes
	for _, keyID := range keyIDs {
		keyHashKey := fmt.Sprintf("key:%d", keyID)
		if err := p.store.Delete(keyHashKey); err != nil {
			logrus.WithFields(logrus.Fields{
				"keyID": keyID,
				"error": err,
			}).Error("Failed to delete key hash")
		}
	}

	logrus.WithFields(logrus.Fields{
		"groupID":  groupID,
		"keyCount": len(keyIDs),
	}).Info("Successfully cleaned up group keys from store")

	return nil
}

// addKeyToStore is a helper to add a single key to the cache.
func (p *KeyProvider) addKeyToStore(key *models.APIKey) error {
	// 1. Store key details in HASH
	keyHashKey := fmt.Sprintf("key:%d", key.ID)
	keyDetails := p.apiKeyToMap(key)
	if err := p.store.HSet(keyHashKey, keyDetails); err != nil {
		return fmt.Errorf("failed to HSet key details for key %d: %w", key.ID, err)
	}

	// 2. If active, add to the appropriate upstream filter LIST
	if key.Status == models.KeyStatusActive {
		upstreamFilter := key.UpstreamFilter
		if upstreamFilter == "" {
			upstreamFilter = "Default"
		}

		activeKeysListKey := fmt.Sprintf("group:%d:upstream:%s:active_keys", key.GroupID, upstreamFilter)
		if err := p.store.LRem(activeKeysListKey, 0, key.ID); err != nil {
			return fmt.Errorf("failed to LRem key %d before LPush for group %d upstream %s: %w", key.ID, key.GroupID, upstreamFilter, err)
		}
		if err := p.store.LPush(activeKeysListKey, key.ID); err != nil {
			return fmt.Errorf("failed to LPush key %d to group %d upstream %s: %w", key.ID, key.GroupID, upstreamFilter, err)
		}
	}
	return nil
}

// removeKeyFromStore is a helper to remove a single key from the cache.
func (p *KeyProvider) removeKeyFromStore(keyID, groupID uint) error {
	// First get key details to determine its UpstreamFilter
	keyHashKey := fmt.Sprintf("key:%d", keyID)
	keyDetails, err := p.store.HGetAll(keyHashKey)
	if err != nil {
		logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to get key details for removal, will try to remove from all possible lists")
	}

	upstreamFilter := "Default"
	if keyDetails != nil && keyDetails["upstream_filter"] != "" {
		upstreamFilter = keyDetails["upstream_filter"]
	}

	// Remove from corresponding upstream filter list
	activeKeysListKey := fmt.Sprintf("group:%d:upstream:%s:active_keys", groupID, upstreamFilter)
	if err := p.store.LRem(activeKeysListKey, 0, keyID); err != nil {
		logrus.WithFields(logrus.Fields{"keyID": keyID, "groupID": groupID, "upstreamFilter": upstreamFilter, "error": err}).Error("Failed to LRem key from active list")
	}

	// Delete key details
	if err := p.store.Delete(keyHashKey); err != nil {
		return fmt.Errorf("failed to delete key HASH for key %d: %w", keyID, err)
	}
	return nil
}

// apiKeyToMap converts an APIKey model to a map for HSET.
func (p *KeyProvider) apiKeyToMap(key *models.APIKey) map[string]any {
	return map[string]any{
		"id":              fmt.Sprint(key.ID),
		"key_string":      key.KeyValue,
		"status":          key.Status,
		"upstream_filter": key.UpstreamFilter,
		"failure_count":   key.FailureCount,
		"group_id":        key.GroupID,
		"created_at":      key.CreatedAt.Unix(),
	}
}

// pluckIDs extracts IDs from a slice of APIKey.
func pluckIDs(keys []models.APIKey) []uint {
	ids := make([]uint, len(keys))
	for i, key := range keys {
		ids[i] = key.ID
	}
	return ids
}

package keypool

import (
	"errors"
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/encryption"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Constants for better code organization
const (
	// Retry configuration
	maxRetries    = 3
	baseDelay     = 50 * time.Millisecond
	maxJitter     = 150 * time.Millisecond

	// Batch processing
	defaultBatchSize = 1000

	// Redis key patterns
	activeKeysPattern = "group:%d:active_keys"
	keyHashPattern    = "key:%d"
	initFlagKey       = "initialization:db_keys_loaded"

	// Status code parsing patterns
	statusPrefix = "[status "
	statusSuffix = "]"
	statusOffset = 8 // len("[status ")
)

type KeyProvider struct {
	db              *gorm.DB
	store           store.Store
	settingsManager *config.SystemSettingsManager
	encryptionSvc   encryption.Service
}

// NewProvider 创建一个新的 KeyProvider 实例。
func NewProvider(db *gorm.DB, store store.Store, settingsManager *config.SystemSettingsManager, encryptionSvc encryption.Service) *KeyProvider {
	return &KeyProvider{
		db:              db,
		store:           store,
		settingsManager: settingsManager,
		encryptionSvc:   encryptionSvc,
	}
}

// SelectKey 为指定的分组原子性地选择并轮换一个可用的 APIKey。
func (p *KeyProvider) SelectKey(groupID uint) (*models.APIKey, error) {
	activeKeysListKey := fmt.Sprintf(activeKeysPattern, groupID)

	// 1. Atomically rotate the key ID from the list
	keyIDStr, err := p.store.Rotate(activeKeysListKey)
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

	// 2. Get key details from HASH
	keyHashKey := fmt.Sprintf(keyHashPattern, keyID)
	keyDetails, err := p.store.HGetAll(keyHashKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get key details for key ID %d: %w", keyID, err)
	}

	// 3. Manually unmarshal the map into an APIKey struct with error handling
	failureCount, err := strconv.ParseInt(keyDetails["failure_count"], 10, 64)
	if err != nil {
		failureCount = 0 // Default to 0 on parse error
	}
	createdAt, err := strconv.ParseInt(keyDetails["created_at"], 10, 64)
	if err != nil {
		createdAt = time.Now().Unix() // Default to current time
	}

	// Decrypt the key value for use by channels
	encryptedKeyValue := keyDetails["key_string"]
	decryptedKeyValue, err := p.encryptionSvc.Decrypt(encryptedKeyValue)
	if err != nil {
		// If decryption fails, try to use the value as-is (backward compatibility for unencrypted keys)
		logrus.WithFields(logrus.Fields{
			"keyID": keyID,
			"error": err,
		}).Debug("Failed to decrypt key value, using as-is for backward compatibility")
		decryptedKeyValue = encryptedKeyValue
	}

	// Initialize struct with required fields only to reduce memory footprint
	apiKey := &models.APIKey{
		ID:           uint(keyID),
		KeyValue:     decryptedKeyValue,
		Status:       keyDetails["status"],
		FailureCount: failureCount,
		GroupID:      groupID,
		CreatedAt:    time.Unix(createdAt, 0),
	}

	// Check blacklist threshold - get current effective config for the group
	// Use Select to only fetch the required fields
	var group models.Group
	if err := p.db.Select("id, config").Where("id = ?", groupID).First(&group).Error; err != nil {
		return nil, fmt.Errorf("failed to get group info for group %d: %w", groupID, err)
	}

	effectiveConfig := p.settingsManager.GetEffectiveConfig(group.Config)
	isBlacklisted := effectiveConfig.BlacklistThreshold > 0 && failureCount >= int64(effectiveConfig.BlacklistThreshold)

	if isBlacklisted {
		logrus.WithFields(logrus.Fields{
			"keyID":        keyID,
			"groupID":      groupID,
			"failureCount": failureCount,
			"threshold":    effectiveConfig.BlacklistThreshold,
			"status":       keyDetails["status"],
		}).Debug("Blacklisted key selected from active pool, this should not happen")

		// The key is blacklisted, don't use it for requests
		// This is a safety mechanism in case the key wasn't properly removed from active pool
		return nil, app_errors.ErrNoActiveKeys
	}

	return apiKey, nil
}

// UpdateStatus 异步地提交一个 Key 状态更新任务。
// Uses goroutine with panic recovery for better stability
func (p *KeyProvider) UpdateStatus(apiKey *models.APIKey, group *models.Group, isSuccess bool, errorMessage string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logrus.WithFields(logrus.Fields{
					"keyID":   apiKey.ID,
					"groupID": group.ID,
					"panic":   r,
				}).Error("Panic in UpdateStatus goroutine")
			}
		}()
		keyHashKey := fmt.Sprintf("key:%d", apiKey.ID)
		activeKeysListKey := fmt.Sprintf("group:%d:active_keys", group.ID)

		if isSuccess {
			if err := p.handleSuccess(apiKey.ID, keyHashKey, activeKeysListKey); err != nil {
				logrus.WithFields(logrus.Fields{"keyID": apiKey.ID, "error": err}).Error("Failed to handle key success")
			}
		} else {
			if app_errors.IsUnCounted(errorMessage) {
				logrus.WithFields(logrus.Fields{
					"keyID": apiKey.ID,
					"error": errorMessage,
				}).Debug("Uncounted error, skipping failure handling")
			} else {
				if err := p.handleFailure(apiKey, group, keyHashKey, activeKeysListKey, errorMessage); err != nil {
					logrus.WithFields(logrus.Fields{"keyID": apiKey.ID, "error": err}).Error("Failed to handle key failure")
				}
			}
		}
	}()
}

// executeTransactionWithRetry wraps a database transaction with a retry mechanism.
func (p *KeyProvider) executeTransactionWithRetry(operation func(tx *gorm.DB) error) error {
	var err error

	for i := range maxRetries {
		err = p.db.Transaction(operation)
		if err == nil {
			return nil
		}

		if strings.Contains(err.Error(), "database is locked") {
			jitter := time.Duration(rand.Intn(int(maxJitter)))
			totalDelay := baseDelay + jitter
			logrus.Debugf("Database is locked, retrying in %v... (attempt %d/%d)", totalDelay, i+1, maxRetries)
			time.Sleep(totalDelay)
			continue
		}

		break
	}

	return err
}

// ParseStatusCodeFromMessage extracts status code from error message like "[status 429] ..."
func ParseStatusCodeFromMessage(errorMessage string) (int, string) {
	// Look for pattern: [status XXX] where XXX is the status code
	start := strings.Index(errorMessage, statusPrefix)
	if start == -1 {
		logrus.Debugf("No status code found in error message: %s", errorMessage)
		return 0, errorMessage
	}

	end := strings.Index(errorMessage[start:], statusSuffix)
	if end == -1 {
		logrus.Debugf("Status code bracket not found in error message: %s", errorMessage)
		return 0, errorMessage
	}

	statusPart := errorMessage[start+statusOffset : start+end]
	statusCode, err := strconv.Atoi(strings.TrimSpace(statusPart))
	if err != nil {
		logrus.Debugf("Failed to parse status code '%s' in error message: %s", statusPart, errorMessage)
		return 0, errorMessage
	}

	// Remove the status code part from the message for cleaner logging
	// Use string builder for efficient concatenation
	var sb strings.Builder
	sb.Grow(len(errorMessage)) // Pre-allocate capacity
	sb.WriteString(errorMessage[:start])
	sb.WriteString(errorMessage[start+end+1:])
	messageWithoutStatus := strings.TrimSpace(sb.String())
	logrus.Debugf("Parsed status code %d from error message", statusCode)
	return statusCode, messageWithoutStatus
}

// MapStatusCode maps HTTP status codes to key status constants
func MapStatusCode(statusCode int, currentStatus string) string {
	if statusCode == 0 {
		return currentStatus // Keep current status for network errors
	}

	// 2xx codes: always active
	if statusCode >= 200 && statusCode < 300 {
		return models.KeyStatusActive
	}

	// Specific status code mappings
	switch statusCode {
	case 429:
		logrus.Debugf("Mapping status code %d to rate_limited", statusCode)
		return models.KeyStatusRateLimited
	case 400:
		logrus.Debugf("Mapping status code %d to bad_request", statusCode)
		return models.KeyStatusBadRequest
	case 401:
		logrus.Debugf("Mapping status code %d to auth_failed", statusCode)
		return models.KeyStatusAuthFailed
	case 403:
		logrus.Debugf("Mapping status code %d to forbidden", statusCode)
		return models.KeyStatusForbidden
	case 404:
		logrus.Debugf("Mapping status code %d to auth_failed (404 treated as auth failure)", statusCode)
		return models.KeyStatusAuthFailed // 404 for API keys is usually auth failure
	default:
		// 4xx errors: authentication/authorization/issue
		if statusCode >= 400 && statusCode < 500 {
			logrus.Debugf("Mapping status code %d to invalid (4xx error)", statusCode)
			return models.KeyStatusInvalid
		}
		// 5xx errors: server errors
		if statusCode >= 500 && statusCode < 600 {
			logrus.Debugf("Mapping status code %d to server_error", statusCode)
			return models.KeyStatusServerError
		}
		// Unknown cases
		logrus.Debugf("Mapping unknown status code %d to invalid", statusCode)
		return models.KeyStatusInvalid
	}
}

func (p *KeyProvider) handleSuccess(keyID uint, keyHashKey, activeKeysListKey string) error {
	keyDetails, err := p.store.HGetAll(keyHashKey)
	if err != nil {
		return fmt.Errorf("failed to get key details from store: %w", err)
	}

	failureCount, _ := strconv.ParseInt(keyDetails["failure_count"], 10, 64)
	currentStatus := keyDetails["status"]

	// If already active with zero failures, no need to update
	if failureCount == 0 && currentStatus == models.KeyStatusActive {
		return nil
	}

	return p.executeTransactionWithRetry(func(tx *gorm.DB) error {
		var key models.APIKey
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&key, keyID).Error; err != nil {
			return fmt.Errorf("failed to lock key %d for update: %w", keyID, err)
		}

		updates := map[string]any{"failure_count": 0}
		if currentStatus != models.KeyStatusActive {
			updates["status"] = models.KeyStatusActive
		}

		if err := tx.Model(&key).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update key in DB: %w", err)
		}

		if err := p.store.HSet(keyHashKey, updates); err != nil {
			return fmt.Errorf("failed to update key details in store: %w", err)
		}

		if currentStatus != models.KeyStatusActive {
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

// helper function to remove key from active pool if it's not active status
func (p *KeyProvider) removeFromActivePoolIfNeeded(status string, keyHashKey, activeKeysListKey string, keyID uint) error {
	// Only active keys should be in the active pool
	if status != models.KeyStatusActive {
		if err := p.store.LRem(activeKeysListKey, 0, keyID); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": keyID, "status": status}).Error("Failed to remove key from active pool")
		}
		// Update store status
		if err := p.store.HSet(keyHashKey, map[string]any{"status": status}); err != nil {
			return fmt.Errorf("failed to update key status to %s in store: %w", status, err)
		}
	}
	return nil
}

func (p *KeyProvider) handleFailure(apiKey *models.APIKey, group *models.Group, keyHashKey, activeKeysListKey string, errorMessage string) error {
	keyDetails, err := p.store.HGetAll(keyHashKey)
	if err != nil {
		return fmt.Errorf("failed to get key details from store: %w", err)
	}

	// Parse status code from error message and map to new status
	statusCode, cleanErrorMessage := ParseStatusCodeFromMessage(errorMessage)
	newStatus := MapStatusCode(statusCode, keyDetails["status"])

	// If status is invalid/same or blacklisted threshold reached, use invalid
	currentStatus := keyDetails["status"]
	failureCount, _ := strconv.ParseInt(keyDetails["failure_count"], 10, 64)
	blacklistThreshold := group.EffectiveConfig.BlacklistThreshold

	// Check if key should be blacklisted (reached failure threshold)
	newFailureCount := failureCount + 1
	shouldBlacklist := blacklistThreshold > 0 && newFailureCount >= int64(blacklistThreshold)

	// Don't override the status based on HTTP code even if blacklisted.
	// The blacklist affects routing, not the status classification.
	if shouldBlacklist {
		logrus.WithFields(logrus.Fields{
			"keyID":      apiKey.ID,
			"threshold":  blacklistThreshold,
			"statusCode": statusCode,
			"newStatus":  newStatus,
		}).Debug("Key reached blacklist threshold, but keeping status based on HTTP code")
	}

	return p.executeTransactionWithRetry(func(tx *gorm.DB) error {
		var key models.APIKey
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&key, apiKey.ID).Error; err != nil {
			return fmt.Errorf("failed to lock key %d for update: %w", apiKey.ID, err)
		}

		updates := map[string]any{"failure_count": newFailureCount}
		if newStatus != currentStatus {
			updates["status"] = newStatus
		}

		if err := tx.Model(&key).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update key stats in DB: %w", err)
		}

		if _, err := p.store.HIncrBy(keyHashKey, "failure_count", 1); err != nil {
			return fmt.Errorf("failed to increment failure count in store: %w", err)
		}

		if shouldBlacklist {
			logrus.WithFields(logrus.Fields{
				"keyID":          apiKey.ID,
				"threshold":      blacklistThreshold,
				"finalStatus":    newStatus,
				"error":          cleanErrorMessage,
				"statusCode":     statusCode,
			}).Warn("Key has reached blacklist threshold, disabling.")
		} else if newStatus != currentStatus {
			logrus.WithFields(logrus.Fields{
				"keyID":        apiKey.ID,
				"oldStatus":    currentStatus,
				"newStatus":    newStatus,
				"error":        cleanErrorMessage,
				"statusCode":   statusCode,
			}).Info("Key status updated based on validation result")
		}

		// Handle active pool management based on new status
		if err := p.removeFromActivePoolIfNeeded(newStatus, keyHashKey, activeKeysListKey, apiKey.ID); err != nil {
			return err
		}

		return nil
	})
}

// LoadKeysFromDB 从数据库加载所有分组和密钥，并填充到 Store 中。
func (p *KeyProvider) LoadKeysFromDB() error {

	exists, err := p.store.Exists(initFlagKey)
	if err != nil {
		return fmt.Errorf("failed to check initialization flag: %w", err)
	}

	if exists {
		logrus.Debug("Keys have already been loaded into the store. Skipping.")
		return nil
	}

	logrus.Debug("First time startup, loading keys from DB...")

	// 1. 分批从数据库加载并使用 Pipeline 写入 Redis
	allActiveKeyIDs := make(map[uint][]any, 16) // Pre-allocate with reasonable capacity
	batchSize := defaultBatchSize
	var batchKeys []*models.APIKey

	err := p.db.Model(&models.APIKey{}).FindInBatches(&batchKeys, batchSize, func(tx *gorm.DB, batch int) error {
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
				allActiveKeyIDs[key.GroupID] = append(allActiveKeyIDs[key.GroupID], key.ID)
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
	for groupID, activeIDs := range allActiveKeyIDs {
		if len(activeIDs) > 0 {
			activeKeysListKey := fmt.Sprintf(activeKeysPattern, groupID)
			p.store.Delete(activeKeysListKey)
			if err := p.store.LPush(activeKeysListKey, activeIDs...); err != nil {
				logrus.WithFields(logrus.Fields{"groupID": groupID, "error": err}).Error("Failed to LPush active keys for group")
			}
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
		var keyHashes []string
		for _, keyValue := range keyValues {
			keyHash := p.encryptionSvc.Hash(keyValue)
			if keyHash != "" {
				keyHashes = append(keyHashes, keyHash)
			}
		}

		if len(keyHashes) == 0 {
			return nil
		}

		if err := tx.Where("group_id = ? AND key_hash IN ?", groupID, keyHashes).Find(&keysToDelete).Error; err != nil {
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

		var keyHashes []string
		for _, keyValue := range keyValues {
			keyHash := p.encryptionSvc.Hash(keyValue)
			if keyHash != "" {
				keyHashes = append(keyHashes, keyHash)
			}
		}

		if len(keyHashes) == 0 {
			return nil
		}

		// 1. 查找要恢复的密钥 - 仅选择必要字段
		if err := tx.Select("id, key_value, group_id, status").Where("group_id = ? AND key_value IN ? AND status = ?", groupID, keyValues, models.KeyStatusInvalid).Find(&keysToRestore).Error; err != nil {
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
				logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to restore key in store after DB update")
				return err
			}
		}

		return nil
	})

	return restoredCount, err
}

// RemoveInvalidKeys 移除组内所有无效的 Key。
func (p *KeyProvider) RemoveInvalidKeys(groupID uint) (int64, error) {
	return p.removeKeysByStatus(groupID, models.KeyStatusInvalid)
}

// RemoveAllKeys 移除组内所有的 Key。
func (p *KeyProvider) RemoveAllKeys(groupID uint) (int64, error) {
	return p.removeKeysByStatus(groupID)
}

// removeKeysByStatus is a generic function to remove keys by status.
// If no status is provided, it removes all keys in the group.
func (p *KeyProvider) removeKeysByStatus(groupID uint, status ...string) (int64, error) {
	var keysToRemove []models.APIKey
	var removedCount int64

	err := p.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Where("group_id = ?", groupID)
		if len(status) > 0 {
			query = query.Where("status IN ?", status)
		}

		if err := query.Find(&keysToRemove).Error; err != nil {
			return err
		}

		if len(keysToRemove) == 0 {
			return nil
		}

		deleteQuery := tx.Where("group_id = ?", groupID)
		if len(status) > 0 {
			deleteQuery = deleteQuery.Where("status IN ?", status)
		}
		result := deleteQuery.Delete(&models.APIKey{})
		if result.Error != nil {
			return result.Error
		}
		removedCount = result.RowsAffected

		for _, key := range keysToRemove {
			if err := p.removeKeyFromStore(key.ID, key.GroupID); err != nil {
				logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to remove key from store after DB deletion, rolling back transaction")
				return err
			}
		}
		return nil
	})

	return removedCount, err
}

// RestoreKeysByStatus 恢复指定状态的密钥
func (p *KeyProvider) RestoreKeysByStatus(groupID uint, status string) (int64, error) {
	var keysToRestore []models.APIKey
	var restoredCount int64

	err := p.db.Transaction(func(tx *gorm.DB) error {
		// 1. 查找要恢复的密钥
		if err := tx.Where("group_id = ? AND status = ?", groupID, status).Find(&keysToRestore).Error; err != nil {
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
				logrus.WithFields(logrus.Fields{"keyID": key.ID, "error": err}).Error("Failed to restore key in store after DB update")
				return err
			}
		}

		return nil
	})

	return restoredCount, err
}

// ClearKeysByStatus 清除指定状态的密钥
func (p *KeyProvider) ClearKeysByStatus(groupID uint, status string) (int64, error) {
	return p.removeKeysByStatus(groupID, status)
}

// RemoveKeysFromStore 直接从内存存储中移除指定的键，不涉及数据库操作
// 这个方法适用于数据库已经删除但需要清理内存存储的场景
func (p *KeyProvider) RemoveKeysFromStore(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}

	activeKeysListKey := fmt.Sprintf(activeKeysPattern, groupID)

	// 第一步：直接删除整个 active_keys 列表
	if err := p.store.Delete(activeKeysListKey); err != nil {
		logrus.WithFields(logrus.Fields{
			"groupID": groupID,
			"error":   err,
		}).Error("Failed to delete active keys list")
		return err
	}

	// 第二步：批量删除所有相关的key hash
	for _, keyID := range keyIDs {
		keyHashKey := fmt.Sprintf(keyHashPattern, keyID)
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

	// 2. If active, add to the active LIST. Only active keys are in the rotation pool.
	if key.Status == models.KeyStatusActive {
		activeKeysListKey := fmt.Sprintf("group:%d:active_keys", key.GroupID)
		if err := p.store.LRem(activeKeysListKey, 0, key.ID); err != nil {
			return fmt.Errorf("failed to LRem key %d before LPush for group %d: %w", key.ID, key.GroupID, err)
		}
		if err := p.store.LPush(activeKeysListKey, key.ID); err != nil {
			return fmt.Errorf("failed to LPush key %d to group %d: %w", key.ID, key.GroupID, err)
		}
	} else {
		// For non-active keys, ensure they're not in the active pool (defense in depth)
		activeKeysListKey := fmt.Sprintf("group:%d:active_keys", key.GroupID)
		if err := p.store.LRem(activeKeysListKey, 0, key.ID); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": key.ID, "status": key.Status}).Debug("Removing non-active key from active pool")
		}
	}
	return nil
}

// removeKeyFromStore is a helper to remove a single key from the cache.
func (p *KeyProvider) removeKeyFromStore(keyID, groupID uint) error {
	activeKeysListKey := fmt.Sprintf(activeKeysPattern, groupID)
	if err := p.store.LRem(activeKeysListKey, 0, keyID); err != nil {
		logrus.WithFields(logrus.Fields{"keyID": keyID, "groupID": groupID, "error": err}).Error("Failed to LRem key from active list")
	}

	keyHashKey := fmt.Sprintf(keyHashPattern, keyID)
	if err := p.store.Delete(keyHashKey); err != nil {
		return fmt.Errorf("failed to delete key HASH for key %d: %w", keyID, err)
	}
	return nil
}

// apiKeyToMap converts an APIKey model to a map for HSET.
// Uses strconv.FormatUint for better performance than fmt.Sprint
func (p *KeyProvider) apiKeyToMap(key *models.APIKey) map[string]any {
	return map[string]any{
		"id":            strconv.FormatUint(uint64(key.ID), 10),
		"key_string":    key.KeyValue,
		"status":        key.Status,
		"failure_count": strconv.FormatInt(key.FailureCount, 10),
		"group_id":      strconv.FormatUint(uint64(key.GroupID), 10),
		"created_at":    strconv.FormatInt(key.CreatedAt.Unix(), 10),
	}
}

// pluckIDs extracts IDs from a slice of APIKey.
// Pre-allocates slice with known capacity for better performance
func pluckIDs(keys []models.APIKey) []uint {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]uint, len(keys))
	for i, key := range keys {
		ids[i] = key.ID
	}
	return ids
}

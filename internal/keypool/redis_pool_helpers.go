package keypool

import (
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

// getPoolSize 获取池大小
func (p *RedisLayeredPool) getPoolSize(groupID uint, poolType PoolType) (int64, error) {
	switch poolType {
	case PoolTypeValidation:
		validationKey := p.getRedisKey(groupID, PoolTypeValidation)
		// 需要实现SCARD操作
		members, err := p.store.SMembers(validationKey)
		if err != nil {
			return 0, err
		}
		return int64(len(members)), nil

	case PoolTypeReady, PoolTypeActive:
		// 需要实现LLEN操作
		// 暂时返回0
		return 0, nil

	case PoolTypeCooling:
		coolingKey := p.getRedisKey(groupID, PoolTypeCooling)
		return p.store.ZCard(coolingKey)

	default:
		return 0, NewPoolError(ErrorTypeValidation, "UNKNOWN_POOL_TYPE", "Unknown pool type")
	}
}

// refillReadyFromValidation 从验证池补充就绪池
func (p *RedisLayeredPool) refillReadyFromValidation(groupID uint, count int) error {
	// 从验证池获取密钥
	validationKeys, err := p.listValidationKeys(groupID)
	if err != nil {
		return err
	}

	if len(validationKeys) == 0 {
		return nil
	}

	// 限制数量
	if count > len(validationKeys) {
		count = len(validationKeys)
	}

	keysToMove := validationKeys[:count]

	// 验证这些密钥
	validatedKeys := make([]uint, 0, len(keysToMove))
	for _, keyID := range keysToMove {
		if p.validator != nil {
			// 获取密钥详情
			details, err := p.getKeyDetails(keyID)
			if err != nil {
				continue
			}

			// 构建APIKey对象进行验证
			apiKey, err := p.buildAPIKeyFromDetails(keyID, groupID, details)
			if err != nil {
				continue
			}

			// 获取分组信息
			var group models.Group
			if err := p.db.First(&group, groupID).Error; err != nil {
				continue
			}

			// 验证密钥
			if err := p.validator.ValidateKey(apiKey, &group); err != nil {
				// 验证失败，从验证池移除
				p.removeFromValidationPool(groupID, []uint{keyID})
				continue
			}
		}

		validatedKeys = append(validatedKeys, keyID)
	}

	if len(validatedKeys) == 0 {
		return nil
	}

	// 移动验证通过的密钥到就绪池
	if err := p.moveToReadyPool(groupID, validatedKeys); err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"groupID":   groupID,
		"validated": len(validatedKeys),
		"total":     len(keysToMove),
	}).Info("Moved validated keys from validation to ready pool")

	return nil
}

// refillFromDatabase 从数据库加载新密钥
func (p *RedisLayeredPool) refillFromDatabase(groupID uint, count int) error {
	// 查找可用的密钥
	var availableKeys []models.APIKey
	if err := p.db.Where("group_id = ? AND status IN ?", groupID,
		[]string{models.KeyStatusActive, models.KeyStatusRateLimited}).
		Limit(count * 2). // 多查一些以防有些不可用
		Find(&availableKeys).Error; err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "DB_QUERY_FAILED", "Failed to query available keys", err)
	}

	if len(availableKeys) == 0 {
		return nil
	}

	// 过滤出真正可用的密钥
	usableKeys := make([]uint, 0, len(availableKeys))
	for _, key := range availableKeys {
		// 检查是否已经在池中
		if p.isKeyInAnyPool(groupID, key.ID) {
			continue
		}

		// 检查429状态的密钥是否已过期
		if key.Status == models.KeyStatusRateLimited {
			if key.RateLimitResetAt != nil && time.Now().Before(*key.RateLimitResetAt) {
				continue // 还在冷却期
			}
		}

		usableKeys = append(usableKeys, key.ID)
		if len(usableKeys) >= count {
			break
		}
	}

	if len(usableKeys) == 0 {
		return nil
	}

	// 添加到验证池
	if err := p.addToValidationPool(groupID, usableKeys); err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"groupID": groupID,
		"loaded":  len(usableKeys),
	}).Info("Loaded new keys from database to validation pool")

	return nil
}

// isKeyInAnyPool 检查密钥是否已在任何池中
func (p *RedisLayeredPool) isKeyInAnyPool(groupID, keyID uint) bool {
	// 检查密钥详情是否存在
	_, err := p.getKeyDetails(keyID)
	return err == nil
}

// RecoverCooledKeys 恢复已过期的冷却密钥
func (p *RedisLayeredPool) RecoverCooledKeys(groupID uint) (int, error) {
	now := time.Now()

	// 获取已过期的冷却密钥
	expiredKeys, err := p.getExpiredFromCoolingPool(groupID)
	if err != nil {
		return 0, NewPoolErrorWithCause(ErrorTypeStorage, "GET_EXPIRED_KEYS_FAILED", "Failed to get expired keys from cooling pool", err)
	}

	if len(expiredKeys) == 0 {
		return 0, nil
	}

	recoveredCount := 0
	for _, keyID := range expiredKeys {
		if err := p.recoverSingleCooledKey(groupID, keyID); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": groupID,
				"keyID":   keyID,
				"error":   err,
			}).Error("Failed to recover cooled key")
			continue
		}
		recoveredCount++
	}

	if recoveredCount > 0 {
		// 记录指标
		if p.metrics != nil {
			p.metrics.RecordKeyRecovery(groupID, recoveredCount)
		}

		// 发送事件
		if p.eventHandler != nil {
			event := &KeyPoolEvent{
				Type:      EventKeyRecovered,
				GroupID:   groupID,
				Message:   fmt.Sprintf("Recovered %d cooled keys", recoveredCount),
				Timestamp: now,
				Metadata:  expiredKeys[:recoveredCount],
			}
			p.eventHandler.HandleEvent(event)
		}

		logrus.WithFields(logrus.Fields{
			"groupID":   groupID,
			"recovered": recoveredCount,
			"total":     len(expiredKeys),
		}).Info("Recovered cooled keys")
	}

	return recoveredCount, nil
}

// recoverSingleCooledKey 恢复单个冷却密钥
func (p *RedisLayeredPool) recoverSingleCooledKey(groupID, keyID uint) error {
	// 从冷却池移除
	if err := p.removeFromCoolingPool(groupID, []uint{keyID}); err != nil {
		return err
	}

	// 更新密钥状态
	updates := map[string]interface{}{
		"status":               models.KeyStatusActive,
		"rate_limit_reset_at":  nil,
	}
	if err := p.setKeyDetails(keyID, updates); err != nil {
		return err
	}

	// 添加到就绪池
	if err := p.addToReadyPool(groupID, []uint{keyID}); err != nil {
		return err
	}

	return nil
}

// GetKeyStatus 获取密钥状态
func (p *RedisLayeredPool) GetKeyStatus(keyID uint) (KeyStatus, error) {
	details, err := p.getKeyDetails(keyID)
	if err != nil {
		if err == store.ErrNotFound {
			return KeyStatusFailed, nil
		}
		return "", NewPoolErrorWithCause(ErrorTypeStorage, "GET_KEY_DETAILS_FAILED", "Failed to get key details", err)
	}

	status := details["status"]
	switch status {
	case models.KeyStatusActive:
		return KeyStatusInUse, nil
	case models.KeyStatusRateLimited:
		return KeyStatusCooling, nil
	case models.KeyStatusInvalid:
		return KeyStatusFailed, nil
	default:
		return KeyStatusPending, nil
	}
}

// ValidateKeys 验证密钥
func (p *RedisLayeredPool) ValidateKeys(groupID uint, keyIDs []uint) error {
	if p.validator == nil {
		return NewPoolError(ErrorTypeConfiguration, "NO_VALIDATOR", "No validator configured")
	}

	// 获取分组信息
	var group models.Group
	if err := p.db.First(&group, groupID).Error; err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "GROUP_NOT_FOUND", "Failed to find group", err)
	}

	validatedKeys := make([]uint, 0, len(keyIDs))
	failedKeys := make([]uint, 0)

	for _, keyID := range keyIDs {
		// 获取密钥详情
		details, err := p.getKeyDetails(keyID)
		if err != nil {
			failedKeys = append(failedKeys, keyID)
			continue
		}

		// 构建APIKey对象
		apiKey, err := p.buildAPIKeyFromDetails(keyID, groupID, details)
		if err != nil {
			failedKeys = append(failedKeys, keyID)
			continue
		}

		// 验证密钥
		if err := p.validator.ValidateKey(apiKey, &group); err != nil {
			failedKeys = append(failedKeys, keyID)
			continue
		}

		validatedKeys = append(validatedKeys, keyID)
	}

	// 移动验证通过的密钥到就绪池
	if len(validatedKeys) > 0 {
		if err := p.moveToReadyPool(groupID, validatedKeys); err != nil {
			return err
		}
	}

	// 移除验证失败的密钥
	if len(failedKeys) > 0 {
		if err := p.removeFromValidationPool(groupID, failedKeys); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID":    groupID,
				"failedKeys": failedKeys,
				"error":      err,
			}).Warn("Failed to remove failed keys from validation pool")
		}
	}

	logrus.WithFields(logrus.Fields{
		"groupID":   groupID,
		"validated": len(validatedKeys),
		"failed":    len(failedKeys),
		"total":     len(keyIDs),
	}).Info("Key validation completed")

	return nil
}

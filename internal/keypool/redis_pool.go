package keypool

import (
	"context"
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// RedisLayeredPool Redis实现的分层密钥池
type RedisLayeredPool struct {
	// 依赖
	store           store.Store
	db              *gorm.DB
	settingsManager *config.SystemSettingsManager

	// 配置
	config       *PoolConfig
	redisConfig  *RedisPoolConfig

	// 组件
	metrics      PoolMetrics
	validator    KeyValidator
	eventHandler EventHandler
	errorHandler ErrorHandler

	// 429恢复组件
	recoveryService    *RecoveryService
	recoveryMonitor    *RecoveryMonitor
	recoveryStrategy   RecoveryStrategy
	batchProcessor     *BatchRecoveryProcessor

	// 性能监控
	performanceMonitor *PerformanceMonitor

	// 运行时状态
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	started    bool
	mu         sync.RWMutex

	// 缓存
	groupConfigs map[uint]*PoolConfig
	configMu     sync.RWMutex
}

// NewRedisLayeredPool 创建新的Redis分层密钥池
func NewRedisLayeredPool(factoryConfig *FactoryConfig) (*RedisLayeredPool, error) {
	if factoryConfig.Store == nil {
		return nil, NewPoolError(ErrorTypeConfiguration, "MISSING_STORE", "Store is required")
	}

	redisStore, ok := factoryConfig.Store.(store.Store)
	if !ok {
		return nil, NewPoolError(ErrorTypeConfiguration, "INVALID_STORE", "Store must implement store.Store interface")
	}

	db, ok := factoryConfig.DB.(*gorm.DB)
	if !ok {
		return nil, NewPoolError(ErrorTypeConfiguration, "INVALID_DB", "DB must be *gorm.DB")
	}

	settingsManager, ok := factoryConfig.SettingsManager.(*config.SystemSettingsManager)
	if !ok {
		return nil, NewPoolError(ErrorTypeConfiguration, "INVALID_SETTINGS_MANAGER", "SettingsManager must be *config.SystemSettingsManager")
	}

	redisConfig := factoryConfig.RedisConfig
	if redisConfig == nil {
		redisConfig = DefaultRedisPoolConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &RedisLayeredPool{
		store:           redisStore,
		db:              db,
		settingsManager: settingsManager,
		config:          factoryConfig.DefaultPoolConfig,
		redisConfig:     redisConfig,
		metrics:         factoryConfig.Metrics,
		validator:       factoryConfig.Validator,
		eventHandler:    factoryConfig.EventHandler,
		ctx:             ctx,
		cancel:          cancel,
		groupConfigs:    make(map[uint]*PoolConfig),
	}

	// 创建默认错误处理器
	if pool.errorHandler == nil {
		pool.errorHandler = &DefaultErrorHandler{}
	}

	// 初始化429恢复组件
	if err := pool.initializeRecoveryComponents(); err != nil {
		return nil, NewPoolErrorWithCause(ErrorTypeConfiguration, "RECOVERY_INIT_FAILED", "Failed to initialize recovery components", err)
	}

	return pool, nil
}

// Start 启动密钥池
func (p *RedisLayeredPool) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return NewPoolError(ErrorTypeConfiguration, "ALREADY_STARTED", "Pool is already started")
	}

	// 启动后台任务
	p.wg.Add(1)
	go p.maintenanceLoop()

	p.started = true
	logrus.Info("Redis layered pool started")

	return nil
}

// Stop 停止密钥池
func (p *RedisLayeredPool) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return nil
	}

	p.cancel()
	p.wg.Wait()

	p.started = false
	logrus.Info("Redis layered pool stopped")

	return nil
}

// Health 检查池健康状态
func (p *RedisLayeredPool) Health() error {
	// 检查Redis连接
	if err := p.store.Set("health_check", []byte("ok"), 10*time.Second); err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "REDIS_UNAVAILABLE", "Redis health check failed", err)
	}

	// 检查数据库连接
	sqlDB, err := p.db.DB()
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "DB_UNAVAILABLE", "Database connection failed", err)
	}

	if err := sqlDB.Ping(); err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "DB_PING_FAILED", "Database ping failed", err)
	}

	return nil
}

// getGroupConfig 获取分组配置
func (p *RedisLayeredPool) getGroupConfig(groupID uint) *PoolConfig {
	p.configMu.RLock()
	config, exists := p.groupConfigs[groupID]
	p.configMu.RUnlock()

	if exists {
		return config
	}

	// 使用默认配置
	defaultConfig := DefaultPoolConfig(groupID)

	p.configMu.Lock()
	p.groupConfigs[groupID] = defaultConfig
	p.configMu.Unlock()

	return defaultConfig
}

// UpdateConfig 更新分组配置
func (p *RedisLayeredPool) UpdateConfig(groupID uint, config *PoolConfig) error {
	if config == nil {
		return NewPoolError(ErrorTypeConfiguration, "NIL_CONFIG", "Config cannot be nil")
	}

	config.GroupID = groupID

	p.configMu.Lock()
	p.groupConfigs[groupID] = config
	p.configMu.Unlock()

	logrus.WithField("groupID", groupID).Info("Pool configuration updated")

	return nil
}

// GetConfig 获取分组配置
func (p *RedisLayeredPool) GetConfig(groupID uint) (*PoolConfig, error) {
	config := p.getGroupConfig(groupID)
	return config, nil
}

// maintenanceLoop 维护循环
func (p *RedisLayeredPool) maintenanceLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.performMaintenance()
		}
	}
}

// performMaintenance 执行维护任务
func (p *RedisLayeredPool) performMaintenance() {
	// 获取所有分组
	var groups []models.Group
	if err := p.db.Find(&groups).Error; err != nil {
		logrus.WithError(err).Error("Failed to load groups for maintenance")
		return
	}

	for _, group := range groups {
		// 恢复冷却的密钥
		if recovered, err := p.RecoverCooledKeys(group.ID); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": group.ID,
				"error":   err,
			}).Error("Failed to recover cooled keys")
		} else if recovered > 0 {
			logrus.WithFields(logrus.Fields{
				"groupID":   group.ID,
				"recovered": recovered,
			}).Info("Recovered cooled keys")
		}

		// 补充池
		if err := p.RefillPools(group.ID); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": group.ID,
				"error":   err,
			}).Error("Failed to refill pools")
		}
	}
}

// getRedisKey 生成Redis键名
func (p *RedisLayeredPool) getRedisKey(groupID uint, poolType PoolType) string {
	return fmt.Sprintf("%sgroup:%d:%s", p.redisConfig.KeyPrefix, groupID, poolType)
}

// getKeyDetailsKey 生成密钥详情键名
func (p *RedisLayeredPool) getKeyDetailsKey(keyID uint) string {
	return fmt.Sprintf("%skey:%d", p.redisConfig.KeyPrefix, keyID)
}

// getStatsKey 生成统计键名
func (p *RedisLayeredPool) getStatsKey(groupID uint) string {
	return fmt.Sprintf("%sstats:%d", p.redisConfig.KeyPrefix, groupID)
}

// DefaultErrorHandler 默认错误处理器
type DefaultErrorHandler struct{}

func (h *DefaultErrorHandler) HandleError(err error, context *ErrorContext) error {
	logrus.WithFields(logrus.Fields{
		"groupID":   context.GroupID,
		"keyID":     context.KeyID,
		"operation": context.Operation,
		"attempt":   context.Attempt,
		"error":     err,
	}).Error("Pool operation error")
	return err
}

func (h *DefaultErrorHandler) HandleRateLimit(rateLimitErr *errors.RateLimitError, context *ErrorContext) error {
	logrus.WithFields(logrus.Fields{
		"groupID":    context.GroupID,
		"keyID":      context.KeyID,
		"retryAfter": rateLimitErr.RetryAfter,
		"resetAt":    rateLimitErr.ResetAt,
	}).Warn("Rate limit encountered")
	return rateLimitErr
}

func (h *DefaultErrorHandler) ShouldRetry(err error, attempt int) bool {
	if attempt >= 3 {
		return false
	}

	// 检查是否为可重试错误
	if poolErr, ok := err.(*PoolError); ok {
		return poolErr.Type == ErrorTypeTimeout || poolErr.Type == ErrorTypeStorage
	}

	return false
}

func (h *DefaultErrorHandler) GetRetryDelay(err error, attempt int) time.Duration {
	baseDelay := time.Duration(attempt) * time.Second
	if baseDelay > 10*time.Second {
		baseDelay = 10 * time.Second
	}
	return baseDelay
}

// Redis数据结构设计说明：
// 1. 验证池 (validation): SET - 存储待验证的密钥ID
// 2. 就绪池 (ready): LIST - 存储已验证可用的密钥ID，支持FIFO
// 3. 活跃池 (active): LIST - 存储正在使用的密钥ID，支持轮询
// 4. 冷却池 (cooling): ZSET - 存储429密钥ID，score为恢复时间戳
// 5. 密钥详情: HASH - 存储密钥的详细信息
// 6. 池统计: HASH - 存储池的统计信息

// addToValidationPool 添加密钥到验证池
func (p *RedisLayeredPool) addToValidationPool(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}

	validationKey := p.getRedisKey(groupID, PoolTypeValidation)

	// 转换为interface{}切片
	members := make([]interface{}, len(keyIDs))
	for i, keyID := range keyIDs {
		members[i] = keyID
	}

	return p.store.SAdd(validationKey, members...)
}

// moveToReadyPool 将密钥从验证池移动到就绪池
func (p *RedisLayeredPool) moveToReadyPool(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}

	validationKey := p.getRedisKey(groupID, PoolTypeValidation)
	readyKey := p.getRedisKey(groupID, PoolTypeReady)

	// 使用事务确保原子性
	if pipeliner, ok := p.store.(store.RedisPipeliner); ok {
		pipe := pipeliner.Pipeline()

		// 从验证池移除
		for _, keyID := range keyIDs {
			// 注意：这里需要实现SREM操作，当前store接口可能需要扩展
		}

		// 添加到就绪池
		for _, keyID := range keyIDs {
			pipe.LPush(readyKey, keyID)
		}

		return pipe.Exec()
	}

	// 回退到单个操作
	for _, keyID := range keyIDs {
		// 从验证池移除（需要实现SREM）
		// 添加到就绪池
		if err := p.store.LPush(readyKey, keyID); err != nil {
			return err
		}
	}

	return nil
}

// moveToActivePool 将密钥从就绪池移动到活跃池
func (p *RedisLayeredPool) moveToActivePool(groupID uint, count int) ([]uint, error) {
	readyKey := p.getRedisKey(groupID, PoolTypeReady)
	activeKey := p.getRedisKey(groupID, PoolTypeActive)

	var movedKeys []uint

	for i := 0; i < count; i++ {
		// 从就绪池弹出一个密钥
		keyIDStr, err := p.store.Rotate(readyKey)
		if err != nil {
			if err == store.ErrNotFound {
				break // 就绪池为空
			}
			return movedKeys, err
		}

		keyID, err := strconv.ParseUint(keyIDStr, 10, 64)
		if err != nil {
			continue
		}

		// 添加到活跃池
		if err := p.store.LPush(activeKey, uint(keyID)); err != nil {
			return movedKeys, err
		}

		movedKeys = append(movedKeys, uint(keyID))
	}

	return movedKeys, nil
}

// addToCoolingPool 添加密钥到冷却池
func (p *RedisLayeredPool) addToCoolingPool(groupID uint, keyID uint, resetAt time.Time) error {
	coolingKey := p.getRedisKey(groupID, PoolTypeCooling)

	// 使用时间戳作为score
	score := float64(resetAt.Unix())

	// 注意：这里需要实现ZADD操作，当前store接口可能需要扩展
	// 临时使用Set操作，实际应该使用ZADD
	return p.store.Set(fmt.Sprintf("%s:%d", coolingKey, keyID), []byte(strconv.FormatInt(resetAt.Unix(), 10)), 0)
}

// getExpiredFromCoolingPool 获取已过期的冷却密钥
func (p *RedisLayeredPool) getExpiredFromCoolingPool(groupID uint) ([]uint, error) {
	coolingKey := p.getRedisKey(groupID, PoolTypeCooling)
	now := time.Now().Unix()

	// 注意：这里需要实现ZRANGEBYSCORE操作
	// 临时实现，实际应该使用ZRANGEBYSCORE获取score <= now的成员

	var expiredKeys []uint
	// 这里需要实现具体的Redis ZSET操作
	// 暂时返回空切片

	return expiredKeys, nil
}

// removeFromCoolingPool 从冷却池移除密钥
func (p *RedisLayeredPool) removeFromCoolingPool(groupID uint, keyIDs []uint) error {
	coolingKey := p.getRedisKey(groupID, PoolTypeCooling)

	for _, keyID := range keyIDs {
		keyName := fmt.Sprintf("%s:%d", coolingKey, keyID)
		if err := p.store.Delete(keyName); err != nil {
			return err
		}
	}

	return nil
}

// setKeyDetails 设置密钥详情
func (p *RedisLayeredPool) setKeyDetails(keyID uint, details map[string]interface{}) error {
	detailsKey := p.getKeyDetailsKey(keyID)
	return p.store.HSet(detailsKey, details)
}

// getKeyDetails 获取密钥详情
func (p *RedisLayeredPool) getKeyDetails(keyID uint) (map[string]string, error) {
	detailsKey := p.getKeyDetailsKey(keyID)
	return p.store.HGetAll(detailsKey)
}

// updatePoolStats 更新池统计
func (p *RedisLayeredPool) updatePoolStats(groupID uint, stats map[string]interface{}) error {
	statsKey := p.getStatsKey(groupID)
	return p.store.HSet(statsKey, stats)
}

// getPoolStatsFromRedis 从Redis获取池统计
func (p *RedisLayeredPool) getPoolStatsFromRedis(groupID uint) (map[string]string, error) {
	statsKey := p.getStatsKey(groupID)
	return p.store.HGetAll(statsKey)
}

// SelectKey 选择一个可用的密钥
func (p *RedisLayeredPool) SelectKey(groupID uint) (*models.APIKey, error) {
	startTime := time.Now()
	var success bool
	defer func() {
		if p.performanceMonitor != nil {
			p.performanceMonitor.RecordRequest(success, time.Since(startTime))
		}
	}()

	config := p.getGroupConfig(groupID)

	// 尝试从活跃池获取密钥
	activeKey := p.getRedisKey(groupID, PoolTypeActive)
	keyIDStr, err := p.store.Rotate(activeKey)

	if err != nil {
		if err == store.ErrNotFound {
			// 活跃池为空，尝试从就绪池补充
			if err := p.RefillPools(groupID); err != nil {
				return nil, NewPoolErrorWithCause(ErrorTypeCapacity, "REFILL_FAILED", "Failed to refill pools", err)
			}

			// 再次尝试从活跃池获取
			keyIDStr, err = p.store.Rotate(activeKey)
			if err != nil {
				if err == store.ErrNotFound {
					return nil, ErrPoolEmpty
				}
				return nil, NewPoolErrorWithCause(ErrorTypeStorage, "SELECT_FAILED", "Failed to select key", err)
			}
		} else {
			return nil, NewPoolErrorWithCause(ErrorTypeStorage, "SELECT_FAILED", "Failed to select key", err)
		}
	}

	keyID, err := strconv.ParseUint(keyIDStr, 10, 64)
	if err != nil {
		return nil, NewPoolErrorWithCause(ErrorTypeInternal, "INVALID_KEY_ID", "Invalid key ID format", err)
	}

	// 获取密钥详情
	details, err := p.getKeyDetails(uint(keyID))
	if err != nil {
		return nil, NewPoolErrorWithCause(ErrorTypeStorage, "KEY_DETAILS_FAILED", "Failed to get key details", err)
	}

	// 构造APIKey对象
	apiKey, err := p.buildAPIKeyFromDetails(uint(keyID), groupID, details)
	if err != nil {
		return nil, err
	}

	// 记录性能指标
	latency := time.Since(startTime)
	if p.metrics != nil {
		p.metrics.RecordKeySelection(groupID, latency, true)
	}

	// 发送事件
	if p.eventHandler != nil {
		event := &KeyPoolEvent{
			Type:      EventKeySelected,
			GroupID:   groupID,
			KeyID:     uint(keyID),
			PoolType:  PoolTypeActive,
			Message:   "Key selected successfully",
			Timestamp: time.Now(),
		}
		p.eventHandler.HandleEvent(event)
	}

	success = true
	return apiKey, nil
}

// ReturnKey 归还密钥并更新状态
func (p *RedisLayeredPool) ReturnKey(keyID uint, success bool) error {
	// 获取密钥详情以确定分组
	details, err := p.getKeyDetails(keyID)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "KEY_DETAILS_FAILED", "Failed to get key details", err)
	}

	groupIDStr, exists := details["group_id"]
	if !exists {
		return NewPoolError(ErrorTypeValidation, "MISSING_GROUP_ID", "Key details missing group_id")
	}

	groupID, err := strconv.ParseUint(groupIDStr, 10, 64)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeInternal, "INVALID_GROUP_ID", "Invalid group ID format", err)
	}

	if success {
		// 成功使用，将密钥放回活跃池
		activeKey := p.getRedisKey(uint(groupID), PoolTypeActive)
		if err := p.store.LPush(activeKey, keyID); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "RETURN_FAILED", "Failed to return key to active pool", err)
		}

		// 更新成功统计
		p.updateKeyStats(keyID, true)
	} else {
		// 使用失败，需要进一步处理
		return p.handleKeyFailure(keyID, uint(groupID))
	}

	// 发送事件
	if p.eventHandler != nil {
		event := &KeyPoolEvent{
			Type:      EventKeyReturned,
			GroupID:   uint(groupID),
			KeyID:     keyID,
			Message:   fmt.Sprintf("Key returned with success=%v", success),
			Timestamp: time.Now(),
		}
		p.eventHandler.HandleEvent(event)
	}

	return nil
}

// HandleRateLimit 处理429错误
func (p *RedisLayeredPool) HandleRateLimit(keyID uint, rateLimitErr *errors.RateLimitError) error {
	// 记录429错误
	if p.performanceMonitor != nil {
		p.performanceMonitor.RecordRateLimit()
	}

	// 获取密钥详情
	details, err := p.getKeyDetails(keyID)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "KEY_DETAILS_FAILED", "Failed to get key details", err)
	}

	groupIDStr, exists := details["group_id"]
	if !exists {
		return NewPoolError(ErrorTypeValidation, "MISSING_GROUP_ID", "Key details missing group_id")
	}

	groupID, err := strconv.ParseUint(groupIDStr, 10, 64)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeInternal, "INVALID_GROUP_ID", "Invalid group ID format", err)
	}

	// 从活跃池移除密钥
	activeKey := p.getRedisKey(uint(groupID), PoolTypeActive)
	if err := p.store.LRem(activeKey, 0, keyID); err != nil {
		logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to remove rate-limited key from active pool")
	}

	// 添加到冷却池
	resetAt := time.Now().Add(rateLimitErr.RetryAfter)
	if rateLimitErr.ResetAt != nil {
		resetAt = *rateLimitErr.ResetAt
	}

	if err := p.addToCoolingPool(uint(groupID), keyID, resetAt); err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "COOLING_FAILED", "Failed to add key to cooling pool", err)
	}

	// 更新密钥详情
	now := time.Now()
	updates := map[string]interface{}{
		"status":               models.KeyStatusRateLimited,
		"rate_limit_count":     p.incrementRateLimitCount(details),
		"last_429_at":          now.Unix(),
		"rate_limit_reset_at":  resetAt.Unix(),
	}

	if err := p.setKeyDetails(keyID, updates); err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "UPDATE_DETAILS_FAILED", "Failed to update key details", err)
	}

	// 记录指标
	if p.metrics != nil {
		p.metrics.RecordRateLimit(uint(groupID), keyID)
	}

	// 发送事件
	if p.eventHandler != nil {
		event := &KeyPoolEvent{
			Type:      EventRateLimitHit,
			GroupID:   uint(groupID),
			KeyID:     keyID,
			Message:   fmt.Sprintf("Key rate limited, reset at %v", resetAt),
			Timestamp: time.Now(),
			Metadata:  rateLimitErr,
		}
		p.eventHandler.HandleEvent(event)
	}

	logrus.WithFields(logrus.Fields{
		"keyID":      keyID,
		"groupID":    groupID,
		"retryAfter": rateLimitErr.RetryAfter,
		"resetAt":    resetAt,
	}).Info("Key moved to cooling pool due to rate limit")

	return nil
}

// buildAPIKeyFromDetails 从Redis详情构建APIKey对象
func (p *RedisLayeredPool) buildAPIKeyFromDetails(keyID, groupID uint, details map[string]string) (*models.APIKey, error) {
	keyValue, exists := details["key_value"]
	if !exists {
		return nil, NewPoolError(ErrorTypeValidation, "MISSING_KEY_VALUE", "Key details missing key_value")
	}

	status := details["status"]
	if status == "" {
		status = models.KeyStatusActive
	}

	// 解析数值字段
	requestCount, _ := strconv.ParseInt(details["request_count"], 10, 64)
	failureCount, _ := strconv.ParseInt(details["failure_count"], 10, 64)
	rateLimitCount, _ := strconv.ParseInt(details["rate_limit_count"], 10, 64)

	apiKey := &models.APIKey{
		ID:             keyID,
		KeyValue:       keyValue,
		GroupID:        groupID,
		Status:         status,
		RequestCount:   requestCount,
		FailureCount:   failureCount,
		RateLimitCount: rateLimitCount,
	}

	// 解析时间字段
	if createdAtStr := details["created_at"]; createdAtStr != "" {
		if createdAt, err := strconv.ParseInt(createdAtStr, 10, 64); err == nil {
			apiKey.CreatedAt = time.Unix(createdAt, 0)
		}
	}

	if lastUsedAtStr := details["last_used_at"]; lastUsedAtStr != "" {
		if lastUsedAt, err := strconv.ParseInt(lastUsedAtStr, 10, 64); err == nil {
			t := time.Unix(lastUsedAt, 0)
			apiKey.LastUsedAt = &t
		}
	}

	if last429AtStr := details["last_429_at"]; last429AtStr != "" {
		if last429At, err := strconv.ParseInt(last429AtStr, 10, 64); err == nil {
			t := time.Unix(last429At, 0)
			apiKey.Last429At = &t
		}
	}

	if resetAtStr := details["rate_limit_reset_at"]; resetAtStr != "" {
		if resetAt, err := strconv.ParseInt(resetAtStr, 10, 64); err == nil {
			t := time.Unix(resetAt, 0)
			apiKey.RateLimitResetAt = &t
		}
	}

	return apiKey, nil
}

// handleKeyFailure 处理密钥失败
func (p *RedisLayeredPool) handleKeyFailure(keyID, groupID uint) error {
	// 获取当前失败次数
	details, err := p.getKeyDetails(keyID)
	if err != nil {
		return err
	}

	failureCount, _ := strconv.ParseInt(details["failure_count"], 10, 64)
	newFailureCount := failureCount + 1

	// 获取分组配置
	var group models.Group
	if err := p.db.First(&group, groupID).Error; err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "GROUP_NOT_FOUND", "Failed to find group", err)
	}

	blacklistThreshold := group.EffectiveConfig.BlacklistThreshold

	updates := map[string]interface{}{
		"failure_count": newFailureCount,
	}

	// 检查是否需要拉黑
	if blacklistThreshold > 0 && newFailureCount >= int64(blacklistThreshold) {
		updates["status"] = models.KeyStatusInvalid

		// 从活跃池移除
		activeKey := p.getRedisKey(groupID, PoolTypeActive)
		if err := p.store.LRem(activeKey, 0, keyID); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to remove invalid key from active pool")
		}

		logrus.WithFields(logrus.Fields{
			"keyID":             keyID,
			"groupID":           groupID,
			"failureCount":      newFailureCount,
			"blacklistThreshold": blacklistThreshold,
		}).Info("Key blacklisted due to excessive failures")
	} else {
		// 失败但未达到拉黑阈值，放回活跃池
		activeKey := p.getRedisKey(groupID, PoolTypeActive)
		if err := p.store.LPush(activeKey, keyID); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "RETURN_FAILED", "Failed to return failed key to active pool", err)
		}
	}

	// 更新密钥详情
	if err := p.setKeyDetails(keyID, updates); err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "UPDATE_DETAILS_FAILED", "Failed to update key details", err)
	}

	return nil
}

// updateKeyStats 更新密钥统计
func (p *RedisLayeredPool) updateKeyStats(keyID uint, success bool) {
	details, err := p.getKeyDetails(keyID)
	if err != nil {
		return
	}

	requestCount, _ := strconv.ParseInt(details["request_count"], 10, 64)
	updates := map[string]interface{}{
		"request_count": requestCount + 1,
		"last_used_at":  time.Now().Unix(),
	}

	if success {
		// 成功时重置失败计数
		updates["failure_count"] = 0
	}

	p.setKeyDetails(keyID, updates)
}

// incrementRateLimitCount 增加429计数
func (p *RedisLayeredPool) incrementRateLimitCount(details map[string]string) int64 {
	rateLimitCount, _ := strconv.ParseInt(details["rate_limit_count"], 10, 64)
	return rateLimitCount + 1
}

// AddKeys 向指定池添加密钥
func (p *RedisLayeredPool) AddKeys(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}

	// 默认添加到验证池
	return p.addKeysToPool(groupID, keyIDs, PoolTypeValidation)
}

// addKeysToPool 向指定池添加密钥的内部方法
func (p *RedisLayeredPool) addKeysToPool(groupID uint, keyIDs []uint, poolType PoolType) error {
	if len(keyIDs) == 0 {
		return nil
	}

	// 首先从数据库获取密钥详情
	var keys []models.APIKey
	if err := p.db.Where("id IN ? AND group_id = ?", keyIDs, groupID).Find(&keys).Error; err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "DB_QUERY_FAILED", "Failed to query keys from database", err)
	}

	if len(keys) == 0 {
		return NewPoolError(ErrorTypeValidation, "NO_KEYS_FOUND", "No valid keys found in database")
	}

	// 验证密钥状态
	validKeyIDs := make([]uint, 0, len(keys))
	for _, key := range keys {
		if key.Status == models.KeyStatusActive || key.Status == models.KeyStatusRateLimited {
			validKeyIDs = append(validKeyIDs, key.ID)
		}
	}

	if len(validKeyIDs) == 0 {
		return NewPoolError(ErrorTypeValidation, "NO_VALID_KEYS", "No valid keys to add to pool")
	}

	// 根据池类型执行不同的添加逻辑
	switch poolType {
	case PoolTypeValidation:
		return p.addToValidationPool(groupID, validKeyIDs)
	case PoolTypeReady:
		return p.addToReadyPool(groupID, validKeyIDs)
	case PoolTypeActive:
		return p.addToActivePool(groupID, validKeyIDs)
	case PoolTypeCooling:
		// 冷却池需要特殊处理，不应该直接添加
		return NewPoolError(ErrorTypeValidation, "INVALID_POOL_TYPE", "Cannot directly add keys to cooling pool")
	default:
		return NewPoolError(ErrorTypeValidation, "UNKNOWN_POOL_TYPE", "Unknown pool type")
	}
}

// addToReadyPool 添加密钥到就绪池
func (p *RedisLayeredPool) addToReadyPool(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}

	readyKey := p.getRedisKey(groupID, PoolTypeReady)

	// 批量添加到就绪池
	for _, keyID := range keyIDs {
		if err := p.store.LPush(readyKey, keyID); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "LPUSH_FAILED", "Failed to add key to ready pool", err)
		}
	}

	// 更新密钥详情到Redis
	for _, keyID := range keyIDs {
		if err := p.syncKeyDetailsToRedis(keyID, groupID); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to sync key details to Redis")
		}
	}

	// 发送事件
	if p.eventHandler != nil {
		event := &KeyPoolEvent{
			Type:      EventPoolRefilled,
			GroupID:   groupID,
			PoolType:  PoolTypeReady,
			Message:   fmt.Sprintf("Added %d keys to ready pool", len(keyIDs)),
			Timestamp: time.Now(),
			Metadata:  keyIDs,
		}
		p.eventHandler.HandleEvent(event)
	}

	logrus.WithFields(logrus.Fields{
		"groupID": groupID,
		"count":   len(keyIDs),
		"pool":    PoolTypeReady,
	}).Info("Keys added to ready pool")

	return nil
}

// addToActivePool 添加密钥到活跃池
func (p *RedisLayeredPool) addToActivePool(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}

	activeKey := p.getRedisKey(groupID, PoolTypeActive)

	// 批量添加到活跃池
	for _, keyID := range keyIDs {
		if err := p.store.LPush(activeKey, keyID); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "LPUSH_FAILED", "Failed to add key to active pool", err)
		}
	}

	// 更新密钥详情到Redis
	for _, keyID := range keyIDs {
		if err := p.syncKeyDetailsToRedis(keyID, groupID); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to sync key details to Redis")
		}
	}

	// 发送事件
	if p.eventHandler != nil {
		event := &KeyPoolEvent{
			Type:      EventPoolRefilled,
			GroupID:   groupID,
			PoolType:  PoolTypeActive,
			Message:   fmt.Sprintf("Added %d keys to active pool", len(keyIDs)),
			Timestamp: time.Now(),
			Metadata:  keyIDs,
		}
		p.eventHandler.HandleEvent(event)
	}

	logrus.WithFields(logrus.Fields{
		"groupID": groupID,
		"count":   len(keyIDs),
		"pool":    PoolTypeActive,
	}).Info("Keys added to active pool")

	return nil
}

// syncKeyDetailsToRedis 同步密钥详情到Redis
func (p *RedisLayeredPool) syncKeyDetailsToRedis(keyID, groupID uint) error {
	var key models.APIKey
	if err := p.db.First(&key, keyID).Error; err != nil {
		return err
	}

	details := map[string]interface{}{
		"id":               keyID,
		"key_value":        key.KeyValue,
		"group_id":         groupID,
		"status":           key.Status,
		"request_count":    key.RequestCount,
		"failure_count":    key.FailureCount,
		"rate_limit_count": key.RateLimitCount,
		"created_at":       key.CreatedAt.Unix(),
	}

	if key.LastUsedAt != nil {
		details["last_used_at"] = key.LastUsedAt.Unix()
	}
	if key.Last429At != nil {
		details["last_429_at"] = key.Last429At.Unix()
	}
	if key.RateLimitResetAt != nil {
		details["rate_limit_reset_at"] = key.RateLimitResetAt.Unix()
	}

	return p.setKeyDetails(keyID, details)
}

// RemoveKeys 从指定池移除密钥
func (p *RedisLayeredPool) RemoveKeys(groupID uint, keyIDs []uint) error {
	if len(keyIDs) == 0 {
		return nil
	}

	// 从所有池中移除这些密钥
	poolTypes := []PoolType{PoolTypeValidation, PoolTypeReady, PoolTypeActive, PoolTypeCooling}

	for _, poolType := range poolTypes {
		if err := p.removeKeysFromPool(groupID, keyIDs, poolType); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID":  groupID,
				"poolType": poolType,
				"error":    err,
			}).Warn("Failed to remove keys from pool")
		}
	}

	// 删除密钥详情
	for _, keyID := range keyIDs {
		detailsKey := p.getKeyDetailsKey(keyID)
		if err := p.store.Delete(detailsKey); err != nil {
			logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to delete key details")
		}
	}

	// 发送事件
	if p.eventHandler != nil {
		event := &KeyPoolEvent{
			Type:      EventKeyMoved,
			GroupID:   groupID,
			Message:   fmt.Sprintf("Removed %d keys from all pools", len(keyIDs)),
			Timestamp: time.Now(),
			Metadata:  keyIDs,
		}
		p.eventHandler.HandleEvent(event)
	}

	logrus.WithFields(logrus.Fields{
		"groupID": groupID,
		"count":   len(keyIDs),
	}).Info("Keys removed from all pools")

	return nil
}

// removeKeysFromPool 从指定池移除密钥的内部方法
func (p *RedisLayeredPool) removeKeysFromPool(groupID uint, keyIDs []uint, poolType PoolType) error {
	if len(keyIDs) == 0 {
		return nil
	}

	switch poolType {
	case PoolTypeValidation:
		return p.removeFromValidationPool(groupID, keyIDs)
	case PoolTypeReady:
		return p.removeFromReadyPool(groupID, keyIDs)
	case PoolTypeActive:
		return p.removeFromActivePool(groupID, keyIDs)
	case PoolTypeCooling:
		return p.removeFromCoolingPool(groupID, keyIDs)
	default:
		return NewPoolError(ErrorTypeValidation, "UNKNOWN_POOL_TYPE", "Unknown pool type")
	}
}

// removeFromValidationPool 从验证池移除密钥
func (p *RedisLayeredPool) removeFromValidationPool(groupID uint, keyIDs []uint) error {
	validationKey := p.getRedisKey(groupID, PoolTypeValidation)

	// 转换为interface{}切片
	members := make([]interface{}, len(keyIDs))
	for i, keyID := range keyIDs {
		members[i] = keyID
	}

	return p.store.SRem(validationKey, members...)
}

// removeFromReadyPool 从就绪池移除密钥
func (p *RedisLayeredPool) removeFromReadyPool(groupID uint, keyIDs []uint) error {
	readyKey := p.getRedisKey(groupID, PoolTypeReady)

	for _, keyID := range keyIDs {
		if err := p.store.LRem(readyKey, 0, keyID); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "LREM_FAILED", "Failed to remove key from ready pool", err)
		}
	}

	return nil
}

// removeFromActivePool 从活跃池移除密钥
func (p *RedisLayeredPool) removeFromActivePool(groupID uint, keyIDs []uint) error {
	activeKey := p.getRedisKey(groupID, PoolTypeActive)

	for _, keyID := range keyIDs {
		if err := p.store.LRem(activeKey, 0, keyID); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "LREM_FAILED", "Failed to remove key from active pool", err)
		}
	}

	return nil
}

// MoveKey 在不同池之间移动密钥
func (p *RedisLayeredPool) MoveKey(keyID uint, fromPool, toPool PoolType) error {
	// 获取密钥详情以确定分组
	details, err := p.getKeyDetails(keyID)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "KEY_DETAILS_FAILED", "Failed to get key details", err)
	}

	groupIDStr, exists := details["group_id"]
	if !exists {
		return NewPoolError(ErrorTypeValidation, "MISSING_GROUP_ID", "Key details missing group_id")
	}

	groupID, err := strconv.ParseUint(groupIDStr, 10, 64)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeInternal, "INVALID_GROUP_ID", "Invalid group ID format", err)
	}

	// 验证池类型
	if fromPool == toPool {
		return NewPoolError(ErrorTypeValidation, "SAME_POOL", "Source and destination pools are the same")
	}

	// 特殊处理冷却池的移动
	if fromPool == PoolTypeCooling || toPool == PoolTypeCooling {
		return p.moveCoolingKey(keyID, uint(groupID), fromPool, toPool)
	}

	// 使用事务确保原子性
	return p.executeAtomicMove(keyID, uint(groupID), fromPool, toPool)
}

// executeAtomicMove 执行原子性的密钥移动
func (p *RedisLayeredPool) executeAtomicMove(keyID, groupID uint, fromPool, toPool PoolType) error {
	// 首先从源池移除
	if err := p.removeKeysFromPool(groupID, []uint{keyID}, fromPool); err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "REMOVE_FROM_SOURCE_FAILED",
			fmt.Sprintf("Failed to remove key from %s pool", fromPool), err)
	}

	// 然后添加到目标池
	if err := p.addKeysToPool(groupID, []uint{keyID}, toPool); err != nil {
		// 移动失败，尝试回滚到源池
		if rollbackErr := p.addKeysToPool(groupID, []uint{keyID}, fromPool); rollbackErr != nil {
			logrus.WithFields(logrus.Fields{
				"keyID":       keyID,
				"fromPool":    fromPool,
				"toPool":      toPool,
				"rollbackErr": rollbackErr,
			}).Error("Failed to rollback key move operation")
		}

		return NewPoolErrorWithCause(ErrorTypeStorage, "ADD_TO_TARGET_FAILED",
			fmt.Sprintf("Failed to add key to %s pool", toPool), err)
	}

	// 发送事件
	if p.eventHandler != nil {
		event := &KeyPoolEvent{
			Type:      EventKeyMoved,
			GroupID:   groupID,
			KeyID:     keyID,
			PoolType:  toPool,
			Message:   fmt.Sprintf("Key moved from %s to %s", fromPool, toPool),
			Timestamp: time.Now(),
			Metadata: map[string]interface{}{
				"fromPool": fromPool,
				"toPool":   toPool,
			},
		}
		p.eventHandler.HandleEvent(event)
	}

	logrus.WithFields(logrus.Fields{
		"keyID":    keyID,
		"groupID":  groupID,
		"fromPool": fromPool,
		"toPool":   toPool,
	}).Info("Key moved between pools")

	return nil
}

// moveCoolingKey 处理涉及冷却池的密钥移动
func (p *RedisLayeredPool) moveCoolingKey(keyID, groupID uint, fromPool, toPool PoolType) error {
	if fromPool == PoolTypeCooling {
		// 从冷却池移出
		if err := p.removeFromCoolingPool(groupID, []uint{keyID}); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "REMOVE_FROM_COOLING_FAILED",
				"Failed to remove key from cooling pool", err)
		}

		// 更新密钥状态为活跃
		updates := map[string]interface{}{
			"status":               models.KeyStatusActive,
			"rate_limit_reset_at":  nil,
		}
		if err := p.setKeyDetails(keyID, updates); err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "UPDATE_KEY_STATUS_FAILED",
				"Failed to update key status", err)
		}

		// 添加到目标池
		return p.addKeysToPool(groupID, []uint{keyID}, toPool)
	}

	if toPool == PoolTypeCooling {
		// 移入冷却池需要特殊处理
		return NewPoolError(ErrorTypeValidation, "INVALID_COOLING_MOVE",
			"Cannot directly move key to cooling pool, use HandleRateLimit instead")
	}

	return NewPoolError(ErrorTypeValidation, "INVALID_POOL_COMBINATION", "Invalid pool combination")
}

// ListKeys 列出指定池中的密钥
func (p *RedisLayeredPool) ListKeys(groupID uint, poolType PoolType) ([]uint, error) {
	switch poolType {
	case PoolTypeValidation:
		return p.listValidationKeys(groupID)
	case PoolTypeReady:
		return p.listReadyKeys(groupID)
	case PoolTypeActive:
		return p.listActiveKeys(groupID)
	case PoolTypeCooling:
		return p.listCoolingKeys(groupID)
	default:
		return nil, NewPoolError(ErrorTypeValidation, "UNKNOWN_POOL_TYPE", "Unknown pool type")
	}
}

// listValidationKeys 列出验证池中的密钥
func (p *RedisLayeredPool) listValidationKeys(groupID uint) ([]uint, error) {
	validationKey := p.getRedisKey(groupID, PoolTypeValidation)
	members, err := p.store.SMembers(validationKey)
	if err != nil {
		return nil, NewPoolErrorWithCause(ErrorTypeStorage, "SMEMBERS_FAILED", "Failed to list validation keys", err)
	}

	keyIDs := make([]uint, 0, len(members))
	for _, member := range members {
		if keyID, err := strconv.ParseUint(member, 10, 64); err == nil {
			keyIDs = append(keyIDs, uint(keyID))
		}
	}

	return keyIDs, nil
}

// listReadyKeys 列出就绪池中的密钥
func (p *RedisLayeredPool) listReadyKeys(groupID uint) ([]uint, error) {
	// 注意：这里需要实现LRANGE操作来获取LIST中的所有元素
	// 当前Store接口可能需要进一步扩展
	// 暂时返回空切片
	return []uint{}, nil
}

// listActiveKeys 列出活跃池中的密钥
func (p *RedisLayeredPool) listActiveKeys(groupID uint) ([]uint, error) {
	// 注意：这里需要实现LRANGE操作来获取LIST中的所有元素
	// 当前Store接口可能需要进一步扩展
	// 暂时返回空切片
	return []uint{}, nil
}

// listCoolingKeys 列出冷却池中的密钥
func (p *RedisLayeredPool) listCoolingKeys(groupID uint) ([]uint, error) {
	// 注意：这里需要实现ZRANGE操作来获取ZSET中的所有元素
	// 当前Store接口可能需要进一步扩展
	// 暂时返回空切片
	return []uint{}, nil
}

// RefillPools 智能池补充机制
func (p *RedisLayeredPool) RefillPools(groupID uint) error {
	config := p.getGroupConfig(groupID)

	// 检查活跃池是否需要补充
	activeCount, err := p.getPoolSize(groupID, PoolTypeActive)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "GET_ACTIVE_COUNT_FAILED", "Failed to get active pool size", err)
	}

	// 计算需要补充的数量
	needRefill := config.MinActiveKeys - int(activeCount)
	if needRefill <= 0 {
		return nil // 不需要补充
	}

	// 限制单次补充数量
	if needRefill > config.RefillBatchSize {
		needRefill = config.RefillBatchSize
	}

	// 尝试从就绪池补充到活跃池
	movedKeys, err := p.moveToActivePool(groupID, needRefill)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "MOVE_TO_ACTIVE_FAILED", "Failed to move keys to active pool", err)
	}

	actualMoved := len(movedKeys)
	if actualMoved > 0 {
		logrus.WithFields(logrus.Fields{
			"groupID":     groupID,
			"moved":       actualMoved,
			"needed":      needRefill,
			"activeCount": activeCount,
		}).Info("Refilled active pool from ready pool")

		// 记录指标
		if p.metrics != nil {
			p.metrics.RecordPoolRefill(groupID, actualMoved)
		}
	}

	// 检查就绪池是否需要补充
	readyCount, err := p.getPoolSize(groupID, PoolTypeReady)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeStorage, "GET_READY_COUNT_FAILED", "Failed to get ready pool size", err)
	}

	needReadyRefill := config.MinReadyKeys - int(readyCount)
	if needReadyRefill > 0 {
		// 从验证池补充到就绪池
		if err := p.refillReadyFromValidation(groupID, needReadyRefill); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": groupID,
				"error":   err,
			}).Warn("Failed to refill ready pool from validation pool")
		}

		// 如果验证池也不够，从数据库加载新密钥
		if err := p.refillFromDatabase(groupID, needReadyRefill); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": groupID,
				"error":   err,
			}).Warn("Failed to refill from database")
		}
	}

	return nil
}

// GetPoolStats 获取池统计信息
func (p *RedisLayeredPool) GetPoolStats(groupID uint) (*PoolStats, error) {
	stats := &PoolStats{
		GroupID:      groupID,
		PoolCounts:   make(map[PoolType]int),
		StatusCounts: make(map[KeyStatus]int),
		LastUpdated:  time.Now(),
	}

	// 获取各池的大小
	poolTypes := []PoolType{PoolTypeValidation, PoolTypeReady, PoolTypeActive, PoolTypeCooling}
	totalKeys := 0

	for _, poolType := range poolTypes {
		count, err := p.getPoolSize(groupID, poolType)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID":  groupID,
				"poolType": poolType,
				"error":    err,
			}).Warn("Failed to get pool size")
			continue
		}

		stats.PoolCounts[poolType] = int(count)
		totalKeys += int(count)
	}

	stats.TotalKeys = totalKeys

	// 获取性能统计
	if p.metrics != nil {
		perfStats, err := p.metrics.GetMetrics(groupID)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": groupID,
				"error":   err,
			}).Warn("Failed to get performance metrics")
		} else {
			stats.Performance = perfStats
		}
	}

	// 统计密钥状态
	if err := p.collectKeyStatusStats(groupID, stats); err != nil {
		logrus.WithFields(logrus.Fields{
			"groupID": groupID,
			"error":   err,
		}).Warn("Failed to collect key status statistics")
	}

	// 更新Redis中的统计信息
	if err := p.updatePoolStatsInRedis(groupID, stats); err != nil {
		logrus.WithFields(logrus.Fields{
			"groupID": groupID,
			"error":   err,
		}).Warn("Failed to update pool stats in Redis")
	}

	return stats, nil
}

// collectKeyStatusStats 收集密钥状态统计
func (p *RedisLayeredPool) collectKeyStatusStats(groupID uint, stats *PoolStats) error {
	// 从数据库查询密钥状态统计
	var statusCounts []struct {
		Status string
		Count  int
	}

	if err := p.db.Model(&models.APIKey{}).
		Select("status, count(*) as count").
		Where("group_id = ?", groupID).
		Group("status").
		Scan(&statusCounts).Error; err != nil {
		return err
	}

	// 映射到KeyStatus
	for _, sc := range statusCounts {
		var keyStatus KeyStatus
		switch sc.Status {
		case models.KeyStatusActive:
			keyStatus = KeyStatusInUse
		case models.KeyStatusRateLimited:
			keyStatus = KeyStatusCooling
		case models.KeyStatusInvalid:
			keyStatus = KeyStatusFailed
		default:
			keyStatus = KeyStatusPending
		}

		stats.StatusCounts[keyStatus] = sc.Count
	}

	return nil
}

// updatePoolStatsInRedis 更新Redis中的池统计
func (p *RedisLayeredPool) updatePoolStatsInRedis(groupID uint, stats *PoolStats) error {
	statsData := map[string]interface{}{
		"total_keys":   stats.TotalKeys,
		"last_updated": stats.LastUpdated.Unix(),
	}

	// 添加池计数
	for poolType, count := range stats.PoolCounts {
		statsData[fmt.Sprintf("pool_%s", poolType)] = count
	}

	// 添加状态计数
	for status, count := range stats.StatusCounts {
		statsData[fmt.Sprintf("status_%s", status)] = count
	}

	// 添加性能指标
	if stats.Performance != nil {
		statsData["select_latency_ms"] = stats.Performance.SelectLatency.Milliseconds()
		statsData["hit_rate"] = stats.Performance.HitRate
		statsData["refill_rate"] = stats.Performance.RefillRate
		statsData["recovery_rate"] = stats.Performance.RecoveryRate
		statsData["rate_limit_rate"] = stats.Performance.RateLimitRate
		statsData["avg_cooling_time_ms"] = stats.Performance.AvgCoolingTime.Milliseconds()
	}

	return p.updatePoolStats(groupID, statsData)
}

// initializeRecoveryComponents 初始化恢复组件
func (p *RedisLayeredPool) initializeRecoveryComponents() error {
	// 创建恢复策略
	if p.recoveryStrategy == nil {
		p.recoveryStrategy = NewSmartRecoveryStrategy(nil)
	}

	// 创建恢复监控器
	if p.recoveryMonitor == nil {
		p.recoveryMonitor = NewRecoveryMonitor(nil)
	}

	// 创建恢复服务
	if p.recoveryService == nil {
		p.recoveryService = NewRecoveryService(
			p.db,
			p, // 传入自身作为LayeredKeyPool
			p.recoveryStrategy,
			nil, // 使用默认配置
		)
	}

	// 创建批量恢复处理器
	if p.batchProcessor == nil {
		calculator := NewDynamicRecoveryCalculator(nil)
		p.batchProcessor = NewBatchRecoveryProcessor(
			p.db,
			p, // 传入自身作为LayeredKeyPool
			calculator,
			nil, // 使用默认配置
		)
	}

	// 创建性能监控器
	if p.performanceMonitor == nil {
		p.performanceMonitor = NewPerformanceMonitor(nil)
	}

	return nil
}

// StartRecoveryServices 启动恢复服务
func (p *RedisLayeredPool) StartRecoveryServices() error {
	if p.recoveryMonitor != nil {
		if err := p.recoveryMonitor.Start(); err != nil {
			return NewPoolErrorWithCause(ErrorTypeInternal, "MONITOR_START_FAILED", "Failed to start recovery monitor", err)
		}
	}

	if p.recoveryService != nil {
		if err := p.recoveryService.Start(); err != nil {
			return NewPoolErrorWithCause(ErrorTypeInternal, "SERVICE_START_FAILED", "Failed to start recovery service", err)
		}
	}

	if p.batchProcessor != nil {
		if err := p.batchProcessor.Start(); err != nil {
			return NewPoolErrorWithCause(ErrorTypeInternal, "BATCH_PROCESSOR_START_FAILED", "Failed to start batch processor", err)
		}
	}

	if p.performanceMonitor != nil {
		if err := p.performanceMonitor.Start(); err != nil {
			return NewPoolErrorWithCause(ErrorTypeInternal, "PERFORMANCE_MONITOR_START_FAILED", "Failed to start performance monitor", err)
		}
	}

	logrus.Info("Recovery services started for Redis layered pool")
	return nil
}

// StopRecoveryServices 停止恢复服务
func (p *RedisLayeredPool) StopRecoveryServices() error {
	var errors []error

	if p.batchProcessor != nil {
		if err := p.batchProcessor.Stop(); err != nil {
			errors = append(errors, err)
		}
	}

	if p.recoveryService != nil {
		if err := p.recoveryService.Stop(); err != nil {
			errors = append(errors, err)
		}
	}

	if p.recoveryMonitor != nil {
		if err := p.recoveryMonitor.Stop(); err != nil {
			errors = append(errors, err)
		}
	}

	if p.performanceMonitor != nil {
		if err := p.performanceMonitor.Stop(); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to stop some recovery services: %v", errors)
	}

	logrus.Info("Recovery services stopped for Redis layered pool")
	return nil
}

// GetRecoveryMetrics 获取恢复指标
func (p *RedisLayeredPool) GetRecoveryMetrics() *RecoveryMonitorMetrics {
	if p.recoveryMonitor != nil {
		return p.recoveryMonitor.GetMetrics()
	}
	return nil
}

// GetPerformanceMetrics 获取性能指标
func (p *RedisLayeredPool) GetPerformanceMetrics() *PerformanceMetrics {
	if p.performanceMonitor != nil {
		return p.performanceMonitor.GetMetrics()
	}
	return nil
}

// GetPerformanceTimeSeries 获取性能时间序列数据
func (p *RedisLayeredPool) GetPerformanceTimeSeries() *TimeSeriesData {
	if p.performanceMonitor != nil {
		return p.performanceMonitor.GetTimeSeries()
	}
	return nil
}

// TriggerManualRecovery 触发手动恢复
func (p *RedisLayeredPool) TriggerManualRecovery(groupID uint, keyIDs []uint) error {
	if p.recoveryService == nil {
		return NewPoolError(ErrorTypeConfiguration, "NO_RECOVERY_SERVICE", "Recovery service not initialized")
	}

	// 创建恢复计划
	var plans []*RecoveryPlan
	for _, keyID := range keyIDs {
		// 获取密钥信息
		var key models.APIKey
		if err := p.db.First(&key, keyID).Error; err != nil {
			logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to load key for manual recovery")
			continue
		}

		// 获取分组信息
		var group models.Group
		if err := p.db.First(&group, groupID).Error; err != nil {
			return NewPoolErrorWithCause(ErrorTypeStorage, "GROUP_NOT_FOUND", "Failed to find group", err)
		}

		// 创建恢复计划
		plan, err := p.recoveryStrategy.CreateRecoveryPlan(&key, &group, nil)
		if err != nil {
			logrus.WithFields(logrus.Fields{"keyID": keyID, "error": err}).Warn("Failed to create recovery plan")
			continue
		}

		// 立即调度
		plan.ScheduledAt = time.Now()
		plans = append(plans, plan)
	}

	if len(plans) == 0 {
		return NewPoolError(ErrorTypeValidation, "NO_VALID_PLANS", "No valid recovery plans created")
	}

	// 创建批次并提交
	batches, err := p.batchProcessor.CreateRecoveryBatches(plans)
	if err != nil {
		return NewPoolErrorWithCause(ErrorTypeInternal, "BATCH_CREATION_FAILED", "Failed to create recovery batches", err)
	}

	for _, batch := range batches {
		if err := p.batchProcessor.SubmitBatch(batch); err != nil {
			logrus.WithFields(logrus.Fields{"batchID": batch.ID, "error": err}).Warn("Failed to submit recovery batch")
		}
	}

	logrus.WithFields(logrus.Fields{
		"groupID":    groupID,
		"keyCount":   len(keyIDs),
		"planCount":  len(plans),
		"batchCount": len(batches),
	}).Info("Manual recovery triggered")

	return nil
}

package keypool

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ConfigApplier 配置应用器
type ConfigApplier struct {
	// 池管理器
	poolManager *PoolManager
	
	// 当前应用的配置
	appliedConfigs map[uint]*OptimalConfig
	mu             sync.RWMutex
	
	// 应用历史
	applicationHistory []*ConfigApplication
}

// ConfigApplication 配置应用记录
type ConfigApplication struct {
	GroupID     uint           `json:"group_id"`
	Config      *OptimalConfig `json:"config"`
	AppliedAt   time.Time      `json:"applied_at"`
	Success     bool           `json:"success"`
	Error       string         `json:"error,omitempty"`
	Rollback    bool           `json:"rollback"`
}

// NewConfigApplier 创建配置应用器
func NewConfigApplier(poolManager *PoolManager) *ConfigApplier {
	return &ConfigApplier{
		poolManager:        poolManager,
		appliedConfigs:     make(map[uint]*OptimalConfig),
		applicationHistory: make([]*ConfigApplication, 0),
	}
}

// ApplyOptimalConfig 应用最优配置到指定分组
func (ca *ConfigApplier) ApplyOptimalConfig(groupID uint, config *OptimalConfig) error {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	
	startTime := time.Now()
	
	// 获取池实例
	pool, err := ca.poolManager.GetPool(groupID)
	if err != nil {
		ca.recordApplication(groupID, config, false, fmt.Sprintf("Failed to get pool: %v", err), false)
		return fmt.Errorf("failed to get pool for group %d: %w", groupID, err)
	}
	
	// 备份当前配置
	var previousConfig *PoolConfig
	if currentConfig, err := pool.GetConfig(groupID); err == nil {
		previousConfig = currentConfig
	}
	
	// 转换为池配置
	poolConfig := ca.convertToPoolConfig(config)
	
	// 应用配置
	if err := pool.UpdateConfig(groupID, poolConfig); err != nil {
		ca.recordApplication(groupID, config, false, fmt.Sprintf("Failed to update config: %v", err), false)
		return fmt.Errorf("failed to apply config to pool: %w", err)
	}
	
	// 应用分片存储配置（如果是Memory池）
	if memoryPool, ok := pool.(*MemoryLayeredPool); ok {
		if err := ca.applyMemoryPoolConfig(memoryPool, config); err != nil {
			// 尝试回滚
			if previousConfig != nil {
				pool.UpdateConfig(groupID, previousConfig)
			}
			ca.recordApplication(groupID, config, false, fmt.Sprintf("Failed to apply memory config: %v", err), true)
			return fmt.Errorf("failed to apply memory pool config: %w", err)
		}
	}
	
	// 应用Redis池配置（如果是Redis池）
	if redisPool, ok := pool.(*RedisLayeredPool); ok {
		if err := ca.applyRedisPoolConfig(redisPool, config); err != nil {
			// 尝试回滚
			if previousConfig != nil {
				pool.UpdateConfig(groupID, previousConfig)
			}
			ca.recordApplication(groupID, config, false, fmt.Sprintf("Failed to apply redis config: %v", err), true)
			return fmt.Errorf("failed to apply redis pool config: %w", err)
		}
	}
	
	// 记录成功应用
	ca.appliedConfigs[groupID] = config
	ca.recordApplication(groupID, config, true, "", false)
	
	logrus.WithFields(logrus.Fields{
		"groupID":     groupID,
		"shardCount":  config.ShardCount,
		"cacheSize":   config.CacheSize,
		"batchSize":   config.BatchSize,
		"duration":    time.Since(startTime),
	}).Info("Optimal configuration applied successfully")
	
	return nil
}

// convertToPoolConfig 转换为池配置
func (ca *ConfigApplier) convertToPoolConfig(optimal *OptimalConfig) *PoolConfig {
	return &PoolConfig{
		// 池大小配置
		ValidationPoolSize: optimal.CacheSize / 4,
		ReadyPoolSize:      optimal.CacheSize / 2,
		ActivePoolSize:     optimal.CacheSize / 4,
		
		// 批量操作配置
		BatchSize:          optimal.BatchSize,
		MaxBatchSize:       optimal.BatchSize * 2,
		BatchTimeout:       optimal.SelectTimeout,
		
		// 并发配置
		MaxConcurrency:     optimal.MaxConcurrency,
		WorkerPoolSize:     optimal.WorkerPoolSize,
		QueueSize:          optimal.QueueSize,
		
		// 超时配置
		SelectTimeout:      optimal.SelectTimeout,
		ReturnTimeout:      optimal.ReturnTimeout,
		RecoveryTimeout:    optimal.RecoveryTimeout,
		
		// 恢复配置
		RecoveryInterval:   5 * time.Minute,
		RecoveryBatchSize:  optimal.BatchSize,
		MaxRecoveryRetries: 3,
		
		// 监控配置
		EnableMetrics:      true,
		MetricsInterval:    30 * time.Second,
		
		// 健康检查配置
		HealthCheckInterval: 1 * time.Minute,
		HealthCheckTimeout:  10 * time.Second,
	}
}

// applyMemoryPoolConfig 应用内存池配置
func (ca *ConfigApplier) applyMemoryPoolConfig(pool *MemoryLayeredPool, config *OptimalConfig) error {
	// 更新分片存储配置
	if pool.shardedStore != nil {
		shardedConfig := &ShardedStoreConfig{
			ShardCount:     config.ShardCount,
			LockTimeout:    1 * time.Second,
			GCInterval:     10 * time.Minute,
			MaxMemoryUsage: config.AvailableMemory * 1024 * 1024, // 转换为字节
			EnableMetrics:  true,
			CacheSize:      config.CacheSize,
		}
		
		// 重新配置分片存储
		if err := pool.shardedStore.Reconfigure(shardedConfig); err != nil {
			return fmt.Errorf("failed to reconfigure sharded store: %w", err)
		}
	}
	
	// 更新本地缓存配置
	if pool.localCache != nil {
		cacheConfig := &LocalCacheConfig{
			MaxSize:     config.CacheSize,
			TTL:         30 * time.Minute,
			CleanupInterval: 5 * time.Minute,
			EnableMetrics: true,
		}
		
		if err := pool.localCache.UpdateConfig(cacheConfig); err != nil {
			return fmt.Errorf("failed to update cache config: %w", err)
		}
	}
	
	return nil
}

// applyRedisPoolConfig 应用Redis池配置
func (ca *ConfigApplier) applyRedisPoolConfig(pool *RedisLayeredPool, config *OptimalConfig) error {
	// 更新Redis配置
	redisConfig := &RedisPoolConfig{
		KeyPrefix:      pool.redisConfig.KeyPrefix, // 保持原有前缀
		DefaultTTL:     3600,
		EnablePipeline: true,
		PipelineSize:   config.BatchSize,
		EnableMetrics:  true,
		MaxRetries:     3,
		RetryDelay:     100 * time.Millisecond,
		PoolSize:       config.MaxConcurrency,
		MinIdleConns:   config.MaxConcurrency / 4,
		MaxConnAge:     30 * time.Minute,
		IdleTimeout:    5 * time.Minute,
	}
	
	// 应用Redis配置
	pool.redisConfig = redisConfig
	
	return nil
}

// ApplyToAllGroups 应用配置到所有分组
func (ca *ConfigApplier) ApplyToAllGroups(config *OptimalConfig) error {
	// 获取所有分组
	groups, err := ca.poolManager.GetAllGroups()
	if err != nil {
		return fmt.Errorf("failed to get groups: %w", err)
	}
	
	var errors []error
	successCount := 0
	
	for _, group := range groups {
		if err := ca.ApplyOptimalConfig(group.ID, config); err != nil {
			errors = append(errors, fmt.Errorf("group %d: %w", group.ID, err))
		} else {
			successCount++
		}
	}
	
	logrus.WithFields(logrus.Fields{
		"totalGroups":   len(groups),
		"successCount":  successCount,
		"failureCount":  len(errors),
	}).Info("Batch configuration application completed")
	
	if len(errors) > 0 {
		return fmt.Errorf("failed to apply config to %d groups: %v", len(errors), errors)
	}
	
	return nil
}

// RollbackConfig 回滚配置
func (ca *ConfigApplier) RollbackConfig(groupID uint) error {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	
	// 查找上一个成功的配置
	var previousConfig *OptimalConfig
	for i := len(ca.applicationHistory) - 1; i >= 0; i-- {
		app := ca.applicationHistory[i]
		if app.GroupID == groupID && app.Success && !app.Rollback {
			previousConfig = app.Config
			break
		}
	}
	
	if previousConfig == nil {
		return fmt.Errorf("no previous configuration found for group %d", groupID)
	}
	
	// 应用上一个配置
	pool, err := ca.poolManager.GetPool(groupID)
	if err != nil {
		return fmt.Errorf("failed to get pool for rollback: %w", err)
	}
	
	poolConfig := ca.convertToPoolConfig(previousConfig)
	if err := pool.UpdateConfig(groupID, poolConfig); err != nil {
		ca.recordApplication(groupID, previousConfig, false, fmt.Sprintf("Rollback failed: %v", err), true)
		return fmt.Errorf("failed to rollback config: %w", err)
	}
	
	// 记录回滚
	ca.recordApplication(groupID, previousConfig, true, "Configuration rolled back", true)
	
	logrus.WithFields(logrus.Fields{
		"groupID": groupID,
	}).Info("Configuration rolled back successfully")
	
	return nil
}

// recordApplication 记录配置应用
func (ca *ConfigApplier) recordApplication(groupID uint, config *OptimalConfig, success bool, errorMsg string, rollback bool) {
	application := &ConfigApplication{
		GroupID:   groupID,
		Config:    config,
		AppliedAt: time.Now(),
		Success:   success,
		Error:     errorMsg,
		Rollback:  rollback,
	}
	
	ca.applicationHistory = append(ca.applicationHistory, application)
	
	// 限制历史记录数量
	if len(ca.applicationHistory) > 1000 {
		ca.applicationHistory = ca.applicationHistory[len(ca.applicationHistory)-1000:]
	}
}

// GetAppliedConfig 获取已应用的配置
func (ca *ConfigApplier) GetAppliedConfig(groupID uint) (*OptimalConfig, bool) {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	
	config, exists := ca.appliedConfigs[groupID]
	return config, exists
}

// GetApplicationHistory 获取应用历史
func (ca *ConfigApplier) GetApplicationHistory(groupID uint) []*ConfigApplication {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	
	var history []*ConfigApplication
	for _, app := range ca.applicationHistory {
		if groupID == 0 || app.GroupID == groupID {
			history = append(history, app)
		}
	}
	
	return history
}

// ValidateConfig 验证配置
func (ca *ConfigApplier) ValidateConfig(config *OptimalConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	
	// 验证分片数量
	if config.ShardCount <= 0 || config.ShardCount > 1024 {
		return fmt.Errorf("invalid shard count: %d (must be 1-1024)", config.ShardCount)
	}
	
	// 验证缓存大小
	if config.CacheSize <= 0 || config.CacheSize > 1000000 {
		return fmt.Errorf("invalid cache size: %d (must be 1-1000000)", config.CacheSize)
	}
	
	// 验证批量大小
	if config.BatchSize <= 0 || config.BatchSize > 10000 {
		return fmt.Errorf("invalid batch size: %d (must be 1-10000)", config.BatchSize)
	}
	
	// 验证并发数
	if config.MaxConcurrency <= 0 || config.MaxConcurrency > 10000 {
		return fmt.Errorf("invalid max concurrency: %d (must be 1-10000)", config.MaxConcurrency)
	}
	
	// 验证超时配置
	if config.SelectTimeout <= 0 || config.SelectTimeout > 5*time.Minute {
		return fmt.Errorf("invalid select timeout: %v (must be > 0 and <= 5m)", config.SelectTimeout)
	}
	
	return nil
}

// GetStats 获取应用器统计信息
func (ca *ConfigApplier) GetStats() map[string]interface{} {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	
	successCount := 0
	failureCount := 0
	rollbackCount := 0
	
	for _, app := range ca.applicationHistory {
		if app.Rollback {
			rollbackCount++
		} else if app.Success {
			successCount++
		} else {
			failureCount++
		}
	}
	
	return map[string]interface{}{
		"applied_groups":     len(ca.appliedConfigs),
		"total_applications": len(ca.applicationHistory),
		"success_count":      successCount,
		"failure_count":      failureCount,
		"rollback_count":     rollbackCount,
	}
}

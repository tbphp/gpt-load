package keypool

import (
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"sync"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// PoolManager 池管理器
type PoolManager struct {
	db          *gorm.DB
	redisStore  store.Store
	config      *config.Config

	// 池实例缓存
	pools       map[uint]LayeredKeyPool
	poolsMu     sync.RWMutex

	// 默认池类型
	defaultPoolType PoolType

	// 性能优化组件
	optimizer     *PerformanceOptimizer
	configApplier *ConfigApplier
}

// PoolType 池类型
type PoolType string

const (
	PoolTypeRedis  PoolType = "redis"
	PoolTypeMemory PoolType = "memory"
)

// NewPoolManager 创建池管理器
func NewPoolManager(db *gorm.DB, redisStore store.Store, config *config.Config) *PoolManager {
	defaultType := PoolTypeMemory
	if redisStore != nil {
		defaultType = PoolTypeRedis
	}

	manager := &PoolManager{
		db:              db,
		redisStore:      redisStore,
		config:          config,
		pools:           make(map[uint]LayeredKeyPool),
		defaultPoolType: defaultType,
	}

	// 初始化性能优化组件
	manager.configApplier = NewConfigApplier(manager)

	// 创建性能优化器（需要一个性能监控器）
	// 这里我们创建一个全局的性能监控器
	globalMonitor := NewPerformanceMonitor(nil)
	manager.optimizer = NewPerformanceOptimizer(nil, globalMonitor)

	return manager
}

// GetPool 获取指定分组的池实例
func (pm *PoolManager) GetPool(groupID uint) (LayeredKeyPool, error) {
	pm.poolsMu.RLock()
	if pool, exists := pm.pools[groupID]; exists {
		pm.poolsMu.RUnlock()
		return pool, nil
	}
	pm.poolsMu.RUnlock()

	// 创建新的池实例
	return pm.createPool(groupID)
}

// createPool 创建池实例
func (pm *PoolManager) createPool(groupID uint) (LayeredKeyPool, error) {
	pm.poolsMu.Lock()
	defer pm.poolsMu.Unlock()

	// 双重检查
	if pool, exists := pm.pools[groupID]; exists {
		return pool, nil
	}

	// 获取分组信息
	var group models.Group
	if err := pm.db.First(&group, groupID).Error; err != nil {
		return nil, fmt.Errorf("failed to find group %d: %w", groupID, err)
	}

	// 确定池类型
	poolType := pm.determinePoolType(&group)

	var pool LayeredKeyPool
	var err error

	switch poolType {
	case PoolTypeRedis:
		pool, err = pm.createRedisPool(&group)
	case PoolTypeMemory:
		pool, err = pm.createMemoryPool(&group)
	default:
		return nil, fmt.Errorf("unsupported pool type: %s", poolType)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create %s pool for group %d: %w", poolType, groupID, err)
	}

	// 缓存池实例
	pm.pools[groupID] = pool

	logrus.WithFields(logrus.Fields{
		"groupID":   groupID,
		"groupName": group.Name,
		"poolType":  poolType,
	}).Info("Created pool instance")

	return pool, nil
}

// createRedisPool 创建Redis池
func (pm *PoolManager) createRedisPool(group *models.Group) (LayeredKeyPool, error) {
	if pm.redisStore == nil {
		return nil, fmt.Errorf("redis store not available")
	}

	// 创建Redis池配置
	redisConfig := &RedisPoolConfig{
		KeyPrefix:      fmt.Sprintf("pool:%d:", group.ID),
		DefaultTTL:     3600, // 1小时
		EnablePipeline: true,
		PipelineSize:   100,
		EnableMetrics:  true,
	}

	// 创建Redis池实例
	pool, err := NewRedisLayeredPool(pm.db, pm.redisStore, redisConfig)
	if err != nil {
		return nil, err
	}

	// 启动恢复服务
	if err := pool.StartRecoveryServices(); err != nil {
		logrus.WithError(err).Warn("Failed to start recovery services for Redis pool")
	}

	return pool, nil
}

// createMemoryPool 创建内存池
func (pm *PoolManager) createMemoryPool(group *models.Group) (LayeredKeyPool, error) {
	// 创建内存池配置
	memoryConfig := &MemoryPoolConfig{
		ShardCount:     8,
		EnableSharding: true,
		LockTimeout:    1000, // 1秒
		GCInterval:     600,  // 10分钟
		MaxMemoryUsage: 100 * 1024 * 1024, // 100MB
	}

	// 创建内存池实例
	pool, err := NewMemoryLayeredPool(pm.db, memoryConfig)
	if err != nil {
		return nil, err
	}

	// 启动恢复服务
	if err := pool.StartRecoveryServices(); err != nil {
		logrus.WithError(err).Warn("Failed to start recovery services for Memory pool")
	}

	return pool, nil
}

// determinePoolType 确定池类型
func (pm *PoolManager) determinePoolType(group *models.Group) PoolType {
	// 检查分组配置中是否指定了池类型
	if poolTypeStr, exists := group.Config["pool_type"]; exists {
		if poolType, ok := poolTypeStr.(string); ok {
			switch poolType {
			case "redis":
				if pm.redisStore != nil {
					return PoolTypeRedis
				}
				logrus.WithField("groupID", group.ID).Warn("Redis pool requested but Redis store not available, falling back to memory pool")
			case "memory":
				return PoolTypeMemory
			}
		}
	}

	// 使用默认池类型
	return pm.defaultPoolType
}

// InitializePools 初始化所有分组的池
func (pm *PoolManager) InitializePools() error {
	var groups []models.Group
	if err := pm.db.Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to query groups: %w", err)
	}

	for _, group := range groups {
		if _, err := pm.GetPool(group.ID); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID":   group.ID,
				"groupName": group.Name,
				"error":     err,
			}).Error("Failed to initialize pool")
		}
	}

	logrus.WithField("poolCount", len(pm.pools)).Info("Pool initialization completed")
	return nil
}

// RefreshPool 刷新指定分组的池
func (pm *PoolManager) RefreshPool(groupID uint) error {
	pm.poolsMu.Lock()
	defer pm.poolsMu.Unlock()

	// 停止现有池的恢复服务
	if existingPool, exists := pm.pools[groupID]; exists {
		if redisPool, ok := existingPool.(*RedisLayeredPool); ok {
			redisPool.StopRecoveryServices()
		} else if memoryPool, ok := existingPool.(*MemoryLayeredPool); ok {
			memoryPool.StopRecoveryServices()
		}
		delete(pm.pools, groupID)
	}

	// 重新创建池
	_, err := pm.createPool(groupID)
	return err
}

// GetPoolStats 获取所有池的统计信息
func (pm *PoolManager) GetPoolStats() map[uint]*PoolStats {
	pm.poolsMu.RLock()
	defer pm.poolsMu.RUnlock()

	stats := make(map[uint]*PoolStats)

	for groupID, pool := range pm.pools {
		if poolStats, err := pool.GetStats(groupID); err == nil {
			stats[groupID] = poolStats
		}
	}

	return stats
}

// GetPoolTypes 获取所有池的类型信息
func (pm *PoolManager) GetPoolTypes() map[uint]string {
	pm.poolsMu.RLock()
	defer pm.poolsMu.RUnlock()

	types := make(map[uint]string)

	for groupID, pool := range pm.pools {
		switch pool.(type) {
		case *RedisLayeredPool:
			types[groupID] = "redis"
		case *MemoryLayeredPool:
			types[groupID] = "memory"
		default:
			types[groupID] = "unknown"
		}
	}

	return types
}

// StopAllPools 停止所有池的服务
func (pm *PoolManager) StopAllPools() error {
	pm.poolsMu.Lock()
	defer pm.poolsMu.Unlock()

	var errors []error

	for groupID, pool := range pm.pools {
		if redisPool, ok := pool.(*RedisLayeredPool); ok {
			if err := redisPool.StopRecoveryServices(); err != nil {
				errors = append(errors, fmt.Errorf("failed to stop Redis pool %d: %w", groupID, err))
			}
		} else if memoryPool, ok := pool.(*MemoryLayeredPool); ok {
			if err := memoryPool.StopRecoveryServices(); err != nil {
				errors = append(errors, fmt.Errorf("failed to stop Memory pool %d: %w", groupID, err))
			}
		}
	}

	// 清空池缓存
	pm.pools = make(map[uint]LayeredKeyPool)

	if len(errors) > 0 {
		return fmt.Errorf("errors stopping pools: %v", errors)
	}

	logrus.Info("All pools stopped successfully")
	return nil
}

// GetPoolCount 获取池数量
func (pm *PoolManager) GetPoolCount() int {
	pm.poolsMu.RLock()
	defer pm.poolsMu.RUnlock()
	return len(pm.pools)
}

// IsPoolActive 检查池是否活跃
func (pm *PoolManager) IsPoolActive(groupID uint) bool {
	pm.poolsMu.RLock()
	defer pm.poolsMu.RUnlock()
	_, exists := pm.pools[groupID]
	return exists
}

// GetDefaultPoolType 获取默认池类型
func (pm *PoolManager) GetDefaultPoolType() PoolType {
	return pm.defaultPoolType
}

// SetDefaultPoolType 设置默认池类型
func (pm *PoolManager) SetDefaultPoolType(poolType PoolType) {
	pm.defaultPoolType = poolType
	logrus.WithField("poolType", poolType).Info("Default pool type updated")
}

// HealthCheck 健康检查
func (pm *PoolManager) HealthCheck() map[uint]bool {
	pm.poolsMu.RLock()
	defer pm.poolsMu.RUnlock()

	health := make(map[uint]bool)

	for groupID, pool := range pm.pools {
		// 简单的健康检查：尝试获取统计信息
		_, err := pool.GetStats(groupID)
		health[groupID] = (err == nil)
	}

	return health
}

// GetAllGroups 获取所有分组（用于批量操作）
func (pm *PoolManager) GetAllGroups() ([]*models.Group, error) {
	var groups []*models.Group
	if err := pm.db.Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("failed to get all groups: %w", err)
	}
	return groups, nil
}

// StartOptimizer 启动性能优化器
func (pm *PoolManager) StartOptimizer() error {
	if pm.optimizer != nil {
		return pm.optimizer.Start()
	}
	return nil
}

// StopOptimizer 停止性能优化器
func (pm *PoolManager) StopOptimizer() error {
	if pm.optimizer != nil {
		return pm.optimizer.Stop()
	}
	return nil
}

// GetOptimalConfig 获取当前最优配置
func (pm *PoolManager) GetOptimalConfig() *OptimalConfig {
	if pm.optimizer != nil {
		return pm.optimizer.GetCurrentConfig()
	}
	return nil
}

// ApplyOptimalConfig 应用最优配置到指定分组
func (pm *PoolManager) ApplyOptimalConfig(groupID uint, config *OptimalConfig) error {
	if pm.configApplier != nil {
		return pm.configApplier.ApplyOptimalConfig(groupID, config)
	}
	return fmt.Errorf("config applier not initialized")
}

// ApplyOptimalConfigToAll 应用最优配置到所有分组
func (pm *PoolManager) ApplyOptimalConfigToAll(config *OptimalConfig) error {
	if pm.configApplier != nil {
		return pm.configApplier.ApplyToAllGroups(config)
	}
	return fmt.Errorf("config applier not initialized")
}

// ForceOptimization 强制执行性能优化
func (pm *PoolManager) ForceOptimization() error {
	if pm.optimizer != nil {
		return pm.optimizer.ForceOptimization()
	}
	return fmt.Errorf("optimizer not initialized")
}

// GetOptimizationHistory 获取优化历史
func (pm *PoolManager) GetOptimizationHistory() []*OptimizationResult {
	if pm.optimizer != nil {
		return pm.optimizer.GetOptimizationHistory()
	}
	return nil
}

// GetOptimizerStats 获取优化器统计信息
func (pm *PoolManager) GetOptimizerStats() map[string]interface{} {
	if pm.optimizer != nil {
		return pm.optimizer.GetStats()
	}
	return nil
}

// RollbackConfig 回滚配置
func (pm *PoolManager) RollbackConfig(groupID uint) error {
	if pm.configApplier != nil {
		return pm.configApplier.RollbackConfig(groupID)
	}
	return fmt.Errorf("config applier not initialized")
}

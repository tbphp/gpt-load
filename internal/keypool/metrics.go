package keypool

import (
	"gpt-load/internal/models"
	"sync"
	"time"
)

// DefaultPoolMetrics 默认的池指标收集器
type DefaultPoolMetrics struct {
	mu      sync.RWMutex
	metrics map[uint]*groupMetrics
}

// groupMetrics 分组指标
type groupMetrics struct {
	// 选择操作指标
	selectCount    int64
	selectLatency  time.Duration
	selectSuccess  int64

	// 池操作指标
	refillCount    int64
	recoveryCount  int64
	rateLimitCount int64

	// 时间窗口统计
	windowStart    time.Time
	windowDuration time.Duration

	// 冷却时间统计
	coolingTimes   []time.Duration
	maxCoolingSize int
}

// NewDefaultPoolMetrics 创建默认指标收集器
func NewDefaultPoolMetrics() *DefaultPoolMetrics {
	return &DefaultPoolMetrics{
		metrics: make(map[uint]*groupMetrics),
	}
}

// RecordKeySelection 记录密钥选择指标
func (m *DefaultPoolMetrics) RecordKeySelection(groupID uint, latency time.Duration, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gm := m.getOrCreateGroupMetrics(groupID)
	gm.selectCount++
	gm.selectLatency += latency

	if success {
		gm.selectSuccess++
	}
}

// RecordPoolRefill 记录池补充指标
func (m *DefaultPoolMetrics) RecordPoolRefill(groupID uint, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gm := m.getOrCreateGroupMetrics(groupID)
	gm.refillCount += int64(count)
}

// RecordKeyRecovery 记录密钥恢复指标
func (m *DefaultPoolMetrics) RecordKeyRecovery(groupID uint, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gm := m.getOrCreateGroupMetrics(groupID)
	gm.recoveryCount += int64(count)
}

// RecordRateLimit 记录429错误指标
func (m *DefaultPoolMetrics) RecordRateLimit(groupID uint, keyID uint) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gm := m.getOrCreateGroupMetrics(groupID)
	gm.rateLimitCount++
}

// RecordCoolingTime 记录冷却时间
func (m *DefaultPoolMetrics) RecordCoolingTime(groupID uint, coolingTime time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gm := m.getOrCreateGroupMetrics(groupID)

	// 限制冷却时间记录数量
	if len(gm.coolingTimes) >= gm.maxCoolingSize {
		// 移除最旧的记录
		copy(gm.coolingTimes, gm.coolingTimes[1:])
		gm.coolingTimes = gm.coolingTimes[:len(gm.coolingTimes)-1]
	}

	gm.coolingTimes = append(gm.coolingTimes, coolingTime)
}

// GetMetrics 获取指标
func (m *DefaultPoolMetrics) GetMetrics(groupID uint) (*PoolPerformanceStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	gm, exists := m.metrics[groupID]
	if !exists {
		return &PoolPerformanceStats{}, nil
	}

	stats := &PoolPerformanceStats{}

	// 计算平均选择延迟
	if gm.selectCount > 0 {
		stats.SelectLatency = gm.selectLatency / time.Duration(gm.selectCount)
	}

	// 计算命中率
	if gm.selectCount > 0 {
		stats.HitRate = float64(gm.selectSuccess) / float64(gm.selectCount)
	}

	// 计算时间窗口内的频率
	windowHours := gm.windowDuration.Hours()
	if windowHours > 0 {
		stats.RefillRate = float64(gm.refillCount) / windowHours
		stats.RecoveryRate = float64(gm.recoveryCount) / windowHours
		stats.RateLimitRate = float64(gm.rateLimitCount) / windowHours
	}

	// 计算平均冷却时间
	if len(gm.coolingTimes) > 0 {
		var total time.Duration
		for _, ct := range gm.coolingTimes {
			total += ct
		}
		stats.AvgCoolingTime = total / time.Duration(len(gm.coolingTimes))
	}

	return stats, nil
}

// getOrCreateGroupMetrics 获取或创建分组指标
func (m *DefaultPoolMetrics) getOrCreateGroupMetrics(groupID uint) *groupMetrics {
	gm, exists := m.metrics[groupID]
	if !exists {
		gm = &groupMetrics{
			windowStart:    time.Now(),
			windowDuration: time.Hour, // 1小时窗口
			maxCoolingSize: 100,       // 最多记录100个冷却时间
		}
		m.metrics[groupID] = gm
	}

	// 检查是否需要重置窗口
	if time.Since(gm.windowStart) > gm.windowDuration {
		m.resetWindow(gm)
	}

	return gm
}

// resetWindow 重置时间窗口
func (m *DefaultPoolMetrics) resetWindow(gm *groupMetrics) {
	gm.windowStart = time.Now()
	gm.refillCount = 0
	gm.recoveryCount = 0
	gm.rateLimitCount = 0
}

// ResetMetrics 重置指标
func (m *DefaultPoolMetrics) ResetMetrics(groupID uint) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.metrics, groupID)
}

// GetAllGroupMetrics 获取所有分组的指标摘要
func (m *DefaultPoolMetrics) GetAllGroupMetrics() map[uint]*PoolPerformanceStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[uint]*PoolPerformanceStats)

	for groupID := range m.metrics {
		if stats, err := m.GetMetrics(groupID); err == nil {
			result[groupID] = stats
		}
	}

	return result
}

// DefaultKeyValidator 默认密钥验证器
type DefaultKeyValidator struct{}

// ValidateKey 验证单个密钥
func (v *DefaultKeyValidator) ValidateKey(key *models.APIKey, group *models.Group) error {
	// 基本验证逻辑
	if key.KeyValue == "" {
		return NewPoolError(ErrorTypeValidation, "EMPTY_KEY_VALUE", "Key value is empty")
	}

	if key.Status == models.KeyStatusInvalid {
		return NewPoolError(ErrorTypeValidation, "INVALID_KEY_STATUS", "Key status is invalid")
	}

	// 检查429状态
	if key.Status == models.KeyStatusRateLimited {
		if key.RateLimitResetAt != nil && time.Now().Before(*key.RateLimitResetAt) {
			return NewPoolError(ErrorTypeValidation, "KEY_RATE_LIMITED", "Key is still rate limited")
		}
	}

	return nil
}

// ValidateBatch 批量验证密钥
func (v *DefaultKeyValidator) ValidateBatch(keys []*models.APIKey, group *models.Group) []ValidationResult {
	results := make([]ValidationResult, len(keys))

	for i, key := range keys {
		startTime := time.Now()
		err := v.ValidateKey(key, group)
		latency := time.Since(startTime)

		results[i] = ValidationResult{
			KeyID:   key.ID,
			Valid:   err == nil,
			Latency: latency,
		}

		if err != nil {
			results[i].Error = err.Error()
		}
	}

	return results
}

// PoolFactory 实现
type DefaultPoolFactory struct{}

// CreatePool 创建池实例
func (f *DefaultPoolFactory) CreatePool(poolType PoolImplementationType, config *FactoryConfig) (LayeredKeyPool, error) {
	switch poolType {
	case PoolImplRedis:
		return NewRedisLayeredPool(config)
	case PoolImplMemory:
		// TODO: 实现内存版本
		return nil, NewPoolError(ErrorTypeConfiguration, "NOT_IMPLEMENTED", "Memory pool not implemented yet")
	case PoolImplHybrid:
		// TODO: 实现混合版本
		return nil, NewPoolError(ErrorTypeConfiguration, "NOT_IMPLEMENTED", "Hybrid pool not implemented yet")
	default:
		return nil, NewPoolError(ErrorTypeConfiguration, "UNKNOWN_POOL_TYPE", "Unknown pool implementation type")
	}
}

// GetSupportedTypes 获取支持的类型
func (f *DefaultPoolFactory) GetSupportedTypes() []PoolImplementationType {
	return []PoolImplementationType{PoolImplRedis}
}

// ValidateConfig 验证配置
func (f *DefaultPoolFactory) ValidateConfig(poolType PoolImplementationType, config *FactoryConfig) error {
	if config == nil {
		return NewPoolError(ErrorTypeConfiguration, "NIL_CONFIG", "Factory config is nil")
	}

	switch poolType {
	case PoolImplRedis:
		if config.Store == nil {
			return NewPoolError(ErrorTypeConfiguration, "MISSING_STORE", "Redis store is required")
		}
		if config.DB == nil {
			return NewPoolError(ErrorTypeConfiguration, "MISSING_DB", "Database is required")
		}
		return nil
	default:
		return NewPoolError(ErrorTypeConfiguration, "UNKNOWN_POOL_TYPE", "Unknown pool implementation type")
	}
}

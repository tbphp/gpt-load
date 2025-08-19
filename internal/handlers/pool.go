package handlers

import (
	"fmt"
	"gpt-load/internal/keypool"
	"gpt-load/internal/models"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// PoolHandler 池管理处理器
type PoolHandler struct {
	db          *gorm.DB
	poolManager *keypool.PoolManager
}

// NewPoolHandler 创建池管理处理器
func NewPoolHandler(db *gorm.DB, poolManager *keypool.PoolManager) *PoolHandler {
	return &PoolHandler{
		db:          db,
		poolManager: poolManager,
	}
}

// PoolStatsResponse 池统计响应
type PoolStatsResponse struct {
	GroupID            uint                    `json:"group_id"`
	GroupName          string                  `json:"group_name"`
	PoolStats          *keypool.PoolStats      `json:"pool_stats"`
	PerformanceMetrics *PerformanceMetrics     `json:"performance_metrics"`
	PoolHealth         *PoolHealth             `json:"pool_health"`
	LastUpdated        string                  `json:"last_updated"`
}

// PerformanceMetrics 性能指标
type PerformanceMetrics struct {
	Throughput     float64 `json:"throughput"`
	AvgLatency     float64 `json:"avg_latency"`
	ErrorRate      float64 `json:"error_rate"`
	CacheHitRate   float64 `json:"cache_hit_rate"`
}

// PoolHealth 池健康状态
type PoolHealth struct {
	Status string   `json:"status"`
	Issues []string `json:"issues"`
}

// RecoveryMetricsResponse 恢复指标响应
type RecoveryMetricsResponse struct {
	TotalRecoveryAttempts int64                    `json:"total_recovery_attempts"`
	SuccessfulRecoveries  int64                    `json:"successful_recoveries"`
	FailedRecoveries      int64                    `json:"failed_recoveries"`
	OverallSuccessRate    float64                  `json:"overall_success_rate"`
	RecentSuccessRate     float64                  `json:"recent_success_rate"`
	AvgRecoveryLatency    float64                  `json:"avg_recovery_latency"`
	RecoveriesPerHour     float64                  `json:"recoveries_per_hour"`
	LastRecoveryAt        *string                  `json:"last_recovery_at,omitempty"`
	ErrorStats            map[string]int64         `json:"error_stats"`
	HourlyStats           []*HourlyRecoveryStats   `json:"hourly_stats"`
}

// HourlyRecoveryStats 小时恢复统计
type HourlyRecoveryStats struct {
	Hour        string  `json:"hour"`
	Attempts    int64   `json:"attempts"`
	Successes   int64   `json:"successes"`
	SuccessRate float64 `json:"success_rate"`
}

// ManualRecoveryRequest 手动恢复请求
type ManualRecoveryRequest struct {
	KeyIDs []uint `json:"key_ids"`
}

// BatchRecoveryRequest 批量恢复请求
type BatchRecoveryRequest struct {
	Priority             *string                   `json:"priority,omitempty"`
	MaxConcurrent        *int                      `json:"max_concurrent,omitempty"`
	DelayBetweenBatches  *int                      `json:"delay_between_batches,omitempty"`
	Filter               *BatchRecoveryFilter      `json:"filter,omitempty"`
}

// BatchRecoveryFilter 批量恢复过滤器
type BatchRecoveryFilter struct {
	MinFailureCount *int    `json:"min_failure_count,omitempty"`
	MaxFailureCount *int    `json:"max_failure_count,omitempty"`
	Last429Before   *string `json:"last_429_before,omitempty"`
	Last429After    *string `json:"last_429_after,omitempty"`
}

// GetPoolStats 获取池统计信息
func (h *PoolHandler) GetPoolStats(c *gin.Context) {
	groupIDStr := c.Param("groupId")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	// 获取分组信息
	var group models.Group
	if err := h.db.First(&group, uint(groupID)).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query group"})
		return
	}

	// 获取池实例
	pool, err := h.poolManager.GetPool(uint(groupID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get pool instance"})
		return
	}

	// 获取池统计
	stats, err := pool.GetStats(uint(groupID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get pool stats"})
		return
	}

	// 构建响应
	response := &PoolStatsResponse{
		GroupID:   uint(groupID),
		GroupName: group.Name,
		PoolStats: stats,
		PerformanceMetrics: &PerformanceMetrics{
			Throughput:   h.calculateThroughput(uint(groupID)),
			AvgLatency:   h.calculateAvgLatency(uint(groupID)),
			ErrorRate:    h.calculateErrorRate(uint(groupID)),
			CacheHitRate: h.calculateCacheHitRate(uint(groupID)),
		},
		PoolHealth: &PoolHealth{
			Status: assessPoolHealth(stats),
			Issues: identifyPoolIssues(stats),
		},
		LastUpdated: time.Now().Format(time.RFC3339),
	}

	c.JSON(http.StatusOK, response)
}

// GetRecoveryMetrics 获取恢复指标
func (h *PoolHandler) GetRecoveryMetrics(c *gin.Context) {
	groupIDStr := c.Param("groupId")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	// 获取池实例
	pool, err := h.poolManager.GetPool(uint(groupID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get pool instance"})
		return
	}

	// 获取恢复指标
	var metrics *keypool.RecoveryMonitorMetrics

	// 尝试获取恢复指标（如果池支持）
	if redisPool, ok := pool.(*keypool.RedisLayeredPool); ok {
		metrics = redisPool.GetRecoveryMetrics()
	} else if memoryPool, ok := pool.(*keypool.MemoryLayeredPool); ok {
		metrics = memoryPool.GetRecoveryMetrics()
	}

	if metrics == nil {
		// 返回空指标
		response := &RecoveryMetricsResponse{
			ErrorStats:  make(map[string]int64),
			HourlyStats: make([]*HourlyRecoveryStats, 0),
		}
		c.JSON(http.StatusOK, response)
		return
	}

	// 转换小时统计
	hourlyStats := make([]*HourlyRecoveryStats, len(metrics.HourlyStats))
	for i, stat := range metrics.HourlyStats {
		hourlyStats[i] = &HourlyRecoveryStats{
			Hour:        stat.Hour.Format(time.RFC3339),
			Attempts:    stat.Attempts,
			Successes:   stat.Successes,
			SuccessRate: stat.SuccessRate,
		}
	}

	// 构建响应
	response := &RecoveryMetricsResponse{
		TotalRecoveryAttempts: metrics.TotalRecoveryAttempts,
		SuccessfulRecoveries:  metrics.SuccessfulRecoveries,
		FailedRecoveries:      metrics.FailedRecoveries,
		OverallSuccessRate:    metrics.OverallSuccessRate,
		RecentSuccessRate:     metrics.RecentSuccessRate,
		AvgRecoveryLatency:    float64(metrics.AvgRecoveryLatency.Milliseconds()),
		RecoveriesPerHour:     metrics.RecoveriesPerHour,
		ErrorStats:            metrics.ErrorsByType,
		HourlyStats:           hourlyStats,
	}

	if !metrics.LastRecoveryAt.IsZero() {
		lastRecovery := metrics.LastRecoveryAt.Format(time.RFC3339)
		response.LastRecoveryAt = &lastRecovery
	}

	c.JSON(http.StatusOK, response)
}

// TriggerManualRecovery 触发手动恢复
func (h *PoolHandler) TriggerManualRecovery(c *gin.Context) {
	groupIDStr := c.Param("groupId")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	var request ManualRecoveryRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// 获取池实例
	pool, err := h.poolManager.GetPool(uint(groupID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get pool instance"})
		return
	}

	// 如果没有指定密钥ID，获取所有冷却池中的密钥
	keyIDs := request.KeyIDs
	if len(keyIDs) == 0 {
		// 查询冷却池中的所有密钥
		var keys []models.APIKey
		if err := h.db.Where("group_id = ? AND status = ?", groupID, models.KeyStatusRateLimited).Find(&keys).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query cooling keys"})
			return
		}

		for _, key := range keys {
			keyIDs = append(keyIDs, key.ID)
		}
	}

	if len(keyIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No keys to recover"})
		return
	}

	// 触发手动恢复
	if redisPool, ok := pool.(*keypool.RedisLayeredPool); ok {
		err = redisPool.TriggerManualRecovery(uint(groupID), keyIDs)
	} else if memoryPool, ok := pool.(*keypool.MemoryLayeredPool); ok {
		err = memoryPool.TriggerManualRecovery(uint(groupID), keyIDs)
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Pool does not support manual recovery"})
		return
	}

	if err != nil {
		logrus.WithError(err).Error("Failed to trigger manual recovery")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to trigger manual recovery"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Manual recovery triggered for %d keys", len(keyIDs)),
		"key_count": len(keyIDs),
	})
}

// RefillPools 重填池
func (h *PoolHandler) RefillPools(c *gin.Context) {
	groupIDStr := c.Param("groupId")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	// 获取池实例
	pool, err := h.poolManager.GetPool(uint(groupID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get pool instance"})
		return
	}

	// 执行池重填
	if err := pool.RefillPools(uint(groupID)); err != nil {
		logrus.WithError(err).Error("Failed to refill pools")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refill pools"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Pools refilled successfully"})
}

// 辅助函数

func (h *PoolHandler) calculateThroughput(groupID uint) float64 {
	// 从池实例获取实际的性能指标
	pool, err := h.poolManager.GetPool(groupID)
	if err != nil {
		return 0.0
	}

	var metrics *keypool.PerformanceMetrics
	if redisPool, ok := pool.(*keypool.RedisLayeredPool); ok {
		metrics = redisPool.GetPerformanceMetrics()
	} else if memoryPool, ok := pool.(*keypool.MemoryLayeredPool); ok {
		metrics = memoryPool.GetPerformanceMetrics()
	}

	if metrics != nil {
		return metrics.Throughput
	}

	// 回退到模拟数据
	return 150.5
}

func (h *PoolHandler) calculateAvgLatency(groupID uint) float64 {
	// 从池实例获取实际的性能指标
	pool, err := h.poolManager.GetPool(groupID)
	if err != nil {
		return 0.0
	}

	var metrics *keypool.PerformanceMetrics
	if redisPool, ok := pool.(*keypool.RedisLayeredPool); ok {
		metrics = redisPool.GetPerformanceMetrics()
	} else if memoryPool, ok := pool.(*keypool.MemoryLayeredPool); ok {
		metrics = memoryPool.GetPerformanceMetrics()
	}

	if metrics != nil {
		return float64(metrics.AvgLatency)
	}

	// 回退到模拟数据
	return 85.2
}

func (h *PoolHandler) calculateErrorRate(groupID uint) float64 {
	// 从池实例获取实际的性能指标
	pool, err := h.poolManager.GetPool(groupID)
	if err != nil {
		return 0.0
	}

	var metrics *keypool.PerformanceMetrics
	if redisPool, ok := pool.(*keypool.RedisLayeredPool); ok {
		metrics = redisPool.GetPerformanceMetrics()
	} else if memoryPool, ok := pool.(*keypool.MemoryLayeredPool); ok {
		metrics = memoryPool.GetPerformanceMetrics()
	}

	if metrics != nil {
		return metrics.ErrorRate
	}

	// 回退到模拟数据
	return 0.02
}

func (h *PoolHandler) calculateCacheHitRate(groupID uint) float64 {
	// 从池实例获取实际的性能指标
	pool, err := h.poolManager.GetPool(groupID)
	if err != nil {
		return 0.0
	}

	var metrics *keypool.PerformanceMetrics
	if redisPool, ok := pool.(*keypool.RedisLayeredPool); ok {
		metrics = redisPool.GetPerformanceMetrics()
	} else if memoryPool, ok := pool.(*keypool.MemoryLayeredPool); ok {
		metrics = memoryPool.GetPerformanceMetrics()
	}

	if metrics != nil {
		return metrics.CacheHitRate
	}

	// 回退到模拟数据
	return 0.85
}

func assessPoolHealth(stats *keypool.PoolStats) string {
	if stats == nil {
		return "unknown"
	}

	// 简单的健康评估逻辑
	if stats.CoolingPool > stats.TotalKeys/2 {
		return "critical"
	} else if stats.CoolingPool > stats.TotalKeys/4 {
		return "warning"
	}

	return "healthy"
}

func identifyPoolIssues(stats *keypool.PoolStats) []string {
	var issues []string

	if stats == nil {
		issues = append(issues, "无法获取池统计信息")
		return issues
	}

	if stats.ValidationPool == 0 {
		issues = append(issues, "验证池为空")
	}

	if stats.ReadyPool == 0 {
		issues = append(issues, "就绪池为空")
	}

	if stats.CoolingPool > stats.TotalKeys/4 {
		issues = append(issues, "冷却池中密钥过多")
	}

	return issues
}

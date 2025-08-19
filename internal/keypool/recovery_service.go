package keypool

import (
	"context"
	"fmt"
	"gpt-load/internal/models"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// RecoveryService 429恢复服务
type RecoveryService struct {
	// 依赖
	db              *gorm.DB
	layeredPool     LayeredKeyPool
	strategy        RecoveryStrategy

	// 配置
	config          *RecoveryServiceConfig

	// 运行时状态
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	running         bool
	mu              sync.RWMutex

	// 恢复队列和统计
	recoveryQueue   chan *RecoveryPlan
	completedPlans  []*CompletedRecoveryPlan
	metrics         *RecoveryMetrics

	// 历史记录缓存
	historyCache    map[uint][]*RateLimitRecord
	historyCacheMu  sync.RWMutex
	lastCacheUpdate time.Time
}

// RecoveryServiceConfig 恢复服务配置
type RecoveryServiceConfig struct {
	// 基础配置
	CheckInterval       time.Duration `json:"check_interval"`        // 检查间隔
	BatchSize          int           `json:"batch_size"`            // 批处理大小
	WorkerCount        int           `json:"worker_count"`          // 工作协程数
	QueueSize          int           `json:"queue_size"`            // 队列大小

	// 历史记录配置
	HistoryRetentionDays int          `json:"history_retention_days"` // 历史记录保留天数
	HistoryCacheTTL     time.Duration `json:"history_cache_ttl"`      // 历史缓存TTL

	// 恢复配置
	MaxConcurrentRecoveries int       `json:"max_concurrent_recoveries"` // 最大并发恢复数
	RecoveryTimeout        time.Duration `json:"recovery_timeout"`       // 恢复超时时间

	// 安全配置
	EnableDryRun           bool          `json:"enable_dry_run"`         // 启用试运行模式
	MaxRecoveriesPerHour   int           `json:"max_recoveries_per_hour"` // 每小时最大恢复数
}

// CompletedRecoveryPlan 已完成的恢复计划
type CompletedRecoveryPlan struct {
	Plan        *RecoveryPlan `json:"plan"`
	Success     bool          `json:"success"`
	Error       string        `json:"error,omitempty"`
	StartTime   time.Time     `json:"start_time"`
	EndTime     time.Time     `json:"end_time"`
	Duration    time.Duration `json:"duration"`
}

// RecoveryMetrics 恢复指标
type RecoveryMetrics struct {
	TotalAttempts      int64         `json:"total_attempts"`
	SuccessfulRecoveries int64       `json:"successful_recoveries"`
	FailedRecoveries   int64         `json:"failed_recoveries"`
	AvgRecoveryTime    time.Duration `json:"avg_recovery_time"`
	LastRecoveryAt     time.Time     `json:"last_recovery_at"`
	RecoveriesPerHour  int           `json:"recoveries_per_hour"`

	// 按优先级统计
	PriorityStats      map[RecoveryPriority]*PriorityMetrics `json:"priority_stats"`
}

// PriorityMetrics 优先级指标
type PriorityMetrics struct {
	Attempts    int64         `json:"attempts"`
	Successes   int64         `json:"successes"`
	Failures    int64         `json:"failures"`
	AvgTime     time.Duration `json:"avg_time"`
}

// DefaultRecoveryServiceConfig 返回默认恢复服务配置
func DefaultRecoveryServiceConfig() *RecoveryServiceConfig {
	return &RecoveryServiceConfig{
		CheckInterval:           5 * time.Minute,
		BatchSize:              50,
		WorkerCount:            3,
		QueueSize:              1000,
		HistoryRetentionDays:   30,
		HistoryCacheTTL:        1 * time.Hour,
		MaxConcurrentRecoveries: 10,
		RecoveryTimeout:        30 * time.Second,
		EnableDryRun:           false,
		MaxRecoveriesPerHour:   100,
	}
}

// NewRecoveryService 创建恢复服务
func NewRecoveryService(
	db *gorm.DB,
	layeredPool LayeredKeyPool,
	strategy RecoveryStrategy,
	config *RecoveryServiceConfig,
) *RecoveryService {
	if config == nil {
		config = DefaultRecoveryServiceConfig()
	}

	if strategy == nil {
		strategy = NewSmartRecoveryStrategy(nil)
	}

	ctx, cancel := context.WithCancel(context.Background())

	service := &RecoveryService{
		db:              db,
		layeredPool:     layeredPool,
		strategy:        strategy,
		config:          config,
		ctx:             ctx,
		cancel:          cancel,
		recoveryQueue:   make(chan *RecoveryPlan, config.QueueSize),
		completedPlans:  make([]*CompletedRecoveryPlan, 0),
		historyCache:    make(map[uint][]*RateLimitRecord),
		metrics: &RecoveryMetrics{
			PriorityStats: make(map[RecoveryPriority]*PriorityMetrics),
		},
	}

	// 初始化优先级统计
	for priority := PriorityLow; priority <= PriorityCritical; priority++ {
		service.metrics.PriorityStats[priority] = &PriorityMetrics{}
	}

	return service
}

// Start 启动恢复服务
func (s *RecoveryService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("recovery service is already running")
	}

	// 启动主检查循环
	s.wg.Add(1)
	go s.checkLoop()

	// 启动工作协程
	for i := 0; i < s.config.WorkerCount; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}

	// 启动历史缓存更新
	s.wg.Add(1)
	go s.historyCacheUpdateLoop()

	s.running = true
	logrus.Info("Recovery service started")

	return nil
}

// Stop 停止恢复服务
func (s *RecoveryService) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.cancel()
	s.wg.Wait()

	close(s.recoveryQueue)

	s.running = false
	logrus.Info("Recovery service stopped")

	return nil
}

// checkLoop 主检查循环
func (s *RecoveryService) checkLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.performRecoveryCheck(); err != nil {
				logrus.WithError(err).Error("Recovery check failed")
			}
		}
	}
}

// performRecoveryCheck 执行恢复检查
func (s *RecoveryService) performRecoveryCheck() error {
	// 查找需要恢复的密钥
	var rateLimitedKeys []models.APIKey
	if err := s.db.Where("status = ?", models.KeyStatusRateLimited).
		Limit(s.config.BatchSize).
		Find(&rateLimitedKeys).Error; err != nil {
		return fmt.Errorf("failed to query rate limited keys: %w", err)
	}

	if len(rateLimitedKeys) == 0 {
		return nil
	}

	logrus.WithField("count", len(rateLimitedKeys)).Debug("Found rate limited keys to check")

	now := time.Now()
	recoveryPlans := make([]*RecoveryPlan, 0)

	for _, key := range rateLimitedKeys {
		// 检查是否应该尝试恢复
		if !s.strategy.ShouldAttemptRecovery(&key, now) {
			continue
		}

		// 获取分组信息
		var group models.Group
		if err := s.db.First(&group, key.GroupID).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"keyID":   key.ID,
				"groupID": key.GroupID,
				"error":   err,
			}).Warn("Failed to load group for key")
			continue
		}

		// 获取历史记录
		history := s.getRateLimitHistory(key.ID)

		// 创建恢复计划
		plan, err := s.strategy.CreateRecoveryPlan(&key, &group, history)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"keyID": key.ID,
				"error": err,
			}).Warn("Failed to create recovery plan")
			continue
		}

		// 检查是否到了计划时间
		if now.Before(plan.ScheduledAt) {
			continue
		}

		recoveryPlans = append(recoveryPlans, plan)
	}

	// 按优先级排序
	s.sortPlansByPriority(recoveryPlans)

	// 提交恢复计划
	for _, plan := range recoveryPlans {
		select {
		case s.recoveryQueue <- plan:
			logrus.WithFields(logrus.Fields{
				"keyID":    plan.KeyID,
				"priority": plan.Priority,
			}).Debug("Submitted recovery plan")
		case <-s.ctx.Done():
			return nil
		default:
			logrus.Warn("Recovery queue is full, skipping plan")
		}
	}

	return nil
}

// worker 工作协程
func (s *RecoveryService) worker(workerID int) {
	defer s.wg.Done()

	logrus.WithField("workerID", workerID).Debug("Recovery worker started")

	for {
		select {
		case <-s.ctx.Done():
			return
		case plan := <-s.recoveryQueue:
			if plan == nil {
				return
			}

			s.executeRecoveryPlan(plan, workerID)
		}
	}
}

// executeRecoveryPlan 执行恢复计划
func (s *RecoveryService) executeRecoveryPlan(plan *RecoveryPlan, workerID int) {
	startTime := time.Now()

	logrus.WithFields(logrus.Fields{
		"workerID": workerID,
		"keyID":    plan.KeyID,
		"priority": plan.Priority,
	}).Info("Executing recovery plan")

	// 创建完成记录
	completed := &CompletedRecoveryPlan{
		Plan:      plan,
		StartTime: startTime,
	}

	// 执行恢复
	var err error
	if s.config.EnableDryRun {
		logrus.WithField("keyID", plan.KeyID).Info("Dry run: would recover key")
		completed.Success = true
	} else {
		err = s.recoverKey(plan)
		completed.Success = (err == nil)
		if err != nil {
			completed.Error = err.Error()
		}
	}

	// 完成记录
	completed.EndTime = time.Now()
	completed.Duration = completed.EndTime.Sub(completed.StartTime)

	// 更新统计
	s.updateMetrics(completed)

	// 保存完成记录
	s.mu.Lock()
	s.completedPlans = append(s.completedPlans, completed)
	// 限制完成记录数量
	if len(s.completedPlans) > 1000 {
		s.completedPlans = s.completedPlans[len(s.completedPlans)-1000:]
	}
	s.mu.Unlock()

	if completed.Success {
		logrus.WithFields(logrus.Fields{
			"keyID":    plan.KeyID,
			"duration": completed.Duration,
		}).Info("Key recovery completed successfully")
	} else {
		logrus.WithFields(logrus.Fields{
			"keyID":    plan.KeyID,
			"error":    completed.Error,
			"duration": completed.Duration,
		}).Error("Key recovery failed")
	}
}

// recoverKey 恢复密钥
func (s *RecoveryService) recoverKey(plan *RecoveryPlan) error {
	// 从冷却池移动到就绪池
	if err := s.layeredPool.MoveKey(plan.KeyID, PoolTypeCooling, PoolTypeReady); err != nil {
		// 记录恢复失败
		if redisPool, ok := s.layeredPool.(*RedisLayeredPool); ok {
			if redisPool.performanceMonitor != nil {
				redisPool.performanceMonitor.RecordRecovery(false)
			}
		} else if memoryPool, ok := s.layeredPool.(*MemoryLayeredPool); ok {
			if memoryPool.performanceMonitor != nil {
				memoryPool.performanceMonitor.RecordRecovery(false)
			}
		}
		return fmt.Errorf("failed to move key from cooling to ready pool: %w", err)
	}

	// 更新数据库中的密钥状态
	if err := s.db.Model(&models.APIKey{}).
		Where("id = ?", plan.KeyID).
		Updates(map[string]interface{}{
			"status":                models.KeyStatusActive,
			"rate_limit_reset_at":   nil,
		}).Error; err != nil {
		// 记录恢复失败
		if redisPool, ok := s.layeredPool.(*RedisLayeredPool); ok {
			if redisPool.performanceMonitor != nil {
				redisPool.performanceMonitor.RecordRecovery(false)
			}
		} else if memoryPool, ok := s.layeredPool.(*MemoryLayeredPool); ok {
			if memoryPool.performanceMonitor != nil {
				memoryPool.performanceMonitor.RecordRecovery(false)
			}
		}
		return fmt.Errorf("failed to update key status in database: %w", err)
	}

	// 记录恢复成功
	if redisPool, ok := s.layeredPool.(*RedisLayeredPool); ok {
		if redisPool.performanceMonitor != nil {
			redisPool.performanceMonitor.RecordRecovery(true)
		}
	} else if memoryPool, ok := s.layeredPool.(*MemoryLayeredPool); ok {
		if memoryPool.performanceMonitor != nil {
			memoryPool.performanceMonitor.RecordRecovery(true)
		}
	}

	return nil
}

// getRateLimitHistory 获取429历史记录
func (s *RecoveryService) getRateLimitHistory(keyID uint) []*RateLimitRecord {
	s.historyCacheMu.RLock()
	history, exists := s.historyCache[keyID]
	s.historyCacheMu.RUnlock()

	if exists {
		return history
	}

	// 从数据库查询（这里需要实现具体的查询逻辑）
	// 暂时返回空切片
	return []*RateLimitRecord{}
}

// historyCacheUpdateLoop 历史缓存更新循环
func (s *RecoveryService) historyCacheUpdateLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.HistoryCacheTTL)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.updateHistoryCache()
		}
	}
}

// updateHistoryCache 更新历史缓存
func (s *RecoveryService) updateHistoryCache() {
	// 这里应该从数据库或日志系统中加载429历史记录
	// 暂时跳过具体实现
	s.lastCacheUpdate = time.Now()
}

// sortPlansByPriority 按优先级排序恢复计划
func (s *RecoveryService) sortPlansByPriority(plans []*RecoveryPlan) {
	// 简单的冒泡排序，按优先级降序排列
	for i := 0; i < len(plans)-1; i++ {
		for j := 0; j < len(plans)-i-1; j++ {
			if plans[j].Priority < plans[j+1].Priority {
				plans[j], plans[j+1] = plans[j+1], plans[j]
			}
		}
	}
}

// updateMetrics 更新指标
func (s *RecoveryService) updateMetrics(completed *CompletedRecoveryPlan) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.metrics.TotalAttempts++
	s.metrics.LastRecoveryAt = completed.EndTime

	if completed.Success {
		s.metrics.SuccessfulRecoveries++
	} else {
		s.metrics.FailedRecoveries++
	}

	// 更新平均恢复时间
	if s.metrics.TotalAttempts > 0 {
		totalTime := s.metrics.AvgRecoveryTime * time.Duration(s.metrics.TotalAttempts-1)
		s.metrics.AvgRecoveryTime = (totalTime + completed.Duration) / time.Duration(s.metrics.TotalAttempts)
	}

	// 更新优先级统计
	priorityStats := s.metrics.PriorityStats[completed.Plan.Priority]
	priorityStats.Attempts++
	if completed.Success {
		priorityStats.Successes++
	} else {
		priorityStats.Failures++
	}

	// 更新优先级平均时间
	if priorityStats.Attempts > 0 {
		totalTime := priorityStats.AvgTime * time.Duration(priorityStats.Attempts-1)
		priorityStats.AvgTime = (totalTime + completed.Duration) / time.Duration(priorityStats.Attempts)
	}
}

// GetMetrics 获取恢复指标
func (s *RecoveryService) GetMetrics() *RecoveryMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 返回指标的副本
	metrics := &RecoveryMetrics{
		TotalAttempts:        s.metrics.TotalAttempts,
		SuccessfulRecoveries: s.metrics.SuccessfulRecoveries,
		FailedRecoveries:     s.metrics.FailedRecoveries,
		AvgRecoveryTime:      s.metrics.AvgRecoveryTime,
		LastRecoveryAt:       s.metrics.LastRecoveryAt,
		PriorityStats:        make(map[RecoveryPriority]*PriorityMetrics),
	}

	for priority, stats := range s.metrics.PriorityStats {
		metrics.PriorityStats[priority] = &PriorityMetrics{
			Attempts:  stats.Attempts,
			Successes: stats.Successes,
			Failures:  stats.Failures,
			AvgTime:   stats.AvgTime,
		}
	}

	return metrics
}

// GetCompletedPlans 获取已完成的恢复计划
func (s *RecoveryService) GetCompletedPlans(limit int) []*CompletedRecoveryPlan {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.completedPlans) {
		limit = len(s.completedPlans)
	}

	// 返回最近的记录
	start := len(s.completedPlans) - limit
	result := make([]*CompletedRecoveryPlan, limit)
	copy(result, s.completedPlans[start:])

	return result
}

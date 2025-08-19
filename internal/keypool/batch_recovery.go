package keypool

import (
	"context"
	"fmt"
	"gpt-load/internal/models"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// BatchRecoveryProcessor 批量恢复处理器
type BatchRecoveryProcessor struct {
	// 依赖
	db              *gorm.DB
	layeredPool     LayeredKeyPool
	calculator      *DynamicRecoveryCalculator
	
	// 配置
	config          *BatchRecoveryConfig
	
	// 运行时状态
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	running         bool
	mu              sync.RWMutex
	
	// 批量队列
	recoveryBatches chan *RecoveryBatch
	results         chan *BatchRecoveryResult
	
	// 统计
	metrics         *BatchRecoveryMetrics
}

// BatchRecoveryConfig 批量恢复配置
type BatchRecoveryConfig struct {
	// 批量处理配置
	MaxBatchSize        int           `json:"max_batch_size"`         // 最大批量大小
	MinBatchSize        int           `json:"min_batch_size"`         // 最小批量大小
	BatchTimeout        time.Duration `json:"batch_timeout"`          // 批量超时时间
	ProcessInterval     time.Duration `json:"process_interval"`       // 处理间隔
	
	// 并发控制
	MaxConcurrentBatches int          `json:"max_concurrent_batches"` // 最大并发批次
	WorkerCount         int           `json:"worker_count"`           // 工作协程数
	
	// 优先级配置
	PriorityBatchSizes  map[RecoveryPriority]int `json:"priority_batch_sizes"` // 按优先级的批量大小
	
	// 安全配置
	EnableRollback      bool          `json:"enable_rollback"`        // 启用回滚
	MaxFailureRate      float64       `json:"max_failure_rate"`       // 最大失败率
	
	// 性能配置
	EnablePipelining    bool          `json:"enable_pipelining"`      // 启用流水线
	PipelineDepth       int           `json:"pipeline_depth"`         // 流水线深度
}

// RecoveryBatch 恢复批次
type RecoveryBatch struct {
	ID          string            `json:"id"`
	Priority    RecoveryPriority  `json:"priority"`
	Keys        []*models.APIKey  `json:"keys"`
	Plans       []*RecoveryPlan   `json:"plans"`
	CreatedAt   time.Time         `json:"created_at"`
	ScheduledAt time.Time         `json:"scheduled_at"`
	GroupID     uint              `json:"group_id"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// BatchRecoveryResult 批量恢复结果
type BatchRecoveryResult struct {
	BatchID         string                    `json:"batch_id"`
	TotalKeys       int                       `json:"total_keys"`
	SuccessfulKeys  int                       `json:"successful_keys"`
	FailedKeys      int                       `json:"failed_keys"`
	Duration        time.Duration             `json:"duration"`
	StartTime       time.Time                 `json:"start_time"`
	EndTime         time.Time                 `json:"end_time"`
	SuccessRate     float64                   `json:"success_rate"`
	KeyResults      []*KeyRecoveryResult      `json:"key_results"`
	Error           string                    `json:"error,omitempty"`
}

// KeyRecoveryResult 单个密钥恢复结果
type KeyRecoveryResult struct {
	KeyID     uint          `json:"key_id"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
	FromPool  PoolType      `json:"from_pool"`
	ToPool    PoolType      `json:"to_pool"`
}

// BatchRecoveryMetrics 批量恢复指标
type BatchRecoveryMetrics struct {
	TotalBatches        int64         `json:"total_batches"`
	SuccessfulBatches   int64         `json:"successful_batches"`
	FailedBatches       int64         `json:"failed_batches"`
	TotalKeysProcessed  int64         `json:"total_keys_processed"`
	TotalKeysRecovered  int64         `json:"total_keys_recovered"`
	AvgBatchSize        float64       `json:"avg_batch_size"`
	AvgProcessingTime   time.Duration `json:"avg_processing_time"`
	OverallSuccessRate  float64       `json:"overall_success_rate"`
	LastProcessedAt     time.Time     `json:"last_processed_at"`
	
	// 按优先级统计
	PriorityMetrics     map[RecoveryPriority]*PriorityBatchMetrics `json:"priority_metrics"`
}

// PriorityBatchMetrics 优先级批量指标
type PriorityBatchMetrics struct {
	Batches     int64         `json:"batches"`
	Keys        int64         `json:"keys"`
	Successes   int64         `json:"successes"`
	AvgTime     time.Duration `json:"avg_time"`
}

// DefaultBatchRecoveryConfig 返回默认批量恢复配置
func DefaultBatchRecoveryConfig() *BatchRecoveryConfig {
	return &BatchRecoveryConfig{
		MaxBatchSize:        50,
		MinBatchSize:        5,
		BatchTimeout:        30 * time.Second,
		ProcessInterval:     10 * time.Second,
		MaxConcurrentBatches: 3,
		WorkerCount:         2,
		PriorityBatchSizes: map[RecoveryPriority]int{
			PriorityCritical: 20,
			PriorityHigh:     30,
			PriorityNormal:   50,
			PriorityLow:      100,
		},
		EnableRollback:      true,
		MaxFailureRate:      0.3,
		EnablePipelining:    true,
		PipelineDepth:       3,
	}
}

// NewBatchRecoveryProcessor 创建批量恢复处理器
func NewBatchRecoveryProcessor(
	db *gorm.DB,
	layeredPool LayeredKeyPool,
	calculator *DynamicRecoveryCalculator,
	config *BatchRecoveryConfig,
) *BatchRecoveryProcessor {
	
	if config == nil {
		config = DefaultBatchRecoveryConfig()
	}
	
	if calculator == nil {
		calculator = NewDynamicRecoveryCalculator(nil)
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	processor := &BatchRecoveryProcessor{
		db:              db,
		layeredPool:     layeredPool,
		calculator:      calculator,
		config:          config,
		ctx:             ctx,
		cancel:          cancel,
		recoveryBatches: make(chan *RecoveryBatch, 100),
		results:         make(chan *BatchRecoveryResult, 100),
		metrics: &BatchRecoveryMetrics{
			PriorityMetrics: make(map[RecoveryPriority]*PriorityBatchMetrics),
		},
	}
	
	// 初始化优先级指标
	for priority := PriorityLow; priority <= PriorityCritical; priority++ {
		processor.metrics.PriorityMetrics[priority] = &PriorityBatchMetrics{}
	}
	
	return processor
}

// Start 启动批量恢复处理器
func (p *BatchRecoveryProcessor) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if p.running {
		return fmt.Errorf("batch recovery processor is already running")
	}
	
	// 启动工作协程
	for i := 0; i < p.config.WorkerCount; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
	
	// 启动结果处理协程
	p.wg.Add(1)
	go p.resultProcessor()
	
	p.running = true
	logrus.Info("Batch recovery processor started")
	
	return nil
}

// Stop 停止批量恢复处理器
func (p *BatchRecoveryProcessor) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if !p.running {
		return nil
	}
	
	p.cancel()
	p.wg.Wait()
	
	close(p.recoveryBatches)
	close(p.results)
	
	p.running = false
	logrus.Info("Batch recovery processor stopped")
	
	return nil
}

// CreateRecoveryBatches 创建恢复批次
func (p *BatchRecoveryProcessor) CreateRecoveryBatches(plans []*RecoveryPlan) ([]*RecoveryBatch, error) {
	if len(plans) == 0 {
		return nil, nil
	}
	
	// 按优先级和分组分类
	groupedPlans := p.groupPlansByPriorityAndGroup(plans)
	
	var batches []*RecoveryBatch
	
	for groupKey, groupPlans := range groupedPlans {
		priority := groupKey.Priority
		groupID := groupKey.GroupID
		
		// 获取该优先级的批量大小
		batchSize := p.config.MaxBatchSize
		if prioritySize, exists := p.config.PriorityBatchSizes[priority]; exists {
			batchSize = prioritySize
		}
		
		// 分割成批次
		for i := 0; i < len(groupPlans); i += batchSize {
			end := i + batchSize
			if end > len(groupPlans) {
				end = len(groupPlans)
			}
			
			batchPlans := groupPlans[i:end]
			
			// 创建批次
			batch := &RecoveryBatch{
				ID:          fmt.Sprintf("batch_%d_%s_%d", groupID, priority, time.Now().UnixNano()),
				Priority:    priority,
				Plans:       batchPlans,
				CreatedAt:   time.Now(),
				ScheduledAt: p.calculateBatchScheduleTime(batchPlans),
				GroupID:     groupID,
				Metadata:    make(map[string]interface{}),
			}
			
			// 从计划中提取密钥
			batch.Keys = make([]*models.APIKey, len(batchPlans))
			for j, plan := range batchPlans {
				// 这里需要从数据库查询密钥详情
				var key models.APIKey
				if err := p.db.First(&key, plan.KeyID).Error; err != nil {
					logrus.WithFields(logrus.Fields{
						"keyID": plan.KeyID,
						"error": err,
					}).Warn("Failed to load key for batch")
					continue
				}
				batch.Keys[j] = &key
			}
			
			batches = append(batches, batch)
		}
	}
	
	// 按优先级排序
	sort.Slice(batches, func(i, j int) bool {
		return batches[i].Priority > batches[j].Priority
	})
	
	return batches, nil
}

// groupPlansByPriorityAndGroup 按优先级和分组分类计划
func (p *BatchRecoveryProcessor) groupPlansByPriorityAndGroup(plans []*RecoveryPlan) map[GroupKey][]*RecoveryPlan {
	grouped := make(map[GroupKey][]*RecoveryPlan)
	
	for _, plan := range plans {
		key := GroupKey{
			Priority: plan.Priority,
			GroupID:  plan.GroupID,
		}
		grouped[key] = append(grouped[key], plan)
	}
	
	return grouped
}

// GroupKey 分组键
type GroupKey struct {
	Priority RecoveryPriority
	GroupID  uint
}

// calculateBatchScheduleTime 计算批次调度时间
func (p *BatchRecoveryProcessor) calculateBatchScheduleTime(plans []*RecoveryPlan) time.Time {
	if len(plans) == 0 {
		return time.Now()
	}
	
	// 使用最晚的调度时间
	latest := plans[0].ScheduledAt
	for _, plan := range plans[1:] {
		if plan.ScheduledAt.After(latest) {
			latest = plan.ScheduledAt
		}
	}
	
	return latest
}

// SubmitBatch 提交批次
func (p *BatchRecoveryProcessor) SubmitBatch(batch *RecoveryBatch) error {
	select {
	case p.recoveryBatches <- batch:
		logrus.WithFields(logrus.Fields{
			"batchID":  batch.ID,
			"priority": batch.Priority,
			"keyCount": len(batch.Keys),
		}).Debug("Submitted recovery batch")
		return nil
	case <-p.ctx.Done():
		return fmt.Errorf("batch recovery processor is stopped")
	default:
		return fmt.Errorf("batch queue is full")
	}
}

// worker 工作协程
func (p *BatchRecoveryProcessor) worker(workerID int) {
	defer p.wg.Done()
	
	logrus.WithField("workerID", workerID).Debug("Batch recovery worker started")
	
	for {
		select {
		case <-p.ctx.Done():
			return
		case batch := <-p.recoveryBatches:
			if batch == nil {
				return
			}
			
			result := p.processBatch(batch, workerID)
			
			select {
			case p.results <- result:
			case <-p.ctx.Done():
				return
			}
		}
	}
}

// processBatch 处理批次
func (p *BatchRecoveryProcessor) processBatch(batch *RecoveryBatch, workerID int) *BatchRecoveryResult {
	startTime := time.Now()
	
	logrus.WithFields(logrus.Fields{
		"workerID": workerID,
		"batchID":  batch.ID,
		"keyCount": len(batch.Keys),
		"priority": batch.Priority,
	}).Info("Processing recovery batch")
	
	result := &BatchRecoveryResult{
		BatchID:    batch.ID,
		TotalKeys:  len(batch.Keys),
		StartTime:  startTime,
		KeyResults: make([]*KeyRecoveryResult, 0, len(batch.Keys)),
	}
	
	// 并发处理密钥恢复
	var wg sync.WaitGroup
	keyResults := make(chan *KeyRecoveryResult, len(batch.Keys))
	
	// 限制并发数
	semaphore := make(chan struct{}, p.config.PipelineDepth)
	
	for i, key := range batch.Keys {
		if key == nil {
			continue
		}
		
		wg.Add(1)
		go func(k *models.APIKey, plan *RecoveryPlan) {
			defer wg.Done()
			
			// 获取信号量
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			keyResult := p.recoverSingleKey(k, plan)
			keyResults <- keyResult
		}(key, batch.Plans[i])
	}
	
	// 等待所有密钥处理完成
	go func() {
		wg.Wait()
		close(keyResults)
	}()
	
	// 收集结果
	for keyResult := range keyResults {
		result.KeyResults = append(result.KeyResults, keyResult)
		if keyResult.Success {
			result.SuccessfulKeys++
		} else {
			result.FailedKeys++
		}
	}
	
	// 计算统计信息
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)
	if result.TotalKeys > 0 {
		result.SuccessRate = float64(result.SuccessfulKeys) / float64(result.TotalKeys)
	}
	
	// 检查是否需要回滚
	if p.config.EnableRollback && result.SuccessRate < (1.0-p.config.MaxFailureRate) {
		logrus.WithFields(logrus.Fields{
			"batchID":     batch.ID,
			"successRate": result.SuccessRate,
			"threshold":   1.0-p.config.MaxFailureRate,
		}).Warn("Batch success rate below threshold, considering rollback")
		
		// 这里可以实现回滚逻辑
		result.Error = "Batch success rate below threshold"
	}
	
	logrus.WithFields(logrus.Fields{
		"batchID":        batch.ID,
		"totalKeys":      result.TotalKeys,
		"successfulKeys": result.SuccessfulKeys,
		"failedKeys":     result.FailedKeys,
		"successRate":    result.SuccessRate,
		"duration":       result.Duration,
	}).Info("Batch recovery completed")
	
	return result
}

// recoverSingleKey 恢复单个密钥
func (p *BatchRecoveryProcessor) recoverSingleKey(key *models.APIKey, plan *RecoveryPlan) *KeyRecoveryResult {
	startTime := time.Now()
	
	result := &KeyRecoveryResult{
		KeyID:    key.ID,
		FromPool: PoolTypeCooling,
		ToPool:   PoolTypeReady,
	}
	
	// 执行恢复操作
	err := p.layeredPool.MoveKey(key.ID, PoolTypeCooling, PoolTypeReady)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
	} else {
		// 更新数据库状态
		err = p.db.Model(&models.APIKey{}).
			Where("id = ?", key.ID).
			Updates(map[string]interface{}{
				"status":                models.KeyStatusActive,
				"rate_limit_reset_at":   nil,
			}).Error
		
		if err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("Failed to update database: %v", err)
		} else {
			result.Success = true
		}
	}
	
	result.Duration = time.Since(startTime)
	return result
}

// resultProcessor 结果处理协程
func (p *BatchRecoveryProcessor) resultProcessor() {
	defer p.wg.Done()
	
	for {
		select {
		case <-p.ctx.Done():
			return
		case result := <-p.results:
			if result == nil {
				return
			}
			
			p.updateMetrics(result)
		}
	}
}

// updateMetrics 更新指标
func (p *BatchRecoveryProcessor) updateMetrics(result *BatchRecoveryResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	p.metrics.TotalBatches++
	p.metrics.TotalKeysProcessed += int64(result.TotalKeys)
	p.metrics.TotalKeysRecovered += int64(result.SuccessfulKeys)
	p.metrics.LastProcessedAt = result.EndTime
	
	if result.SuccessRate >= 0.5 { // 成功率超过50%认为是成功的批次
		p.metrics.SuccessfulBatches++
	} else {
		p.metrics.FailedBatches++
	}
	
	// 更新平均值
	if p.metrics.TotalBatches > 0 {
		p.metrics.AvgBatchSize = float64(p.metrics.TotalKeysProcessed) / float64(p.metrics.TotalBatches)
		p.metrics.OverallSuccessRate = float64(p.metrics.TotalKeysRecovered) / float64(p.metrics.TotalKeysProcessed)
		
		// 更新平均处理时间
		totalTime := p.metrics.AvgProcessingTime * time.Duration(p.metrics.TotalBatches-1)
		p.metrics.AvgProcessingTime = (totalTime + result.Duration) / time.Duration(p.metrics.TotalBatches)
	}
}

// GetMetrics 获取批量恢复指标
func (p *BatchRecoveryProcessor) GetMetrics() *BatchRecoveryMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	// 返回指标的副本
	metrics := &BatchRecoveryMetrics{
		TotalBatches:        p.metrics.TotalBatches,
		SuccessfulBatches:   p.metrics.SuccessfulBatches,
		FailedBatches:       p.metrics.FailedBatches,
		TotalKeysProcessed:  p.metrics.TotalKeysProcessed,
		TotalKeysRecovered:  p.metrics.TotalKeysRecovered,
		AvgBatchSize:        p.metrics.AvgBatchSize,
		AvgProcessingTime:   p.metrics.AvgProcessingTime,
		OverallSuccessRate:  p.metrics.OverallSuccessRate,
		LastProcessedAt:     p.metrics.LastProcessedAt,
		PriorityMetrics:     make(map[RecoveryPriority]*PriorityBatchMetrics),
	}
	
	for priority, stats := range p.metrics.PriorityMetrics {
		metrics.PriorityMetrics[priority] = &PriorityBatchMetrics{
			Batches:   stats.Batches,
			Keys:      stats.Keys,
			Successes: stats.Successes,
			AvgTime:   stats.AvgTime,
		}
	}
	
	return metrics
}

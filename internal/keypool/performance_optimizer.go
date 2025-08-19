package keypool

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// PerformanceOptimizer 性能优化器
type PerformanceOptimizer struct {
	// 配置
	config *OptimizerConfig
	
	// 运行时状态
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	
	// 优化历史
	optimizationHistory []*OptimizationResult
	currentConfig       *OptimalConfig
	
	// 监控组件
	performanceMonitor *PerformanceMonitor
	tuner             *PerformanceTuner
	
	// 自适应调整
	adaptiveEnabled    bool
	lastOptimization   time.Time
	optimizationCount  int
}

// OptimizerConfig 优化器配置
type OptimizerConfig struct {
	// 基础配置
	EnableAutoOptimization bool          `json:"enable_auto_optimization"` // 启用自动优化
	OptimizationInterval   time.Duration `json:"optimization_interval"`    // 优化间隔
	MinOptimizationGap     time.Duration `json:"min_optimization_gap"`     // 最小优化间隔
	
	// 性能阈值
	ThroughputThreshold    float64 `json:"throughput_threshold"`     // 吞吐量阈值
	LatencyThreshold       int64   `json:"latency_threshold"`        // 延迟阈值(ms)
	ErrorRateThreshold     float64 `json:"error_rate_threshold"`     // 错误率阈值
	MemoryUsageThreshold   int64   `json:"memory_usage_threshold"`   // 内存使用阈值(MB)
	
	// 优化策略
	AggressiveOptimization bool    `json:"aggressive_optimization"`  // 激进优化模式
	ConservativeMode       bool    `json:"conservative_mode"`        // 保守模式
	AdaptiveTuning         bool    `json:"adaptive_tuning"`          // 自适应调优
	
	// 资源限制
	MaxConcurrentTests     int     `json:"max_concurrent_tests"`     // 最大并发测试数
	MaxMemoryUsage         int64   `json:"max_memory_usage"`         // 最大内存使用(MB)
	MaxCPUUsage            float64 `json:"max_cpu_usage"`            // 最大CPU使用率
}

// OptimizationResult 优化结果
type OptimizationResult struct {
	Timestamp       time.Time         `json:"timestamp"`
	PreviousConfig  *OptimalConfig    `json:"previous_config"`
	NewConfig       *OptimalConfig    `json:"new_config"`
	Improvement     *ImprovementStats `json:"improvement"`
	Duration        time.Duration     `json:"duration"`
	Success         bool              `json:"success"`
	ErrorMessage    string            `json:"error_message,omitempty"`
}

// ImprovementStats 改进统计
type ImprovementStats struct {
	ThroughputImprovement float64 `json:"throughput_improvement"`  // 吞吐量改进百分比
	LatencyImprovement    float64 `json:"latency_improvement"`     // 延迟改进百分比
	MemoryImprovement     float64 `json:"memory_improvement"`      // 内存改进百分比
	OverallScore          float64 `json:"overall_score"`           // 总体得分改进
}

// OptimalConfig 最优配置
type OptimalConfig struct {
	// 分片配置
	ShardCount       int     `json:"shard_count"`
	CacheSize        int     `json:"cache_size"`
	BatchSize        int     `json:"batch_size"`
	
	// 并发配置
	MaxConcurrency   int     `json:"max_concurrency"`
	WorkerPoolSize   int     `json:"worker_pool_size"`
	QueueSize        int     `json:"queue_size"`
	
	// 超时配置
	SelectTimeout    time.Duration `json:"select_timeout"`
	ReturnTimeout    time.Duration `json:"return_timeout"`
	RecoveryTimeout  time.Duration `json:"recovery_timeout"`
	
	// 性能指标
	Score            float64 `json:"score"`
	Throughput       float64 `json:"throughput"`
	AvgLatency       int64   `json:"avg_latency"`
	MemoryEfficiency float64 `json:"memory_efficiency"`
	
	// 元数据
	GeneratedAt      time.Time `json:"generated_at"`
	Environment      string    `json:"environment"`
	CPUCores         int       `json:"cpu_cores"`
	AvailableMemory  int64     `json:"available_memory"`
}

// DefaultOptimizerConfig 返回默认优化器配置
func DefaultOptimizerConfig() *OptimizerConfig {
	return &OptimizerConfig{
		EnableAutoOptimization: true,
		OptimizationInterval:   1 * time.Hour,
		MinOptimizationGap:     10 * time.Minute,
		ThroughputThreshold:    100.0,  // 100 req/s
		LatencyThreshold:       1000,   // 1000ms
		ErrorRateThreshold:     0.05,   // 5%
		MemoryUsageThreshold:   512,    // 512MB
		AggressiveOptimization: false,
		ConservativeMode:       true,
		AdaptiveTuning:         true,
		MaxConcurrentTests:     3,
		MaxMemoryUsage:         1024,   // 1GB
		MaxCPUUsage:            0.8,    // 80%
	}
}

// NewPerformanceOptimizer 创建性能优化器
func NewPerformanceOptimizer(config *OptimizerConfig, monitor *PerformanceMonitor) *PerformanceOptimizer {
	if config == nil {
		config = DefaultOptimizerConfig()
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	optimizer := &PerformanceOptimizer{
		config:              config,
		ctx:                 ctx,
		cancel:              cancel,
		optimizationHistory: make([]*OptimizationResult, 0),
		performanceMonitor:  monitor,
		adaptiveEnabled:     config.AdaptiveTuning,
		lastOptimization:    time.Now(),
	}
	
	// 创建性能调优器
	tunerConfig := &TuningConfig{
		TestDuration:    30 * time.Second,
		WarmupDuration:  5 * time.Second,
		ShardCounts:     []int{4, 8, 16, 32},
		CacheSizes:      []int{1000, 5000, 10000, 20000},
		BatchSizes:      []int{10, 50, 100, 200},
	}
	optimizer.tuner = NewPerformanceTuner(tunerConfig)
	
	// 生成初始最优配置
	optimizer.currentConfig = optimizer.generateInitialConfig()
	
	return optimizer
}

// Start 启动性能优化器
func (o *PerformanceOptimizer) Start() error {
	if o.config.EnableAutoOptimization {
		go o.optimizationLoop()
		logrus.Info("Performance optimizer started with auto-optimization enabled")
	} else {
		logrus.Info("Performance optimizer started in manual mode")
	}
	return nil
}

// Stop 停止性能优化器
func (o *PerformanceOptimizer) Stop() error {
	o.cancel()
	logrus.Info("Performance optimizer stopped")
	return nil
}

// optimizationLoop 优化循环
func (o *PerformanceOptimizer) optimizationLoop() {
	ticker := time.NewTicker(o.config.OptimizationInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-o.ctx.Done():
			return
		case <-ticker.C:
			if o.shouldOptimize() {
				if err := o.RunOptimization(); err != nil {
					logrus.WithError(err).Error("Auto-optimization failed")
				}
			}
		}
	}
}

// shouldOptimize 判断是否应该进行优化
func (o *PerformanceOptimizer) shouldOptimize() bool {
	// 检查最小间隔
	if time.Since(o.lastOptimization) < o.config.MinOptimizationGap {
		return false
	}
	
	// 检查性能指标
	if o.performanceMonitor != nil {
		metrics := o.performanceMonitor.GetMetrics()
		if metrics != nil {
			// 吞吐量过低
			if metrics.Throughput < o.config.ThroughputThreshold {
				return true
			}
			
			// 延迟过高
			if metrics.AvgLatency > o.config.LatencyThreshold {
				return true
			}
			
			// 错误率过高
			if metrics.ErrorRate > o.config.ErrorRateThreshold {
				return true
			}
		}
	}
	
	// 检查系统资源
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	memoryUsageMB := int64(memStats.HeapInuse / 1024 / 1024)
	
	if memoryUsageMB > o.config.MemoryUsageThreshold {
		return true
	}
	
	return false
}

// RunOptimization 运行优化
func (o *PerformanceOptimizer) RunOptimization() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	
	startTime := time.Now()
	logrus.Info("Starting performance optimization")
	
	// 记录当前配置
	previousConfig := o.currentConfig
	
	// 运行性能调优
	newConfig, err := o.tuner.RunFullTuning()
	if err != nil {
		result := &OptimizationResult{
			Timestamp:      startTime,
			PreviousConfig: previousConfig,
			Duration:       time.Since(startTime),
			Success:        false,
			ErrorMessage:   err.Error(),
		}
		o.optimizationHistory = append(o.optimizationHistory, result)
		return fmt.Errorf("performance tuning failed: %w", err)
	}
	
	// 计算改进统计
	improvement := o.calculateImprovement(previousConfig, newConfig)
	
	// 决定是否应用新配置
	shouldApply := o.shouldApplyNewConfig(previousConfig, newConfig, improvement)
	
	result := &OptimizationResult{
		Timestamp:      startTime,
		PreviousConfig: previousConfig,
		NewConfig:      newConfig,
		Improvement:    improvement,
		Duration:       time.Since(startTime),
		Success:        shouldApply,
	}
	
	if shouldApply {
		o.currentConfig = newConfig
		o.lastOptimization = time.Now()
		o.optimizationCount++
		
		logrus.WithFields(logrus.Fields{
			"throughput_improvement": improvement.ThroughputImprovement,
			"latency_improvement":    improvement.LatencyImprovement,
			"memory_improvement":     improvement.MemoryImprovement,
			"overall_score":          improvement.OverallScore,
		}).Info("Performance optimization completed successfully")
	} else {
		result.ErrorMessage = "New configuration did not provide sufficient improvement"
		logrus.Info("Performance optimization completed but new configuration was not applied")
	}
	
	o.optimizationHistory = append(o.optimizationHistory, result)
	
	// 限制历史记录数量
	if len(o.optimizationHistory) > 100 {
		o.optimizationHistory = o.optimizationHistory[len(o.optimizationHistory)-100:]
	}
	
	return nil
}

// calculateImprovement 计算改进统计
func (o *PerformanceOptimizer) calculateImprovement(previous, new *OptimalConfig) *ImprovementStats {
	if previous == nil {
		return &ImprovementStats{
			ThroughputImprovement: 0,
			LatencyImprovement:    0,
			MemoryImprovement:     0,
			OverallScore:          new.Score,
		}
	}
	
	throughputImprovement := 0.0
	if previous.Throughput > 0 {
		throughputImprovement = ((new.Throughput - previous.Throughput) / previous.Throughput) * 100
	}
	
	latencyImprovement := 0.0
	if previous.AvgLatency > 0 {
		latencyImprovement = ((float64(previous.AvgLatency) - float64(new.AvgLatency)) / float64(previous.AvgLatency)) * 100
	}
	
	memoryImprovement := 0.0
	if previous.MemoryEfficiency > 0 {
		memoryImprovement = ((new.MemoryEfficiency - previous.MemoryEfficiency) / previous.MemoryEfficiency) * 100
	}
	
	overallScore := new.Score - previous.Score
	
	return &ImprovementStats{
		ThroughputImprovement: throughputImprovement,
		LatencyImprovement:    latencyImprovement,
		MemoryImprovement:     memoryImprovement,
		OverallScore:          overallScore,
	}
}

// shouldApplyNewConfig 判断是否应该应用新配置
func (o *PerformanceOptimizer) shouldApplyNewConfig(previous, new *OptimalConfig, improvement *ImprovementStats) bool {
	if previous == nil {
		return true
	}
	
	// 保守模式下需要显著改进
	if o.config.ConservativeMode {
		minImprovement := 5.0 // 5%
		if o.config.AggressiveOptimization {
			minImprovement = 1.0 // 1%
		}
		
		// 至少一个关键指标有显著改进
		if improvement.ThroughputImprovement > minImprovement ||
		   improvement.LatencyImprovement > minImprovement ||
		   improvement.OverallScore > minImprovement {
			return true
		}
		
		return false
	}
	
	// 非保守模式下，任何改进都接受
	return improvement.OverallScore > 0
}

// generateInitialConfig 生成初始配置
func (o *PerformanceOptimizer) generateInitialConfig() *OptimalConfig {
	cpuCores := runtime.NumCPU()
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	return &OptimalConfig{
		ShardCount:       cpuCores * 2,
		CacheSize:        10000,
		BatchSize:        100,
		MaxConcurrency:   cpuCores * 4,
		WorkerPoolSize:   cpuCores,
		QueueSize:        1000,
		SelectTimeout:    5 * time.Second,
		ReturnTimeout:    3 * time.Second,
		RecoveryTimeout:  10 * time.Second,
		Score:            0,
		Throughput:       0,
		AvgLatency:       0,
		MemoryEfficiency: 0,
		GeneratedAt:      time.Now(),
		Environment:      "initial",
		CPUCores:         cpuCores,
		AvailableMemory:  int64(memStats.Sys / 1024 / 1024),
	}
}

// GetCurrentConfig 获取当前最优配置
func (o *PerformanceOptimizer) GetCurrentConfig() *OptimalConfig {
	o.mu.RLock()
	defer o.mu.RUnlock()
	
	if o.currentConfig == nil {
		return o.generateInitialConfig()
	}
	
	// 返回配置的副本
	config := *o.currentConfig
	return &config
}

// GetOptimizationHistory 获取优化历史
func (o *PerformanceOptimizer) GetOptimizationHistory() []*OptimizationResult {
	o.mu.RLock()
	defer o.mu.RUnlock()
	
	// 返回历史的副本
	history := make([]*OptimizationResult, len(o.optimizationHistory))
	copy(history, o.optimizationHistory)
	return history
}

// UpdateConfig 更新优化器配置
func (o *PerformanceOptimizer) UpdateConfig(config *OptimizerConfig) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	
	o.config = config
	o.adaptiveEnabled = config.AdaptiveTuning
	
	logrus.Info("Performance optimizer configuration updated")
	return nil
}

// GetStats 获取优化器统计信息
func (o *PerformanceOptimizer) GetStats() map[string]interface{} {
	o.mu.RLock()
	defer o.mu.RUnlock()
	
	stats := map[string]interface{}{
		"optimization_count":    o.optimizationCount,
		"last_optimization":     o.lastOptimization,
		"current_config":        o.currentConfig,
		"adaptive_enabled":      o.adaptiveEnabled,
		"auto_optimization":     o.config.EnableAutoOptimization,
		"history_count":         len(o.optimizationHistory),
	}
	
	// 添加最近的改进统计
	if len(o.optimizationHistory) > 0 {
		latest := o.optimizationHistory[len(o.optimizationHistory)-1]
		stats["latest_optimization"] = latest
	}
	
	return stats
}

// ForceOptimization 强制执行优化
func (o *PerformanceOptimizer) ForceOptimization() error {
	return o.RunOptimization()
}

// ResetToDefaults 重置为默认配置
func (o *PerformanceOptimizer) ResetToDefaults() {
	o.mu.Lock()
	defer o.mu.Unlock()
	
	o.currentConfig = o.generateInitialConfig()
	o.optimizationHistory = make([]*OptimizationResult, 0)
	o.optimizationCount = 0
	o.lastOptimization = time.Now()
	
	logrus.Info("Performance optimizer reset to defaults")
}
